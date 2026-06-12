package gateway

import (
	"context"
	"encoding/json"
)

// ── tools ─────────────────────────────────────────────────────────────

func (m *Methods) handleToolsList(_ context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	names := m.Registry.All()
	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	var tools []toolInfo
	for _, name := range names {
		if t, ok := m.Registry.Get(name); ok {
			tools = append(tools, toolInfo{Name: name, Description: t.Description()})
		}
	}
	return map[string]any{"tools": tools}, nil
}

func (m *Methods) handleToolsToggle(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.Name == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "name required"}
	}
	err := m.Builtins.SetEnabled(ctx, p.Name, p.Enabled)
	return map[string]any{"ok": err == nil}, err
}

func (m *Methods) handleToolsNames(_ context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	names := m.Registry.All()
	return map[string]any{"names": names}, nil
}
