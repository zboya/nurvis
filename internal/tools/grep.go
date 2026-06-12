// Package tools - grep: fast regex search across the workspace.
//
// Implementation notes (performance):
//   - File discovery is done by filepath.WalkDir which streams readdir results
//     and is significantly faster than filepath.Walk (no per-entry os.Lstat).
//   - Search is done in parallel by a worker pool sized to GOMAXPROCS.
//   - Each file is scanned with bufio.Scanner using a reusable buffer; we never
//     load the whole file unless its size is small enough to fit in memory.
//   - Binary files are detected by sniffing the first 512 bytes for NUL.
//   - We compile the pattern once and reuse the *regexp.Regexp across workers.
//   - Heavy directories (.git, node_modules, vendor, ...) are pruned by default.
//
// The output format mimics ripgrep / Claude Code's `grep` tool: one match per
// line, formatted as `path:lineno:matched-line`. Output mode `files_with_matches`
// returns just unique paths.
package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/zboya/nurvis/internal/provider"
)

// Grep performs regex search across files in the workspace.
type Grep struct{}

func (*Grep) Name() string { return "grep" }
func (*Grep) Description() string {
	return "Search workspace files with a regular expression. High-performance, ripgrep-like; supports include glob, exclude glob, case-insensitive, and several output modes."
}

func (*Grep) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "grep",
		Description: "Search the workspace for a regular expression (Go RE2 syntax). Returns matching lines (or filenames). Honors common ignore directories.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regular expression (Go RE2 syntax).",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Optional sub-directory (relative to workspace) to search inside. Defaults to workspace root.",
				},
				"include": map[string]any{
					"type":        "string",
					"description": "Glob (with `**`) for files to include, e.g. `**/*.go`. If unset, all non-binary files are scanned.",
				},
				"exclude": map[string]any{
					"type":        "string",
					"description": "Glob (with `**`) for files to skip.",
				},
				"case_insensitive": map[string]any{
					"type":        "boolean",
					"description": "If true, the pattern matches case-insensitively (prepends (?i)).",
				},
				"output_mode": map[string]any{
					"type":        "string",
					"description": "`content` (default, lines with path:line:match), `files_with_matches` (unique file paths), `count` (path:count).",
				},
				"include_hidden": map[string]any{
					"type":        "boolean",
					"description": "If true, also search hidden directories and node_modules/vendor.",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Max number of result lines/files. Defaults to 500.",
				},
				"context_before": map[string]any{
					"type":        "integer",
					"description": "Lines of context before each match (content mode only). Defaults to 0.",
				},
				"context_after": map[string]any{
					"type":        "integer",
					"description": "Lines of context after each match (content mode only). Defaults to 0.",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (*Grep) Invoke(ctx context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		Include         string `json:"include"`
		Exclude         string `json:"exclude"`
		CaseInsensitive bool   `json:"case_insensitive"`
		OutputMode      string `json:"output_mode"`
		IncludeHidden   bool   `json:"include_hidden"`
		MaxResults      int    `json:"max_results"`
		ContextBefore   int    `json:"context_before"`
		ContextAfter    int    `json:"context_after"`
	}
	_ = json.Unmarshal(raw, &args)

	if args.Pattern == "" {
		return &Result{Content: "pattern is required", IsError: true}, nil
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 500
	}
	if args.OutputMode == "" {
		args.OutputMode = "content"
	}

	rootRel := args.Path
	if rootRel == "" {
		rootRel = "."
	}
	root, err := safeJoin(scope.WorkspaceDir, rootRel)
	if err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}

	pattern := args.Pattern
	if args.CaseInsensitive && !strings.HasPrefix(pattern, "(?i)") {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return &Result{Content: fmt.Sprintf("invalid regex: %v", err), IsError: true}, nil
	}

	var includeM, excludeM *globMatcher
	if args.Include != "" {
		includeM, err = compileGlob(args.Include)
		if err != nil {
			return &Result{Content: fmt.Sprintf("invalid include pattern: %v", err), IsError: true}, nil
		}
	}
	if args.Exclude != "" {
		excludeM, err = compileGlob(args.Exclude)
		if err != nil {
			return &Result{Content: fmt.Sprintf("invalid exclude pattern: %v", err), IsError: true}, nil
		}
	}

	files, err := collectGrepTargets(ctx, root, includeM, excludeM, args.IncludeHidden)
	if err != nil {
		return &Result{Content: fmt.Sprintf("walk error: %v", err), IsError: true}, nil
	}

	mode := args.OutputMode
	switch mode {
	case "content", "files_with_matches", "count":
	default:
		return &Result{Content: fmt.Sprintf("unknown output_mode %q", mode), IsError: true}, nil
	}

	results := runGrep(ctx, root, files, re, mode, args.ContextBefore, args.ContextAfter, args.MaxResults)

	if len(results.lines) == 0 {
		return &Result{Content: "no matches"}, nil
	}

	var sb strings.Builder
	for _, line := range results.lines {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if results.truncated {
		sb.WriteString(fmt.Sprintf("... (truncated at %d results)\n", args.MaxResults))
	}
	return &Result{
		Content: sb.String(),
		Meta: map[string]any{
			"files_scanned":  len(files),
			"matches":        len(results.lines),
			"truncated":      results.truncated,
			"files_matched":  results.filesMatched,
			"output_mode":    mode,
		},
	}, nil
}

