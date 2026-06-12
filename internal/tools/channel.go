package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zboya/nurvis/internal/provider"
)

// ChannelDispatcher is the minimal dependency facet of channel.Dispatcher at the tools layer,
// avoiding a tools → channel reverse dependency: the app wiring phase adapts *channel.Dispatcher into it.
type ChannelDispatcher interface {
	SendTo(ctx context.Context, channelID string, out ChannelOutbound) error
}

// ChannelOutbound is the outbound message dispatched by a tool to the Dispatcher.
// Fields correspond one-to-one with channel.Outbound, translated by the app adapter.
type ChannelOutbound struct {
	To    ChannelIdentity
	Text  string
	Media []ChannelArtifact
}

// ChannelIdentity describes the target peer.
type ChannelIdentity struct {
	ID   string // QQ: user openid or group_<gid>; WeChat: wxid, etc.
	Type string // user | group
}

// ChannelArtifact describes the media to attach (same name and semantics as channel.Artifact).
type ChannelArtifact struct {
	Kind     string // image | video | audio | file (Channel infers when empty)
	Name     string
	MimeType string
	Path     string // Local absolute path (mutually exclusive with URL / Data)
	URL      string
	Data     []byte
}

// ChannelSend is a tool that proactively pushes a message (text + optional media) to a Channel.
//
// Primary use cases:
//   - Sending notifications to a QQ user/group from cron / scheduled tasks
//   - Explicitly requesting the Agent to "send results to QQ group" from a desktop conversation
//   - Cross-channel forwarding
type ChannelSend struct {
	disp ChannelDispatcher
}

// NewChannelSend constructs the tool instance. When disp is nil, Invoke returns an error directly
// (keeping the Registry registration shape consistent).
func NewChannelSend(disp ChannelDispatcher) *ChannelSend {
	return &ChannelSend{disp: disp}
}

func (*ChannelSend) Name() string { return "channel.send" }

func (*ChannelSend) Description() string {
	return "Reply to the user on the channel that triggered this conversation."
}

func (*ChannelSend) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name: "channel.send",
		Description: "Reply to the user on the channel that triggered this conversation. " +
			"At least one of `text` or `media` must be present. " +
			"`media` items are read from local `path` (preferred for binary) or referenced by public `url`.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Plain text body.",
				},
				"media": map[string]any{
					"type":        "array",
					"description": "Optional media attachments. Each item must provide either `path` (local file) or `url` (public download).",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"kind": map[string]any{
								"type":        "string",
								"description": "image | video | audio | file. Optional; inferred from mime/ext when omitted.",
								"enum":        []string{"image", "video", "audio", "file"},
							},
							"path": map[string]any{
								"type":        "string",
								"description": "Local absolute path (resolved against workspace if relative).",
							},
							"url": map[string]any{
								"type":        "string",
								"description": "Public URL the channel can fetch (alternative to path).",
							},
							"name": map[string]any{
								"type":        "string",
								"description": "Display filename / caption.",
							},
							"mime_type": map[string]any{
								"type":        "string",
								"description": "MIME type, e.g. image/png. Optional; inferred from file extension when omitted.",
							},
						},
					},
				},
			},
		},
	}
}

