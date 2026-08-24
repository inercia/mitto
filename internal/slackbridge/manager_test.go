package slackbridge

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
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

type neverReadySource struct {
	started chan struct{}
	ready   chan struct{}
	once    sync.Once
}

func (s *neverReadySource) Run(ctx context.Context, _ func(Event)) error {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return ctx.Err()
}

func (s *neverReadySource) RunDurableObserved(ctx context.Context, _ func(Event) error, observe func(SourceObservation)) error {
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ready:
		observe(SourceTransportReady)
	}
	<-ctx.Done()
	return ctx.Err()
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
		"install-1": {Installation: slackcatalog.Installation{ID: "install-1", AppID: "app-1", CredentialKind: slackcatalog.CredentialKindBot, TeamID: "team-1", BotID: "bot-1", BotUserID: "user-bot-1"}, TokenConfigured: true},
		"install-2": {Installation: slackcatalog.Installation{ID: "install-2", AppID: "app-1", CredentialKind: slackcatalog.CredentialKindBot, TeamID: "team-2", BotID: "bot-2", BotUserID: "user-bot-2"}, TokenConfigured: true},
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
	if status[0].EventsAPIReceived != 4 || status[0].AcceptedCount != 4 || status[0].DeliveredCount != 2 ||
		status[0].ConnectedAt.IsZero() || status[0].LastEnvelopeAt.IsZero() {
		t.Fatalf("delivery diagnostics = %#v", status[0])
	}
}

func TestManagerWorkerRemainsConnectingUntilSourceReady(t *testing.T) {
	store := newManagerStore(t)
	addSlackLoop(t, store, "not-ready", true, false, session.SlackSubscription{InstallationID: "install-1", ChannelID: "channel"})
	source := &neverReadySource{started: make(chan struct{}), ready: make(chan struct{})}
	manager := NewManager(store, testInstallations(), &managerCredentials{token: "token"}, &managerRunner{}, nil)
	manager.factory = func(string, string) (Source, error) { return source, nil }
	t.Cleanup(manager.Close)
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	status := manager.Status()
	if len(status) != 1 || status[0].State != "connecting" {
		t.Fatalf("status before transport readiness = %#v, want connecting", status)
	}
	close(source.ready)
	waitForManager(t, "transport readiness", func() bool {
		status = manager.Status()
		return len(status) == 1 && status[0].State == "connected" && !status[0].ConnectedAt.IsZero()
	})
}

