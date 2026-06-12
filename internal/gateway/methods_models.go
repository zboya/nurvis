package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/hardware"
	"github.com/zboya/nurvis/internal/modelmgr"
	"github.com/zboya/nurvis/internal/provider"
)

// ── models ───────────────────────────────────────────────────────────────────

func (m *Methods) handleModelsList(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	infos, err := m.Models.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("models.list: %w", err)
	}
	out := make([]provider.ModelInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, provider.ModelInfo{
			Name:       info.Name,
			SizeBytes:  info.SizeBytes,
			Format:     "gguf",
			ModifiedAt: info.ModifiedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return map[string]any{"models": out}, nil
}

// models.library returns the curated default catalog (HuggingFace GGUF references).
func (m *Methods) handleModelsLibrary(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Search string
		Limit  int
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	lib, err := m.Models.ListLibrary(ctx, p.Search, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("models.library: %w", err)
	}
	return map[string]any{"library": lib}, nil
}

// models.repo_files lists files in a HuggingFace model repo. Frontend filters by .gguf suffix.
func (m *Methods) handleModelsRepoFiles(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if strings.TrimSpace(p.Repo) == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "repo required"}
	}
	files, err := m.Models.ListRepoFiles(ctx, p.Repo)
	if err != nil {
		return nil, fmt.Errorf("models.repo_files: %w", err)
	}
	// Filter to files only and prefer .gguf.
	out := make([]modelmgr.RepoFile, 0, len(files))
	for _, f := range files {
		if f.Type != "" && f.Type != "file" {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			continue
		}
		out = append(out, f)
	}
	return map[string]any{"files": out}, nil
}

