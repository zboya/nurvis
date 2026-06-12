// Package provider provides the OpenAI-compatible Provider implementation.
// It is compatible with /v1/chat/completions, /v1/embeddings, /v1/models,
// and works with OpenAI, Ollama(/v1), and any OpenAI-compatible gateway.
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider implements the Provider interface, connecting to an OpenAI-compatible HTTP API.
type OpenAIProvider struct {
	name    string // provider identifier
	baseURL string // e.g. http://host:port/v1 (without trailing slash)
	apiKey  string // optional; if empty, no Authorization header is sent
	client  *http.Client
}

// OpenAIOption configures OpenAIProvider.
type OpenAIOption func(*OpenAIProvider)

// WithAPIKey sets the Bearer Token.
func WithAPIKey(key string) OpenAIOption {
	return func(p *OpenAIProvider) { p.apiKey = key }
}

// WithName overrides the default provider name.
func WithName(name string) OpenAIOption {
	return func(p *OpenAIProvider) { p.name = name }
}

// WithHTTPClient injects a custom http.Client.
func WithHTTPClient(c *http.Client) OpenAIOption {
	return func(p *OpenAIProvider) { p.client = c }
}

// NewOpenAI creates an OpenAI-compatible Provider.
// baseURL like "https://api.openai.com/v1" or "http://127.0.0.1:11434/v1".
func NewOpenAI(baseURL string, opts ...OpenAIOption) *OpenAIProvider {
	p := &OpenAIProvider{
		name:    "openai",
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *OpenAIProvider) Name() string { return p.name }

// ── Wire types (OpenAI Chat Completions protocol) ────────────────────────────────

type oaiToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"` // string or multimodal array
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type oaiChatRequest struct {
	Model    string       `json:"model"`
	Messages []oaiMessage `json:"messages"`
	Tools    []oaiTool    `json:"tools,omitempty"`
	Stream   bool         `json:"stream"`
	// pass-through options (temperature/top_p/max_tokens, etc.)
	Temperature any `json:"temperature,omitempty"`
	TopP        any `json:"top_p,omitempty"`
	MaxTokens   any `json:"max_tokens,omitempty"`
	KeepAlive   any `json:"keep_alive,omitempty"` // Ollama-specific
}

// buildOAIMessages converts internal Message to OpenAI wire messages.
func buildOAIMessages(in []Message) []oaiMessage {
	out := make([]oaiMessage, 0, len(in))
	for _, m := range in {
		msg := oaiMessage{Role: m.Role, ToolCallID: m.ToolCallID, Name: m.Name}

		// multimodal: use array content when images are present, otherwise plain string
		if len(m.Images) > 0 {
			parts := make([]map[string]any, 0, len(m.Images)+1)
			if m.Content != "" {
				parts = append(parts, map[string]any{"type": "text", "text": m.Content})
			}
			for _, img := range m.Images {
				url := img
				if !strings.HasPrefix(img, "data:") && !strings.HasPrefix(img, "http") {
					url = "data:image/png;base64," + img
				}
				parts = append(parts, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": url},
				})
			}
			msg.Content = parts
		} else {
			msg.Content = m.Content
		}

		// tool calls initiated by assistant
		for _, tc := range m.ToolCalls {
			var oc oaiToolCall
			oc.ID = tc.ID
			oc.Type = "function"
			oc.Function.Name = tc.Name
			oc.Function.Arguments = string(tc.Arguments)
			msg.ToolCalls = append(msg.ToolCalls, oc)
		}
		out = append(out, msg)
	}
	return out
}

func buildOAITools(in []ToolSchema) []oaiTool {
	if len(in) == 0 {
		return nil
	}
	out := make([]oaiTool, len(in))
	for i, t := range in {
		out[i].Type = "function"
		out[i].Function.Name = t.Name
		out[i].Function.Description = t.Description
		out[i].Function.Parameters = t.Parameters
	}
	return out
}

// Chat calls /v1/chat/completions, supporting SSE streaming output.
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	payload := oaiChatRequest{
		Model:    req.Model,
		Messages: buildOAIMessages(req.Messages),
		Tools:    buildOAITools(req.Tools),
		Stream:   req.Stream,
	}
	if p.name == "ollama" {
		payload.KeepAlive = "24h"
	}
	if req.Options != nil {
		payload.Temperature = req.Options["temperature"]
		payload.TopP = req.Options["top_p"]
		payload.MaxTokens = req.Options["max_tokens"]
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	slog.Debug("openai.chat.request",
		"provider", p.name,
		"url", p.baseURL+"/chat/completions",
		"body", string(body),
	)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	p.setHeaders(httpReq)

	start := time.Now()
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s chat: %w", p.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s chat: status %d: %s", p.name, resp.StatusCode, string(b))
	}

	ch := make(chan Chunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		if req.Stream {
			p.streamSSE(ctx, start, resp.Body, ch)
		} else {
			p.readNonStream(resp.Body, ch)
		}
	}()
	return ch, nil
}

