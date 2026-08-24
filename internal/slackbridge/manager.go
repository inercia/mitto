package slackbridge

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/slackcatalog"
)

const (
	defaultUnusedGrace = 30 * time.Second
	defaultSlackSettle = 2 * time.Second
)

// ManagedLoopTriggerer is the canonical batched onSlack dispatch seam.
type ManagedLoopTriggerer interface {
	TriggerNowWithSlackEvents(string, bool, session.LoopTrigger, []conversation.PromptSlackEvent) error
}

// Catalog is the credential-free catalog view needed to resolve subscriptions.
type Catalog interface {
	GetInstallation(string) (slackcatalog.InstallationView, error)
}

// CredentialResolver resolves app credentials only when a worker starts.
type CredentialResolver interface {
	Resolve(secrets.CredentialRef) (string, error)
}

// SourceFactory constructs one Socket Mode source for an app profile.
type SourceFactory func(appID, appToken string) (Source, error)

// ConnectionStatus is safe to log and send to clients: it contains no tokens,
// message content, or raw SDK errors.
type ConnectionStatus struct {
	AppID                    string    `json:"app_id"`
	State                    string    `json:"state"`
	SubscriptionCount        int       `json:"subscription_count"`
	PendingCount             int       `json:"pending_count,omitempty"`
	FailedCount              int       `json:"failed_count,omitempty"`
	DeadLetterCount          int       `json:"dead_letter_count,omitempty"`
	DeliveredCount           int       `json:"delivered_count,omitempty"`
	EventsAPIReceived        uint64    `json:"events_api_received"`
	AcceptedCount            uint64    `json:"accepted_count"`
	IgnoredCount             uint64    `json:"ignored_count"`
	ConnectedAt              time.Time `json:"connected_at,omitempty"`
	LastEnvelopeAt           time.Time `json:"last_envelope_at,omitempty"`
	LastAuthorizationErrorAt time.Time `json:"last_authorization_error_at,omitempty"`
	LastJournalErrorAt       time.Time `json:"last_journal_error_at,omitempty"`
	RetryAt                  time.Time `json:"retry_at,omitempty"`
	ErrorClass               string    `json:"error_class,omitempty"`
}

type resolvedSubscription struct {
	sessionID      string
	appID          string
	installationID string
	teamID         string
	channelID      string
	eventMode      session.SlackEventMode
	threadPolicy   session.SlackThreadPolicy
	botID          string
	botUserID      string
	credentialKind slackcatalog.CredentialKind
	authorizedUser string
}

type appWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
	// dedupe is retained for legacy package tests/adapters. Canonical manager
	// routing uses the durable journal's event_id index instead.
	dedupe *dedupeSet
	timer  *time.Timer
}

type statusNotification struct {
	status ConnectionStatus
	fn     func(ConnectionStatus)
}

// Manager owns one Socket Mode worker per referenced Slack app profile and an
// idempotent, process-local loop-subscription index.
type Manager struct {
	mu           sync.Mutex
	store        *session.Store
	catalog      Catalog
	credentials  CredentialResolver
	runner       ManagedLoopTriggerer
	factory      SourceFactory
	logger       *slog.Logger
	grace        time.Duration
	settle       time.Duration
	journal      *FileJournal
	ctx          context.Context
	cancel       context.CancelFunc
	sessions     map[string][]resolvedSubscription
	workers      map[string]*appWorker
	statuses     map[string]ConnectionStatus
	onStatus     func(ConnectionStatus)
	statusCond   *sync.Cond
	statusQueue  []statusNotification
	statusDone   chan struct{}
	statusClosed bool
	drainTimers  map[string]*time.Timer
	journalTemp  string
}

