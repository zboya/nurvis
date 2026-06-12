package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/provider"
)

// ── Stage: prune (before each think) ───────────────────────────────────────

type pruneStage struct {
	l *Loop
}

func (*pruneStage) Name() string { return "prune" }
func (s *pruneStage) Run(ctx context.Context, state *RunState) error {
	state.StageResult = Continue

	overhead := CountMessage(state.Buf.System())
	overhead += CountToolSchemas(s.l.toolReg.Schemas(s.l.agent.AllowedTools))
	overhead += CountMessages(state.Buf.Pending())

	budget := s.l.contextWindow - overhead - s.l.maxOutputTokens - s.l.reserveTokens
	if budget <= 0 {
		state.Buf.SetHistory(nil)
		return nil
	}

	pruned, stats, err := pruneAndCompact(
		ctx, s.l.provider, s.l.agent.Model,
		state.Buf.History(), budget, compactKeepRecent,
	)
	if errors.Is(err, ErrStillOverBudget) {
		// Still over budget after compaction: tell main loop to abort
		state.Buf.SetHistory(pruned)
		state.StageResult = AbortRun
		return err
	}
	if err != nil {
		// Other errors (e.g. chat failure): non-fatal, continue this round
		slog.Warn("loop/prune: error (continuing)", "session", state.SessionID, "err", err)
		return nil
	}
	if stats.TokensAfter != stats.TokensBefore || stats.Compacted {
		state.Buf.SetHistory(pruned)
		slog.Info("loop/prune: applied",
			"session", state.SessionID,
			"tokens_before", stats.TokensBefore,
			"tokens_after", stats.TokensAfter,
			"budget", budget,
			"trimmed", stats.ResultsTrimmed,
			"cleared", stats.ResultsCleared,
			"compacted", stats.Compacted,
		)
		s.l.bus.Publish(bus.TopicAgentStage, map[string]any{
			"session_id":    state.SessionID,
			"stage":         "prune",
			"event":         "applied",
			"tokens_before": stats.TokensBefore,
			"tokens_after":  stats.TokensAfter,
			"budget":        budget,
			"compacted":     stats.Compacted,
		})
	}
	return nil
}

// PruneStats records the outcome of a pruning pass, for events / logging.
type PruneStats struct {
	ResultsTrimmed int  // Soft prune: tool result was truncated
	ResultsCleared int  // Hard prune: tool result was cleared (placeholder only)
	Compacted      bool // LLM compaction was triggered
	TokensBefore   int
	TokensAfter    int
}

// pruneToolResults is Phase 1: soft-prune tool results from oldest to newest within budget.
//
// Design motivation:
//   - In practice, 90% of context bloat comes from tool output (read_file / exec / http);
//     earlier tool results have less value for current reasoning (the model has already read them).
//   - User messages, assistant text, and model reasoning are never touched — that is task memory.
//
// Pruning order (oldest to newest):
//
//	First truncate any single tool content exceeding hardCap (with a trailing truncation hint);
//	if still over budget, replace earlier tool content entirely with a short placeholder.
//
// Does not repair tool_calls pairing — that is sanitizeHistory's responsibility,
// called by the caller after prune.
func pruneToolResults(msgs []provider.Message, budget int) ([]provider.Message, PruneStats) {
	stats := PruneStats{TokensBefore: CountMessages(msgs)}
	if stats.TokensBefore <= budget {
		stats.TokensAfter = stats.TokensBefore
		return msgs, stats
	}

	// Maximum character count for a single tool result (roughly ~hardCap/4 tokens).
	const (
		hardCapChars       = 4000
		placeholderContent = "[tool result trimmed by context manager]"
	)

	// Copy to avoid in-place modification of the caller's slice
	out := make([]provider.Message, len(msgs))
	copy(out, msgs)

	// pass 1: truncate individual overlong entries
	for i := range out {
		if out[i].Role != provider.RoleTool {
			continue
		}
		if len([]rune(out[i].Content)) <= hardCapChars {
			continue
		}
		runes := []rune(out[i].Content)
		out[i].Content = string(runes[:hardCapChars]) +
			"\n…[truncated, original length=" + itoa(len(runes)) + " chars]"
		stats.ResultsTrimmed++
	}

	current := CountMessages(out)
	if current <= budget {
		stats.TokensAfter = current
		return out, stats
	}

	// pass 2: from the oldest tool message, replace entire content with placeholder until budget is met
	for i := 0; i < len(out) && current > budget; i++ {
		if out[i].Role != provider.RoleTool {
			continue
		}
		if out[i].Content == placeholderContent {
			continue
		}
		before := CountMessage(out[i])
		out[i].Content = placeholderContent
		current -= before - CountMessage(out[i])
		stats.ResultsCleared++
	}

	stats.TokensAfter = current
	return out, stats
}

