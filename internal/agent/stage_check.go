package agent

import (
	"context"
	"log/slog"
	"strings"

	"github.com/zboya/nurvis/internal/provider"
)

// ── Stage: check task is or not complete ─────────────────────

type checkTaskStage struct {
	l *Loop
}

func (*checkTaskStage) Name() string { return "check_task" }
func (s *checkTaskStage) Run(ctx context.Context, state *RunState) error {
	state.Round++
	pending := state.Buf.FlushPending()
	slog.Debug("loop/check_task: flushed", "session", state.SessionID, "count", len(pending),
		"round", state.Round, "stage_result", state.StageResult)
	if len(pending) > 0 {
		s.l.persistMessages(ctx, state, pending, true)
	}
	// s.check(ctx, state)
	return nil
}

// check triggers a model summarization and writes back to sessions.summary.
func (s *checkTaskStage) check(ctx context.Context, state *RunState) {
	if state.StageResult != NoToolCalls {
		return
	}
	slog.Info("loop/check_task: generating",
		"session", state.SessionID, "msgs", len(state.Buf.History()))

	transcript := renderTranscript(state.Buf.History())
	prompt := `Task: Evaluate whether the conversation below has completed the user's specified task.

Instructions:
1. The first line of your output must contain ONLY "yes" or "no" (lowercase, no other text).
2. If completed, output "yes".
3. If NOT completed, output "no" on the first line, then list the uncompleted items on the following lines.

Conversation:` + "\n\n" + transcript

	chunks, err := s.l.provider.Chat(ctx, provider.ChatRequest{
		Model:    s.l.agent.Model,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: prompt}},
		Stream:   false,
	})
	if err != nil {
		slog.Warn("loop/check_task: chat error", "err", err)
		return
	}
	var sb strings.Builder
	for c := range chunks {
		sb.WriteString(c.Content)
	}

	slog.Debug("loop/check_task result", "input", prompt, "output", sb.String())

	result := strings.Split(sb.String(), "\n")
	firstIdx := -1
	for i, l := range result {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if firstIdx < 0 {
			firstIdx = i
			break
		}
	}
	firstLine := result[firstIdx]
	if strings.ToLower(firstLine) == "no" {
		state.StageResult = RetryIteration
		state.Summary = strings.Join(result[firstIdx:], "\n")
	} else {
		state.StageResult = BreakLoop
	}
}
