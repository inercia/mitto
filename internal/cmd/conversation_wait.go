package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	client "github.com/inercia/mitto/pkg/api"
)

// queueGate correlates the "queue_message_sending" WebSocket event (fired by
// SessionCallbacks.OnQueueMessageSending, since queue_* events are not
// modelled by the Event stream) with the ID returned by the REST enqueue
// call, which only becomes known after the WebSocket is already connected
// and streaming (see conversation_send.go's ordering). onQueueMessageSending
// and setWant can race in either order, so both sides record what they've
// seen and check the other's state before deciding whether to fire.
type queueGate struct {
	mu       sync.Mutex
	wantID   string
	haveWant bool
	sentIDs  map[string]bool
	matched  chan struct{}
	once     sync.Once
}

func newQueueGate() *queueGate {
	return &queueGate{sentIDs: make(map[string]bool), matched: make(chan struct{})}
}

// onQueueMessageSending is a SessionCallbacks.OnQueueMessageSending value.
func (g *queueGate) onQueueMessageSending(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.haveWant && g.wantID == id {
		g.fireLocked()
		return
	}
	g.sentIDs[id] = true
}

// setWant records the message ID this command is waiting on, firing
// immediately if a matching "sending" notification already arrived.
func (g *queueGate) setWant(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.wantID = id
	g.haveWant = true
	if g.sentIDs[id] {
		g.fireLocked()
	}
}

func (g *queueGate) fireLocked() {
	g.once.Do(func() { close(g.matched) })
}

// waitResult is the --wait outcome, emitted as a single object for
// --output json/yaml (docs/devel/cli-conversation.md; the DDR's
// SDK-response-types-only rule doesn't apply here since this composes two
// pieces of information — the queued message and the reply — that have no
// single REST representation).
type waitResult struct {
	Queued     *client.QueuedMessage `json:"queued"`
	Message    string                `json:"message"`
	EventCount int                   `json:"event_count"`
}

// waitForQueuedMessage ranges over evCh/errCh (from Session.EventsChan,
// registered before the enqueue REST call so no event is missed) until the
// message identified by gate.setWant (called by the caller once the REST
// enqueue response is known) finishes its turn, a terminal event/error
// occurs, or ctx's deadline (--wait-timeout) expires.
//
// Progress (tool calls, thoughts, permissions, file I/O) is always written
// to stderr. The agent's markdown body is streamed live to stdout only when
// streamText is true (table output); otherwise it is only accumulated for
// the caller to render as a single --output json/yaml object.
func waitForQueuedMessage(ctx context.Context, evCh <-chan client.Event, errCh <-chan error, gate *queueGate, streamText bool, stdout, stderr io.Writer) (*waitResult, error) {
	var (
		sawSending bool
		curSeq     int64 = -1
		printed    string
		eventCount int
	)

	matchedCh := gate.matched

	for {
		// Priority check: consume a pending "sending" notification before
		// considering any already-buffered stream event. A plain two-case
		// select does not preserve which channel became ready first — if this
		// goroutine is scheduled late enough that both matchedCh (closed some
		// time ago) and evCh (an event for OUR turn, arrived after) are ready
		// simultaneously, select could pick evCh first and this loop would
		// treat our own first chunk(s) as belonging to someone else's
		// in-flight turn (silently dropped by the !sawSending branch below).
		// Rare in practice (the gap is normally microseconds vs. the
		// network/agent latency before any reply arrives), but deterministic
		// to close: never let an evCh read jump ahead of an already-fired gate.
		if matchedCh != nil {
			select {
			case <-matchedCh:
				sawSending = true
				matchedCh = nil
			default:
			}
		}

		select {
		case <-matchedCh:
			sawSending = true
			matchedCh = nil // never select on it again

		case ev, ok := <-evCh:
			if !ok {
				werr := <-errCh
				if errors.Is(werr, context.DeadlineExceeded) {
					return nil, newExitCodeError(exitWaitTimeout,
						fmt.Errorf("timed out waiting for the agent to finish (the message is still queued/running server-side): %w", werr))
				}
				return nil, fmt.Errorf("connection lost while waiting for completion: %w", werr)
			}

			switch ev.Kind {
			case client.EventAgentMessage:
				if !sawSending {
					// Belongs to an in-flight turn that predates ours.
					continue
				}
				if ev.Seq != curSeq {
					if curSeq != -1 && streamText {
						fmt.Fprintln(stdout)
					}
					curSeq = ev.Seq
					printed = ""
				}
				delta := ev.Text
				if strings.HasPrefix(ev.Text, printed) {
					delta = ev.Text[len(printed):]
				}
				printed = ev.Text
				if streamText && delta != "" {
					fmt.Fprint(stdout, delta)
				}

			case client.EventPromptComplete:
				if sawSending {
					if streamText && printed != "" && !strings.HasSuffix(printed, "\n") {
						fmt.Fprintln(stdout)
					}
					eventCount = ev.EventCount
					return &waitResult{Message: printed, EventCount: eventCount}, nil
				}
				// Someone else's turn finished; keep waiting for ours.
				curSeq = -1
				printed = ""

			case client.EventError:
				return nil, fmt.Errorf("agent reported an error while waiting: %s", ev.Message)

			case client.EventACPStopped:
				return nil, fmt.Errorf("agent connection stopped while waiting (reason: %s)", ev.Reason)

			case client.EventSessionGone:
				return nil, fmt.Errorf("conversation was deleted while waiting")

			case client.EventToolCall:
				fmt.Fprintf(stderr, "[tool] %s: %s\n", ev.Title, ev.Status)

			case client.EventToolUpdate:
				fmt.Fprintf(stderr, "[tool] %s: %s\n", ev.ID, ev.Status)

			case client.EventAgentThought:
				fmt.Fprintf(stderr, "[thinking] %s\n", ev.Text)

			case client.EventPermission:
				fmt.Fprintf(stderr, "[permission] %s: %s\n", ev.Title, ev.Description)

			case client.EventFileRead, client.EventFileWrite:
				fmt.Fprintf(stderr, "[file] %s (%d bytes)\n", ev.Path, ev.Size)
			}
		}
	}
}
