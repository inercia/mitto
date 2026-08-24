package slackbridge

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

type durableTriggerCall struct {
	sessionID string
	events    []conversation.PromptSlackEvent
}

type durableRunner struct {
	mu       sync.Mutex
	calls    []durableTriggerCall
	outcomes map[string][]error
}

func (r *durableRunner) TriggerNowWithSlackEvents(sessionID string, _ bool, firedBy session.LoopTrigger, events []conversation.PromptSlackEvent) error {
	if firedBy != session.TriggerOnSlack || len(events) == 0 {
		return errors.New("unexpected Slack dispatch")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, durableTriggerCall{sessionID: sessionID, events: append([]conversation.PromptSlackEvent(nil), events...)})
	if len(r.outcomes[sessionID]) == 0 {
		return nil
	}
	err := r.outcomes[sessionID][0]
	r.outcomes[sessionID] = r.outcomes[sessionID][1:]
	return err
}

func (r *durableRunner) snapshot() []durableTriggerCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]durableTriggerCall(nil), r.calls...)
}

func routedManager(t *testing.T, runner *durableRunner, sessionIDs ...string) *Manager {
	t.Helper()
	m := NewManager(nil, nil, nil, runner, nil)
	t.Cleanup(m.Close)
	m.sessions = make(map[string][]resolvedSubscription)
	for _, sessionID := range sessionIDs {
		m.sessions[sessionID] = []resolvedSubscription{{sessionID: sessionID, appID: "app", installationID: "install",
			teamID: "team", channelID: "channel", eventMode: session.SlackEventModeAnyHumanMessage, threadPolicy: session.SlackThreadPolicyAny}}
	}
	return m
}

func TestManagerRetriesContentionAfterConversationIdle(t *testing.T) {
	for name, contention := range map[string]error{
		"session_busy":   conversation.ErrSessionBusy,
		"workspace_busy": conversation.ErrWorkspaceBusy,
		"coalesced":      conversation.ErrLoopDispatchCoalesced,
	} {
		t.Run(name, func(t *testing.T) {
			runner := &durableRunner{outcomes: map[string][]error{"s": {contention, nil}}}
			manager := routedManager(t, runner, "s")
			event := Event{EventID: "e1", TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "message", Text: "hello"}
			if err := manager.routeEvent("app", nil, event); err != nil {
				t.Fatal(err)
			}
			stats, _ := manager.journal.Stats("app")
			if len(runner.snapshot()) != 1 || stats.Pending != 1 || stats.Failed != 0 {
				t.Fatalf("after contention calls=%d stats=%+v", len(runner.snapshot()), stats)
			}
			manager.OnConversationIdle("s")
			stats, _ = manager.journal.Stats("app")
			if len(runner.snapshot()) != 2 || stats.Delivered != 1 || stats.Pending != 0 {
				t.Fatalf("after idle calls=%d stats=%+v", len(runner.snapshot()), stats)
			}
			if err := manager.routeEvent("app", nil, event); err != nil || len(runner.snapshot()) != 2 {
				t.Fatalf("duplicate redelivery err=%v calls=%d", err, len(runner.snapshot()))
			}
		})
	}
}

func TestManagerFanoutProgressIsIndependent(t *testing.T) {
	runner := &durableRunner{outcomes: map[string][]error{"a-busy": {conversation.ErrSessionBusy, nil}}}
	manager := routedManager(t, runner, "a-busy", "b-ready")
	event := Event{EventID: "fanout", TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "message"}
	if err := manager.routeEvent("app", nil, event); err != nil {
		t.Fatal(err)
	}
	stats, _ := manager.journal.Stats("app")
	if stats.Pending != 1 || stats.Delivered != 1 || len(runner.snapshot()) != 2 {
		t.Fatalf("partial fanout calls=%#v stats=%+v", runner.snapshot(), stats)
	}
	manager.OnConversationIdle("a-busy")
	stats, _ = manager.journal.Stats("app")
	if stats.Delivered != 2 || stats.Pending != 0 || len(runner.snapshot()) != 3 {
		t.Fatalf("completed fanout calls=%#v stats=%+v", runner.snapshot(), stats)
	}
}

func TestManagerPersistsNoRecipientTombstone(t *testing.T) {
	// An event with zero recipients must never be journaled at all
	// (mitto-d8y): permanent empty-recipient records are only reclaimed
	// after the 24h retention window and can pin the journal's hard cap on a
	// busy channel with no subscribed session.
	runner := &durableRunner{}
	manager := routedManager(t, runner)
	event := Event{EventID: "unmatched", TeamID: "team", ChannelID: "elsewhere", AuthorID: "human", Kind: "message", Text: "discard"}
	if err := manager.routeEvent("app", nil, event); err != nil {
		t.Fatal(err)
	}
	doc := readJournalDocument(t, manager.journal, "app")
	if len(doc.Records) != 0 {
		t.Fatalf("no-recipient event was journaled=%#v", doc.Records)
	}
	if err := manager.routeEvent("app", nil, event); err != nil || len(readJournalDocument(t, manager.journal, "app").Records) != 0 {
		t.Fatalf("repeated unmatched event err=%v", err)
	}
}

