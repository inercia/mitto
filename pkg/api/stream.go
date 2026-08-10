package api

import (
	"context"
	"encoding/json"
	"iter"
	"sync"
)

// EventKind identifies the type of a streamed Event. It mirrors a subset of
// the WebSocket message types handled by handleMessage — only the ones
// meaningful to a streaming consumer are modelled; a caller that needs the
// rest (session_sync, events_loaded, queue_*, keepalive_ack) still sets the
// corresponding SessionCallbacks field, which continues to fire alongside
// the stream (docs/devel/go-client-library.md §6).
type EventKind string

const (
	EventConnected      EventKind = "connected"
	EventAgentMessage   EventKind = "agent_message"
	EventAgentThought   EventKind = "agent_thought"
	EventToolCall       EventKind = "tool_call"
	EventToolUpdate     EventKind = "tool_update"
	EventFileRead       EventKind = "file_read"
	EventFileWrite      EventKind = "file_write"
	EventPermission     EventKind = "permission"
	EventPromptReceived EventKind = "prompt_received"
	EventPromptComplete EventKind = "prompt_complete"
	EventUserPrompt     EventKind = "user_prompt"
	EventError          EventKind = "error"
	EventACPStopped     EventKind = "acp_stopped"
	EventACPStarted     EventKind = "acp_started"
	EventSessionGone    EventKind = "session_gone"
)

// Event is a flat typed record of a single streamed message. Only the
// fields relevant to Kind are populated; the rest are zero. Raw always
// holds the original message payload for callers needing custom decoding.
type Event struct {
	Kind        EventKind
	Seq         int64
	SessionID   string
	ClientID    string
	ACPServer   string
	HTML        string
	Text        string
	ID          string
	Title       string
	Status      string
	Path        string
	Size        int
	RequestID   string
	Description string
	PromptID    string
	SenderID    string
	Message     string
	Reason      string
	EventCount  int
	Raw         json.RawMessage
}

// eventStream is the at-most-one-per-Session channel/iterator adapter
// registered via Events/EventsChan. events is the bounded buffer fed by
// emitStream; errc receives exactly one terminal error, sent before events
// is closed, so a reader that observes a closed events channel can always
// read the terminal error from errc without blocking forever.
type eventStream struct {
	events    chan Event
	errc      chan error
	closeOnce sync.Once
}

func newEventStream(bufSize int) *eventStream {
	if bufSize <= 0 {
		bufSize = defaultStreamBuffer
	}
	return &eventStream{
		events: make(chan Event, bufSize),
		errc:   make(chan error, 1),
	}
}

// terminate ends the stream with err exactly once; later calls are no-ops.
func (es *eventStream) terminate(err error) {
	es.closeOnce.Do(func() {
		es.errc <- err
		close(es.events)
	})
}

// registerStream installs a new eventStream, or returns ErrStreamActive if
// one is already active (at most one stream per Session).
func (s *Session) registerStream() (*eventStream, error) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.stream != nil {
		return nil, ErrStreamActive
	}
	es := newEventStream(s.resilience.streamBuffer)
	s.stream = es
	return es, nil
}

// unregisterStream detaches es if it is still the active stream, allowing a
// subsequent Events/EventsChan call to succeed.
func (s *Session) unregisterStream(es *eventStream) {
	s.streamMu.Lock()
	if s.stream == es {
		s.stream = nil
	}
	s.streamMu.Unlock()
}

// terminateActiveStream detaches and terminates the active stream (if any)
// with err. Safe to call multiple times/concurrently: only the caller that
// observes a non-nil stream under the lock performs the termination.
func (s *Session) terminateActiveStream(err error) {
	s.streamMu.Lock()
	es := s.stream
	s.stream = nil
	s.streamMu.Unlock()
	if es != nil {
		es.terminate(err)
	}
}

// emitStream delivers msg to the active stream, if any, after it has been
// decoded and dispatched to SessionCallbacks. Delivery is non-blocking: a
// full buffer (the consumer is slower than the producer) terminates the
// stream with ErrSlowConsumer rather than blocking the read loop or
// silently dropping the event.
func (s *Session) emitStream(msg wsMessage) {
	s.streamMu.Lock()
	es := s.stream
	s.streamMu.Unlock()
	if es == nil {
		return
	}
	ev, ok := eventFromMessage(msg)
	if !ok {
		return
	}
	select {
	case es.events <- ev:
	default:
		s.terminateActiveStream(ErrSlowConsumer)
	}
}

