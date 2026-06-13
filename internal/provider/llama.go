// Package provider's LlamaProvider is the local-inference Provider implementation.
//
// Architecture:
//
//   - llamax.Runtime spawns one `llama-server` subprocess per GGUF model and
//     exposes each as an OpenAI-compatible HTTP endpoint on a private
//     127.0.0.1 port. llamax does NOT perform inference itself.
//   - LlamaProvider resolves a request's model name to a local path, asks the
//     runtime for an Engine (starting the subprocess on first use), then
//     dispatches the actual chat to a cached OpenAIProvider client pointed at
//     the engine's BaseURL+/v1.
//   - Streaming chunks (including tool_calls) flow back unmodified — the
//     OpenAIProvider already handles SSE parsing and OpenAI-style tool calling.
package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/zboya/nurvis/internal/backends/llamax"
	"github.com/zboya/nurvis/internal/modelmgr"
)

// LlamaProvider runs inference against local llama-server subprocesses managed
// by llamax.
type LlamaProvider struct {
	rt  llamax.Runtime
	mgr modelmgr.Manager

	mu      sync.Mutex
	clients map[string]*OpenAIProvider // engine BaseURL -> client
}

// NewLlama creates a LlamaProvider. The runtime must be EnsureReady'd before any
// Chat call; the provider does not call it implicitly to keep the cold-start
// cost visible to the caller.
func NewLlama(rt llamax.Runtime, mgr modelmgr.Manager) *LlamaProvider {
	return &LlamaProvider{
		rt:      rt,
		mgr:     mgr,
		clients: make(map[string]*OpenAIProvider),
	}
}

// Name returns the provider identifier.
func (p *LlamaProvider) Name() string { return "llama" }

// Chat resolves the model, ensures the corresponding `llama-server` is running,
// and forwards the request to its OpenAI-compatible endpoint.
func (p *LlamaProvider) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if req.Model == "" {
		return nil, errors.New("llama: model required")
	}
	path, err := p.mgr.Resolve(req.Model)
	if err != nil {
		return nil, fmt.Errorf("llama: resolve %q: %w", req.Model, err)
	}

	opts := llamax.ModelOptions{}
	if v, ok := req.Options["context_window"]; ok {
		opts.ContextSize = toUint32(v)
	}
	if v, ok := req.Options["n_gpu_layers"]; ok {
		opts.GPULayers = int32(toInt(v))
	}
	if v, ok := req.Options["threads"]; ok {
		opts.Threads = int32(toInt(v))
	}

	eng, err := p.rt.LoadModel(path, opts)
	if err != nil {
		return nil, fmt.Errorf("llama: load model: %w", err)
	}

	client := p.clientFor(eng)

	// llama-server doesn't really care about the model field (it serves whatever
	// was passed via -m), but we forward the requested name for log clarity.
	subReq := req
	subReq.Stream = true
	return client.Chat(ctx, subReq)
}

// clientFor returns a cached OpenAIProvider client targeted at the engine's
// /v1 endpoint, creating one lazily.
func (p *LlamaProvider) clientFor(eng *llamax.Engine) *OpenAIProvider {
	url := eng.OpenAIBaseURL()
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[url]; ok {
		return c
	}
	c := NewOpenAI(url, WithName("llama"))
	p.clients[url] = c
	return c
}

// Embed is not implemented for the local provider in phase 1.
//
// Memory module currently does not depend on embeddings; remote OpenAI-compatible
// providers can still be configured for vector search if needed in the future.
func (p *LlamaProvider) Embed(ctx context.Context, model, text string) ([]float32, error) {
	return nil, errors.New("llama: Embed not implemented (use a remote OpenAI-compatible provider)")
}

// ListModels enumerates locally available GGUF files via modelmgr.
//
// modelmgr serves this from the models registry (rows with
// status='success'); structural details like architecture / quant / context
// length are no longer parsed from the GGUF header on the read path. For a
// LIVE model the authoritative source remains llamax.Engine.Props().
func (p *LlamaProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	infos, err := p.mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, ModelInfo{
			Name:         info.Name,
			SizeBytes:    info.SizeBytes,
			Format:       "gguf",
			ModifiedAt:   info.ModifiedAt.Format("2006-01-02T15:04:05Z07:00"),
			Capabilities: info.Modalities,
		})
	}
	return out, nil
}

// ── numeric coercion helpers (Options is map[string]any) ─────────────────────

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float32:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

func toUint32(v any) uint32 {
	n := toInt(v)
	if n < 0 {
		return 0
	}
	return uint32(n)
}
