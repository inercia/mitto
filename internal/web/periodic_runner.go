package web

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/session"
)

const (
	// DefaultPollInterval is the default interval between periodic prompt checks.
	DefaultPollInterval = 1 * time.Minute

	// MaxPeriodicResumeFailures is the number of consecutive ACP resume failures
	// after which a periodic session's schedule is automatically disabled.
	MaxPeriodicResumeFailures = 3
)

// Errors for periodic runner operations.
var (
	ErrSessionStoreNotAvailable   = errors.New("session store not available")
	ErrSessionManagerNotAvailable = errors.New("session manager not available")
	ErrPeriodicNotEnabled         = errors.New("periodic is not enabled for this session")
	ErrSessionBusy                = errors.New("session is currently processing a prompt")
)

// PeriodicStartedCallback is called when a periodic prompt is delivered.
// sessionID is the session that received the prompt.
// sessionName is the display name of the session.
type PeriodicStartedCallback func(sessionID, sessionName string)

// AutoArchiveCallback is called when the periodic runner auto-archives a session.
// It should handle broadcasting the archive state change and stopping ACP.
type AutoArchiveCallback func(sessionID string)

// PeriodicRunner manages scheduled periodic prompt delivery and session housekeeping.
// It polls all sessions at regular intervals and:
// - Delivers periodic prompts that are due
// - Auto-archives sessions inactive beyond the configured threshold
// - Cleans up archived sessions past their retention period
type PeriodicRunner struct {
	store          *session.Store
	sessionManager *SessionManager
	logger         *slog.Logger

	pollInterval time.Duration

	// onPeriodicStarted is called when a periodic prompt is delivered
	onPeriodicStarted PeriodicStartedCallback

	// onAutoArchive is called when a session is auto-archived.
	// The callback should broadcast the archive state change and ACP stop to WebSocket clients.
	onAutoArchive AutoArchiveCallback

	// autoArchiveAfter, when > 0, causes sessions inactive for this long to be archived.
	autoArchiveAfter time.Duration

	// archiveRetentionPeriod, when non-empty, causes archived sessions older than this
	// to be permanently deleted during each poll cycle (not just at startup).
	archiveRetentionPeriod string

	// consecutiveFailures tracks how many times in a row a session's periodic
	// prompt delivery failed due to ACP resume errors. After MaxPeriodicResumeFailures
	// consecutive failures, the periodic config is automatically disabled.
	consecutiveFailures   map[string]int
	consecutiveFailuresMu sync.Mutex

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewPeriodicRunner creates a new periodic runner.
func NewPeriodicRunner(store *session.Store, sm *SessionManager, logger *slog.Logger) *PeriodicRunner {
	return &PeriodicRunner{
		store:               store,
		sessionManager:      sm,
		logger:              logger,
		pollInterval:        DefaultPollInterval,
		consecutiveFailures: make(map[string]int),
	}
}

// SetPollInterval sets the polling interval. Must be called before Start().
func (r *PeriodicRunner) SetPollInterval(interval time.Duration) {
	r.pollInterval = interval
}

// SetOnPeriodicStarted sets the callback for when a periodic prompt is delivered.
func (r *PeriodicRunner) SetOnPeriodicStarted(callback PeriodicStartedCallback) {
	r.onPeriodicStarted = callback
}

// SetAutoArchiveAfter configures the runner to automatically archive sessions
// that have been inactive for longer than the given duration.
// A duration of 0 disables auto-archiving.
func (r *PeriodicRunner) SetAutoArchiveAfter(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoArchiveAfter = d
}

// SetOnAutoArchive sets the callback for when a session is auto-archived.
func (r *PeriodicRunner) SetOnAutoArchive(callback AutoArchiveCallback) {
	r.onAutoArchive = callback
}

// SetArchiveRetentionPeriod sets the retention period for archived session cleanup.
// When set, archived sessions older than this period are permanently deleted during each poll.
// Pass an empty string to disable periodic cleanup.
func (r *PeriodicRunner) SetArchiveRetentionPeriod(period string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archiveRetentionPeriod = period
}

// Start begins the periodic polling loop in a background goroutine.
// It returns immediately. Call Stop() to stop the runner.
func (r *PeriodicRunner) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return
	}

	r.running = true
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})

	go r.pollLoop()

	if r.logger != nil {
		r.logger.Debug("Periodic runner started", "poll_interval", r.pollInterval)
	}
}

// Stop gracefully stops the periodic runner and waits for it to finish.
func (r *PeriodicRunner) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	close(r.stopCh)
	doneCh := r.doneCh
	r.mu.Unlock()

	// Wait for the poll loop to finish
	<-doneCh

	if r.logger != nil {
		r.logger.Debug("Periodic runner stopped")
	}
}

