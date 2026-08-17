package slackbridge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/slackcatalog"
)

type managerCatalog map[string]slackcatalog.InstallationView

func (c managerCatalog) GetInstallation(id string) (slackcatalog.InstallationView, error) {
	installation, ok := c[id]
	if !ok {
		return slackcatalog.InstallationView{}, slackcatalog.ErrNotFound
	}
	return installation, nil
}

type managerCredentials struct {
	mu    sync.Mutex
	token string
	err   error
}

func (c *managerCredentials) Resolve(secrets.CredentialRef) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, c.err
}

func (c *managerCredentials) set(token string, err error) {
	c.mu.Lock()
	c.token, c.err = token, err
	c.mu.Unlock()
}

type managerTriggerCall struct {
	sessionID string
	event     conversation.PromptSlackEvent
}

type managerRunner struct {
	mu    sync.Mutex
	calls []managerTriggerCall
}

func (r *managerRunner) TriggerNowWithSlackEvents(sessionID string, _ bool, firedBy session.LoopTrigger, events []conversation.PromptSlackEvent) error {
	if firedBy != session.TriggerOnSlack || len(events) != 1 {
		return errors.New("unexpected Slack dispatch shape")
	}
	r.mu.Lock()
	r.calls = append(r.calls, managerTriggerCall{sessionID: sessionID, event: events[0]})
	r.mu.Unlock()
	return nil
}

func (r *managerRunner) snapshot() []managerTriggerCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]managerTriggerCall(nil), r.calls...)
}

type managerSource struct {
	h       *sourceHarness
	started chan struct{}
	events  chan Event
	err     error
	once    sync.Once
}

func (s *managerSource) Run(ctx context.Context, emit func(Event)) error {
	s.h.mu.Lock()
	s.h.active++
	if s.h.active > s.h.maxActive {
		s.h.maxActive = s.h.active
	}
	s.h.mu.Unlock()
	s.once.Do(func() { close(s.started) })
	defer func() {
		s.h.mu.Lock()
		s.h.active--
		s.h.mu.Unlock()
	}()
	if s.err != nil {
		return s.err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-s.events:
			emit(event)
		}
	}
}

type sourceHarness struct {
	mu        sync.Mutex
	created   int
	active    int
	maxActive int
	tokens    []string
	sources   []*managerSource
	err       error
}

func (h *sourceHarness) factory(_ string, token string) (Source, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	source := &managerSource{h: h, started: make(chan struct{}), events: make(chan Event, 16), err: h.err}
	h.created++
	h.tokens = append(h.tokens, token)
	h.sources = append(h.sources, source)
	return source, nil
}

func (h *sourceHarness) snapshot() (created, active, maxActive int, tokens []string, sources []*managerSource) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.created, h.active, h.maxActive, append([]string(nil), h.tokens...), append([]*managerSource(nil), h.sources...)
}

