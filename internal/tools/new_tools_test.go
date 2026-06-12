package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestGlob_Doublestar(t *testing.T) {
	g := &Glob{}
	args, _ := json.Marshal(map[string]any{"pattern": "internal/tools/**/*.go", "limit": 100})
	res, err := g.Invoke(context.Background(), args, Scope{WorkspaceDir: "../.."})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "internal/tools/glob.go") {
		t.Fatalf("expected glob.go in result, got:\n%s", res.Content)
	}
}

func TestGrep_Content(t *testing.T) {
	g := &Grep{}
	args, _ := json.Marshal(map[string]any{
		"pattern": "func \\(\\*Glob\\) Name",
		"include": "internal/tools/**/*.go",
	})
	res, err := g.Invoke(context.Background(), args, Scope{WorkspaceDir: "../.."})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "internal/tools/glob.go") {
		t.Fatalf("expected match line, got:\n%s", res.Content)
	}
}

func TestEditFile_UniqueRequired(t *testing.T) {
	tmp := t.TempDir()
	// Create a file with two occurrences.
	path := tmp + "/a.txt"
	if err := writeStringFile(path, "hello\nhello\n"); err != nil {
		t.Fatal(err)
	}
	e := &EditFile{}
	args, _ := json.Marshal(map[string]any{
		"path":       "a.txt",
		"old_string": "hello",
		"new_string": "world",
	})
	res, _ := e.Invoke(context.Background(), args, Scope{WorkspaceDir: tmp})
	if !res.IsError {
		t.Fatalf("expected ambiguity error, got: %s", res.Content)
	}
	args2, _ := json.Marshal(map[string]any{
		"path":        "a.txt",
		"old_string":  "hello",
		"new_string":  "world",
		"replace_all": true,
	})
	res2, _ := e.Invoke(context.Background(), args2, Scope{WorkspaceDir: tmp})
	if res2.IsError {
		t.Fatalf("replace_all failed: %s", res2.Content)
	}
}

func writeStringFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
