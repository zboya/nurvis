package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/provider"
	"github.com/zboya/nurvis/internal/tools"
)

// ── Stage: act + observe ─────────────────────────────────────────────────

type actStage struct{ l *Loop }

func (*actStage) Name() string { return "act" }

// Execute all tool calls sequentially; return immediately on ctx cancellation;
// tool errors are converted to IsError and execution continues.
func (s *actStage) Run(ctx context.Context, state *RunState) error {
	if len(state.ToolCalls) == 0 {
		return nil
	}
	scope := tools.Scope{
		WorkspaceDir: state.Workspace,
		AgentID:      state.AgentID,
		SessionID:    state.SessionID,
		ProjectID:    state.ProjectID,
		ChannelID:    state.ChannelInstanceID,
		SkillRoots:   state.SkillRoots,
	}
	if state.ReplyTo != nil {
		scope.ReplyTo = &tools.ScopePeer{
			ID:   state.ReplyTo.ID,
			Name: state.ReplyTo.Name,
			Type: state.ReplyTo.Type,
		}
	}

	for i, tc := range state.ToolCalls {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.l.bus.Publish(bus.TopicToolCall, map[string]any{
			"session_id":    state.SessionID,
			"name":          tc.Name,
			"id":            tc.ID,
			"tool_key":      toolCallEventKey(state, tc),
			"index":         tc.Index,
			"arguments":     toolCallArgumentsForEvent(tc.Arguments),
			"arguments_raw": string(tc.Arguments),
			"status":        "running",
		})
		slog.Info("loop/act: invoking tool",
			"session", state.SessionID, "tool", tc.Name, "call_id", tc.ID, "index", i, "args", string(tc.Arguments))

		t, ok := s.l.toolReg.Get(tc.Name)
		var res *tools.Result
		if !ok {
			slog.Warn("loop/act: tool not found", "session", state.SessionID, "tool", tc.Name)
			res = &tools.Result{Content: fmt.Sprintf("tool %q not found", tc.Name), IsError: true}
		} else {
			var err error
			res, err = t.Invoke(ctx, tc.Arguments, scope)
			if err != nil {
				res = &tools.Result{Content: err.Error(), IsError: true}
			}
		}
		slog.Debug("loop/act: tool result", "session", state.SessionID, "tool", tc.Name, "call_id", tc.ID,
			"result", res)
		if res.IsError {
			slog.Warn("loop/act: tool error",
				"session", state.SessionID, "tool", tc.Name, "content", res.Content)
		}
		s.l.bus.Publish(bus.TopicToolResult, map[string]any{
			"session_id": state.SessionID,
			"name":       tc.Name,
			"id":         tc.ID,
			"tool_key":   toolCallEventKey(state, tc),
			"result":     res.Content,
			"is_error":   res.IsError,
		})

		// Aggregate media produced by tools: failed tools don't add artifacts,
		// avoiding noise in channel pushes.
		if !res.IsError && len(res.Media) > 0 {
			for _, art := range res.Media {
				state.OutputMedia = append(state.OutputMedia, MediaArtifact{
					Kind:     guessMediaKind(art.MimeType, art.Name),
					Name:     art.Name,
					MimeType: art.MimeType,
					Path:     art.Path,
					URL:      art.URL,
					Data:     art.Data,
				})
			}
		}

		// Observe inlined: tool result is written back to pending immediately,
		// preserving order consistent with the think stage.
		state.Buf.AppendPending(provider.Message{
			Role:       provider.RoleTool,
			Content:    res.Content,
			ToolCallID: tc.ID,
			Name:       tc.Name,
		})
	}
	state.ToolCalls = nil
	return nil
}
