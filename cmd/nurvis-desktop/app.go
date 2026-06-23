package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/zboya/nurvis/internal/app"
	"github.com/zboya/nurvis/internal/version"
)

type service struct {
	app *app.App
	ctx context.Context
}

// ServiceStartup is called by Wails v3 when the application starts.
// It implements the application.Service startup hook.
func (s *service) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.ctx = ctx

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     resolveLogLevel(),
	})))

	slog.Info("nurvis-desktop: version", "version", version.String())

	cfg := app.DefaultConfig()
	applyEnv(&cfg)

	backend, err := app.New(ctx, cfg)
	if err != nil {
		slog.Error("nurvis-desktop: backend init failed", "err", err)
		os.Exit(1)
	}
	if err := backend.Start(ctx); err != nil {
		slog.Error("nurvis-desktop: backend start failed", "err", err)
		os.Exit(1)
	}
	slog.Info("nurvis-desktop: backend started", "gateway", backend.ListenAddr())
	s.app = backend
	return nil
}

// ServiceShutdown is called by Wails v3 when the application is closing.
// It implements the application.Service shutdown hook.
func (s *service) ServiceShutdown() error {
	if s.app != nil {
		if err := s.app.Close(); err != nil {
			slog.Error("nurvis-desktop: backend close error", "err", err)
		} else {
			slog.Info("nurvis-desktop: backend closed")
		}
	}
	return nil
}

func (s *service) GetGatewayAddr() string {
	if s.app != nil {
		return s.app.ListenAddr()
	}
	return ""
}

// SelectDirectory 弹出系统目录选择对话框，返回用户选择的目录路径。
// 用户取消选择时返回空字符串。
func (s *service) SelectDirectory() (string, error) {
	dir, err := application.Get().Dialog.
		OpenFile().
		SetTitle("选择项目目录").
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true).
		ShowHiddenFiles(true).
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return dir, nil
}

// SelectFiles 弹出系统文件选择对话框，支持多选，返回用户选择的文件绝对路径列表。
// 用户取消选择时返回空切片。前端用此 API 给对话窗口添加附件。
func (s *service) SelectFiles() ([]string, error) {
	files, err := application.Get().Dialog.
		OpenFile().
		SetTitle("选择附件").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		ShowHiddenFiles(true).
		PromptForMultipleSelection()
	if err != nil {
		return nil, err
	}
	if files == nil {
		return []string{}, nil
	}
	return files, nil
}

// applyEnv applies NURVIS_ prefixed environment variable overrides
// (kept consistent with nurvisd).
func applyEnv(cfg *app.Config) {
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
	if v := os.Getenv("NURVIS_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
}

// resolveLogLevel 从 NURVIS_LOG_LEVEL 解析日志级别，默认 info。
func resolveLogLevel() slog.Level {
	switch os.Getenv("NURVIS_LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