func TestManagerAppliesEventModeThreadAndBotFilters(t *testing.T) {
	runner := &managerRunner{}
	manager := NewManager(nil, nil, nil, runner, nil)
	t.Cleanup(manager.Close)
	manager.sessions = map[string][]resolvedSubscription{
		"roots":    {{sessionID: "roots", appID: "app", installationID: "install", teamID: "team", channelID: "channel", eventMode: session.SlackEventModeAnyHumanMessage, threadPolicy: session.SlackThreadPolicyRootOnly, botID: "bot", botUserID: "bot-user"}},
		"mentions": {{sessionID: "mentions", appID: "app", installationID: "install", teamID: "team", channelID: "channel", eventMode: session.SlackEventModeAppMention, threadPolicy: session.SlackThreadPolicyAny}},
		"replies":  {{sessionID: "replies", appID: "app", installationID: "install", teamID: "team", channelID: "channel", eventMode: session.SlackEventModeAnyHumanMessage, threadPolicy: session.SlackThreadPolicyRepliesOnly}},
		"user-mentions": {{sessionID: "user-mentions", appID: "app", installationID: "user-install", teamID: "team", channelID: "channel",
			eventMode: session.SlackEventModeAppMention, threadPolicy: session.SlackThreadPolicyAny,
			credentialKind: slackcatalog.CredentialKindUser, authorizedUser: "delegated"}},
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
	if counts["roots"] != 2 || counts["mentions"] != 1 || counts["replies"] != 1 || counts["user-mentions"] != 0 || len(calls) != 4 {
		t.Fatalf("filtered dispatch counts = %v, calls=%#v", counts, calls)
	}
}

func TestManagerRoutesBotAndDelegatedUserAuthorizationsWithoutDuplicates(t *testing.T) {
	runner := &managerRunner{}
	manager := NewManager(nil, nil, nil, runner, nil)
	t.Cleanup(manager.Close)
	bot := resolvedSubscription{appID: "app", installationID: "bot-install", teamID: "team", channelID: "private",
		eventMode: session.SlackEventModeAnyHumanMessage, threadPolicy: session.SlackThreadPolicyAny,
		credentialKind: slackcatalog.CredentialKindBot, authorizedUser: "UBOT"}
	user := resolvedSubscription{appID: "app", installationID: "user-install", teamID: "team", channelID: "private",
		eventMode: session.SlackEventModeAnyHumanMessage, threadPolicy: session.SlackThreadPolicyAny,
		credentialKind: slackcatalog.CredentialKindUser, authorizedUser: "UDELEGATED"}
	manager.sessions = map[string][]resolvedSubscription{
		"bot":  {withSession(bot, "bot")},
		"user": {withSession(user, "user")},
		"dual": {withSession(bot, "dual"), withSession(user, "dual")},
	}
	event := Event{EventID: "both", TeamID: "team", ChannelID: "private", AuthorID: "human", Kind: "message",
		AuthorizationScopeKnown: true, Authorizations: []EventAuthorization{{UserID: "UBOT", IsBot: true}, {UserID: "UDELEGATED"}}}
	if err := manager.routeEvent("app", nil, event); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, call := range runner.snapshot() {
		counts[call.sessionID]++
	}
	if counts["bot"] != 1 || counts["user"] != 1 || counts["dual"] != 1 || len(runner.snapshot()) != 3 {
		t.Fatalf("authorization dispatch counts=%v calls=%#v", counts, runner.snapshot())
	}
}

func TestManagerReconcileAssociatesCredentialModesAndSkipsMissingCredential(t *testing.T) {
	store := newManagerStore(t)
	for _, installationID := range []string{"bot-install", "user-install", "revoked-install"} {
		addSlackLoop(t, store, installationID, true, false, session.SlackSubscription{InstallationID: installationID, ChannelID: "private"})
	}
	catalog := managerCatalog{
		"bot-install": {Installation: slackcatalog.Installation{ID: "bot-install", AppID: "app", CredentialKind: slackcatalog.CredentialKindBot,
			TeamID: "team", BotUserID: "UBOT"}, TokenConfigured: true},
		"user-install": {Installation: slackcatalog.Installation{ID: "user-install", AppID: "app", CredentialKind: slackcatalog.CredentialKindUser,
			TeamID: "team", UserID: "UDELEGATED"}, TokenConfigured: true},
		"revoked-install": {Installation: slackcatalog.Installation{ID: "revoked-install", AppID: "app", CredentialKind: slackcatalog.CredentialKindUser,
			TeamID: "team", UserID: "UREVOKED"}, TokenConfigured: false},
	}
	sources := &sourceHarness{}
	manager := NewManager(store, catalog, &managerCredentials{token: "app-token"}, &managerRunner{}, nil)
	manager.factory = sources.factory
	t.Cleanup(manager.Close)
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "mode-aware worker", func() bool { created, active, _, _, _ := sources.snapshot(); return created == 1 && active == 1 })
	manager.mu.Lock()
	bot := append([]resolvedSubscription(nil), manager.sessions["bot-install"]...)
	user := append([]resolvedSubscription(nil), manager.sessions["user-install"]...)
	_, revokedPresent := manager.sessions["revoked-install"]
	manager.mu.Unlock()
	if len(bot) != 1 || bot[0].credentialKind != slackcatalog.CredentialKindBot || bot[0].authorizedUser != "UBOT" ||
		len(user) != 1 || user[0].credentialKind != slackcatalog.CredentialKindUser || user[0].authorizedUser != "UDELEGATED" || revokedPresent {
		t.Fatalf("resolved bot=%#v user=%#v revoked_present=%v", bot, user, revokedPresent)
	}
}

