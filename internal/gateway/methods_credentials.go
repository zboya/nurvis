package gateway

import (
	"context"
	"encoding/json"
)

// ── credentials.list ─────────────────────────────────────────────────────────

func (m *Methods) handleCredentialsList(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Provider string `json:"provider"`
	}
	_ = json.Unmarshal(params, &p)

	list, err := m.Credentials.List(context.Background(), p.Provider)
	if err != nil {
		return nil, err
	}
	// Desensitize: don't return full config_json, only summary info
	type item struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Enabled  bool   `json:"enabled"`
		// Expose partial field summary only (frontend needs to know if configured)
		HasConfig bool  `json:"has_config"`
		CreatedAt int64 `json:"created_at"`
		UpdatedAt int64 `json:"updated_at"`
	}
	items := make([]item, 0, len(list))
	for _, c := range list {
		items = append(items, item{
			ID:        c.ID,
			Name:      c.Name,
			Provider:  c.Provider,
			Enabled:   c.Enabled,
			HasConfig: c.ConfigJSON != "" && c.ConfigJSON != "{}",
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}
	return map[string]any{"credentials": items}, nil
}

// ── credentials.create ───────────────────────────────────────────────────────

func (m *Methods) handleCredentialsCreate(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Name       string          `json:"name"`
		Provider   string          `json:"provider"`
		ConfigJSON json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: "invalid params"}
	}
	if p.Name == "" || p.Provider == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "name and provider are required"}
	}
	configStr := "{}"
	if len(p.ConfigJSON) > 0 {
		configStr = string(p.ConfigJSON)
	}

	cred, err := m.Credentials.Create(context.Background(), p.Name, p.Provider, configStr)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": cred.ID}, nil
}

// ── credentials.update ───────────────────────────────────────────────────────

func (m *Methods) handleCredentialsUpdate(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID         string          `json:"id"`
		Name       string          `json:"name"`
		ConfigJSON json.RawMessage `json:"config"`
		Enabled    *bool           `json:"enabled"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: "invalid params"}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id is required"}
	}

	// Fetch existing record first
	existing, err := m.Credentials.GetByID(context.Background(), p.ID)
	if err != nil || existing == nil {
		return nil, &RPCError{Code: "not_found", Message: "credential not found"}
	}

	name := existing.Name
	if p.Name != "" {
		name = p.Name
	}
	configStr := existing.ConfigJSON
	if len(p.ConfigJSON) > 0 {
		configStr = string(p.ConfigJSON)
	}

	if err := m.Credentials.Update(context.Background(), p.ID, name, configStr, p.Enabled); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ── credentials.delete ───────────────────────────────────────────────────────

func (m *Methods) handleCredentialsDelete(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: "invalid params"}
	}
	if p.ID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "id is required"}
	}
	if err := m.Credentials.Delete(context.Background(), p.ID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