// IsRunning returns true if the runner is currently active.
func (r *PeriodicRunner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// TriggerNow immediately delivers the periodic prompt for a session,
// bypassing the normal schedule check. This is used for manual "run now" requests.
// Returns an error if the delivery fails or the session is not configured for periodic prompts.
func (r *PeriodicRunner) TriggerNow(sessionID string) error {
	if r.store == nil {
		return ErrSessionStoreNotAvailable
	}

	// Get session metadata
	meta, err := r.store.GetMetadata(sessionID)
	if err != nil {
		return err
	}

	// Get periodic config for this session
	periodicStore := r.store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil {
		return err
	}

	// Check if enabled
	if !periodic.Enabled {
		return ErrPeriodicNotEnabled
	}

	// Check if session manager is available
	if r.sessionManager == nil {
		return ErrSessionManagerNotAvailable
	}

	// Check if session is running (has an active ACP connection)
	bs := r.sessionManager.GetSession(sessionID)
	if bs == nil {
		// Session not running - auto-resume it to deliver the periodic prompt
		if r.logger != nil {
			r.logger.Debug("Auto-resuming session for immediate periodic delivery",
				"session_id", sessionID,
				"session_name", meta.Name)
		}

		bs, err = r.sessionManager.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
		if err != nil {
			return err
		}

		if r.logger != nil {
			r.logger.Info("Session auto-resumed for immediate periodic delivery",
				"session_id", sessionID,
				"session_name", meta.Name)
		}
	}

	// Check if session is currently processing a prompt
	if bs.IsPrompting() {
		return ErrSessionBusy
	}

	if r.logger != nil {
		r.logger.Info("Triggering immediate periodic delivery",
			"session_id", sessionID,
			"session_name", meta.Name,
			"prompt_preview", truncatePrompt(periodic.Prompt, 100))
	}

	// Deliver the prompt
	return r.deliverPrompt(bs, meta.Name, periodic, periodicStore)
}

// pollLoop is the main polling loop that checks for due prompts.
func (r *PeriodicRunner) pollLoop() {
	defer close(r.doneCh)

	// Run immediately on start to handle any prompts that were due
	r.RunOnce()

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.RunOnce()
		}
	}
}

// RunOnce performs a single poll iteration, checking all sessions for due prompts,
// auto-archiving inactive sessions, and cleaning up old archived sessions.
// Returns counts of delivered, skipped, and errored prompts.
// This method is exported for testing purposes.
func (r *PeriodicRunner) RunOnce() (delivered, skipped, errored int) {
	if r.store == nil {
		return 0, 0, 0
	}

	// List all sessions
	sessions, err := r.store.List()
	if err != nil {
		if r.logger != nil {
			r.logger.Error("Failed to list sessions for periodic check", "error", err)
		}
		return 0, 0, 1
	}

	now := time.Now().UTC()

	for _, meta := range sessions {
		d, s, e := r.checkSession(meta, now)
		delivered += d
		skipped += s
		errored += e
	}

	// Auto-archive inactive sessions
	r.checkAutoArchive(sessions, now)

	// Clean up archived sessions past retention
	r.checkArchiveCleanup()

	if r.logger != nil {
		r.logger.Debug("Periodic poll completed",
			"delivered", delivered,
			"skipped", skipped,
			"errored", errored)
	}

	return delivered, skipped, errored
}

