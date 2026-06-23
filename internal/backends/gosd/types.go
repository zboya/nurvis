// Package gosd drives image / video generation through the sd-server HTTP
// service shipped with stable-diffusion.cpp (examples/server/sd-server).
//
// Design parallels internal/llamax:
//   - Runtime is a process-wide singleton that owns the sd-server binary
//     install lifecycle (download into ~/.nurvis/lib/sd) and supervises one
//     sd-server child process per loaded ModelConfig.
//   - Engine wraps a single sd-server instance bound to a fixed set of
//     model paths (diffusion / vae / text encoder / high-noise diffusion)
//     and talks to it over HTTP (POST /v1/images/generations for images
//     and POST /sdcpp/v1/vid_gen + GET /sdcpp/v1/jobs/{id} for videos).
//     Engines are cached by model fingerprint inside Runtime.
//   - The public Generate methods always take primitives and return an
//     Artifact{Path, Kind, MimeType, ...} so callers (agent.MediaLoop) do
//     not need to depend on any sd-server type.
//
// Why HTTP instead of an in-process FFI: sd-server is the upstream-supported
// way to consume stable-diffusion.cpp from a long-running app. Talking to it
// over HTTP gives us a stable, versioned API surface (no C struct layout
// drift), avoids CGO/FFI alignment landmines, and isolates GPU crashes to
// the child process.
package gosd

import (
	"context"
	"errors"
)

// Kind tags the artifact produced by a generation call.
type Kind string

const (
	KindImage Kind = "image"
	KindVideo Kind = "video"
)

// ModelConfig groups the model paths that the underlying sd-server
// instance is launched with. All paths are local absolute paths to .gguf or
// .safetensors files.
//
// Required vs optional varies by model family:
//   - Plain SD/SDXL image generation: DiffusionModelPath (or LegacyModelPath)
//     plus VAEPath are usually enough.
//   - Flux / Wan video: also need TextEncoderPath (T5XXL) and, for Wan T2V,
//     a HighNoiseModelPath companion.
//   - LLM-conditioned image models (e.g. Flux 2 Klein / Z-Image-Turbo) use
//     TextEncoderPath to point at the LLM (Qwen3 / Qwen2.5-VL / Mistral).
type ModelConfig struct {
	DiffusionModelPath string `json:"diffusion_model,omitempty"`
	VAEPath            string `json:"vae,omitempty"`
	TextEncoderPath    string `json:"text_encoder,omitempty"` // T5XXL / LLM
	HighNoiseModelPath string `json:"high_noise,omitempty"`
	LegacyModelPath    string `json:"model_path,omitempty"` // sd1/sd2/sdxl single-file checkpoint
	ClipLPath          string `json:"clip_l,omitempty"`
	ClipGPath          string `json:"clip_g,omitempty"`
	LoraModelDir       string `json:"lora_dir,omitempty"`
	KeepClipOnCPU      bool   `json:"keep_clip_on_cpu,omitempty"`
	DiffusionFlashAttn bool   `json:"flash_attn,omitempty"`
}

// ImageRequest describes one image generation / image edit call.
//
// If InitImagePath is non-empty, the call performs image-to-image (edit /
// variation) instead of pure text-to-image. RefImagePaths supplies optional
// reference images for IP-Adapter / ControlNet style models.
type ImageRequest struct {
	Prompt         string
	NegativePrompt string
	Width          int32 // 0 → 512
	Height         int32 // 0 → 512
	SampleSteps    int32 // 0 → use sd defaults
	CFGScale       float32
	Seed           int64 // -1 → random

	InitImagePath string
	RefImagePaths []string
	StrengthVal   float32 // img2img strength, 0..1, used when InitImagePath set
	OutputPath    string  // absolute path; ".png" appended if missing
}

// VideoRequest describes one video generation call.
type VideoRequest struct {
	Prompt         string
	NegativePrompt string
	Width          int32
	Height         int32
	Frames         int32 // total frames; 0 → engine default
	FPS            int   // mp4 framerate; 0 → 24
	SampleSteps    int32
	HighNoiseSteps int32
	CFGScale       float32
	Seed           int64

	InitImagePath string // optional, for image-to-video
	OutputPath    string // absolute path; container suffix appended if missing
}

// Artifact is the public result of a generation call.
type Artifact struct {
	Kind      Kind   `json:"kind"`
	Path      string `json:"path"`
	MimeType  string `json:"mime_type"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Frames    int    `json:"frames,omitempty"`
	FPS       int    `json:"fps,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// ProgressFunc reports lib install + per-step generation progress.
//   - phase: "downloading" | "extracting" | "ready" | "generating" | "polling" | ...
//   - percent: 0..1
type ProgressFunc func(phase string, percent float64)

// Runtime owns the sd-server binary install lifecycle and the cache of
// loaded Engine instances (one sd-server child process per Engine).
type Runtime interface {
	// EnsureReady downloads + extracts the sd-server binary bundle (if
	// missing) into LibPath. Safe to call multiple times; returns nil
	// immediately once ready.
	EnsureReady(ctx context.Context) error

	// LibPath returns the directory currently used to host the sd-server
	// binary and accompanying shared libraries.
	LibPath() string

	// Ready reports whether EnsureReady has succeeded.
	Ready() bool

	// LoadEngine returns (or builds) an Engine for the given model config.
	// Engines are deduplicated by fingerprint(cfg). Each fresh Engine spawns
	// a sd-server child process bound to a free local port.
	LoadEngine(cfg ModelConfig) (Engine, error)

	// Close terminates all sd-server child processes.
	Close() error
}

// Engine wraps one sd-server child process loaded with a specific
// ModelConfig.
type Engine interface {
	GenerateImage(ctx context.Context, req ImageRequest, onProgress ProgressFunc) (*Artifact, error)
	GenerateVideo(ctx context.Context, req VideoRequest, onProgress ProgressFunc) (*Artifact, error)
	Close() error
}

// ErrNotReady is returned when a generation call is made before the runtime
// has been initialized, e.g. when the sd-server binary is missing.
var ErrNotReady = errors.New("gosd: runtime not ready (call EnsureReady first)")

// ModelResolver resolves a logical model identifier (a bare filename or a
// HuggingFace "owner/repo/file" reference) to an absolute on-disk path.
//
// gosd uses this to translate the model paths embedded in ModelConfig before
// spawning sd-server, so callers can configure agents with the same HF refs
// they already use elsewhere (e.g. modelmgr.Pull keys) instead of having to
// know where files actually live on disk.
//
// Implementations are expected to be idempotent for absolute paths: when
// passed a path that already exists they should return it unchanged. The
// canonical implementation is *modelmgr.manager, which satisfies this
// interface directly.
type ModelResolver interface {
	Resolve(name string) (string, error)
}