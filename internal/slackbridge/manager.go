package slackbridge

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/slackcatalog"
)

const defaultUnusedGrace = 30 * time.Second

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
	AppID             string    `json:"app_id"`
	State             string    `json:"state"`
	SubscriptionCount int       `json:"subscription_count"`
	RetryAt           time.Time `json:"retry_at,omitempty"`
	ErrorClass        string    `json:"error_class,omitempty"`
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
}

type appWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
	dedupe *dedupeSet
	timer  *time.Timer
}

// Manager owns one Socket Mode worker per referenced Slack app profile and an
// idempotent, process-local loop-subscription index.
type Manager struct {
	mu          sync.Mutex
	store       *session.Store
	catalog     Catalog
	credentials CredentialResolver
	runner      ManagedLoopTriggerer
	factory     SourceFactory
	logger      *slog.Logger
	grace       time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	sessions    map[string][]resolvedSubscription
	workers     map[string]*appWorker
	statuses    map[string]ConnectionStatus
	onStatus    func(ConnectionStatus)
}

// NewManager constructs a manager. Call Start after all callbacks are wired.
func NewManager(store *session.Store, catalog Catalog, credentials CredentialResolver, runner ManagedLoopTriggerer, logger *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{store: store, catalog: catalog, credentials: credentials, runner: runner, logger: logger,
		grace: defaultUnusedGrace, ctx: ctx, cancel: cancel, sessions: make(map[string][]resolvedSubscription),
		workers: make(map[string]*appWorker), statuses: make(map[string]ConnectionStatus)}
	m.factory = func(_ string, token string) (Source, error) {
		return NewSlackSource(Config{AppToken: token}, logger, nil), nil
	}
	return m
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

// Start rebuilds subscriptions from persisted sessions and starts needed workers.
func (m *Manager) Start() error { return m.ReconcileAll() }

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
	loop, loopErr := m.store.Loop(sessionID).Get()
	if loopErr == nil && !meta.Archived && loop.Enabled && loop.IsOnSlack() {
		for _, sub := range loop.SlackSubscriptions {
			installation, err := m.catalog.GetInstallation(sub.InstallationID)
			if err != nil {
				continue
			}
			resolved = append(resolved, resolvedSubscription{sessionID: sessionID, appID: installation.AppID,
				installationID: installation.ID, teamID: installation.TeamID, channelID: sub.ChannelID,
				eventMode: sub.EventMode, threadPolicy: sub.ThreadPolicy,
				botID: installation.BotID, botUserID: installation.BotUserID})
		}
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
	wanted := make(map[string]bool, len(installationIDs))
	for _, id := range installationIDs {
		wanted[id] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]bool)
	var refs []slackcatalog.Reference
	for sessionID, subs := range m.sessions {
		for _, sub := range subs {
			if sub.appID == appID && (len(wanted) == 0 || wanted[sub.installationID]) {
				if !seen[sessionID] {
					refs = append(refs, slackcatalog.Reference{SessionID: sessionID})
					seen[sessionID] = true
				}
				break
			}
		}
	}
	return refs, nil
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
	worker := &appWorker{cancel: cancel, done: make(chan struct{}), dedupe: newDedupeSet(0)}
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
				m.emitStatus(ConnectionStatus{AppID: appID, State: "connected", SubscriptionCount: m.subscriptionCount(appID)})
				err = source.Run(ctx, func(evt Event) { m.routeEvent(appID, worker, evt) })
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
	m.emitStatus(ConnectionStatus{AppID: appID, State: "disconnected"})
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

func (m *Manager) routeEvent(appID string, worker *appWorker, evt Event) {
	if evt.EventID == "" || worker.dedupe.SeenBefore(evt.EventID) {
		return
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
	targets := make(map[string]conversation.PromptSlackEvent)
	for _, sub := range candidates {
		if evt.AuthorID == sub.botID || evt.AuthorID == sub.botUserID {
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
		targets[sub.sessionID] = conversation.PromptSlackEvent{InstallationID: sub.installationID,
			EventID: evt.EventID, ChannelID: evt.ChannelID, Kind: evt.Kind, AuthorID: evt.AuthorID,
			Timestamp: evt.Timestamp, ThreadTimestamp: evt.ThreadTimestamp, Text: boundSlackText(evt.Text), Untrusted: true}
	}
	for sessionID, event := range targets {
		if err := m.runner.TriggerNowWithSlackEvents(sessionID, true, session.TriggerOnSlack, []conversation.PromptSlackEvent{event}); err != nil && m.logger != nil {
			m.logger.Warn("slackbridge: failed to trigger subscribed loop", "session_id", sessionID, "error_class", "dispatch")
		}
	}
}

func (m *Manager) subscriptionCount(appID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appReferencesLocked(appID)
}
func (m *Manager) emitStatus(status ConnectionStatus) {
	m.mu.Lock()
	m.emitStatusLocked(status)
	m.mu.Unlock()
}
func (m *Manager) emitStatusLocked(status ConnectionStatus) {
	m.statuses[status.AppID] = status
	fn := m.onStatus
	if m.logger != nil {
		m.logger.Info("slackbridge: connection state changed", "app_id", status.AppID, "state", status.State, "subscription_count", status.SubscriptionCount, "error_class", status.ErrorClass)
	}
	if fn != nil {
		go fn(status)
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
	m.workers = make(map[string]*appWorker)
	m.mu.Unlock()
	deadline := time.After(5 * time.Second)
	for _, worker := range workers {
		select {
		case <-worker.done:
		case <-deadline:
			return
		}
	}
}
