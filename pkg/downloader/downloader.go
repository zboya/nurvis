// Package downloader is a self-contained, dependency-free file downloader
// with multi-part concurrent transfer and durable resume support.
//
// Design goals (in priority order):
//
//  1. Correctness over speed. Every chunk's expected byte count is tracked
//     explicitly; on-disk size is never used as the sole authority for
//     "how much have I downloaded". A sidecar JSON file (dest + ".part.json")
//     records the per-chunk progress so resume across processes works even
//     when the OS lies about file size or the server momentarily returns a
//     non-Range response.
//
//  2. Crash-safety. Bytes land in <dest>.part (never the final dest path)
//     and are only renamed onto <dest> after a full size check passes.
//     Callers that see <dest> exist can therefore trust it.
//
//  3. No third-party deps. Only the Go standard library.
//
// The package exposes a single entry point, Download, plus an Options
// struct for tunables. The Progress callback receives the aggregated
// "bytes written so far" across all chunks and is throttled by the caller
// (we fire it on every chunk update; rate-limit upstream if needed).
package downloader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Progress is a snapshot of an in-flight download.
type Progress struct {
	Total      int64   // authoritative total size (0 if unknown)
	Downloaded int64   // bytes written so far, aggregated across chunks
	Percent    float64 // 0..100, only valid when Total > 0
}

// ProgressCallback is invoked whenever a chunk reports new bytes. The
// callback runs on the downloader's internal goroutines; keep it cheap
// and non-blocking. Callers wanting rate limiting should debounce here.
type ProgressCallback func(Progress)

// Options tunes a Download call. Zero values are sensible defaults.
type Options struct {
	// Concurrency is the number of parallel chunks to download when the
	// server supports byte ranges. Capped to 16 internally to avoid
	// hammering CDNs. Defaults to 4.
	Concurrency int

	// ChunkSize is the target size per chunk in bytes. Defaults to 32 MiB.
	// The final chunk may be smaller.
	ChunkSize int64

	// MinMultipartSize is the file-size threshold below which Download
	// stays single-stream regardless of server capabilities. Defaults to
	// 10 MiB. Multipart on tiny files just adds overhead.
	MinMultipartSize int64

	// MaxRetries is the per-chunk retry budget. Each retry waits with
	// exponential backoff starting at RetryBackoff. Defaults to 5.
	MaxRetries int

	// RetryBackoff is the initial backoff between retries. Doubles each
	// attempt up to RetryBackoffMax. Defaults to 1s.
	RetryBackoff time.Duration

	// RetryBackoffMax caps the exponential backoff. Defaults to 30s.
	RetryBackoffMax time.Duration

	// RequestTimeout caps a single HTTP request's wall time (TCP + TLS +
	// headers + body). 0 means no per-request timeout — only the parent
	// context applies. Defaults to 0 because large chunks legitimately
	// take a long time.
	RequestTimeout time.Duration

	// Headers are applied to every HTTP request (probe + chunks).
	Headers map[string]string

	// UserAgent overrides the User-Agent header. Defaults to
	// "nurvis-downloader/1.0".
	UserAgent string

	// HTTPClient lets callers inject a pre-configured client (custom
	// transport, proxies, etc.). Defaults to http.DefaultClient.
	HTTPClient *http.Client

	// Progress receives aggregated progress ticks. nil disables.
	Progress ProgressCallback

	// SidecarFlushInterval is the minimum interval between sidecar
	// JSON flushes. Defaults to 1s. We always flush on chunk completion
	// regardless of this interval.
	SidecarFlushInterval time.Duration
}

// Stats describes the outcome of a successful Download.
type Stats struct {
	URL        string
	Dest       string
	Total      int64
	Resumed    bool          // true if any byte was carried over from a previous attempt
	Multipart  bool          // true if multipart concurrent transfer was used
	Duration   time.Duration // wall time of this Download call
	Retries    int           // total retries across all chunks
}

