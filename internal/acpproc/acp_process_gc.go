package acpproc

import (
	"log/slog"
	"time"

	"github.com/inercia/mitto/internal/conversation"
)

// tier6StreamedProgressQuietWindow is the minimum time since a session's last
// streamed agent update before Tier 6 considers it eligible for recycle
// (mitto-1h0). Mirrors conversation.agentWorkingHeartbeatQuietThreshold (10s):
// that constant is unexported in package conversation, so it is duplicated here
// rather than imported. Keep the two values in sync if either changes.
const tier6StreamedProgressQuietWindow = 10 * time.Second

// GCConfig configures the garbage collection loop.
type GCConfig struct {
	// Interval is how often the GC runs (default: 30s).
	Interval time.Duration
	// GracePeriod is how long a process must be sessionless before it is stopped (default: 60s).
	GracePeriod time.Duration
	// ObserverGracePeriod is how long to keep a session alive after the last observer
	// disconnects. Prevents closing sessions during transient reconnect windows (default: 60s).
	ObserverGracePeriod time.Duration
	// IdleTimeout is how long a session must be inactive before the GC considers
	// closing it. Recent keepalive, prompt, or observer changes reset this (default: 5m).
	IdleTimeout time.Duration
	// MaxClosuresPerCycle limits how many sessions the GC closes per cycle.
	// 0 means unlimited. Prevents reconnection storms when many sessions go idle at once.
	MaxClosuresPerCycle int
	// AuxIdleTimeout is how long an auxiliary session can be idle before the GC
	// cleans it up. Cleaned-up sessions are lazily re-created on next use (default: 10m).
	AuxIdleTimeout time.Duration
	// ChildIdleTimeout is how long a child session (spawned via MCP mitto_conversation_new)
	// must be inactive before the GC considers closing it. Child sessions are less critical
	// than user-created sessions and should be reclaimed faster to reduce memory pressure.
	// Default: 5m (same as IdleTimeout). Set shorter than IdleTimeout for faster child GC.
	ChildIdleTimeout time.Duration
	// LoopSuspendThreshold is the minimum time until the next loop prompt
	// before a loop session is eligible for suspension. Loop sessions whose
	// next run is farther away than this threshold will have their ACP connection
	// closed even if they have active WebSocket observers. The session is NOT archived —
	// it remains visible in the sidebar and resumes transparently via ensure_resumed
	// when the user focuses it, or via the LoopRunner when the prompt is due.
	// This saves significant memory by stopping MCP server processes for idle loop
	// conversations. Set to 0 to disable loop suspension (default: 30m).
	LoopSuspendThreshold time.Duration
	// LoopSuspendGracePeriod is a generous post-activity buffer that protects a
	// loop session from being suspended too soon after it finishes a turn. A
	// loop session is NOT suspended while its most recent activity — the agent
	// completing a response (LastResponseCompleteAt) or a prompt/observer change
	// (LastActivityAt) — is within this window. This prevents aggressively reclaiming
	// a conversation that just ended a turn and may be about to continue (a queued
	// follow-up, a nudge, or the user inspecting results). It is deliberately distinct
	// from LastActivityAt-only checks because LastActivityAt is set at prompt START and
	// is therefore stale after a long-running task. Set to a negative value to disable
	// the grace window (default: 10m).
	LoopSuspendGracePeriod time.Duration
	// MemoryRecycleThreshold is the RSS threshold in bytes (summed over the agent
	// process tree) above which an IDLE shared ACP process is recycled (stopped) to
	// reclaim memory. Recycling only happens when the process has no prompting
	// session, no in-flight RPCs, empty queues, and no loop prompt due soon —
	// affected conversations resume transparently on next focus. 0 means disabled
	// (opt-in; no default is applied).
	MemoryRecycleThreshold uint64
}

// SessionQueryFunc returns running sessions grouped by workspace UUID.
// Used by the GC to determine which processes still have active sessions.
type SessionQueryFunc func() map[string][]conversation.SessionInfo

