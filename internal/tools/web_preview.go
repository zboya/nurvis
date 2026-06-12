package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zboya/nurvis/internal/preview"
	"github.com/zboya/nurvis/internal/provider"
)

// WebPreview serves a workspace-local static directory over the gateway's
// existing HTTP server and returns a browsable URL.
//
// This is the "static" preview mode: it works for plain HTML projects and
// already-built outputs (e.g. dist/, build/, out/). It does NOT run a dev
// server or build step.
type WebPreview struct {
	registry     *preview.Registry
	localBaseURL string // loopback fallback (e.g. "http://127.0.0.1:18981")
}

// NewWebPreview constructs the tool.
func NewWebPreview(registry *preview.Registry, localBaseURL string) *WebPreview {
	return &WebPreview{registry: registry, localBaseURL: localBaseURL}
}

func (t *WebPreview) Name() string { return "web_preview" }

func (t *WebPreview) Description() string {
	return "Preview a local static web project by serving a directory over HTTP and returning a browsable URL. " +
		"Use for plain HTML sites or already-built outputs (dist/, build/, out/). " +
		"Does NOT run a dev server or build step. The directory must be inside the agent workspace."
}

func (t *WebPreview) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "web_preview",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the static directory to preview (absolute or relative to the workspace). Must contain the site's files (e.g. index.html).",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *WebPreview) Invoke(ctx context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(raw, &args)
	if strings.TrimSpace(args.Path) == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}

	ws := scope.WorkspaceDir
	if ws == "" {
		return &Result{Content: "no workspace available for preview", IsError: true}, nil
	}
	wsClean := filepath.Clean(ws)

	// Resolve target: absolute or relative to workspace.
	dir := args.Path
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(wsClean, dir)
	}
	dir = filepath.Clean(dir)

	// Boundary check: the directory must be inside the workspace.
	if !isWithin(dir, wsClean) {
		return &Result{Content: "path must be inside the workspace", IsError: true}, nil
	}

	info, err := os.Stat(dir)
	if err != nil {
		return &Result{Content: fmt.Sprintf("cannot access path: %v", err), IsError: true}, nil
	}
	if !info.IsDir() {
		return &Result{Content: "path must be a directory (the static site root)", IsError: true}, nil
	}

	token, err := t.registry.Register(dir)
	if err != nil {
		return &Result{Content: fmt.Sprintf("failed to register preview: %v", err), IsError: true}, nil
	}

	urlPath := "/v1/preview/" + token + "/"
	base := strings.TrimRight(t.localBaseURL, "/")
	fullURL := urlPath
	if base != "" {
		fullURL = base + urlPath
	}

	hasIndex := false
	if _, statErr := os.Stat(filepath.Join(dir, "index.html")); statErr == nil {
		hasIndex = true
	}

	var sb strings.Builder
	sb.WriteString("Static preview ready.\n")
	sb.WriteString(fmt.Sprintf("- Preview: [Open preview](%s)\n", fullURL))
	sb.WriteString(fmt.Sprintf("- URL: %s\n", fullURL))
	sb.WriteString(fmt.Sprintf("- Serving directory: %s\n", dir))
	if !hasIndex {
		sb.WriteString("- Note: no index.html found at the root; open a specific file path under the URL.\n")
	}

	return &Result{Content: sb.String()}, nil
}

// isWithin reports whether path is the same as or inside dir.
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
