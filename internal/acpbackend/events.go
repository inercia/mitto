package acpbackend

import (
	"context"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// subscription is the Connection's agentbackend.Subscription implementation.
type subscription struct {
	conn   *Connection
	key    string
	fn     func(agentbackend.Event)
	closed bool
}

// Close implements agentbackend.Subscription. Safe to call multiple times.
func (s *subscription) Close() error {
	s.conn.subMu.Lock()
	defer s.conn.subMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	list := s.conn.subs[s.key]
	for i, x := range list {
		if x == s {
			s.conn.subs[s.key] = append(list[:i], list[i+1:]...)
			break
		}
	}
	return nil
}

// Subscribe implements agentbackend.EventDelivery: it registers fn for
// events on ref.ConversationID (or host-wide when ref is the zero value),
// mirroring agentbackend's own fakeHost.Subscribe semantics so the same
// contract tests exercise both implementations identically.
func (c *Connection) Subscribe(ctx context.Context, ref agentbackend.SessionRef, fn func(agentbackend.Event)) (agentbackend.Subscription, error) {
	key := ref.ConversationID
	sub := &subscription{conn: c, key: key, fn: fn}
	c.subMu.Lock()
	c.subs[key] = append(c.subs[key], sub)
	c.subMu.Unlock()
	return sub, nil
}

// publish delivers ev, in order, to subscribers registered for
// ev.Session.ConversationID plus any host-wide subscribers (key ""),
// mirroring agentbackend's fakeHost.publish locking/ordering semantics.
func (c *Connection) publish(ev agentbackend.Event) {
	c.subMu.Lock()
	scoped := c.subs[ev.Session.ConversationID]
	wide := c.subs[""]
	subs := make([]*subscription, 0, len(scoped)+len(wide))
	subs = append(subs, scoped...)
	subs = append(subs, wide...)
	c.subMu.Unlock()
	for _, s := range subs {
		s.fn(ev)
	}
}

// translateSessionUpdate translates one ACP SessionUpdate into a neutral
// Event for ref, returning ok=false when the update kind has no neutral
// analogue yet. AvailableCommandsUpdate/ConfigOptionUpdate/SessionInfoUpdate/
// UsageUpdate/UserMessageChunk are deferred (EventKind has no dedicated slot
// for them yet). ToolCall/ToolCallUpdate/Plan carry no faithful neutral
// payload — Event has no tool-call/plan-specific field — so they are
// delivered as bare marker events (Kind set, Content left nil); full
// tool-call/plan event payload modeling is deferred to the sequence/
// streaming extraction work (mitto-lrt.8).
//
// Origin is always OriginLocal: in the current ACP integration, a session's
// SessionNotification stream only flows while this same Connection has an
// in-flight Prompt call on that session (there is no multi-client scenario
// where another client mutates shared session state independently, unlike
// agentbackend's fakeHost.ResumeSession simulation) — so every translated
// notification is a direct, local-caused consequence, per the adapter plan.
func translateSessionUpdate(ref agentbackend.SessionRef, u acp.SessionUpdate) (agentbackend.Event, bool) {
	base := agentbackend.Event{Session: ref, Origin: agentbackend.OriginLocal, Time: time.Now()}
	switch {
	case u.AgentMessageChunk != nil:
		base.Kind = agentbackend.EventAgentMessage
		if nb, ok := ToNeutralContentBlock(u.AgentMessageChunk.Content); ok {
			base.Content = []agentbackend.ContentBlock{nb}
		}
		return base, true
	case u.AgentThoughtChunk != nil:
		base.Kind = agentbackend.EventAgentThought
		if nb, ok := ToNeutralContentBlock(u.AgentThoughtChunk.Content); ok {
			base.Content = []agentbackend.ContentBlock{nb}
		}
		return base, true
	case u.ToolCall != nil || u.ToolCallUpdate != nil:
		base.Kind = agentbackend.EventToolCall
		return base, true
	case u.Plan != nil:
		base.Kind = agentbackend.EventPlan
		return base, true
	case u.CurrentModeUpdate != nil:
		base.Kind = agentbackend.EventModeChange
		base.Modes = &agentbackend.ModeState{CurrentModeID: string(u.CurrentModeUpdate.CurrentModeId)}
		return base, true
	default:
		return agentbackend.Event{}, false
	}
}

var _ agentbackend.EventDelivery = (*Connection)(nil)
var _ agentbackend.Subscription = (*subscription)(nil)
