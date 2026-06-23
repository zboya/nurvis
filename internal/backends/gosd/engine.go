// Package gosd: Engine — supervises one sd-server child process and talks
// to it over HTTP.
//
// Image generation goes through `POST /sdcpp/v1/img_gen` + polling
// `GET /sdcpp/v1/jobs/{id}` (the native sdcpp async endpoint). The request
// body uses the native sdcpp schema directly, so no `<sd_cpp_extra_args>`
// embedding is needed.
//
// Video generation goes through `POST /sdcpp/v1/vid_gen` + polling
// `GET /sdcpp/v1/jobs/{id}` — same job model.
package gosd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// engineImpl owns one sd-server child process bound to a free local port.
type engineImpl struct {
	cfg     ModelConfig
	binPath string
	host    string
	port    int

	cmd     *exec.Cmd
	httpCli *http.Client

	closeOnce sync.Once
	closed    bool

	genMu sync.Mutex // serialize generation calls
}

// newEngine spawns a sd-server child for cfg and waits up to 5 minutes for
// the server to start responding to /sdcpp/v1/capabilities.
func newEngine(binPath, host string, port int, cfg ModelConfig) (*engineImpl, error) {
	if !hasAnyModel(cfg) {
		return nil, errors.New("gosd: model config requires at least one of diffusion_model / model_path")
	}

	args := []string{
		"--listen-ip", host,
		"--listen-port", fmt.Sprintf("%d", port),
	}
	if cfg.LegacyModelPath != "" {
		args = append(args, "-m", cfg.LegacyModelPath)
	}
	if cfg.DiffusionModelPath != "" {
		args = append(args, "--diffusion-model", cfg.DiffusionModelPath)
	}
	if cfg.HighNoiseModelPath != "" {
		args = append(args, "--high-noise-diffusion-model", cfg.HighNoiseModelPath)
	}
	if cfg.VAEPath != "" {
		args = append(args, "--vae", cfg.VAEPath)
	}
	if cfg.ClipLPath != "" {
		args = append(args, "--clip_l", cfg.ClipLPath)
	}
	if cfg.ClipGPath != "" {
		args = append(args, "--clip_g", cfg.ClipGPath)
	}
	if cfg.TextEncoderPath != "" {
		// Same heuristic as the previous FFI implementation: route a t5/umt5
		// encoder to --t5xxl, anything else (typically Qwen-LLM) to --llm.
		lower := strings.ToLower(filepath.Base(cfg.TextEncoderPath))
		switch {
		case strings.Contains(lower, "t5") || strings.Contains(lower, "umt5"):
			args = append(args, "--t5xxl", cfg.TextEncoderPath)
		default:
			args = append(args, "--llm", cfg.TextEncoderPath)
		}
	}
	if cfg.LoraModelDir != "" {
		args = append(args, "--lora-model-dir", cfg.LoraModelDir)
	}
	if cfg.KeepClipOnCPU {
		args = append(args, "--clip-on-cpu")
	}

	// Default runtime flags applied to every sd-server invocation.
	//   --diffusion-fa     enable flash-attention in the diffusion backbone (faster, lower VRAM)
	//   --offload-to-cpu   offload idle weights to CPU RAM to keep VRAM footprint small
	//   -v                 verbose logging (useful for debugging model loading)
	//   --cfg-scale 1.0    safe default; per-request override still wins via the
	//                      <sd_cpp_extra_args> prompt extension.
	// cfg.DiffusionFlashAttn is kept for backwards compatibility but no longer
	// needed since --diffusion-fa is always on.
	args = append(args,
		"--diffusion-fa",
		"--offload-to-cpu",
		"-v",
		"--cfg-scale", "1.0",
	)

	cmd := exec.Command(binPath, args...)
	cmd.Stdout = os.Stderr // sd-server logs to stdout; mirror to our stderr for visibility
	cmd.Stderr = os.Stderr
	slog.Info("start sd-server", "cmd", cmd.String())

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("gosd: start sd-server: %w", err)
	}

	e := &engineImpl{
		cfg:     cfg,
		binPath: binPath,
		host:    host,
		port:    port,
		cmd:     cmd,
		httpCli: &http.Client{Timeout: 0}, // streaming + long generation
	}

	// Wait for /sdcpp/v1/capabilities to respond. Model load can take a
	// while on first launch (cold cache), so poll for up to 30s.
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.waitReady(cmd, waitCtx); err != nil {
		_ = e.Close()
		return nil, fmt.Errorf("gosd: sd-server did not become ready: %w", err)
	}
	return e, nil
}

