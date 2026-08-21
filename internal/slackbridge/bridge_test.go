package slackbridge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

var errForcedDisconnect = errors.New("simulated forced disconnect")

// fakeLoopTriggerer records every TriggerNowWithSlackEvent call for
// assertions, without needing a real LoopRunner/SessionManager/Store graph.
type fakeLoopTriggerer struct {
	mu    sync.Mutex
	calls []fakeTriggerCall
	err   error
}

type fakeTriggerCall struct {
	sessionID string
	firedBy   session.LoopTrigger
	evt       *conversation.PromptSlackContext
}

func (f *fakeLoopTriggerer) TriggerNowWithSlackEvent(sessionID string, resetTimer bool, firedBy session.LoopTrigger, evt *conversation.PromptSlackContext) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeTriggerCall{sessionID: sessionID, firedBy: firedBy, evt: evt})
	return f.err
}

func (f *fakeLoopTriggerer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

const testChannel = "C-TARGET"
const testTeam = "T-TARGET"
const testTargetSession = "sess-target"

func testConfig() Config {
	return Config{TeamID: testTeam, ChannelID: testChannel, TargetSessionID: testTargetSession}
}

// TestBridge_HandleEvent_MatchingEventTriggers verifies acceptance criterion
// #1/#4: a matching event triggers the target session with bounded metadata
// threaded through as a *conversation.PromptSlackContext.
func TestBridge_HandleEvent_MatchingEventTriggers(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)

	b.handleEvent(Event{
		EventID: "Ev1", TeamID: testTeam, ChannelID: testChannel, AuthorID: "U1",
		Kind: "message", Timestamp: "100.1", ThreadTimestamp: "99.1", Text: "hello",
	})

	if trig.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1", trig.callCount())
	}
	call := trig.calls[0]
	if call.sessionID != testTargetSession {
		t.Errorf("sessionID = %q, want %q", call.sessionID, testTargetSession)
	}
	if call.firedBy != TriggerSlack {
		t.Errorf("firedBy = %q, want %q", call.firedBy, TriggerSlack)
	}
	if call.evt == nil || call.evt.EventID != "Ev1" || call.evt.ChannelID != testChannel ||
		call.evt.AuthorID != "U1" || call.evt.Timestamp != "100.1" ||
		call.evt.ThreadTimestamp != "99.1" || call.evt.Text != "hello" {
		t.Errorf("evt = %#v, want fields copied from the Event", call.evt)
	}
}

// TestBridge_HandleEvent_OtherChannelDoesNotTrigger covers acceptance #2.
func TestBridge_HandleEvent_OtherChannelDoesNotTrigger(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)

	b.handleEvent(Event{EventID: "Ev1", TeamID: testTeam, ChannelID: "C-OTHER", AuthorID: "U1", Text: "hi"})

	if trig.callCount() != 0 {
		t.Errorf("callCount = %d, want 0 for a non-matching channel", trig.callCount())
	}
}

// TestBridge_HandleEvent_OtherTeamDoesNotTrigger covers acceptance #2.
func TestBridge_HandleEvent_OtherTeamDoesNotTrigger(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)

	b.handleEvent(Event{EventID: "Ev1", TeamID: "T-OTHER", ChannelID: testChannel, AuthorID: "U1", Text: "hi"})

	if trig.callCount() != 0 {
		t.Errorf("callCount = %d, want 0 for a non-matching team", trig.callCount())
	}
}

func TestBridge_HandleEvent_MissingTeamDoesNotTrigger(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)

	b.handleEvent(Event{EventID: "Ev1", ChannelID: testChannel, AuthorID: "U1", Text: "hi"})

	if trig.callCount() != 0 {
		t.Errorf("callCount = %d, want 0 when team metadata is missing", trig.callCount())
	}
}

func TestBridge_HandleEvent_BoundsSlackText(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)
	text := string(make([]rune, maxSlackTextRunes+1))

	b.handleEvent(Event{EventID: "Ev1", TeamID: testTeam, ChannelID: testChannel, AuthorID: "U1", Text: text})

	if trig.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1", trig.callCount())
	}
	if got := len([]rune(trig.calls[0].evt.Text)); got != maxSlackTextRunes {
		t.Errorf("bounded text length = %d runes, want %d", got, maxSlackTextRunes)
	}
}

// TestBridge_HandleEvent_SelfEventsDoNotTrigger covers acceptance #2's
// "Mitto's own Slack app" half.
func TestBridge_HandleEvent_SelfEventsDoNotTrigger(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)
	b.SetSelfUserID("U-SELF")

	b.handleEvent(Event{EventID: "Ev1", TeamID: testTeam, ChannelID: testChannel, AuthorID: "U-SELF", Text: "hi"})

	if trig.callCount() != 0 {
		t.Errorf("callCount = %d, want 0 for a self-authored event", trig.callCount())
	}
}

// TestBridge_HandleEvent_DuplicateEventIDTriggersOnce covers acceptance #3.
func TestBridge_HandleEvent_DuplicateEventIDTriggersOnce(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)

	evt := Event{EventID: "Ev1", TeamID: testTeam, ChannelID: testChannel, AuthorID: "U1", Text: "hi"}
	b.handleEvent(evt)
	b.handleEvent(evt) // redelivery of the exact same event_id
	b.handleEvent(evt)

	if got := trig.callCount(); got != 1 {
		t.Errorf("callCount = %d, want 1 (dedupe by event_id)", got)
	}
}

// TestBridge_HandleEvent_MissingEventIDDropped verifies an event with no
// event_id is dropped rather than dispatched un-deduplicable.
func TestBridge_HandleEvent_MissingEventIDDropped(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)

	b.handleEvent(Event{TeamID: testTeam, ChannelID: testChannel, AuthorID: "U1", Text: "hi"})

	if trig.callCount() != 0 {
		t.Errorf("callCount = %d, want 0 for an event with no event_id", trig.callCount())
	}
}

// TestBridge_Run_ReconnectsAndDeliversAnotherEvent covers acceptance #5: a
// forced disconnect (the fake source's first Run returning an error after
// one event) is followed by a reconnect and a second event being delivered.
func TestBridge_Run_ReconnectsAndDeliversAnotherEvent(t *testing.T) {
	trig := &fakeLoopTriggerer{}
	b := NewBridge(testConfig(), trig, nil)

	src := &FakeSource{
		Runs: []FakeRun{
			{
				Events: []Event{{EventID: "Ev1", TeamID: testTeam, ChannelID: testChannel, AuthorID: "U1", Text: "first"}},
				Err:    errForcedDisconnect,
			},
			{
				Events: []Event{{EventID: "Ev2", TeamID: testTeam, ChannelID: testChannel, AuthorID: "U1", Text: "second"}},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		b.Run(ctx, src)
		close(done)
	}()

	deadline := time.After(4 * time.Second)
	for trig.callCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for 2 triggers after reconnect, got %d", trig.callCount())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done

	if trig.calls[0].evt.EventID != "Ev1" || trig.calls[1].evt.EventID != "Ev2" {
		t.Errorf("unexpected event order: %#v", trig.calls)
	}
}
