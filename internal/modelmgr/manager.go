// Package modelmgr manages local GGUF model files and downloads from HuggingFace.
//
// In the new yzma-based architecture nurvis no longer manages an external
// process; this package therefore only deals with files on disk:
//
//   - List installed models (sourced from the models registry table).
//   - Resolve a logical model name (HF "repo/file" or bare filename) to an
//     absolute path.
//   - Pull a model from HuggingFace via https://huggingface.co/<repo>/resolve/main/<file>.
//   - Delete a model file.
//   - Surface a curated recommendation library.
package modelmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zboya/nurvis/internal/store/repo"
)

// PullProgress reports a single download progress event.
//
// Status values mirror what the gateway forwards to the frontend:
//
//	"resolving"   metadata lookup before bytes start flowing
//	"downloading" stream in progress
//	"verifying"   final size check
//	"success"     file present and verified
//	"error"       failed; Error carries the message
type PullProgress struct {
	Model   string  `json:"model"`
	Status  string  `json:"status"`
	Total   int64   `json:"total,omitempty"`
	Current int64   `json:"current,omitempty"`
	Percent float64 `json:"percent,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// ModelInfo describes a single installed GGUF model. It is now sourced from
// the models registry rather than the filesystem walk, so HuggingFace
// metadata (pipeline_tag, modalities, tags) is included alongside GGUF data.
type ModelInfo struct {
	Name       string    `json:"name"`           // canonical display name (file basename)
	Repo       string    `json:"repo,omitempty"` // HF repo path
	File       string    `json:"file"`           // GGUF filename
	LocalPath  string    `json:"local_path"`
	SizeBytes  int64     `json:"size_bytes"`
	ModifiedAt time.Time `json:"modified_at"`

	// HuggingFace-derived metadata.
	PipelineTag string   `json:"pipeline_tag,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Modalities  []string `json:"modalities,omitempty"` // text / image / audio / video
}

// Manager is the surface area used by the rest of the codebase.
type Manager interface {
	// Dir returns the root directory containing all models (e.g. ~/.nurvis/models).
	Dir() string

	// List enumerates installed models. The list is served from the
	// models table (rows with status='success'); the filesystem is only
	// consulted as a fallback when no repo is wired in.
	List(ctx context.Context) ([]ModelInfo, error)

	// Resolve maps a logical model name to its absolute on-disk path.
	// Accepts either a bare filename (e.g. "gemma-3-1b-it-Q4_K_M.gguf") or
	// a HF reference (e.g. "ggml-org/gemma-3-1b-it-GGUF/gemma-3-1b-it-Q4_K_M.gguf").
	Resolve(name string) (string, error)

	// Pull downloads a GGUF file from HuggingFace. The returned channel emits
	// progress events and is closed when the download terminates. As a side
	// effect, model metadata is persisted into the models table.
	Pull(ctx context.Context, ref ModelRef) (<-chan PullProgress, error)

	// Delete removes a local GGUF file. Accepts the same name forms as Resolve.
	Delete(ctx context.Context, name string) error

	// ListLibrary get models from hf
	ListLibrary(ctx context.Context, search string, limit int) ([]Model, error)

	// ListRepoFiles lists files in a HuggingFace model repo (main branch).
	ListRepoFiles(ctx context.Context, repo string) ([]RepoFile, error)
}

// ModelRef identifies a model on HuggingFace.
type ModelRef struct {
	Repo string `json:"repo"` // e.g. "ggml-org/gemma-3-1b-it-GGUF"
	File string `json:"file"` // e.g. "gemma-3-1b-it-Q4_K_M.gguf"
}

// String renders a ref as "repo/file", which is also the canonical PullRequest
// model name accepted by the gateway.
func (r ModelRef) String() string {
	if r.Repo == "" {
		return r.File
	}
	return r.Repo + "/" + r.File
}

// ParseRef accepts "repo_owner/repo_name/file.gguf" or just "file.gguf".
func ParseRef(s string) (ModelRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ModelRef{}, errors.New("modelmgr: empty model ref")
	}
	// HF repo names are <owner>/<repo>, so a full ref has at least 3 segments.
	parts := strings.Split(s, "/")
	if len(parts) >= 3 {
		return ModelRef{
			Repo: strings.Join(parts[:len(parts)-1], "/"),
			File: parts[len(parts)-1],
		}, nil
	}
	if len(parts) == 1 {
		return ModelRef{File: parts[0]}, nil
	}
	return ModelRef{}, fmt.Errorf("modelmgr: invalid model ref %q (expected file or repo/file)", s)
}

// manager is the file-system Manager implementation.
type manager struct {
	dir       string
	pull      *repo.ModelRepo // optional; when nil, List falls back to filesystem walk
	tokenFunc TokenProviderFunc
}

// TokenProviderFunc returns a HuggingFace access token (or empty string) for
// authenticating downloads and metadata calls. It is consulted lazily on each
// HTTP request so the user can update the token via the credentials UI
// without restarting the daemon.
type TokenProviderFunc func(ctx context.Context) string

// Option configures the Manager at construction time.
type Option func(*manager)

// WithTokenProvider wires a HuggingFace token provider (e.g. backed by the
// site credentials store). When set, the returned token takes precedence over
// the HF_TOKEN environment variable.
func WithTokenProvider(fn TokenProviderFunc) Option {
	return func(m *manager) { m.tokenFunc = fn }
}