func TestManagerAuthorizationLossFailsClosedAndErasesContent(t *testing.T) {
	runner := &managerRunner{}
	manager := NewManager(nil, nil, nil, runner, nil)
	t.Cleanup(manager.Close)
	manager.sessions = map[string][]resolvedSubscription{"user": {{sessionID: "user", appID: "app", installationID: "user-install",
		teamID: "team", channelID: "private", eventMode: session.SlackEventModeAnyHumanMessage,
		credentialKind: slackcatalog.CredentialKindUser, authorizedUser: "UDELEGATED"}}}
	event := Event{EventID: "revoked", TeamID: "team", ChannelID: "private", AuthorID: "human", Kind: "message", Text: "sensitive",
		AuthorizationScopeKnown: true, Authorizations: []EventAuthorization{}}
	if err := manager.routeEvent("app", nil, event); err != nil {
		t.Fatal(err)
	}
	// An authorization-loss event resolves to zero recipients, so it must not
	// be journaled at all (mitto-d8y: zero-recipient records are never
	// persisted, preventing them from pinning the journal's hard cap).
	doc := readJournalDocument(t, manager.journal, "app")
	if len(runner.snapshot()) != 0 || len(doc.Records) != 0 {
		t.Fatalf("authorization-loss calls=%#v records=%#v", runner.snapshot(), doc.Records)
	}
}

func TestManagerNamesAndRemovesSlackReferences(t *testing.T) {
	store := newManagerStore(t)
	addSlackLoop(t, store, "named", true, false, session.SlackSubscription{InstallationID: "install-1", ChannelID: "channel"})
	if err := store.UpdateMetadata("named", func(metadata *session.Metadata) { metadata.Name = "Release watcher" }); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, testInstallations(), &managerCredentials{token: "token"}, &managerRunner{}, nil)
	t.Cleanup(manager.Close)
	if err := manager.ReconcileSession("named"); err != nil {
		t.Fatal(err)
	}

	references, err := manager.FindSlackReferences(context.Background(), "app-1", []string{"install-1"})
	if err != nil || len(references) != 1 || references[0].Name != "Release watcher" {
		t.Fatalf("FindSlackReferences() = %#v, %v", references, err)
	}
	removed, err := manager.RemoveSlackReferences(context.Background(), "app-1", nil)
	if err != nil || len(removed) != 1 || removed[0].Name != "Release watcher" {
		t.Fatalf("RemoveSlackReferences() = %#v, %v", removed, err)
	}
	if _, err := store.Loop("named").Get(); !errors.Is(err, session.ErrLoopNotFound) {
		t.Fatalf("loop Get() error = %v, want ErrLoopNotFound", err)
	}
	references, err = manager.FindSlackReferences(context.Background(), "app-1", []string{"install-1"})
	if err != nil || len(references) != 0 {
		t.Fatalf("references after removal = %#v, %v", references, err)
	}
}