// checkSession checks a single session for due periodic prompts.
// Returns (1, 0, 0) if delivered, (0, 1, 0) if skipped, (0, 0, 1) if error.
func (r *PeriodicRunner) checkSession(meta session.Metadata, now time.Time) (delivered, skipped, errored int) {
	sessionID := meta.SessionID

	// Skip archived sessions - periodic prompts are inactive for archived sessions
	if meta.Archived {
		return 0, 0, 0
	}

	// Get periodic config for this session
	periodicStore := r.store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil {
		if err == session.ErrPeriodicNotFound {
			// No periodic config - this is normal, not an error
			return 0, 0, 0
		}
		if r.logger != nil {
			r.logger.Error("Failed to read periodic config",
				"session_id", sessionID,
				"error", err)
		}
		return 0, 0, 1
	}

	// Skip if disabled
	if !periodic.Enabled {
		return 0, 0, 0
	}

	// Check if due
	if periodic.NextScheduledAt == nil || periodic.NextScheduledAt.After(now) {
		return 0, 0, 0
	}

	// Prompt is due - calculate how overdue it is
	scheduledAt := *periodic.NextScheduledAt
	overdueBy := now.Sub(scheduledAt)

	// Calculate how many runs were missed (for logging purposes)
	missedRuns := 0
	if overdueBy > 0 && periodic.Frequency.Duration() > 0 {
		// Number of full intervals that passed since scheduled time
		missedRuns = int(overdueBy / periodic.Frequency.Duration())
	}

	// Log the catch-up situation
	if r.logger != nil {
		if missedRuns > 0 {
			r.logger.Debug("Periodic prompt overdue - running catch-up (skipping missed runs)",
				"session_id", sessionID,
				"scheduled_at", scheduledAt,
				"overdue_by", overdueBy.Round(time.Second),
				"missed_runs", missedRuns,
				"prompt_preview", truncatePrompt(periodic.Prompt, 50))
		} else {
			r.logger.Debug("Periodic prompt is due",
				"session_id", sessionID,
				"scheduled_at", scheduledAt,
				"prompt_preview", truncatePrompt(periodic.Prompt, 50))
		}
	}

	// Check if session manager is available
	if r.sessionManager == nil {
		if r.logger != nil {
			r.logger.Debug("Skipping periodic prompt - no session manager",
				"session_id", sessionID)
		}
		return 0, 1, 0
	}

	// Check if session is running (has an active ACP connection)
	bs := r.sessionManager.GetSession(sessionID)
	if bs == nil {
		// Session not running - auto-resume it to deliver the periodic prompt
		if r.logger != nil {
			r.logger.Debug("Auto-resuming session for periodic prompt",
				"session_id", sessionID,
				"session_name", meta.Name)
		}

		var err error
		bs, err = r.sessionManager.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
		if err != nil {
			r.consecutiveFailuresMu.Lock()
			r.consecutiveFailures[sessionID]++
			failures := r.consecutiveFailures[sessionID]
			r.consecutiveFailuresMu.Unlock()

			if r.logger != nil {
				r.logger.Error("Failed to resume session for periodic prompt",
					"session_id", sessionID,
					"consecutive_failures", failures,
					"max_failures", MaxPeriodicResumeFailures,
					"error", err)
			}

			// After too many consecutive failures, disable the periodic schedule
			// to stop the retry storm. The user can re-enable it manually.
			if failures >= MaxPeriodicResumeFailures {
				if r.logger != nil {
					r.logger.Warn("Disabling periodic schedule after repeated failures",
						"session_id", sessionID,
						"session_name", meta.Name,
						"consecutive_failures", failures)
				}
				disabled := false
				if updateErr := periodicStore.Update(nil, nil, &disabled); updateErr != nil {
					if r.logger != nil {
						r.logger.Error("Failed to disable periodic schedule",
							"session_id", sessionID,
							"error", updateErr)
					}
				}
				// Reset counter after disabling
				r.consecutiveFailuresMu.Lock()
				delete(r.consecutiveFailures, sessionID)
				r.consecutiveFailuresMu.Unlock()
			}
			return 0, 0, 1
		}

		// Reset consecutive failure counter on successful resume
		r.consecutiveFailuresMu.Lock()
		delete(r.consecutiveFailures, sessionID)
		r.consecutiveFailuresMu.Unlock()

		if r.logger != nil {
			r.logger.Info("Session auto-resumed for periodic prompt",
				"session_id", sessionID,
				"session_name", meta.Name)
		}
	}

	// Check if session is currently processing a prompt
	if bs.IsPrompting() {
		if r.logger != nil {
			r.logger.Debug("Skipping periodic prompt - session is busy",
				"session_id", sessionID)
		}
		return 0, 1, 0
	}

	// Deliver the prompt
	if err := r.deliverPrompt(bs, meta.Name, periodic, periodicStore); err != nil {
		if r.logger != nil {
			r.logger.Error("Failed to deliver periodic prompt",
				"session_id", sessionID,
				"error", err)
		}
		return 0, 0, 1
	}

	return 1, 0, 0
}

// deliverPrompt sends the periodic prompt to the session.
func (r *PeriodicRunner) deliverPrompt(bs *BackgroundSession, sessionName string, periodic *session.PeriodicPrompt, periodicStore *session.PeriodicStore) error {
	sessionID := bs.GetSessionID()

	if r.logger != nil {
		r.logger.Debug("Delivering periodic prompt",
			"session_id", sessionID,
			"session_name", sessionName,
			"prompt_preview", truncatePrompt(periodic.Prompt, 100))
	}

	// Send the prompt with metadata indicating it's periodic
	// Using a special sender ID to identify periodic prompts
	meta := PromptMeta{
		SenderID: "periodic-runner",
		PromptID: "", // No client to confirm delivery to
	}

	if err := bs.PromptWithMeta(periodic.Prompt, meta); err != nil {
		return err
	}

	// Notify about the periodic prompt delivery
	if r.onPeriodicStarted != nil {
		r.onPeriodicStarted(sessionID, sessionName)
	}

	// Update last_sent_at and next_scheduled_at
	if err := periodicStore.RecordSent(); err != nil {
		// Log but don't fail - the prompt was sent successfully
		if r.logger != nil {
			r.logger.Warn("Failed to update periodic last_sent_at",
				"session_id", sessionID,
				"error", err)
		}
	} else {
		// Log the new schedule (useful for debugging catch-up behavior)
		if r.logger != nil {
			if updated, err := periodicStore.Get(); err == nil && updated.NextScheduledAt != nil {
				r.logger.Debug("Periodic schedule updated after delivery",
					"session_id", sessionID,
					"next_scheduled_at", updated.NextScheduledAt)
			}
		}
	}

	return nil
}

