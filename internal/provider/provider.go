// Package provider defines the LLM Provider abstract interface and common types.
package provider

import (
	"context"
	"encoding/json"
)

// Role represents the message role.
type Role = string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolSchema describes the JSON Schema of a single tool (OpenAI function calling format).
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema object
}

// ToolCall represents a tool call initiated by the model.
type ToolCall struct {
	Index     int             `json:"index,omitempty"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Message represents a conversation message.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // associated call id when role=tool
	Name       string     `json:"name,omitempty"`         // tool name when role=tool
	Images     []string   `json:"images,omitempty"`       // base64-encoded images (multimodal)
}

// ChatRequest is a chat request sent to the Provider.
type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []ToolSchema
	Stream   bool
	Options  map[string]any // temperature, num_ctx, top_p, etc.
}

// Chunk is an incremental fragment of streaming output; for non-streaming, Done=true returns the whole.
type Chunk struct {
	Content   string
	Reasoning string // reasoning increment (from the reasoning field, e.g. Ollama thinking models)
	ToolCalls []ToolCall
	Done      bool
	Usage     *Usage

	// FinishReason is the reason the model terminated this turn, meaningful only when Done=true.
	// Standardized values (following OpenAI Chat Completions):
	//   "stop"          normal completion
	//   "length"        reached max_tokens / context limit, truncated
	//   "tool_calls"    model stopped after requesting tool calls
	//   "content_filter" blocked by content safety policy
	//   ""              provider did not return (treated as "stop")
	FinishReason string
}

// Usage records token consumption for this call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ModelInfo describes an available model.
type ModelInfo struct {
	Name         string   `json:"name"`
	SizeBytes    int64    `json:"size_bytes,omitempty"`
	ParamSize    string   `json:"param_size,omitempty"`  // parameter size, e.g. "5.1B"
	Family       string   `json:"family,omitempty"`      // model family, e.g. "gemma4", "llama"
	QuantLevel   string   `json:"quant_level,omitempty"` // quantization level, e.g. "Q4_K_M"
	Format       string   `json:"format,omitempty"`      // file format, e.g. "gguf"
	ModifiedAt   string   `json:"modified_at,omitempty"` // last modified time (RFC3339)
	IsRemote     bool     `json:"is_remote,omitempty"`   // whether this is a remote proxied model
	ContextLen   int      `json:"context_len,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"` // completion, vision, audio, tools, thinking, embed
}

// Provider is the LLM backend abstraction. Phase 1 implements Yzma (local
// llama.cpp) and OpenAI-compatible (remote) providers; the interface is
// reserved for future extensions.
type Provider interface {
	// Name returns the provider identifier, e.g. "llama" or "openai".
	Name() string
	// Chat initiates a conversation, returning a streaming Chunk channel; the caller consumes until Chunk.Done=true or ctx is cancelled.
	Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
	// Embed generates text embedding vectors (for memory retrieval).
	Embed(ctx context.Context, model, text string) ([]float32, error)
	// ListModels lists all available models under the current provider.
	ListModels(ctx context.Context) ([]ModelInfo, error)
}
