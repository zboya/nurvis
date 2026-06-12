// Package gateway implements all RPC method handlers for the Gateway.
// Covers all method groups listed in AGENTS.md §12 phase-one method registry.
package gateway

import (
	"context"
	"encoding/json"

	"github.com/zboya/nurvis/internal/agent"
	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/hardware"
	"github.com/zboya/nurvis/internal/llamax"
	"github.com/zboya/nurvis/internal/mcp"
	"github.com/zboya/nurvis/internal/modelmgr"
	"github.com/zboya/nurvis/internal/provider"
	"github.com/zboya/nurvis/internal/scheduler"
	"github.com/zboya/nurvis/internal/skill"
	"github.com/zboya/nurvis/internal/store"
	"github.com/zboya/nurvis/internal/store/repo"
	"github.com/zboya/nurvis/internal/tools"
	"github.com/zboya/nurvis/internal/workspace"
)

// Methods holds references to subsystems for handler registration.
type Methods struct {
	Agents     *agent.Manager
	Workspaces workspace.Manager
	Models     modelmgr.Manager
	Runtime    llamax.Runtime
	HWInfo     hardware.Info
	Scheduler  *scheduler.Scheduler
	MCPMgr     *mcp.Manager
	SkillMgr   *skill.Manager
	Provider   provider.Provider
	Registry   *tools.Registry
	Store      *store.Store
	Bus        bus.Bus

	// Data access objects (DAO). Gateway never assembles SQL directly; all via repo.
	Sessions    *repo.SessionRepo
	Messages    *repo.MessageRepo
	MCP         *repo.MCPRepo
	Skills      *repo.SkillRepo
	Channels    *repo.ChannelRepo
	Builtins    *repo.BuiltinToolRepo
	Settings    *repo.SettingsRepo
	Credentials *repo.SiteCredentialRepo
	ModelRepo   *repo.ModelRepo
}

// Register registers all methods to the Server.
func (m *Methods) Register(s *Server) {
	// handshake
	s.Handle("connect", m.handleConnect)
	s.Handle("health", m.handleHealth)
	s.Handle("status", m.handleStatus)

	// chat
	s.Handle("chat.send", m.handleChatSend)
	s.Handle("chat.abort", m.handleChatAbort)
	s.Handle("chat.history", m.handleChatHistory)

	// sessions
	s.Handle("sessions.list", m.handleSessionsList)
	s.Handle("sessions.delete", m.handleSessionsDelete)
	s.Handle("sessions.label", m.handleSessionsLabel)

	// agents
	s.Handle("agents.list", m.handleAgentsList)
	s.Handle("agents.create", m.handleAgentsCreate)
	s.Handle("agents.update", m.handleAgentsUpdate)
	s.Handle("agents.delete", m.handleAgentsDelete)

	// projects
	s.Handle("projects.list", m.handleProjectsList)
	s.Handle("projects.create", m.handleProjectsCreate)
	s.Handle("projects.update", m.handleProjectsUpdate)
	s.Handle("projects.delete", m.handleProjectsDelete)

	// models
	s.Handle("models.list", m.handleModelsList)
	s.Handle("models.library", m.handleModelsLibrary)
	s.Handle("models.repo_files", m.handleModelsRepoFiles)
	s.Handle("models.pull", m.handleModelsPull)
	s.Handle("models.delete", m.handleModelsDelete)
	s.Handle("models.recommend", m.handleModelsRecommend)
	s.Handle("models.run", m.handleModelsRun)
	s.Handle("models.capabilities", m.handleModelsCapabilities)
	s.Handle("models.pull_list", m.handleModelsPullList)
	s.Handle("models.pull_dismiss", m.handleModelsPullDismiss)

	// builtin tools
	s.Handle("tools.list", m.handleToolsList)
	s.Handle("tools.toggle", m.handleToolsToggle)
	s.Handle("tools.names", m.handleToolsNames)

	// mcp
	s.Handle("mcp.list", m.handleMCPList)
	s.Handle("mcp.add", m.handleMCPAdd)
	s.Handle("mcp.update", m.handleMCPUpdate)
	s.Handle("mcp.delete", m.handleMCPDelete)
	s.Handle("mcp.grant", m.handleMCPGrant)

	// skills
	s.Handle("skills.list", m.handleSkillsList)
	s.Handle("skills.toggle", m.handleSkillsToggle)
	s.Handle("skills.grant", m.handleSkillsGrant)

	// channels
	s.Handle("channels.list", m.handleChannelsList)
	s.Handle("channels.create", m.handleChannelsCreate)
	s.Handle("channels.update", m.handleChannelsUpdate)
	s.Handle("channels.delete", m.handleChannelsDelete)
	s.Handle("channels.status", m.handleChannelsStatus)

	// cron
	s.Handle("cron.list", m.handleCronList)
	s.Handle("cron.create", m.handleCronCreate)
	s.Handle("cron.delete", m.handleCronDelete)
	s.Handle("cron.toggle", m.handleCronToggle)
	s.Handle("cron.run", m.handleCronRun)
	s.Handle("cron.runs", m.handleCronRuns)

	// hardware / runtime
	s.Handle("hardware.probe", m.handleHardwareProbe)
	s.Handle("runtime.status", m.handleRuntimeStatus)
	s.Handle("runtime.ensure", m.handleRuntimeEnsure)

	// global settings
	s.Handle("settings.get", m.handleSettingsGet)
	s.Handle("settings.set", m.handleSettingsSet)

	// site credentials
	s.Handle("credentials.list", m.handleCredentialsList)
	s.Handle("credentials.create", m.handleCredentialsCreate)
	s.Handle("credentials.update", m.handleCredentialsUpdate)
	s.Handle("credentials.delete", m.handleCredentialsDelete)
}

// ── connect ──────────────────────────────────────────────────────────────────

func (m *Methods) handleConnect(_ context.Context, conn *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Token  string `json:"token"`
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(params, &p)
	conn.auth = true
	return map[string]any{"conn_id": conn.id, "ok": true}, nil
}

func (m *Methods) handleHealth(_ context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	return map[string]any{"status": "ok"}, nil
}

func (m *Methods) handleStatus(_ context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	return map[string]any{"service": "nurvis", "version": "0.1.0"}, nil
}