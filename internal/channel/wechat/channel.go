// Package wechat provides the skeleton implementation for the WeChat Channel.
// Phase 1 only implements the interface and configuration loading; the specific protocol gateway (Gewechat/personal account) is left as TODO for integration.
package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/zboya/nurvis/internal/channel"
)

// Config is the configuration for the WeChat Channel (from channels.config_json).
type Config struct {
	// GewechatURL is the HTTP address of the Gewechat / personal account gateway
	GewechatURL string `json:"gewechat_url"`
	// Token is used for authentication
	Token string `json:"token"`
	// CallbackPort is the local listening port, used to receive webhook pushes
	CallbackPort int `json:"callback_port"`
}

// Channel implements the channel.Channel interface.
type Channel struct {
	id     string
	cfg    Config
	server *http.Server
	inCh   chan<- channel.Inbound
	mu     sync.Mutex
}

// New creates a WeChat Channel instance.
func New(id string, cfgJSON string) (*Channel, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return nil, fmt.Errorf("wechat: parse config: %w", err)
	}
	if cfg.CallbackPort == 0 {
		cfg.CallbackPort = 7777
	}
	return &Channel{id: id, cfg: cfg}, nil
}

func (c *Channel) Type() string      { return "wechat" }
func (c *Channel) ChannelID() string { return c.id }

// Start starts the HTTP webhook server to receive message pushes from the gateway.
func (c *Channel) Start(ctx context.Context, in chan<- channel.Inbound) error {
	c.mu.Lock()
	c.inCh = in
	c.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/wechat/callback", c.handleCallback)

	c.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", c.cfg.CallbackPort),
		Handler: mux,
	}

	slog.Info("wechat: starting webhook server", "port", c.cfg.CallbackPort)

	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("wechat: server error", "err", err)
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.server.Shutdown(shutCtx)
}

// handleCallback handles webhook pushes from the WeChat gateway (Gewechat format example).
func (c *Channel) handleCallback(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		MsgID   string `json:"msgId"`
		FromID  string `json:"fromId"`
		ToID    string `json:"toId"`
		Content string `json:"content"`
		IsGroup bool   `json:"isGroup"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	fromType := "user"
	if payload.IsGroup {
		fromType = "group"
	}

	msg := channel.Inbound{
		ChannelID: c.id,
		Type:      "wechat",
		From: channel.Identity{
			ID:   payload.FromID,
			Type: fromType,
		},
		Text:     payload.Content,
		Ts:       time.Now(),
		RawMsgID: payload.MsgID,
	}

	c.mu.Lock()
	ch := c.inCh
	c.mu.Unlock()

	if ch != nil {
		select {
		case ch <- msg:
		default:
			slog.Warn("wechat: inbound channel full, dropping message")
		}
	}
	w.WriteHeader(http.StatusOK)
}

// Send sends messages through the WeChat gateway HTTP API.
func (c *Channel) Send(ctx context.Context, out channel.Outbound) error {
	if c.cfg.GewechatURL == "" {
		slog.Warn("wechat: no gateway URL configured, drop outbound message")
		return nil
	}

	// TODO: Implement sending according to the specific gateway API format
	// Example placeholder: POST /sendText
	slog.Info("wechat: [TODO] send message", "to", out.To.ID, "text_len", len(out.Text))
	return nil
}

// Stop stops the WeChat Channel.
func (c *Channel) Stop() error {
	if c.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return c.server.Shutdown(ctx)
	}
	return nil
}
