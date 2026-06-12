package gateway

import (
	"context"
	"encoding/json"

	"github.com/zboya/nurvis/internal/agent"
)

// ── agents ───────────────────────────────────────────────────────────────────

func (m *Methods) handleAgentsList(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	agents, err := m.Agents.List(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"agents": agents}, nil
}

func (m *Methods) handleAgentsCreate(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var a agent.Agent
	if err := json.Unmarshal(params, &a); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if a.Name == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "name required"}
	}
	if a.Model == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "model required"}
	}
	created, err := m.Agents.Create(ctx, a)
	if err != nil {
		return nil, err
	}
	return map[string]any{"agent": created}, nil
}

func (m *Methods) handleAgentsUpdate(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var a agent.Agent
	if err := json.Unmarshal(params, &a); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if a.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	updated, err := m.Agents.Update(ctx, a)
	if err != nil {
		return nil, err
	}
	return map[string]any{"agent": updated}, nil
}

func (m *Methods) handleAgentsDelete(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	return map[string]any{"ok": true}, m.Agents.Delete(ctx, p.ID)
}
