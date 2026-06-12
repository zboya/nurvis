package agent

import (
	"encoding/json"
	"unicode"

	"github.com/zboya/nurvis/internal/provider"
)

// Heuristic token estimator.
//
// # Design motivation
//
// Phase 1 does not introduce heavy dependencies like tiktoken/sentencepiece, but the
// "whether to trigger" decision for pruning/compaction is not sensitive to count
// precision — it only needs to grow monotonically with message length and have stable
// coefficients.
//
// Estimation formula (character-level BPE approximation):
//   - ASCII letters/digits: ~4 chars / 1 token
//   - CJK ideographs (Chinese/Japanese/Korean): 1 char / ~1.6 tokens (Chinese BPE is typically more fragmented)
//   - Other unicode (punctuation, whitespace, emoji, etc.): 3 chars / 1 token
//
// Tool call schemas and tool_calls JSON structures are estimated by their serialized
// byte length using the same formula.
//
// Error tolerance: ±20%. Much better than truncating by "message count", sufficient
// to drive the two-phase pruning logic.
const (
	asciiCharsPerToken  = 4
	cjkTokensPer10Chars = 16 // 1.6 token/char → multiplied by 10 for integer math
	otherCharsPerToken  = 3
)

// EstimateTokens estimates the token count of a text string.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	var ascii, cjk, other int
	for _, r := range s {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			ascii++
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			cjk++
		default:
			other++
		}
	}
	t := ascii/asciiCharsPerToken + (cjk*cjkTokensPer10Chars)/10 + other/otherCharsPerToken
	if t == 0 && (ascii+cjk+other) > 0 {
		return 1
	}
	return t
}

// CountMessage estimates the token count of a single message (including role marker
// and tool_calls serialization overhead).
func CountMessage(m provider.Message) int {
	t := EstimateTokens(m.Content)
	t += 4 // Fixed overhead approximation for role + separators
	if len(m.ToolCalls) > 0 {
		if b, err := json.Marshal(m.ToolCalls); err == nil {
			t += EstimateTokens(string(b))
		}
	}
	if m.ToolCallID != "" {
		t += EstimateTokens(m.ToolCallID)
	}
	if m.Name != "" {
		t += EstimateTokens(m.Name)
	}
	// images: base64 is a major prompt token consumer, but vision model counting
	// is special; here we roughly estimate by byte length to avoid underestimation.
	for _, img := range m.Images {
		t += len(img) / 4
	}
	return t
}

// CountMessages estimates the total token count of a message list.
func CountMessages(msgs []provider.Message) int {
	total := 0
	for _, m := range msgs {
		total += CountMessage(m)
	}
	return total
}

// CountToolSchemas estimates the token count of tool schemas injected into the prompt.
// LLMs typically serialize schemas into the system segment; here we estimate directly
// by JSON length.
func CountToolSchemas(schemas []provider.ToolSchema) int {
	if len(schemas) == 0 {
		return 0
	}
	b, err := json.Marshal(schemas)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(b))
}