// oaiStreamResp corresponds to a single chunk in the SSE data line.
type oaiStreamResp struct {
	Choices []struct {
		Delta struct {
			Content   string        `json:"content"`
			Reasoning string        `json:"reasoning"` // reasoning returned by Ollama thinking models
			ToolCalls []oaiToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// streamSSE parses the SSE stream and forwards incremental tool_call deltas.
func (p *OpenAIProvider) streamSSE(ctx context.Context, reqStart time.Time, body io.Reader, ch chan<- Chunk) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	var lastUsage *Usage

	start := time.Now()    // time when stream reception started
	var ttft time.Duration // time-to-first-token
	tokenCount := 0        // count of text token chunks received

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var r oaiStreamResp
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			slog.Warn("oaiStreamResp unmarshal", "error", err, "raw", data)
			continue
		}
		if r.Usage != nil {
			lastUsage = &Usage{
				PromptTokens:     r.Usage.PromptTokens,
				CompletionTokens: r.Usage.CompletionTokens,
				TotalTokens:      r.Usage.TotalTokens,
			}
		}
		if len(r.Choices) == 0 {
			continue
		}
		choice := r.Choices[0]

		for _, tc := range choice.Delta.ToolCalls {
			if tc.ID == "" && tc.Function.Name == "" && tc.Function.Arguments == "" {
				continue
			}
			safeChunkSend(ch, Chunk{ToolCalls: []ToolCall{{
				Index:     tc.Index,
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			}}})
		}

		// reasoning increment (thinking process)
		if choice.Delta.Reasoning != "" {
			safeChunkSend(ch, Chunk{Reasoning: choice.Delta.Reasoning})
		}

		// emit text increments chunk by chunk
		if choice.Delta.Content != "" {
			if tokenCount == 0 {
				ttft = time.Since(reqStart)
				slog.Info("openai.stream.ttft",
					"provider", p.name,
					"ttft_ms", ttft.Milliseconds(),
				)
			}
			tokenCount++
			safeChunkSend(ch, Chunk{Content: choice.Delta.Content})
		}

		// done
		if choice.FinishReason != nil {
			total := time.Since(start)
			// calculate average tokens/s
			var tokensPerSec float64
			if total.Seconds() > 0 {
				if lastUsage != nil && lastUsage.CompletionTokens > 0 {
					tokensPerSec = float64(lastUsage.CompletionTokens) / total.Seconds()
				} else if tokenCount > 0 {
					tokensPerSec = float64(tokenCount) / total.Seconds()
				}
			}
			slog.Info("openai.stream.done",
				"provider", p.name,
				"ttft_ms", ttft.Milliseconds(),
				"total_ms", total.Milliseconds(),
				"token_chunks", tokenCount,
				"finish_reason", *choice.FinishReason,
				"tokens_per_sec", fmt.Sprintf("%.2f", tokensPerSec),
			)
			final := Chunk{
				Done:         true,
				Usage:        lastUsage,
				FinishReason: *choice.FinishReason,
			}
			safeChunkSend(ch, final)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Error("openai.stream.scan.error", "provider", p.name, "error", err)
		safeChunkSend(ch, Chunk{Done: true, Content: "[stream error]: " + err.Error()})
		return
	}

	// fallback for unexpected stream end: send a Done
	safeChunkSend(ch, Chunk{Done: true, Usage: lastUsage})
}

// oaiNonStreamResp corresponds to a non-streaming response.
type oaiNonStreamResp struct {
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (p *OpenAIProvider) readNonStream(body io.Reader, ch chan<- Chunk) {
	var r oaiNonStreamResp
	if err := json.NewDecoder(body).Decode(&r); err != nil {
		return
	}
	start := time.Now()
	chunk := Chunk{Done: true}
	if r.Usage != nil {
		chunk.Usage = &Usage{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens:      r.Usage.TotalTokens,
		}
	}
	if len(r.Choices) > 0 {
		choice := r.Choices[0]
		chunk.Content = choice.Message.Content
		if choice.FinishReason != nil {
			chunk.FinishReason = *choice.FinishReason
		}
		for i, tc := range choice.Message.ToolCalls {
			id := tc.ID
			if id == "" {
				id = fmt.Sprintf("call_%d", i)
			}
			raw := json.RawMessage(tc.Function.Arguments)
			if len(raw) == 0 {
				raw = json.RawMessage("{}")
			}
			chunk.ToolCalls = append(chunk.ToolCalls, ToolCall{
				Index: i, ID: id, Name: tc.Function.Name, Arguments: raw,
			})
		}
	}
	elapsed := time.Since(start)
	var tokensPerSec float64
	if elapsed.Seconds() > 0 && chunk.Usage != nil && chunk.Usage.CompletionTokens > 0 {
		tokensPerSec = float64(chunk.Usage.CompletionTokens) / elapsed.Seconds()
	}
	slog.Info("openai.nonstream.done",
		"provider", p.name,
		"total_ms", elapsed.Milliseconds(),
		"completion_tokens", func() int {
			if chunk.Usage != nil {
				return chunk.Usage.CompletionTokens
			}
			return 0
		}(),
		"tokens_per_sec", fmt.Sprintf("%.2f", tokensPerSec),
	)
	safeChunkSend(ch, chunk)
}

// Embed calls /v1/embeddings to generate text embeddings.
func (p *OpenAIProvider) Embed(ctx context.Context, model, text string) ([]float32, error) {
	reqBody := map[string]any{"model": model, "input": text}
	body, _ := json.Marshal(reqBody)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	p.setHeaders(httpReq)

	r, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s embed: %w", p.name, err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		return nil, fmt.Errorf("%s embed: status %d: %s", p.name, r.StatusCode, string(b))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("%s embed: empty data", p.name)
	}
	return result.Data[0].Embedding, nil
}

// ListModels calls /v1/models to list available models.
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.setHeaders(httpReq)

	r, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s list models: %w", p.name, err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		return nil, fmt.Errorf("%s list models: status %d: %s", p.name, r.StatusCode, string(b))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, len(result.Data))
	for i, m := range result.Data {
		models[i] = ModelInfo{Name: m.ID, Capabilities: []string{"chat"}}
	}
	return models, nil
}

func (p *OpenAIProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// safeChunkSend avoids panic when the channel is already closed.
func safeChunkSend(ch chan<- Chunk, c Chunk) {
	defer func() { _ = recover() }()
	ch <- c
}
