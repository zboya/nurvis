// Package dingtalk provides the DingTalk Channel implementation.
//
// Inbound: HTTP webhook callback registered at /dingtalk/callback (DingTalk Outgoing
// callback or self-hosted Stream proxy that forwards JSON to this endpoint).
// Outbound: DingTalk message API, supporting:
//   - Group bot custom robot via webhook URL (simple, no token refresh)
//   - Enterprise Internal App push (robot/send) when AppKey + AppSecret + RobotCode are configured
//
// Reference:
//   - Custom Robot:  https://open.dingtalk.com/document/robots/custom-robot-access
//   - Robot Send:    https://open.dingtalk.com/document/orgapp/the-robot-sends-a-group-message
package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zboya/nurvis/internal/channel"
)

const (
	dingTokenURL = "https://oapi.dingtalk.com/gettoken"
	dingRobotAPI = "https://api.dingtalk.com/v1.0/robot/groupMessages/send"
)

// Config is the DingTalk Channel configuration.
type Config struct {
	// WebhookURL is the custom group robot webhook (simple mode). When set, AppKey/AppSecret can be omitted.
	WebhookURL string `json:"webhook_url,omitempty"`
	// Secret is the signing secret for the custom group robot (optional, recommended).
	Secret string `json:"secret,omitempty"`

	// AppKey + AppSecret are for the enterprise internal app (advanced mode).
	AppKey    string `json:"app_key,omitempty"`
	AppSecret string `json:"app_secret,omitempty"`
	// RobotCode is required when using enterprise internal app to send.
	RobotCode string `json:"robot_code,omitempty"`

	// CallbackPort is the local listening port for inbound webhook
	CallbackPort int `json:"callback_port"`
	// CallbackPath defaults to /dingtalk/callback
	CallbackPath string `json:"callback_path,omitempty"`
}

// Channel implements channel.Channel for DingTalk.
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

// New creates a DingTalk Channel instance.
func New(id string, cfgJSON string) (*Channel, error) {
	var cfg Config
	if cfgJSON != "" {
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			return nil, fmt.Errorf("dingtalk: parse config: %w", err)
		}
	}
	if cfg.WebhookURL == "" && (cfg.AppKey == "" || cfg.AppSecret == "") {
		return nil, errors.New("dingtalk: requires either webhook_url or (app_key + app_secret)")
	}
	if cfg.CallbackPort == 0 {
		cfg.CallbackPort = 7788
	}
	if cfg.CallbackPath == "" {
		cfg.CallbackPath = "/dingtalk/callback"
	}
	return &Channel{
		id:     id,
		cfg:    cfg,
		client: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *Channel) Type() string      { return "dingtalk" }
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
	slog.Info("dingtalk: webhook server listening",
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

// inboundPayload mirrors the DingTalk Outgoing callback shape (simplified).
// senderId / chatbotUserId / conversationType: 1=private, 2=group
type inboundPayload struct {
	MsgID            string         `json:"msgId"`
	ConversationID   string         `json:"conversationId"`
	ConversationType string         `json:"conversationType"`
	SenderID         string         `json:"senderId"`
	SenderNick       string         `json:"senderNick"`
	Text             struct{ Content string } `json:"text"`
	MsgType          string         `json:"msgtype"`
	Content          map[string]any `json:"content"`
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
		slog.Warn("dingtalk: bad inbound payload", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	fromID := p.SenderID
	fromType := "user"
	if p.ConversationType == "2" || strings.HasPrefix(p.ConversationID, "cid") {
		fromID = "group_" + p.ConversationID
		fromType = "group"
	}

	text := strings.TrimSpace(p.Text.Content)
	if text == "" {
		// fallback for non-text payloads
		if v, ok := p.Content["content"].(string); ok {
			text = strings.TrimSpace(v)
		}
	}
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	msg := channel.Inbound{
		ChannelID: c.id,
		Type:      "dingtalk",
		From: channel.Identity{
			ID:   fromID,
			Name: p.SenderNick,
			Type: fromType,
		},
		Text:     text,
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
		slog.Warn("dingtalk: inbound channel full, drop", "msg_id", p.MsgID)
	}
	// DingTalk supports inline reply by responding directly with a message body; we
	// use the unified outbound channel.send tool path instead, so simply ack here.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

// Send dispatches text messages. Two transport modes:
//  1. Custom group robot webhook (simple): when WebhookURL is set, post to it directly.
//  2. Enterprise app: use access_token + robot/groupMessages/send for group, no
//     1-to-1 push without ChatBotUserId; we surface a clear error in that case.
//
// Media (image/file) on DingTalk requires uploadMedia + sampleImageMsg; phase 1
// only supports text + markdown-as-text. Media artifacts are reported as warnings.
func (c *Channel) Send(ctx context.Context, out channel.Outbound) error {
	text := strings.TrimSpace(out.Text)
	if text == "" && len(out.Media) > 0 {
		// build a simple markdown listing media URLs as a fallback
		var sb strings.Builder
		sb.WriteString("[media]\n")
		for _, a := range out.Media {
			if a.URL != "" {
				fmt.Fprintf(&sb, "- %s\n", a.URL)
			} else if a.Name != "" {
				fmt.Fprintf(&sb, "- %s\n", a.Name)
			}
		}
		text = sb.String()
	}
	if text == "" {
		return nil
	}

	if c.cfg.WebhookURL != "" {
		return c.sendViaWebhook(ctx, text)
	}
	return c.sendViaApp(ctx, out, text)
}

// ---------- webhook (custom group robot) ----------

func (c *Channel) sendViaWebhook(ctx context.Context, text string) error {
	body := map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.WebhookURL, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var ar struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return err
	}
	if ar.ErrCode != 0 {
		return fmt.Errorf("dingtalk webhook errcode=%d %s", ar.ErrCode, ar.ErrMsg)
	}
	return nil
}

// ---------- enterprise app ----------

type tokenResp struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func (c *Channel) getAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry.Add(-5*time.Minute)) {
		t := c.accessToken
		c.mu.RUnlock()
		return t, nil
	}
	c.mu.RUnlock()

	url := fmt.Sprintf("%s?appkey=%s&appsecret=%s", dingTokenURL, c.cfg.AppKey, c.cfg.AppSecret)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gettoken: %w", err)
	}
	defer resp.Body.Close()
	var tr tokenResp
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	if tr.ErrCode != 0 {
		return "", fmt.Errorf("gettoken errcode=%d %s", tr.ErrCode, tr.ErrMsg)
	}
	c.mu.Lock()
	c.accessToken = tr.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	c.mu.Unlock()
	return tr.AccessToken, nil
}

func (c *Channel) sendViaApp(ctx context.Context, out channel.Outbound, text string) error {
	if c.cfg.RobotCode == "" {
		return errors.New("dingtalk: robot_code is required for enterprise app mode")
	}
	if out.To.Type != "group" && !strings.HasPrefix(out.To.ID, "group_") {
		return errors.New("dingtalk: enterprise app mode currently supports group send only")
	}
	openConvID := strings.TrimPrefix(out.To.ID, "group_")

	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}

	msgParam, _ := json.Marshal(map[string]string{"content": text})
	body := map[string]any{
		"robotCode":      c.cfg.RobotCode,
		"openConversationId": openConvID,
		"msgKey":         "sampleText",
		"msgParam":       string(msgParam),
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, dingRobotAPI, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dingtalk app send http %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
