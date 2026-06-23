package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// chunkProgressFunc is invoked from inside a chunk runner whenever new
// bytes have been written to disk. delta is the number of bytes added
// since the last call (NOT a cumulative total) so the caller can fold
// it into an atomic counter without double-counting on retries.
type chunkProgressFunc func(delta int64)

// runChunk drives a single chunk to completion, retrying on transient
// errors with exponential backoff. The function is safe to call with a
// partially-downloaded chunk (c.Downloaded > 0) — it picks up from the
// recorded offset.
//
// Concurrency contract: runChunk holds exclusive write access to its
// own byte range of file (via WriteAt at distinct offsets); the caller
// guarantees no two goroutines own overlapping chunks. We mutate c
// directly because chunk slices in downloadState are pre-allocated and
// indexed; the surrounding code serializes sidecar flushes with a
// mutex.
func runChunk(
	ctx context.Context,
	url string,
	file *os.File,
	c *chunkState,
	probe *probeResult,
	opts Options,
	onProgress chunkProgressFunc,
	totalRetries *atomic.Int64,
) error {
	expected := c.End - c.Start + 1
	if expected <= 0 {
		return fmt.Errorf("chunk: invalid range [%d,%d]", c.Start, c.End)
	}
	if c.Downloaded >= expected {
		return nil
	}

	var lastErr error
	backoff := opts.RetryBackoff

	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if attempt > 0 {
			totalRetries.Add(1)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > opts.RetryBackoffMax {
				backoff = opts.RetryBackoffMax
			}
		}

		err := doChunkOnce(ctx, url, file, c, probe, opts, onProgress)
		if err == nil {
			return nil
		}
		// ctx cancellation is terminal — don't retry, just propagate.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		// 4xx that isn't 416/408/429 isn't going to get better.
		var hse *httpStatusError
		if errors.As(err, &hse) {
			switch hse.StatusCode {
			case http.StatusRequestedRangeNotSatisfiable,
				http.StatusRequestTimeout,
				http.StatusTooManyRequests,
				http.StatusInternalServerError,
				http.StatusBadGateway,
				http.StatusServiceUnavailable,
				http.StatusGatewayTimeout:
				// retriable
			default:
				if hse.StatusCode >= 400 && hse.StatusCode < 500 {
					return err
				}
			}
		}
		lastErr = err
	}
	return fmt.Errorf("chunk [%d,%d] exhausted retries: %w", c.Start, c.End, lastErr)
}

// doChunkOnce issues one HTTP GET for the un-downloaded suffix of c
// and streams the body into file at the correct absolute offset. On
// any I/O or HTTP error it returns; the caller decides whether to
// retry.
func doChunkOnce(
	ctx context.Context,
	url string,
	file *os.File,
	c *chunkState,
	probe *probeResult,
	opts Options,
	onProgress chunkProgressFunc,
) error {
	expected := c.End - c.Start + 1
	rangeStart := c.Start + c.Downloaded
	if rangeStart > c.End {
		return nil // nothing to fetch
	}

	rctx := ctx
	if opts.RequestTimeout > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, opts.RequestTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	applyHeaders(req, opts)
	// Always send Range; if the server doesn't support it we handle
	// the 200 response below.
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rangeStart, c.End))
	// If-Range guards against the file changing under us mid-resume.
	if probe.ETag != "" {
		req.Header.Set("If-Range", `"`+probe.ETag+`"`)
	} else if !probe.LastModified.IsZero() {
		req.Header.Set("If-Range", probe.LastModified.Format(http.TimeFormat))
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
		// Expected case. The body is exactly the requested range.
	case http.StatusOK:
		// Server ignored Range or refused to honor If-Range. The body
		// is the FULL file starting at offset 0, so we must treat this
		// chunk as "single-stream from scratch":
		//   - rewind c.Downloaded to 0;
		//   - require this chunk to cover the whole file (it will,
		//     because the caller collapses to one chunk when probe
		//     reports AcceptRanges=false, and re-collapses on 200 by
		//     returning an error that escalates to a fresh planChunks
		//     run).
		// To keep the code simple and predictable, treat 200 as a
		// hard error UNLESS this chunk already spans the entire file.
		if c.Start != 0 || c.End != probe.Size-1 {
			return fmt.Errorf("chunk: got 200 OK to a partial range [%d,%d]; server refusing ranges", c.Start, c.End)
		}
		c.Downloaded = 0
		rangeStart = 0
	default:
		return &httpStatusError{StatusCode: resp.StatusCode, URL: url}
	}

	// Stream body → file.WriteAt at rangeStart, updating c.Downloaded
	// after each successful block so progress + sidecar reflect reality
	// in near-real-time.
	buf := make([]byte, 1<<20) // 1 MiB
	offset := rangeStart
	written := int64(0)
	for {
		select {
		case <-rctx.Done():
			return rctx.Err()
		default:
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			// Clamp the write so we never exceed the chunk boundary;
			// a misbehaving origin could otherwise overflow into the
			// next chunk's territory.
			remaining := c.End - offset + 1
			if int64(n) > remaining {
				n = int(remaining)
			}
			if _, werr := file.WriteAt(buf[:n], offset); werr != nil {
				return fmt.Errorf("chunk: write at %d: %w", offset, werr)
			}
			offset += int64(n)
			written += int64(n)
			c.Downloaded += int64(n)
			if onProgress != nil {
				onProgress(int64(n))
			}
			if c.Downloaded >= expected {
				// Done; drain the rest of the body so the connection
				// can be reused, but only briefly.
				_, _ = io.CopyN(io.Discard, resp.Body, 64*1024)
				return nil
			}
		}
		if rerr == io.EOF {
			if c.Downloaded < expected {
				return fmt.Errorf("chunk: short read: got %d/%d bytes", c.Downloaded, expected)
			}
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("chunk: read: %w (after %d bytes)", rerr, written)
		}
	}
}

// copyWithProgress is used by the unknown-size fallback path. It
// streams src→dst sequentially, reporting cumulative bytes to cb.
// startOffset lets the caller report a non-zero baseline (currently
// unused because the fallback always starts from 0, but kept for
// symmetry with the multipart path).
func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, cb ProgressCallback, startOffset int64) (int64, error) {
	buf := make([]byte, 256*1024)
	total := startOffset
	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			if cb != nil {
				cb(Progress{Total: 0, Downloaded: total})
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}
