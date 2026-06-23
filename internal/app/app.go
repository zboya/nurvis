// Package app handles dependency wiring and lifecycle management for all components.
// See AGENTS.md §16 for the assembly order table.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zboya/nurvis/internal/agent"
	"github.com/zboya/nurvis/internal/backends/gosd"
	"github.com/zboya/nurvis/internal/backends/llamax"
	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/channel"
	channeldispatcher "github.com/zboya/nurvis/internal/channel"
	"github.com/zboya/nurvis/internal/channel/qq"
	"github.com/zboya/nurvis/internal/gateway"
	"github.com/zboya/nurvis/internal/hardware"
	"github.com/zboya/nurvis/internal/mcp"
	"github.com/zboya/nurvis/internal/memory"
	"github.com/zboya/nurvis/internal/modelmgr"
	"github.com/zboya/nurvis/internal/preview"
	"github.com/zboya/nurvis/internal/provider"
	"github.com/zboya/nurvis/internal/scheduler"
	"github.com/zboya/nurvis/internal/skill"
	"github.com/zboya/nurvis/internal/store"
	"github.com/zboya/nurvis/internal/store/repo"
	"github.com/zboya/nurvis/internal/tools"
	"github.com/zboya/nurvis/internal/workspace"
)

// Config holds application configuration, loadable from TOML / environment.
type Config struct {
	DataDir   string `toml:"data_dir"`   // Directory for SQLite + state, defaults to ~/.nurvis
	LibDir    string `toml:"lib_dir"`    // llama.cpp shared libraries dir, defaults to <DataDir>/lib
	ModelsDir string `toml:"models_dir"` // GGUF model store, defaults to <DataDir>/models

	ListenAddr string `toml:"listen_addr"` // Gateway listen address, defaults to :18981
	GWToken    string `toml:"gw_token"`    // Gateway auth token (empty = no auth)

	// SkipRuntime bypasses llama.cpp library install + load. Useful for tests
	// and any setup where inference is delegated to a remote OpenAI-compatible
	// provider.
	SkipRuntime bool `toml:"skip_runtime"`

	// OpenAI-compatible provider (optional). When OpenAIBaseURL is set, the
	// default provider switches to it instead of the local yzma engine.
	OpenAIBaseURL string `toml:"openai_base_url"`
	OpenAIAPIKey  string `toml:"openai_api_key"`
}

// DefaultConfig returns sensible default configuration.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".nurvis")
	return Config{
		DataDir:    dataDir,
		LibDir:     filepath.Join(dataDir, "lib"),
		ModelsDir:  filepath.Join(dataDir, "models"),
		ListenAddr: ":18981",
	}
}

// App aggregates all long-lived components.
type App struct {
	cfg             Config
	store           *store.Store
	bus             bus.Bus
	runtime         llamax.Runtime
	gosdRT          gosd.Runtime
	models          modelmgr.Manager
	agents          *agent.Manager
	sched           *scheduler.Scheduler
	chanDisp        *channeldispatcher.Dispatcher
	mcpMgr          *mcp.Manager
	previewRegistry *preview.Registry
	gw              *gateway.Server
	httpSrv         *http.Server
	hwInfo          hardware.Info
}

