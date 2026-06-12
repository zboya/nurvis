// Package bus defines event topic constants for all modules, preventing topic strings from scattering across the codebase.
package bus

const (
	// Agent Loop lifecycle
	TopicAgentRunStarted   = "agent.run.started"
	TopicAgentRunCompleted = "agent.run.completed"
	TopicAgentRunAborted   = "agent.run.aborted"
	TopicAgentChunk        = "agent.chunk" // streaming token
	TopicAgentStage        = "agent.stage" // stage enter/exit events

	// Tool
	TopicToolCall   = "tool.call"
	TopicToolResult = "tool.result"

	// Channel inbound/outbound
	TopicChannelInbound  = "channel.inbound"
	TopicChannelOutbound = "channel.outbound"
	TopicChannelStatus   = "channel.status"

	// Local llama.cpp runtime + model management
	TopicRuntimeLibProgress = "runtime.lib.progress"
	TopicModelsPullProgress = "models.pull.progress"

	// Cron
	TopicCronFired = "cron.fired"
)