func (t *ChannelSend) Invoke(ctx context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	if t.disp == nil {
		return &Result{Content: "channel.send: dispatcher unavailable", IsError: true}, nil
	}

	// The target is entirely determined by Scope (injected from RunState during the act stage).
	// This is the current first-phase design choice: in passive response scenarios, the model only
	// needs to care about content and shouldn't have to remember IDs.
	// Active push scenarios (cron/desktop, etc.) are currently handled by the system side
	// launching a separate Loop through other paths, not mixed into this same tool.
	if scope.ChannelID == "" {
		return &Result{
			Content: "channel.send: no channel context for this run (this conversation was not triggered by a channel inbound)",
			IsError: true,
		}, nil
	}
	if scope.ReplyTo == nil || scope.ReplyTo.ID == "" {
		return &Result{
			Content: "channel.send: no reply target available in current context",
			IsError: true,
		}, nil
	}

	channelID := scope.ChannelID
	peerID := scope.ReplyTo.ID
	peerType := scope.ReplyTo.Type
	if peerType == "" {
		peerType = "user"
	}
	peerType = strings.ToLower(peerType)

	var args struct {
		Text  string `json:"text"`
		Media []struct {
			Kind     string `json:"kind"`
			Path     string `json:"path"`
			URL      string `json:"url"`
			Name     string `json:"name"`
			MimeType string `json:"mime_type"`
		} `json:"media"`
	}
	_ = json.Unmarshal(raw, &args)

	// Parse media
	var media []ChannelArtifact
	for i, m := range args.Media {
		art, err := buildArtifact(map[string]any{
			"kind":      m.Kind,
			"path":      m.Path,
			"url":       m.URL,
			"name":      m.Name,
			"mime_type": m.MimeType,
		}, scope.WorkspaceDir)
		if err != nil {
			return &Result{
				Content: fmt.Sprintf("channel.send: media[%d]: %v", i, err),
				IsError: true,
			}, nil
		}
		media = append(media, art)
	}

	if args.Text == "" && len(media) == 0 {
		return &Result{Content: "channel.send: nothing to send (text and media both empty)", IsError: true}, nil
	}

	// Actually send (with a 30s timeout to avoid blocking the Loop)
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out := ChannelOutbound{
		To:    ChannelIdentity{ID: peerID, Type: peerType},
		Text:  args.Text,
		Media: media,
	}
	if err := t.disp.SendTo(sendCtx, channelID, out); err != nil {
		return &Result{
			Content: fmt.Sprintf("channel.send failed: %v", err),
			IsError: true,
		}, nil
	}

	// Return a brief summary to the model (without exposing channel_id / peer ID details to avoid noise)
	summary := map[string]any{
		"ok":        true,
		"text_len":  len(args.Text),
		"media_cnt": len(media),
	}
	b, _ := json.Marshal(summary)
	return &Result{Content: string(b)}, nil
}

// buildArtifact parses a single media entry from args into a ChannelArtifact.
// Rule: path takes priority (read file → Data + infer mime/kind); falls back to url when no path.
func buildArtifact(m map[string]any, workspaceDir string) (ChannelArtifact, error) {
	art := ChannelArtifact{}
	if s, ok := m["kind"].(string); ok {
		art.Kind = strings.ToLower(s)
	}
	if s, ok := m["name"].(string); ok {
		art.Name = s
	}
	if s, ok := m["mime_type"].(string); ok {
		art.MimeType = s
	}
	if s, ok := m["url"].(string); ok {
		art.URL = s
	}

	pathStr, _ := m["path"].(string)
	if pathStr != "" {
		p := pathStr
		if !filepath.IsAbs(p) && workspaceDir != "" {
			p = filepath.Join(workspaceDir, p)
		}
		// Size limit: QQ rich media single file is ~30MB; use 32MB as a safety margin
		const maxBytes = 32 * 1024 * 1024
		fi, err := os.Stat(p)
		if err != nil {
			return art, fmt.Errorf("stat: %w", err)
		}
		if fi.Size() > maxBytes {
			return art, fmt.Errorf("file too large: %d bytes (limit %d)", fi.Size(), maxBytes)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return art, fmt.Errorf("read: %w", err)
		}
		art.Path = p // Keep absolute path for downstream logging / re-validation / future streaming upload
		art.Data = data
		if art.Name == "" {
			art.Name = filepath.Base(p)
		}
		if art.MimeType == "" {
			art.MimeType = mimeFromExt(p)
		}
	}

	if art.URL == "" && len(art.Data) == 0 {
		return art, fmt.Errorf("either path or url is required")
	}

	if art.Kind == "" {
		art.Kind = inferKind(art.MimeType, art.Name)
	}
	return art, nil
}

// mimeFromExt returns a best-effort MIME type from the file extension;
// the standard library's mime.TypeByExtension may not have all types registered in every environment.
func mimeFromExt(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".ogg":
		return "audio/ogg"
	case ".silk", ".amr":
		return "audio/amr"
	}
	// Fallback: let net/http sniff the type to avoid an empty string
	return http.DetectContentType([]byte(ext))
}

func inferKind(mime, name string) string {
	m := strings.ToLower(mime)
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "video/"):
		return "video"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".jpg"),
		strings.HasSuffix(lower, ".jpeg"), strings.HasSuffix(lower, ".gif"),
		strings.HasSuffix(lower, ".webp"):
		return "image"
	case strings.HasSuffix(lower, ".mp4"), strings.HasSuffix(lower, ".mov"),
		strings.HasSuffix(lower, ".webm"):
		return "video"
	case strings.HasSuffix(lower, ".mp3"), strings.HasSuffix(lower, ".wav"),
		strings.HasSuffix(lower, ".m4a"), strings.HasSuffix(lower, ".ogg"),
		strings.HasSuffix(lower, ".silk"), strings.HasSuffix(lower, ".amr"):
		return "audio"
	}
	return "file"
}
