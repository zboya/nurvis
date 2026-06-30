// Package wework provides the WeWork (Enterprise WeChat) Channel implementation.
//
// Inbound: HTTP webhook callback registered at /wework/callback (subscribed in WeWork Admin).
// Outbound: WeWork active push API (https://qyapi.weixin.qq.com), authenticated by access_token.
//
// Phase 1 implements text + image/file media; advanced types (markdown, news) are left as TODO.
//
// Reference: https://developer.work.weixin.qq.com/document/path/90236
package wework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zboya/nurvis/internal/channel"
)

const (
	apiBase           = "https://qyapi.weixin.qq.com/cgi-bin"
	tokenRefreshAhead = 5 * time.Minute
)

// Config is the WeWork Channel configuration (from channels.config_json).
type Config struct {
	// CorpID is the enterprise ID
	CorpID string `json:"corp_id"`
	// CorpSecret is the secret of the self-built application
	CorpSecret string `json:"corp_secret"`
	// AgentID is the AgentID of the self-built application (used when sending messages)
	AgentID int64 `json:"agent_id"`
	// CallbackPort is the local listening port for inbound webhook
	CallbackPort int `json:"callback_port"`
	// CallbackPath is the URL path of the inbound webhook (default /wework/callback)
	CallbackPath string `json:"callback_path,omitempty"`
}

// Channel implements channel.Channel for WeWork.
type Channel struct {
	id  string
	cfg Config

	server *http.Server
	client *http.Client

	mu          sync.RWMutex
	inCh        chan<- channel.Inbound
	accessToken string
	tokenExpiry time.Time

	cancel context.CancelFunc
}

// New creates a WeWork Channel instance.
func New(id string, cfgJSON string) (*Channel, error) {
	var cfg Config
	if cfgJSON != "" {
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			return nil, fmt.Errorf("wework: parse config: %w", err)
		}
	}
	if cfg.CorpID == "" || cfg.CorpSecret == "" {
		return nil, errors.New("wework: corp_id and corp_secret are required")
	}
	if cfg.AgentID == 0 {
		return nil, errors.New("wework: agent_id is required")
	}
	if cfg.CallbackPort == 0 {
		cfg.CallbackPort = 7787
	}
	if cfg.CallbackPath == "" {
		cfg.CallbackPath = "/wework/callback"
	}
	return &Channel{
		id:     id,
		cfg:    cfg,
		client: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *Channel) Type() string      { return "wework" }
func (c *Channel) ChannelID() string { return c.id }

// Start launches the inbound webhook server until ctx is canceled.
func (c *Channel) Start(ctx context.Context, in chan<- channel.Inbound) error {
	c.mu.Lock()
	c.inCh = in
	ctx, c.cancel = context.WithCancel(ctx)
	c.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc(c.cfg.CallbackPath, c.handleCallback)

	c.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", c.cfg.CallbackPort),
		Handler: mux,
	}
	slog.Info("wework: webhook server listening",
		"channel_id", c.id, "port", c.cfg.CallbackPort, "path", c.cfg.CallbackPath)

	errCh := make(chan error, 1)
	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.server.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// Stop terminates the listening server.
func (c *Channel) Stop() error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// handleCallback handles inbound messages.
//
// WeWork official callback uses encrypted XML; here we accept a simplified JSON
// format (plaintext, suitable for self-deployed proxies / mock platforms).
// For production, place an XML decryption proxy in front (out of scope of phase 1).
type inboundPayload struct {
	MsgID    string `json:"msg_id"`
	FromUser string `json:"from_user"`     // user's userid in the enterprise
	ChatID   string `json:"chat_id"`       // group chat id, empty for private chat
	MsgType  string `json:"msg_type"`      // text | image | file | voice | video
	Content  string `json:"content"`       // text content
	MediaURL string `json:"media_url"`     // direct media URL
	MediaName string `json:"media_name"`   // filename
	MimeType string `json:"mime_type"`     // image/png, etc.
}