// Download fetches url to dest. The function is synchronous and blocks
// until the file is fully written (or an error occurs). On success the
// destination is renamed onto dest atomically; on failure the partial
// file <dest>.part and its sidecar <dest>.part.json are left on disk so
// a subsequent call can resume.
//
// Resume semantics: if <dest>.part.json exists and its recorded
// {url, size, etag, last_modified} match the current server response,
// only the un-completed bytes of each chunk are re-fetched. Otherwise
// the part file and sidecar are discarded and the download restarts
// from zero.
func Download(ctx context.Context, url, dest string, opts Options) (*Stats, error) {
	opts = applyDefaults(opts)
	startedAt := time.Now()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, fmt.Errorf("downloader: mkdir parent: %w", err)
	}

	// Fast path: dest already exists as a regular file. A previous run
	// either finished cleanly (we always rename .part → dest atomically
	// and then drop the sidecar) or the caller staged the file there
	// themselves. Either way, re-probing the origin and re-downloading
	// would be wasteful and risks clobbering a known-good file when
	// the network is flaky. Callers that explicitly want to redownload
	// can delete dest first.
	if fi, err := os.Stat(dest); err == nil && fi.Mode().IsRegular() {
		// Surface a 100% progress tick so subscribers don't sit at 0%.
		emitProgress(opts.Progress, fi.Size(), fi.Size())
		// Clean up any stale sidecar/part remnants from prior aborted
		// attempts so the next non-fast-path run starts clean.
		_ = os.Remove(dest + ".part")
		_ = os.Remove(dest + ".part.json")
		return &Stats{
			URL:      url,
			Dest:     dest,
			Total:    fi.Size(),
			Duration: time.Since(startedAt),
		}, nil
	}

	// 1) Probe the origin so we know size + range support + validators.
	probe, err := probe(ctx, url, opts)
	if err != nil {
		return nil, fmt.Errorf("downloader: probe: %w", err)
	}
	if probe.Size <= 0 {
		// Without a known size we can't safely do multipart or resume.
		// Fall back to a plain single-stream download that writes
		// directly to <dest>.part and verifies nothing — callers should
		// avoid this path for important files.
		return downloadUnknownSize(ctx, url, dest, opts, startedAt)
	}

	partPath := dest + ".part"
	sidecarPath := dest + ".part.json"

	// 2) Try to load an existing sidecar and decide whether it matches
	// the current origin response. If not, wipe both files.
	state, resumed, err := loadOrInitState(sidecarPath, partPath, url, probe, opts)
	if err != nil {
		return nil, fmt.Errorf("downloader: state init: %w", err)
	}

	// 3) Open the part file at exactly the expected size (sparse if new).
	// Using WriteAt against a pre-sized file lets multiple chunks land
	// at their respective offsets without contention.
	file, err := openPartFile(partPath, probe.Size)
	if err != nil {
		return nil, fmt.Errorf("downloader: open part: %w", err)
	}
	defer file.Close()

	// 4) Determine strategy: multipart vs single-stream.
	multipart := probe.AcceptRanges && probe.Size >= opts.MinMultipartSize && opts.Concurrency > 1

	if !multipart {
		// Collapse to a single chunk covering the whole file.
		if len(state.Chunks) != 1 || state.Chunks[0].End != probe.Size-1 {
			state.Chunks = []chunkState{{Start: 0, End: probe.Size - 1}}
			// Preserve already-downloaded bytes when reasonable: if the
			// existing part file is exactly the right size for a
			// single-stream resume, trust on-disk length.
			if fi, ferr := os.Stat(partPath); ferr == nil && fi.Size() > 0 && fi.Size() <= probe.Size {
				state.Chunks[0].Downloaded = fi.Size()
			}
			_ = writeSidecar(sidecarPath, state)
		}
	}

	// 5) Spin up workers. Each chunk is owned by exactly one goroutine.
	totalDownloaded := atomic.Int64{}
	for _, c := range state.Chunks {
		totalDownloaded.Add(c.Downloaded)
	}

	retries := atomic.Int64{}
	mu := sync.Mutex{} // guards state.Chunks slice + sidecar flush
	lastFlush := time.Now()

	// Emit initial progress tick so callers leave the "resolving" state.
	emitProgress(opts.Progress, probe.Size, totalDownloaded.Load())

	wg := sync.WaitGroup{}
	errCh := make(chan error, opts.Concurrency)

	// Work-stealing scheduler: a fixed pool of opts.Concurrency
	// workers each pull the next pending chunk index from `jobs`.
	// This avoids the failure mode of static "one goroutine per
	// chunk" where a single slow CDN edge blocks an entire worker
	// slot while other workers sit idle.
	jobs := make(chan int, len(state.Chunks))
	for i := range state.Chunks {
		jobs <- i
	}
	close(jobs)

	// Stop signal so workers can exit early once any chunk errors out.
	stopCtx, stopCancel := context.WithCancel(ctx)
	defer stopCancel()

	workers := opts.Concurrency
	if workers > len(state.Chunks) {
		workers = len(state.Chunks)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCtx.Done():
					return
				case i, ok := <-jobs:
					if !ok {
						return
					}
					err := runChunk(stopCtx, url, file, &state.Chunks[i], probe, opts, func(delta int64) {
						newTotal := totalDownloaded.Add(delta)
						emitProgress(opts.Progress, probe.Size, newTotal)

						mu.Lock()
						now := time.Now()
						if now.Sub(lastFlush) >= opts.SidecarFlushInterval {
							_ = writeSidecar(sidecarPath, state)
							lastFlush = now
						}
						mu.Unlock()
					}, &retries)
					if err != nil {
						errCh <- err
						stopCancel()
						return
					}
					// Always flush on chunk completion so a crash
					// right after this returns still leaves an
					// accurate sidecar on disk.
					mu.Lock()
					_ = writeSidecar(sidecarPath, state)
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	if err := firstError(errCh); err != nil {
		// Persist final state before bailing so the next run can resume
		// from the latest known offsets.
		_ = writeSidecar(sidecarPath, state)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}

	// 6) Sanity-check the result, fsync, then atomically rename.
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("downloader: fsync part: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("downloader: close part: %w", err)
	}
	if fi, err := os.Stat(partPath); err != nil {
		return nil, fmt.Errorf("downloader: stat part: %w", err)
	} else if fi.Size() != probe.Size {
		// This should be impossible given chunk accounting, but it's
		// cheap insurance and avoids ever producing a wrong-size dest.
		return nil, fmt.Errorf("downloader: part size mismatch: got %d want %d", fi.Size(), probe.Size)
	}

	if err := os.Rename(partPath, dest); err != nil {
		return nil, fmt.Errorf("downloader: rename part->dest: %w", err)
	}
	_ = os.Remove(sidecarPath)

	return &Stats{
		URL:       url,
		Dest:      dest,
		Total:     probe.Size,
		Resumed:   resumed,
		Multipart: multipart,
		Duration:  time.Since(startedAt),
		Retries:   int(retries.Load()),
	}, nil
}

