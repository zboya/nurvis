// Corresponds to AGENTS.md §13.6 agents table
export interface Agent {
  id: string
  name: string
  role?: string
  system_prompt?: string
  model: string
  default_project?: string
  options_json?: string
  max_rounds: number
  enabled: boolean
  allowed_tools?: string[]
  // Runtime modality classification: 'to-text' (default) | 'to-image' | 'to-video'.
  // chat.send routes the message through different pipelines based on this.
  tag?: 'to-text' | 'to-image' | 'to-video' | string
  // Chat-capable LLM used by to-image / to-video agents to converse with the user
  // (prompt refinement, follow-up questions, summary). Unused for to-text.
  chat_model?: string
  created_at: number
  updated_at: number
}

export interface AgentInput {
  name: string
  role?: string
  system_prompt?: string
  model: string
  default_project?: string
  max_rounds?: number
  enabled?: boolean
  allowed_tools?: string[]
  tag?: 'to-text' | 'to-image' | 'to-video'
  chat_model?: string
}

// Corresponds to AGENTS.md §13.3 projects table
export interface Project {
  id: string
  name: string
  dir: string
  description?: string
  created_at: number
  updated_at: number
}

// Corresponds to AGENTS.md §13.12 sessions table
export interface Session {
  id: string
  agent_id: string
  project_id?: string
  label?: string
  channel?: string
  summary?: string
  created_at: number
  updated_at: number
}

// Corresponds to AGENTS.md §13.13 messages table
export interface MessageMedia {
  kind?: 'image' | 'video' | string
  name?: string
  mime_type?: string
  path?: string
  url?: string
}

export interface Message {
  id: string
  session_id: string
  role: 'system' | 'user' | 'assistant' | 'tool'
  content?: string
  tool_calls?: ToolCall[]
  tool_name?: string
  tokens?: number
  created_at: number
  // UI supplemental fields
  isStreaming?: boolean
  streamingContent?: string
  thinkContent?: string    // Thinking content within <think> tags
  isThinking?: boolean     // Whether currently outputting thinking content
  files?: string[]         // User-attached file path list
  media?: MessageMedia[]   // Generated media (assistant; from to-image / to-video agents)
}

export interface ToolCall {
  id: string
  toolKey?: string
  name: string
  arguments: unknown
  argumentsRaw?: string
  status?: 'streaming' | 'ready' | 'running' | 'done'
  result?: string
  isError?: boolean
}

// Model recommendation
export interface ModelRecommend {
  recommended: string[]
  default_model: string
  hardware: {
    ram_gb: number
    cpu_cores: number
    is_apple_silicon: boolean
    gpus: Array<{ vendor: string; name: string; vram_gb: number }>
  }
}

// Runtime status (llama local llama.cpp backend).
// Mirrors the payload returned by the gateway `runtime.status` RPC.
export interface RuntimeStatus {
  backend: string   // "llama"
  lib_path: string
  ready: boolean
}

// Backwards-compatibility alias for callers still using the old name.
export type OllamaStatus = RuntimeStatus

// Model pull progress
// Fields correspond to PullProgress in internal/modelmgr/manager.go
export interface PullProgress {
  model: string
  // Backend raw values: resolving | downloading | verifying | success | error
  status: string
  percent: number
  // Downloaded bytes (backend field name: current)
  current: number
  total: number
  // Terminal error message; non-empty implies status === "error".
  error?: string
}

// Persisted model row (backend repo.Model). Returned by `models.pull_list`.
export interface ModelRow {
  model: string
  repo: string
  file: string
  // Backend statuses include the persisted-only value "interrupted".
  status: 'queued' | 'resolving' | 'downloading' | 'verifying' | 'success' | 'error' | 'interrupted' | string
  percent: number
  current: number
  total: number
  error?: string
  started_at: number
  updated_at: number
  finished_at?: number
}
