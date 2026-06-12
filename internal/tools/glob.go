// Package tools - glob: fast filesystem glob with `**` (doublestar) support.
//
// Implementation notes (performance):
//   - We walk the tree with filepath.WalkDir (uses readdir without stat per
//     entry, which is cheaper than filepath.Walk).
//   - We prune entire directory subtrees as early as possible by checking the
//     literal directory prefix of the pattern, and by skipping common heavy
//     directories (.git, node_modules, vendor, ...) unless include_hidden is
//     enabled or the pattern explicitly references them.
//   - The glob matcher is a small, allocation-light segment-based matcher that
//     supports `*`, `?`, character classes `[...]`, and `**` for any-depth
//     matching. It avoids building a regexp.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zboya/nurvis/internal/provider"
)

// Glob finds files in the workspace matching a glob pattern.
type Glob struct{}

func (*Glob) Name() string { return "glob" }
func (*Glob) Description() string {
	return "Find files in the workspace by glob pattern. Supports `*`, `?`, character classes, and `**` for any-depth directory matching."
}

func (*Glob) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "glob",
		Description: "Find files matching a glob pattern, relative to the workspace root. Supports `**` for recursive matching (e.g. `src/**/*.go`).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern, e.g. `**/*.go`, `src/**/*.{ts,tsx}` is NOT supported — use simpler patterns.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Optional sub-directory (relative to workspace) to search inside. Defaults to workspace root.",
				},
				"include_hidden": map[string]any{
					"type":        "boolean",
					"description": "If true, traverse into hidden directories (starting with `.`) and `node_modules`/`vendor`. Defaults to false.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max number of results to return. Defaults to 1000.",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (*Glob) Invoke(ctx context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Pattern       string `json:"pattern"`
		Path          string `json:"path"`
		IncludeHidden bool   `json:"include_hidden"`
		Limit         int    `json:"limit"`
	}
	_ = json.Unmarshal(raw, &args)

	if args.Pattern == "" {
		return &Result{Content: "pattern is required", IsError: true}, nil
	}
	if args.Limit <= 0 {
		args.Limit = 1000
	}

	rootRel := args.Path
	if rootRel == "" {
		rootRel = "."
	}
	root, err := safeJoin(scope.WorkspaceDir, rootRel)
	if err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}

	matcher, err := compileGlob(args.Pattern)
	if err != nil {
		return &Result{Content: fmt.Sprintf("invalid pattern: %v", err), IsError: true}, nil
	}

	matches, truncated, err := walkGlob(ctx, root, matcher, args.IncludeHidden, args.Limit)
	if err != nil {
		return &Result{Content: fmt.Sprintf("glob error: %v", err), IsError: true}, nil
	}

	// Sort by modification time desc (Claude Code behavior), then by path for stability.
	type entry struct {
		path string
		mod  int64
	}
	entries := make([]entry, 0, len(matches))
	for _, p := range matches {
		var mod int64
		if info, err := os.Stat(filepath.Join(root, p)); err == nil {
			mod = info.ModTime().UnixNano()
		}
		entries = append(entries, entry{p, mod})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].mod != entries[j].mod {
			return entries[i].mod > entries[j].mod
		}
		return entries[i].path < entries[j].path
	})

	if len(entries) == 0 {
		return &Result{Content: fmt.Sprintf("no files matching %q", args.Pattern)}, nil
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.path)
		sb.WriteByte('\n')
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("... (truncated at %d results)\n", args.Limit))
	}
	return &Result{
		Content: sb.String(),
		Meta: map[string]any{
			"count":     len(entries),
			"truncated": truncated,
			"pattern":   args.Pattern,
		},
	}, nil
}

// ── walker ─────────────────────────────────────────────────────────────────────

// commonSkipDirs lists directories that are skipped by default to avoid
// traversing huge irrelevant trees (parity with `rg` defaults).
var commonSkipDirs = map[string]struct{}{
	".git":         {},
	".hg":          {},
	".svn":         {},
	"node_modules": {},
	"vendor":       {},
	".idea":        {},
	".vscode":      {},
	"dist":         {},
	"build":        {},
	".next":        {},
	".cache":       {},
	"__pycache__":  {},
}

func walkGlob(ctx context.Context, root string, m *globMatcher, includeHidden bool, limit int) ([]string, bool, error) {
	var matches []string
	truncated := false

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Don't fail the whole walk on permission errors.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		// Use forward slashes for matching (works the same on macOS/Linux,
		// and matches what users expect on Windows).
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
			// Optimisation: if the matcher's literal directory prefix can never
			// match this subtree, prune it.
			if !m.couldMatchDir(relSlash) {
				return fs.SkipDir
			}
			return nil
		}

		if !includeHidden && strings.HasPrefix(base, ".") {
			return nil
		}

		if m.matchPath(relSlash) {
			if len(matches) >= limit {
				truncated = true
				return errStopWalk
			}
			matches = append(matches, relSlash)
		}
		return nil
	})

	if err != nil && err != errStopWalk {
		return matches, truncated, err
	}
	return matches, truncated, nil
}

