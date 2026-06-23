// Package gosd: Runtime singleton — supervises the sd-server binary install
// and one sd-server child process per loaded ModelConfig.
package gosd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// runtimeImpl is the package-private Runtime.
//
// Lifecycle:
//  1. New(libDir, progress) returns an idle handle (no I/O yet).
//  2. EnsureReady checks for an existing sd-server binary in libDir →
//     downloads + extracts the upstream stable-diffusion.cpp release zip
//     if absent.
//  3. LoadEngine is callable after Ready() returns true. Each Engine spawns
//     a sd-server child bound to a free localhost port and waits for it to
//     respond on /sdcpp/v1/capabilities.
//  4. Close terminates every supervised sd-server process.
type runtimeImpl struct {
	libPath  string
	progress ProgressFunc
	// resolver translates HF refs / bare filenames inside ModelConfig into
	// absolute on-disk paths before LoadEngine spawns sd-server. May be nil;
	// when nil, paths are passed through unchanged (only ~ expansion happens).
	resolver ModelResolver

	mu      sync.Mutex
	ready   bool
	closed  bool
	engines map[string]*engineImpl // fingerprint(cfg) → engine
}

// Option configures the Runtime at construction time.
type Option func(*runtimeImpl)

// WithResolver wires a ModelResolver (typically modelmgr.Manager) that gosd
// uses to translate HF refs / bare filenames into absolute paths before
// spawning sd-server. Without a resolver, all ModelConfig path fields are
// passed through to sd-server unchanged (after ~ expansion).
func WithResolver(r ModelResolver) Option {
	return func(rt *runtimeImpl) { rt.resolver = r }
}

// New constructs a Runtime rooted at libPath (or NURVIS_GOSD_LIB / GOSD_DYN_LIB
// / DefaultLibDir() in that order). progress may be nil. Pass WithResolver to
// enable HF-ref → absolute-path translation inside LoadEngine.
func New(libPath string, progress ProgressFunc, opts ...Option) Runtime {
	if libPath == "" {
		libPath = os.Getenv("NURVIS_GOSD_LIB")
	}
	if libPath == "" {
		libPath = os.Getenv("GOSD_DYN_LIB")
	}
	if libPath == "" {
		libPath = DefaultLibDir()
	}
	rt := &runtimeImpl{
		libPath:  libPath,
		progress: progress,
		engines:  make(map[string]*engineImpl),
	}
	for _, o := range opts {
		o(rt)
	}
	return rt
}

// DefaultLibDir returns ~/.nurvis/lib/sd (or /tmp/.nurvis/lib/sd if the home
// directory cannot be resolved).
func DefaultLibDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), ".nurvis", "lib", "sd")
	}
	return filepath.Join(home, ".nurvis", "lib", "sd")
}

func (r *runtimeImpl) LibPath() string { return r.libPath }

func (r *runtimeImpl) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready
}

func (r *runtimeImpl) EnsureReady(ctx context.Context) error {
	r.mu.Lock()
	if r.ready {
		r.mu.Unlock()
		return nil
	}
	if r.closed {
		r.mu.Unlock()
		return errors.New("gosd: runtime closed")
	}
	r.mu.Unlock()

	if err := os.MkdirAll(r.libPath, 0o755); err != nil {
		return fmt.Errorf("gosd: mkdir lib: %w", err)
	}

	want := expectedTag()
	have := installedTag(r.libPath)
	needInstall := !bundlePresent(r.libPath) || (have != "" && have != want)

	if needInstall {
		if have != "" && have != want {
			slog.Warn("gosd: installed bundle is stale, replacing",
				"have", have, "want", want, "lib", r.libPath)
			if err := wipeLibDir(r.libPath); err != nil {
				return fmt.Errorf("gosd: clean stale bundle: %w", err)
			}
		}
		slog.Info("gosd: sd-server binary not found or outdated, installing",
			"lib", r.libPath, "tag", want)
		if err := installBundle(ctx, r.libPath, r.progress); err != nil {
			return fmt.Errorf("gosd: install bundle: %w", err)
		}
	}

	bin, err := serverBinaryPath(r.libPath)
	if err != nil {
		return fmt.Errorf("gosd: locate sd-server: %w", err)
	}
	// Ensure executable bit on unix.
	if runtime.GOOS != "windows" {
		_ = os.Chmod(bin, 0o755)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = true
	slog.Info("gosd: ready", "lib", r.libPath, "binary", bin)
	return nil
}

// LoadEngine returns (or builds) an engine for the given config. Engines are
// cached by fingerprint so two agents with identical model paths share one
// underlying sd-server child process.
//
// Before fingerprinting, every path field in cfg is run through the
// configured ModelResolver (if any). This lets callers pass HuggingFace
// "<owner>/<repo>/<file>" refs (e.g. the same identifiers used by modelmgr)
// instead of absolute on-disk paths.
func (r *runtimeImpl) LoadEngine(cfg ModelConfig) (Engine, error) {
	r.mu.Lock()
	if !r.ready {
		r.mu.Unlock()
		return nil, ErrNotReady
	}
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("gosd: runtime closed")
	}
	resolver := r.resolver
	r.mu.Unlock()

	resolved, err := resolveConfigPaths(cfg, resolver)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	fp := fingerprintConfig(resolved)
	if eng, ok := r.engines[fp]; ok {
		r.mu.Unlock()
		return eng, nil
	}
	libPath := r.libPath
	r.mu.Unlock()

	bin, err := serverBinaryPath(libPath)
	if err != nil {
		return nil, fmt.Errorf("gosd: locate sd-server: %w", err)
	}
	port, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("gosd: pick port: %w", err)
	}
	// Spawning a sd-server child can take seconds (model load) — release the lock.
	eng, err := newEngine(bin, "127.0.0.1", port, resolved)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.engines[fp]; ok {
		_ = eng.Close()
		return existing, nil
	}
	if r.closed {
		_ = eng.Close()
		return nil, errors.New("gosd: runtime closed")
	}
	r.engines[fp] = eng
	return eng, nil
}

