//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"fmt"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// TestEventInjector wraps a session.Store and makes it easy to inject synthetic
// events into a session without going through the ACP pipeline. This lets tests
// create precise event histories with known sequence numbers.
//
// Events are appended directly to the store's event log; the BackgroundSession's
// in-memory sequence counter is NOT updated. The load_events handler reads from
// the store directly, so reconnecting clients will still receive the injected
// events via the store read path.
type TestEventInjector struct {
	store     *session.Store
	sessionID string
	t         *testing.T
}

// NewEventInjector creates a new TestEventInjector for the given session.
// The injector uses ts.Store which shares the same on-disk data as the web server.
func NewEventInjector(t *testing.T, ts *TestServer, sessionID string) *TestEventInjector {
	t.Helper()
	return &TestEventInjector{
		store:     ts.Store,
		sessionID: sessionID,
		t:         t,
	}
}

// InjectAgentMessages injects n synthetic agent_message events into the session.
// Returns the seq of the first and last injected event.
// Calls t.Fatalf on any store error.
func (inj *TestEventInjector) InjectAgentMessages(n int) (firstSeq, lastSeq int64) {
	inj.t.Helper()

	meta, err := inj.store.GetMetadata(inj.sessionID)
	if err != nil {
		inj.t.Fatalf("InjectAgentMessages: GetMetadata failed: %v", err)
	}

	firstSeq = int64(meta.EventCount + 1)

	for i := 0; i < n; i++ {
		ev := session.Event{
			Type: session.EventTypeAgentMessage,
			Data: session.AgentMessageData{Text: fmt.Sprintf("<p>Injected agent message %d</p>", i+1)},
		}
		if err := inj.store.AppendEvent(inj.sessionID, ev); err != nil {
			inj.t.Fatalf("InjectAgentMessages: AppendEvent %d failed: %v", i+1, err)
		}
	}

	lastSeq = firstSeq + int64(n) - 1
	inj.t.Logf("Injected %d agent_message events: seq %d-%d", n, firstSeq, lastSeq)
	return firstSeq, lastSeq
}

// InjectUserPrompts injects n synthetic user_prompt events into the session.
// Returns the seq of the first and last injected event.
// Calls t.Fatalf on any store error.
func (inj *TestEventInjector) InjectUserPrompts(n int) (firstSeq, lastSeq int64) {
	inj.t.Helper()

	meta, err := inj.store.GetMetadata(inj.sessionID)
	if err != nil {
		inj.t.Fatalf("InjectUserPrompts: GetMetadata failed: %v", err)
	}

	firstSeq = int64(meta.EventCount + 1)

	for i := 0; i < n; i++ {
		ev := session.Event{
			Type: session.EventTypeUserPrompt,
			Data: session.UserPromptData{Message: fmt.Sprintf("Injected user prompt %d", i+1)},
		}
		if err := inj.store.AppendEvent(inj.sessionID, ev); err != nil {
			inj.t.Fatalf("InjectUserPrompts: AppendEvent %d failed: %v", i+1, err)
		}
	}

	lastSeq = firstSeq + int64(n) - 1
	inj.t.Logf("Injected %d user_prompt events: seq %d-%d", n, firstSeq, lastSeq)
	return firstSeq, lastSeq
}

// InjectMixed injects a mix of user_prompt + agent_message pairs (rounds rounds).
// Each round consists of one user_prompt followed by one agent_message (2 events total per round).
// Returns the seq of the first and last injected event.
// Calls t.Fatalf on any store error.
func (inj *TestEventInjector) InjectMixed(rounds int) (firstSeq, lastSeq int64) {
	inj.t.Helper()

	meta, err := inj.store.GetMetadata(inj.sessionID)
	if err != nil {
		inj.t.Fatalf("InjectMixed: GetMetadata failed: %v", err)
	}

	firstSeq = int64(meta.EventCount + 1)

	for i := 0; i < rounds; i++ {
		promptEv := session.Event{
			Type: session.EventTypeUserPrompt,
			Data: session.UserPromptData{Message: fmt.Sprintf("Injected user prompt, round %d", i+1)},
		}
		if err := inj.store.AppendEvent(inj.sessionID, promptEv); err != nil {
			inj.t.Fatalf("InjectMixed: AppendEvent (user_prompt round %d) failed: %v", i+1, err)
		}

		msgEv := session.Event{
			Type: session.EventTypeAgentMessage,
			Data: session.AgentMessageData{Text: fmt.Sprintf("<p>Injected agent message, round %d</p>", i+1)},
		}
		if err := inj.store.AppendEvent(inj.sessionID, msgEv); err != nil {
			inj.t.Fatalf("InjectMixed: AppendEvent (agent_message round %d) failed: %v", i+1, err)
		}
	}

	// Each round produces 2 events (prompt + message)
	lastSeq = firstSeq + int64(rounds*2) - 1
	inj.t.Logf("Injected %d mixed rounds (%d events): seq %d-%d", rounds, rounds*2, firstSeq, lastSeq)
	return firstSeq, lastSeq
}

// CurrentMaxSeq returns the current highest seq in the session by reading metadata.
// It returns max(MaxSeq, EventCount) to correctly handle sessions that mix
// AppendEvent (updates EventCount only) and RecordEvent (updates both MaxSeq and EventCount).
//
// Background: AppendEvent assigns seq = EventCount+1 and increments EventCount.
// RecordEvent also increments EventCount AND updates MaxSeq. After using AppendEvent
// for injection, EventCount reflects the total stored events while MaxSeq may lag
// behind (still reflecting the last RecordEvent seq). Taking the max ensures we
// always return the true highest seq present in the store.
func (inj *TestEventInjector) CurrentMaxSeq() int64 {
	inj.t.Helper()

	meta, err := inj.store.GetMetadata(inj.sessionID)
	if err != nil {
		inj.t.Fatalf("CurrentMaxSeq: GetMetadata failed: %v", err)
		return 0
	}

	// EventCount is always updated by both AppendEvent and RecordEvent paths.
	// MaxSeq is updated only by RecordEvent, so after AppendEvent-based injection
	// MaxSeq < EventCount. Take the max to get the true highest seq.
	maxSeq := meta.MaxSeq
	if int64(meta.EventCount) > maxSeq {
		maxSeq = int64(meta.EventCount)
	}
	return maxSeq
}
