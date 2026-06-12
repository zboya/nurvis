// Package agent implements multi-stage orchestration for the Agent Loop.
//
// Overall design:
//
//	Run = setup []Stage  →  iteration []Stage (loop)  →  finalize []Stage
//
// Each stage implements the unified `Stage` interface, and controls the main loop
// by writing RunState.StageResult (Continue / BreakLoop / AbortRun / RetryIteration).
// Stages are stateless; all mutable state lives on RunState.
//
// Key capabilities:
//   - MessageBuffer three-segment buffer (system / history / pending); pruning/compaction only touches history.
//   - prune stage: two-phase pruning based on token budget → LLM compaction when needed → sanitize.
//   - think stage: finish_reason="length" truncation retry ≤3 times; context overflow → emergency compaction + 1 retry.
//   - act stage: sequential tool execution; results written back to pending inline (observe inlined).
//   - checkpoint stage: flush pending to DB every N rounds for crash recovery.
//   - finalize stage: flush remaining pending + update session + generate summary if needed.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/provider"
	"github.com/zboya/nurvis/internal/skill"
	"github.com/zboya/nurvis/internal/store/repo"
	"github.com/zboya/nurvis/internal/tools"
	"github.com/zboya/nurvis/internal/workspace"
)

// Budget / threshold constants
const (
	defaultMaxHistoryMessages = 200
	defaultReserveTokens      = 256

	maxTruncRetries    = 3
	maxOverflowRetries = 1

	compactKeepRecent = 6

	summarizeMessageThreshold = 10

	defaultMaxRounds = 100
)

// RunState holds mutable state that persists throughout the entire Loop.
type RunState struct {
	AgentID   string
	SessionID string
	ProjectID string
	Workspace string
	Channel   string // Source label: desktop | wechat | qq | cron
	Summary   string

	// ChannelInstanceID / ReplyTo: only populated when the Loop is triggered
	// by a Channel inbound message. Passed through to tools.Scope so channel.send
	// can auto-reply to the original sender when no ID is specified.
	ChannelInstanceID string
	ReplyTo           *PeerIdentity

	Buf *MessageBuffer

	ToolCalls []provider.ToolCall

	Output strings.Builder

	// OutputMedia accumulates media artifacts produced during this Loop that need
	// to be sent back to the Channel. Source: tool.Result.Media from the act stage
	// (e.g. images from image.gen). Published with agent.run.completed after finalize;
	// the Channel Dispatcher converts them to Outbound.Media.
	OutputMedia []MediaArtifact

	StageResult StageResult
	Round       int

	truncRetries    int
	overflowRetries int

	nudge70Done bool
	nudge90Done bool

	userInjected bool
	userText     string

	// SkillRoots is populated after a use_skill tool call; the act stage injects
	// it into Scope so subsequent exec calls can access skill directory env vars.
	SkillRoots map[string]string

	// freshSession is set when historyStage finishes loading. true means this is
	// the first turn of the session (0 history messages loaded), used by finalize
	// to decide whether to set the session label.
	freshSession bool
}

// Loop encapsulates all dependencies for a single Agent Loop.
type Loop struct {
	agent    *Agent
	req      ChatRequest
	sessions *repo.SessionRepo
	messages *repo.MessageRepo
	provider provider.Provider
	toolReg  *tools.Registry
	ws       workspace.Manager
	bus      bus.Bus
	skillMgr *skill.Manager // May be nil (test scenarios)

	contextWindow   int
	maxOutputTokens int
	reserveTokens   int

	// Stage lists assembled by NewLoop; replaceable in tests.
	setup     []Stage
	iteration []Stage
	finalize  []Stage
}

// NewLoop creates a Loop instance.
func NewLoop(
	a *Agent,
	sessionID string,
	req ChatRequest,
	sessions *repo.SessionRepo,
	messages *repo.MessageRepo,
	prov provider.Provider,
	registry *tools.Registry,
	ws workspace.Manager,
	b bus.Bus,
	skillMgr *skill.Manager,
) *Loop {
	req.SessionID = sessionID
	l := &Loop{
		agent:    a,
		req:      req,
		sessions: sessions,
		messages: messages,
		provider: prov,
		toolReg:  registry,
		ws:       ws,
		bus:      b,
		skillMgr: skillMgr,
	}

	maxRounds := a.MaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}

	// Assemble the stage pipeline
	l.setup = []Stage{
		&contextStage{l: l},
		&historyStage{l: l},
		&promptStage{l: l},
	}
	l.iteration = []Stage{
		&prepareStage{l: l, maxRounds: maxRounds},
		&pruneStage{l: l},
		&thinkStage{l: l},
		&actStage{l: l},
		&checkTaskStage{l: l},
	}
	l.finalize = []Stage{
		&finalizeStage{l: l},
	}
	return l
}

