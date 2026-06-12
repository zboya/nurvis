package modelmgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/forest6511/gdl"
	"github.com/zboya/nurvis/internal/store/repo"
)

const (
	hfBaseURL = "https://huggingface.co"

	// Tunables for the underlying multi-part downloader.
	dlConcurrency  = 4
	dlRetryAttempt = 3

	// progressMinInterval throttles how often we forward gdl progress events
	// upstream (gateway throttles further on its own).
	progressMinInterval = 200 * time.Millisecond
)

// Pull downloads ref.File from ref.Repo into <Dir>/<repo>/<file>.
//
// Implementation uses github.com/forest6511/gdl for concurrent multi-part
// downloads with built-in cross-process resume: gdl writes directly into the
// destination path and keeps a "<dest>.resume" sidecar file with the partial
// download state, so a download interrupted in a previous process can be
// continued the next time Pull is invoked for the same ref.
//
// Side effects on the models registry (when a pull repo is wired in):
//   - During "resolving" the HuggingFace single-model API is consulted and
//     pipeline_tag / tags / modalities are persisted via UpsertMetadata.
//   - On "success" local_path + size_bytes are persisted via MarkSuccess, so
//     subsequent List calls can serve from DB only.
func (m *manager) Pull(ctx context.Context, ref ModelRef) (<-chan PullProgress, error) {
	if ref.File == "" {
		return nil, errors.New("modelmgr: pull: empty file")
	}
	if ref.Repo == "" {
		return nil, errors.New("modelmgr: pull: empty repo")
	}

	ch := make(chan PullProgress, 32)
	go func() {
		defer close(ch)
		err := m.pullSync(ctx, ref, func(p PullProgress) {
			// slog.Debug("pullSync", "model", ref.String(), "process", p)
			select {
			case ch <- p:
			default:
				// drop event if subscriber is slow; preserves liveness
			}
		})
		if err != nil {
			slog.Error("pullSync", "error", err)
			ch <- PullProgress{
				Model:  ref.String(),
				Status: "error",
				Error:  err.Error(),
			}
			return
		}
	}()
	return ch, nil
}

func (m *manager) pullSync(ctx context.Context, ref ModelRef, emit func(PullProgress)) error {
	emit(PullProgress{Model: ref.String(), Status: "resolving"})

	url := fmt.Sprintf("%s/%s/resolve/main/%s", hfBaseURL, ref.Repo, ref.File)
	destDir := filepath.Join(m.dir, filepath.FromSlash(ref.Repo))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	dest := filepath.Join(destDir, ref.File)

	headers := map[string]string{}
	if token := os.Getenv("HF_TOKEN"); token != "" {
		headers["Authorization"] = "Bearer " + token
	}

	model := ref.String()
	var lastEmit time.Time
	var lastReported atomic.Int64 // bytes last reported by either source
	opts := &gdl.Options{
		MaxConcurrency: dlConcurrency,
		EnableResume:   true,
		RetryAttempts:  dlRetryAttempt,
		UserAgent:      "nurvis-modelmgr/1.0",
		Headers:        headers,
		CreateDirs:     true,
		ProgressCallback: func(p gdl.Progress) {
			now := time.Now()
			// always forward the final 100% tick, throttle the rest
			if p.Percentage < 100 && now.Sub(lastEmit) < progressMinInterval {
				return
			}
			lastEmit = now
			lastReported.Store(p.BytesDownloaded)
			emit(PullProgress{
				Model:   model,
				Status:  "downloading",
				Total:   p.TotalSize,
				Current: p.BytesDownloaded,
				Percent: p.Percentage,
			})
		},
	}

	// Probe Content-Length up front so the polling fallback below has a
	// total to compute percentage against. HF returns 302 → CDN; a GET with
	// immediate close is more reliable than HEAD (which often 400s on HF).
	totalSize := probeContentLength(ctx, url, headers)

	slog.Info("start downloading", "url", url, "probedSize", totalSize)
	// Emit an initial downloading tick so the UI can leave the
	// "resolving" state even before gdl produces its first progress
	// callback (which can take a while when renegotiating resume).
	emit(PullProgress{Model: model, Status: "downloading", Total: totalSize})

	// Fallback progress poller: gdl's ProgressCallback can be silent for
	// long stretches when it falls back to single-stream mode (HF CDN
	// returning 400 on HEAD/Range probes). Stat the destination file
	// periodically so the UI keeps moving regardless.
	pollCtx, pollCancel := context.WithCancel(ctx)
	defer pollCancel()
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(dest)
				if err != nil {
					continue
				}
				cur := info.Size()
				if cur <= lastReported.Load() {
					continue // gdl's callback is fresher
				}
				lastReported.Store(cur)
				var pct float64
				if totalSize > 0 {
					pct = float64(cur) / float64(totalSize) * 100
				}
				emit(PullProgress{
					Model:   model,
					Status:  "downloading",
					Total:   totalSize,
					Current: cur,
					Percent: pct,
				})
			}
		}
	}()

	stats, err := gdl.DownloadWithOptions(ctx, url, dest, opts)
	pollCancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("download: %w", err)
	}

	emit(PullProgress{Model: model, Status: "verifying"})

	info, statErr := os.Stat(dest)
	if statErr != nil {
		return fmt.Errorf("stat dest: %w", statErr)
	}
	total := info.Size()
	if stats != nil && stats.TotalSize > 0 {
		total = stats.TotalSize
	}

	// Persist GGUF metadata + local path so List can serve from DB only.
	m.persistSuccessMeta(ctx, ref, dest, total)

	emit(PullProgress{
		Model:   model,
		Status:  "success",
		Total:   total,
		Current: info.Size(),
		Percent: 100,
	})
	return nil
}

