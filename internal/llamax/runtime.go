// Package llamax manages local llama.cpp inference by spawning `llama-server`
// subprocesses (one per model) and exposing each as an OpenAI-compatible HTTP
// endpoint on a private localhost port.
//
// Design notes:
//
//   - Runtime owns the install/check lifecycle and the registry of running
//     Engines. Engine owns one `llama-server` child process and its assigned
//     127.0.0.1:<port> endpoint.
//   - Chat is performed by POSTing OpenAI-style requests to the engine's
//     baseURL and streaming back SSE chunks. The provider layer continues to
//     consume the same `<-chan Chunk` API as before, so the change is
//     transparent above llamax.
package llamax

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Runtime is the process-wide handle to the llama.cpp install and the set of
// running per-model server engines.
type Runtime interface {
	// EnsureReady installs (if missing) the llama.cpp bundle (libraries +
	// llama-server binary) under LibPath. Safe to call multiple times.
	EnsureReady(ctx context.Context) error

	// LibPath returns the directory holding the llama.cpp bundle.
	LibPath() string

	// LoadModel returns (or starts) an Engine that wraps a `llama-server`
	// subprocess serving the given GGUF file. Engines are cached by absolute
	// model path; concurrent callers asking for the same path share one
	// subprocess.
	LoadModel(path string, opts ModelOptions) (*Engine, error)

	// Close stops every running engine subprocess.
	Close() error
}

// ModelOptions configures how `llama-server` is launched for a model.
type ModelOptions struct {
	// ContextSize maps to `-c` / `--ctx-size`. 0 = let llama-server decide.
	ContextSize uint32
	// BatchSize maps to `-b`. 0 = default.
	BatchSize uint32
	// UbatchSize maps to `-ub`. 0 = default.
	UbatchSize uint32
	// GPULayers maps to `-ngl` / `--n-gpu-layers`. 0 = default (typically all
	// on Metal/CUDA builds, none on CPU builds).
	GPULayers int32
	// Threads maps to `-t`. 0 = llama-server auto.
	Threads int32
	// ExtraArgs are appended verbatim to the llama-server command line, after
	// all auto-derived flags. Use for advanced tuning.
	ExtraArgs []string
}

// ProgressFunc reports library/binary install progress (0..1, plus a status label).
type ProgressFunc func(status string, percent float64)

// runtimeImpl implements Runtime.
type runtimeImpl struct {
	libPath string

	mu       sync.Mutex
	ready    bool
	closed   bool
	engines  map[string]*Engine // canonical model path → engine
	progress ProgressFunc
}

// New creates a Runtime rooted at libPath. If libPath is empty, it falls back
// to NURVIS_LIB → DefaultLibDir().
func New(libPath string, progress ProgressFunc) Runtime {
	if libPath == "" {
		libPath = os.Getenv("NURVIS_LIB")
	}
	if libPath == "" {
		libPath = DefaultLibDir()
	}
	return &runtimeImpl{
		libPath:  libPath,
		engines:  make(map[string]*Engine),
		progress: progress,
	}
}

// DefaultLibDir returns ~/.nurvis/lib/llama (or /tmp/.nurvis/lib/llama if home is unavailable).
func DefaultLibDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), ".nurvis", "lib", "llama")
	}
	return filepath.Join(home, ".nurvis", "lib", "llama")
}

func (r *runtimeImpl) LibPath() string { return r.libPath }

// EnsureReady downloads + extracts the llama.cpp bundle if needed.
//
// Unlike the previous yzma-in-process implementation, no global C library
// initialization is performed — each Engine starts its own subprocess.
func (r *runtimeImpl) EnsureReady(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ready {
		return nil
	}
	if r.closed {
		return errors.New("llamax: runtime closed")
	}

	if err := os.MkdirAll(r.libPath, 0o755); err != nil {
		return fmt.Errorf("llamax: mkdir lib: %w", err)
	}

	if !bundlePresent(r.libPath) {
		slog.Info("llamax: llama.cpp bundle not found, installing", "lib", r.libPath)
		if err := installLibs(ctx, r.libPath, r.progress); err != nil {
			return fmt.Errorf("llamax: install llama.cpp: %w", err)
		}
	}

	if _, err := os.Stat(serverBinaryPath(r.libPath)); err != nil {
		return fmt.Errorf("llamax: llama-server binary missing under %s: %w", r.libPath, err)
	}

	r.ready = true
	slog.Info("llamax: ready", "lib", r.libPath, "server", serverBinaryPath(r.libPath))
	return nil
}

// LoadModel returns a per-model Engine, starting `llama-server` lazily.
func (r *runtimeImpl) LoadModel(path string, opts ModelOptions) (*Engine, error) {
	if path == "" {
		return nil, errors.New("llamax: model path empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("llamax: abs(%q): %w", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("llamax: stat model %q: %w", abs, err)
	}

	r.mu.Lock()
	if !r.ready {
		r.mu.Unlock()
		return nil, errors.New("llamax: runtime not ready (call EnsureReady first)")
	}
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("llamax: runtime closed")
	}
	if eng, ok := r.engines[abs]; ok {
		r.mu.Unlock()
		return eng, nil
	}
	r.mu.Unlock()

	// Starting a llama-server subprocess can take seconds (model load) — do it
	// without holding the runtime lock so other callers aren't blocked.
	eng, err := newEngine(abs, r.libPath, opts)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.engines[abs]; ok {
		// Lost the race: another goroutine started the same model first.
		_ = eng.Close()
		return existing, nil
	}
	if r.closed {
		_ = eng.Close()
		return nil, errors.New("llamax: runtime closed")
	}
	r.engines[abs] = eng
	return eng, nil
}

// Close stops every running engine subprocess.
func (r *runtimeImpl) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	for path, eng := range r.engines {
		if err := eng.Close(); err != nil {
			slog.Warn("llamax: engine close error", "path", path, "err", err)
		}
	}
	r.engines = nil
	r.ready = false
	return nil
}

// bundlePresent reports whether both the core llama shared library and the
// llama-server binary exist in dir.
func bundlePresent(dir string) bool {
	if _, err := os.Stat(serverBinaryPath(dir)); err != nil {
		return false
	}
	for _, lib := range []string{"llama", "ggml", "ggml-base"} {
		p := platformLibName(dir, lib)
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}
