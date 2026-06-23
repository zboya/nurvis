// Package agent provides Agent CRUD management and runtime instance caching.
package agent

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/zboya/nurvis/internal/backends/gosd"
	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/provider"
	"github.com/zboya/nurvis/internal/skill"
	"github.com/zboya/nurvis/internal/store/repo"
	"github.com/zboya/nurvis/internal/tools"
	"github.com/zboya/nurvis/internal/workspace"
)

// Agent model is defined in the repo package; type alias preserves compatibility externally.
type Agent = repo.Agent

// MediaArtifact is a neutral media attachment structure for Loop input/output.
//
// Fields align with channel.Artifact, but it is defined in the agent package to
// avoid a reverse dependency from agent → channel. The Channel Dispatcher converts
// channel.Artifact to this type when calling DispatchChannel; the agent.run.completed
// event payload uses the same field names (kind/name/mime_type/url/data).
type MediaArtifact struct {
	Kind     string `json:"kind,omitempty"` // image | video | audio | file
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Path     string `json:"path,omitempty"`
	URL      string `json:"url,omitempty"`
	Data     []byte `json:"data,omitempty"`
}

// PeerIdentity describes a Channel peer (user or group), isomorphic to channel.Identity.
// Defined separately in the agent package to avoid agent → channel reverse dependency.
type PeerIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"` // user | group
}

// ChatRequest holds the parameters for triggering an Agent Loop.
type ChatRequest struct {
	AgentID   string   `json:"agent_id"`
	SessionID string   `json:"session_id,omitempty"` // Empty → create new
	ProjectID string   `json:"project_id,omitempty"` // Overrides agent default workspace
	Text      string   `json:"text"`
	Channel   string   `json:"channel,omitempty"` // Source label: desktop | wechat | qq | cron
	Files     []string `json:"files,omitempty"`   // Local absolute paths of user attachments (image/text), read and injected by backend

	// ChannelInstanceID is the real Channel instance ID (i.e. channels table primary key).
	// Only populated when this Loop is triggered by a Channel inbound message; empty for desktop/cron.
	// The channel.send tool uses this value from Scope as the default target when no
	// explicit channel_id is provided.
	ChannelInstanceID string `json:"channel_instance_id,omitempty"`

	// ReplyTo is the "original sender" in passive-reply scenarios, allowing tools
	// to default-reply to that peer.
	// For group messages: ID=group_<gid>, Type=group; for DMs: ID=openid, Type=user.
	ReplyTo *PeerIdentity `json:"reply_to,omitempty"`

	// InboundMedia contains media attachments from the Channel (images/voice/video sent
	// by QQ/WeChat users). Complements Files (local paths): InboundMedia typically only
	// has remote URLs; the prompt stage appends an "attachment list" to the task so the
	// model is aware. Full multimodal byte injection is deferred to a later phase.
	InboundMedia []MediaArtifact `json:"inbound_media,omitempty"`
}

// Manager handles Agent CRUD and runtime dispatch.
type Manager struct {
	repo     *repo.AgentRepo
	sessions *repo.SessionRepo
	messages *repo.MessageRepo
	provider provider.Provider
	registry *tools.Registry
	ws       workspace.Manager
	bus      bus.Bus
	skillMgr *skill.Manager

	// Optional gosd runtime for to-image / to-video agents. May be nil; in
	// that case to-image / to-video chats fail with a friendly error.
	gosdRT gosd.Runtime

	// Optional model-meta lookup used by Create to auto-infer agent.Tag.
	// When nil the caller-supplied tag (or default to-text) is preserved.
	modelMeta modelMetaLookup

	// MediaPreviewURL produces a publicly-reachable URL for a local file
	// path. Set by app wiring; if nil the MediaLoop falls back to the raw
	// file path (frontend must handle file:// URLs which Wails3 does).
	MediaPreviewURL func(path string) (url string, err error)

	// MediaOutputDir is where to-image / to-video artifacts are saved.
	// Defaults to ~/.nurvis/outputs.
	MediaOutputDir string

	mu      sync.Mutex
	running map[string]context.CancelFunc // sessionID → cancel
}

// NewManager creates an Agent Manager.
func NewManager(
	db *sql.DB,
	prov provider.Provider,
	registry *tools.Registry,
	ws workspace.Manager,
	b bus.Bus,
	skillMgr *skill.Manager,
) *Manager {
	return &Manager{
		repo:     repo.NewAgentRepo(db),
		sessions: repo.NewSessionRepo(db),
		messages: repo.NewMessageRepo(db),
		provider: prov,
		registry: registry,
		ws:       ws,
		bus:      b,
		skillMgr: skillMgr,
		running:  make(map[string]context.CancelFunc),
	}
}

// SetMediaRuntime wires the gosd runtime, model metadata lookup and media
// output directory used by to-image / to-video agents. Safe to call once
// after NewManager during app wiring.
func (m *Manager) SetMediaRuntime(rt gosd.Runtime, lookup modelMetaLookup, outDir string) {
	m.gosdRT = rt
	m.modelMeta = lookup
	m.MediaOutputDir = outDir
}

// SetMediaOutputDir updates the media output directory at runtime.
// Empty string falls back to the default (~/.nurvis/outputs) inside mediaLoop.
func (m *Manager) SetMediaOutputDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MediaOutputDir = dir
}

// GetMediaOutputDir returns the currently configured media output directory.
func (m *Manager) GetMediaOutputDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.MediaOutputDir
}

