package gateway

import (
	"context"
	"encoding/json"

	"github.com/zboya/nurvis/internal/mcp"
)

// ── mcp ──────────────────────────────────────────────────────────────────────

func (m *Methods) handleMCPList(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	servers, err := m.MCP.List(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"servers": servers}, nil
}

func (m *Methods) handleMCPAdd(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var cfg mcp.ServerConfig
	if err := json.Unmarshal(params, &cfg); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if cfg.Name == "" || cfg.Transport == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "name and transport required"}
	}
	if err := m.MCP.Add(ctx, cfg); err != nil {
		return nil, err
	}
	// Try to connect immediately
	if cfg.Enabled {
		if err := m.MCPMgr.Connect(ctx, cfg); err != nil {
			return map[string]any{"ok": true, "warning": err.Error()}, nil
		}
	}
	return map[string]any{"ok": true}, nil
}

func (m *Methods) handleMCPUpdate(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var cfg mcp.ServerConfig
	if err := json.Unmarshal(params, &cfg); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if cfg.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	err := m.MCP.Update(ctx, cfg)
	return map[string]any{"ok": err == nil}, err
}

func (m *Methods) handleMCPDelete(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id required"}
	}
	m.MCPMgr.Disconnect(p.ID)
	err := m.MCP.Delete(ctx, p.ID)
	return map[string]any{"ok": err == nil}, err
}

func (m *Methods) handleMCPGrant(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ServerID string `json:"server_id"`
		AgentID  string `json:"agent_id"`
		Revoke   bool   `json:"revoke"` // true = revoke grant
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.ServerID == "" || p.AgentID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "server_id and agent_id required"}
	}
	var err error
	if p.Revoke {
		err = m.MCP.Revoke(ctx, p.ServerID, p.AgentID)
	} else {
		err = m.MCP.Grant(ctx, p.ServerID, p.AgentID)
	}
	return map[string]any{"ok": err == nil}, err
}