// NewManager constructs a manager. Call Start after all callbacks are wired.
func NewManager(store *session.Store, catalog Catalog, credentials CredentialResolver, runner ManagedLoopTriggerer, logger *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	journalDir := ""
	journalTemp := ""
	if store == nil {
		// Nil stores are used by narrow manager unit seams that cannot own durable
		// subscriptions. Keep those instances out of the real app-data journal.
		if tempDir, err := os.MkdirTemp("", "mitto-slack-journal-"); err == nil {
			journalDir, journalTemp = tempDir, tempDir
		}
	} else {
		// Production uses the app-data journal. Stores rooted elsewhere (most
		// notably isolated tests) keep their journal beside that store instead.
		if sessionsDir, err := appdir.SessionsDir(); err != nil || filepath.Clean(store.BaseDir()) != filepath.Clean(sessionsDir) {
			journalDir = filepath.Join(store.BaseDir(), ".slack-event-journal")
		}
	}
	m := &Manager{store: store, catalog: catalog, credentials: credentials, runner: runner, logger: logger,
		grace: defaultUnusedGrace, ctx: ctx, cancel: cancel, sessions: make(map[string][]resolvedSubscription),
		workers: make(map[string]*appWorker), statuses: make(map[string]ConnectionStatus), statusDone: make(chan struct{}),
		journal: NewFileJournal(journalDir), drainTimers: make(map[string]*time.Timer), journalTemp: journalTemp}
	m.statusCond = sync.NewCond(&m.mu)
	m.factory = func(_ string, token string) (Source, error) {
		return NewSlackSource(Config{AppToken: token}, logger, nil), nil
	}
	go m.dispatchStatuses()
	return m
}

// SetSourceFactory replaces the Socket Mode source constructor. Call it before
// Start; it exists so offline integration tests can exercise the production
// manager with FakeSource and no Slack network or credentials.
func (m *Manager) SetSourceFactory(factory SourceFactory) {
	if factory == nil {
		return
	}
	m.mu.Lock()
	m.factory = factory
	m.mu.Unlock()
}

// SetStatusCallback installs the value-free connection status observer.
func (m *Manager) SetStatusCallback(fn func(ConnectionStatus)) {
	m.mu.Lock()
	m.onStatus = fn
	m.mu.Unlock()
}

// Status returns a credential-free point-in-time connection snapshot.
func (m *Manager) Status() []ConnectionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]ConnectionStatus, 0, len(m.statuses))
	for _, status := range m.statuses {
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AppID < result[j].AppID })
	return result
}

// Start recovers in-flight journal recipients before accepting new Socket Mode
// events, then rebuilds subscriptions and drains any restart backlog.
func (m *Manager) Start() error {
	profiles, err := m.journal.Recover()
	if err != nil {
		return err
	}
	if m.settle == 0 {
		m.settle = defaultSlackSettle
	}
	if err := m.ReconcileAll(); err != nil {
		return err
	}
	for _, appID := range profiles {
		m.scheduleDrain(appID, 0)
	}
	return nil
}

// ReconcileAll rebuilds every session entry from the authoritative stores.
func (m *Manager) ReconcileAll() error {
	metadata, err := m.store.List()
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(metadata))
	for _, meta := range metadata {
		seen[meta.SessionID] = true
		if err := m.ReconcileSession(meta.SessionID); err != nil && m.logger != nil {
			m.logger.Warn("slackbridge: failed to reconcile session", "session_id", meta.SessionID, "error_class", "reconcile")
		}
	}
	m.mu.Lock()
	for id := range m.sessions {
		if !seen[id] {
			delete(m.sessions, id)
		}
	}
	m.reconcileWorkersLocked()
	m.mu.Unlock()
	return nil
}