// compactMessages is Phase 2: use the LLM to compress history into a summary + keep the most recent keepRecent messages.
//
// Behavior:
//  1. Take history[:-keepRecent] and concatenate into a transcript to feed the model; request a 5-10 line summary.
//  2. Return [summaryAssistantMsg, ...recent], typically ~ keepRecent + 1 in length.
//  3. The caller is responsible for writing the result back to the buffer and sanitizing.
//
// On failure, returns the original history and error; the caller decides whether to abort.
func compactMessages(
	ctx context.Context,
	prov provider.Provider,
	model string,
	history []provider.Message,
	keepRecent int,
) ([]provider.Message, error) {
	if len(history) <= keepRecent+1 {
		return history, nil
	}
	cutoff := len(history) - keepRecent
	older := history[:cutoff]
	recent := history[cutoff:]

	transcript := renderTranscript(older)
	prompt := "You are a conversation compression assistant. Summarize the following " +
		"conversation history in 5-10 bullet points. Preserve: the user's goal, key " +
		"decisions, completed tool actions with brief result summaries, and any open " +
		"questions. Do NOT quote tool outputs verbatim.\n\n----\n" + transcript

	chunks, err := prov.Chat(ctx, provider.ChatRequest{
		Model: model,
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: prompt},
		},
		Stream: false,
	})
	if err != nil {
		return history, fmt.Errorf("compact: chat: %w", err)
	}

	var sb strings.Builder
	for c := range chunks {
		if ctx.Err() != nil {
			return history, ctx.Err()
		}
		sb.WriteString(c.Content)
	}
	summary := strings.TrimSpace(sb.String())
	if summary == "" {
		return history, fmt.Errorf("compact: empty summary")
	}

	summaryMsg := provider.Message{
		Role: provider.RoleSystem,
		Content: "<conversation_summary>\n" + summary + "\n</conversation_summary>\n" +
			"The above is a summary of the earlier conversation; the messages that " +
			"follow are the most recent original messages.",
	}
	out := make([]provider.Message, 0, 1+len(recent))
	out = append(out, summaryMsg)
	out = append(out, recent...)
	return out, nil
}