func (l *Loop) beforeRun(_ context.Context) error {
	a := l.agent
	const fallbackContextLen = 32 * 1024
	l.contextWindow = a.GetContextWindow(fallbackContextLen)
	l.maxOutputTokens = a.GetMaxOutputTokens(l.contextWindow / 2)
	l.reserveTokens = a.GetReserveTokens(defaultReserveTokens)
	slog.Info("model info",
		"model", a.Model,
		"context_window", l.contextWindow,
		"max_output_tokens", l.maxOutputTokens,
	)
	return nil
}

// Run executes the entire Agent Loop.
func (l *Loop) Run(ctx context.Context) error {
	err := l.beforeRun(ctx)
	if err != nil {
		return err
	}

	state := &RunState{
		AgentID:           l.agent.ID,
		SessionID:         l.req.SessionID,
		ProjectID:         l.req.ProjectID,
		Channel:           l.req.Channel,
		ChannelInstanceID: l.req.ChannelInstanceID,
		ReplyTo:           l.req.ReplyTo,
		Buf:               NewMessageBuffer(),
		userText:          l.req.Text,
		SkillRoots:        make(map[string]string),
	}

	slog.Info("loop: started",
		"agent", l.agent.Name,
		"session", state.SessionID,
		"channel", state.Channel,
		"text_len", len(l.req.Text),
	)
	l.bus.Publish(bus.TopicAgentRunStarted, map[string]any{
		"agent_id":   l.agent.ID,
		"session_id": l.req.SessionID,
	})

	// 1. setup: run sequentially; abort on first failure
	for _, st := range l.setup {
		if err := l.runStage(ctx, st, state); err != nil {
			return l.abort(state, st.Name(), err)
		}
	}

	// 2. iteration: run within maxRounds limit; stages control flow via Result()
	maxRounds := l.agent.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 16
	}
	exited := false
	for !exited && state.Round < maxRounds {
		stop, err := l.runIteration(ctx, state)
		if err != nil {
			return err // runIteration already called abort
		}
		exited = stop
	}
	if state.Round >= maxRounds && !exited {
		slog.Warn("loop: max_rounds reached", "session", state.SessionID, "max_rounds", maxRounds)
	}

	// 3. finalize: errors are downgraded to warn; do not affect run.completed
	finCtx := context.WithoutCancel(ctx)
	for _, st := range l.finalize {
		if err := l.runStage(finCtx, st, state); err != nil {
			slog.Warn("loop/finalize: stage error", "stage", st.Name(), "err", err)
		}
	}

	l.bus.Publish(bus.TopicAgentRunCompleted, map[string]any{
		"agent_id":   l.agent.ID,
		"session_id": l.req.SessionID,
		"output":     state.Output.String(),
		"media":      state.OutputMedia,
	})
	slog.Info("loop: completed",
		"agent", l.agent.Name,
		"session", state.SessionID,
		"rounds", state.Round,
	)
	return nil
}

// runIteration runs one iteration. Returns (shouldStop, error).
//
// Flow-control rules:
//   - RetryIteration: skip remaining stages in this iteration, re-enter the next iteration (Round not incremented).
//     Used for retries after think truncation or context-overflow compaction.
//   - BreakLoop: finish remaining stages in this iteration, then exit (natural end; model gave final reply).
//   - AbortRun: immediately error out; main loop aborts.
//   - Continue: proceed to next stage.
//
// Round increment is handled by checkpointStage (flow-control perspective: "completing a round"
// = running past checkpoint). On RetryIteration, checkpoint is not reached, so Round is not
// incremented — which is the desired behavior.
func (l *Loop) runIteration(ctx context.Context, state *RunState) (bool, error) {
	breakAfter := false
	state.StageResult = Continue
	for _, st := range l.iteration {
		if err := l.runStage(ctx, st, state); err != nil {
			return false, l.abort(state, st.Name(), err)
		}
		switch state.StageResult {
		case RetryIteration:
			return false, nil // re-enter next iteration
		case BreakLoop:
			breakAfter = true // finish remaining stages, then exit
		case AbortRun:
			return false, l.abort(state, st.Name(), fmt.Errorf("stage requested abort"))
		}
	}
	return breakAfter, nil
}

// runStage runs a single stage and emits start/end events.
func (l *Loop) runStage(ctx context.Context, st Stage, state *RunState) error {
	l.emitStage(state, st.Name(), "start")
	err := st.Run(ctx, state)
	l.emitStage(state, st.Name(), "end")
	return err
}

// ─────────────────────────────────────────────────────────────────────────
// Shared helper methods used by stages in stages.go.