// ReconcileSession atomically replaces one session's resolved subscriptions.
func (m *Manager) ReconcileSession(sessionID string) error {
	meta, err := m.store.GetMetadata(sessionID)
	if err != nil {
		m.RemoveSession(sessionID)
		return err
	}
	var resolved []resolvedSubscription
	armed := false
	loop, loopErr := m.store.Loop(sessionID).Get()
	if loopErr == nil && !meta.Archived && loop.Enabled && loop.IsOnSlack() {
		armed = true
		for _, sub := range loop.SlackSubscriptions {
			installation, err := m.catalog.GetInstallation(sub.InstallationID)
			if err != nil {
				if m.logger != nil {
					m.logger.Debug("slackbridge: skipped subscription", "session_id", sessionID,
						"installation_id", sub.InstallationID, "reason", "installation_not_found")
				}
				continue
			}
			if !installation.TokenConfigured {
				if m.logger != nil {
					m.logger.Debug("slackbridge: skipped subscription", "session_id", sessionID,
						"installation_id", installation.ID, "reason", "token_not_configured")
				}
				continue
			}
			authorizedUser := installation.BotUserID
			if installation.CredentialKind == slackcatalog.CredentialKindUser {
				authorizedUser = installation.UserID
			}
			resolved = append(resolved, resolvedSubscription{sessionID: sessionID, appID: installation.AppID,
				installationID: installation.ID, teamID: installation.TeamID, channelID: sub.ChannelID,
				eventMode: sub.EventMode, threadPolicy: sub.ThreadPolicy,
				botID: installation.BotID, botUserID: installation.BotUserID,
				credentialKind: installation.CredentialKind, authorizedUser: authorizedUser})
		}
	}
	if armed && len(resolved) == 0 && m.logger != nil {
		m.logger.Warn("slackbridge: session armed for onSlack but resolved zero subscriptions", "session_id", sessionID)
	}
	m.mu.Lock()
	if len(resolved) == 0 {
		delete(m.sessions, sessionID)
	} else {
		m.sessions[sessionID] = resolved
	}
	m.reconcileWorkersLocked()
	m.mu.Unlock()
	return nil
}

// RemoveSession removes a deleted session from routing immediately.
func (m *Manager) RemoveSession(sessionID string) {
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.reconcileWorkersLocked()
	m.mu.Unlock()
}

// FindSlackReferences lets catalog deletion fail closed while active loops
// reference an app profile or one of its installations.
func (m *Manager) FindSlackReferences(_ context.Context, appID string, installationIDs []string) ([]slackcatalog.Reference, error) {
	sessionIDs := m.matchingReferenceSessionIDs(appID, installationIDs)
	refs := make([]slackcatalog.Reference, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		name := "Untitled conversation"
		if m.store != nil {
			if metadata, err := m.store.GetMetadata(sessionID); err == nil && strings.TrimSpace(metadata.Name) != "" {
				name = metadata.Name
			}
		}
		refs = append(refs, slackcatalog.Reference{SessionID: sessionID, Name: name})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].SessionID < refs[j].SessionID
	})
	return refs, nil
}

