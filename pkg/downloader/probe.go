package downloader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// probeResult captures everything we need to know about the origin
// before kicking off the actual download: how big the file is, whether
// the server will honor Range requests, and which validators (ETag,
// Last-Modified) it exposes for resume invalidation.
type probeResult struct {
	Size         int64
	AcceptRanges bool
	ETag         string
	LastModified time.Time
}

// probe issues a Range GET ("bytes=0-0") instead of HEAD because many
// CDNs (notably Cloudfront fronting HuggingFace) reject HEAD with 400
// while happily honoring a Range GET. The body of a 206 response is at
// most one byte, which we discard. We accept three response shapes:
//
//   - 206 Partial Content with Content-Range: bytes 0-0/<total>
//     The total is authoritative; AcceptRanges is true by definition.
//   - 200 OK with Content-Length: <total>
//     The server ignored Range entirely. We mark AcceptRanges=false so
//     the main loop stays single-stream.
//   - Anything 4xx/5xx: error.
func probe(ctx context.Context, url string, opts Options) (*probeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, opts)
	req.Header.Set("Range", "bytes=0-0")

	// Apply a generous timeout to the probe even when the caller's
	// context has no deadline, so a hung CDN doesn't strand the user
	// in the "resolving" state forever.
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req = req.WithContext(pctx)

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Drain a tiny prefix so the connection can be reused.
	_, _ = io.CopyN(io.Discard, resp.Body, 8)

	switch {
	case resp.StatusCode == http.StatusPartialContent:
		size := parseContentRangeTotal(resp.Header.Get("Content-Range"))
		if size <= 0 {
			return nil, errors.New("probe: 206 without parseable Content-Range")
		}
		return &probeResult{
			Size:         size,
			AcceptRanges: true,
			ETag:         strings.Trim(resp.Header.Get("ETag"), `"`),
			LastModified: parseHTTPTime(resp.Header.Get("Last-Modified")),
		}, nil
	case resp.StatusCode == http.StatusOK:
		size := resp.ContentLength
		acceptRanges := strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes")
		// Some origins return 200 to a Range request but DO support
		// ranges on a fresh GET. Without evidence either way we default
		// to false; the multipart code path will then stay single-stream.
		return &probeResult{
			Size:         size,
			AcceptRanges: acceptRanges,
			ETag:         strings.Trim(resp.Header.Get("ETag"), `"`),
			LastModified: parseHTTPTime(resp.Header.Get("Last-Modified")),
		}, nil
	default:
		return nil, &httpStatusError{StatusCode: resp.StatusCode, URL: url}
	}
}

func parseContentRangeTotal(h string) int64 {
	// "bytes 0-0/12345"  or  "bytes 0-0/*"
	if h == "" {
		return 0
	}
	i := strings.LastIndex(h, "/")
	if i < 0 || i == len(h)-1 {
		return 0
	}
	tail := strings.TrimSpace(h[i+1:])
	if tail == "*" {
		return 0
	}
	n, err := strconv.ParseInt(tail, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func parseHTTPTime(h string) time.Time {
	if h == "" {
		return time.Time{}
	}
	// RFC 1123 with fixed GMT is the canonical HTTP date form. Try a
	// couple of fallbacks for legacy servers.
	for _, layout := range []string{
		http.TimeFormat,
		time.RFC1123,
		time.RFC1123Z,
	} {
		if t, err := time.Parse(layout, h); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func applyHeaders(req *http.Request, opts Options) {
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
}

type httpStatusError struct {
	StatusCode int
	URL        string
}

func (e *httpStatusError) Error() string {
	return "downloader: unexpected status " + strconv.Itoa(e.StatusCode)
}
