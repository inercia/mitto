package conversation

// MoveSessionToAgent (bead mitto-f7yo.1) rebinds an existing conversation to a
// different ACP agent configured for the same folder, while keeping the
// conversation active, its persisted history visible, and its loop config
// (loop.json) untouched.
//
// This is the backend core every entry point (REST — mitto-f7yo.2, MCP —
// mitto-f7yo.3) will call. It deliberately does NOT implement model mapping
// (mitto-f7yo.4) or richer context handoff (mitto-f7yo.5) — those are
// separate beads; this function only leaves clean seams for them
// (MoveAgentResult.PreviousBaselineModel).

import (
	"errors"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// Sentinel preflight errors for MoveSessionToAgent. Callers (e.g. the REST
// handler in mitto-f7yo.2) can map these to HTTP status codes with
// errors.Is: ErrMoveAgentUnknownTarget/ErrMoveAgentNoWorkspace/
// ErrMoveAgentSameAgent → 400, session.ErrSessionNotFound → 404,
// ErrMoveAgentArchived/ErrMoveAgentBusy → 409.
var (
	// ErrMoveAgentArchived is returned when the conversation is archived.
	// Archived conversations must be unarchived before they can be moved.
	ErrMoveAgentArchived = errors.New("conversation is archived")
	// ErrMoveAgentSameAgent is returned when targetAgent equals the
	// conversation's current ACP server.
	ErrMoveAgentSameAgent = errors.New("target agent is the same as the current agent")
	// ErrMoveAgentUnknownTarget is returned when targetAgent is not a
	// configured ACP server.
	ErrMoveAgentUnknownTarget = errors.New("target agent is not configured")
	// ErrMoveAgentNoWorkspace is returned when there is no workspace
	// registered for (conversation's working dir, targetAgent) — i.e. the
	// target agent is not set up for this folder.
	ErrMoveAgentNoWorkspace = errors.New("target agent is not configured for this conversation's folder")
	// ErrMoveAgentBusy is returned when the conversation has a turn
	// streaming or a loop run in flight (waiting on children). Callers
	// should retry once the conversation goes idle.
	ErrMoveAgentBusy = errors.New("conversation is busy (a turn is streaming or a loop run is in flight)")
)

// DefaultMoveAgentCloseTimeout bounds how long MoveSessionToAgent waits for an
// in-flight response to finish before force-closing the old agent's
// BackgroundSession. It is a package variable (not a const) so tests can
// shorten it to keep unreachable-old-agent scenarios fast; production callers
// normally leave MoveAgentOptions.CloseTimeout unset (zero) and get this
// default.
var DefaultMoveAgentCloseTimeout = 30 * time.Second

// MoveAgentOptions configures MoveSessionToAgent.
type MoveAgentOptions struct {
	// IncludeChildren also moves every non-archived, non-busy descendant
	// conversation (recursively, via store.FindAllChildrenRecursive) that is
	// still bound to the OLD agent onto the target agent. Descendants bound
	// to a different agent, archived, or busy are left untouched and
	// reported in MoveAgentResult.Skipped.
	IncludeChildren bool
	// CloseTimeout bounds how long to wait for an in-flight response on the
	// old agent to complete before force-closing. Zero means
	// DefaultMoveAgentCloseTimeout.
	CloseTimeout time.Duration
}

// MoveAgentSkip records a descendant conversation that IncludeChildren
// declined to move, and why.
type MoveAgentSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// MoveAgentResult reports the outcome of MoveSessionToAgent.
type MoveAgentResult struct {
	// Moved lists every session ID actually rebound to the target agent:
	// the requested sessionID first, followed by any moved descendants
	// (only populated when MoveAgentOptions.IncludeChildren is set).
	Moved []string `json:"moved"`
	// Skipped lists descendant conversations IncludeChildren declined to
	// move (busy, archived, or bound to a different agent), with a reason.
	Skipped []MoveAgentSkip `json:"skipped,omitempty"`
	// PreviousAgent is the ACP server sessionID was bound to before the move.
	PreviousAgent string `json:"previous_agent"`
	// PreviousBaselineModel is sessionID's BaselineModel value before it was
	// cleared by the move. It is a seam for mitto-f7yo.4 (map baseline model
	// to the closest equivalent on the target agent) — this bead does not
	// use it for anything beyond reporting it.
	PreviousBaselineModel string `json:"previous_baseline_model,omitempty"`
	// ResumeError is set when metadata was rewritten successfully (the move
	// stands) but resuming sessionID on the target agent failed — e.g. the
	// new agent's process failed to start. We intentionally do not roll back
	// the metadata rewrite in this case (see MoveSessionToAgent doc comment
	// for the rationale): the conversation will simply attempt to resume
	// normally, like any other idle persisted conversation, the next time it
	// is accessed (next prompt, sidebar open, loop tick, ...).
	ResumeError error `json:"-"`
}

// MoveSessionToAgent rebinds sessionID from its current ACP agent to
// targetAgent, keeping it active. High-level steps:
//
//  1. Preflight validates the request (not found / archived / same agent /
//     unknown target / no workspace for the folder / busy) and returns a
//     typed sentinel error on failure without touching anything.
//  2. The live BackgroundSession (if any) is stopped with a bounded timeout
//     so an unreachable old agent cannot hang the move.
//  3. Metadata is rewritten in one store.UpdateMetadata call: ACPServer is
//     set to targetAgent; ACPSessionID, CurrentModeID and
//     ACPStartFailureCount (all agent-specific) are cleared; BaselineModel
//     is captured into the result and cleared.
//  4. A "session_change" (kind "agent") event is recorded so the timeline
//     shows the move.
//  5. The conversation is resumed on the new agent in the background.
//
// If the resume in step 5 fails, the move still stands — metadata has
// already been rewritten, so rolling back would silently rebind the
// conversation back to a (possibly still-unreachable) old agent behind the
// caller's back. Instead the resume error is reported on the result, and the
// conversation is left idle/persisted on the new agent: it resumes normally
// on the next access, exactly like any other idle conversation whose ACP
// process isn't currently running.
func (sm *SessionManager) MoveSessionToAgent(sessionID, targetAgent string, opts MoveAgentOptions) (MoveAgentResult, error) {
	var result MoveAgentResult

	store := sm.store
	if store == nil {
		return result, session.ErrSessionNotFound
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		return result, err
	}

	if err := sm.moveAgentPreflight(meta, targetAgent); err != nil {
		return result, err
	}

	closeTimeout := opts.CloseTimeout
	if closeTimeout <= 0 {
		closeTimeout = DefaultMoveAgentCloseTimeout
	}

	previousAgent := meta.ACPServer
	if err := sm.moveAgentStopAndRebind(sessionID, previousAgent, targetAgent, closeTimeout, &result); err != nil {
		return result, err
	}
	result.PreviousAgent = previousAgent
	result.Moved = append(result.Moved, sessionID)

	// Resume on the new agent so the next prompt goes to it, carrying recent
	// history over via the existing resumed-session injection
	// (buildPromptWithHistory). A resume failure does not roll back the
	// metadata rewrite — see the doc comment above.
	if _, resumeErr := sm.ResumeSessionBackground(sessionID, meta.Name, meta.WorkingDir); resumeErr != nil {
		result.ResumeError = resumeErr
		if sm.logger != nil {
			sm.logger.Warn("MoveSessionToAgent: resume on target agent failed; conversation remains idle on the new agent",
				"session_id", sessionID,
				"previous_agent", previousAgent,
				"target_agent", targetAgent,
				"error", resumeErr)
		}
	}

	if opts.IncludeChildren {
		sm.moveAgentChildren(sessionID, previousAgent, targetAgent, closeTimeout, &result)
	}

	return result, nil
}

// moveAgentPreflight validates a move request against meta without mutating
// anything. It mirrors the busy checks used by the archive path
// (LoopRunner.isSessionBusy: BackgroundSession.IsPrompting() OR
// SessionManager.IsWaitingForChildren, which covers a loop run currently
// dispatched/in flight for this session).
func (sm *SessionManager) moveAgentPreflight(meta session.Metadata, targetAgent string) error {
	if meta.Archived {
		return ErrMoveAgentArchived
	}
	if targetAgent == meta.ACPServer {
		return ErrMoveAgentSameAgent
	}
	_, mittoConfig := sm.GetGlobalRunnerInfo()
	if mittoConfig == nil {
		return ErrMoveAgentUnknownTarget
	}
	if _, err := mittoConfig.GetServer(targetAgent); err != nil {
		return ErrMoveAgentUnknownTarget
	}
	if sm.GetWorkspaceByDirAndACP(meta.WorkingDir, targetAgent) == nil {
		return ErrMoveAgentNoWorkspace
	}
	if sm.moveAgentIsBusy(meta.SessionID) {
		return ErrMoveAgentBusy
	}
	return nil
}

// moveAgentIsBusy reports whether sessionID has a turn currently streaming or
// a loop run in flight, using the same signals as LoopRunner.isSessionBusy.
func (sm *SessionManager) moveAgentIsBusy(sessionID string) bool {
	if bs := sm.GetSession(sessionID); bs != nil && bs.IsPrompting() {
		return true
	}
	return sm.IsWaitingForChildren(sessionID)
}

// moveAgentStopAndRebind stops the live BackgroundSession for sessionID (bounded
// by closeTimeout so an unreachable old agent cannot hang the move) and
// rewrites its persisted metadata in one store.UpdateMetadata call. It records
// the pre-move BaselineModel into result.PreviousBaselineModel.
func (sm *SessionManager) moveAgentStopAndRebind(sessionID, previousAgent, targetAgent string, closeTimeout time.Duration, result *MoveAgentResult) error {
	// Stop the live session first. CloseSessionGracefully waits (up to
	// closeTimeout) for any in-flight response, then closes; on timeout it
	// returns false without closing, so we force-close via CloseSession —
	// this bounds the whole stop step even when the old agent's process is
	// wedged/unreachable. Both paths broadcast "ACP stopped" to observers via
	// BackgroundSession.Close (reason "agent_moved" is treated like
	// "acp_server_reconfigured": Suspend() instead of recording a
	// session_end, since we resume immediately after — see Close in
	// background_session.go).
	if !sm.CloseSessionGracefully(sessionID, "agent_moved", closeTimeout) {
		sm.CloseSession(sessionID, "agent_moved_timeout")
	}

	if err := sm.store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		result.PreviousBaselineModel = m.BaselineModel
		m.ACPServer = targetAgent
		m.ACPSessionID = ""
		m.CurrentModeID = ""
		m.ACPStartFailureCount = 0
		m.BaselineModel = ""
	}); err != nil {
		return err
	}

	sm.recordMoveAgentEvent(sessionID, targetAgent, previousAgent)
	return nil
}

