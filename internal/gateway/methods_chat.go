package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zboya/nurvis/internal/agent"
)

// ── chat ─────────────────────────────────────────────────────────────────────

func (m *Methods) handleChatSend(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var req agent.ChatRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if req.AgentID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "agent_id required"}
	}
	if req.Text == "" && len(req.Files) == 0 {
		return nil, &RPCError{Code: "invalid_params", Message: "text or files required"}
	}
	req.Channel = "desktop"

	// Pre-validate attachments only for to-text agents. For to-image / to-video
	// the file becomes the gosd InitImage and is handled by the media loop.
	if len(req.Files) > 0 {
		a, err := m.Agents.Get(ctx, req.AgentID)
		if err != nil {
			return nil, fmt.Errorf("chat.send: %w", err)
		}
		if a.Tag == agent.TagToText || a.Tag == "" {
			hasVision, err := m.modelHasVision(ctx, a.Model)
			if err != nil {
				// Probe failure does not block — conservatively treat as "no vision";
				// an actual error will only surface when images are truly present.
				hasVision = false
			}
			if _, err := agent.LoadAttachments(req.Files, hasVision); err != nil {
				return nil, &RPCError{Code: "invalid_attachment", Message: err.Error()}
			}
		}
	}

	sessionID, err := m.Agents.Dispatch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("chat.send: %w", err)
	}
	return map[string]any{"session_id": sessionID}, nil
}

// modelHasVision determines whether a model supports vision input.
//
// In the yzma backend there is no protocol-level capability flag, so we infer
// from the GGUF filename. This intentionally errs on the side of "no" — the
// real failure mode (sending image bytes to a text-only model) only happens
// when an attachment is actually provided, at which point the loop will
// surface a clear error.
func (m *Methods) modelHasVision(_ context.Context, model string) (bool, error) {
	if model == "" {
		return false, nil
	}
	lower := strings.ToLower(model)
	if strings.Contains(lower, "vl") ||
		strings.Contains(lower, "vision") ||
		strings.Contains(lower, "gemma-3") ||
		strings.Contains(lower, "gemma3") {
		return true, nil
	}
	return false, nil
}

func (m *Methods) handleChatAbort(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "session_id required"}
	}
	m.Agents.Abort(p.SessionID)
	return map[string]any{"ok": true}, nil
}

func (m *Methods) handleChatHistory(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"session_id"`
		Limit     int    `json:"limit"`
		Before    int64  `json:"before"` // Unix ms, cursor-based pagination
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: "invalid_params", Message: err.Error()}
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: "invalid_params", Message: "session_id required"}
	}
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}

	records, err := m.Messages.ListBefore(ctx, p.SessionID, p.Before, p.Limit)
	if err != nil {
		return nil, err
	}

	type toolCallItem struct {
		ID        string         `json:"id"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		Result    string         `json:"result,omitempty"`
		IsError   bool           `json:"isError,omitempty"`
	}
	type mediaItem struct {
		Kind     string `json:"kind,omitempty"`
		Name     string `json:"name,omitempty"`
		MimeType string `json:"mime_type,omitempty"`
		Path     string `json:"path,omitempty"`
		URL      string `json:"url,omitempty"`
	}
	type msg struct {
		ID        string         `json:"id"`
		Role      string         `json:"role"`
		Content   string         `json:"content,omitempty"`
		ToolCalls []toolCallItem `json:"tool_calls,omitempty"`
		ToolName  string         `json:"tool_name,omitempty"`
		Files     []string       `json:"files,omitempty"`
		Media     []mediaItem    `json:"media,omitempty"`
		CreatedAt int64          `json:"created_at"`
	}

	// Parse tool_calls_json into []toolCallItem
	parseToolCalls := func(raw any) []toolCallItem {
		if raw == nil {
			return nil
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var items []toolCallItem
		if err := json.Unmarshal(b, &items); err != nil {
			return nil
		}
		return items
	}

	// Build tool_call_id → tool result message index (for backfilling result).
	// Tool messages store role=tool, Name=tool name, Content=result.
	// Assistant messages carry tool_calls with IDs, but tool messages don't store tool_call_id.
	// Pair them sequentially: each assistant tool_calls message is followed by
	// the corresponding number of tool messages.
	type rawRec = struct {
		id        string
		role      string
		content   string
		toolCalls []toolCallItem
		toolName  string
		mediaJSON string
		createdAt int64
	}

	recs := make([]rawRec, 0, len(records))
	for _, rec := range records {
		recs = append(recs, rawRec{
			id:        rec.ID,
			role:      rec.Role,
			content:   rec.Content,
			toolCalls: parseToolCalls(rec.ToolCalls),
			toolName:  rec.ToolName,
			mediaJSON: rec.MediaJSON,
			createdAt: rec.CreatedAt.UnixMilli(),
		})
	}

	msgs := make([]msg, 0, len(recs))
	i := 0
	for i < len(recs) {
		rec := recs[i]
		switch rec.role {
		case "assistant":
			if len(rec.toolCalls) > 0 {
				// Assistant initiated tool calls; backfill subsequent tool results.
				// Each tool_call maps to one following role=tool message.
				tcs := rec.toolCalls
				j := i + 1
				for k := range tcs {
					if j < len(recs) && recs[j].role == "tool" {
						tcs[k].Result = recs[j].content
						j++
					}
				}
				// Output each tool_call as a separate role=tool message (consistent with streaming)
				for _, tc := range tcs {
					msgs = append(msgs, msg{
						ID:        rec.id + "_" + tc.ID,
						Role:      "tool",
						ToolCalls: []toolCallItem{tc},
						ToolName:  tc.Name,
						CreatedAt: rec.createdAt,
					})
				}
				i = j // skip consumed tool messages
			} else if strings.TrimSpace(rec.content) != "" {
				// Plain assistant text reply (may carry generated media for
				// to-image / to-video agents).
				var media []mediaItem
				if rec.mediaJSON != "" {
					_ = json.Unmarshal([]byte(rec.mediaJSON), &media)
				}
				// The URL persisted into media_json points to the preview
				// registry (in-memory, TTL-bound), so after a restart or
				// 6h expiry it returns 404. Re-issue a fresh URL from the
				// stable local path on every history read; the registry
				// reuses the existing token for the same directory.
				if m.Agents != nil && m.Agents.MediaPreviewURL != nil {
					for k := range media {
						if media[k].Path == "" {
							continue
						}
						if u, err := m.Agents.MediaPreviewURL(media[k].Path); err == nil {
							media[k].URL = u
						}
					}
				}
				msgs = append(msgs, msg{
					ID:        rec.id,
					Role:      rec.role,
					Content:   rec.content,
					Media:     media,
					CreatedAt: rec.createdAt,
				})
				i++
			} else {
				i++ // skip empty assistant message
			}
		case "tool":
			// Already consumed in the assistant branch; standalone occurrence means data anomaly, skip
			i++
		default:
			// user / system etc. — pass through directly
			var files []string
			if rec.mediaJSON != "" {
				_ = json.Unmarshal([]byte(rec.mediaJSON), &files)
			}
			msgs = append(msgs, msg{
				ID:        rec.id,
				Role:      rec.role,
				Content:   rec.content,
				Files:     files,
				CreatedAt: rec.createdAt,
			})
			i++
		}
	}
	return map[string]any{"messages": msgs}, nil
}