// Events returns an iterator over this Session's streamed events, the
// primary streaming form (Go 1.25's iter.Seq2). At most one stream may be
// active per Session; a second concurrent call yields (Event{},
// ErrStreamActive) once. The sequence always terminates by yielding exactly
// one (Event{}, err) with a non-nil err — from ctx cancellation, connection
// disconnect, or a slow consumer — except when the caller itself stops
// ranging early (e.g. after observing EventPromptComplete), which needs no
// error. See docs/devel/go-client-library.md §6.
func (s *Session) Events(ctx context.Context) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		es, err := s.registerStream()
		if err != nil {
			yield(Event{}, err)
			return
		}
		defer s.unregisterStream(es)
		for {
			select {
			case <-ctx.Done():
				yield(Event{}, ctx.Err())
				return
			case ev, ok := <-es.events:
				if !ok {
					yield(Event{}, <-es.errc)
					return
				}
				if !yield(ev, nil) {
					return
				}
			}
		}
	}
}

// EventsChan is a select-based variant of Events for callers that prefer
// channels over range-over-func. It shares the same underlying buffer and
// termination semantics: errc receives exactly one terminal error (from ctx
// cancellation or disconnect) and is then closed, alongside out.
func (s *Session) EventsChan(ctx context.Context) (<-chan Event, <-chan error, error) {
	es, err := s.registerStream()
	if err != nil {
		return nil, nil, err
	}
	out := make(chan Event, cap(es.events))
	errc := make(chan error, 1)
	go func() {
		defer s.unregisterStream(es)
		defer close(out)
		defer close(errc)
		for {
			select {
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			case ev, ok := <-es.events:
				if !ok {
					errc <- <-es.errc
					return
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					errc <- ctx.Err()
					return
				}
			}
		}
	}()
	return out, errc, nil
}

// eventFromMessage decodes msg into an Event. ok is false for message types
// the stream does not model (session_sync, events_loaded, queue_*,
// keepalive_ack, and anything unrecognized) — those remain callback-only.
func eventFromMessage(msg wsMessage) (Event, bool) {
	ev := Event{Seq: msg.Seq, Raw: msg.Data}
	switch msg.Type {
	case "connected":
		var d struct {
			SessionID string `json:"session_id"`
			ClientID  string `json:"client_id"`
			ACPServer string `json:"acp_server"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.SessionID, ev.ClientID, ev.ACPServer = EventConnected, d.SessionID, d.ClientID, d.ACPServer
	case "agent_message":
		var d struct {
			HTML string `json:"html"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.HTML, ev.Text = EventAgentMessage, d.HTML, d.Text
	case "agent_thought":
		var d struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.Text = EventAgentThought, d.Text
	case "tool_call":
		var d struct{ ID, Title, Status string }
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.ID, ev.Title, ev.Status = EventToolCall, d.ID, d.Title, d.Status
	case "tool_update":
		var d struct{ ID, Status string }
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.ID, ev.Status = EventToolUpdate, d.ID, d.Status
	case "file_read", "file_write":
		var d struct {
			Path string `json:"path"`
			Size int    `json:"size"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		if msg.Type == "file_read" {
			ev.Kind = EventFileRead
		} else {
			ev.Kind = EventFileWrite
		}
		ev.Path, ev.Size = d.Path, d.Size
	case "permission":
		var d struct {
			RequestID   string `json:"request_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.RequestID, ev.Title, ev.Description = EventPermission, d.RequestID, d.Title, d.Description
	case "prompt_received":
		var d struct {
			PromptID string `json:"prompt_id"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.PromptID = EventPromptReceived, d.PromptID
	case "prompt_complete":
		var d struct {
			EventCount int `json:"event_count"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.EventCount = EventPromptComplete, d.EventCount
	case "user_prompt":
		var d struct {
			SenderID string `json:"sender_id"`
			PromptID string `json:"prompt_id"`
			Message  string `json:"message"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.SenderID, ev.PromptID, ev.Message = EventUserPrompt, d.SenderID, d.PromptID, d.Message
	case "error":
		var d struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.Message = EventError, d.Message
	case "acp_stopped":
		var d struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.Reason = EventACPStopped, d.Reason
	case "acp_started":
		ev.Kind = EventACPStarted
	case "session_gone":
		var d struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(msg.Data, &d)
		ev.Kind, ev.SessionID = EventSessionGone, d.SessionID
	default:
		return Event{}, false
	}
	return ev, true
}
