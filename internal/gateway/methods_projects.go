package gateway

import (
	"context"
	"encoding/json"
)

// ── projects ─────────────────────────────────────────────────────────────────

func (m *Methods) handleProjectsList(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	projects, err := m.Workspaces.List(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"projects": projects}, nil
}

func (m *Methods) handleProjectsCreate(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Name        string `json:"name"`
		Dir         string `json:"dir"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Name == "" || p.Dir == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "name and dir required"}
	}
	return m.Workspaces.Create(ctx, p.Name, p.Dir, p.Description)
}

func (m *Methods) handleProjectsUpdate(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Dir         string `json:"dir"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	return m.Workspaces.Update(ctx, p.ID, p.Name, p.Dir, p.Description)
}

func (m *Methods) handleProjectsDelete(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	return map[string]any{"ok": true}, m.Workspaces.Delete(ctx, p.ID)
}
