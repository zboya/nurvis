package agent

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/zboya/nurvis/internal/provider"
)

// ── Stage: history ───────────────────────────────────────────────────────

type historyStage struct{ l *Loop }

func (*historyStage) Name() string { return "history" }
func (s *historyStage) Run(ctx context.Context, state *RunState) error {
	records, err := s.l.messages.List(ctx, state.SessionID, defaultMaxHistoryMessages)
	if err != nil {
		return err
	}
	state.freshSession = len(records) == 0
	for _, rec := range records {
		m := provider.Message{Role: rec.Role, Content: rec.Content}
		if rec.ToolName != "" {
			m.Name = rec.ToolName
		}
		if rec.ToolCalls != nil {
			if b, err := json.Marshal(rec.ToolCalls); err == nil {
				_ = json.Unmarshal(b, &m.ToolCalls)
			}
		}
		state.Buf.AppendHistory(m)
	}
	// Load rolling summary
	if sess, err := s.l.sessions.Get(ctx, state.SessionID); err == nil && sess != nil {
		state.Summary = sess.Summary
	}
	// Repair tool_calls/tool pairing breakage in history (from stale data / interrupted run)
	if cleaned, mutated := sanitizeHistory(state.Buf.History()); mutated > 0 {
		state.Buf.SetHistory(cleaned)
		slog.Info("loop/history: sanitized loaded history", "mutated", mutated)
	}
	return nil
}