// TestManagerRemoveSlackReferencesSelectiveInstallationPreservesOthers uses a
// real *session.Store (not a fake that clears everything) to prove selective
// installation removal: only the subscription(s) matching the targeted
// installation are dropped, from only the session(s) that reference it.
// Unrelated subscriptions on the same loop, and other sessions entirely, are
// left untouched.
func TestManagerRemoveSlackReferencesSelectiveInstallationPreservesOthers(t *testing.T) {
	store := newManagerStore(t)
	// "multi" subscribes to both install-1 and install-2 under app-1.
	addSlackLoop(t, store, "multi", true, false,
		session.SlackSubscription{InstallationID: "install-1", ChannelID: "channel-a"},
		session.SlackSubscription{InstallationID: "install-2", ChannelID: "channel-b"})
	// "unrelated" only subscribes to install-2 and must be untouched by an
	// install-1-scoped removal.
	addSlackLoop(t, store, "unrelated", true, false,
		session.SlackSubscription{InstallationID: "install-2", ChannelID: "channel-c"})

	manager := NewManager(store, testInstallations(), &managerCredentials{token: "token"}, &managerRunner{}, nil)
	t.Cleanup(manager.Close)
	if err := manager.ReconcileSession("multi"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSession("unrelated"); err != nil {
		t.Fatal(err)
	}

	removed, err := manager.RemoveSlackReferences(context.Background(), "app-1", []string{"install-1"})
	if err != nil || len(removed) != 1 || removed[0].SessionID != "multi" {
		t.Fatalf("RemoveSlackReferences(install-1) = %#v, %v", removed, err)
	}

	// "multi" survives (install-2 subscription remains) instead of being
	// deleted as a sole-onSlack loop.
	multiLoop, err := store.Loop("multi").Get()
	if err != nil {
		t.Fatalf("multi loop Get() error = %v, want it to still exist", err)
	}
	if len(multiLoop.SlackSubscriptions) != 1 || multiLoop.SlackSubscriptions[0].InstallationID != "install-2" || !multiLoop.IsOnSlack() {
		t.Fatalf("multi loop after selective removal = %#v", multiLoop)
	}

	// "unrelated" is completely untouched.
	unrelatedLoop, err := store.Loop("unrelated").Get()
	if err != nil {
		t.Fatalf("unrelated loop Get() error = %v, want it to still exist", err)
	}
	if len(unrelatedLoop.SlackSubscriptions) != 1 || unrelatedLoop.SlackSubscriptions[0].InstallationID != "install-2" {
		t.Fatalf("unrelated loop was mutated: %#v", unrelatedLoop)
	}

	// install-1 no longer has any references; install-2 still has both.
	afterInstall1, err := manager.FindSlackReferences(context.Background(), "app-1", []string{"install-1"})
	if err != nil || len(afterInstall1) != 0 {
		t.Fatalf("references for install-1 after removal = %#v, %v", afterInstall1, err)
	}
	afterInstall2, err := manager.FindSlackReferences(context.Background(), "app-1", []string{"install-2"})
	if err != nil || len(afterInstall2) != 2 {
		t.Fatalf("references for install-2 after removal = %#v, %v", afterInstall2, err)
	}
}

func withSession(subscription resolvedSubscription, sessionID string) resolvedSubscription {
	subscription.sessionID = sessionID
	return subscription
}

func TestManagerReconcilesLifecycleAndCancelsGraceStop(t *testing.T) {
	store := newManagerStore(t)
	const sessionID = "lifecycle"
	addSlackLoop(t, store, sessionID, false, false, session.SlackSubscription{InstallationID: "install-1", ChannelID: "old"})
	runner, sources := &managerRunner{}, &sourceHarness{}
	manager := NewManager(store, testInstallations(), &managerCredentials{token: "token"}, runner, nil)
	// grace is generous (well beyond the 5ms waitForManager poll interval) so
	// the re-enable-before-grace-expiry window below has a wide safety margin
	// under CI scheduling jitter — a tight margin here previously caused an
	// intermittent flake where the grace timer fired for real before the
	// cancelling reconcile ran (mitto CI run 32370964532).
	manager.factory, manager.grace = sources.factory, 300*time.Millisecond
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
	time.Sleep(20 * time.Millisecond)
	if err := store.Loop(sessionID).Update(session.LoopUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSession(sessionID); err != nil {
		t.Fatal(err)
	}
	// Wait well past the original grace deadline to confirm the cancelled
	// timer never fired, then keep polling for the grace window's full
	// duration to catch a late/racy stop that a single point-in-time check
	// right at the deadline could miss.
	deadline := time.Now().Add(2 * manager.grace)
	for time.Now().Before(deadline) {
		if created, active, _, _, _ := sources.snapshot(); created != 1 || active != 1 {
			t.Fatalf("cancelled grace stop created=%d active=%d, want 1/1", created, active)
		}
		time.Sleep(10 * time.Millisecond)
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
	if status := manager.Status(); len(status) != 1 || status[0].State != "disconnected" || status[0].SubscriptionCount != 1 {
		t.Fatalf("shutdown status = %#v", status)
	}

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

func TestManagerStatusCallbacksPreserveTransitionOrder(t *testing.T) {
	manager := NewManager(nil, nil, nil, nil, nil)
	defer manager.Close()
	connectingStarted := make(chan struct{})
	releaseConnecting := make(chan struct{})
	var mu sync.Mutex
	var states []string
	manager.SetStatusCallback(func(status ConnectionStatus) {
		if status.State == "connecting" {
			close(connectingStarted)
			<-releaseConnecting
		}
		mu.Lock()
		states = append(states, status.State)
		mu.Unlock()
	})

	manager.emitStatus(ConnectionStatus{AppID: "app", State: "connecting"})
	<-connectingStarted
	manager.emitStatus(ConnectionStatus{AppID: "app", State: "connected"})
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	if len(states) != 0 {
		t.Fatalf("later status overtook blocked callback: %v", states)
	}
	mu.Unlock()
	close(releaseConnecting)
	waitForManager(t, "ordered status callbacks", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(states) == 2
	})
	mu.Lock()
	defer mu.Unlock()
	if states[0] != "connecting" || states[1] != "connected" {
		t.Fatalf("status callback order = %v", states)
	}
}

// TestReconcileSessionLogsSkipReasonsAndZeroResolvedWarning reproduces mitto-n11:
// ReconcileSession silently drops a subscription when the catalog installation
// lookup fails (installation_not_found) or the installation's token isn't
// configured (token_not_configured), and never emits a summary when an
// enabled, onSlack-armed loop resolves zero subscriptions — leaving an
// operator with no signal that the loop is "armed but not watching". All
// three assertions below are expected to fail against today's
// (non-logging) ReconcileSession.
func TestReconcileSessionLogsSkipReasonsAndZeroResolvedWarning(t *testing.T) {
	store := newManagerStore(t)
	catalog := managerCatalog{
		"install-configured": {
			Installation:    slackcatalog.Installation{ID: "install-configured", AppID: "app-1"},
			TokenConfigured: false,
		},
	}
	// sess-not-found references an installation ID absent from the catalog view.
	addSlackLoop(t, store, "sess-not-found", true, false,
		session.SlackSubscription{InstallationID: "install-missing", ChannelID: "channel-1"})
	// sess-no-token references a real installation whose bot token isn't configured.
	addSlackLoop(t, store, "sess-no-token", true, false,
		session.SlackSubscription{InstallationID: "install-configured", ChannelID: "channel-2"})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	manager := NewManager(store, catalog, &managerCredentials{token: "token"}, &managerRunner{}, logger)
	t.Cleanup(manager.Close)

	if err := manager.ReconcileSession("sess-not-found"); err != nil {
		t.Fatalf("ReconcileSession(sess-not-found): %v", err)
	}
	if err := manager.ReconcileSession("sess-no-token"); err != nil {
		t.Fatalf("ReconcileSession(sess-no-token): %v", err)
	}

	out := buf.String()

	if !strings.Contains(out, "installation_not_found") || !strings.Contains(out, "sess-not-found") || !strings.Contains(out, "install-missing") {
		t.Errorf("expected a credential-free skip log naming reason=installation_not_found, session_id=sess-not-found, installation_id=install-missing; got log output:\n%s", out)
	}
	if !strings.Contains(out, "token_not_configured") || !strings.Contains(out, "sess-no-token") || !strings.Contains(out, "install-configured") {
		t.Errorf("expected a credential-free skip log naming reason=token_not_configured, session_id=sess-no-token, installation_id=install-configured; got log output:\n%s", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("expected a WARN summary for an enabled, onSlack-armed session that resolved zero subscriptions (\"armed but not watching\"); got log output:\n%s", out)
	}
}

// TestEmitStatusLockedLogsInfoOnTransitionDebugOnCounterBump reproduces the
// fix for the INFO-log flood: emitStatusLocked must log at INFO only on a
// meaningful state transition (or the first status for an app), and at DEBUG
// when only counters/timestamps that don't reflect a state change move.
func TestEmitStatusLockedLogsInfoOnTransitionDebugOnCounterBump(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	manager := NewManager(nil, nil, nil, nil, logger)
	t.Cleanup(manager.Close)

	// First status for this app is always INFO.
	manager.mu.Lock()
	manager.emitStatusLocked(ConnectionStatus{AppID: "app", State: "connected", SubscriptionCount: 1})
	manager.mu.Unlock()
	out := buf.String()
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("expected level=INFO for the first status; got:\n%s", out)
	}

	// Pure counter bump: same State/SubscriptionCount/error fields, only
	// EventsAPIReceived/IgnoredCount move -> DEBUG, not INFO.
	buf.Reset()
	manager.mu.Lock()
	manager.emitStatusLocked(ConnectionStatus{
		AppID:             "app",
		State:             "connected",
		SubscriptionCount: 1,
		EventsAPIReceived: 1,
		IgnoredCount:      1,
	})
	manager.mu.Unlock()
	out = buf.String()
	if !strings.Contains(out, "level=DEBUG") || strings.Contains(out, "level=INFO") {
		t.Errorf("expected level=DEBUG (not INFO) for a pure counter bump; got:\n%s", out)
	}

	// Real transition: State + ErrorClass change -> INFO.
	buf.Reset()
	manager.mu.Lock()
	manager.emitStatusLocked(ConnectionStatus{
		AppID:             "app",
		State:             "backoff",
		SubscriptionCount: 1,
		EventsAPIReceived: 1,
		IgnoredCount:      1,
		ErrorClass:        "connection_failed",
	})
	manager.mu.Unlock()
	out = buf.String()
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("expected level=INFO for a real state transition; got:\n%s", out)
	}
}
