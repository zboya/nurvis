// Package tools - edit_file: Claude Code style precise string replacement.
//
// Performs exact literal string replacement on a single file inside the
// workspace. The tool intentionally rejects ambiguous edits: by default
// `old_string` must appear exactly once. Set `replace_all` to true to replace
// every occurrence.
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

// EditFile replaces an exact text segment in a workspace file.
type EditFile struct{}

func (*EditFile) Name() string { return "edit_file" }
func (*EditFile) Description() string {
	return "Edit a file by replacing an exact literal string. By default the old_string must occur exactly once; set replace_all=true to replace every occurrence."
}

func (*EditFile) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name: "edit_file",
		Description: "Replace exact literal text in a workspace file. " +
			"old_string must match the file content character-for-character (whitespace, indentation, newlines included). " +
			"By default it must be unique in the file; set replace_all=true to replace all occurrences.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative path to the file from workspace root.",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "The exact literal text to replace. Must include enough surrounding context to be unique unless replace_all=true.",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "The replacement text. Must differ from old_string.",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "If true, replace every occurrence of old_string. Defaults to false (single, unique replacement).",
				},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
	}
}

func (*EditFile) Invoke(_ context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	_ = json.Unmarshal(raw, &args)

	if args.Path == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}
	if args.OldString == "" {
		return &Result{Content: "old_string is required and cannot be empty", IsError: true}, nil
	}
	if args.OldString == args.NewString {
		return &Result{Content: "new_string must differ from old_string", IsError: true}, nil
	}

	abs, err := safeJoin(scope.WorkspaceDir, args.Path)
	if err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return &Result{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}
	original := string(data)

	count := strings.Count(original, args.OldString)
	if count == 0 {
		return &Result{
			Content: fmt.Sprintf("old_string not found in %s; the literal text must match exactly (whitespace and newlines included)", args.Path),
			IsError: true,
		}, nil
	}
	if !args.ReplaceAll && count > 1 {
		return &Result{
			Content: fmt.Sprintf("old_string occurs %d times in %s; provide more surrounding context to make it unique, or set replace_all=true", count, args.Path),
			IsError: true,
		}, nil
	}

	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(original, args.OldString, args.NewString)
	} else {
		updated = strings.Replace(original, args.OldString, args.NewString, 1)
	}

	// Preserve original file mode if possible.
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(abs); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return &Result{Content: fmt.Sprintf("mkdir error: %v", err), IsError: true}, nil
	}
	if err := os.WriteFile(abs, []byte(updated), mode); err != nil {
		return &Result{Content: fmt.Sprintf("write error: %v", err), IsError: true}, nil
	}

	replaced := 1
	if args.ReplaceAll {
		replaced = count
	}
	return &Result{
		Content: fmt.Sprintf("edited %s: replaced %d occurrence(s), %d -> %d bytes",
			args.Path, replaced, len(original), len(updated)),
		Meta: map[string]any{
			"path":         args.Path,
			"replacements": replaced,
			"old_size":     len(original),
			"new_size":     len(updated),
		},
	}, nil
}
