package llamax

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Defaults tuned for small / medium GGUF models on consumer hardware.
const (
	defaultContextSize uint32 = 32 * 1024

	// startupTimeout caps how long we wait for `llama-server` to become healthy.
	startupTimeout = 90 * time.Second
	// shutdownTimeout caps how long we wait for graceful shutdown before SIGKILL.
	shutdownTimeout = 5 * time.Second
)

// Engine wraps a single `llama-server` subprocess serving one GGUF model.
//
// Engine intentionally has no inference API: callers obtain BaseURL() and
// drive the OpenAI-compatible HTTP endpoint themselves (typically via the
// provider package). This keeps llamax focused on process lifecycle.
type Engine struct {
	path    string
	libDir  string
	port    int
	baseURL string

	cmd     *exec.Cmd
	logFile *os.File      // dedicated log file capturing llama-server stdout+stderr
	logPath string        // absolute path of logFile (for diagnostics)
	done    chan struct{} // closed when the subprocess exits
	closeMu sync.Mutex
	closed  atomic.Bool
}

// Path returns the GGUF model file path served by this engine.
func (e *Engine) Path() string { return e.path }

// Port returns the localhost port `llama-server` is listening on.
func (e *Engine) Port() int { return e.port }

// BaseURL returns the OpenAI-compatible endpoint root WITHOUT any /v1 suffix,
// e.g. "http://127.0.0.1:34521". Callers building an OpenAI client should
// append "/v1" themselves to mirror conventional baseURL handling.
func (e *Engine) BaseURL() string { return e.baseURL }

// OpenAIBaseURL is a convenience helper returning BaseURL()+"/v1".
func (e *Engine) OpenAIBaseURL() string { return e.baseURL + "/v1" }

// LogPath returns the absolute path of the dedicated llama-server log file.
// Empty if logging to file failed (the engine still runs without persistent logs).
func (e *Engine) LogPath() string { return e.logPath }

type ModelPropsResp struct {
	BosToken                  string         `json:"bos_token"`
	BuildInfo                 string         `json:"build_info"`
	ChatTemplate              string         `json:"chat_template"`
	ChatTemplateCaps          map[string]any `json:"chat_template_caps"`
	CorsProxyEnabled          bool           `json:"cors_proxy_enabled"`
	DefaultGenerationSettings struct {
		NCtx   int `json:"n_ctx"`
		Params struct {
			BackendSampling    bool     `json:"backend_sampling"`
			ChatFormat         string   `json:"chat_format"`
			DryAllowedLength   int      `json:"dry_allowed_length"`
			DryBase            float64  `json:"dry_base"`
			DryMultiplier      int      `json:"dry_multiplier"`
			DryPenaltyLastN    int      `json:"dry_penalty_last_n"`
			DynatempExponent   int      `json:"dynatemp_exponent"`
			DynatempRange      int      `json:"dynatemp_range"`
			FrequencyPenalty   int      `json:"frequency_penalty"`
			GenerationPrompt   string   `json:"generation_prompt"`
			IgnoreEos          bool     `json:"ignore_eos"`
			Lora               []any    `json:"lora"`
			MaxTokens          int      `json:"max_tokens"`
			MinKeep            int      `json:"min_keep"`
			MinP               float64  `json:"min_p"`
			Mirostat           int      `json:"mirostat"`
			MirostatEta        float64  `json:"mirostat_eta"`
			MirostatTau        int      `json:"mirostat_tau"`
			NDiscard           int      `json:"n_discard"`
			NKeep              int      `json:"n_keep"`
			NPredict           int      `json:"n_predict"`
			NProbs             int      `json:"n_probs"`
			PostSamplingProbs  bool     `json:"post_sampling_probs"`
			PresencePenalty    int      `json:"presence_penalty"`
			ReasoningFormat    string   `json:"reasoning_format"`
			ReasoningInContent bool     `json:"reasoning_in_content"`
			RepeatLastN        int      `json:"repeat_last_n"`
			RepeatPenalty      int      `json:"repeat_penalty"`
			Samplers           []string `json:"samplers"`
			Seed               int64    `json:"seed"`
			SpeculativeTypes   string   `json:"speculative.types"`
			Stream             bool     `json:"stream"`
			Temperature        int      `json:"temperature"`
			TimingsPerToken    bool     `json:"timings_per_token"`
			TopK               int      `json:"top_k"`
			TopNSigma          int      `json:"top_n_sigma"`
			TopP               float64  `json:"top_p"`
			TypicalP           int      `json:"typical_p"`
			XtcProbability     int      `json:"xtc_probability"`
			XtcThreshold       float64  `json:"xtc_threshold"`
		} `json:"params"`
	} `json:"default_generation_settings"`
	EndpointMetrics bool   `json:"endpoint_metrics"`
	EndpointProps   bool   `json:"endpoint_props"`
	EndpointSlots   bool   `json:"endpoint_slots"`
	EosToken        string `json:"eos_token"`
	IsSleeping      bool   `json:"is_sleeping"`
	MediaMarker     string `json:"media_marker"`
	Modalities      struct {
		Audio  bool `json:"audio"`
		Video  bool `json:"video"`
		Vision bool `json:"vision"`
	} `json:"modalities"`
	ModelAlias string `json:"model_alias"`
	ModelPath  string `json:"model_path"`
	TotalSlots int    `json:"total_slots"`
	UI         bool   `json:"ui"`
	UISettings struct {
	} `json:"ui_settings"`
	Webui         bool `json:"webui"`
	WebuiSettings struct {
	} `json:"webui_settings"`
}

