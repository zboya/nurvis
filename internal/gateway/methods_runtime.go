package gateway

import (
	"context"
	"encoding/json"

	"github.com/zboya/nurvis/internal/hardware"
)

// ── hardware / runtime ───────────────────────────────────────────────────────

func (m *Methods) handleHardwareProbe(_ context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	hw, err := hardware.Probe()
	if err != nil {
		return nil, err
	}
	return hw, nil
}

// runtime.status reports the current state of the local llama.cpp runtime.
//
// In this architecture there is no external server process to introspect
// from the gateway side (engines live inside llamax), so we simply report
// whether the lib directory is populated and which dir is currently active.
func (m *Methods) handleRuntimeStatus(_ context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	libPath := ""
	ready := false
	if m.Runtime != nil {
		libPath = m.Runtime.LibPath()
		// EnsureReady is idempotent; we don't trigger it here, but the
		// presence of the path is a useful signal for the frontend.
		ready = libPath != ""
	}
	return map[string]any{
		"backend":  "llama",
		"lib_path": libPath,
		"ready":    ready,
	}, nil
}

// runtime.ensure (re-)installs the llama.cpp libraries if missing.
func (m *Methods) handleRuntimeEnsure(ctx context.Context, _ *Conn, _ json.RawMessage) (any, error) {
	if m.Runtime == nil {
		return map[string]any{"ok": false, "error": "runtime not configured"}, nil
	}
	if err := m.Runtime.EnsureReady(ctx); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":       true,
		"lib_path": m.Runtime.LibPath(),
	}, nil
}
