// Package qq provides the QQ official bot Channel implementation (based on Tencent botgo SDK, WebSocket gateway).
//
// Protocol reference: https://bot.q.qq.com/wiki/develop/api-v2/
// Same approach as LongClaw:
//   - Use botgo.NewOpenAPI + token.QQBotCredentials to maintain authentication
//   - Maintain WebSocket long connection via sessionManager.Start(wsInfo, ...)
//   - Register C2CMessageEventHandler (private chat) + GroupATMessageEventHandler (group @) to receive messages
//   - Send via HTTP API: PostC2CMessage / PostGroupMessage
package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tencent-connect/botgo"
	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/event"
	"github.com/tencent-connect/botgo/openapi"
	"github.com/tencent-connect/botgo/token"
	"golang.org/x/oauth2"

	"github.com/zboya/nurvis/internal/channel"
)

// Config is the configuration for QQ Channel (from channels.config_json).
type Config struct {
	// AppID is the AppID of the QQ Open Platform bot
	AppID string `json:"app_id"`
	// AppSecret is the AppSecret of the QQ Open Platform bot
	AppSecret string `json:"app_secret"`
	// Sandbox indicates whether to use the sandbox environment (default false, use production environment)
	Sandbox bool `json:"sandbox,omitempty"`
}

// Channel implements the channel.Channel interface, interfacing with the QQ official bot.
type Channel struct {
	id  string
	cfg Config

	api            openapi.OpenAPI
	tokenSource    oauth2.TokenSource
	sessionManager botgo.SessionManager
	media          *mediaClient

	ctx    context.Context
	cancel context.CancelFunc

	inCh chan<- channel.Inbound

	mu      sync.RWMutex
	running bool
	seen    map[string]struct{} // Deduplication of inbound message IDs
}

// New creates a QQ Channel instance.
func New(id string, cfgJSON string) (*Channel, error) {
	var cfg Config
	if cfgJSON != "" {
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			return nil, fmt.Errorf("qq: parse config: %w", err)
		}
	}
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, fmt.Errorf("qq: app_id and app_secret are required")
	}
	return &Channel{
		id:   id,
		cfg:  cfg,
		seen: make(map[string]struct{}),
	}, nil
}

func (c *Channel) Type() string      { return "qq" }
func (c *Channel) ChannelID() string { return c.id }

// Start establishes the QQ bot WebSocket long connection and delivers inbound messages to in.
func (c *Channel) Start(ctx context.Context, in chan<- channel.Inbound) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("qq: already running")
	}
	c.inCh = in
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.mu.Unlock()

	// 1. token source + 自动刷新
	creds := &token.QQBotCredentials{
		AppID:     c.cfg.AppID,
		AppSecret: c.cfg.AppSecret,
	}
	c.tokenSource = token.NewQQBotTokenSource(creds)
	if err := token.StartRefreshAccessToken(c.ctx, c.tokenSource); err != nil {
		return fmt.Errorf("qq: start refresh access token: %w", err)
	}

	// 2. OpenAPI client
	c.api = botgo.NewOpenAPI(c.cfg.AppID, c.tokenSource).WithTimeout(5 * time.Second)
	c.media = newMediaClient(c.api, c.tokenSource, c.cfg.Sandbox)

	// 3. Register event handlers: private chat + group @
	intent := event.RegisterHandlers(
		c.handleC2CMessage(),
		c.handleGroupATMessage(),
	)

	// 4. Fetch WS gateway information
	wsInfo, err := c.api.WS(c.ctx, nil, "")
	if err != nil {
		return fmt.Errorf("qq: get websocket info: %w", err)
	}
	slog.Info("qq: got websocket info", "channel_id", c.id, "shards", wsInfo.Shards)

	// 5. Start SessionManager (blocks inside goroutine, listens for ctx exit)
	c.sessionManager = botgo.NewSessionManager()
	go func() {
		if err := c.sessionManager.Start(wsInfo, c.tokenSource, &intent); err != nil {
			slog.Warn("qq: websocket session ended", "channel_id", c.id, "err", err)
		}
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()
	slog.Info("qq: started", "channel_id", c.id)

	// Block until ctx is canceled
	<-c.ctx.Done()
	return c.stopInternal()
}