// SessionCloseFunc closes an idle session by session ID.
type SessionCloseFunc func(sessionID string)

// defaultGCConfig returns a GCConfig with sensible defaults.
func defaultGCConfig() GCConfig {
	return GCConfig{
		Interval:               30 * time.Second,
		GracePeriod:            60 * time.Second,
		ObserverGracePeriod:    60 * time.Second,
		IdleTimeout:            5 * time.Minute,
		ChildIdleTimeout:       5 * time.Minute,
		MaxClosuresPerCycle:    3,
		AuxIdleTimeout:         10 * time.Minute,
		LoopSuspendThreshold:   30 * time.Minute,
		LoopSuspendGracePeriod: 10 * time.Minute,
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
	if config.ObserverGracePeriod <= 0 {
		config.ObserverGracePeriod = defaultGCConfig().ObserverGracePeriod
	}
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = defaultGCConfig().IdleTimeout
	}
	if config.AuxIdleTimeout <= 0 {
		config.AuxIdleTimeout = defaultGCConfig().AuxIdleTimeout
	}
	if config.ChildIdleTimeout <= 0 {
		config.ChildIdleTimeout = defaultGCConfig().ChildIdleTimeout
	}
	// LoopSuspendThreshold: 0 means "not set" → use default.
	// Negative means "explicitly disabled" → set to 0 so RunGCOnce skips the heuristic.
	if config.LoopSuspendThreshold == 0 {
		config.LoopSuspendThreshold = defaultGCConfig().LoopSuspendThreshold
	} else if config.LoopSuspendThreshold < 0 {
		config.LoopSuspendThreshold = 0
	}
	// LoopSuspendGracePeriod: 0 means "not set" → use generous default.
	// Negative means "explicitly disabled" → set to 0 so RunGCOnce skips the grace check.
	if config.LoopSuspendGracePeriod == 0 {
		config.LoopSuspendGracePeriod = defaultGCConfig().LoopSuspendGracePeriod
	} else if config.LoopSuspendGracePeriod < 0 {
		config.LoopSuspendGracePeriod = 0
	}
	// Note: MaxClosuresPerCycle == 0 means unlimited — no default applied.

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

// UpdateLoopSuspendThreshold updates the loop suspend threshold on the
// running GC. This is safe to call while the GC is running. A threshold of 0
// disables the loop suspend heuristic.
func (m *ACPProcessManager) UpdateLoopSuspendThreshold(d time.Duration) {
	m.gcMu.Lock()
	defer m.gcMu.Unlock()
	m.gcConfig.LoopSuspendThreshold = d
	if m.logger != nil {
		m.logger.Info("GC: updated loop suspend threshold", "threshold", d)
	}
}

// UpdateMemoryRecycleThreshold updates the memory recycle threshold on the
// running GC. This is safe to call while the GC is running. A threshold of 0
// disables the memory-recycle tier.
func (m *ACPProcessManager) UpdateMemoryRecycleThreshold(bytes uint64) {
	m.gcMu.Lock()
	defer m.gcMu.Unlock()
	m.gcConfig.MemoryRecycleThreshold = bytes
	if m.logger != nil {
		m.logger.Info("GC: updated memory recycle threshold", "threshold_bytes", bytes)
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
// prompt, an empty queue, and no loop prompt due within 2× the GC interval.
//
// Loop suspend heuristic: loop sessions whose next prompt is farther
// away than LoopSuspendThreshold (default 30m) are eligible for suspension
// even when they have active WebSocket observers. The session is NOT archived —
// it stays visible and resumes transparently via ensure_resumed (user focus)
// or LoopRunner (when the prompt is due). This saves memory by stopping
// MCP server processes for idle loop conversations. A generous
// LoopSuspendGracePeriod (default 10m) protects sessions that recently
// finished a turn from being suspended too aggressively.
//
// Tier 2 stops shared ACP processes that have had no active sessions for longer
// than the configured grace period.
//
// Tier 4 recycles memory-bloated idle processes: when a shared process's RSS
// (summed over its process tree) exceeds MemoryRecycleThreshold and the process
// is fully idle (no in-flight RPCs, no prompting session, empty queues, no
// loop prompt due soon), its sessions are GC-suspended and closed and the
// process is stopped to reclaim memory. Disabled when MemoryRecycleThreshold is 0.
//
// Tier 5 proactively recycles degraded (saturated) idle processes (mitto-tfb): when
// a shared process has been flagged saturated by the mitto-13ck.2 infra (repeated
// NewSession/LoadSession deadlines) and is fully idle (same gates as Tier 4), its
// sessions are GC-suspended and closed and the process is stopped so the next
// NewSession lazily builds a fresh, healthy process — instead of the degraded one
// continuing to starve resumes/loop-prompts.
//
// Tier 6 escalates Tier 5 to recycle a CONFIRMED-degraded process (saturationLevel
// >= confirmedDegradedLevel) even while it is busy (mitto-1h0): a control-plane
// wedge shows up as in-flight-but-timing-out RPCs, which Tier 5's idle gate reads
// as legitimate work and refuses to recycle. Tier 6 drops the ActiveRPCs()/
// IsPrompting gate for confirmed-degraded processes, but is guarded by a
// no-streamed-progress check (LastStreamActivityAt) so a legitimately slow but
// progressing prompt is never killed.
//
// Tier 3 cleans up auxiliary sessions that have been idle longer than AuxIdleTimeout.
// Cleaned-up sessions are lazily re-created on next use via getOrCreateAuxiliarySession.
func (m *ACPProcessManager) RunGCOnce() {
	if m.sessionQuery == nil || m.sessionClose == nil {
		return
	}

	now := time.Now()

	// ----------------------------------------------------------------
	// Tier 1: close idle sessions
	// ----------------------------------------------------------------
	sessionsByWorkspace := m.sessionQuery()

	closedCount := 0
gcTier1:
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
			// Skip sessions that were recently resumed — they may not have
			// observers registered yet (async resume + load_events race).
			// Give them at least one full GC interval to establish observers.
			if !s.ResumedAt.IsZero() && now.Sub(s.ResumedAt) < m.gcConfig.Interval {
				if m.logger != nil {
					m.logger.Debug("GC: skipping session (recently resumed)",
						"session_id", s.SessionID,
						"workspace_uuid", workspaceUUID,
						"resumed_ago", now.Sub(s.ResumedAt))
				}
				continue
			}
			// Skip pinned keepalive sessions: the adaptive pre-warming path pins a
			// warm session for slow/broken workspaces so GC does not reap it before
			// the first real prompt. An expired PinExpiry (non-nil and in the past)
			// falls through so the max-pin-duration cap self-heals a stuck pin.
			if s.Pinned && (s.PinExpiry == nil || now.Before(*s.PinExpiry)) {
				if m.logger != nil {
					m.logger.Debug("GC: skipping session (pinned)",
						"session_id", s.SessionID,
						"workspace_uuid", workspaceUUID,
						"pin_reason", s.PinReason)
				}
				continue
			}

			// Determine if this is a loop session eligible for suspension.
			// A loop session qualifies when:
			//   1. It has a NextLoopAt set (i.e., has an enabled loop prompt)
			//   2. The next prompt is farther away than LoopSuspendThreshold
			//   3. The session is not actively prompting (checked above)
			//   4. The queue is empty (checked below)
			// When eligible, we bypass the observer, connected-client, and idle-timeout
			// checks — the session is suspended even if the user has it open in the
			// sidebar. The user will see it transition to "not running" and it resumes
			// instantly via ensure_resumed when they focus it.
			loopSuspendEligible := false
			if m.gcConfig.LoopSuspendThreshold > 0 && s.NextLoopAt != nil {
				suspendThreshold := now.Add(m.gcConfig.LoopSuspendThreshold)
				if s.NextLoopAt.After(suspendThreshold) {
					loopSuspendEligible = true
				}
			}

			// Generous post-activity grace: never suspend a loop session that
			// recently finished a turn. The agent may be about to continue (a queued
			// follow-up, a nudge, or the user inspecting results). We use the most
			// recent of LastResponseCompleteAt (turn END) and LastActivityAt (prompt
			// START / observer change); the former is the reliable signal here because
			// LastActivityAt is stale by the end of a long-running task.
			if loopSuspendEligible && m.gcConfig.LoopSuspendGracePeriod > 0 {
				recentActivity := s.LastActivityAt
				if s.LastResponseCompleteAt.After(recentActivity) {
					recentActivity = s.LastResponseCompleteAt
				}
				if !recentActivity.IsZero() && now.Sub(recentActivity) < m.gcConfig.LoopSuspendGracePeriod {
					if m.logger != nil {
						m.logger.Debug("GC: skipping loop suspend (recently active, within grace)",
							"session_id", s.SessionID,
							"workspace_uuid", workspaceUUID,
							"active_ago", now.Sub(recentActivity),
							"grace", m.gcConfig.LoopSuspendGracePeriod)
					}
					continue
				}
			}

			if !loopSuspendEligible {
				// Standard idle-session checks (apply only to non-suspend-eligible sessions).
				if s.HasObservers {
					if m.logger != nil {
						m.logger.Debug("GC: skipping session (has observers)",
							"session_id", s.SessionID,
							"workspace_uuid", workspaceUUID)
					}
					continue
				}
				// Skip sessions where observers recently disconnected — they may be
				// in the middle of a reconnect (e.g., macOS app staggered reconnect).
				if !s.LastObserverRemovedAt.IsZero() && now.Sub(s.LastObserverRemovedAt) < m.gcConfig.ObserverGracePeriod {
					if m.logger != nil {
						m.logger.Debug("GC: skipping session (observers recently disconnected)",
							"session_id", s.SessionID,
							"workspace_uuid", workspaceUUID,
							"observer_removed_ago", now.Sub(s.LastObserverRemovedAt))
					}
					continue
				}
				// Skip sessions with connected WebSocket clients — they may be
				// reconnecting or haven't sent load_events yet.
				if s.HasConnectedClients {
					if m.logger != nil {
						m.logger.Debug("GC: skipping session (has connected clients)",
							"session_id", s.SessionID,
							"workspace_uuid", workspaceUUID)
					}
					continue
				}
				// Skip sessions with recent activity — keepalive, prompt, or observer
				// changes within the idle timeout window. Child sessions use ChildIdleTimeout
				// which can be set shorter than IdleTimeout for faster GC under memory pressure.
				idleTimeout := m.gcConfig.IdleTimeout
				if s.IsChild && m.gcConfig.ChildIdleTimeout > 0 {
					idleTimeout = m.gcConfig.ChildIdleTimeout
				}
				if !s.LastActivityAt.IsZero() && now.Sub(s.LastActivityAt) < idleTimeout {
					if m.logger != nil {
						m.logger.Debug("GC: skipping session (recent activity)",
							"session_id", s.SessionID,
							"workspace_uuid", workspaceUUID,
							"last_activity_ago", now.Sub(s.LastActivityAt),
							"is_child", s.IsChild)
					}
					continue
				}
			}

			// Queue and loop-due-soon checks apply to both standard and
			// loop-suspend-eligible sessions.
			if s.QueueLength > 0 {
				if m.logger != nil {
					m.logger.Debug("GC: skipping session (non-empty queue)",
						"session_id", s.SessionID,
						"workspace_uuid", workspaceUUID,
						"queue_length", s.QueueLength)
				}
				continue
			}
			if s.NextLoopAt != nil {
				threshold := now.Add(2 * m.gcConfig.Interval)
				if s.NextLoopAt.Before(threshold) {
					if m.logger != nil {
						m.logger.Debug("GC: skipping session (loop prompt due soon)",
							"session_id", s.SessionID,
							"workspace_uuid", workspaceUUID,
							"next_loop_at", s.NextLoopAt)
					}
					continue
				}
			}

			if loopSuspendEligible {
				if m.logger != nil {
					m.logger.Info("GC: suspending loop session (next run far away)",
						"session_id", s.SessionID,
						"workspace_uuid", workspaceUUID,
						"next_loop_at", s.NextLoopAt,
						"threshold", m.gcConfig.LoopSuspendThreshold)
				}
				// Mark session as GC-suspended BEFORE closing, so the WebSocket
				// auto-resume handler sees the flag and skips resume. This prevents
				// the suspend/resume thrashing loop where reconnectAllSessionsStaggered
				// immediately re-opens what the GC just closed.
				m.MarkGCSuspended(s.SessionID)
			} else {
				if m.logger != nil {
					m.logger.Info("GC: closing idle session",
						"session_id", s.SessionID,
						"workspace_uuid", workspaceUUID)
				}
			}
			m.sessionClose(s.SessionID)
			closedCount++
			if m.gcConfig.MaxClosuresPerCycle > 0 && closedCount >= m.gcConfig.MaxClosuresPerCycle {
				if m.logger != nil {
					m.logger.Info("GC: reached max closures per cycle, deferring remaining",
						"closed_count", closedCount,
						"max_per_cycle", m.gcConfig.MaxClosuresPerCycle)
				}
				break gcTier1
			}
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

		// Adaptive pre-warming pin (mitto-mw0): a pinned workspace holds a warm
		// PurposeKeepAlive auxiliary session but may have no BackgroundSessions.
		// Skip Tier 2 shutdown so the process stays warm for the first real prompt.
		// An expired pin falls through so the max-pin-duration cap self-heals.
		if m.IsPinned(workspaceUUID) {
			m.lastSessionSeen[workspaceUUID] = now
			if m.logger != nil {
				m.logger.Debug("GC: skipping process shutdown (pinned by prewarm)",
					"workspace_uuid", workspaceUUID)
			}
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

	// ----------------------------------------------------------------
	// Tier 4: recycle memory-bloated idle processes
	// Re-query sessions so newly closed sessions (Tier 1) are excluded.
	// ----------------------------------------------------------------
	if m.gcConfig.MemoryRecycleThreshold > 0 {
		sampler := m.rssSampler
		if sampler == nil {
			sampler = func(p *SharedACPProcess) (uint64, error) { return p.RSSBytes() }
		}
		breakdownSampler := m.rssBreakdownSampler
		if breakdownSampler == nil {
			breakdownSampler = func(p *SharedACPProcess) (uint64, uint64, int, error) {
				return p.RSSBytesDetailed()
			}
		}

		sessionsByWorkspace = m.sessionQuery()

		m.mu.RLock()
		recycleUUIDs := make([]string, 0, len(m.processes))
		for uuid := range m.processes {
			recycleUUIDs = append(recycleUUIDs, uuid)
		}
		m.mu.RUnlock()

		for _, workspaceUUID := range recycleUUIDs {
			p := m.GetProcess(workspaceUUID)
			if p == nil {
				continue
			}

			// Adaptive pre-warming pin (mitto-54k.7): never memory-recycle a
			// pinned workspace. A warm shared Auggie process is the whole
			// point of the pin — recycling it here would force a cold
			// MCP-server spawn on the next prompt, exactly the hang we are
			// keeping the process warm to avoid.
			if m.IsPinned(workspaceUUID) {
				if m.logger != nil {
					m.logger.Debug("GC: skipping memory recycle (pinned/warm)",
						"workspace_uuid", workspaceUUID)
				}
				continue
			}

			// Hard safety gates: only recycle a fully-idle process.
			if rpcs := p.ActiveRPCs(); rpcs > 0 {
				if m.logger != nil {
					m.logger.Debug("GC: skipping memory recycle (busy)",
						"workspace_uuid", workspaceUUID,
						"reason", "in-flight RPCs",
						"active_rpcs", rpcs)
				}
				continue
			}
			sessions := sessionsByWorkspace[workspaceUUID]
			busy := false
			for _, s := range sessions {
				if s.IsPrompting {
					if m.logger != nil {
						m.logger.Debug("GC: skipping memory recycle (busy)",
							"workspace_uuid", workspaceUUID,
							"reason", "session prompting",
							"session_id", s.SessionID)
					}
					busy = true
					break
				}
				if s.QueueLength > 0 {
					if m.logger != nil {
						m.logger.Debug("GC: skipping memory recycle (busy)",
							"workspace_uuid", workspaceUUID,
							"reason", "non-empty queue",
							"session_id", s.SessionID,
							"queue_length", s.QueueLength)
					}
					busy = true
					break
				}
				if s.NextLoopAt != nil && s.NextLoopAt.Before(now.Add(2*m.gcConfig.Interval)) {
					if m.logger != nil {
						m.logger.Debug("GC: skipping memory recycle (busy)",
							"workspace_uuid", workspaceUUID,
							"reason", "loop prompt due soon",
							"session_id", s.SessionID,
							"next_loop_at", s.NextLoopAt)
					}
					busy = true
					break
				}
			}
			if busy {
				continue
			}

			// Sample memory: only recycle when over the configured threshold.
			rss, err := sampler(p)
			if err != nil {
				if m.logger != nil {
					m.logger.Debug("GC: skipping memory recycle (RSS sample failed)",
						"workspace_uuid", workspaceUUID,
						"error", err)
				}
				continue
			}
			// Sample the breakdown so both log lines can distinguish agent-side
			// (parent) growth from MCP-child (descendant) growth (mitto-3gu). A
			// breakdown-sampler error is non-fatal: log with zero fields so the
			// recycle decision still uses the tree total from `sampler`.
			parentRSS, descendantRSS, descendantCount, breakdownErr := breakdownSampler(p)
			if breakdownErr != nil {
				parentRSS, descendantRSS, descendantCount = 0, 0, 0
			}
			if rss <= m.gcConfig.MemoryRecycleThreshold {
				if m.logger != nil {
					m.logger.Debug("GC: memory recycle below threshold",
						"workspace_uuid", workspaceUUID,
						"rss_bytes", rss,
						"threshold_bytes", m.gcConfig.MemoryRecycleThreshold,
						"parent_rss_bytes", parentRSS,
						"descendant_rss_bytes", descendantRSS,
						"descendant_count", descendantCount)
				}
				continue
			}

			// Over threshold and idle — recycle.
			if m.logger != nil {
				m.logger.Info("GC: recycling memory-bloated idle shared ACP process",
					"workspace_uuid", workspaceUUID,
					"rss_bytes", rss,
					"threshold_bytes", m.gcConfig.MemoryRecycleThreshold,
					"session_count", len(sessions),
					"parent_rss_bytes", parentRSS,
					"descendant_rss_bytes", descendantRSS,
					"descendant_count", descendantCount)
			}
			// Mark each session GC-suspended BEFORE closing so the WebSocket
			// auto-resume handler skips resume and avoids a thrash loop — same
			// ordering as Tier 1's loop-suspend path.
			recycledCount := len(sessions)
			for _, s := range sessions {
				m.MarkGCSuspended(s.SessionID)
				m.sessionClose(s.SessionID)
			}
			// Stop the now-sessionless process to reclaim memory.
			m.StopProcess(workspaceUUID)
			// Keep sessionless bookkeeping consistent.
			m.gcMu.Lock()
			delete(m.lastSessionSeen, workspaceUUID)
			m.gcMu.Unlock()
			// Notify clients so they can surface a toast. Affected conversations
			// resume transparently on next focus.
			if m.onMemoryRecycled != nil {
				m.onMemoryRecycled(workspaceUUID, rss, m.gcConfig.MemoryRecycleThreshold, recycledCount)
			}
		}
	}

	// ----------------------------------------------------------------
	// Tier 5: proactively recycle degraded (saturated) idle processes (mitto-tfb)
	// The saturation infra (mitto-13ck.2) only fails fast on new requests; it never
	// recycles the degraded process, so a shared process that has starved one resume
	// keeps starving the next resume/loop-prompt. Convert repeated NewSession/
	// LoadSession deadlines into a fresh process: when a process is flagged saturated
	// AND fully idle (same hard safety gates as Tier 4), close its sessions and stop
	// it so the next NewSession lazily builds a healthy replacement. Re-query sessions
	// so any closed by earlier tiers are excluded.
	// ----------------------------------------------------------------
	{
		sessionsByWorkspace = m.sessionQuery()

		m.mu.RLock()
		healthUUIDs := make([]string, 0, len(m.processes))
		for uuid := range m.processes {
			healthUUIDs = append(healthUUIDs, uuid)
		}
		m.mu.RUnlock()

		for _, workspaceUUID := range healthUUIDs {
			p := m.GetProcess(workspaceUUID)
			if p == nil {
				continue
			}

			// Only act on degraded processes.
			if !p.IsSaturated() {
				continue
			}

			// Hard safety gates: only recycle a fully-idle process (same as Tier 4).
			if rpcs := p.ActiveRPCs(); rpcs > 0 {
				if m.logger != nil {
					m.logger.Debug("GC: skipping health recycle (busy)",
						"workspace_uuid", workspaceUUID,
						"reason", "in-flight RPCs",
						"active_rpcs", rpcs)
				}
				continue
			}
			sessions := sessionsByWorkspace[workspaceUUID]
			busy := false
			for _, s := range sessions {
				if s.IsPrompting {
					if m.logger != nil {
						m.logger.Debug("GC: skipping health recycle (busy)",
							"workspace_uuid", workspaceUUID,
							"reason", "session prompting",
							"session_id", s.SessionID)
					}
					busy = true
					break
				}
				if s.QueueLength > 0 {
					if m.logger != nil {
						m.logger.Debug("GC: skipping health recycle (busy)",
							"workspace_uuid", workspaceUUID,
							"reason", "non-empty queue",
							"session_id", s.SessionID,
							"queue_length", s.QueueLength)
					}
					busy = true
					break
				}
				if s.NextLoopAt != nil && s.NextLoopAt.Before(now.Add(2*m.gcConfig.Interval)) {
					if m.logger != nil {
						m.logger.Debug("GC: skipping health recycle (busy)",
							"workspace_uuid", workspaceUUID,
							"reason", "loop prompt due soon",
							"session_id", s.SessionID,
							"next_loop_at", s.NextLoopAt)
					}
					busy = true
					break
				}
			}
			if busy {
				continue
			}

			// Saturated and idle — recycle to reclaim a healthy process.
			if m.logger != nil {
				m.logger.Info("GC: recycling saturated idle shared ACP process",
					"workspace_uuid", workspaceUUID,
					"session_count", len(sessions))
			}
			// Mark each session GC-suspended BEFORE closing so the WebSocket
			// auto-resume handler skips resume and avoids a thrash loop — same
			// ordering as Tier 1's loop-suspend and Tier 4's memory-recycle paths.
			for _, s := range sessions {
				m.MarkGCSuspended(s.SessionID)
				m.sessionClose(s.SessionID)
			}
			// Stop the now-sessionless process; the next NewSession lazily builds a
			// fresh one with zeroed saturation state.
			m.StopProcess(workspaceUUID)
			// Keep sessionless bookkeeping consistent.
			m.gcMu.Lock()
			delete(m.lastSessionSeen, workspaceUUID)
			m.gcMu.Unlock()
			// mitto-clc: proactive re-warm after recycle was inactivated — a
			// naturally cold next NewSession is preferable to piling load onto
			// an already-degraded workspace.
		}
	}

	// ----------------------------------------------------------------
	// Tier 6: recycle CONFIRMED-degraded processes even while busy (mitto-1h0)
	// Re-query sessions so any closed by earlier tiers are excluded.
	// ----------------------------------------------------------------
	{
		sessionsByWorkspace = m.sessionQuery()

		m.mu.RLock()
		degradedUUIDs := make([]string, 0, len(m.processes))
		for uuid := range m.processes {
			degradedUUIDs = append(degradedUUIDs, uuid)
		}
		m.mu.RUnlock()

		for _, workspaceUUID := range degradedUUIDs {
			p := m.GetProcess(workspaceUUID)
			if p == nil {
				continue
			}

			// Only act on confirmed-degraded processes (saturationLevel >= 2):
			// they have already tripped saturation, served a cooldown, and their
			// post-cooldown probe ALSO timed out — demonstrably not self-healing.
			if !p.IsConfirmedDegraded() {
				continue
			}

			sessions := sessionsByWorkspace[workspaceUUID]

			// Just-resumed grace: never recycle a session that was resumed within
			// the last GC interval (same as Tier 1/5's grace window).
			skip := false
			for _, s := range sessions {
				if !s.ResumedAt.IsZero() && now.Sub(s.ResumedAt) < m.gcConfig.Interval {
					if m.logger != nil {
						m.logger.Debug("GC: skipping confirmed-degraded recycle (recently resumed)",
							"workspace_uuid", workspaceUUID,
							"session_id", s.SessionID,
							"resumed_ago", now.Sub(s.ResumedAt))
					}
					skip = true
					break
				}
				if s.NextLoopAt != nil && s.NextLoopAt.Before(now.Add(2*m.gcConfig.Interval)) {
					if m.logger != nil {
						m.logger.Debug("GC: skipping confirmed-degraded recycle (loop prompt due soon)",
							"workspace_uuid", workspaceUUID,
							"session_id", s.SessionID,
							"next_loop_at", s.NextLoopAt)
					}
					skip = true
					break
				}
			}
			if skip {
				continue
			}

			// No-streamed-progress guard (mandatory anti-regression): if any session
			// has streamed activity within the quiet window, it is legitimately
			// progressing — do NOT kill it, regardless of in-flight RPCs or prompting
			// state. Note: unlike Tier 5, we deliberately do NOT gate on
			// ActiveRPCs()/IsPrompting here — the in-flight, timing-out control RPCs
			// (session/new, set_model) ARE the wedge, not real work.
			progressing := false
			for _, s := range sessions {
				if !s.LastStreamActivityAt.IsZero() && now.Sub(s.LastStreamActivityAt) < tier6StreamedProgressQuietWindow {
					if m.logger != nil {
						m.logger.Debug("GC: skipping confirmed-degraded recycle (session progressing)",
							"workspace_uuid", workspaceUUID,
							"session_id", s.SessionID,
							"stream_idle", now.Sub(s.LastStreamActivityAt))
					}
					progressing = true
					break
				}
			}
			if progressing {
				continue
			}

			// Confirmed-degraded, not progressing — recycle even though busy.
			if m.logger != nil {
				m.logger.Info("GC: recycling confirmed-degraded busy shared ACP process",
					"workspace_uuid", workspaceUUID,
					"saturation_level", p.SaturationLevel(),
					"active_rpcs", p.ActiveRPCs(),
					"session_count", len(sessions))
			}
			for _, s := range sessions {
				m.MarkGCSuspended(s.SessionID)
				m.sessionClose(s.SessionID)
			}
			m.StopProcess(workspaceUUID)
			m.gcMu.Lock()
			delete(m.lastSessionSeen, workspaceUUID)
			m.gcMu.Unlock()
			// mitto-clc: proactive re-warm after recycle was inactivated.
		}
	}

	// ----------------------------------------------------------------
	// Tier 3: clean up idle auxiliary sessions
	// ----------------------------------------------------------------
	m.CleanupStaleAuxiliarySessions(m.gcConfig.AuxIdleTimeout)
}