// recordMoveAgentEvent appends a "session_change" (kind "agent") event to
// sessionID's event log via a fresh Recorder bound to the persisted session
// (the BackgroundSession, and therefore its live recorder, was just stopped).
// Best-effort: a failure here does not fail the move (metadata is already the
// source of truth for ACPServer), it only means the timeline won't show the
// move — logged at Warn.
func (sm *SessionManager) recordMoveAgentEvent(sessionID, targetAgent, previousAgent string) {
	if sm.store == nil {
		return
	}
	recorder := session.NewRecorderWithID(sm.store, sessionID)
	if err := recorder.Resume(); err != nil {
		if sm.logger != nil {
			sm.logger.Warn("MoveSessionToAgent: failed to resume recorder to record session_change event",
				"session_id", sessionID, "error", err)
		}
		return
	}
	data := session.SessionChangeData{
		Kind:          "agent",
		Value:         targetAgent,
		PreviousValue: previousAgent,
	}
	if err := recorder.RecordSessionChange(data); err != nil && sm.logger != nil {
		sm.logger.Warn("MoveSessionToAgent: failed to record session_change event",
			"session_id", sessionID, "error", err)
	}
}

// moveAgentChildren moves every non-archived, non-busy descendant of
// sessionID that is still bound to previousAgent onto targetAgent. Used by
// MoveSessionToAgent when MoveAgentOptions.IncludeChildren is set. Descendants
// are looked up via store.FindAllChildrenRecursive so grandchildren (etc.) are
// covered, not just direct children.
func (sm *SessionManager) moveAgentChildren(sessionID, previousAgent, targetAgent string, closeTimeout time.Duration, result *MoveAgentResult) {
	descendantIDs, err := sm.store.FindAllChildrenRecursive(sessionID)
	if err != nil {
		if sm.logger != nil {
			sm.logger.Warn("MoveSessionToAgent: failed to list descendants for IncludeChildren",
				"session_id", sessionID, "error", err)
		}
		return
	}

	for _, childID := range descendantIDs {
		childMeta, err := sm.store.GetMetadata(childID)
		if err != nil {
			result.Skipped = append(result.Skipped, MoveAgentSkip{ID: childID, Reason: "metadata unavailable: " + err.Error()})
			continue
		}
		if childMeta.Archived {
			result.Skipped = append(result.Skipped, MoveAgentSkip{ID: childID, Reason: "archived"})
			continue
		}
		if childMeta.ACPServer != previousAgent {
			result.Skipped = append(result.Skipped, MoveAgentSkip{ID: childID, Reason: "bound to a different agent (" + childMeta.ACPServer + ")"})
			continue
		}
		if sm.moveAgentIsBusy(childID) {
			result.Skipped = append(result.Skipped, MoveAgentSkip{ID: childID, Reason: "busy"})
			continue
		}

		if err := sm.moveAgentStopAndRebind(childID, previousAgent, targetAgent, closeTimeout, &MoveAgentResult{}); err != nil {
			result.Skipped = append(result.Skipped, MoveAgentSkip{ID: childID, Reason: "failed to rewrite metadata: " + err.Error()})
			continue
		}
		result.Moved = append(result.Moved, childID)

		if _, resumeErr := sm.ResumeSessionBackground(childID, childMeta.Name, childMeta.WorkingDir); resumeErr != nil && sm.logger != nil {
			sm.logger.Warn("MoveSessionToAgent: resume on target agent failed for child conversation",
				"session_id", childID,
				"previous_agent", previousAgent,
				"target_agent", targetAgent,
				"error", resumeErr)
		}
	}
}