var errStopWalk = fmt.Errorf("stop walk")

// ── glob matcher ───────────────────────────────────────────────────────────────

// globMatcher matches a path against a glob pattern with `**` semantics.
// The pattern is split by `/` into segments; `**` segments match zero or more
// path segments. Other segments are matched element-wise with `*`, `?`, `[...]`.
type globMatcher struct {
	segments []string
	// staticDirPrefix is the leading run of literal (no-wildcard) segments;
	// used to prune the walk when relSlash cannot share that prefix.
	staticDirPrefix []string
}

func compileGlob(pattern string) (*globMatcher, error) {
	pattern = strings.TrimPrefix(pattern, "./")
	pattern = filepath.ToSlash(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}
	segs := strings.Split(pattern, "/")
	for _, s := range segs {
		if s == "" {
			return nil, fmt.Errorf("empty segment in pattern %q", pattern)
		}
	}
	m := &globMatcher{segments: segs}
	for _, s := range segs {
		if containsMeta(s) {
			break
		}
		m.staticDirPrefix = append(m.staticDirPrefix, s)
	}
	return m, nil
}

func containsMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// matchPath reports whether the given relative path (forward-slash separated)
// matches the compiled pattern.
func (m *globMatcher) matchPath(relSlash string) bool {
	parts := strings.Split(relSlash, "/")
	return matchSegments(m.segments, parts)
}

// couldMatchDir reports whether the directory at relSlash could still lead to
// a match. Used purely for pruning during the walk: false means we can SkipDir.
func (m *globMatcher) couldMatchDir(relSlash string) bool {
	if len(m.staticDirPrefix) == 0 {
		return true
	}
	dirParts := strings.Split(relSlash, "/")
	// Walk down the static prefix as far as we have directory components.
	for i, want := range m.staticDirPrefix {
		if i >= len(dirParts) {
			return true // dir is shallower than prefix; could still descend into it
		}
		if want == "**" {
			return true
		}
		if dirParts[i] != want {
			return false
		}
	}
	return true
}

// matchSegments matches pattern segments against path segments, with `**`
// matching zero or more path components (Bash globstar / Claude Code semantics).
func matchSegments(pattern, parts []string) bool {
	pi, ti := 0, 0
	// Backtrack frame for the last `**` we saw.
	starPi, starTi := -1, 0

	for ti < len(parts) {
		if pi < len(pattern) {
			if pattern[pi] == "**" {
				starPi = pi
				starTi = ti
				pi++
				continue
			}
			if matchSegment(pattern[pi], parts[ti]) {
				pi++
				ti++
				continue
			}
		}
		// Mismatch: try to extend the most recent `**` by one more component.
		if starPi >= 0 {
			pi = starPi + 1
			starTi++
			ti = starTi
			continue
		}
		return false
	}
	// Consume trailing `**` segments.
	for pi < len(pattern) && pattern[pi] == "**" {
		pi++
	}
	return pi == len(pattern)
}

// matchSegment matches a single pattern segment against a single path component
// using `*`, `?`, and `[...]` (character classes).
func matchSegment(pattern, name string) bool {
	pi, ni := 0, 0
	starPi, starNi := -1, 0
	for ni < len(name) {
		if pi < len(pattern) {
			c := pattern[pi]
			switch c {
			case '*':
				starPi = pi
				starNi = ni
				pi++
				continue
			case '?':
				pi++
				ni++
				continue
			case '[':
				end := strings.IndexByte(pattern[pi:], ']')
				if end <= 0 {
					// Treat as literal `[`.
					if pattern[pi] == name[ni] {
						pi++
						ni++
						continue
					}
				} else {
					class := pattern[pi+1 : pi+end]
					if matchCharClass(class, name[ni]) {
						pi += end + 1
						ni++
						continue
					}
				}
			default:
				if c == name[ni] {
					pi++
					ni++
					continue
				}
			}
		}
		if starPi >= 0 {
			pi = starPi + 1
			starNi++
			ni = starNi
			continue
		}
		return false
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

func matchCharClass(class string, b byte) bool {
	negate := false
	i := 0
	if len(class) > 0 && (class[0] == '!' || class[0] == '^') {
		negate = true
		i = 1
	}
	matched := false
	for i < len(class) {
		if i+2 < len(class) && class[i+1] == '-' {
			lo, hi := class[i], class[i+2]
			if b >= lo && b <= hi {
				matched = true
			}
			i += 3
			continue
		}
		if class[i] == b {
			matched = true
		}
		i++
	}
	if negate {
		return !matched
	}
	return matched
}