// ── target collection ─────────────────────────────────────────────────────────

func collectGrepTargets(ctx context.Context, root string, include, exclude *globMatcher, includeHidden bool) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		base := d.Name()

		if d.IsDir() {
			if !includeHidden {
				if strings.HasPrefix(base, ".") {
					return fs.SkipDir
				}
				if _, skip := commonSkipDirs[base]; skip {
					return fs.SkipDir
				}
			}
			if include != nil && !include.couldMatchDir(relSlash) {
				return fs.SkipDir
			}
			return nil
		}
		if !includeHidden && strings.HasPrefix(base, ".") {
			return nil
		}
		if include != nil && !include.matchPath(relSlash) {
			return nil
		}
		if exclude != nil && exclude.matchPath(relSlash) {
			return nil
		}
		files = append(files, relSlash)
		return nil
	})
	return files, err
}

// ── parallel scan ─────────────────────────────────────────────────────────────

type grepOutcome struct {
	lines        []string
	filesMatched int
	truncated    bool
}

func runGrep(ctx context.Context, root string, files []string, re *regexp.Regexp, mode string, before, after, maxResults int) grepOutcome {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > len(files) {
		workers = len(files)
	}
	if workers < 1 {
		workers = 1
	}

	type fileResult struct {
		path  string
		lines []string
		count int
	}

	in := make(chan string, workers*2)
	out := make(chan fileResult, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range in {
				if ctx.Err() != nil {
					return
				}
				abs := filepath.Join(root, rel)
				lines, count := scanFile(abs, rel, re, mode, before, after)
				if count > 0 {
					out <- fileResult{path: rel, lines: lines, count: count}
				}
			}
		}()
	}

	go func() {
		for _, f := range files {
			if ctx.Err() != nil {
				break
			}
			in <- f
		}
		close(in)
		wg.Wait()
		close(out)
	}()

	var collected []fileResult
	for r := range out {
		collected = append(collected, r)
	}

	// Sort for stable output by path.
	sort.Slice(collected, func(i, j int) bool { return collected[i].path < collected[j].path })

	var outcome grepOutcome
	outcome.filesMatched = len(collected)

	switch mode {
	case "files_with_matches":
		for _, r := range collected {
			if len(outcome.lines) >= maxResults {
				outcome.truncated = true
				break
			}
			outcome.lines = append(outcome.lines, r.path)
		}
	case "count":
		for _, r := range collected {
			if len(outcome.lines) >= maxResults {
				outcome.truncated = true
				break
			}
			outcome.lines = append(outcome.lines, fmt.Sprintf("%s:%d", r.path, r.count))
		}
	default: // content
		for _, r := range collected {
			for _, line := range r.lines {
				if len(outcome.lines) >= maxResults {
					outcome.truncated = true
					break
				}
				outcome.lines = append(outcome.lines, line)
			}
			if outcome.truncated {
				break
			}
		}
	}
	return outcome
}

// scanFile scans a single file and returns either formatted match lines
// (mode=content) or simply a non-zero count (mode=files_with_matches/count).
func scanFile(abs, rel string, re *regexp.Regexp, mode string, before, after int) ([]string, int) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, 0
	}
	defer f.Close()

	// Sniff for binary: read first 512 bytes; if NUL present, skip.
	br := bufio.NewReaderSize(f, 64*1024)
	head, _ := br.Peek(512)
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, 0
	}

	scanner := bufio.NewScanner(br)
	// Allow long lines (up to 1 MiB).
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var (
		lines      []string
		count      int
		ringBefore []string
		afterLeft  int
		lineNo     int
	)

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		matched := re.MatchString(line)

		if mode != "content" {
			if matched {
				count++
				// In files_with_matches mode we can stop after the first hit.
				if mode == "files_with_matches" {
					return nil, count
				}
			}
			continue
		}

		if matched {
			count++
			// Flush context-before.
			for i, prev := range ringBefore {
				prevNo := lineNo - len(ringBefore) + i
				lines = append(lines, fmt.Sprintf("%s-%d-%s", rel, prevNo, prev))
			}
			ringBefore = ringBefore[:0]
			lines = append(lines, fmt.Sprintf("%s:%d:%s", rel, lineNo, line))
			afterLeft = after
		} else if afterLeft > 0 {
			lines = append(lines, fmt.Sprintf("%s-%d-%s", rel, lineNo, line))
			afterLeft--
		} else if before > 0 {
			ringBefore = append(ringBefore, line)
			if len(ringBefore) > before {
				ringBefore = ringBefore[1:]
			}
		}
	}
	return lines, count
}