// New initializes all components following the assembly order in AGENTS.md §16.
func New(ctx context.Context, cfg Config) (*App, error) {
	a := &App{cfg: cfg}

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("app: mkdir data dir: %w", err)
	}

	// ── 1. store ────────────────────────────────────────────
	slog.Info("app: [1/13] initializing store")
	s, err := store.Open(filepath.Join(cfg.DataDir, "nurvis.db"))
	if err != nil {
		return nil, fmt.Errorf("app: store: %w", err)
	}
	a.store = s

	// ── 2. bus ──────────────────────────────────────────────
	slog.Info("app: [2/13] initializing event bus")
	b := bus.New(2048, 4)
	b.Start(ctx)
	a.bus = b

	// ── 3. hardware probe ───────────────────────────────────
	slog.Info("app: [3/13] probing hardware")
	hw, err := hardware.Probe()
	if err != nil {
		slog.Warn("app: hardware probe failed (using defaults)", "err", err)
	}
	a.hwInfo = hw
	slog.Info("app: hardware ready",
		"ram_gb", fmt.Sprintf("%.1f", hw.RAMGB),
		"platform", hw.Platform,
		"arch", hw.Arch,
	)

	// ── 4. llama.cpp runtime ────────────────────────────────
	slog.Info("app: [4/13] initializing llama.cpp runtime", "lib", cfg.LibDir)
	llamaxDix := filepath.Join(cfg.LibDir, "llama")
	rt := llamax.New(llamaxDix, func(status string, percent float64) {
		b.Publish(bus.TopicRuntimeLibProgress, map[string]any{
			"status":  status,
			"percent": percent,
		})
	})
	a.runtime = rt
	if !cfg.SkipRuntime {
		if err := rt.EnsureReady(ctx); err != nil {
			// Don't abort startup — the user may want to fall back to a
			// remote OpenAI-compatible provider, or fix the lib install
			// from the UI later.
			slog.Warn("app: llama.cpp runtime not ready (continuing)", "err", err)
		}
	}

	// ── 5. modelmgr ─────────────────────────────────────────
	slog.Info("app: [5/13] initializing model manager", "models_dir", cfg.ModelsDir)
	modelRepo := repo.NewModelRepo(s.DB())
	credRepo := repo.NewSiteCredentialRepo(s.DB())
	models := modelmgr.New(cfg.ModelsDir, modelRepo,
		modelmgr.WithTokenProvider(huggingfaceTokenFromCreds(credRepo)),
	)
	a.models = models

	// ── 6. provider ─────────────────────────────────────────
	var prov provider.Provider
	if cfg.OpenAIBaseURL != "" {
		slog.Info("app: [6/13] initializing openai-compatible provider", "base_url", cfg.OpenAIBaseURL)
		prov = provider.NewOpenAI(cfg.OpenAIBaseURL,
			provider.WithName("openai"),
			provider.WithAPIKey(cfg.OpenAIAPIKey),
		)
	} else {
		slog.Info("app: [6/13] initializing llama local provider")
		prov = provider.NewLlama(rt, models)
	}

	// ── 7. tool registry + builtin tools ────────────────────
	slog.Info("app: [7/13] initializing tool registry")
	registry := tools.NewRegistry()
	previewReg := preview.NewRegistry(0)
	a.previewRegistry = previewReg
	localBase := fmt.Sprintf("http://127.0.0.1%s", cfg.ListenAddr)
	chanToolAdapter := &channelToolAdapter{}
	cronToolAdpt := &cronToolAdapter{}
	tools.RegisterAll(registry, previewReg, localBase, credRepo, chanToolAdapter, cronToolAdpt)
	slog.Info("app: builtin tools registered", "count", len(registry.All()))

	// ── 8. mcp / skill ──────────────────────────────────────
	slog.Info("app: [8/13] initializing mcp & skill managers")
	mcpMgr := mcp.NewManager(s.DB(), registry)
	a.mcpMgr = mcpMgr
	if err := mcpMgr.LoadAndConnect(ctx); err != nil {
		slog.Warn("app: mcp load failed", "err", err)
	}

	skillMgr := skill.NewManager(s.DB())
	if err := skillMgr.Load(ctx); err != nil {
		slog.Warn("app: skill load failed", "err", err)
	}
	tools.RegisterSkillTool(registry, skillProviderAdapter{m: skillMgr})

	// ── 9. workspace ────────────────────────────────────────
	slog.Info("app: [9/13] initializing workspace manager")
	wsMgr := workspace.New(s.DB())

	// ── 10. memory ──────────────────────────────────────────
	slog.Info("app: [10/13] initializing memory store")
	_ = memory.New(s.DB())

	// ── 11. agent manager ───────────────────────────────────
	slog.Info("app: [11/13] initializing agent manager")
	agentMgr := agent.NewManager(s.DB(), prov, registry, wsMgr, b, skillMgr)
	a.agents = agentMgr

	// gosd runtime (lazy: do not block startup if libs are missing or the
	// host has no AVX/Metal/Vulkan support). Gets exercised the first time
	// a to-image / to-video agent is dispatched.
	gosdLib := filepath.Join(cfg.LibDir, "sd")
	gosdRT := gosd.New(gosdLib, func(status string, percent float64) {
		b.Publish("gosd.lib.progress", map[string]any{
			"status":  status,
			"percent": percent,
		})
	})
	a.gosdRT = gosdRT
	mediaOutDir := filepath.Join(cfg.DataDir, "outputs")
	// Allow user override via settings.media_output_dir
	settingsRepo := repo.NewSettingsRepo(s.DB())
	if raw, err := settingsRepo.GetRaw(ctx, "media_output_dir"); err == nil && raw != nil {
		var custom string
		if err := json.Unmarshal(raw, &custom); err == nil {
			custom = strings.TrimSpace(custom)
			if custom != "" {
				mediaOutDir = custom
			}
		}
	}
	agentMgr.SetMediaRuntime(gosdRT, &modelMetaAdapter{repo: modelRepo}, mediaOutDir)
	agentMgr.MediaPreviewURL = func(p string) (string, error) {
		dir := filepath.Dir(p)
		token, err := previewReg.Register(dir)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s/v1/preview/%s/%s", localBase, token, filepath.Base(p)), nil
	}

	// ── 12. scheduler + channel dispatcher ──────────────────
	slog.Info("app: [12/13] initializing scheduler & channel dispatcher")
	sched := scheduler.New(s.DB(), b, agentMgr)
	a.sched = sched
	cronToolAdpt.setScheduler(sched)

	chanDisp := channel.NewDispatcher(s.DB(), b, &channelLoopAdapter{agents: agentMgr})
	a.chanDisp = chanDisp
	chanToolAdapter.setDispatcher(chanDisp)
	if err := loadChannels(ctx, s, chanDisp); err != nil {
		slog.Warn("app: load channels failed", "err", err)
	}

	// ── 13. gateway ─────────────────────────────────────────
	slog.Info("app: [13/13] initializing gateway")
	gw := gateway.NewServer(b, cfg.GWToken)
	gw.Use(gateway.DebugLogMiddleware())
	methods := &gateway.Methods{
		Agents:     agentMgr,
		Workspaces: wsMgr,
		Models:     models,
		Runtime:    rt,
		HWInfo:     hw,
		Scheduler:  sched,
		MCPMgr:     mcpMgr,
		SkillMgr:   skillMgr,
		Provider:   prov,
		Registry:   registry,
		Store:      s,
		Bus:        b,

		Sessions:    repo.NewSessionRepo(s.DB()),
		Messages:    repo.NewMessageRepo(s.DB()),
		MCP:         repo.NewMCPRepo(s.DB()),
		Skills:      repo.NewSkillRepo(s.DB()),
		Channels:    repo.NewChannelRepo(s.DB()),
		Builtins:    repo.NewBuiltinToolRepo(s.DB()),
		Settings:    repo.NewSettingsRepo(s.DB()),
		Credentials: credRepo,
		ModelRepo:   modelRepo,
	}
	// Downgrade any leftover non-terminal pull rows from a previous run so the
	// UI does not keep showing them as "downloading" forever. The user can
	// retry from the settings page.
	if err := methods.ModelRepo.MarkInterruptedAll(ctx); err != nil {
		slog.Warn("app: mark interrupted pulls failed", "err", err)
	}
	methods.Register(gw)
	a.gw = gw

	return a, nil
}