func (c *Channel) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var p inboundPayload
	if err := json.Unmarshal(body, &p); err != nil {
		slog.Warn("wework: bad inbound payload", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	fromID := p.FromUser
	fromType := "user"
	if p.ChatID != "" {
		fromID = "group_" + p.ChatID
		fromType = "group"
	}

	var media []channel.Artifact
	if p.MediaURL != "" {
		kind := channel.MediaKindFile
		switch {
		case strings.HasPrefix(p.MimeType, "image/"), p.MsgType == "image":
			kind = channel.MediaKindImage
		case strings.HasPrefix(p.MimeType, "video/"), p.MsgType == "video":
			kind = channel.MediaKindVideo
		case strings.HasPrefix(p.MimeType, "audio/"), p.MsgType == "voice":
			kind = channel.MediaKindAudio
		}
		media = append(media, channel.Artifact{
			Kind:     kind,
			Name:     p.MediaName,
			MimeType: p.MimeType,
			URL:      p.MediaURL,
		})
	}

	if strings.TrimSpace(p.Content) == "" && len(media) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	msg := channel.Inbound{
		ChannelID: c.id,
		Type:      "wework",
		From: channel.Identity{
			ID:   fromID,
			Type: fromType,
		},
		Text:     p.Content,
		Media:    media,
		Ts:       time.Now(),
		RawMsgID: p.MsgID,
	}

	c.mu.RLock()
	ch := c.inCh
	c.mu.RUnlock()
	if ch == nil {
		http.Error(w, "not running", http.StatusServiceUnavailable)
		return
	}
	select {
	case ch <- msg:
	default:
		slog.Warn("wework: inbound channel full, drop", "msg_id", p.MsgID)
	}
	w.WriteHeader(http.StatusOK)
}

// Send sends a message to the specified peer.
//
// Routing:
//   - To.Type == "group" (or peer prefixed with "group_") → appchat/send (group chat)
//   - To.Type == "user"  → message/send (single user)
//
// Text and media coexist: text is sent first, then each media one by one.
func (c *Channel) Send(ctx context.Context, out channel.Outbound) error {
	if c.cfg.CorpID == "" || c.cfg.CorpSecret == "" {
		return errors.New("wework: missing credentials")
	}
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}

	peerID := out.To.ID
	peerType := out.To.Type
	if peerType == "" && strings.HasPrefix(peerID, "group_") {
		peerType = "group"
	}
	if peerType == "group" {
		peerID = strings.TrimPrefix(peerID, "group_")
	}

	// 1) Text
	if txt := strings.TrimSpace(out.Text); txt != "" {
		if err := c.sendText(ctx, token, peerType, peerID, txt); err != nil {
			return err
		}
	}

	// 2) Media
	for i, art := range out.Media {
		if err := c.sendMedia(ctx, token, peerType, peerID, art); err != nil {
			return fmt.Errorf("wework: send media[%d] %s: %w", i, art.Name, err)
		}
	}
	return nil
}

// ---------- access token ----------

type tokenResp struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func (c *Channel) getAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry.Add(-tokenRefreshAhead)) {
		t := c.accessToken
		c.mu.RUnlock()
		return t, nil
	}
	c.mu.RUnlock()

	url := fmt.Sprintf("%s/gettoken?corpid=%s&corpsecret=%s", apiBase, c.cfg.CorpID, c.cfg.CorpSecret)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("wework: gettoken: %w", err)
	}
	defer resp.Body.Close()
	var tr tokenResp
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("wework: decode token: %w", err)
	}
	if tr.ErrCode != 0 {
		return "", fmt.Errorf("wework: gettoken errcode=%d %s", tr.ErrCode, tr.ErrMsg)
	}
	c.mu.Lock()
	c.accessToken = tr.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	c.mu.Unlock()
	return tr.AccessToken, nil
}

// ---------- text ----------

func (c *Channel) sendText(ctx context.Context, token, peerType, peerID, text string) error {
	var (
		url  string
		body map[string]any
	)
	if peerType == "group" {
		url = fmt.Sprintf("%s/appchat/send?access_token=%s", apiBase, token)
		body = map[string]any{
			"chatid":  peerID,
			"msgtype": "text",
			"text":    map[string]string{"content": text},
		}
	} else {
		url = fmt.Sprintf("%s/message/send?access_token=%s", apiBase, token)
		body = map[string]any{
			"touser":  peerID,
			"msgtype": "text",
			"agentid": c.cfg.AgentID,
			"text":    map[string]string{"content": text},
		}
	}
	return c.postJSON(ctx, url, body)
}

