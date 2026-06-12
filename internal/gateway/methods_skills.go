package gateway

import (
	"context"
	"encoding/json"
)

// ── skills ───────────────────────────────────────────────────────────────────

func (m *Methods) handleSkillsList(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	skills, err := m.Skills.List(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"skills": skills}, nil
}

func (m *Methods) handleSkillsToggle(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
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
	err := m.Skills.SetEnabled(ctx, p.ID, p.Enabled)
	return map[string]any{"ok": err == nil}, err
}

func (m *Methods) handleSkillsGrant(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		SkillID string `json:"skill_id"`
		AgentID string `json:"agent_id"`
		Revoke  bool   `json:"revoke"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.SkillID == "" || p.AgentID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "skill_id and agent_id required"}
	}
	var err error
	if p.Revoke {
		err = m.Skills.Revoke(ctx, p.SkillID, p.AgentID)
	} else {
		err = m.Skills.Grant(ctx, p.SkillID, p.AgentID)
	}
	return map[string]any{"ok": err == nil}, err
}
