package web

import (
	"log/slog"
	"time"
)

// GCConfig configures the garbage collection loop.
type GCConfig struct {
	// Interval is how often the GC runs (default: 30s).
	Interval time.Duration
	// GracePeriod is how long a process must be sessionless before it is stopped (default: 60s).
	GracePeriod time.Duration
}

// SessionInfo contains the minimum information the GC needs about a session.
type SessionInfo struct {
	SessionID     string
	WorkspaceUUID string
	IsPrompting   bool
	HasObservers  bool
	QueueLength   int
	// NextPeriodicAt is when the next periodic prompt is due (nil = no periodic config).
	NextPeriodicAt *time.Time
}

// SessionQueryFunc returns running sessions grouped by workspace UUID.
// Used by the GC to determine which processes still have active sessions.
type SessionQueryFunc func() map[string][]SessionInfo

// SessionCloseFunc closes an idle session by session ID.
type SessionCloseFunc func(sessionID string)

// defaultGCConfig returns a GCConfig with sensible defaults.
func defaultGCConfig() GCConfig {
	return GCConfig{
		Interval:    30 * time.Second,
		GracePeriod: 60 * time.Second,
	}
}

// StartGC starts the GC goroutine loop. It is a no-op if the GC is already running.
// query is called each GC cycle to enumerate running sessions by workspace.
// closeSession is called on idle sessions identified in Tier 1.
func (m *ACPProcessManager) StartGC(config GCConfig, query SessionQueryFunc, closeSession SessionCloseFunc) {
	m.gcMu.Lock()
	defer m.gcMu.Unlock()

	if m.gcRunning {
		return
	}

	if config.Interval <= 0 {
		config.Interval = defaultGCConfig().Interval
	}
	if config.GracePeriod <= 0 {
		config.GracePeriod = defaultGCConfig().GracePeriod
	}

	m.gcConfig = config
	m.sessionQuery = query
	m.sessionClose = closeSession
	m.gcStop = make(chan struct{})
	m.gcDone = make(chan struct{})
	m.gcRunning = true

	if m.lastSessionSeen == nil {
		m.lastSessionSeen = make(map[string]time.Time)
	}

	go m.gcLoop()

	if m.logger != nil {
		m.logger.Debug("ACP process GC started",
			"interval", config.Interval,
			"grace_period", config.GracePeriod)
	}
}

// StopGC stops the GC goroutine and waits for it to finish. It is a no-op if
// the GC is not running.
func (m *ACPProcessManager) StopGC() {
	m.gcMu.Lock()
	if !m.gcRunning {
		m.gcMu.Unlock()
		return
	}
	m.gcRunning = false
	close(m.gcStop)
	doneCh := m.gcDone
	m.gcMu.Unlock()

	<-doneCh

	if m.logger != nil {
		m.logger.Debug("ACP process GC stopped")
	}
}

// gcLoop is the background goroutine that runs RunGCOnce periodically.
func (m *ACPProcessManager) gcLoop() {
	defer func() {
		m.gcMu.Lock()
		close(m.gcDone)
		m.gcMu.Unlock()
	}()

	ticker := time.NewTicker(m.gcConfig.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.gcStop:
			return
		case <-ticker.C:
			m.RunGCOnce()
		}
	}
}