func waitForManager(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func newManagerStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func addSlackLoop(t *testing.T, store *session.Store, id string, enabled, archived bool, subscriptions ...session.SlackSubscription) {
	t.Helper()
	if err := store.Create(session.Metadata{SessionID: id, ACPServer: "test", WorkingDir: t.TempDir(), Archived: archived}); err != nil {
		t.Fatal(err)
	}
	loop := &session.LoopPrompt{Prompt: "inspect", Enabled: enabled, Triggers: []session.LoopTrigger{session.TriggerOnSlack}, SlackSubscriptions: subscriptions}
	if err := store.Loop(id).Set(loop); err != nil {
		t.Fatal(err)
	}
}

func testInstallations() managerCatalog {
	return managerCatalog{
		"install-1": {Installation: slackcatalog.Installation{ID: "install-1", AppID: "app-1", TeamID: "team-1", BotID: "bot-1", BotUserID: "user-bot-1"}},
		"install-2": {Installation: slackcatalog.Installation{ID: "install-2", AppID: "app-1", TeamID: "team-2", BotID: "bot-2", BotUserID: "user-bot-2"}},
	}
}

func TestManagerPoolsOneWorkerAndFansOutExactlyOnce(t *testing.T) {
	store := newManagerStore(t)
	sub := session.SlackSubscription{InstallationID: "install-1", ChannelID: "channel-1"}
	addSlackLoop(t, store, "match-1", true, false, sub)
	addSlackLoop(t, store, "match-2", true, false, sub)
	addSlackLoop(t, store, "other-channel", true, false, session.SlackSubscription{InstallationID: "install-1", ChannelID: "channel-2"})
	addSlackLoop(t, store, "other-team", true, false, session.SlackSubscription{InstallationID: "install-2", ChannelID: "channel-1"})

	runner, sources := &managerRunner{}, &sourceHarness{}
	manager := NewManager(store, testInstallations(), &managerCredentials{token: "vault-token"}, runner, nil)
	manager.factory = sources.factory
	t.Cleanup(manager.Close)
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "single app worker", func() bool { created, active, _, _, _ := sources.snapshot(); return created == 1 && active == 1 })
	_, _, maxActive, _, activeSources := sources.snapshot()
	if maxActive != 1 {
		t.Fatalf("max active sources = %d, want 1", maxActive)
	}

	activeSources[0].events <- Event{EventID: "event-1", TeamID: "team-1", ChannelID: "channel-1", AuthorID: "human", Kind: "message", Text: "hello"}
	waitForManager(t, "two matching loop dispatches", func() bool { return len(runner.snapshot()) == 2 })
	calls := runner.snapshot()
	got := map[string]bool{calls[0].sessionID: true, calls[1].sessionID: true}
	if !got["match-1"] || !got["match-2"] || calls[0].event.InstallationID != "install-1" || !calls[0].event.Untrusted {
		t.Fatalf("fan-out calls = %#v", calls)
	}
	activeSources[0].events <- Event{EventID: "event-1", TeamID: "team-1", ChannelID: "channel-1", AuthorID: "human", Kind: "message"}
	activeSources[0].events <- Event{EventID: "event-2", TeamID: "team-1", ChannelID: "channel-x", AuthorID: "human", Kind: "message"}
	activeSources[0].events <- Event{EventID: "event-3", TeamID: "team-x", ChannelID: "channel-1", AuthorID: "human", Kind: "message"}
	time.Sleep(30 * time.Millisecond)
	if len(runner.snapshot()) != 2 {
		t.Fatalf("nonmatching/duplicate event changed calls: %#v", runner.snapshot())
	}
	status := manager.Status()
	if len(status) != 1 || status[0].AppID != "app-1" || status[0].State != "connected" || status[0].SubscriptionCount != 4 {
		t.Fatalf("status = %#v", status)
	}
}

func TestManagerAppliesEventModeThreadAndBotFilters(t *testing.T) {
	runner := &managerRunner{}
	manager := NewManager(nil, nil, nil, runner, nil)
	manager.sessions = map[string][]resolvedSubscription{
		"roots":    {{sessionID: "roots", appID: "app", installationID: "install", teamID: "team", channelID: "channel", eventMode: session.SlackEventModeAnyHumanMessage, threadPolicy: session.SlackThreadPolicyRootOnly, botID: "bot", botUserID: "bot-user"}},
		"mentions": {{sessionID: "mentions", appID: "app", installationID: "install", teamID: "team", channelID: "channel", eventMode: session.SlackEventModeAppMention, threadPolicy: session.SlackThreadPolicyAny}},
		"replies":  {{sessionID: "replies", appID: "app", installationID: "install", teamID: "team", channelID: "channel", eventMode: session.SlackEventModeAnyHumanMessage, threadPolicy: session.SlackThreadPolicyRepliesOnly}},
	}
	worker := &appWorker{dedupe: newDedupeSet(0)}
	manager.routeEvent("app", worker, Event{EventID: "root", TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "message", Timestamp: "1"})
	manager.routeEvent("app", worker, Event{EventID: "reply", TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "message", Timestamp: "2", ThreadTimestamp: "1"})
	manager.routeEvent("app", worker, Event{EventID: "mention", TeamID: "team", ChannelID: "channel", AuthorID: "human", Kind: "app_mention", Timestamp: "3"})
	manager.routeEvent("app", worker, Event{EventID: "bot", TeamID: "team", ChannelID: "channel", AuthorID: "bot", Kind: "message"})
	calls := runner.snapshot()
	counts := map[string]int{}
	for _, call := range calls {
		counts[call.sessionID]++
	}
	if counts["roots"] != 2 || counts["mentions"] != 1 || counts["replies"] != 1 || len(calls) != 4 {
		t.Fatalf("filtered dispatch counts = %v, calls=%#v", counts, calls)
	}
}

