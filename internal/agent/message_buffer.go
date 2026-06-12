package agent

import "github.com/zboya/nurvis/internal/provider"

// MessageBuffer splits the message stream into three segments:
//
//	system  : System prompt, rebuilt each round by stagePrompt (injects workspace/memory/nudge)
//	history : Stable persisted history (target of pruning / compaction)
//	pending : New messages not yet persisted (assistant + tool result + user nudge)
//	          Flushed to history and written to DB at checkpoint / finalize
//
// This split ensures pruning/compaction only touches the history segment;
// pending messages are never truncated due to long tool output within the same round,
// preserving tool_calls / tool pairings.
type MessageBuffer struct {
	system  provider.Message
	history []provider.Message
	pending []provider.Message
}

// NewMessageBuffer creates an empty buffer; system is unset (set later by stagePrompt).
func NewMessageBuffer() *MessageBuffer { return &MessageBuffer{} }

// SetSystem replaces the system prompt; stagePrompt calls this each round to inject latest context.
func (b *MessageBuffer) SetSystem(m provider.Message) { b.system = m }

// System returns the current system prompt (read-only view).
func (b *MessageBuffer) System() provider.Message { return b.system }

// SetHistory replaces history entirely (used for pruning / compaction result write-back).
func (b *MessageBuffer) SetHistory(msgs []provider.Message) {
	b.history = append(b.history[:0], msgs...)
}

// History returns the history segment (read-only slice view).
func (b *MessageBuffer) History() []provider.Message { return b.history }

// AppendHistory appends messages directly to history (used for initial persisted history loading).
func (b *MessageBuffer) AppendHistory(msgs ...provider.Message) {
	b.history = append(b.history, msgs...)
}

// AppendPending adds new messages to the pending segment.
func (b *MessageBuffer) AppendPending(msgs ...provider.Message) {
	b.pending = append(b.pending, msgs...)
}

// Pending returns the pending segment.
func (b *MessageBuffer) Pending() []provider.Message { return b.pending }

// FlushPending moves pending to history and returns the flushed messages (for persistence).
func (b *MessageBuffer) FlushPending() []provider.Message {
	if len(b.pending) == 0 {
		return nil
	}
	flushed := b.pending
	b.history = append(b.history, b.pending...)
	b.pending = nil
	return flushed
}

// All returns a merged slice [system, ...history, ...pending] for sending to the Provider.
func (b *MessageBuffer) All() []provider.Message {
	out := make([]provider.Message, 0, 1+len(b.history)+len(b.pending))
	if b.system.Role != "" {
		out = append(out, b.system)
	}
	out = append(out, b.history...)
	out = append(out, b.pending...)
	return out
}

// Len returns the total message count (including system, if set).
func (b *MessageBuffer) Len() int {
	n := len(b.history) + len(b.pending)
	if b.system.Role != "" {
		n++
	}
	return n
}