// Create creates a new Agent.
func (m *Manager) Create(ctx context.Context, a Agent) (*Agent, error) {
	if a.Options == nil {
		a.Options = make(map[string]any)
	}
	_, ok := a.Options["context_window"]
	if !ok {
		a.Options["context_window"] = 32 * 1024
	}

	// Tag handling:
	//   - Caller-supplied valid tag wins.
	//   - Otherwise, attempt to infer from model metadata (HF pipeline_tag /
	//     tags / modalities) so a Wan2.2 video model is auto-tagged
	//     to-video without the user picking it manually.
	//   - Default to to-text.
	if !IsValidTag(a.Tag) {
		a.Tag = ""
		if m.modelMeta != nil && a.Model != "" {
			if pt, tags, mods, ok := m.modelMeta.LookupModelMeta(ctx, a.Model); ok {
				a.Tag = InferAgentTag(pt, tags, mods)
			}
		}
		if a.Tag == "" {
			a.Tag = TagToText
		}
	}

	return m.repo.Create(ctx, a)
}

// Get retrieves an Agent by ID.
func (m *Manager) Get(ctx context.Context, id string) (*Agent, error) {
	return m.repo.Get(ctx, id)
}

// List returns all Agents.
func (m *Manager) List(ctx context.Context) ([]Agent, error) {
	return m.repo.List(ctx)
}

// Update modifies Agent fields.
func (m *Manager) Update(ctx context.Context, a Agent) (*Agent, error) {
	if a.Options == nil {
		a.Options = make(map[string]any)
	}
	if _, ok := a.Options["context_window"]; !ok {
		a.Options["context_window"] = 32 * 1024
	}
	if !IsValidTag(a.Tag) {
		a.Tag = TagToText
	}
	return m.repo.Update(ctx, a)
}

// Delete removes an Agent.
func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.repo.Delete(ctx, id)
}

// Dispatch starts an Agent Loop (runs in a new goroutine).
func (m *Manager) Dispatch(ctx context.Context, req ChatRequest) (sessionID string, err error) {
	a, err := m.Get(ctx, req.AgentID)
	if err != nil {
		return "", fmt.Errorf("agent dispatch: %w", err)
	}

	sessionID = req.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	loopCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.running[sessionID] = cancel
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.running, sessionID)
			m.mu.Unlock()
		}()

		req.SessionID = sessionID

		var runErr error
		switch a.Tag {
		case TagToImage, TagToVideo:
			// Media generation runs through the gosd runtime instead of the
			// llama agent loop. Falls back to a friendly error if the
			// runtime hasn't been wired in (e.g. tests, or build without the
			// sd shared libs).
			runner := newMediaLoop(a, sessionID, req,
				m.sessions, m.messages, m.bus,
				m.gosdRT, m.GetMediaOutputDir(), m.MediaPreviewURL)
			runErr = runner.Run(loopCtx)
		default:
			loop := NewLoop(a, sessionID, req, m.sessions, m.messages, m.provider, m.registry, m.ws, m.bus, m.skillMgr)
			runErr = loop.Run(loopCtx)
		}
		if runErr != nil {
			m.bus.Publish(bus.TopicAgentRunAborted, map[string]any{
				"session_id": sessionID,
				"error":      runErr.Error(),
			})
		}
	}()
	return sessionID, nil
}

// Abort cancels a running Loop.
func (m *Manager) Abort(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.running[sessionID]; ok {
		cancel()
	}
}

// --- helpers ---

// DispatchCronJob implements the scheduler.Dispatcher interface (no circular dependency version).
// agentID, projectID, prompt are unpacked by the scheduler.
//
// channelID / peerID / peerType are optional "target peer":
//   - All empty: pure trigger Loop (does not enter "passive reply" branch;
//     channel.send tool calls will report no channel context).
//   - Non-empty: ChannelInstanceID + ReplyTo are filled into ChatRequest;
//     Scope is auto-injected so channel.send inside the Agent hits the target.
func (m *Manager) DispatchCronJob(
	ctx context.Context,
	agentID, projectID, prompt string,
	channelID, peerID, peerType string,
) (string, error) {
	req := ChatRequest{
		AgentID:           agentID,
		ProjectID:         projectID,
		Text:              prompt,
		Channel:           "cron",
		ChannelInstanceID: channelID,
	}
	if peerID != "" {
		pt := peerType
		if pt == "" {
			pt = "user"
		}
		req.ReplyTo = &PeerIdentity{ID: peerID, Type: pt}
	}
	return m.Dispatch(ctx, req)
}

// DispatchChannel implements the channel.LoopDispatcher interface.
//
// Parameter semantics:
//   - channelID: real channels table primary key (→ ChannelInstanceID).
//     The source label ("qq"/"wechat") is explicitly passed via channelType,
//     avoiding dual semantics in a single field.
//   - replyTo: original sender (user/group), passed through to Scope so the
//     channel.send tool defaults to replying there.
func (m *Manager) DispatchChannel(
	ctx context.Context,
	channelID, channelType, agentID, sessionID, text string,
	replyTo *PeerIdentity,
	inboundMedia []MediaArtifact,
) (string, error) {
	req := ChatRequest{
		AgentID:           agentID,
		SessionID:         sessionID,
		Text:              text,
		Channel:           channelType, // Source label
		ChannelInstanceID: channelID,
		ReplyTo:           replyTo,
		InboundMedia:      inboundMedia,
	}
	return m.Dispatch(ctx, req)
}