func applyDefaults(o Options) Options {
	if o.Concurrency <= 0 {
		// 8 parallel TCP streams is a sweet spot on home/mobile links
		// against HTTP/1.1-forced CDNs (HuggingFace via Cloudfront in
		// particular). Above ~16 the marginal gain disappears and
		// servers start rate-limiting per-IP.
		o.Concurrency = 8
	}
	if o.Concurrency > 16 {
		o.Concurrency = 16
	}
	if o.ChunkSize <= 0 {
		// 8 MiB chunks give the work-stealing scheduler enough
		// granularity to absorb slow CDN edges without paying too
		// much per-request HTTP overhead.
		o.ChunkSize = 8 * 1024 * 1024
	}
	if o.MinMultipartSize <= 0 {
		o.MinMultipartSize = 10 * 1024 * 1024
	}
	if o.MaxRetries < 0 {
		o.MaxRetries = 5
	}
	if o.MaxRetries == 0 {
		o.MaxRetries = 5
	}
	if o.RetryBackoff <= 0 {
		o.RetryBackoff = 1 * time.Second
	}
	if o.RetryBackoffMax <= 0 {
		o.RetryBackoffMax = 30 * time.Second
	}
	if o.UserAgent == "" {
		o.UserAgent = "nurvis-downloader/1.0"
	}
	if o.HTTPClient == nil {
		// Use the package-tuned transport: HTTP/1.1 forced, gzip off,
		// generous idle pool. See transport.go for rationale.
		o.HTTPClient = sharedHTTPClient()
	}
	if o.SidecarFlushInterval <= 0 {
		o.SidecarFlushInterval = 1 * time.Second
	}
	return o
}

// openPartFile opens (or creates) the part file pre-sized to size bytes.
// Pre-sizing turns later WriteAt calls into in-place writes regardless
// of arrival order.
func openPartFile(path string, size int64) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if fi.Size() != size {
		// Truncate up (sparse) or down (drop garbage tail) to match.
		if err := f.Truncate(size); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}

// planChunks splits [0,size) into a list of inclusive-end chunks.
func planChunks(size, chunkSize int64) []chunkState {
	if size <= 0 {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = 32 * 1024 * 1024
	}
	n := (size + chunkSize - 1) / chunkSize
	out := make([]chunkState, 0, n)
	for i := int64(0); i < n; i++ {
		start := i * chunkSize
		end := start + chunkSize - 1
		if end >= size {
			end = size - 1
		}
		out = append(out, chunkState{Start: start, End: end})
	}
	return out
}

