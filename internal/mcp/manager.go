// Package mcp manages MCP (Model Context Protocol) server connections
// and adapts MCP tools as tools.Tool registered into a unified Registry.
//
// It uses the community-driven github.com/mark3labs/mcp-go SDK
// and supports stdio / sse / streamable-http transports.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpproto "github.com/mark3labs/mcp-go/mcp"

	"github.com/zboya/nurvis/internal/provider"
	"github.com/zboya/nurvis/internal/store/repo"
	"github.com/zboya/nurvis/internal/tools"
)

// ServerConfig is defined in the repo package; type alias preserves compatibility.
type ServerConfig = repo.ServerConfig

// Transport values.
const (
	TransportStdio = "stdio"
	TransportSSE   = "sse"
	TransportHTTP  = "http" // streamable HTTP
)

// Manager manages multiple MCP server connections and registers tools into the Registry.
type Manager struct {
	repo     *repo.MCPRepo
	registry *tools.Registry
	mu       sync.Mutex
	clients  map[string]*serverClient // serverID → client
}

// serverClient wraps an mcp-go Client along with its related metadata.
type serverClient struct {
	cfg    ServerConfig
	client *mcpclient.Client
	tools  []string // tool names registered in registry (with prefix)
}

// NewManager creates a new MCP Manager.
func NewManager(db *sql.DB, registry *tools.Registry) *Manager {
	return &Manager{
		repo:     repo.NewMCPRepo(db),
		registry: registry,
		clients:  make(map[string]*serverClient),
	}
}

// LoadAndConnect loads all enabled MCP servers from the database and establishes connections.
func (m *Manager) LoadAndConnect(ctx context.Context) error {
	servers, err := m.listServers(ctx)
	if err != nil {
		return fmt.Errorf("mcp: load servers: %w", err)
	}
	for _, s := range servers {
		if !s.Enabled {
			continue
		}
		if err := m.Connect(ctx, s); err != nil {
			slog.Warn("mcp: connect failed", "server", s.Name, "transport", s.Transport, "err", err)
		}
	}
	return nil
}

// Connect connects to a single MCP server, fetches its tool list, and registers them.
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig) error {
	cli, err := newClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("mcp: new client %s: %w", cfg.Name, err)
	}

	// Some transports (SSE / HTTP) require an explicit Start; stdio auto-starts
	// on construction. Calling Start again is safe (the implementation ignores it).
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := cli.Start(startCtx); err != nil {
		cancel()
		_ = cli.Close()
		return fmt.Errorf("mcp: start %s: %w", cfg.Name, err)
	}
	cancel()

	// Initialize handshake.
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	initReq := mcpproto.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpproto.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpproto.Implementation{Name: "nurvis", Version: "0.1.0"}
	if _, err := cli.Initialize(initCtx, initReq); err != nil {
		_ = cli.Close()
		return fmt.Errorf("mcp: initialize %s: %w", cfg.Name, err)
	}

	// Fetch the tool list.
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	listResp, err := cli.ListTools(listCtx, mcpproto.ListToolsRequest{})
	if err != nil {
		_ = cli.Close()
		return fmt.Errorf("mcp: list tools %s: %w", cfg.Name, err)
	}

	sc := &serverClient{cfg: cfg, client: cli}

	for _, t := range listResp.Tools {
		regName := sanitizeToolName(fmt.Sprintf("mcp_%s_%s", cfg.Name, t.Name))
		adapter := &mcpToolAdapter{
			registeredName: regName,
			toolName:       t.Name,
			desc:           t.Description,
			schema:         extractSchema(t),
			client:         cli,
		}
		if err := m.registry.Register(adapter); err != nil {
			slog.Warn("mcp: register tool", "name", regName, "err", err)
			continue
		}
		sc.tools = append(sc.tools, regName)
	}

	m.mu.Lock()
	// If the same server was previously connected, clean up first.
	if old, ok := m.clients[cfg.ID]; ok {
		m.unregisterLocked(old)
	}
	m.clients[cfg.ID] = sc
	m.mu.Unlock()

	slog.Info("mcp: connected", "server", cfg.Name, "transport", cfg.Transport, "tools", len(sc.tools))
	return nil
}

// Disconnect disconnects the specified server and unregisters its tools.
func (m *Manager) Disconnect(serverID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[serverID]; ok {
		m.unregisterLocked(c)
		delete(m.clients, serverID)
	}
}

