// Package agent: MediaLoop drives to-image / to-video agents.
//
// Unlike the llama agent loop, media generation is non-streaming and has no
// tool-call orchestration: one user message in, one image or video out.
// The loop emits the same agent.run.* topology so the frontend chat hook can
// render progress with no extra wiring.
//
// Lifecycle:
//  1. publish agent.run.started
//  2. persist user message
//  3. resolve gosd ModelConfig from the agent's options_json
//  4. call gosd.Engine.GenerateImage / GenerateVideo
//  5. expose the local artifact via MediaPreviewURL (preview registry)
//  6. persist the assistant message (content = caption, media_json = artifact)
//  7. publish agent.run.completed with the media payload
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zboya/nurvis/internal/backends/gosd"
	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/store/repo"
)

// mediaLoop is the to-image / to-video execution unit.
type mediaLoop struct {
	agent     *Agent
	sessionID string
	req       ChatRequest
	sessions  *repo.SessionRepo
	messages  *repo.MessageRepo
	bus       bus.Bus
	rt        gosd.Runtime
	outDir    string
	makeURL   func(string) (string, error)
}

func newMediaLoop(
	a *Agent,
	sessionID string,
	req ChatRequest,
	sessions *repo.SessionRepo,
	messages *repo.MessageRepo,
	b bus.Bus,
	rt gosd.Runtime,
	outDir string,
	makeURL func(string) (string, error),
) *mediaLoop {
	if outDir == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = os.TempDir()
		}
		outDir = filepath.Join(home, ".nurvis", "outputs")
	}
	return &mediaLoop{
		agent:     a,
		sessionID: sessionID,
		req:       req,
		sessions:  sessions,
		messages:  messages,
		bus:       b,
		rt:        rt,
		outDir:    outDir,
		makeURL:   makeURL,
	}
}

