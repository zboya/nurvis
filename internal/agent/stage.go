package agent

import "context"

// StageResult is a flow-control signal from a stage to the main loop.
type StageResult int

const (
	// Continue proceeds to the next stage (default).
	Continue StageResult = iota
	// RetryIteration skips the remaining stages in this iteration and restarts
	// from the beginning of the next iteration.
	// Used for retries after think truncation or context-overflow compaction.
	RetryIteration
	// NoToolCalls think stage no tool calls
	NoToolCalls
	// BreakLoop finishes the remaining stages in this iteration, then exits the loop.
	BreakLoop
	// AbortRun immediately aborts the entire run (used after logging the error).
	AbortRun
)

// Stage is a single execution unit in the Agent Loop.
//
// Design principles (aligned with AGENTS.md §6):
//   - A stage is stateless; all mutable state lives on RunState.
//   - If Run returns an error, the main loop aborts and emits an agent.run.aborted event.
//   - To control flow, a stage writes state.StageResult (default Continue).
type Stage interface {
	Name() string
	Run(ctx context.Context, state *RunState) error
}

// stageFunc adapts a plain function into a Stage.
type stageFunc struct {
	name string
	run  func(ctx context.Context, state *RunState) error
}

func (s *stageFunc) Name() string                                   { return s.name }
func (s *stageFunc) Run(ctx context.Context, state *RunState) error { return s.run(ctx, state) }
