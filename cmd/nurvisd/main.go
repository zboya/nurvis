// Command nurvisd is the Nurvis daemon entrypoint.
// Usage: nurvisd [--config path/to/config.toml] [--log-level debug|info|warn|error]
//
// Log level priority: --log-level flag > NURVIS_LOG_LEVEL env > default info
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/zboya/nurvis/internal/app"
	"github.com/zboya/nurvis/internal/version"
)

func main() {
	configPath := flag.String("config", "", "path to config file (optional)")
	skipRuntime := flag.Bool("skip-runtime", false, "skip llama.cpp runtime initialization (for testing)")
	logLevel := flag.String("log-level", "", "log level: debug|info|warn|error (overrides NURVIS_LOG_LEVEL env)")
	printVersion := flag.Bool("v", false, "print version and exit")
	flag.Parse()

	if *printVersion {
		fmt.Println(version.String())
		os.Exit(0)
	}

	level := resolveLogLevel(*logLevel)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})))
	slog.Debug("nurvisd: debug logging enabled")

	cfg := app.DefaultConfig()
	cfg.SkipRuntime = *skipRuntime

	// Environment overrides (NURVIS_ prefix).
	if v := os.Getenv("NURVIS_LIB"); v != "" {
		cfg.LibDir = v
	}
	if v := os.Getenv("NURVIS_MODELS_DIR"); v != "" {
		cfg.ModelsDir = v
	}
	if v := os.Getenv("NURVIS_OPENAI_BASE_URL"); v != "" {
		cfg.OpenAIBaseURL = v
	}
	if v := os.Getenv("NURVIS_OPENAI_API_KEY"); v != "" {
		cfg.OpenAIAPIKey = v
	}

	if *configPath != "" {
		// TODO: load TOML config into cfg
		slog.Info("nurvisd: config file support not yet implemented, using defaults")
	}

	slog.Info("nurvisd: version", "version", version.String())
	slog.Info("nurvisd: starting",
		"data_dir", cfg.DataDir,
		"listen", cfg.ListenAddr,
		"lib_dir", cfg.LibDir,
		"models_dir", cfg.ModelsDir,
		"log_level", level.String(),
	)

	ctx := context.Background()
	a, err := app.New(ctx, cfg)
	if err != nil {
		slog.Error("nurvisd: init failed", "err", err)
		os.Exit(1)
	}

	if err := a.Run(ctx); err != nil {
		slog.Error("nurvisd: run error", "err", err)
		os.Exit(1)
	}
}

func resolveLogLevel(flagVal string) slog.Level {
	if flagVal != "" {
		if l, ok := parseLevel(flagVal); ok {
			return l
		}
		slog.Warn("nurvisd: invalid --log-level value, falling back to env/default", "value", flagVal)
	}
	if envVal := os.Getenv("NURVIS_LOG_LEVEL"); envVal != "" {
		if l, ok := parseLevel(envVal); ok {
			return l
		}
		slog.Warn("nurvisd: invalid NURVIS_LOG_LEVEL value, using default info", "value", envVal)
	}
	return slog.LevelInfo
}

func parseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}
