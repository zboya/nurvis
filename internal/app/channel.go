package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/zboya/nurvis/internal/agent"
	"github.com/zboya/nurvis/internal/channel"
	"github.com/zboya/nurvis/internal/tools"
)

// channelLoopAdapter adapts *agent.Manager to the channel.LoopDispatcher interface.
//
// Both packages maintain their own neutral media types (agent.MediaArtifact / channel.InboundMedia)
// to avoid reverse dependencies; this adapter performs a 1:1 field-by-field translation.
type channelLoopAdapter struct{ agents *agent.Manager }

func (a *channelLoopAdapter) DispatchChannel(
	ctx context.Context,
	channelID, channelType, agentID, sessionID, text string,
	replyTo *channel.PeerIdentity,
	inboundMedia []channel.InboundMedia,
) (string, error) {
	var ams []agent.MediaArtifact
	if len(inboundMedia) > 0 {
		ams = make([]agent.MediaArtifact, 0, len(inboundMedia))
		for _, m := range inboundMedia {
			ams = append(ams, agent.MediaArtifact{
				Kind:     m.Kind,
				Name:     m.Name,
				MimeType: m.MimeType,
				URL:      m.URL,
				Path:     m.Path,
				Data:     m.Data,
			})
		}
	}
	var rt *agent.PeerIdentity
	if replyTo != nil {
		rt = &agent.PeerIdentity{
			ID:   replyTo.ID,
			Name: replyTo.Name,
			Type: replyTo.Type,
		}
	}
	return a.agents.DispatchChannel(ctx, channelID, channelType, agentID, sessionID, text, rt, ams)
}

// channelToolAdapter adapts *channel.Dispatcher to the tools.ChannelDispatcher interface.
//
// It solves the assembly order problem: the tool registry (step 6) needs a dispatcher,
// but the dispatcher is not created until step 12. A shell is registered first, and the
// real dispatcher is injected via setDispatcher once ready. Calling the tool before
// injection returns a friendly "unavailable" error.
type channelToolAdapter struct {
	mu   sync.RWMutex
	disp *channel.Dispatcher
}

func (a *channelToolAdapter) setDispatcher(d *channel.Dispatcher) {
	a.mu.Lock()
	a.disp = d
	a.mu.Unlock()
}

func (a *channelToolAdapter) get() *channel.Dispatcher {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.disp
}

func (a *channelToolAdapter) SendTo(ctx context.Context, channelID string, out tools.ChannelOutbound) error {
	d := a.get()
	if d == nil {
		return fmt.Errorf("channel dispatcher not ready")
	}
	media := make([]channel.Artifact, 0, len(out.Media))
	for _, m := range out.Media {
		media = append(media, channel.Artifact{
			Kind:     m.Kind,
			Name:     m.Name,
			MimeType: m.MimeType,
			Path:     m.Path,
			URL:      m.URL,
			Data:     m.Data,
		})
	}
	return d.SendTo(ctx, channelID, channel.Outbound{
		To: channel.Identity{
			ID:   out.To.ID,
			Type: out.To.Type,
		},
		Text:  out.Text,
		Media: media,
	})
}