func emitProgress(cb ProgressCallback, total, downloaded int64) {
	if cb == nil {
		return
	}
	var pct float64
	if total > 0 {
		pct = float64(downloaded) / float64(total) * 100
		if pct > 100 {
			pct = 100
		}
	}
	cb(Progress{Total: total, Downloaded: downloaded, Percent: pct})
}

func firstError(ch <-chan error) error {
	var first error
	for e := range ch {
		if first == nil {
			first = e
		}
	}
	return first
}

// loadOrInitState reconciles an on-disk sidecar with the current probe.
// Returns the working state and whether we successfully resumed.
func loadOrInitState(sidecarPath, partPath, url string, p *probeResult, opts Options) (*downloadState, bool, error) {
	existing, err := readSidecar(sidecarPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// No prior attempt. Start fresh.
		fresh := &downloadState{
			URL:          url,
			Size:         p.Size,
			ETag:         p.ETag,
			LastModified: p.LastModified,
			AcceptRanges: p.AcceptRanges,
			Chunks:       planChunks(p.Size, opts.ChunkSize),
			CreatedAt:    time.Now(),
		}
		// Wipe any stale part file from a non-resume previous run.
		_ = os.Remove(partPath)
		return fresh, false, nil
	case err != nil:
		// Corrupt sidecar — treat as no resume, wipe both.
		slog.Warn("downloader: sidecar unreadable, restarting", "path", sidecarPath, "err", err)
		_ = os.Remove(sidecarPath)
		_ = os.Remove(partPath)
		return &downloadState{
			URL: url, Size: p.Size, ETag: p.ETag, LastModified: p.LastModified,
			AcceptRanges: p.AcceptRanges,
			Chunks:       planChunks(p.Size, opts.ChunkSize),
			CreatedAt:    time.Now(),
		}, false, nil
	}

	// Validate the sidecar against the current probe.
	if !sidecarMatches(existing, url, p) {
		slog.Info("downloader: sidecar mismatch, restarting",
			"sidecar_url", existing.URL, "url", url,
			"sidecar_size", existing.Size, "size", p.Size,
			"sidecar_etag", existing.ETag, "etag", p.ETag)
		_ = os.Remove(sidecarPath)
		_ = os.Remove(partPath)
		return &downloadState{
			URL: url, Size: p.Size, ETag: p.ETag, LastModified: p.LastModified,
			AcceptRanges: p.AcceptRanges,
			Chunks:       planChunks(p.Size, opts.ChunkSize),
			CreatedAt:    time.Now(),
		}, false, nil
	}

	// Sidecar matches. Make sure every chunk's Downloaded is sane.
	for i := range existing.Chunks {
		c := &existing.Chunks[i]
		expected := c.End - c.Start + 1
		if c.Downloaded < 0 || c.Downloaded > expected {
			// Clamp; the chunk runner will refetch the missing tail.
			c.Downloaded = 0
		}
	}
	resumed := false
	for _, c := range existing.Chunks {
		if c.Downloaded > 0 {
			resumed = true
			break
		}
	}
	return existing, resumed, nil
}

func sidecarMatches(s *downloadState, url string, p *probeResult) bool {
	if s == nil {
		return false
	}
	if s.URL != url {
		return false
	}
	if s.Size != p.Size {
		return false
	}
	// Validators are best-effort: if both sides have one and they
	// disagree, restart; if either side is empty, fall through (origin
	// just doesn't expose it).
	if s.ETag != "" && p.ETag != "" && s.ETag != p.ETag {
		return false
	}
	if !s.LastModified.IsZero() && !p.LastModified.IsZero() && !s.LastModified.Equal(p.LastModified) {
		return false
	}
	return true
}

// downloadUnknownSize handles the degenerate case where probe couldn't
// determine a content length. We stream the whole body into <dest>.part,
// rename on success, and skip all resume bookkeeping. No retry, no
// multipart, no sidecar — callers should treat this as best-effort.
func downloadUnknownSize(ctx context.Context, url, dest string, opts Options, startedAt time.Time) (*Stats, error) {
	partPath := dest + ".part"
	f, err := os.OpenFile(partPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("downloader: open part (unknown size): %w", err)
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, opts)
	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("downloader: unexpected status %d", resp.StatusCode)
	}

	written, err := copyWithProgress(ctx, f, resp.Body, opts.Progress, 0)
	if err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(partPath, dest); err != nil {
		return nil, err
	}
	return &Stats{
		URL: url, Dest: dest, Total: written,
		Resumed: false, Multipart: false,
		Duration: time.Since(startedAt),
	}, nil
}
