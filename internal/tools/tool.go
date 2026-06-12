// Package tool defines the tool abstraction interface and unified Registry.
// Built-in tools, MCP tools, and Skill tools are all adapted as Tool and registered into the same Registry.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zboya/nurvis/internal/provider"
)

// Scope is the runtime context injected during tool execution.
type Scope struct {
	WorkspaceDir string // Root directory of the current workspace (tool file operations must be constrained within this path)
	AgentID      string
	SessionID    string
	ProjectID    string // Current project (workspace) ID, optional. Used by tools like cron.* to inherit the active workspace.

	// ChannelID is the Channel instance ID of the current Loop (only set when this
	// Loop is triggered by a Channel inbound message). The channel.send tool uses
	// this value when channel_id is not explicitly passed, so the model doesn't need
	// to worry about ID details in passive response scenarios.
	ChannelID string

	// ReplyTo is the original sender (user/group) in passive response scenarios;
	// channel.send defaults to replying to this peer.
	ReplyTo *ScopePeer

	// SkillRoots maps skill names to directory paths for skills activated via use_skill
	// in this session. The exec tool passes them as NURVIS_SKILL_<NAME>_DIR environment
	// variables to child processes, allowing skill scripts to reference paths relatively.
	SkillRoots map[string]string
}

// ScopePeer describes the default reply target in passive response scenarios,
// isomorphic to channel.Identity / agent.PeerIdentity, defined independently to avoid reverse dependencies.
type ScopePeer struct {
	ID   string
	Name string
	Type string // user | group
}

// Artifact represents a media file produced by a tool (image, document, etc.).
type Artifact struct {
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Path     string `json:"path,omitempty"` // Local absolute path (if the tool writes a file to disk)
	URL      string `json:"url,omitempty"`  // Remote URL
	Data     []byte `json:"data,omitempty"` // Inline binary
}

// Result is the execution result of a tool.
type Result struct {
	Content string         `json:"content"`         // Text content fed back to the model
	Media   []Artifact     `json:"media,omitempty"` // Artifacts
	IsError bool           `json:"is_error,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// Tool is the unified interface for all tools.
type Tool interface {
	Name() string
	Description() string
	Schema() provider.ToolSchema
	Invoke(ctx context.Context, args json.RawMessage, scope Scope) (*Result, error)
}

// Registry manages all registered tools, supporting filtering by agent allowlist.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register registers a tool; returns an error if the name is already taken.
func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name()]; ok {
		return fmt.Errorf("tool registry: %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

// MustRegister registers a tool; panics on failure (used during init phase).
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Unregister removes a registered tool; silently returns if not found.
// Primarily used by dynamic sources (MCP / Skill) to clean up tools on disconnect/uninstall.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns the names of all registered tools.
func (r *Registry) All() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	return names
}

// Schemas filters and returns tool schemas by allowlist.
// Returns all schemas when allow is nil or empty.
func (r *Registry) Schemas(allow []string) []provider.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	allowSet := make(map[string]bool, len(allow))
	for _, a := range allow {
		allowSet[a] = true
	}

	var schemas []provider.ToolSchema
	for name, t := range r.tools {
		if len(allowSet) > 0 && !allowSet[name] {
			continue
		}
		schemas = append(schemas, t.Schema())
	}
	return schemas
}
