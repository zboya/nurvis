package agent

import (
	"context"
	"time"
)

// ── Stage: finalize ──────────────────────────────────────────────────────

type finalizeStage struct{ l *Loop }

func (*finalizeStage) Name() string { return "memory" } // Reuse legacy event name
func (s *finalizeStage) Run(ctx context.Context, state *RunState) error {
	// 1. Flush remaining pending; persistMessages(partial=false) handles
	//    session.updated_at update and first-turn label setting internally;
	//    no need to duplicate here.
	if pending := state.Buf.FlushPending(); len(pending) > 0 {
		s.l.persistMessages(ctx, state, pending, false)
	} else {
		// Even if pending is empty (e.g. already flushed at checkpoint), still
		// Touch the session so the UI shows the latest activity time. Label
		// was already set by the final-save branch triggered by checkpoint
		// on the first turn, so it won't be set again.
		_ = s.l.sessions.Touch(ctx, state.SessionID, time.Now())
		if state.freshSession {
			s.l.maybeSetLabel(ctx, state)
			state.freshSession = false
		}
	}
	// 2. Summarize if needed
	if len(state.Buf.History()) >= summarizeMessageThreshold {
		s.l.runSummarize(ctx, state)
	}
	return nil
}