func TestManagerFailedDispatchUsesSanitizedBackoffThenRetries(t *testing.T) {
	runner := &durableRunner{outcomes: map[string][]error{"s": {errors.New("raw failure detail"), nil}}}
	manager := routedManager(t, runner, "s")
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	manager.journal.now = func() time.Time { return now }
	if err := manager.routeEvent("app", nil, Event{EventID: "retry", TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "message"}); err != nil {
		t.Fatal(err)
	}
	doc := readJournalDocument(t, manager.journal, "app")
	recipient := doc.Records[0].Recipients[0]
	if recipient.State != recipientFailed || recipient.Attempts != 1 || recipient.LastErrorClass != "dispatch" || !recipient.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("failed recipient=%#v", recipient)
	}
	manager.OnConversationIdle("s")
	if len(runner.snapshot()) != 1 {
		t.Fatal("failed recipient retried before backoff elapsed")
	}
	now = now.Add(time.Minute)
	manager.OnConversationIdle("s")
	stats, _ := manager.journal.Stats("app")
	if len(runner.snapshot()) != 2 || stats.Delivered != 1 || stats.Failed != 0 {
		t.Fatalf("retry calls=%d stats=%+v", len(runner.snapshot()), stats)
	}
}

func TestManagerSettlesAndDrainsBoundedOrderedBatches(t *testing.T) {
	runner := &durableRunner{}
	manager := routedManager(t, runner, "s")
	manager.settle = time.Hour
	for n := 0; n < 25; n++ {
		event := Event{EventID: fmt.Sprintf("e%02d", n), TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "message", Text: "x"}
		if err := manager.routeEvent("app", nil, event); err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.snapshot()) != 0 {
		t.Fatal("settle window dispatched before drain")
	}
	manager.drainProfile("app")
	calls := runner.snapshot()
	if len(calls) != 1 || len(calls[0].events) != conversation.MaxSlackEventsPerDispatch || calls[0].events[0].EventID != "e00" || calls[0].events[19].EventID != "e19" {
		t.Fatalf("first batch=%#v", calls)
	}
	manager.OnConversationIdle("s")
	calls = runner.snapshot()
	if len(calls) != 2 || len(calls[1].events) != 5 || calls[1].events[0].EventID != "e20" || calls[1].events[4].EventID != "e24" {
		t.Fatalf("overflow batch=%#v", calls)
	}
}

func TestManagerRestartDeduplicatesDeliveredEvent(t *testing.T) {
	store := newManagerStore(t)
	runner1 := &durableRunner{}
	m1 := NewManager(store, nil, nil, runner1, nil)
	m1.sessions = map[string][]resolvedSubscription{"s": {{sessionID: "s", appID: "app", installationID: "i", teamID: "team", channelID: "channel"}}}
	event := Event{EventID: "restart", TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "message"}
	if err := m1.routeEvent("app", nil, event); err != nil {
		t.Fatal(err)
	}
	m1.Close()

	runner2 := &durableRunner{}
	m2 := NewManager(store, nil, nil, runner2, nil)
	defer m2.Close()
	m2.sessions = map[string][]resolvedSubscription{"s": {{sessionID: "s", appID: "app", installationID: "i", teamID: "team", channelID: "channel"}}}
	if err := m2.routeEvent("app", nil, event); err != nil {
		t.Fatal(err)
	}
	if len(runner1.snapshot()) != 1 || len(runner2.snapshot()) != 0 {
		t.Fatalf("restart calls before=%d after=%d", len(runner1.snapshot()), len(runner2.snapshot()))
	}
}

func TestManagerStartDrainsRecoveredBacklog(t *testing.T) {
	store := newManagerStore(t)
	addSlackLoop(t, store, "s", true, false, session.SlackSubscription{InstallationID: "install-1", ChannelID: "channel-1"})
	runner, sources := &durableRunner{}, &sourceHarness{}
	manager := NewManager(store, testInstallations(), &managerCredentials{token: "token"}, runner, nil)
	manager.factory = sources.factory
	t.Cleanup(manager.Close)
	if _, err := manager.journal.Accept("app-1", Event{EventID: "backlog", TeamID: "team-1", ChannelID: "channel-1"},
		[]journalRecipient{{SessionID: "s", InstallationID: "install-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "startup backlog drain", func() bool { return len(runner.snapshot()) == 1 })
	stats, _ := manager.journal.Stats("app-1")
	if stats.Delivered != 1 || stats.Pending != 0 {
		t.Fatalf("startup stats=%+v", stats)
	}
}

func TestManagerStatusReportsExpiredDeadLetters(t *testing.T) {
	runner := &durableRunner{outcomes: map[string][]error{"s": {conversation.ErrSessionBusy}}}
	manager := routedManager(t, runner, "s")
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	manager.journal.now = func() time.Time { return now }
	if err := manager.routeEvent("app", nil, Event{EventID: "old", TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "message", Text: "erase"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(journalRetention)
	manager.refreshJournalStatus("app")
	status := manager.Status()
	if len(status) != 1 || status[0].DeadLetterCount != 1 || status[0].PendingCount != 0 || status[0].FailedCount != 0 {
		t.Fatalf("status=%#v", status)
	}
}