// New creates a Manager rooted at dir. If dir is empty, falls back to
// $NURVIS_MODELS_DIR or ~/.nurvis/models. The pull repo is optional but
// recommended: with it List/Pull share the models table as the
// installed-model registry.
func New(dir string, pullRepo *repo.ModelRepo, opts ...Option) Manager {
	if dir == "" {
		dir = os.Getenv("NURVIS_MODELS_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			dir = filepath.Join(home, ".nurvis", "models")
		} else {
			dir = filepath.Join(os.TempDir(), ".nurvis", "models")
		}
	}
	m := &manager{dir: dir, pull: pullRepo}
	for _, o := range opts {
		o(m)
	}
	return m
}

// resolveHFToken returns the HuggingFace token to use for outbound requests.
// Precedence: configured TokenProvider (e.g. credentials store) > HF_TOKEN env.
func (m *manager) resolveHFToken(ctx context.Context) string {
	if m.tokenFunc != nil {
		if tok := strings.TrimSpace(m.tokenFunc(ctx)); tok != "" {
			return tok
		}
	}
	return strings.TrimSpace(os.Getenv("HF_TOKEN"))
}

func (m *manager) Dir() string { return m.dir }

// List returns the set of installed models. Source of truth is the
// models table (rows with status='success'); each row already carries
// HF metadata + GGUF metadata + on-disk path captured at download time, so
// no filesystem walk or GGUF reparse is required on the read path.
//
// If a row references a file that no longer exists on disk it is skipped.
func (m *manager) List(ctx context.Context) ([]ModelInfo, error) {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return nil, fmt.Errorf("modelmgr: mkdir: %w", err)
	}
	if m.pull == nil {
		return nil, fmt.Errorf("modelmgr: pull repo not wired; cannot list installed models")
	}
	rows, err := m.pull.ListSuccess(ctx)
	if err != nil {
		return nil, fmt.Errorf("modelmgr: list installed: %w", err)
	}
	out := make([]ModelInfo, 0, len(rows))
	for _, r := range rows {
		path := r.LocalPath
		if path == "" {
			// Older rows may not have local_path filled; reconstruct it
			// from repo/file under the models dir.
			path = filepath.Join(m.dir, filepath.FromSlash(r.Repo), r.File)
		}
		var modTime time.Time
		size := r.SizeBytes
		if info, statErr := os.Stat(path); statErr == nil {
			modTime = info.ModTime()
			if size == 0 {
				size = info.Size()
			}
		} else {
			// File is missing on disk: skip so the UI doesn't lie.
			continue
		}
		out = append(out, ModelInfo{
			Name:        r.File,
			Repo:        r.Repo,
			File:        r.File,
			LocalPath:   path,
			SizeBytes:   size,
			ModifiedAt:  modTime,
			PipelineTag: r.PipelineTag,
			Tags:        r.Tags,
			Modalities:  r.Modalities,
		})
	}
	return out, nil
}

func (m *manager) Resolve(name string) (string, error) {
	// Tilde expansion: shell-style "~/..." paths don't get expanded by Go's
	// os.Stat or by C++ child processes (sd-server, llama-server) downstream,
	// so normalise them up front. Users routinely paste "~/.cache/..." style
	// paths into the agent form.
	if expanded := expandUserPath(name); expanded != name {
		name = expanded
	}
	// Fast path: if the caller already passed an absolute path that exists
	// on disk, return it as-is. This lets users point to GGUF / safetensors
	// files outside the managed models dir (e.g. via the agent form's
	// "browse local file" picker).
	if trimmed := strings.TrimSpace(name); filepath.IsAbs(trimmed) {
		if _, err := os.Stat(trimmed); err == nil {
			return trimmed, nil
		}
	}

	ref, err := ParseRef(name)
	if err != nil {
		return "", err
	}
	candidates := []string{}
	if ref.Repo != "" {
		candidates = append(candidates, filepath.Join(m.dir, filepath.FromSlash(ref.Repo), ref.File))
	}
	// Bare filename: search anywhere under the models dir.
	candidates = append(candidates, filepath.Join(m.dir, ref.File))

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// Last resort: walk to find any file matching the basename.
	var found string
	_ = filepath.Walk(m.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.EqualFold(info.Name(), ref.File) {
			found = path
			return errStopWalk
		}
		return nil
	})
	if found != "" {
		return found, nil
	}
	return "", fmt.Errorf("modelmgr: model %q not found under %s", name, m.dir)
}

func (m *manager) Delete(ctx context.Context, name string) error {
	path, err := m.Resolve(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("modelmgr: delete: %w", err)
	}
	// Best-effort: remove now-empty parent dirs up to m.dir.
	dir := filepath.Dir(path)
	for dir != m.dir && strings.HasPrefix(dir, m.dir) {
		if err := os.Remove(dir); err != nil {
			break // not empty, stop
		}
		dir = filepath.Dir(dir)
	}
	// Drop the registry row so List stops surfacing this model.
	if m.pull != nil {
		ref, perr := ParseRef(name)
		if perr == nil {
			_ = m.pull.Delete(ctx, ref.String())
		}
	}
	return nil
}

// errStopWalk is a sentinel used to short-circuit filepath.Walk.
var errStopWalk = errors.New("modelmgr: stop walk")

// expandUserPath rewrites a leading "~" or "~/..." segment into the current
// user's home directory. C++ children (sd-server, llama-server) don't do
// shell-style tilde expansion, so any "~"-prefixed user path would otherwise
// fail downstream with "file not found". Non-tilde paths and empty strings
// are returned unchanged.
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
	return p
}