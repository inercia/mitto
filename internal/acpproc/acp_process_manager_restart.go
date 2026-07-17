package acpproc

import (
	"time"

	mittoAcp "github.com/inercia/mitto/internal/acp"
)

// RecordGlobalRestart records a restart attempt in the global rate limiter.
// Called by SharedACPProcess.Restart() via the RecordRestart callback.
func (m *ACPProcessManager) RecordGlobalRestart() {
	m.globalRestartMu.Lock()
	defer m.globalRestartMu.Unlock()
	m.globalRestartTimes = append(m.globalRestartTimes, time.Now())
}

// CanRestartGlobally checks if a restart is allowed by the global rate limiter.
// Returns true if restart is allowed, false if we're in a global cooldown.
func (m *ACPProcessManager) CanRestartGlobally() bool {
	m.globalRestartMu.Lock()
	defer m.globalRestartMu.Unlock()

	now := time.Now()

	// Check if we're in cooldown
	if now.Before(m.globalCooldownUntil) {
		if m.logger != nil {
			m.logger.Warn("Global restart cooldown active, blocking restart",
				"cooldown_remaining", m.globalCooldownUntil.Sub(now).Round(time.Second))
		}
		return false
	}

	// Clean old entries outside the window
	cutoff := now.Add(-mittoAcp.GlobalRestartWindow)
	valid := m.globalRestartTimes[:0]
	for _, t := range m.globalRestartTimes {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	m.globalRestartTimes = valid

	// Check if limit exceeded
	if len(m.globalRestartTimes) >= mittoAcp.MaxGlobalRestarts {
		// Enter cooldown
		m.globalCooldownUntil = now.Add(mittoAcp.GlobalCooldownDuration)
		if m.logger != nil {
			m.logger.Warn("Global restart limit exceeded, entering cooldown",
				"recent_restarts", len(m.globalRestartTimes),
				"max_global_restarts", mittoAcp.MaxGlobalRestarts,
				"cooldown_duration", mittoAcp.GlobalCooldownDuration)
		}
		return false
	}

	return true
}