// Start launches all background services and listens for HTTP in a background goroutine (non-blocking).
func (a *App) Start(ctx context.Context) error {
	if err := a.chanDisp.Start(ctx); err != nil {
		slog.Warn("app: channel dispatcher start error", "err", err)
	}

	if err := a.sched.Start(ctx); err != nil {
		slog.Warn("app: scheduler start error", "err", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/preview/", preview.NewHandler(a.previewRegistry))
	mux.Handle("/", a.gw)
	a.httpSrv = &http.Server{
		Addr:    a.cfg.ListenAddr,
		Handler: mux,
	}
	go func() {
		slog.Info("gateway: listening", "addr", a.cfg.ListenAddr)
		if err := a.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("gateway: serve error", "err", err)
		}
	}()
	return nil
}

// Run starts all services and blocks until SIGINT/SIGTERM.
func (a *App) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := a.Start(ctx); err != nil {
		return err
	}

	<-ctx.Done()
	slog.Info("app: shutdown signal received")
	return a.Close()
}

// ListenAddr returns the actual Gateway listen address.
func (a *App) ListenAddr() string { return a.cfg.ListenAddr }

// Close shuts down all components in reverse order.
func (a *App) Close() error {
	slog.Info("app: closing components")

	if a.httpSrv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = a.httpSrv.Shutdown(shutCtx)
		cancel()
	}

	if a.chanDisp != nil {
		a.chanDisp.Stop()
	}

	if a.sched != nil {
		a.sched.Stop()
	}

	if a.mcpMgr != nil {
		a.mcpMgr.Close()
	}

	// Tear down llama.cpp before SQLite — engines reference no DB state but
	// the lifecycle ordering matches AGENTS.md §16.
	if a.runtime != nil {
		if err := a.runtime.Close(); err != nil {
			slog.Warn("app: runtime close error", "err", err)
		}
	}

	if a.gosdRT != nil {
		if err := a.gosdRT.Close(); err != nil {
			slog.Warn("app: gosd runtime close error", "err", err)
		}
	}

	if a.store != nil {
		if err := a.store.Close(); err != nil {
			slog.Warn("app: store close error", "err", err)
		}
	}

	slog.Info("app: closed")
	return nil
}