// Send sends messages via HTTP API.
//
// Routing: based on out.To.Type
//   - "user"  → PostC2CMessage (private chat) / RichMediaMsg with media upload
//   - "group" → PostGroupMessage (group) / Same as group version
//   - Compatibility with old format "group_<id>" → Automatically strip prefix and send as group message
//
// Text and media can coexist: Send text first, then send each media one by one (each media can carry Caption/Name as content).
func (c *Channel) Send(ctx context.Context, out channel.Outbound) error {
	c.mu.RLock()
	running := c.running
	api := c.api
	media := c.media
	c.mu.RUnlock()
	if !running || api == nil {
		return fmt.Errorf("qq: channel not running")
	}

	// Parse recipient
	peerID := out.To.ID
	peerType := out.To.Type
	if peerType == "" && strings.HasPrefix(peerID, "group_") {
		peerType = "group"
		peerID = strings.TrimPrefix(peerID, "group_")
	}
	isGroup := peerType == "group"

	// 1) Text (QQ does not allow empty text, skip if no text)
	if txt := strings.TrimSpace(out.Text); txt != "" {
		msg := &dto.MessageToCreate{Content: txt, MsgType: dto.TextMsg}
		if isGroup {
			if _, err := api.PostGroupMessage(ctx, peerID, msg); err != nil {
				return fmt.Errorf("qq: post group message: %w", err)
			}
		} else {
			if _, err := api.PostC2CMessage(ctx, peerID, msg); err != nil {
				return fmt.Errorf("qq: post c2c message: %w", err)
			}
		}
	}

	// 2) Media (upload each Artifact and send a RichMediaMsg)
	if len(out.Media) == 0 || media == nil {
		return nil
	}
	for i, art := range out.Media {
		// Path priority: If a local path is given but no Data is provided, read from disk as needed (upper layer can only fill Path to reduce bus traffic).
		data := art.Data
		if len(data) == 0 && art.Path != "" {
			b, err := os.ReadFile(art.Path)
			if err != nil {
				slog.Warn("qq: read media file failed",
					"index", i, "path", art.Path, "err", err)
				continue
			}
			data = b
		}
		if len(data) == 0 && art.URL == "" {
			slog.Warn("qq: skip empty artifact", "index", i, "name", art.Name)
			continue
		}
		ft := kindToFileType(art.Kind, art.MimeType)
		// Group messages currently do not support file type, give a clear prompt instead of blocking the main process with an error
		if isGroup && ft == mediaFile {
			slog.Warn("qq: group does not support file type, skipped",
				"name", art.Name, "mime", art.MimeType)
			continue
		}

		// caption: Use explicit Name first, leave empty if none
		caption := art.Name

		var err error
		if isGroup {
			_, err = media.SendGroupMedia(ctx, peerID, caption, ft, data, art.URL)
		} else {
			_, err = media.SendC2CMedia(ctx, peerID, caption, ft, data, art.URL)
		}
		if err != nil {
			return fmt.Errorf("qq: send media[%d] %s: %w", i, art.Name, err)
		}
	}
	return nil
}

// Stop stops the QQ Channel.
func (c *Channel) Stop() error {
	return c.stopInternal()
}

func (c *Channel) stopInternal() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.running = false
	return nil
}

// handleC2CMessage handles QQ private chat messages (C2C).
func (c *Channel) handleC2CMessage() event.C2CMessageEventHandler {
	return func(_ *dto.WSPayload, data *dto.WSC2CMessageData) error {
		if c.isDuplicate(data.ID) {
			return nil
		}
		if data.Author == nil || data.Author.ID == "" {
			slog.Warn("qq: c2c message without author")
			return nil
		}
		content := strings.TrimSpace(data.Content)
		media := convertAttachments(data.Attachments)
		// Only throw up if there is at least one text or media
		if content == "" && len(media) == 0 {
			return nil
		}

		c.dispatch(channel.Inbound{
			ChannelID: c.id,
			Type:      "qq",
			From: channel.Identity{
				ID:   data.Author.ID,
				Type: "user",
			},
			Text:     content,
			Media:    media,
			Ts:       time.Now(),
			RawMsgID: data.ID,
		})
		return nil
	}
}

// handleGroupATMessage handles messages @ the bot in a group.
func (c *Channel) handleGroupATMessage() event.GroupATMessageEventHandler {
	return func(_ *dto.WSPayload, data *dto.WSGroupATMessageData) error {
		if c.isDuplicate(data.ID) {
			return nil
		}
		if data.Author == nil || data.Author.ID == "" {
			slog.Warn("qq: group@ message without author")
			return nil
		}
		content := strings.TrimSpace(data.Content)
		media := convertAttachments(data.Attachments)
		if content == "" && len(media) == 0 {
			return nil
		}

		// Use group_<gid> for the From.ID of group messages to uniquely distinguish in the routing table;
		// Strip prefix and convert back to groupID for PostGroupMessage when sending.
		c.dispatch(channel.Inbound{
			ChannelID: c.id,
			Type:      "qq",
			From: channel.Identity{
				ID:   "group_" + data.GroupID,
				Name: data.Author.ID, // Actual speaker, for upper layer display
				Type: "group",
			},
			Text:     content,
			Media:    media,
			Ts:       time.Now(),
			RawMsgID: data.ID,
		})
		return nil
	}
}

// convertAttachments converts botgo's attachment structure to channel.Artifact.
// QQ's ContentType is like "image/png" / "video/mp4" / "voice".
func convertAttachments(atts []*dto.MessageAttachment) []channel.Artifact {
	if len(atts) == 0 {
		return nil
	}
	result := make([]channel.Artifact, 0, len(atts))
	for _, a := range atts {
		if a == nil || a.URL == "" {
			continue
		}
		kind := channel.MediaKindFile
		switch {
		case strings.HasPrefix(a.ContentType, "image/"):
			kind = channel.MediaKindImage
		case strings.HasPrefix(a.ContentType, "video/"):
			kind = channel.MediaKindVideo
		case strings.HasPrefix(a.ContentType, "audio/"), a.ContentType == "voice":
			kind = channel.MediaKindAudio
		}
		result = append(result, channel.Artifact{
			Kind:     kind,
			Name:     a.FileName,
			MimeType: a.ContentType,
			URL:      a.URL,
		})
	}
	return result
}

func (c *Channel) dispatch(msg channel.Inbound) {
	c.mu.RLock()
	ch := c.inCh
	c.mu.RUnlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default:
		slog.Warn("qq: inbound channel full, dropping", "msg_id", msg.RawMsgID)
	}
}

// isDuplicate uses message ID for in-memory deduplication, removes half when exceeding 10000 to avoid infinite growth.
func (c *Channel) isDuplicate(id string) bool {
	if id == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.seen[id]; ok {
		return true
	}
	c.seen[id] = struct{}{}
	if len(c.seen) > 10000 {
		i := 0
		for k := range c.seen {
			delete(c.seen, k)
			i++
			if i >= 5000 {
				break
			}
		}
	}
	return false
}