// ---------- media ----------

type uploadResp struct {
	ErrCode  int    `json:"errcode"`
	ErrMsg   string `json:"errmsg"`
	Type     string `json:"type"`
	MediaID  string `json:"media_id"`
	CreateAt string `json:"created_at"`
}

func (c *Channel) sendMedia(ctx context.Context, token, peerType, peerID string, art channel.Artifact) error {
	mediaType := mapMediaType(art.Kind, art.MimeType)
	mediaID, err := c.uploadMedia(ctx, token, mediaType, art)
	if err != nil {
		return err
	}

	var (
		url  string
		body map[string]any
	)
	if peerType == "group" {
		url = fmt.Sprintf("%s/appchat/send?access_token=%s", apiBase, token)
		body = map[string]any{
			"chatid":  peerID,
			"msgtype": mediaType,
			mediaType: map[string]string{"media_id": mediaID},
		}
	} else {
		url = fmt.Sprintf("%s/message/send?access_token=%s", apiBase, token)
		body = map[string]any{
			"touser":  peerID,
			"msgtype": mediaType,
			"agentid": c.cfg.AgentID,
			mediaType: map[string]string{"media_id": mediaID},
		}
	}
	return c.postJSON(ctx, url, body)
}

func (c *Channel) uploadMedia(ctx context.Context, token, mediaType string, art channel.Artifact) (string, error) {
	data := art.Data
	if len(data) == 0 && art.Path != "" {
		b, err := os.ReadFile(art.Path)
		if err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}
		data = b
	}
	if len(data) == 0 {
		return "", errors.New("empty media payload (URL-only artifacts not supported by wework upload)")
	}
	name := art.Name
	if name == "" {
		name = "file" + filepath.Ext(art.Path)
	}

	var buf bytes.Buffer
	boundary := "----nurvis-wework-" + fmt.Sprint(time.Now().UnixNano())
	w := multipartWriter{buf: &buf, boundary: boundary}
	if err := w.writeFile("media", name, art.MimeType, data); err != nil {
		return "", err
	}
	w.close()

	url := fmt.Sprintf("%s/media/upload?access_token=%s&type=%s", apiBase, token, mediaType)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var ur uploadResp
	if err := json.NewDecoder(resp.Body).Decode(&ur); err != nil {
		return "", err
	}
	if ur.ErrCode != 0 {
		return "", fmt.Errorf("upload errcode=%d %s", ur.ErrCode, ur.ErrMsg)
	}
	return ur.MediaID, nil
}

func mapMediaType(kind, mime string) string {
	switch kind {
	case channel.MediaKindImage:
		return "image"
	case channel.MediaKindVideo:
		return "video"
	case channel.MediaKindAudio:
		return "voice"
	}
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "voice"
	}
	return "file"
}

// ---------- helpers ----------

type apiResp struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func (c *Channel) postJSON(ctx context.Context, url string, body any) error {
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var ar apiResp
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return err
	}
	if ar.ErrCode != 0 {
		return fmt.Errorf("wework api errcode=%d %s", ar.ErrCode, ar.ErrMsg)
	}
	return nil
}

// minimal multipart writer to avoid pulling in mime/multipart with extra allocations.
type multipartWriter struct {
	buf      *bytes.Buffer
	boundary string
}

func (w *multipartWriter) writeFile(field, filename, mime string, data []byte) error {
	if mime == "" {
		mime = "application/octet-stream"
	}
	fmt.Fprintf(w.buf, "--%s\r\n", w.boundary)
	fmt.Fprintf(w.buf, "Content-Disposition: form-data; name=%q; filename=%q\r\n", field, filename)
	fmt.Fprintf(w.buf, "Content-Type: %s\r\n\r\n", mime)
	if _, err := w.buf.Write(data); err != nil {
		return err
	}
	w.buf.WriteString("\r\n")
	return nil
}

func (w *multipartWriter) close() {
	fmt.Fprintf(w.buf, "--%s--\r\n", w.boundary)
}