// Run drives the full media pipeline.
func (l *mediaLoop) Run(ctx context.Context) error {
	l.bus.Publish(bus.TopicAgentRunStarted, map[string]any{
		"agent_id":   l.agent.ID,
		"session_id": l.sessionID,
	})
	l.emitStage("preparing", "start")

	if err := l.ensureSession(ctx); err != nil {
		// Failing to create the session row would leave the message orphaned
		// (not visible in the sidebar history). Surface this as a hard error.
		return l.fail(ctx, fmt.Errorf("create session: %w", err))
	}

	if err := l.persistUserMessage(ctx); err != nil {
		slog.Warn("media-loop: persist user message failed", "err", err)
	}

	if l.rt == nil {
		return l.fail(ctx, errors.New("gosd runtime not initialized; cannot run to-image/to-video agent"))
	}
	if !l.rt.Ready() {
		// Lazy ensure: try to install if the runtime is bound but not ready.
		if err := l.rt.EnsureReady(ctx); err != nil {
			return l.fail(ctx, fmt.Errorf("gosd not ready: %w", err))
		}
	}

	cfg, err := l.resolveModelConfig()
	if err != nil {
		return l.fail(ctx, err)
	}
	eng, err := l.rt.LoadEngine(cfg)
	if err != nil {
		return l.fail(ctx, fmt.Errorf("gosd load engine: %w", err))
	}
	l.emitStage("preparing", "end")

	progress := func(phase string, percent float64) {
		l.bus.Publish(bus.TopicAgentStage, map[string]any{
			"session_id": l.sessionID,
			"stage":      "generating",
			"event":      phase,
			"percent":    percent,
		})
	}

	l.emitStage("generating", "start")
	outFile := l.allocateOutputPath()
	prompt, negPrompt := splitNegativePrompt(l.req.Text)
	initImg := firstImagePath(l.req.Files)

	var (
		artifact *gosd.Artifact
		genErr   error
	)
	switch l.agent.Tag {
	case TagToImage:
		artifact, genErr = eng.GenerateImage(ctx, gosd.ImageRequest{
			Prompt:         prompt,
			NegativePrompt: negPrompt,
			InitImagePath:  initImg,
			OutputPath:     outFile + ".png",
			Width:          int32(optInt(l.agent.Options, "image_width", 512)),
			Height:         int32(optInt(l.agent.Options, "image_height", 512)),
			SampleSteps:    int32(optInt(l.agent.Options, "sample_steps", 0)),
			CFGScale:       float32(optFloat(l.agent.Options, "cfg_scale", 0)),
			Seed:           int64(optInt(l.agent.Options, "seed", 0)),
		}, progress)
	case TagToVideo:
		artifact, genErr = eng.GenerateVideo(ctx, gosd.VideoRequest{
			Prompt:         prompt,
			NegativePrompt: negPrompt,
			InitImagePath:  initImg,
			OutputPath:     outFile + ".mp4",
			Width:          int32(optInt(l.agent.Options, "video_width", 0)),
			Height:         int32(optInt(l.agent.Options, "video_height", 0)),
			Frames:         int32(optInt(l.agent.Options, "video_frames", 0)),
			FPS:            optInt(l.agent.Options, "fps", 0),
			SampleSteps:    int32(optInt(l.agent.Options, "sample_steps", 0)),
			HighNoiseSteps: int32(optInt(l.agent.Options, "high_noise_steps", 0)),
			CFGScale:       float32(optFloat(l.agent.Options, "cfg_scale", 0)),
			Seed:           int64(optInt(l.agent.Options, "seed", 0)),
		}, progress)
	default:
		return l.fail(ctx, fmt.Errorf("media-loop: unsupported tag %q", l.agent.Tag))
	}
	l.emitStage("generating", "end")

	if genErr != nil {
		return l.fail(ctx, genErr)
	}

	url := ""
	if l.makeURL != nil {
		if u, err := l.makeURL(artifact.Path); err == nil {
			url = u
		} else {
			slog.Warn("media-loop: makeURL failed (using raw path)", "err", err)
		}
	}

	media := MediaArtifact{
		Kind:     string(artifact.Kind),
		Name:     filepath.Base(artifact.Path),
		MimeType: artifact.MimeType,
		Path:     artifact.Path,
		URL:      url,
	}

	if err := l.persistAssistantMessage(ctx, prompt, media); err != nil {
		slog.Warn("media-loop: persist assistant message failed", "err", err)
	}
	_ = l.sessions.Touch(ctx, l.sessionID, time.Now())

	l.bus.Publish(bus.TopicAgentRunCompleted, map[string]any{
		"agent_id":   l.agent.ID,
		"session_id": l.sessionID,
		"output":     captionFor(media),
		"media":      []MediaArtifact{media},
	})
	slog.Info("media-loop: done",
		"agent", l.agent.Name,
		"session", l.sessionID,
		"kind", media.Kind,
		"path", media.Path,
	)
	return nil
}

// fail emits agent.run.aborted (parity with llama loop) and returns the error.
func (l *mediaLoop) fail(_ context.Context, err error) error {
	slog.Error("media-loop: aborted", "session", l.sessionID, "err", err)
	l.bus.Publish(bus.TopicAgentRunAborted, map[string]any{
		"session_id": l.sessionID,
		"stage":      "generating",
		"error":      err.Error(),
	})
	return err
}

func (l *mediaLoop) emitStage(stage, event string) {
	l.bus.Publish(bus.TopicAgentStage, map[string]any{
		"session_id": l.sessionID,
		"stage":      stage,
		"event":      event,
	})
}

func (l *mediaLoop) ensureSession(ctx context.Context) error {
	projID := l.req.ProjectID
	if projID == "" {
		projID = l.agent.DefaultProject
	}
	return l.sessions.EnsureCreated(ctx, repo.Session{
		ID:        l.sessionID,
		AgentID:   l.agent.ID,
		ProjectID: projID,
		Channel:   l.req.Channel,
	})
}

func (l *mediaLoop) persistUserMessage(ctx context.Context) error {
	var mediaJSON string
	if len(l.req.Files) > 0 {
		if b, err := json.Marshal(l.req.Files); err == nil {
			mediaJSON = string(b)
		}
	}
	return l.messages.Save(ctx, repo.Message{
		SessionID: l.sessionID,
		Role:      "user",
		Content:   l.req.Text,
		MediaJSON: mediaJSON,
		CreatedAt: time.Now(),
	})
}

func (l *mediaLoop) persistAssistantMessage(ctx context.Context, prompt string, media MediaArtifact) error {
	mediaBytes, _ := json.Marshal([]MediaArtifact{media})
	return l.messages.Save(ctx, repo.Message{
		SessionID: l.sessionID,
		Role:      "assistant",
		Content:   captionFor(media),
		MediaJSON: string(mediaBytes),
		CreatedAt: time.Now().Add(time.Millisecond),
	})
}

