package gateway

import (
	"context"
	"encoding/json"
)

// ── settings ─────────────────────────────────────────────────────────────────

func (m *Methods) handleSettingsGet(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Key == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "key required"}
	}
	raw, err := m.Settings.GetRaw(ctx, p.Key)
	if err != nil {
		return nil, err
	}
	// When key does not exist, raw is nil; return value=null
	var value any
	if raw != nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
	}
	return map[string]any{"key": p.Key, "value": value}, nil
}

func (m *Methods) handleSettingsSet(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Key == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "key required"}
	}
	if err := m.Settings.SetRaw(ctx, p.Key, p.Value); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