func TestManagerReconcilesLifecycleAndCancelsGraceStop(t *testing.T) {
	store := newManagerStore(t)
	const sessionID = "lifecycle"
	addSlackLoop(t, store, sessionID, false, false, session.SlackSubscription{InstallationID: "install-1", ChannelID: "old"})
	runner, sources := &managerRunner{}, &sourceHarness{}
	manager := NewManager(store, testInstallations(), &managerCredentials{token: "token"}, runner, nil)
	manager.factory, manager.grace = sources.factory, 40*time.Millisecond
	t.Cleanup(manager.Close)
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	if created, _, _, _, _ := sources.snapshot(); created != 0 {
		t.Fatalf("disabled loop started %d workers", created)
	}
	enabled := true
	if err := store.Loop(sessionID).Update(session.LoopUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSession(sessionID); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "enabled worker", func() bool { created, active, _, _, _ := sources.snapshot(); return created == 1 && active == 1 })

	disabled := false
	if err := store.Loop(sessionID).Update(session.LoopUpdate{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSession(sessionID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.Loop(sessionID).Update(session.LoopUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSession(sessionID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if created, active, _, _, _ := sources.snapshot(); created != 1 || active != 1 {
		t.Fatalf("cancelled grace stop created=%d active=%d, want 1/1", created, active)
	}

	updated := []session.SlackSubscription{{InstallationID: "install-1", ChannelID: "new"}}
	if err := store.Loop(sessionID).Update(session.LoopUpdate{SlackSubscriptions: &updated}); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSession(sessionID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, activeSources := sources.snapshot()
	activeSources[0].events <- Event{EventID: "old", TeamID: "team-1", ChannelID: "old", AuthorID: "human", Kind: "message"}
	activeSources[0].events <- Event{EventID: "new", TeamID: "team-1", ChannelID: "new", AuthorID: "human", Kind: "message"}
	waitForManager(t, "edited route", func() bool { return len(runner.snapshot()) == 1 })

	if err := store.UpdateMetadata(sessionID, func(meta *session.Metadata) { meta.Archived = true }); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSession(sessionID); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "archive worker stop", func() bool { _, active, _, _, _ := sources.snapshot(); return active == 0 })
	if err := store.UpdateMetadata(sessionID, func(meta *session.Metadata) { meta.Archived = false }); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSession(sessionID); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "unarchive worker restart", func() bool { created, active, _, _, _ := sources.snapshot(); return created == 2 && active == 1 })
	manager.RemoveSession(sessionID)
	waitForManager(t, "deleted session worker stop", func() bool { _, active, _, _, _ := sources.snapshot(); return active == 0 })
}

func TestManagerCredentialRestartStatusAndShutdown(t *testing.T) {
	store := newManagerStore(t)
	addSlackLoop(t, store, "restart", true, false, session.SlackSubscription{InstallationID: "install-1", ChannelID: "channel"})
	credentials, runner, sources := &managerCredentials{token: "token-one"}, &managerRunner{}, &sourceHarness{}
	manager := NewManager(store, testInstallations(), credentials, runner, nil)
	manager.factory = sources.factory
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "first credential worker", func() bool { created, active, _, _, _ := sources.snapshot(); return created == 1 && active == 1 })
	credentials.set("token-two", nil)
	manager.RestartApp("app-1")
	waitForManager(t, "replacement credential worker", func() bool {
		created, active, _, tokens, _ := sources.snapshot()
		return created == 2 && active == 1 && len(tokens) == 2
	})
	_, _, maxActive, tokens, _ := sources.snapshot()
	if maxActive != 1 || tokens[0] != "token-one" || tokens[1] != "token-two" {
		t.Fatalf("restart maxActive=%d tokens=%v", maxActive, tokens)
	}
	manager.Close()
	waitForManager(t, "clean shutdown", func() bool { _, active, _, _, _ := sources.snapshot(); return active == 0 })

	failingSources := &sourceHarness{err: errors.New("raw transport detail")}
	manager = NewManager(store, testInstallations(), credentials, runner, nil)
	manager.factory = failingSources.factory
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "sanitized backoff status", func() bool {
		status := manager.Status()
		return len(status) == 1 && status[0].State == "backoff"
	})
	status := manager.Status()[0]
	if status.ErrorClass != "connection_failed" || status.AppID != "app-1" || status.SubscriptionCount != 1 {
		t.Fatalf("backoff status = %#v", status)
	}
	manager.Close()
}