func (m *Manager) matchingReferenceSessionIDs(appID string, installationIDs []string) []string {
	matches := m.matchingReferenceSessions(appID, installationIDs)
	sessionIDs := make([]string, 0, len(matches))
	for sessionID := range matches {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	return sessionIDs
}

func (m *Manager) matchingReferenceSessions(appID string, installationIDs []string) map[string][]string {
	wanted := make(map[string]bool, len(installationIDs))
	for _, id := range installationIDs {
		wanted[id] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	matched := make(map[string]map[string]bool)
	for sessionID, subs := range m.sessions {
		for _, sub := range subs {
			if sub.appID == appID && (len(wanted) == 0 || wanted[sub.installationID]) {
				if matched[sessionID] == nil {
					matched[sessionID] = make(map[string]bool)
				}
				matched[sessionID][sub.installationID] = true
			}
		}
	}
	result := make(map[string][]string, len(matched))
	for sessionID, ids := range matched {
		for id := range ids {
			result[sessionID] = append(result[sessionID], id)
		}
		sort.Strings(result[sessionID])
	}
	return result
}

// RemoveSlackReferences removes subscriptions for the selected installations
// from every matching active loop and reconciles Socket Mode routing.
func (m *Manager) RemoveSlackReferences(_ context.Context, appID string, installationIDs []string) ([]slackcatalog.Reference, error) {
	if m.store == nil {
		return nil, errors.New("slack session store is unavailable")
	}
	matches := m.matchingReferenceSessions(appID, installationIDs)
	sessionIDs := make([]string, 0, len(matches))
	for sessionID := range matches {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	removed := make([]slackcatalog.Reference, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		metadata, err := m.store.GetMetadata(sessionID)
		if err != nil {
			return removed, err
		}
		_, changed, err := m.store.Loop(sessionID).RemoveSlackSubscriptions(matches[sessionID])
		if err != nil {
			return removed, err
		}
		if !changed {
			continue
		}
		name := strings.TrimSpace(metadata.Name)
		if name == "" {
			name = "Untitled conversation"
		}
		removed = append(removed, slackcatalog.Reference{SessionID: sessionID, Name: name})
		if err := m.ReconcileSession(sessionID); err != nil && !errors.Is(err, session.ErrLoopNotFound) {
			return removed, err
		}
	}
	return removed, nil
}

// RestartApp gracefully replaces a worker after an app credential change.
func (m *Manager) RestartApp(appID string) {
	m.mu.Lock()
	worker := m.workers[appID]
	if worker == nil {
		if m.appReferencesLocked(appID) > 0 {
			m.startWorkerLocked(appID)
		}
		m.mu.Unlock()
		return
	}
	worker.cancel()
	m.mu.Unlock()
	select {
	case <-worker.done:
	case <-time.After(5 * time.Second):
		go m.finishStoppedWorker(appID, worker)
		return
	}
	m.mu.Lock()
	if m.workers[appID] == worker {
		delete(m.workers, appID)
	}
	if m.workers[appID] == nil && m.appReferencesLocked(appID) > 0 {
		m.startWorkerLocked(appID)
	}
	m.mu.Unlock()
}

func (m *Manager) appReferencesLocked(appID string) int {
	n := 0
	for _, subs := range m.sessions {
		for _, sub := range subs {
			if sub.appID == appID {
				n++
			}
		}
	}
	return n
}

func (m *Manager) reconcileWorkersLocked() {
	referenced := make(map[string]bool)
	for _, subs := range m.sessions {
		for _, sub := range subs {
			referenced[sub.appID] = true
		}
	}
	for appID := range referenced {
		if worker := m.workers[appID]; worker != nil {
			if worker.timer != nil {
				worker.timer.Stop()
				worker.timer = nil
			}
		} else {
			m.startWorkerLocked(appID)
		}
	}
	for appID, worker := range m.workers {
		if referenced[appID] || worker.timer != nil {
			continue
		}
		id := appID
		worker.timer = time.AfterFunc(m.grace, func() { m.stopUnused(id) })
	}
	// A worker is shared by every subscription for its app, so its retained
	// health snapshot must track reference changes even when connection state
	// itself does not transition.
	for appID, status := range m.statuses {
		count := m.appReferencesLocked(appID)
		if status.SubscriptionCount != count {
			status.SubscriptionCount = count
			m.emitStatusLocked(status)
		}
	}
}

func (m *Manager) stopUnused(appID string) {
	m.mu.Lock()
	worker := m.workers[appID]
	if worker == nil || m.appReferencesLocked(appID) > 0 {
		m.mu.Unlock()
		return
	}
	worker.cancel()
	worker.timer = nil
	m.emitStatusLocked(ConnectionStatus{AppID: appID, State: "stopping"})
	m.mu.Unlock()
	go m.finishStoppedWorker(appID, worker)
}

func (m *Manager) finishStoppedWorker(appID string, worker *appWorker) {
	<-worker.done
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.workers[appID] != worker {
		return
	}
	delete(m.workers, appID)
	if m.appReferencesLocked(appID) > 0 {
		m.startWorkerLocked(appID)
	}
}

func (m *Manager) startWorkerLocked(appID string) {
	ctx, cancel := context.WithCancel(m.ctx)
	worker := &appWorker{cancel: cancel, done: make(chan struct{})}
	m.workers[appID] = worker
	go m.runWorker(ctx, appID, worker)
}

func (m *Manager) runWorker(ctx context.Context, appID string, worker *appWorker) {
	defer close(worker.done)
	backoff := minReconnectBackoff
	for ctx.Err() == nil {
		m.emitStatus(ConnectionStatus{AppID: appID, State: "connecting", SubscriptionCount: m.subscriptionCount(appID)})
		token, err := m.credentials.Resolve(secrets.SlackAppCredential(appID, slackcatalog.AppTokenCredential))
		if err == nil {
			var source Source
			source, err = m.factory(appID, token)
			if err == nil {
				accept := func(evt Event) error {
					acceptErr := m.routeEvent(appID, worker, evt)
					if acceptErr == nil {
						m.recordAccepted(appID)
					}
					return acceptErr
				}
				if observed, ok := source.(ObservedDurableSource); ok {
					err = observed.RunDurableObserved(ctx, accept, func(observation SourceObservation) {
						m.observeSource(appID, observation)
					})
				} else if durable, ok := source.(DurableSource); ok {
					var ready sync.Once
					err = durable.RunDurable(ctx, func(evt Event) error {
						ready.Do(func() { m.observeSource(appID, SourceTransportReady) })
						m.observeSource(appID, SourceEventsAPIEnvelope)
						return accept(evt)
					})
				} else {
					var ready sync.Once
					err = source.Run(ctx, func(evt Event) {
						ready.Do(func() { m.observeSource(appID, SourceTransportReady) })
						m.observeSource(appID, SourceEventsAPIEnvelope)
						if acceptErr := accept(evt); acceptErr != nil && m.logger != nil {
							m.logger.Warn("slackbridge: legacy source durable acceptance failed", "app_id", appID, "error_class", "journal")
						}
					})
				}
			}
		}
		if ctx.Err() != nil {
			break
		}
		retryAt := time.Now().Add(backoff)
		m.emitStatus(ConnectionStatus{AppID: appID, State: "backoff", SubscriptionCount: m.subscriptionCount(appID), RetryAt: retryAt, ErrorClass: classifyWorkerError(err)})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxReconnectBackoff {
			backoff = maxReconnectBackoff
		}
	}
	m.emitStatus(ConnectionStatus{AppID: appID, State: "disconnected", SubscriptionCount: m.subscriptionCount(appID)})
}

func classifyWorkerError(err error) string {
	if err == nil {
		return "disconnected"
	}
	if errors.Is(err, secrets.ErrNotFound) {
		return "credential_unavailable"
	}
	return "connection_failed"
}

func (m *Manager) routeEvent(appID string, _ *appWorker, evt Event) error {
	if evt.EventID == "" {
		return errors.New("slack event has no event_id")
	}
	m.mu.Lock()
	var candidates []resolvedSubscription
	for _, subs := range m.sessions {
		for _, sub := range subs {
			if sub.appID == appID && sub.teamID == evt.TeamID && sub.channelID == evt.ChannelID {
				candidates = append(candidates, sub)
			}
		}
	}
	m.mu.Unlock()
	targets := make(map[string]journalRecipient)
	for _, sub := range candidates {
		if !subscriptionAuthorized(sub, evt) {
			continue
		}
		if evt.AuthorID == sub.botID || evt.AuthorID == sub.botUserID {
			continue
		}
		if normalizedCredentialKind(sub.credentialKind) == slackcatalog.CredentialKindUser && evt.Kind == "app_mention" {
			continue
		}
		if sub.eventMode == session.SlackEventModeAppMention && evt.Kind != "app_mention" {
			continue
		}
		isReply := evt.ThreadTimestamp != "" && evt.ThreadTimestamp != evt.Timestamp
		if sub.threadPolicy == session.SlackThreadPolicyRootOnly && isReply {
			continue
		}
		if sub.threadPolicy == session.SlackThreadPolicyRepliesOnly && !isReply {
			continue
		}
		if _, exists := targets[sub.sessionID]; !exists {
			targets[sub.sessionID] = journalRecipient{SessionID: sub.sessionID, InstallationID: sub.installationID}
		}
	}
	recipients := make([]journalRecipient, 0, len(targets))
	for _, recipient := range targets {
		recipients = append(recipients, recipient)
	}
	if len(recipients) == 0 {
		// No session is subscribed to this event's channel: never persist a
		// permanent empty-recipient journal record for it (mitto-d8y). Such
		// records are vacuously "terminal" but only reclaimed after the 24h
		// retention window, so a busy channel with no subscriber can pin the
		// journal's hard cap and reject events that DO have recipients.
		m.refreshJournalStatus(appID)
		return nil
	}
	evt.Text = boundSlackText(evt.Text)
	duplicate, err := m.journal.Accept(appID, evt, recipients)
	if err != nil {
		m.logJournalFailure(appID, "accept")
		return err
	}
	m.refreshJournalStatus(appID)
	if duplicate {
		return nil
	}
	if m.settle <= 0 {
		m.drainProfile(appID)
	} else {
		m.scheduleDrain(appID, m.settle)
	}
	return nil
}

func normalizedCredentialKind(kind slackcatalog.CredentialKind) slackcatalog.CredentialKind {
	if kind == "" {
		return slackcatalog.CredentialKindBot
	}
	return kind
}

func subscriptionAuthorized(sub resolvedSubscription, evt Event) bool {
	if !evt.AuthorizationScopeKnown {
		return true
	}
	kind := normalizedCredentialKind(sub.credentialKind)
	for _, authorization := range evt.Authorizations {
		if authorization.UserID != sub.authorizedUser {
			continue
		}
		if kind == slackcatalog.CredentialKindBot && authorization.IsBot {
			return true
		}
		if kind == slackcatalog.CredentialKindUser && !authorization.IsBot {
			return true
		}
	}
	return false
}

func (m *Manager) scheduleDrain(appID string, delay time.Duration) {
	m.mu.Lock()
	if existing := m.drainTimers[appID]; existing != nil {
		existing.Stop()
	}
	m.drainTimers[appID] = time.AfterFunc(delay, func() {
		m.mu.Lock()
		delete(m.drainTimers, appID)
		m.mu.Unlock()
		m.drainProfile(appID)
	})
	m.mu.Unlock()
}

func (m *Manager) drainProfile(appID string) {
	sessions, err := m.journal.PendingSessions(appID)
	if err != nil {
		m.logJournalFailure(appID, "scan")
		return
	}
	needsRetry := false
	for _, sessionID := range sessions {
		batch, claimErr := m.journal.ClaimBatch(appID, sessionID, conversation.MaxSlackEventsPerDispatch, conversation.MaxSlackEventBatchBytes)
		if claimErr != nil {
			m.logJournalFailure(appID, "claim")
			continue
		}
		if len(batch.Events) == 0 {
			continue
		}
		dispatchErr := m.runner.TriggerNowWithSlackEvents(sessionID, true, session.TriggerOnSlack, batch.Events)
		contention := errors.Is(dispatchErr, conversation.ErrSessionBusy) || errors.Is(dispatchErr, conversation.ErrWorkspaceBusy) || errors.Is(dispatchErr, conversation.ErrLoopDispatchCoalesced)
		errorClass := ""
		if dispatchErr != nil && !contention {
			errorClass, needsRetry = "dispatch", true
		}
		if completeErr := m.journal.Complete(batch, dispatchErr == nil, contention, errorClass); completeErr != nil {
			// The batch remains delivering on disk. Restart recovery turns it back
			// into pending, yielding the documented at-least-once crash ambiguity.
			m.logJournalFailure(appID, "complete")
		}
	}
	stats := m.refreshJournalStatus(appID)
	if needsRetry || stats.Failed > 0 {
		m.scheduleDrain(appID, time.Minute)
	}
}

// OnConversationIdle drains every app journal with pending work for sessionID.
// It is invoked after LoopRunner releases the per-session dispatch claim.
func (m *Manager) OnConversationIdle(sessionID string) {
	profiles, err := m.journal.ProfilesForSession(sessionID)
	if err != nil {
		m.logJournalFailure("", "idle_scan")
		return
	}
	for _, appID := range profiles {
		m.drainProfile(appID)
	}
}

func (m *Manager) logJournalFailure(appID, class string) {
	if appID != "" {
		m.mu.Lock()
		status := m.statuses[appID]
		status.AppID = appID
		if status.State == "" {
			status.State = "disconnected"
		}
		status.LastJournalErrorAt = time.Now().UTC()
		m.emitStatusLocked(status)
		m.mu.Unlock()
	}
	if m.logger != nil {
		m.logger.Warn("slackbridge: journal operation failed", "app_id", appID, "error_class", class)
	}
}

func (m *Manager) observeSource(appID string, observation SourceObservation) {
	m.mu.Lock()
	status := m.statuses[appID]
	status.AppID = appID
	status.SubscriptionCount = m.appReferencesLocked(appID)
	now := time.Now().UTC()
	switch observation {
	case SourceTransportReady:
		status.State = "connected"
		status.ConnectedAt = now
		status.RetryAt = time.Time{}
		status.ErrorClass = ""
	case SourceEventsAPIEnvelope:
		status.EventsAPIReceived++
		status.LastEnvelopeAt = now
	case SourceEnvelopeIgnored:
		status.IgnoredCount++
	case SourceAuthorizationError:
		status.LastAuthorizationErrorAt = now
	}
	m.emitStatusLocked(status)
	m.mu.Unlock()
}

func (m *Manager) recordAccepted(appID string) {
	m.mu.Lock()
	status := m.statuses[appID]
	status.AppID = appID
	status.AcceptedCount++
	m.emitStatusLocked(status)
	m.mu.Unlock()
}

func (m *Manager) refreshJournalStatus(appID string) JournalStats {
	stats, err := m.journal.Stats(appID)
	if err != nil {
		m.logJournalFailure(appID, "stats")
		return JournalStats{}
	}
	m.mu.Lock()
	status := m.statuses[appID]
	status.AppID = appID
	if status.State == "" {
		status.State = "disconnected"
	}
	status.PendingCount, status.FailedCount, status.DeadLetterCount, status.DeliveredCount = stats.Pending, stats.Failed, stats.Expired, stats.Delivered
	m.emitStatusLocked(status)
	m.mu.Unlock()
	return stats
}

func (m *Manager) subscriptionCount(appID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appReferencesLocked(appID)
}
func (m *Manager) emitStatus(status ConnectionStatus) {
	m.mu.Lock()
	if previous, ok := m.statuses[status.AppID]; ok {
		status.PendingCount = previous.PendingCount
		status.FailedCount = previous.FailedCount
		status.DeadLetterCount = previous.DeadLetterCount
		status.DeliveredCount = previous.DeliveredCount
		status.EventsAPIReceived = previous.EventsAPIReceived
		status.AcceptedCount = previous.AcceptedCount
		status.IgnoredCount = previous.IgnoredCount
		status.ConnectedAt = previous.ConnectedAt
		status.LastEnvelopeAt = previous.LastEnvelopeAt
		status.LastAuthorizationErrorAt = previous.LastAuthorizationErrorAt
		status.LastJournalErrorAt = previous.LastJournalErrorAt
	}
	m.emitStatusLocked(status)
	m.mu.Unlock()
}
func (m *Manager) emitStatusLocked(status ConnectionStatus) {
	previous, hadPrevious := m.statuses[status.AppID]
	m.statuses[status.AppID] = status
	fn := m.onStatus
	if m.logger != nil {
		// INFO only for a meaningful state transition; a pure counter bump
		// (envelopes/accepted/ignored/delivered counts, connected/envelope
		// timestamps) is logged at DEBUG so a busy workspace doesn't flood
		// the console with identical-state INFO lines on every envelope.
		meaningful := !hadPrevious ||
			previous.State != status.State ||
			previous.ErrorClass != status.ErrorClass ||
			previous.SubscriptionCount != status.SubscriptionCount ||
			previous.PendingCount != status.PendingCount ||
			previous.FailedCount != status.FailedCount ||
			previous.DeadLetterCount != status.DeadLetterCount ||
			!previous.RetryAt.Equal(status.RetryAt) ||
			!previous.LastAuthorizationErrorAt.Equal(status.LastAuthorizationErrorAt) ||
			!previous.LastJournalErrorAt.Equal(status.LastJournalErrorAt)
		args := []any{"app_id", status.AppID, "state", status.State, "subscription_count", status.SubscriptionCount,
			"events_api_received", status.EventsAPIReceived, "accepted_count", status.AcceptedCount, "ignored_count", status.IgnoredCount,
			"pending_count", status.PendingCount, "failed_count", status.FailedCount, "dead_letter_count", status.DeadLetterCount,
			"delivered_count", status.DeliveredCount, "connected_at", status.ConnectedAt, "last_envelope_at", status.LastEnvelopeAt,
			"last_authorization_error_at", status.LastAuthorizationErrorAt, "last_journal_error_at", status.LastJournalErrorAt, "error_class", status.ErrorClass}
		if meaningful {
			m.logger.Info("slackbridge: connection state changed", args...)
		} else {
			m.logger.Debug("slackbridge: connection state changed", args...)
		}
	}
	if fn != nil && !m.statusClosed {
		m.statusQueue = append(m.statusQueue, statusNotification{status: status, fn: fn})
		m.statusCond.Signal()
	}
}

func (m *Manager) dispatchStatuses() {
	defer close(m.statusDone)
	for {
		m.mu.Lock()
		for len(m.statusQueue) == 0 && !m.statusClosed {
			m.statusCond.Wait()
		}
		if len(m.statusQueue) == 0 {
			m.mu.Unlock()
			return
		}
		notification := m.statusQueue[0]
		m.statusQueue[0] = statusNotification{}
		m.statusQueue = m.statusQueue[1:]
		m.mu.Unlock()
		notification.fn(notification.status)
	}
}

// Close cancels and joins every worker.
func (m *Manager) Close() {
	m.cancel()
	m.mu.Lock()
	workers := make([]*appWorker, 0, len(m.workers))
	for _, w := range m.workers {
		if w.timer != nil {
			w.timer.Stop()
		}
		w.cancel()
		workers = append(workers, w)
	}
	for _, timer := range m.drainTimers {
		timer.Stop()
	}
	m.drainTimers = make(map[string]*time.Timer)
	m.workers = make(map[string]*appWorker)
	m.mu.Unlock()
	deadline := time.After(5 * time.Second)
	timedOut := false
	for _, worker := range workers {
		select {
		case <-worker.done:
		case <-deadline:
			timedOut = true
		}
		if timedOut {
			break
		}
	}
	m.mu.Lock()
	m.statusClosed = true
	m.statusCond.Broadcast()
	m.mu.Unlock()
	<-m.statusDone
	if m.journalTemp != "" {
		_ = os.RemoveAll(m.journalTemp)
	}
}