func (m *Methods) handleModelsPull(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Model string `json:"model"`
		Repo  string `json:"repo,omitempty"`
		File  string `json:"file,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}

	ref, err := resolvePullRef(p.Model, p.Repo, p.File)
	if err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}

	// Persist a pull record so the UI can recover after a restart and the user
	// can later see / retry interrupted downloads.
	if m.ModelRepo != nil {
		if err := m.ModelRepo.UpsertStart(ctx, ref.String(), ref.Repo, ref.File); err != nil {
			return nil, fmt.Errorf("models.pull: persist: %w", err)
		}
	}

	// Detach the download from the request context so a frontend reload
	// (which closes the WS connection) does not abort the download. The
	// download still terminates if the whole process exits.
	dlCtx := context.Background()

	progressCh, err := m.Models.Pull(dlCtx, ref)
	if err != nil {
		if m.ModelRepo != nil {
			_ = m.ModelRepo.MarkError(ctx, ref.String(), err.Error())
		}
		return nil, fmt.Errorf("models.pull: %w", err)
	}
	go func() {
		const throttle = 150 * time.Millisecond
		const dbThrottle = 1500 * time.Millisecond
		var lastPub time.Time
		var lastDB time.Time
		var lastStatus string
		for prog := range progressCh {
			now := time.Now()
			statusChanged := prog.Status != lastStatus
			isError := prog.Error != "" || prog.Status == "error"
			isTerminal := isError || prog.Status == "success" || prog.Percent >= 100

			// Persist progress (throttled) and terminal state (always).
			if m.ModelRepo != nil {
				if isTerminal || statusChanged || now.Sub(lastDB) >= dbThrottle {
					switch {
					case isError:
						_ = m.ModelRepo.MarkError(context.Background(), prog.Model, prog.Error)
					case prog.Status == "success":
						// modelmgr already persisted full metadata
						// (local_path, size, GGUF fields, capabilities)
						// via MarkSuccessFull. Don't downgrade by writing
						// MarkSuccess here.
					default:
						_ = m.ModelRepo.UpdateProgress(context.Background(), prog.Model, prog.Status,
							prog.Current, prog.Total, prog.Percent)
					}
					lastDB = now
				}
			}

			// Publish to bus (also throttled, but always on status change & terminal).
			if m.Bus != nil && (statusChanged || isTerminal || now.Sub(lastPub) >= throttle) {
				m.Bus.Publish(bus.TopicModelsPullProgress, prog)
				lastPub = now
				lastStatus = prog.Status
			}
		}
	}()
	return map[string]any{"ok": true, "model": ref.String()}, nil
}

// handleModelsPullList returns all known pull records (active + finished) so
// the frontend can rebuild the progress UI after a reconnect or restart.
func (m *Methods) handleModelsPullList(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	if m.ModelRepo == nil {
		return map[string]any{"pulls": []any{}}, nil
	}
	rows, err := m.ModelRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("models.pull_list: %w", err)
	}
	return map[string]any{"pulls": rows}, nil
}

// handleModelsPullDismiss removes a pull record (e.g. after the user closes
// the progress card for a finished/failed/interrupted entry).
func (m *Methods) handleModelsPullDismiss(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Model == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "model required"}
	}
	if m.ModelRepo != nil {
		if err := m.ModelRepo.Delete(ctx, p.Model); err != nil {
			return nil, fmt.Errorf("models.pull_dismiss: %w", err)
		}
	}
	return map[string]any{"ok": true}, nil
}

// resolvePullRef interprets the various param shapes the frontend may send.
//
// Supported forms:
//   - {repo, file}: explicit
//   - {model: "owner/repo/file.gguf"}: parsed via modelmgr.ParseRef
//   - {model: "library_name"}: matched against DefaultLibrary by display name
func resolvePullRef(model, repo, file string) (modelmgr.ModelRef, error) {
	if repo != "" && file != "" {
		return modelmgr.ModelRef{Repo: repo, File: file}, nil
	}
	if model == "" {
		return modelmgr.ModelRef{}, fmt.Errorf("model or {repo,file} required")
	}
	return modelmgr.ParseRef(model)
}

func (m *Methods) handleModelsRecommend(_ context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	recommended := hardware.Recommend(m.HWInfo)
	return map[string]any{
		"recommended":   recommended,
		"default_model": hardware.DefaultModel(m.HWInfo),
		"hardware":      m.HWInfo,
	}, nil
}

func (m *Methods) handleModelsDelete(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Model == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "model required"}
	}
	if err := m.Models.Delete(ctx, p.Model); err != nil {
		return nil, fmt.Errorf("models.delete: %w", err)
	}
	return map[string]any{"ok": true, "model": p.Model}, nil
}

// handleModelsRun is a no-op in the yzma backend: there is no separate "run"
// step — the model is loaded lazily by the provider on the first Chat call.
// We still respond with a helpful payload so the frontend can probe.
func (m *Methods) handleModelsRun(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Model == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "model required"}
	}
	// Resolve to ensure the file actually exists before declaring success.
	if _, err := m.Models.Resolve(p.Model); err != nil {
		return nil, fmt.Errorf("models.run: %w", err)
	}
	return map[string]any{"ok": true, "model": p.Model, "lazy": true}, nil
}

// handleModelsCapabilities reports vision/tool capabilities for a model.
//
// Source: filename-based heuristic (inferCapabilities). Heavy GGUF header
// parsing was removed from the read path; for an authoritative answer on a
// LIVE model use llamax.Engine.Props() instead.
func (m *Methods) handleModelsCapabilities(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Model == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "model required"}
	}

	// Verify the model is installed before answering, but capabilities
	// themselves come purely from the filename heuristic.
	if _, err := m.Models.Resolve(p.Model); err != nil {
		return nil, fmt.Errorf("models.capabilities: %w", err)
	}

	caps := inferCapabilities(p.Model)
	hasVision := false
	for _, c := range caps {
		if strings.EqualFold(c, "vision") {
			hasVision = true
			break
		}
	}
	return map[string]any{
		"model":        p.Model,
		"capabilities": caps,
		"vision":       hasVision,
	}, nil
}

// inferCapabilities returns a best-effort capability list from a model name.
func inferCapabilities(name string) []string {
	caps := []string{"chat"}
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "vl"),
		strings.Contains(lower, "vision"),
		strings.Contains(lower, "gemma-3"),
		strings.Contains(lower, "gemma3"):
		caps = append(caps, "vision")
	}
	if strings.Contains(lower, "qwen") || strings.Contains(lower, "phi") || strings.Contains(lower, "mistral") {
		caps = append(caps, "tools")
	}
	return caps
}