// truncatePrompt truncates a string to maxLen characters, adding "..." if truncated.
func truncatePrompt(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// autoArchiveWaitTimeout is the maximum time to wait for a response to complete
// before forcibly closing a session during auto-archiving.
const autoArchiveWaitTimeout = 30 * time.Second

// checkAutoArchive archives sessions that have been inactive for longer than autoArchiveAfter.
// It skips sessions that are already archived, child sessions (children are archived via parent cascade),
// or sessions with enabled periodic prompts (they should remain active indefinitely).
func (r *PeriodicRunner) checkAutoArchive(sessions []session.Metadata, now time.Time) {
	r.mu.Lock()
	threshold := r.autoArchiveAfter
	r.mu.Unlock()

	if threshold <= 0 {
		return
	}

	if r.sessionManager == nil {
		return
	}

	for _, meta := range sessions {
		// Skip already archived sessions
		if meta.Archived {
			continue
		}

		// Skip child sessions — they are archived via parent cascade only
		if meta.ParentSessionID != "" {
			continue
		}

		// Skip sessions with enabled periodic prompts — they should remain active indefinitely
		periodicStore := r.store.Periodic(meta.SessionID)
		periodic, err := periodicStore.Get()
		if err != nil && err != session.ErrPeriodicNotFound {
			if r.logger != nil {
				r.logger.Error("Failed to read periodic config during auto-archive check",
					"session_id", meta.SessionID,
					"error", err)
			}
			// Continue processing other sessions even if we can't read this one's config
			continue
		}
		if err == nil && periodic.Enabled {
			if r.logger != nil {
				r.logger.Debug("Skipping auto-archive for periodic session",
					"session_id", meta.SessionID,
					"session_name", meta.Name)
			}
			continue
		}

		// Check inactivity: use LastUserMessageAt if available, fall back to UpdatedAt
		lastActivity := meta.UpdatedAt
		if !meta.LastUserMessageAt.IsZero() && meta.LastUserMessageAt.After(lastActivity) {
			lastActivity = meta.LastUserMessageAt
		}

		if now.Sub(lastActivity) < threshold {
			continue
		}

		// Session is inactive beyond threshold — auto-archive it
		sessionID := meta.SessionID
		if r.logger != nil {
			r.logger.Info("Auto-archiving inactive session",
				"session_id", sessionID,
				"session_name", meta.Name,
				"last_activity", lastActivity,
				"inactive_for", now.Sub(lastActivity).Round(time.Minute))
		}

		// 1. Gracefully close ACP process (wait for any in-progress response)
		reason := "auto_archived"
		if !r.sessionManager.CloseSessionGracefully(sessionID, reason, autoArchiveWaitTimeout) {
			if r.logger != nil {
				r.logger.Warn("Timeout waiting for response before auto-archiving, forcing close",
					"session_id", sessionID)
			}
			reason = "auto_archived_timeout"
			r.sessionManager.CloseSession(sessionID, reason)
		}

		// 2. Update metadata to mark as archived
		err = r.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
			m.Archived = true
			m.ArchivedAt = now
		})
		if err != nil {
			if r.logger != nil {
				r.logger.Error("Failed to mark session as archived",
					"session_id", sessionID,
					"error", err)
			}
			continue
		}

		// 3. Notify via callback (broadcasts to WebSocket clients)
		if r.onAutoArchive != nil {
			r.onAutoArchive(sessionID)
		}

		// 4. Delete child sessions (async, same as manual archive)
		go r.sessionManager.DeleteChildSessions(sessionID)

		if r.logger != nil {
			r.logger.Info("Session auto-archived successfully",
				"session_id", sessionID,
				"session_name", meta.Name)
		}
	}
}

// checkArchiveCleanup permanently deletes archived sessions older than the retention period.
func (r *PeriodicRunner) checkArchiveCleanup() {
	r.mu.Lock()
	retentionPeriod := r.archiveRetentionPeriod
	r.mu.Unlock()

	if retentionPeriod == "" {
		return
	}

	deleted, err := r.store.CleanupArchivedSessions(retentionPeriod)
	if err != nil {
		if r.logger != nil {
			r.logger.Error("Failed to clean up archived sessions",
				"retention_period", retentionPeriod,
				"error", err)
		}
		return
	}

	if deleted > 0 && r.logger != nil {
		r.logger.Info("Periodic archive cleanup completed",
			"deleted_count", deleted,
			"retention_period", retentionPeriod)
	}
}