func (e *engineImpl) baseURL() string {
	return fmt.Sprintf("http://%s:%d", e.host, e.port)
}

// waitReady polls /sdcpp/v1/capabilities until it returns 200 or ctx expires.
func (e *engineImpl) waitReady(c *exec.Cmd, ctx context.Context) error {
	url := e.baseURL() + "/sdcpp/v1/capabilities"
	probe := &http.Client{Timeout: 5 * time.Second}
	done := make(chan struct{})
	go func() {
		c.Wait()
		close(done)
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// If the child has already exited, stop early.
		if e.cmd != nil && e.cmd.ProcessState != nil && e.cmd.ProcessState.Exited() {
			return fmt.Errorf("sd-server exited prematurely (code=%d)", e.cmd.ProcessState.ExitCode())
		}
		resp, err := probe.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-done:
			return errors.New("process exit")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (e *engineImpl) Close() error {
	e.closeOnce.Do(func() {
		e.closed = true
		if e.cmd != nil && e.cmd.Process != nil {
			_ = e.cmd.Process.Kill()
			_, _ = e.cmd.Process.Wait()
		}
	})
	return nil
}

// ───────────────────────────── Image ─────────────────────────────

// imgGenRequest is the native /sdcpp/v1/img_gen request body. Field names
// mirror the schema documented in examples/server/api.md.
type imgGenRequest struct {
	Prompt         string         `json:"prompt"`
	NegativePrompt string         `json:"negative_prompt,omitempty"`
	Width          int32          `json:"width,omitempty"`
	Height         int32          `json:"height,omitempty"`
	Strength       float32        `json:"strength,omitempty"`
	Seed           int64          `json:"seed,omitempty"`
	BatchCount     int            `json:"batch_count,omitempty"`
	InitImage      string         `json:"init_image,omitempty"`
	RefImages      []string       `json:"ref_images,omitempty"`
	SampleParams   map[string]any `json:"sample_params,omitempty"`
	OutputFormat   string         `json:"output_format,omitempty"`
}

// imgGenJobResult mirrors the `result` block of a completed img_gen job.
type imgGenJobResult struct {
	OutputFormat string `json:"output_format"`
	Images       []struct {
		Index   int    `json:"index"`
		B64JSON string `json:"b64_json"`
	} `json:"images"`
}

type imgJobStatusResponse struct {
	ID            string           `json:"id"`
	Kind          string           `json:"kind"`
	Status        string           `json:"status"`
	QueuePosition int              `json:"queue_position"`
	Result        *imgGenJobResult `json:"result"`
	Error         *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (e *engineImpl) GenerateImage(ctx context.Context, req ImageRequest, onProgress ProgressFunc) (*Artifact, error) {
	if e.closed {
		return nil, errors.New("gosd: engine closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.genMu.Lock()
	defer e.genMu.Unlock()

	if onProgress != nil {
		onProgress("generating", 0)
	}

	body := imgGenRequest{
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		Width:          req.Width,
		Height:         req.Height,
		Seed:           req.Seed,
		BatchCount:     1,
		OutputFormat:   "png",
	}
	if req.InitImagePath != "" {
		b64, err := loadImageBase64(req.InitImagePath)
		if err != nil {
			return nil, fmt.Errorf("gosd: load init image: %w", err)
		}
		body.InitImage = b64
		if req.StrengthVal > 0 {
			body.Strength = req.StrengthVal
		} else {
			body.Strength = 0.75
		}
	}
	for _, p := range req.RefImagePaths {
		b64, err := loadImageBase64(p)
		if err != nil {
			return nil, fmt.Errorf("gosd: load ref image %q: %w", p, err)
		}
		body.RefImages = append(body.RefImages, b64)
	}
	sample := map[string]any{}
	if req.SampleSteps > 0 {
		sample["sample_steps"] = req.SampleSteps
	}
	if req.CFGScale > 0 {
		sample["guidance"] = map[string]any{"txt_cfg": req.CFGScale}
	}
	if len(sample) > 0 {
		body.SampleParams = sample
	}

	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL()+"/sdcpp/v1/img_gen", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.httpCli.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gosd: POST /sdcpp/v1/img_gen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gosd: img_gen submit HTTP %d: %s", resp.StatusCode, string(msg))
	}
	var sub jobSubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&sub); err != nil {
		return nil, fmt.Errorf("gosd: decode submit: %w", err)
	}

	// Poll until terminal status.
	pollURL := e.baseURL() + "/sdcpp/v1/jobs/" + sub.ID
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
		req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		pres, perr := e.httpCli.Do(req2)
		if perr != nil {
			return nil, fmt.Errorf("gosd: poll job: %w", perr)
		}
		var st imgJobStatusResponse
		dec := json.NewDecoder(pres.Body)
		_ = dec.Decode(&st)
		_ = pres.Body.Close()
		if onProgress != nil {
			onProgress("polling", 0.5)
		}
		switch st.Status {
		case "completed":
			if st.Result == nil || len(st.Result.Images) == 0 || st.Result.Images[0].B64JSON == "" {
				return nil, errors.New("gosd: completed job has no image result")
			}
			imgBytes, err := base64.StdEncoding.DecodeString(st.Result.Images[0].B64JSON)
			if err != nil {
				return nil, fmt.Errorf("gosd: decode b64: %w", err)
			}
			artifact, err := saveImageBytes(imgBytes, req.OutputPath)
			if err != nil {
				return nil, err
			}
			artifact.Width = int(req.Width)
			artifact.Height = int(req.Height)
			if onProgress != nil {
				onProgress("ready", 1.0)
			}
			return artifact, nil
		case "failed":
			msg := "unknown error"
			if st.Error != nil {
				msg = st.Error.Message
			}
			return nil, fmt.Errorf("gosd: img_gen failed: %s", msg)
		case "cancelled":
			return nil, errors.New("gosd: img_gen cancelled")
		case "queued", "generating", "":
			continue
		default:
			continue
		}
	}
}

// ───────────────────────────── Video ─────────────────────────────

type vidGenRequest struct {
	Prompt                string         `json:"prompt"`
	NegativePrompt        string         `json:"negative_prompt,omitempty"`
	Width                 int32          `json:"width,omitempty"`
	Height                int32          `json:"height,omitempty"`
	Seed                  int64          `json:"seed,omitempty"`
	VideoFrames           int32          `json:"video_frames,omitempty"`
	FPS                   int            `json:"fps,omitempty"`
	InitImage             string         `json:"init_image,omitempty"`
	SampleParams          map[string]any `json:"sample_params,omitempty"`
	HighNoiseSampleParams map[string]any `json:"high_noise_sample_params,omitempty"`
	OutputFormat          string         `json:"output_format,omitempty"`
}

type jobSubmitResponse struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	PollURL string `json:"poll_url"`
}

type jobStatusResponse struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	QueuePosition int    `json:"queue_position"`
	Result        *struct {
		B64JSON      string `json:"b64_json"`
		MimeType     string `json:"mime_type"`
		OutputFormat string `json:"output_format"`
		FPS          int    `json:"fps"`
		FrameCount   int    `json:"frame_count"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (e *engineImpl) GenerateVideo(ctx context.Context, req VideoRequest, onProgress ProgressFunc) (*Artifact, error) {
	if e.closed {
		return nil, errors.New("gosd: engine closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.genMu.Lock()
	defer e.genMu.Unlock()

	if onProgress != nil {
		onProgress("generating", 0)
	}

	body := vidGenRequest{
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		Width:          req.Width,
		Height:         req.Height,
		Seed:           req.Seed,
		VideoFrames:    req.Frames,
		FPS:            req.FPS,
		OutputFormat:   "webm",
	}
	if req.InitImagePath != "" {
		b64, err := loadImageBase64(req.InitImagePath)
		if err != nil {
			return nil, fmt.Errorf("gosd: load init image: %w", err)
		}
		body.InitImage = b64
	}
	sample := map[string]any{}
	if req.SampleSteps > 0 {
		sample["sample_steps"] = req.SampleSteps
	}
	if req.CFGScale > 0 {
		sample["guidance"] = map[string]any{"txt_cfg": req.CFGScale}
	}
	if len(sample) > 0 {
		body.SampleParams = sample
	}
	if req.HighNoiseSteps > 0 {
		body.HighNoiseSampleParams = map[string]any{"sample_steps": req.HighNoiseSteps}
	}

	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL()+"/sdcpp/v1/vid_gen", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.httpCli.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gosd: POST /sdcpp/v1/vid_gen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gosd: vid_gen submit HTTP %d: %s", resp.StatusCode, string(msg))
	}
	var sub jobSubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&sub); err != nil {
		return nil, fmt.Errorf("gosd: decode submit: %w", err)
	}

	// Poll until terminal status.
	pollURL := e.baseURL() + "/sdcpp/v1/jobs/" + sub.ID
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		pres, perr := e.httpCli.Do(req2)
		if perr != nil {
			return nil, fmt.Errorf("gosd: poll job: %w", perr)
		}
		var st jobStatusResponse
		dec := json.NewDecoder(pres.Body)
		_ = dec.Decode(&st)
		_ = pres.Body.Close()
		if onProgress != nil {
			onProgress("polling", 0.5)
		}
		switch st.Status {
		case "completed":
			if st.Result == nil || st.Result.B64JSON == "" {
				return nil, errors.New("gosd: completed job has no result")
			}
			vidBytes, err := base64.StdEncoding.DecodeString(st.Result.B64JSON)
			if err != nil {
				return nil, fmt.Errorf("gosd: decode video b64: %w", err)
			}
			ext := outputExt(st.Result.OutputFormat, st.Result.MimeType)
			artifact, err := saveVideoBytes(vidBytes, req.OutputPath, ext, st.Result.MimeType)
			if err != nil {
				return nil, err
			}
			artifact.Frames = st.Result.FrameCount
			artifact.FPS = st.Result.FPS
			if artifact.FPS == 0 {
				artifact.FPS = req.FPS
			}
			artifact.Width = int(req.Width)
			artifact.Height = int(req.Height)
			if onProgress != nil {
				onProgress("ready", 1.0)
			}
			return artifact, nil
		case "failed":
			msg := "unknown error"
			if st.Error != nil {
				msg = st.Error.Message
			}
			return nil, fmt.Errorf("gosd: vid_gen failed: %s", msg)
		case "cancelled":
			return nil, errors.New("gosd: vid_gen cancelled")
		case "queued", "generating", "":
			continue
		default:
			// Unknown status — keep polling defensively.
			continue
		}
	}
}

func outputExt(format, mime string) string {
	switch strings.ToLower(format) {
	case "webm":
		return ".webm"
	case "webp":
		return ".webp"
	case "avi":
		return ".avi"
	}
	switch mime {
	case "video/webm":
		return ".webm"
	case "image/webp":
		return ".webp"
	case "video/x-msvideo":
		return ".avi"
	}
	return ".webm"
}

// ───────────────────────────── helpers ─────────────────────────────

func hasAnyModel(c ModelConfig) bool {
	return c.LegacyModelPath != "" || c.DiffusionModelPath != ""
}

// loadImageBase64 reads imgPath and returns its standard base64 string.
// sd-server accepts either raw base64 or a `data:image/...;base64,` URL.
func loadImageBase64(imgPath string) (string, error) {
	b, err := os.ReadFile(imgPath)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// saveImageBytes writes raw PNG bytes to outputPath (or a generated path if
// empty) and returns a populated Artifact.
func saveImageBytes(data []byte, outputPath string) (*Artifact, error) {
	if outputPath == "" {
		outputPath = filepath.Join(os.TempDir(), fmt.Sprintf("gosd-%d.png", time.Now().UnixNano()))
	}
	if !strings.HasSuffix(strings.ToLower(outputPath), ".png") {
		outputPath += ".png"
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, fmt.Errorf("gosd: mkdir output: %w", err)
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("gosd: write png %q: %w", outputPath, err)
	}
	st, _ := os.Stat(outputPath)
	a := &Artifact{
		Kind:     KindImage,
		Path:     outputPath,
		MimeType: "image/png",
	}
	if st != nil {
		a.SizeBytes = st.Size()
	}
	return a, nil
}

// saveVideoBytes writes the encoded container payload to outputPath. The
// extension is chosen based on the server's output_format; sd-server's
// vid_gen returns .webm/.webp/.avi rather than .mp4 — callers should treat
// MimeType as the source of truth.
func saveVideoBytes(data []byte, outputPath, ext, mime string) (*Artifact, error) {
	if outputPath == "" {
		outputPath = filepath.Join(os.TempDir(), fmt.Sprintf("gosd-%d%s", time.Now().UnixNano(), ext))
	}
	// Replace any caller-supplied stale extension (.mp4) with the actual one.
	if filepath.Ext(outputPath) == "" {
		outputPath += ext
	} else if !strings.EqualFold(filepath.Ext(outputPath), ext) {
		outputPath = strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ext
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, fmt.Errorf("gosd: mkdir output: %w", err)
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("gosd: write video %q: %w", outputPath, err)
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	st, _ := os.Stat(outputPath)
	a := &Artifact{
		Kind:     KindVideo,
		Path:     outputPath,
		MimeType: mime,
	}
	if st != nil {
		a.SizeBytes = st.Size()
	}
	return a, nil
}
