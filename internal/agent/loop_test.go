package agent

import (
	"testing"

	"github.com/zboya/nurvis/internal/provider"
)

func TestEstimateTokens_Empty(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("empty: got %d, want 0", got)
	}
}

func TestEstimateTokens_AsciiAndCJK(t *testing.T) {
	// Pure ASCII letters: 8 chars → 2 tokens
	if got := EstimateTokens("abcdefgh"); got != 2 {
		t.Errorf("ascii: got %d, want 2", got)
	}
	// CJK 5 chars → 5*1.6 = 8 tokens
	if got := EstimateTokens("你好世界啊"); got != 8 {
		t.Errorf("cjk: got %d, want 8", got)
	}
}

func TestSanitizeHistory_DropsOrphanToolCalls(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "hi"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "a", Name: "read_file"},  // has matching result
			{ID: "b", Name: "write_file"}, // no match → should be removed
		}},
		{Role: provider.RoleTool, ToolCallID: "a", Content: "ok"},
	}
	cleaned, mutated := sanitizeHistory(msgs)
	if mutated == 0 {
		t.Fatalf("expected mutation, got 0")
	}
	if len(cleaned) != 3 {
		t.Fatalf("expected 3 msgs, got %d", len(cleaned))
	}
	if got := len(cleaned[1].ToolCalls); got != 1 {
		t.Errorf("expected 1 tool_call left, got %d", got)
	}
}

func TestSanitizeHistory_DropsOrphanToolMsg(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "hi"},
		// No assistant issued a tool_call with id=z
		{Role: provider.RoleTool, ToolCallID: "z", Content: "result"},
	}
	cleaned, mutated := sanitizeHistory(msgs)
	if mutated != 1 {
		t.Fatalf("expected 1 mutation, got %d", mutated)
	}
	if len(cleaned) != 1 {
		t.Errorf("orphan tool msg should be dropped, got len=%d", len(cleaned))
	}
}

func TestPruneToolResults_TrimsLongTool(t *testing.T) {
	long := make([]rune, 5000)
	for i := range long {
		long[i] = 'x'
	}
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "go"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "1"}}},
		{Role: provider.RoleTool, ToolCallID: "1", Content: string(long)},
	}
	tokensBefore := CountMessages(msgs)
	out, stats := pruneToolResults(msgs, tokensBefore/4) // Give a tight budget
	if stats.ResultsTrimmed == 0 && stats.ResultsCleared == 0 {
		t.Fatalf("expected some prune action, got %+v", stats)
	}
	if CountMessages(out) >= tokensBefore {
		t.Errorf("tokens not reduced: before=%d after=%d", tokensBefore, CountMessages(out))
	}
}

func TestMessageBuffer_Flow(t *testing.T) {
	b := NewMessageBuffer()
	b.SetSystem(provider.Message{Role: provider.RoleSystem, Content: "sys"})
	b.AppendHistory(provider.Message{Role: provider.RoleUser, Content: "u1"})
	b.AppendPending(provider.Message{Role: provider.RoleAssistant, Content: "a1"})

	all := b.All()
	if len(all) != 3 {
		t.Fatalf("All len=%d, want 3", len(all))
	}
	if all[0].Role != provider.RoleSystem ||
		all[1].Role != provider.RoleUser ||
		all[2].Role != provider.RoleAssistant {
		t.Errorf("All ordering wrong: %+v", all)
	}

	flushed := b.FlushPending()
	if len(flushed) != 1 || len(b.Pending()) != 0 || len(b.History()) != 2 {
		t.Errorf("flush state wrong: flushed=%d pending=%d history=%d",
			len(flushed), len(b.Pending()), len(b.History()))
	}
}