// probeContentLength tries to discover the final size of url via a HEAD
// request. Returns 0 on any error — callers should treat 0 as "unknown".
func probeContentLength(ctx context.Context, url string, headers map[string]string) int64 {
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodHead, url, nil)
	if err != nil {
		return 0
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "nurvis-modelmgr/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0
	}
	return resp.ContentLength
}

// fetchAndPersistHFMetadata calls the HF single-model API and writes the
// resulting pipeline_tag / tags / modalities to the registry. All errors are
// swallowed (logged at warn level): metadata is best-effort and must not
// block the actual download.
func (m *manager) fetchAndPersistHFMetadata(ctx context.Context, ref ModelRef) {
	if m.pull == nil {
		return
	}
	// Tighter timeout than the surrounding download context so a flaky
	// metadata endpoint can't hold up the user.
	mctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	detail, err := FetchModelDetail(mctx, ref.Repo)
	if err != nil {
		slog.Warn("modelmgr: hf detail fetch failed (continuing)", "repo", ref.Repo, "err", err)
		return
	}
	mods := ModalitiesFromHF(detail.PipelineTag, detail.Tags)
	slog.Info("UpsertMetadata", "model", ref.String(), "PipelineTag", detail.PipelineTag, "Tags", detail.Tags, "mods", mods)
	if err := m.pull.UpsertMetadata(ctx, ref.String(), detail.PipelineTag, detail.Tags, mods); err != nil {
		slog.Warn("modelmgr: persist hf metadata failed", "model", ref.String(), "err", err)
	}
}

// persistSuccessMeta writes the local file info (path + size) onto the
// models row. Heavy GGUF header parsing was intentionally removed —
// it was slow on large files and the fields it would populate are not
// consumed on the read path. Parse lazily on first use if needed later.
func (m *manager) persistSuccessMeta(ctx context.Context, ref ModelRef, dest string, size int64) {
	if m.pull == nil {
		return
	}
	// Best-effort: pull HF metadata before kicking off the download. Failure
	// here is non-fatal — we still want users to download GGUF files even
	// when HF's metadata API is rate-limited or briefly unavailable.
	m.fetchAndPersistHFMetadata(ctx, ref)
	meta := repo.SuccessMeta{
		LocalPath: dest,
		SizeBytes: size,
	}
	if err := m.pull.MarkSuccess(ctx, ref.String(), meta); err != nil {
		slog.Warn("modelmgr: persist success meta failed", "model", ref.String(), "err", err)
	}
}
