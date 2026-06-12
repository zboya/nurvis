// Package tools provides ready-to-use built-in tools (fs / exec / http).
// All file operations are constrained via Scope.WorkspaceDir to prevent path traversal.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zboya/nurvis/internal/provider"
)

// ── read_file ──────────────────────────────────────────────────────────────────

// FSRead reads the content of a file within the workspace.
type FSRead struct{}

func (*FSRead) Name() string { return "read_file" }
func (*FSRead) Description() string {
	return "Read the content of a file within the current workspace."
}
func (*FSRead) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "read_file",
		Description: "Read the content of a file within the current workspace.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative path to the file from workspace root.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (*FSRead) Invoke(_ context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(raw, &args)
	rel := args.Path
	if rel == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}
	abs, err := safeJoin(scope.WorkspaceDir, rel)
	if err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return &Result{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}
	return &Result{Content: string(data)}, nil
}

// ── write_file ─────────────────────────────────────────────────────────────────

// FSWrite writes content to a file in the workspace.
type FSWrite struct{}

func (*FSWrite) Name() string        { return "write_file" }
func (*FSWrite) Description() string { return "Write content to a file within the current workspace." }
func (*FSWrite) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "write_file",
		Description: "Write or overwrite a file within the current workspace.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative path to the file from workspace root.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Text content to write.",
				},
				"append": map[string]any{
					"type":        "boolean",
					"description": "If true, append instead of overwrite.",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (*FSWrite) Invoke(_ context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}
	_ = json.Unmarshal(raw, &args)
	rel := args.Path
	content := args.Content
	appendMode := args.Append
	if rel == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}
	abs, err := safeJoin(scope.WorkspaceDir, rel)
	if err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return &Result{Content: fmt.Sprintf("mkdir error: %v", err), IsError: true}, nil
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if appendMode {
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	f, err := os.OpenFile(abs, flag, 0o644)
	if err != nil {
		return &Result{Content: fmt.Sprintf("open error: %v", err), IsError: true}, nil
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return &Result{Content: fmt.Sprintf("write error: %v", err), IsError: true}, nil
	}
	return &Result{Content: fmt.Sprintf("written %d bytes to %s", len(content), rel)}, nil
}

// ── list_files ──────────────────────────────────────────────────────────────────

// FSList lists files and directories within the workspace.
type FSList struct{}

func (*FSList) Name() string { return "list_files" }
func (*FSList) Description() string {
	return "List files and directories within a workspace directory."
}
func (*FSList) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "list_files",
		Description: "List files and directories within a workspace directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative directory path from workspace root. Defaults to '.' (root).",
				},
			},
			"required": []string{},
		},
	}
}

func (*FSList) Invoke(_ context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(raw, &args)
	rel := args.Path
	if rel == "" {
		rel = "."
	}
	abs, err := safeJoin(scope.WorkspaceDir, rel)
	if err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return &Result{Content: fmt.Sprintf("list error: %v", err), IsError: true}, nil
	}
	if len(entries) == 0 {
		return &Result{Content: "no files"}, nil
	}
	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			sb.WriteString(fmt.Sprintf("[DIR]  %s\n", e.Name()))
		} else {
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			sb.WriteString(fmt.Sprintf("[FILE] %s  (%d bytes)\n", e.Name(), size))
		}
	}
	return &Result{Content: sb.String()}, nil
}

// ── delete_file ─────────────────────────────────────────────────────────────────

// FSDelete deletes a file within the workspace (directories are not allowed).
type FSDelete struct{}

func (*FSDelete) Name() string        { return "delete_file" }
func (*FSDelete) Description() string { return "Delete a file within the current workspace." }
func (*FSDelete) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "delete_file",
		Description: "Delete a file within the current workspace.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative path to the file from workspace root.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (*FSDelete) Invoke(_ context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(raw, &args)
	rel := args.Path
	if rel == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}
	abs, err := safeJoin(scope.WorkspaceDir, rel)
	if err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return &Result{Content: fmt.Sprintf("stat error: %v", err), IsError: true}, nil
	}
	if info.IsDir() {
		return &Result{Content: "cannot delete a directory with delete_file; use fs.rmdir", IsError: true}, nil
	}
	if err := os.Remove(abs); err != nil {
		return &Result{Content: fmt.Sprintf("delete error: %v", err), IsError: true}, nil
	}
	return &Result{Content: fmt.Sprintf("deleted %s", rel)}, nil
}

// ── helper ────────────────────────────────────────────────────────────────────

// safeJoin joins a relative path with the workspace root, rejecting ../ traversal.
func safeJoin(wsDir, rel string) (string, error) {
	if wsDir == "" {
		return "", fmt.Errorf("no workspace set for this session")
	}
	abs := filepath.Join(wsDir, rel)
	abs = filepath.Clean(abs)
	if !strings.HasPrefix(abs, filepath.Clean(wsDir)+string(os.PathSeparator)) &&
		abs != filepath.Clean(wsDir) {
		return "", fmt.Errorf("path %q escapes workspace boundary", rel)
	}
	return abs, nil
}
