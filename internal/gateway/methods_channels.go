package gateway

import (
	"context"
	"encoding/json"
)

// ── channels ─────────────────────────────────────────────────────────────────

func (m *Methods) handleChannelsList(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	channels, err := m.Channels.List(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"channels": channels}, nil
}

func (m *Methods) handleChannelsCreate(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Config  any    `json:"config"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Type == "" || p.Name == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "type and name required"}
	}
	err := m.Channels.Create(ctx, p.Type, p.Name, p.Config, p.AgentID)
	return map[string]any{"ok": err == nil}, err
}

func (m *Methods) handleChannelsUpdate(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Config  any    `json:"config"`
		AgentID string `json:"agent_id"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	err := m.Channels.Update(ctx, p.ID, p.Name, p.Config, p.AgentID, p.Enabled)
	return map[string]any{"ok": err == nil}, err
}

func (m *Methods) handleChannelsDelete(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	err := m.Channels.Delete(ctx, p.ID)
	return map[string]any{"ok": err == nil}, err
}

func (m *Methods) handleChannelsStatus(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(params, &p)

	// Query status of a specified channel or all channels (current phase returns config state from DB only)
	if p.ID != "" {
		st, err := m.Channels.Status(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id": st.ID, "type": st.Type, "name": st.Name,
			"enabled": st.Enabled, "running": st.Running,
		}, nil
	}
	statuses, err := m.Channels.ListStatus(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"channels": statuses}, nil
}
