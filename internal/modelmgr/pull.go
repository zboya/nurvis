package modelmgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zboya/nurvis/internal/store/repo"
	"github.com/zboya/nurvis/pkg/downloader"
)

const (
	hfBaseURL = "https://huggingface.co"

	// hfEndpointEnv lets users point Pull at a HuggingFace-compatible
	// mirror (e.g. https://hf-mirror.com) without recompiling. Useful
	// in regions where huggingface.co is slow or blocked.
	hfEndpointEnv = "NURVIS_HF_ENDPOINT"

	// Multipart tuning. Higher concurrency + smaller chunks pair well
	// with the downloader's work-stealing scheduler and HTTP/1.1-only
	// transport: each worker opens its own TCP stream, and slow CDN
	// edges get drained by faster workers stealing the next chunk.
	dlConcurrency = 8
	dlChunkSize   = 8 * 1024 * 1024 // 8 MiB per chunk
	dlMaxRetries  = 6

	// progressMinInterval throttles how often we forward downloader
	// progress events upstream (gateway throttles further on its own).
	progressMinInterval = 200 * time.Millisecond
)

// resolveHFEndpoint returns the configured HuggingFace endpoint,
// trimmed of trailing slashes. Empty env falls back to the canonical
// host. Honored at call time so a user changing the env doesn't need
// to restart Nurvis (the value is read once per Pull).
func resolveHFEndpoint() string {
	if v := strings.TrimRight(os.Getenv(hfEndpointEnv), "/"); v != "" {
		return v
	}
	return hfBaseURL
}

// Pull downloads ref.File from ref.Repo into <Dir>/<repo>/<file>.
//
// The transfer is performed by the in-house pkg/downloader, which
// supports multi-part concurrent downloads with durable resume backed
// by a sidecar file (<dest>.part.json). Bytes land in <dest>.part and
// are atomically renamed onto <dest> only after a full size check
// passes; an interrupted run can therefore be resumed safely on the
// next Pull call.
//
// Side effects on the models registry (when a pull repo is wired in):
//   - During "resolving" the HuggingFace single-model API is consulted and
//     pipeline_tag / tags / modalities are persisted via UpsertMetadata.
//   - On "success" local_path + size_bytes are persisted via MarkSuccess,
//     so subsequent List calls can serve from DB only.
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
	model := ref.String()
	emit(PullProgress{Model: model, Status: "resolving"})

	url := fmt.Sprintf("%s/%s/resolve/main/%s", resolveHFEndpoint(), ref.Repo, ref.File)
	destDir := filepath.Join(m.dir, filepath.FromSlash(ref.Repo))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	dest := filepath.Join(destDir, ref.File)

	headers := map[string]string{}
	if token := m.resolveHFToken(ctx); token != "" {
		headers["Authorization"] = "Bearer " + token
	}

	// Fast path: dest already exists and looks complete. We don't know
	// the authoritative size yet, but the downloader will probe and
	// either confirm-then-no-op or detect a mismatch and restart. Emit
	// an early tick so the UI doesn't sit at "resolving".
	emit(PullProgress{Model: model, Status: "downloading"})

	// Throttle progress emissions: chunk callbacks can fire very
	// frequently (every 256 KiB), but the UI only needs ~5 Hz.
	var lastEmit atomic.Int64 // unix nanos
	progressCB := func(p downloader.Progress) {
		nowNs := time.Now().UnixNano()
		last := lastEmit.Load()
		// Always emit the final 100% tick; throttle the rest.
		if p.Percent < 100 && nowNs-last < int64(progressMinInterval) {
			return
		}
		if !lastEmit.CompareAndSwap(last, nowNs) {
			return
		}
		emit(PullProgress{
			Model:   model,
			Status:  "downloading",
			Total:   p.Total,
			Current: p.Downloaded,
			Percent: p.Percent,
		})
	}

	opts := downloader.Options{
		Concurrency:      dlConcurrency,
		ChunkSize:        dlChunkSize,
		MaxRetries:       dlMaxRetries,
		Headers:          headers,
		UserAgent:        "nurvis-modelmgr/1.0",
		Progress:         progressCB,
		MinMultipartSize: 16 * 1024 * 1024, // skip multipart for tiny configs/tokenizers
	}

	slog.Info("modelmgr: pull starting", "url", url, "dest", dest)
	stats, err := downloader.Download(ctx, url, dest, opts)
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
	if stats != nil && stats.Total > 0 {
		total = stats.Total
	}

	// pkg/downloader already guarantees the dest size equals the
	// authoritative Content-Length before performing the atomic
	// rename, so reaching this line means the file is good. Keep the
	// log for observability.
	slog.Info("modelmgr: pull done",
		"model", model, "size", info.Size(),
		"resumed", stats != nil && stats.Resumed,
		"multipart", stats != nil && stats.Multipart,
		"retries", func() int {
			if stats == nil {
				return 0
			}
			return stats.Retries
		}(),
		"duration", func() time.Duration {
			if stats == nil {
				return 0
			}
			return stats.Duration
		}())

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

	detail, err := fetchModelDetailWithToken(mctx, ref.Repo, m.resolveHFToken(mctx))
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