// persistMessages batch-persists messages to the database.
//
// partial semantics:
//   - true (mid-run snapshot, called by checkpointStage):
//     Only INSERT messages; do not update session.updated_at, set label, or trigger summary.
//     Emits agent.persisted event with partial=true; frontend can show "saved up to message N".
//     Single Save failure is only logged as warn — next checkpoint / finalize can still recover.
//   - false (final persistence, called by finalizeStage / abort):
//     INSERT messages → update session.updated_at → set label on first turn.
//     These side effects were previously scattered in finalizeStage; now consolidated here
//     so the abort path also gets consistent session metadata updates.
//
// Failure semantics:
//   - Any single message Save failure does not propagate (to avoid blocking finalize / abort paths),
//     but is counted in the failed tally and broadcast with the event for troubleshooting.
func (l *Loop) persistMessages(ctx context.Context, state *RunState, msgs []provider.Message, partial bool) {
	if len(msgs) == 0 {
		return
	}

	now := time.Now()
	saved, failed := 0, 0
	for i, msg := range msgs {
		// Restore original user text for persistence (prompt stage wraps it in <task>)
		if msg.Role == provider.RoleUser &&
			strings.HasPrefix(msg.Content, "<task>") &&
			strings.HasSuffix(strings.TrimSpace(msg.Content), "</task>") {
			msg.Content = state.userText
		}
		var toolCalls any
		if len(msg.ToolCalls) > 0 {
			toolCalls = msg.ToolCalls
		}
		// 1ms offset ensures created_at is monotonically increasing; list query order matches message order
		createdAt := now.Add(time.Duration(i) * time.Millisecond)
		// File paths attached to user messages are stored in media_json
		var mediaJSON string
		if msg.Role == provider.RoleUser && len(l.req.Files) > 0 {
			if b, err := json.Marshal(l.req.Files); err == nil {
				mediaJSON = string(b)
			}
		}
		if err := l.messages.Save(ctx, repo.Message{
			SessionID: state.SessionID,
			Role:      msg.Role,
			Content:   msg.Content,
			ToolCalls: toolCalls,
			ToolName:  msg.Name,
			MediaJSON: mediaJSON,
			CreatedAt: createdAt,
		}); err != nil {
			failed++
			slog.Warn("loop/persist: save message failed",
				"session", state.SessionID,
				"role", msg.Role,
				"partial", partial,
				"err", err,
			)
			continue
		}
		saved++
	}

	// Only update session metadata / set label on final persistence.
	// Mid-run checkpoint does not touch updated_at — avoids a long-running session
	// jumping to the top of the UI list while still in progress.
	if !partial {
		if err := l.sessions.Touch(ctx, state.SessionID, now); err != nil {
			slog.Warn("loop/persist: touch session failed",
				"session", state.SessionID, "err", err)
		}
		if state.freshSession {
			l.maybeSetLabel(ctx, state)
			state.freshSession = false // Idempotent: only set label once on final save
		}
	}

	// Do not emit a "persisted" event to the frontend: no subscriber currently
	// cares about partial vs final. Failures are covered by the slog.warn above.
	// Restore bus.Publish when implementing "crash recovery / persistence health monitoring".
	slog.Debug("loop/persist: done",
		"session", state.SessionID,
		"partial", partial,
		"saved", saved,
		"failed", failed,
	)
}

func (l *Loop) maybeSetLabel(ctx context.Context, state *RunState) {
	if state.userText == "" {
		return
	}
	label := state.userText
	if runes := []rune(label); len(runes) > 50 {
		label = string(runes[:50]) + "…"
	}
	label = strings.ReplaceAll(label, "\n", " ")
	label = strings.ReplaceAll(label, "\r", "")
	_ = l.sessions.SetLabel(ctx, state.SessionID, label)
}

// runSummarize triggers a model summarization and writes back to sessions.summary.
func (l *Loop) runSummarize(ctx context.Context, state *RunState) {
	slog.Info("loop/summarize: generating",
		"session", state.SessionID, "msgs", len(state.Buf.History()))
	l.emitStage(state, "summarize", "start")
	defer l.emitStage(state, "summarize", "end")

	transcript := renderTranscript(state.Buf.History())
	prompt := "Summarize the following conversation in no more than 5 sentences. " +
		"Preserve: the user's goal, key decisions, and any unfinished items.\n\n----\n" + transcript

	chunks, err := l.provider.Chat(ctx, provider.ChatRequest{
		Model:    l.agent.Model,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: prompt}},
		Stream:   false,
	})
	if err != nil {
		slog.Warn("loop/summarize: chat error", "err", err)
		return
	}
	var sb strings.Builder
	for c := range chunks {
		sb.WriteString(c.Content)
	}
	summary := strings.TrimSpace(sb.String())
	if summary != "" {
		_ = l.sessions.SetSummary(ctx, state.SessionID, summary)
	}
}

func (l *Loop) emitStage(state *RunState, stage, event string) {
	l.bus.Publish(bus.TopicAgentStage, map[string]any{
		"session_id": state.SessionID,
		"stage":      stage,
		"event":      event,
	})
}

// abort logs the error, emits an event, and attempts to persist pending messages for post-mortem review.
func (l *Loop) abort(state *RunState, stage string, err error) error {
	slog.Error("loop aborted", "session", state.SessionID, "stage", stage, "err", err)
	l.bus.Publish(bus.TopicAgentRunAborted, map[string]any{
		"session_id": state.SessionID,
		"stage":      stage,
		"error":      err.Error(),
	})
	if state != nil && state.Buf != nil {
		if pending := state.Buf.FlushPending(); len(pending) > 0 {
			l.persistMessages(context.WithoutCancel(context.Background()), state, pending, false)
		}
	}
	return fmt.Errorf("loop[%s]: %w", stage, err)
}