// Props queries `llama-server`'s /props endpoint, returning the runtime view
// of the loaded model: chat_template, n_ctx (active, possibly clamped by -c),
// bos/eos tokens, modalities, and any other server-exposed capability flags.
//
// Use this for authoritative answers about the LIVE model. For offline
// metadata (file on disk, server not yet started) use modelmgr.GGUFMetadata
// via Manager.List() / readGGUF().
func (e *Engine) Props(ctx context.Context) (*ModelPropsResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/props", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llamax: GET /props: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llamax: /props status %d", resp.StatusCode)
	}
	var out ModelPropsResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("llamax: decode /props: %w", err)
	}
	return &out, nil
}

// newEngine starts a `llama-server` subprocess for the model and waits for it
// to become healthy. The caller owns the returned Engine and must Close() it.
func newEngine(modelPath, libDir string, opts ModelOptions) (*Engine, error) {
	port, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("llamax: pick port: %w", err)
	}

	args := buildServerArgs(modelPath, port, opts)
	bin := serverBinaryPath(libDir)

	cmd := exec.Command(bin, args...)
	cmd.Env = withLibraryPath(libDir)

	// Open a dedicated log file for this engine; llama-server stdout/stderr
	// is written directly to it. If creation fails the engine still runs,
	// but the subprocess output is discarded.
	logFile, logPath, logErr := openEngineLogFile(port)
	if logErr != nil {
		slog.Warn("llamax: cannot open engine log file, subprocess output will be discarded", "err", logErr)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	setSysProcAttr(cmd)

	slog.Info("llamax: starting llama-server",
		"bin", bin,
		"args", args,
		"log", logPath,
	)

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, fmt.Errorf("llamax: start llama-server: %w", err)
	}

	eng := &Engine{
		path:    modelPath,
		libDir:  libDir,
		port:    port,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		cmd:     cmd,
		logFile: logFile,
		logPath: logPath,
		done:    make(chan struct{}),
	}

	go func() {
		_ = cmd.Wait()
		if eng.logFile != nil {
			_ = eng.logFile.Close()
		}
		close(eng.done)
		if !eng.closed.Load() {
			slog.Warn("llamax: llama-server exited unexpectedly",
				"port", port, "model", modelPath, "log", eng.logPath)
		}
	}()

	if err := waitHealthy(eng.baseURL, eng.done, startupTimeout); err != nil {
		_ = eng.Close()
		return nil, fmt.Errorf("llamax: server unhealthy (see %s): %w", eng.logPath, err)
	}

	slog.Info("llamax: engine ready", "model", modelPath, "port", port, "url", eng.baseURL, "log", eng.logPath)
	return eng, nil
}

// Close terminates the subprocess (graceful first, then SIGKILL).
func (e *Engine) Close() error {
	e.closeMu.Lock()
	defer e.closeMu.Unlock()
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}

	if runtime.GOOS == "windows" {
		_ = e.cmd.Process.Kill()
	} else {
		_ = e.cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-e.done:
		return nil
	case <-time.After(shutdownTimeout):
		slog.Warn("llamax: SIGTERM timed out, killing", "port", e.port)
		_ = e.cmd.Process.Kill()
		<-e.done
		return nil
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// waitHealthy polls /health until OK, the subprocess exits, or timeout fires.
func waitHealthy(baseURL string, done <-chan struct{}, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := baseURL + "/health"
	client := &http.Client{Timeout: 2 * time.Second}
	backoff := 100 * time.Millisecond
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return errors.New("llama-server exited before becoming healthy")
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(backoff)
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
	return fmt.Errorf("llama-server did not become healthy within %s", timeout)
}

// buildServerArgs derives the llama-server CLI flags for one model.
//
// Flags chosen per the upstream README (tools/server/README.md):
//   - --no-webui   disable the bundled HTML UI (we want HTTP API only)
//   - --jinja      enable proper Jinja chat template + tool-calling support
func buildServerArgs(modelPath string, port int, opts ModelOptions) []string {
	args := []string{
		"-m", modelPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--no-webui",
		"--jinja",
	}
	ctx := opts.ContextSize
	if ctx == 0 {
		ctx = defaultContextSize
	}
	args = append(args, "-c", strconv.FormatUint(uint64(ctx), 10))

	if opts.BatchSize > 0 {
		args = append(args, "-b", strconv.FormatUint(uint64(opts.BatchSize), 10))
	}
	if opts.UbatchSize > 0 {
		args = append(args, "-ub", strconv.FormatUint(uint64(opts.UbatchSize), 10))
	}
	if opts.GPULayers > 0 {
		args = append(args, "-ngl", strconv.FormatInt(int64(opts.GPULayers), 10))
	}
	if opts.Threads > 0 {
		args = append(args, "-t", strconv.FormatInt(int64(opts.Threads), 10))
	}
	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}
	return args
}

// pickFreePort asks the kernel for an unused TCP port and returns it.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// openEngineLogFile creates a per-engine log file under the configured log
// directory. Filename pattern: llama-server-<port>-<YYYYMMDD-HHMMSS>.log.
//
// Directory resolution order:
//  1. $NURVIS_LOG_DIR
//  2. ~/.nurvis/logs
//  3. <os.TempDir>/.nurvis/logs   (home unavailable)
func openEngineLogFile(port int) (*os.File, string, error) {
	dir := os.Getenv("NURVIS_LOG_DIR")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".nurvis", "logs")
		} else {
			dir = filepath.Join(os.TempDir(), ".nurvis", "logs")
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("mkdir log dir %q: %w", dir, err)
	}
	name := fmt.Sprintf("llama-server-%d-%s.log", port, time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open log file %q: %w", path, err)
	}
	// Write a small header for diagnostics.
	_, _ = fmt.Fprintf(f, "=== llama-server log opened at %s (port=%d) ===\n",
		time.Now().Format(time.RFC3339), port)
	return f, path, nil
}
