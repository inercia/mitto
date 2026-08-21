package acpproc

import (
	"encoding/json"
	"log/slog"
	"sync/atomic"

	acp "github.com/coder/acp-go-sdk"
)

const sharedACPNotificationQueueCapacity = 8192

type loadReplaySuppression struct {
	active     int
	suppressed atomic.Uint64
}

func (p *SharedACPProcess) beginLoadReplaySuppression(sessionID acp.SessionId) {
	p.loadReplayMu.Lock()
	defer p.loadReplayMu.Unlock()
	if p.loadReplaySuppressions == nil {
		p.loadReplaySuppressions = make(map[acp.SessionId]*loadReplaySuppression)
	}
	state := p.loadReplaySuppressions[sessionID]
	if state == nil {
		state = &loadReplaySuppression{}
		p.loadReplaySuppressions[sessionID] = state
	}
	state.active++
}

func (p *SharedACPProcess) endLoadReplaySuppression(sessionID acp.SessionId) {
	p.loadReplayMu.Lock()
	state := p.loadReplaySuppressions[sessionID]
	if state == nil {
		p.loadReplayMu.Unlock()
		return
	}
	state.active--
	if state.active > 0 {
		p.loadReplayMu.Unlock()
		return
	}
	delete(p.loadReplaySuppressions, sessionID)
	count := state.suppressed.Load()
	p.loadReplayMu.Unlock()

	if count > 0 && p.logger != nil {
		p.logger.Info("Suppressed session/load replay before ACP notification queue",
			"acp_session_id", sessionID,
			"suppressed_notifications", count,
			"queue_capacity", sharedACPNotificationQueueCapacity)
	}
}

func (p *SharedACPProcess) filterLoadReplayNotification(line []byte) bool {
	p.loadReplayMu.RLock()
	hasActiveLoads := len(p.loadReplaySuppressions) > 0
	p.loadReplayMu.RUnlock()
	if !hasActiveLoads {
		return false
	}

	var envelope struct {
		Method string `json:"method"`
		Params struct {
			SessionID acp.SessionId `json:"sessionId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.Method != acp.ClientMethodSessionUpdate {
		return false
	}

	p.loadReplayMu.RLock()
	state := p.loadReplaySuppressions[envelope.Params.SessionID]
	count := uint64(0)
	if state != nil {
		count = state.suppressed.Add(1)
	}
	p.loadReplayMu.RUnlock()
	if isReplayPressureMilestone(count) {
		logReplayPressure(p.logger, envelope.Params.SessionID, count)
	}
	return state != nil
}

func isReplayPressureMilestone(count uint64) bool {
	capacity := uint64(sharedACPNotificationQueueCapacity)
	return count == capacity/2 || count == capacity*3/4 || count == capacity
}

func logReplayPressure(logger *slog.Logger, sessionID acp.SessionId, count uint64) {
	if logger == nil {
		return
	}
	logger.Warn("Session/load replay would pressure ACP notification queue",
		"acp_session_id", sessionID,
		"suppressed_notifications", count,
		"queue_capacity", sharedACPNotificationQueueCapacity)
}