// loadChannels reads enabled channel rows from DB and registers them with the Dispatcher.
func loadChannels(ctx context.Context, s *store.Store, disp *channel.Dispatcher) error {
	channelRepo := repo.NewChannelRepo(s.DB())
	enabled, err := channelRepo.ListEnabledRaw(ctx)
	if err != nil {
		return err
	}
	for _, e := range enabled {
		switch e.Type {
		case "qq":
			ch, err := qq.New(e.ID, e.ConfigJSON)
			if err != nil {
				slog.Warn("app: skip qq channel (config invalid)",
					"channel_id", e.ID, "err", err)
				continue
			}
			disp.Register(ch)
			slog.Info("app: registered qq channel", "channel_id", e.ID)
		case "wechat":
			slog.Info("app: wechat channel skipped (not yet implemented)",
				"channel_id", e.ID)
		default:
			slog.Warn("app: unknown channel type", "type", e.Type, "channel_id", e.ID)
		}
	}
	return nil
}

// skillProviderAdapter adapts *skill.Manager to the tools.SkillProvider interface.
type skillProviderAdapter struct{ m *skill.Manager }

func (a skillProviderAdapter) LookupForAgent(ctx context.Context, agentID, skillName string) (bool, tools.SkillInfo, bool) {
	if a.m == nil {
		return false, tools.SkillInfo{}, false
	}
	granted, info, ok := a.m.LookupForAgent(ctx, agentID, skillName)
	return granted, tools.SkillInfo{
		Name:        info.Name,
		Description: info.Description,
		Body:        info.Body,
		Dir:         info.Dir,
	}, ok
}