// Close disconnects all servers.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, c := range m.clients {
		m.unregisterLocked(c)
		delete(m.clients, id)
	}
}

// unregisterLocked unregisters a single server's tools and closes its connection while holding the lock.
func (m *Manager) unregisterLocked(sc *serverClient) {
	for _, name := range sc.tools {
		m.registry.Unregister(name)
	}
	if sc.client != nil {
		_ = sc.client.Close()
	}
}

// listServers reads all MCP server configurations from the database.
func (m *Manager) listServers(ctx context.Context) ([]ServerConfig, error) {
	return m.repo.List(ctx)
}

// newClient constructs an appropriate mcp-go client based on the transport type.
func newClient(ctx context.Context, cfg ServerConfig) (*mcpclient.Client, error) {
	switch strings.ToLower(cfg.Transport) {
	case "", TransportStdio:
		if cfg.Command == "" {
			return nil, fmt.Errorf("stdio transport requires command")
		}
		env := make([]string, 0, len(cfg.Env))
		for k, v := range cfg.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		return mcpclient.NewStdioMCPClient(cfg.Command, env, cfg.Args...)
	case TransportSSE:
		if cfg.URL == "" {
			return nil, fmt.Errorf("sse transport requires url")
		}
		return mcpclient.NewSSEMCPClient(cfg.URL)
	case TransportHTTP, "streamable", "streamable_http", "streamable-http":
		if cfg.URL == "" {
			return nil, fmt.Errorf("http transport requires url")
		}
		return mcpclient.NewStreamableHttpClient(cfg.URL)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", cfg.Transport)
	}
}

// extractSchema extracts the JSON Schema map from an mcp.Tool for reporting to the LLM.
func extractSchema(t mcpproto.Tool) map[string]any {
	// Prefer RawInputSchema if set.
	if len(t.RawInputSchema) > 0 {
		var m map[string]any
		if err := json.Unmarshal(t.RawInputSchema, &m); err == nil && m != nil {
			return m
		}
	}
	props := t.InputSchema.Properties
	if props == nil {
		props = map[string]any{}
	}
	m := map[string]any{
		"type":       firstNonEmpty(t.InputSchema.Type, "object"),
		"properties": props,
	}
	if len(t.InputSchema.Required) > 0 {
		m["required"] = t.InputSchema.Required
	}
	return m
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// sanitizeToolName normalizes a tool name to conform to LLM function-calling
// naming conventions (only alphanumeric characters, underscores, and hyphens).
func sanitizeToolName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// ── MCP 工具 Adapter ──────────────────────────────────────────────────────────

type mcpToolAdapter struct {
	registeredName string
	toolName       string
	desc           string
	schema         map[string]any
	client         *mcpclient.Client
}

func (a *mcpToolAdapter) Name() string        { return a.registeredName }
func (a *mcpToolAdapter) Description() string { return a.desc }
func (a *mcpToolAdapter) Schema() provider.ToolSchema {
	params := a.schema
	if params == nil {
		params = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return provider.ToolSchema{
		Name:        a.registeredName,
		Description: a.desc,
		Parameters:  params,
	}
}

func (a *mcpToolAdapter) Invoke(ctx context.Context, raw json.RawMessage, _ tools.Scope) (*tools.Result, error) {
	req := mcpproto.CallToolRequest{}
	req.Params.Name = a.toolName
	// Unmarshal into map for MCP protocol compatibility
	var args map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &args)
	}
	req.Params.Arguments = args

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := a.client.CallTool(callCtx, req)
	if err != nil {
		return &tools.Result{Content: err.Error(), IsError: true}, nil
	}

	text := flattenContent(resp.Content)
	return &tools.Result{Content: text, IsError: resp.IsError}, nil
}

// flattenContent concatenates multi-part MCP content into plain text.
func flattenContent(parts []mcpproto.Content) string {
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			sb.WriteString("\n")
		}
		switch v := p.(type) {
		case mcpproto.TextContent:
			sb.WriteString(v.Text)
		case *mcpproto.TextContent:
			sb.WriteString(v.Text)
		default:
			// Non-text content (images/audio/embedded resources) is JSON-serialized
			// as a fallback so the model can still perceive it.
			if data, err := json.Marshal(p); err == nil {
				sb.Write(data)
			}
		}
	}
	return sb.String()
}
