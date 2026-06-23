package downloader

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// downloadState is the on-disk shape of <dest>.part.json. It records
// just enough metadata to validate a resume attempt against a fresh
// probe and to know which byte ranges still need fetching.
//
// The schema is intentionally minimal and self-describing — adding new
// fields is a backward-compatible operation as long as readers tolerate
// unknown JSON keys (encoding/json does by default).
type downloadState struct {
	// SchemaVersion is bumped whenever an incompatible change requires
	// existing sidecars to be discarded. Currently 1.
	SchemaVersion int `json:"schema_version"`

	URL          string    `json:"url"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag,omitempty"`
	LastModified time.Time `json:"last_modified,omitempty"`
	AcceptRanges bool      `json:"accept_ranges"`

	Chunks []chunkState `json:"chunks"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// chunkState describes one contiguous byte range. End is inclusive to
// match HTTP Range semantics ("bytes=Start-End"). Downloaded counts how
// many bytes have already landed at Start..Start+Downloaded-1.
type chunkState struct {
	Start      int64 `json:"start"`
	End        int64 `json:"end"`        // inclusive
	Downloaded int64 `json:"downloaded"` // bytes written so far, relative to Start
}

const sidecarSchemaVersion = 1

// readSidecar loads a previously written sidecar from path. Returns
// os.ErrNotExist (wrapped) when no file is present so callers can use
// errors.Is to distinguish "never started" from "corrupted".
func readSidecar(path string) (*downloadState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s downloadState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("sidecar: decode: %w", err)
	}
	if s.SchemaVersion != sidecarSchemaVersion {
		return nil, fmt.Errorf("sidecar: unsupported schema_version %d", s.SchemaVersion)
	}
	return &s, nil
}

// writeSidecar atomically replaces path with the current state. We
// write to a temp file in the same directory and rename onto path so a
// crash mid-write can never leave a half-baked JSON document on disk.
func writeSidecar(path string, s *downloadState) error {
	s.SchemaVersion = sidecarSchemaVersion
	s.UpdatedAt = time.Now()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sidecar-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename never happened.
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
