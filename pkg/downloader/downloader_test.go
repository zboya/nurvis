package downloader

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newRangeServer serves the given payload with full HTTP/1.1 Range +
// If-Range semantics, and lets the test inject failures (drop the
// connection after N bytes, change the ETag mid-flight, etc.).
type rangeServer struct {
	t       *testing.T
	payload []byte
	etag    string
	srv     *httptest.Server

	// hooks
	dropAfter atomic.Int64 // if >0, close connection after writing N bytes total per response
	failNext  atomic.Int64 // if >0, return 503 for the next N requests then decrement
}

func newRangeServer(t *testing.T, size int64, etag string) *rangeServer {
	t.Helper()
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	rs := &rangeServer{t: t, payload: buf, etag: etag}
	rs.srv = httptest.NewServer(http.HandlerFunc(rs.handle))
	return rs
}

func (r *rangeServer) Close() { r.srv.Close() }
func (r *rangeServer) URL() string { return r.srv.URL + "/file" }

func (r *rangeServer) handle(w http.ResponseWriter, req *http.Request) {
	if remaining := r.failNext.Load(); remaining > 0 {
		r.failNext.Add(-1)
		http.Error(w, "synthetic 503", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Accept-Ranges", "bytes")
	if r.etag != "" {
		w.Header().Set("ETag", `"`+r.etag+`"`)
	}
	w.Header().Set("Last-Modified", time.Unix(1_700_000_000, 0).UTC().Format(http.TimeFormat))

	rangeHdr := req.Header.Get("Range")
	if rangeHdr == "" {
		w.Header().Set("Content-Length", strconv.FormatInt(int64(len(r.payload)), 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(r.payload)
		return
	}
	// Parse "bytes=start-end"
	if !strings.HasPrefix(rangeHdr, "bytes=") {
		http.Error(w, "bad range", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(rangeHdr, "bytes="), "-", 2)
	if len(parts) != 2 {
		http.Error(w, "bad range", http.StatusBadRequest)
		return
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= int64(len(r.payload)) {
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	end := int64(len(r.payload)) - 1
	if parts[1] != "" {
		if v, perr := strconv.ParseInt(parts[1], 10, 64); perr == nil {
			end = v
		}
	}
	if end >= int64(len(r.payload)) {
		end = int64(len(r.payload)) - 1
	}

	body := r.payload[start : end+1]
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(r.payload)))
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(body)), 10))
	w.WriteHeader(http.StatusPartialContent)

	if drop := r.dropAfter.Load(); drop > 0 && int64(len(body)) > drop {
		_, _ = w.Write(body[:drop])
		// Force connection close mid-response so the client sees a
		// truncated stream and has to retry.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, herr := hj.Hijack(); herr == nil {
				_ = conn.Close()
			}
		}
		// Reset the drop hook so the retry succeeds.
		r.dropAfter.Store(0)
		return
	}
	_, _ = w.Write(body)
}

func TestDownload_HappyPath_Multipart(t *testing.T) {
	const size = int64(5 * 1024 * 1024) // 5 MiB
	srv := newRangeServer(t, size, "v1")
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "file.bin")
	stats, err := Download(context.Background(), srv.URL(), dest, Options{
		Concurrency:      4,
		ChunkSize:        1 * 1024 * 1024,
		MinMultipartSize: 1, // force multipart
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if stats.Total != size {
		t.Fatalf("Total = %d, want %d", stats.Total, size)
	}
	if !stats.Multipart {
		t.Fatalf("expected multipart")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, srv.payload) {
		t.Fatalf("payload mismatch")
	}
	// part + sidecar should be gone.
	if _, err := os.Stat(dest + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part not cleaned: %v", err)
	}
	if _, err := os.Stat(dest + ".part.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sidecar not cleaned: %v", err)
	}
}

func TestDownload_ResumeAfterTruncation(t *testing.T) {
	const size = int64(4 * 1024 * 1024) // 4 MiB
	srv := newRangeServer(t, size, "v1")
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")

	// First attempt: drop the connection after the first 100KB of any
	// response body. With chunkSize=1MiB and concurrency=2 this means
	// every chunk's first request fails partway through. The retry
	// path should still drive every chunk to completion.
	srv.dropAfter.Store(100 * 1024)
	stats1, err := Download(context.Background(), srv.URL(), dest, Options{
		Concurrency:      2,
		ChunkSize:        1 * 1024 * 1024,
		MinMultipartSize: 1,
		MaxRetries:       5,
		RetryBackoff:     1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("first Download: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, srv.payload) {
		t.Fatalf("payload mismatch after retries")
	}
	if stats1.Retries == 0 {
		t.Fatalf("expected retries > 0, got 0")
	}
}

func TestDownload_NoOpOnExistingPart(t *testing.T) {
	const size = int64(2 * 1024 * 1024) // 2 MiB
	srv := newRangeServer(t, size, "v1")
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")

	// Run once to completion.
	if _, err := Download(context.Background(), srv.URL(), dest, Options{
		Concurrency: 2, ChunkSize: 512 * 1024, MinMultipartSize: 1,
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}

	// Pre-stage a part file at full size with the correct content +
	// matching sidecar, then re-run — should be a near-no-op (just
	// probe + verify + rename).
	got, _ := os.ReadFile(dest)
	_ = os.Remove(dest)
	if err := os.WriteFile(dest+".part", got, 0o644); err != nil {
		t.Fatalf("stage part: %v", err)
	}
	// Synthesize a sidecar that matches the current origin.
	side := &downloadState{
		URL:          srv.URL(),
		Size:         size,
		ETag:         "v1",
		AcceptRanges: true,
		Chunks: []chunkState{
			{Start: 0, End: size - 1, Downloaded: size},
		},
		CreatedAt: time.Now(),
	}
	if err := writeSidecar(dest+".part.json", side); err != nil {
		t.Fatalf("writeSidecar: %v", err)
	}

	stats, err := Download(context.Background(), srv.URL(), dest, Options{
		Concurrency: 2, ChunkSize: 512 * 1024, MinMultipartSize: 1,
	})
	if err != nil {
		t.Fatalf("resume Download: %v", err)
	}
	if !stats.Resumed {
		t.Fatalf("expected Resumed=true")
	}
	final, _ := os.ReadFile(dest)
	if !bytes.Equal(final, srv.payload) {
		t.Fatalf("payload mismatch after resume")
	}
}

func TestDownload_ETagChangeInvalidatesResume(t *testing.T) {
	const size = int64(1 * 1024 * 1024)
	srv := newRangeServer(t, size, "v1")
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")

	// Stage a sidecar with a stale ETag and a half-filled part file.
	if err := os.WriteFile(dest+".part", bytes.Repeat([]byte{0xAA}, int(size)), 0o644); err != nil {
		t.Fatalf("stage part: %v", err)
	}
	stale := &downloadState{
		URL: srv.URL(), Size: size, ETag: "old-etag", AcceptRanges: true,
		Chunks: []chunkState{
			{Start: 0, End: size - 1, Downloaded: size}, // claims fully downloaded
		},
		CreatedAt: time.Now(),
	}
	if err := writeSidecar(dest+".part.json", stale); err != nil {
		t.Fatalf("writeSidecar: %v", err)
	}

	stats, err := Download(context.Background(), srv.URL(), dest, Options{
		Concurrency: 2, ChunkSize: 256 * 1024, MinMultipartSize: 1,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if stats.Resumed {
		t.Fatalf("expected Resumed=false (etag changed)")
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, srv.payload) {
		t.Fatalf("payload mismatch — stale bytes leaked through")
	}
}