// resolveConfigPaths walks every path-bearing field of cfg and passes it
// through resolver.Resolve. Empty fields are skipped. When resolver is nil,
// cfg is returned unchanged. A resolver error on any required field aborts
// the load with a descriptive error; LoraModelDir is treated as best-effort
// since it may legitimately point at a directory that the resolver doesn't
// manage.
func resolveConfigPaths(cfg ModelConfig, resolver ModelResolver) (ModelConfig, error) {
	if resolver == nil {
		return cfg, nil
	}
	resolve := func(field, in string) (string, error) {
		if strings.TrimSpace(in) == "" {
			return in, nil
		}
		out, err := resolver.Resolve(in)
		if err != nil {
			return in, fmt.Errorf("gosd: resolve %s %q: %w", field, in, err)
		}
		return out, nil
	}

	var err error
	if cfg.LegacyModelPath, err = resolve("model_path", cfg.LegacyModelPath); err != nil {
		return cfg, err
	}
	if cfg.DiffusionModelPath, err = resolve("diffusion_model", cfg.DiffusionModelPath); err != nil {
		return cfg, err
	}
	if cfg.HighNoiseModelPath, err = resolve("high_noise", cfg.HighNoiseModelPath); err != nil {
		return cfg, err
	}
	if cfg.VAEPath, err = resolve("vae", cfg.VAEPath); err != nil {
		return cfg, err
	}
	if cfg.TextEncoderPath, err = resolve("text_encoder", cfg.TextEncoderPath); err != nil {
		return cfg, err
	}
	if cfg.ClipLPath, err = resolve("clip_l", cfg.ClipLPath); err != nil {
		return cfg, err
	}
	if cfg.ClipGPath, err = resolve("clip_g", cfg.ClipGPath); err != nil {
		return cfg, err
	}
	// LoraModelDir is best-effort: it's a directory, not a file the resolver
	// is guaranteed to know about. Swallow lookup errors and keep the user
	// value as-is so absolute / external dirs still work.
	if cfg.LoraModelDir != "" {
		if out, lerr := resolver.Resolve(cfg.LoraModelDir); lerr == nil {
			cfg.LoraModelDir = out
		}
	}
	return cfg, nil
}

func (r *runtimeImpl) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	for fp, eng := range r.engines {
		if err := eng.Close(); err != nil {
			slog.Warn("gosd: engine close error", "fp", fp, "err", err)
		}
	}
	r.engines = nil
	r.ready = false
	return nil
}

// fingerprintConfig produces a stable cache key for a ModelConfig.
func fingerprintConfig(c ModelConfig) string {
	return c.LegacyModelPath + "|" + c.DiffusionModelPath + "|" + c.HighNoiseModelPath +
		"|" + c.VAEPath + "|" + c.TextEncoderPath +
		"|" + c.ClipLPath + "|" + c.ClipGPath + "|" + c.LoraModelDir
}

// pickFreePort asks the kernel for a free TCP port by binding to :0.
// Brief race window between Close and child bind is acceptable; sd-server
// will surface the bind error in its log, which we surface to the caller.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// serverBinaryPath returns the absolute path of the sd-server executable
// inside libDir, or an error if not found.
func serverBinaryPath(libDir string) (string, error) {
	name := "sd-server"
	if runtime.GOOS == "windows" {
		name = "sd-server.exe"
	}
	p := filepath.Join(libDir, name)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	// Fallback: walk one level deep (some archives nest binaries under build/bin/).
	var found string
	_ = filepath.Walk(libDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() == name {
			found = path
		}
		return nil
	})
	if found != "" {
		return found, nil
	}
	return "", fmt.Errorf("sd-server binary %q not found under %s", name, libDir)
}
