// Package channel defines the conversation channel abstraction interface and common types.
// Phase 1 implementations: wechat, qq. desktop handles via Gateway directly, not through the Channel interface.
package channel

import (
	"context"
	"errors"
	"time"
)

// ErrChannelNotFound is returned by Dispatcher.SendTo when the specified channel_id does not exist.
var ErrChannelNotFound = errors.New("channel: not found")

// Identity identifies a sender (user or group).
type Identity struct {
	ID   string `json:"id"`   // Unique ID within the platform
	Name string `json:"name"` // Nickname (optional)
	Type string `json:"type"` // user | group
}

// Artifact is a media attachment carried in a message.
type Artifact struct {
	// Kind is the media category: image | video | audio | file (different Channels choose upload channels based on this).
	// When left empty, each Channel infers based on MimeType; if inference fails, it is treated as file.
	Kind     string `json:"kind,omitempty"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	// Path is the absolute path on the backend server (used for generation / download / upload).
	// At least one of Path / URL / Data must have a value; priority: Data > Path > URL.
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
	Data []byte `json:"data,omitempty"`
}

// MediaKind constants for type safety when upper layers fill Artifact.Kind.
const (
	MediaKindImage = "image"
	MediaKindVideo = "video"
	MediaKindAudio = "audio"
	MediaKindFile  = "file"
)

// Inbound is a message received from a Channel.
type Inbound struct {
	ChannelID string     `json:"channel_id"`
	Type      string     `json:"type"` // wechat | qq
	From      Identity   `json:"from"`
	Text      string     `json:"text"`
	Media     []Artifact `json:"media,omitempty"`
	Ts        time.Time  `json:"ts"`
	RawMsgID  string     `json:"raw_msg_id,omitempty"`
}

// Outbound is a message to be sent to a Channel.
type Outbound struct {
	ChannelID string     `json:"channel_id"`
	To        Identity   `json:"to"`
	Text      string     `json:"text"`
	Media     []Artifact `json:"media,omitempty"`
}

// Channel is the conversation channel interface. Each IM platform implements one.
type Channel interface {
	// Type returns the channel type identifier, such as "wechat" / "qq".
	Type() string
	// ChannelID returns the channel instance ID (corresponds to the id field in the channels table).
	ChannelID() string
	// Start starts listening to the channel, delivering received messages to the in channel until ctx is canceled.
	Start(ctx context.Context, in chan<- Inbound) error
	// Send sends a message to the specified recipient.
	Send(ctx context.Context, out Outbound) error
	// Stop stops the channel (graceful shutdown).
	Stop() error
}