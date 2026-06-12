package agent

import (
	"context"

	"github.com/zboya/nurvis/internal/provider"
)

// ── Stage: prepare (start of each iteration) ────────────────────────────────
//
// Rebuilds system message + injects 70/90% nudge. These two things were previously
// scattered in Run(); consolidating into a side-effect-free stage makes testing
// and instrumentation easier.

type prepareStage struct {
	l         *Loop
	maxRounds int
}

func (*prepareStage) Name() string { return "prepare" }
func (s *prepareStage) Run(_ context.Context, state *RunState) error {
	state.Buf.SetSystem(s.l.buildSystemMessage(state))
	if s.maxRounds > 0 {
		pct := float64(state.Round) / float64(s.maxRounds)
		if pct >= 0.9 && !state.nudge90Done {
			state.nudge90Done = true
			state.Buf.AppendPending(provider.Message{
				Role:    provider.RoleUser,
				Content: "[System] URGENT: You have used 90% of your iteration budget. Please wrap up immediately and provide your final answer.",
			})
		} else if pct >= 0.7 && !state.nudge70Done {
			state.nudge70Done = true
			state.Buf.AppendPending(provider.Message{
				Role:    provider.RoleUser,
				Content: "[System] You have used 70% of your iteration budget. Please start wrapping up.",
			})
		}
	}
	return nil
}
