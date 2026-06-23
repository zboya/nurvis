package gateway

// File-system helpers exposed to the frontend "资源" (resources) settings tab.
//
// Two endpoints are provided:
//   - fs.list_dir          list files in a directory (non-recursive)
//   - fs.media_output_dir  return current media output dir + the default one
//
// list_dir is read-only and includes a best-effort preview URL for files
// inside the directory so the frontend can render image thumbnails directly.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fsEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified_ms"`
	URL      string `json:"url,omitempty"`
}

func (m *Methods) handleFsListDir(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(params, &p)

	dir := strings.TrimSpace(p.Path)
	if dir == "" {
		// Fall back to the current media output dir.
		if m.Agents != nil {
			dir = m.Agents.GetMediaOutputDir()
		}
	}
	if dir == "" {
		return map[string]any{"path": "", "entries": []fsEntry{}, "exists": false}, nil
	}
	// Expand ~ shorthand.
	if strings.HasPrefix(dir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
	}
	dir = filepath.Clean(dir)

	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return map[string]any{"path": dir, "entries": []fsEntry{}, "exists": false}, nil
	}
	raw, err := os.ReadDir(dir)
	if err != nil {
		return nil, &RPCError{Code: "io_error", Message: err.Error()}
	}

	entries := make([]fsEntry, 0, len(raw))
	for _, e := range raw {
		// Skip hidden files for a cleaner UI.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		full := filepath.Join(dir, e.Name())
		ent := fsEntry{
			Name:     e.Name(),
			Path:     full,
			IsDir:    e.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime().UnixMilli(),
		}
		// Generate a preview URL for files (images especially).
		if !e.IsDir() && m.Agents != nil && m.Agents.MediaPreviewURL != nil {
			if u, err := m.Agents.MediaPreviewURL(full); err == nil {
				ent.URL = u
			}
		}
		entries = append(entries, ent)
	}
	// Sort: directories first, then by modified time desc.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Modified > entries[j].Modified
	})

	return map[string]any{
		"path":    dir,
		"exists":  true,
		"entries": entries,
	}, nil
}

// handleFsMediaOutputDir returns the currently effective media output dir.
// Useful for the resources tab to show what is in use before saving.
func (m *Methods) handleFsMediaOutputDir(_ context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	current := ""
	if m.Agents != nil {
		current = m.Agents.GetMediaOutputDir()
	}
	return map[string]any{"path": current}, nil
}
