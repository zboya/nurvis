package gateway

import (
	"context"
	"encoding/json"

	"github.com/zboya/nurvis/internal/scheduler"
)

// ── cron ─────────────────────────────────────────────────────────────────────

func (m *Methods) handleCronList(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	jobs, err := m.Scheduler.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"jobs": jobs}, nil
}

func (m *Methods) handleCronCreate(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var j scheduler.Job
	if err := json.Unmarshal(params, &j); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if j.Name == "" || j.Spec == "" || j.AgentID == "" || j.Prompt == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "name, spec, agent_id, prompt required"}
	}
	// Target trio: either all empty (no channel binding), or both channel_id + peer_id must be provided.
	// peer_type defaults to "user".
	if j.TargetPeerID != "" && j.TargetChannelID == "" {
		return nil, &RPCError{
			Code:    "invalid_params",
			Message: "target_channel_id is required when target_peer_id is set",
		}
	}
	if j.TargetPeerType != "" && j.TargetPeerType != "user" && j.TargetPeerType != "group" {
		return nil, &RPCError{
			Code:    "invalid_params",
			Message: "target_peer_type must be 'user' or 'group'",
		}
	}
	return m.Scheduler.AddJob(ctx, j)
}

func (m *Methods) handleCronDelete(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	return map[string]any{"ok": true}, m.Scheduler.DeleteJob(ctx, p.ID)
}

func (m *Methods) handleCronToggle(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	return map[string]any{"ok": true}, m.Scheduler.ToggleJob(ctx, p.ID, p.Enabled)
}

func (m *Methods) handleCronRun(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	return map[string]any{"ok": true}, m.Scheduler.RunNow(ctx, p.ID)
}

func (m *Methods) handleCronRuns(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		JobID string `json:"job_id"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.JobID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "job_id required"}
	}
	runs, err := m.Scheduler.ListRuns(ctx, p.JobID, p.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"runs": runs}, nil
}
