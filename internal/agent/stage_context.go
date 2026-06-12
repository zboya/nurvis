package agent

import (
	"context"
	"fmt"

	"github.com/zboya/nurvis/internal/store/repo"
)

// ── Stage: context ───────────────────────────────────────────────────────

type contextStage struct{ l *Loop }

func (*contextStage) Name() string { return "context" }
func (s *contextStage) Run(ctx context.Context, state *RunState) error {
	projID := state.ProjectID
	if projID == "" {
		projID = s.l.agent.DefaultProject
	}
	if err := s.l.sessions.EnsureCreated(ctx, repo.Session{
		ID:        state.SessionID,
		AgentID:   s.l.agent.ID,
		ProjectID: projID,
		Channel:   state.Channel,
	}); err != nil {
		return fmt.Errorf("context: create session: %w", err)
	}
	if state.ProjectID == "" {
		state.ProjectID = projID
	}
	if projID != "" {
		if p, err := s.l.ws.Resolve(ctx, projID); err == nil {
			state.Workspace = p.Dir
		}
	}
	return nil
}
