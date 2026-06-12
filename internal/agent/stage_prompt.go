package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/zboya/nurvis/internal/provider"
)

// ── Stage: prompt ────────────────────────────────────────────────────────

type promptStage struct{ l *Loop }

func (*promptStage) Name() string { return "prompt" }
func (s *promptStage) Run(_ context.Context, state *RunState) error {
	state.Buf.SetSystem(s.l.buildSystemMessage(state))

	// Process user attachments: images go to Images field (multimodal), text is appended to task.
	// Note: chat.send already validated against model vision capability; here we trust requireVision=true.
	userMsg := provider.Message{
		Role: provider.RoleUser,
	}
	taskBody := s.l.req.Text
	if len(s.l.req.Files) > 0 {
		atts, err := LoadAttachments(s.l.req.Files, true)
		if err != nil {
			return fmt.Errorf("prompt: load attachments: %w", err)
		}
		for _, att := range atts {
			switch att.Kind {
			case AttachmentText:
				taskBody += RenderAttachmentText(att)
			case AttachmentImage:
				userMsg.Images = append(userMsg.Images, att.Base64)
			}
		}
	}
	// Channel inbound attachments (images/voice/video from QQ/WeChat users):
	// Current phase only appends an "attachment list" to the task for model awareness
	// (including URL/name/type). Full multimodal byte injection is deferred to a later
	// phase where downloads are performed per mime type and adapted per provider.
	if len(s.l.req.InboundMedia) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\n[Inbound attachments from channel]\n")
		for i, m := range s.l.req.InboundMedia {
			label := m.Name
			if label == "" {
				label = fmt.Sprintf("attachment_%d", i+1)
			}
			kind := m.Kind
			if kind == "" {
				kind = guessMediaKind(m.MimeType, m.Name)
			}
			if m.URL != "" {
				sb.WriteString(fmt.Sprintf("- %s (%s): %s\n", label, kind, m.URL))
			} else {
				sb.WriteString(fmt.Sprintf("- %s (%s): inline %s\n", label, kind, m.MimeType))
			}
		}
		taskBody += sb.String()
	}
	userMsg.Content = fmt.Sprintf("<task>%s</task>", taskBody)
	state.Buf.AppendPending(userMsg)
	state.userInjected = true
	return nil
}