// resolveModelConfig pulls the four diffusion model paths out of the agent's
// options_json. See AGENTS.md / chat.send for the contract:
//
//	options.diffusion_model   (required)
//	options.vae               (recommended)
//	options.text_encoder      (T5XXL or LLM, model-family dependent)
//	options.high_noise        (Wan2.2 video only)
//	options.model_path        (single-file legacy SD/SDXL checkpoint)
//	options.lora_dir
//	options.flash_attn        (bool)
//	options.keep_clip_on_cpu  (bool)
func (l *mediaLoop) resolveModelConfig() (gosd.ModelConfig, error) {
	opts := l.agent.Options
	cfg := gosd.ModelConfig{
		LegacyModelPath:    expandUserPath(optString(opts, "model_path")),
		DiffusionModelPath: expandUserPath(optString(opts, "diffusion_model")),
		HighNoiseModelPath: expandUserPath(optString(opts, "high_noise")),
		VAEPath:            expandUserPath(optString(opts, "vae")),
		TextEncoderPath:    expandUserPath(optString(opts, "text_encoder")),
		ClipLPath:          expandUserPath(optString(opts, "clip_l")),
		ClipGPath:          expandUserPath(optString(opts, "clip_g")),
		LoraModelDir:       expandUserPath(optString(opts, "lora_dir")),
		KeepClipOnCPU:      optBool(opts, "keep_clip_on_cpu"),
		DiffusionFlashAttn: optBool(opts, "flash_attn"),
	}
	// agent.Model is the chat-capable LLM (used by to-image / to-video agents
	// to converse with the user). It is NOT a diffusion checkpoint and must
	// not be used to populate DiffusionModelPath — that has to come from
	// options.diffusion_model (or options.model_path for legacy SD/SDXL).
	if cfg.DiffusionModelPath == "" && cfg.LegacyModelPath == "" {
		return cfg, errors.New("agent options missing 'diffusion_model' (or 'model_path')")
	}
	if cfg.TextEncoderPath == "" {
		cfg.TextEncoderPath = expandUserPath(l.agent.Model)
	}
	return cfg, nil
}

func (l *mediaLoop) allocateOutputPath() string {
	_ = os.MkdirAll(l.outDir, 0o755)
	stem := uuid.New().String()
	return filepath.Join(l.outDir, l.sessionID+"-"+stem)
}

// splitNegativePrompt splits a "<prompt> -- <negative>" into two halves.
// Users can pass a negative prompt by suffixing the message with `--` (a
// convention copied from automatic1111). When no `--` is present the entire
// text is the prompt.
func splitNegativePrompt(text string) (string, string) {
	if i := strings.Index(text, "\n--\n"); i >= 0 {
		return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+4:])
	}
	if i := strings.Index(text, " -- "); i >= 0 {
		return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+4:])
	}
	return strings.TrimSpace(text), ""
}

// firstImagePath returns the first attached file whose extension looks like
// a raster image. gosd.ImageFromPNG only handles .png; non-PNG inputs are
// skipped here (a friendlier error fires later from gosd).
func firstImagePath(files []string) string {
	for _, p := range files {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".png":
			return p
		}
	}
	return ""
}

func captionFor(m MediaArtifact) string {
	switch m.Kind {
	case "video":
		return "已生成视频：" + m.Name
	default:
		return "已生成图片：" + m.Name
	}
}

// --- helpers (Options dictionary access) -----------------------------------

// expandUserPath rewrites a leading "~" or "~/..." segment into the current
// user's home directory. C++ children (sd-server, llama-server) don't do
// shell-style tilde expansion, so any user-supplied path written as "~/..."
// would otherwise reach them verbatim and fail with "file not found".
// Non-tilde paths and empty strings are returned unchanged.
func expandUserPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	// "~user/..." form: not supported, return as-is.
	return p
}

func optString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func optBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func optInt(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	switch x := m[key].(type) {
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
	return def
}

func optFloat(m map[string]any, key string, def float64) float64 {
	if m == nil {
		return def
	}
	switch x := m[key].(type) {
	case float32:
		return float64(x)
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return def
}
