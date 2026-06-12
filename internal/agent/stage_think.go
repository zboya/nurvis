package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/provider"
)

// ── Stage: think ─────────────────────────────────────────────────────────

type thinkStage struct {
	l *Loop
}

func (*thinkStage) Name() string { return "think" }

func (s *thinkStage) Run(ctx context.Context, state *RunState) error {
	state.StageResult = Continue

	chatReq := provider.ChatRequest{
		Model:    s.l.agent.Model,
		Messages: state.Buf.All(),
		Tools:    s.l.toolReg.Schemas(s.l.agent.AllowedTools),
		Stream:   true,
		Options:  s.l.agent.Options,
	}

	chunks, err := s.l.provider.Chat(ctx, chatReq)
	if err != nil {
		return s.handleChatErr(ctx, state, err)
	}

	var content strings.Builder
	toolAccums := map[int]*toolCallAccum{}
	var finishReason string

	for chunk := range chunks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if chunk.Reasoning != "" {
			s.l.bus.Publish(bus.TopicAgentChunk, map[string]any{
				"session_id": state.SessionID,
				"reasoning":  chunk.Reasoning,
			})
		}
		if chunk.Content != "" {
			content.WriteString(chunk.Content)
			state.Output.WriteString(chunk.Content)
			s.l.bus.Publish(bus.TopicAgentChunk, map[string]any{
				"session_id": state.SessionID,
				"content":    chunk.Content,
			})
		}
		for _, delta := range chunk.ToolCalls {
			slog.Debug("ToolCalls delta", "content", string(delta.Arguments))
			tc, changed := mergeToolCallDelta(toolAccums, delta)
			if changed {
				s.publishToolCall(ctx, state, tc, "streaming")
			}
		}
		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
	}
	toolCalls := finalizeToolCalls(toolAccums)
	for _, tc := range toolCalls {
		s.publishToolCall(ctx, state, tc, "ready")
	}

	// Truncation check: finish_reason=length means the model hit the context limit
	truncated := finishReason == "length"
	if truncated {
		if state.truncRetries >= maxTruncRetries {
			return fmt.Errorf("think: truncated %d times", state.truncRetries)
		}
		state.truncRetries++
		hint := "[System] The previous output was truncated (finish_reason=length or tool argument parse failure). Please complete the task in shorter increments across multiple turns."
		state.Buf.AppendPending(provider.Message{Role: provider.RoleAssistant, Content: content.String()})
		state.Buf.AppendPending(provider.Message{Role: provider.RoleUser, Content: hint})
		state.StageResult = RetryIteration
		slog.Warn("loop/think: truncation retry",
			"session", state.SessionID,
			"attempt", state.truncRetries,
			"finish_reason", finishReason,
		)
		return nil
	}
	state.truncRetries = 0
	state.overflowRetries = 0

	state.Buf.AppendPending(provider.Message{
		Role:      provider.RoleAssistant,
		Content:   content.String(),
		ToolCalls: toolCalls,
	})
	state.ToolCalls = toolCalls

	if len(toolCalls) > 0 {
		names := make([]string, len(toolCalls))
		for i, tc := range toolCalls {
			names[i] = tc.Name
		}
		slog.Info("loop/think: tool calls", "session", state.SessionID, "tools", names)
	} else {
		// Model gave a final reply; exit the loop
		state.StageResult = BreakLoop
		slog.Debug("loop/think: completed", "session", state.SessionID, "context", content.String())
	}
	return nil
}

type toolCallAccum struct {
	id   string
	name string
	args strings.Builder
}

func mergeToolCallDelta(accums map[int]*toolCallAccum, delta provider.ToolCall) (provider.ToolCall, bool) {
	a, ok := accums[delta.Index]
	if !ok {
		a = &toolCallAccum{}
		accums[delta.Index] = a
	}
	changed := false
	if delta.ID != "" && delta.ID != a.id {
		a.id = delta.ID
		changed = true
	}
	if delta.Name != "" && delta.Name != a.name {
		a.name = delta.Name
		changed = true
	}
	if len(delta.Arguments) > 0 {
		a.args.Write(delta.Arguments)
		changed = true
	}
	return buildToolCall(delta.Index, a), changed
}

func finalizeToolCalls(accums map[int]*toolCallAccum) []provider.ToolCall {
	if len(accums) == 0 {
		return nil
	}
	max := -1
	for idx := range accums {
		if idx > max {
			max = idx
		}
	}
	calls := make([]provider.ToolCall, 0, len(accums))
	for i := 0; i <= max; i++ {
		a, ok := accums[i]
		if !ok {
			continue
		}
		calls = append(calls, buildToolCall(i, a))
	}
	return calls
}

func buildToolCall(index int, a *toolCallAccum) provider.ToolCall {
	id := a.id
	if id == "" {
		id = fmt.Sprintf("call_%d", index)
	}
	raw := json.RawMessage(a.args.String())
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return provider.ToolCall{Index: index, ID: id, Name: a.name, Arguments: raw}
}

func (s *thinkStage) publishToolCall(_ context.Context, state *RunState, tc provider.ToolCall, status string) {
	s.l.bus.Publish(bus.TopicToolCall, map[string]any{
		"session_id":    state.SessionID,
		"name":          tc.Name,
		"id":            tc.ID,
		"tool_key":      toolCallEventKey(state, tc),
		"index":         tc.Index,
		"arguments":     toolCallArgumentsForEvent(tc.Arguments),
		"arguments_raw": string(tc.Arguments),
		"status":        status,
	})
}

func toolCallEventKey(state *RunState, tc provider.ToolCall) string {
	return fmt.Sprintf("round_%d_tool_%d", state.Round, tc.Index)
}

func toolCallArgumentsForEvent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		return v
	}
	return string(raw)
}

// handleChatErr attempts emergency compaction + retry when the Chat call itself fails.
func (s *thinkStage) handleChatErr(ctx context.Context, state *RunState, err error) error {
	if !isContextOverflow(err) {
		return fmt.Errorf("think: chat: %w", err)
	}
	if state.overflowRetries >= maxOverflowRetries {
		return fmt.Errorf("think: context overflow after compaction: %w", err)
	}
	state.overflowRetries++
	slog.Warn("loop/think: context overflow, emergency compaction", "session", state.SessionID)
	compacted, cerr := compactMessages(ctx, s.l.provider, s.l.agent.Model,
		state.Buf.History(), compactKeepRecent)
	if cerr != nil {
		return fmt.Errorf("emergency compact failed: %w (orig: %v)", cerr, err)
	}
	state.Buf.SetHistory(compacted)
	state.StageResult = RetryIteration
	return nil
}

// isContextOverflow does simple keyword matching for provider overflow errors.
func isContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	x := strings.ToLower(err.Error())
	return strings.Contains(x, "context length") ||
		strings.Contains(x, "context_length") ||
		strings.Contains(x, "maximum context") ||
		strings.Contains(x, "too many tokens") ||
		strings.Contains(x, "exceeds the maximum")
}
