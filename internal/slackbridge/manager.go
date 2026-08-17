package slackbridge

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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
	AppID             string    `json:"app_id"`
	State             string    `json:"state"`
	SubscriptionCount int       `json:"subscription_count"`
	PendingCount      int       `json:"pending_count,omitempty"`
	FailedCount       int       `json:"failed_count,omitempty"`
	DeadLetterCount   int       `json:"dead_letter_count,omitempty"`
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
				m.emitStatus(ConnectionStatus{AppID: appID, State: "connected", SubscriptionCount: m.subscriptionCount(appID)})
				if durable, ok := source.(DurableSource); ok {
					err = durable.RunDurable(ctx, func(evt Event) error { return m.routeEvent(appID, worker, evt) })
				} else {
					err = source.Run(ctx, func(evt Event) {
						if acceptErr := m.routeEvent(appID, worker, evt); acceptErr != nil && m.logger != nil {
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
		return errors.New("Slack event has no event_id")
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
		targets[sub.sessionID] = journalRecipient{SessionID: sub.sessionID, InstallationID: sub.installationID}
	}
	recipients := make([]journalRecipient, 0, len(targets))
	for _, recipient := range targets {
		recipients = append(recipients, recipient)
	}
	evt.Text = boundSlackText(evt.Text)
	duplicate, err := m.journal.Accept(appID, evt, recipients)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("slackbridge: durable event acceptance failed", "app_id", appID, "error_class", "journal")
		}
		return err
	}
	m.refreshJournalStatus(appID)
	if duplicate || len(recipients) == 0 {
		return nil
	}
	if m.settle <= 0 {
		m.drainProfile(appID)
	} else {
		m.scheduleDrain(appID, m.settle)
	}
	return nil
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
	if m.logger != nil {
		m.logger.Warn("slackbridge: journal operation failed", "app_id", appID, "error_class", class)
	}
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
	status.PendingCount, status.FailedCount, status.DeadLetterCount = stats.Pending, stats.Failed, stats.Expired
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
	}
	m.emitStatusLocked(status)
	m.mu.Unlock()
}
func (m *Manager) emitStatusLocked(status ConnectionStatus) {
	m.statuses[status.AppID] = status
	fn := m.onStatus
	if m.logger != nil {
		m.logger.Info("slackbridge: connection state changed", "app_id", status.AppID, "state", status.State, "subscription_count", status.SubscriptionCount,
			"pending_count", status.PendingCount, "failed_count", status.FailedCount, "dead_letter_count", status.DeadLetterCount, "error_class", status.ErrorClass)
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
