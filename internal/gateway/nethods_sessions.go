package gateway

import (
	"context"
	"encoding/json"
)

// ── sessions ─────────────────────────────────────────────────────────────────

func (m *Methods) handleSessionsList(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		AgentID string `json:"agent_id"`
		Limit   int    `json:"limit"`
	}
	_ = json.Unmarshal(params, &p)

	sessions, err := m.Sessions.List(ctx, p.AgentID, p.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sessions": sessions}, nil
}

func (m *Methods) handleSessionsDelete(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "session_id required"}
	}
	m.Agents.Abort(p.SessionID) // abort any in-progress loop first
	err := m.Sessions.Delete(ctx, p.SessionID)
	return map[string]any{"ok": err == nil}, err
}

func (m *Methods) handleSessionsLabel(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "session_id required"}
	}
	err := m.Sessions.SetLabel(ctx, p.SessionID, p.Label)
	return map[string]any{"ok": err == nil}, err
}