// renderTranscript renders a message list into "role: content" plain text.
func renderTranscript(msgs []provider.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(strings.ToUpper(m.Role))
		sb.WriteString(": ")
		if m.Content != "" {
			sb.WriteString(m.Content)
		}
		if len(m.ToolCalls) > 0 {
			sb.WriteString(" [tool_calls=")
			for i, tc := range m.ToolCalls {
				if i > 0 {
					sb.WriteByte(',')
				}
				sb.WriteString(tc.Name)
			}
			sb.WriteString("]")
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// pruneAndCompact is the unified entry point for pruning/compaction, called by stagePrune.
//
// Decision logic:
//   - tokens <= 70% budget: return directly, no changes.
//   - 70%~100%: only soft-prune tool results (pruneToolResults) + sanitize.
//   - >100%: if still over budget after soft prune, trigger LLM compaction + sanitize.
//
// If compaction result is still over budget, returns ErrStillOverBudget; caller decides whether to abort.
func pruneAndCompact(
	ctx context.Context,
	prov provider.Provider,
	model string,
	history []provider.Message,
	budget int,
	keepRecent int,
) ([]provider.Message, PruneStats, error) {
	stats := PruneStats{TokensBefore: CountMessages(history)}
	if budget <= 0 || stats.TokensBefore <= budget*70/100 {
		stats.TokensAfter = stats.TokensBefore
		return history, stats, nil
	}

	// Phase 1: soft-prune tool results
	pruned, pStats := pruneToolResults(history, budget)
	stats.ResultsTrimmed = pStats.ResultsTrimmed
	stats.ResultsCleared = pStats.ResultsCleared

	if mutated := stats.ResultsTrimmed + stats.ResultsCleared; mutated > 0 {
		pruned, _ = sanitizeHistory(pruned)
	}

	tokens := CountMessages(pruned)
	stats.TokensAfter = tokens

	if tokens <= budget {
		return pruned, stats, nil
	}

	// Phase 2: compaction
	slog.Info("loop/prune: triggering compaction",
		"tokens", tokens, "budget", budget,
	)
	compacted, err := compactMessages(ctx, prov, model, pruned, keepRecent)
	if err != nil {
		return pruned, stats, err
	}
	compacted, _ = sanitizeHistory(compacted)
	stats.Compacted = true
	stats.TokensAfter = CountMessages(compacted)

	if stats.TokensAfter > budget {
		return compacted, stats, ErrStillOverBudget
	}
	return compacted, stats, nil
}

// ErrStillOverBudget is returned when history is still over budget after compaction, triggering Loop abort.
var ErrStillOverBudget = fmt.Errorf("history still over budget after compaction")

func itoa(n int) string {
	// Avoid importing strconv just for this one use
	return fmt.Sprintf("%d", n)
}

// sanitizeHistory repairs tool_calls ↔ tool message breakage caused by
// pruning or compaction.
//
// The LLM protocol requires: every assistant.tool_calls[i].id must have a
// matching role=tool & tool_call_id=id message later in the sequence; conversely,
// every tool message must trace back to an earlier assistant message carrying the
// same id. A missing entry on either side causes a 400 from OpenAI/Anthropic.
//
// Strategy (conservative — never drops user/assistant text):
//  1. Collect the set of existing tool_call_ids (pass A: scan tool messages).
//  2. Second pass:
//     - For assistant messages: keep ToolCalls whose id is in the set; drop the rest.
//     If all ToolCalls are dropped and Content is also empty, discard the entire message
//     (pure empty stub).
//     - For tool messages: must find any earlier assistant carrying a tool_calls entry
//     with the same id.
//
// Returns (cleaned slice, mutation count).
func sanitizeHistory(msgs []provider.Message) ([]provider.Message, int) {
	if len(msgs) == 0 {
		return msgs, 0
	}

	// pass A: id set provided by tool messages
	toolIDs := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == provider.RoleTool && m.ToolCallID != "" {
			toolIDs[m.ToolCallID] = true
		}
	}

	out := make([]provider.Message, 0, len(msgs))
	emittedAssistantIDs := make(map[string]bool)
	mutated := 0

	for _, m := range msgs {
		switch m.Role {
		case provider.RoleAssistant:
			if len(m.ToolCalls) == 0 {
				out = append(out, m)
				continue
			}
			kept := m.ToolCalls[:0:0]
			for _, tc := range m.ToolCalls {
				if toolIDs[tc.ID] {
					kept = append(kept, tc)
					emittedAssistantIDs[tc.ID] = true
				} else {
					mutated++
				}
			}
			if len(kept) == 0 && m.Content == "" {
				// Drop entirely: assistant has neither text nor surviving tool_calls
				continue
			}
			m.ToolCalls = kept
			out = append(out, m)

		case provider.RoleTool:
			// Must have a matching, previously emitted assistant.tool_calls entry
			if m.ToolCallID == "" || !emittedAssistantIDs[m.ToolCallID] {
				mutated++
				continue
			}
			out = append(out, m)

		default:
			out = append(out, m)
		}
	}

	return out, mutated
}
