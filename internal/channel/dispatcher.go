// Package channel implements Channel inbound message dispatching:
// after receiving messages from each Channel, routes them to Agent+Session via channel_routes mapping, triggering the Loop.
package channel

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/store/repo"
)

// LoopDispatcher is the interface to trigger Agent Loop (implemented by agent.Manager).
//
// channelID is the primary key of the channels table; channelType is the source label ("qq"/"wechat").
// Splitting them to avoid dual semantics in a single field; replyTo / inboundMedia allows the Agent to get the default target through Scope when responding passively,
// without requiring the model to remember these IDs.
type LoopDispatcher interface {
	DispatchChannel(
		ctx context.Context,
		channelID, channelType, agentID, sessionID, text string,
		replyTo *PeerIdentity,
		inboundMedia []InboundMedia,
	) (string, error)
}

// PeerIdentity is structurally isomorphic to channel.Identity, independently defined to avoid reverse dependencies.
type PeerIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"` // user | group
}

// InboundMedia is a neutral structure used to pass media attachments from Channel to Agent,
// with field names corresponding one-to-one to channel.Artifact / agent.MediaArtifact.
type InboundMedia struct {
	Kind     string `json:"kind,omitempty"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	URL      string `json:"url,omitempty"`
	Path     string `json:"path,omitempty"`
	Data     []byte `json:"data,omitempty"`
}

// Dispatcher manages all Channel instances, uniformly handling inbound messages and routing them to the Agent.
type Dispatcher struct {
	repo       *repo.ChannelRepo
	bus        bus.Bus
	dispatcher LoopDispatcher
	channels   []Channel
	inbound    chan Inbound
	mu         sync.RWMutex
	seen       map[string]time.Time // Message deduplication map: msgID -> first time
}

// NewDispatcher creates a Channel dispatcher.
func NewDispatcher(db *sql.DB, b bus.Bus, d LoopDispatcher) *Dispatcher {
	return &Dispatcher{
		repo:       repo.NewChannelRepo(db),
		bus:        b,
		dispatcher: d,
		inbound:    make(chan Inbound, 256),
		seen:       make(map[string]time.Time),
	}
}

// Register registers a Channel instance.
func (d *Dispatcher) Register(ch Channel) {
	d.mu.Lock()
	d.channels = append(d.channels, ch)
	d.mu.Unlock()
}

// Start starts all Channel listeners and inbound consumption goroutines.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.RLock()
	channels := make([]Channel, len(d.channels))
	copy(channels, d.channels)
	d.mu.RUnlock()

	for _, ch := range channels {
		ch := ch
		go func() {
			if err := ch.Start(ctx, d.inbound); err != nil {
				slog.Warn("channel: start error", "type", ch.Type(), "id", ch.ChannelID(), "err", err)
			}
		}()
	}

	// Start deduplication cleanup goroutine
	go d.cleanSeen(ctx)

	// Start inbound consumption
	go d.consume(ctx)
	return nil
}

// Stop stops all Channels.
func (d *Dispatcher) Stop() {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, ch := range d.channels {
		if err := ch.Stop(); err != nil {
			slog.Warn("channel: stop error", "id", ch.ChannelID(), "err", err)
		}
	}
}

// SendTo actively sends a message to a specified Channel instance (for tool/external calls).
// The difference from sendReply: SendTo does not require the source to be a certain Inbound, but allows Agent tools
// to directly pick the target channel and peer for push.
//
// Returns ErrChannelNotFound if the Channel corresponding to channelID is not found, the caller can use this to downgrade.
func (d *Dispatcher) SendTo(ctx context.Context, channelID string, out Outbound) error {
	d.mu.RLock()
	var target Channel
	for _, ch := range d.channels {
		if ch.ChannelID() == channelID {
			target = ch
			break
		}
	}
	d.mu.RUnlock()
	if target == nil {
		return ErrChannelNotFound
	}
	out.ChannelID = channelID
	if err := target.Send(ctx, out); err != nil {
		return err
	}
	d.bus.Publish(bus.TopicChannelOutbound, out)
	return nil
}

func (d *Dispatcher) consume(ctx context.Context) {
	for {
		select {
		case msg, ok := <-d.inbound:
			if !ok {
				return
			}
			if d.isDuplicate(msg) {
				continue
			}
			d.bus.Publish(bus.TopicChannelInbound, msg)
			go d.route(ctx, msg)
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) route(ctx context.Context, msg Inbound) {
	// Lookup routing table
	agentID, sessionID, found, err := d.repo.ResolveRoute(ctx, msg.ChannelID, msg.From.ID)
	if err != nil {
		slog.Warn("channel: resolve route error", "err", err)
	}
	if !found || agentID == "" {
		// No matching route: lookup the default agent of the channel
		if da, derr := d.repo.DefaultAgent(ctx, msg.ChannelID); derr == nil && da != "" {
			agentID = da
		}
	}
	if agentID == "" {
		slog.Warn("channel: no agent for message", "channel", msg.ChannelID, "from", msg.From.ID)
		return
	}

	// Translate inbound media to neutral structure and pass to Agent
	var inboundMedia []InboundMedia
	if len(msg.Media) > 0 {
		inboundMedia = make([]InboundMedia, 0, len(msg.Media))
		for _, a := range msg.Media {
			inboundMedia = append(inboundMedia, InboundMedia{
				Kind:     a.Kind,
				Name:     a.Name,
				MimeType: a.MimeType,
				URL:      a.URL,
				Path:     a.Path,
				Data:     a.Data,
			})
		}
	}

	// Reply to peer: translate the sender to Agent, let the channel.send tool default to reply here.
	replyTo := &PeerIdentity{
		ID:   msg.From.ID,
		Name: msg.From.Name,
		Type: msg.From.Type,
	}

	// Trigger Agent Loop. The reply is completely decided by the Agent internally (explicitly call channel.send tool),
	// Dispatcher no longer waits for run.completed to automatically reply - this avoids the dual-track coexistence of "automatic reply + tool explicit send"
	// leading to duplicate replies, and also allows the model to choose flexible strategies such as "no reply / multiple replies / cross-channel".
	newSessionID, err := d.dispatcher.DispatchChannel(
		ctx, msg.ChannelID, msg.Type, agentID, sessionID, msg.Text, replyTo, inboundMedia,
	)
	if err != nil {
		slog.Warn("channel: dispatch failed", "err", err)
		return
	}

	// Establish/update route (bind this session)
	if sessionID == "" && newSessionID != "" {
		_ = d.repo.UpsertRoute(ctx, newSessionID, msg.ChannelID, msg.From.ID, agentID, newSessionID)
	}
}

// isDuplicate uses RawMsgID for simple deduplication.
func (d *Dispatcher) isDuplicate(msg Inbound) bool {
	if msg.RawMsgID == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[msg.RawMsgID]; ok {
		return true
	}
	d.seen[msg.RawMsgID] = msg.Ts
	return false
}

// cleanSeen cleans up deduplication records that are more than 5 minutes old every minute.
func (d *Dispatcher) cleanSeen(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-5 * time.Minute)
			d.mu.Lock()
			for k, t := range d.seen {
				if t.Before(cutoff) {
					delete(d.seen, k)
				}
			}
			d.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}