// RunGCOnce executes a single GC iteration. It is exported for testing.
//
// Tier 1 closes idle sessions — those with no WebSocket observers, no active
// prompt, an empty queue, and no periodic prompt due within 2× the GC interval.
//
// Tier 2 stops shared ACP processes that have had no active sessions for longer
// than the configured grace period.
func (m *ACPProcessManager) RunGCOnce() {
	if m.sessionQuery == nil || m.sessionClose == nil {
		return
	}

	now := time.Now()

	// ----------------------------------------------------------------
	// Tier 1: close idle sessions
	// ----------------------------------------------------------------
	sessionsByWorkspace := m.sessionQuery()

	for workspaceUUID, sessions := range sessionsByWorkspace {
		for _, s := range sessions {
			if s.IsPrompting {
				if m.logger != nil {
					m.logger.Debug("GC: skipping session (prompting)",
						"session_id", s.SessionID,
						"workspace_uuid", workspaceUUID)
				}
				continue
			}
			if s.HasObservers {
				if m.logger != nil {
					m.logger.Debug("GC: skipping session (has observers)",
						"session_id", s.SessionID,
						"workspace_uuid", workspaceUUID)
				}
				continue
			}
			if s.QueueLength > 0 {
				if m.logger != nil {
					m.logger.Debug("GC: skipping session (non-empty queue)",
						"session_id", s.SessionID,
						"workspace_uuid", workspaceUUID,
						"queue_length", s.QueueLength)
				}
				continue
			}
			if s.NextPeriodicAt != nil {
				threshold := now.Add(2 * m.gcConfig.Interval)
				if s.NextPeriodicAt.Before(threshold) {
					if m.logger != nil {
						m.logger.Debug("GC: skipping session (periodic prompt due soon)",
							"session_id", s.SessionID,
							"workspace_uuid", workspaceUUID,
							"next_periodic_at", s.NextPeriodicAt)
					}
					continue
				}
			}

			if m.logger != nil {
				m.logger.Info("GC: closing idle session",
					"session_id", s.SessionID,
					"workspace_uuid", workspaceUUID)
			}
			m.sessionClose(s.SessionID)
		}
	}

	// ----------------------------------------------------------------
	// Tier 2: stop idle processes
	// Re-query after tier 1 so that newly closed sessions are excluded.
	// ----------------------------------------------------------------
	sessionsByWorkspace = m.sessionQuery()

	m.mu.RLock()
	workspaceUUIDs := make([]string, 0, len(m.processes))
	for uuid := range m.processes {
		workspaceUUIDs = append(workspaceUUIDs, uuid)
	}
	m.mu.RUnlock()

	m.gcMu.Lock()
	for _, workspaceUUID := range workspaceUUIDs {
		if sessions, ok := sessionsByWorkspace[workspaceUUID]; ok && len(sessions) > 0 {
			// Sessions still present — reset the clock.
			m.lastSessionSeen[workspaceUUID] = now
			continue
		}

		// No active sessions for this workspace.
		last, seen := m.lastSessionSeen[workspaceUUID]
		if !seen {
			// First time we see it empty — record the time and give it a grace period.
			m.lastSessionSeen[workspaceUUID] = now
			if m.logger != nil {
				m.logger.Debug("GC: workspace process now sessionless (grace period started)",
					"workspace_uuid", workspaceUUID,
					"grace_period", m.gcConfig.GracePeriod)
			}
			continue
		}

		if now.Sub(last) < m.gcConfig.GracePeriod {
			// Still within grace period.
			if m.logger != nil {
				m.logger.Debug("GC: workspace process sessionless but within grace period",
					"workspace_uuid", workspaceUUID,
					"sessionless_for", now.Sub(last))
			}
			continue
		}

		// Grace period expired — but check for in-flight RPCs first.
		// LoadSession and NewSession can take 70+ seconds. If we kill the pipe while
		// one is in-flight, the RPC fails with "peer disconnected" and the fallback
		// NewSession then fails with "write |1: file already closed". Both affected
		// sessions hard-fail with "Failed to resume session".
		// Reset the grace period clock and skip this cycle; the process will be
		// stopped once all RPCs complete.
		if p := m.GetProcess(workspaceUUID); p != nil && p.ActiveRPCs() > 0 {
			if m.logger != nil {
				m.logger.Info("GC: deferring process stop (in-flight RPCs)",
					"workspace_uuid", workspaceUUID,
					"active_rpcs", p.ActiveRPCs())
			}
			m.lastSessionSeen[workspaceUUID] = now
			continue
		}

		// No in-flight RPCs — stop the process.
		delete(m.lastSessionSeen, workspaceUUID)
		m.gcMu.Unlock()

		if m.logger != nil {
			m.logger.Info("GC: stopping idle shared ACP process",
				"workspace_uuid", workspaceUUID,
				"sessionless_for", now.Sub(last),
				slog.Group("gc",
					"interval", m.gcConfig.Interval,
					"grace_period", m.gcConfig.GracePeriod))
		}
		m.StopProcess(workspaceUUID)

		m.gcMu.Lock()
	}
	m.gcMu.Unlock()
}
