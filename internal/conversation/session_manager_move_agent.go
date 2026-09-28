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
//
// MoveSessionToAgentPreflight (below) is the read-only counterpart used by
// the REST GET .../move-agent/preflight endpoint (mitto-f7yo.2) to describe
// move affordance (candidates, busy/archived state, loop info, children
// count) without performing a move.

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

// MoveAgentCandidate describes one alternative agent a conversation could be
// moved to (returned by MoveSessionToAgentPreflight).
type MoveAgentCandidate struct {
	// Name is the ACP server name (config.ACPServer.Name / workspace ACPServer).
	Name string `json:"name"`
	// Type is the ACP server's type identifier (config.ACPServer.GetType():
	// Type if set, else Name) — servers of the same type share prompts.
	Type string `json:"type"`
	// Available reports whether the candidate is currently usable. This bead
	// only asserts "has a workspace for this folder and is present in
	// config" (always true for entries in Candidates, since unconfigured/
	// no-workspace agents are never added) — the richer four-state
	// installed/configured/connected view from mitto-lrt.9
	// (ComposeAvailability) is not yet wired into a cheap per-request lookup
	// here; see docs/devel/agent-backend-architecture.md. A future bead can
	// tighten this without changing the field's meaning (true = "safe to
	// offer as a move target").
	Available bool `json:"available"`
	// LoopPromptAvailable is left nil here: MoveSessionToAgentPreflight has
	// no access to the prompt-merge pipeline (internal/prompts,
	// PromptsCache, per-workspace prompt dirs — all wired at the web/config
	// layer, not the conversation layer). The REST handler
	// (mitto-f7yo.2, internal/web/handlers/session_move_agent.go) fills this
	// in by name-only resolution (no enabledWhen evaluation — see that
	// file's doc comment for the documented limitation) when the
	// conversation is a loop. Nil means "not applicable" (not a loop, or not
	// computed); non-nil means the target agent does/doesn't have a
	// same-named prompt available.
	LoopPromptAvailable *bool `json:"loop_prompt_available,omitempty"`
}

// MoveAgentPreflight reports move-agent affordance for a conversation without
// performing (or requiring) an actual move. It backs the REST
// GET .../move-agent/preflight endpoint (mitto-f7yo.2). Unlike
// moveAgentPreflight (the private validation gate MoveSessionToAgent itself
// uses, which returns a sentinel error), this call only errors when the
// session itself cannot be found — archived/busy/no-candidates are reported
// as plain fields so a caller (typically a UI) can render an informative
// state instead of a hard failure.
type MoveAgentPreflight struct {
	// CurrentAgent is the conversation's current ACP server.
	CurrentAgent string `json:"current_agent"`
	// WorkingDir is the conversation's working directory. Not serialized
	// (json:"-"): it exists so the REST handler can resolve
	// LoopPromptAvailable per candidate without a second store round trip;
	// it is not part of the documented preflight response shape.
	WorkingDir string `json:"-"`
	// Candidates lists every other configured ACP server that has a
	// workspace for CurrentAgent's WorkingDir (i.e. a valid move target).
	Candidates []MoveAgentCandidate `json:"candidates"`
	// Busy reports whether a turn is currently streaming or a loop run is in
	// flight (see moveAgentBusyReason) — a move would be rejected right now.
	Busy bool `json:"busy"`
	// BusyReason is "turn_streaming" or "loop_run_in_flight" when Busy, empty otherwise.
	BusyReason string `json:"busy_reason,omitempty"`
	// Archived reports whether the conversation is archived — a move would
	// be rejected right now (ErrMoveAgentArchived).
	Archived bool `json:"archived"`
	// IsLoop reports whether the conversation has a loop configured
	// (loop.json present, regardless of Enabled).
	IsLoop bool `json:"is_loop"`
	// LoopPromptName is the loop's named prompt (session.LoopPrompt.PromptName),
	// empty when the conversation has no loop or its loop uses a free-text
	// prompt instead of a named one.
	LoopPromptName string `json:"loop_prompt_name,omitempty"`
	// ChildrenCount is the number of non-archived descendant conversations
	// (recursive, via store.FindAllChildrenRecursive), in the same
	// WorkingDir, still bound to CurrentAgent — i.e. how many conversations
	// opts.IncludeChildren would attempt to move (some may still be skipped
	// at execute time if they become busy in the interim).
	ChildrenCount int `json:"children_count"`
	// BaselineModel is the conversation's current baseline model, if any.
	BaselineModel string `json:"baseline_model,omitempty"`
}

// MoveSessionToAgentPreflight computes MoveAgentPreflight for sessionID. See
// the type doc comment: this never returns ErrMoveAgent* sentinels, only
// session.ErrSessionNotFound (or a store-lookup error) when the session
// itself cannot be resolved.
func (sm *SessionManager) MoveSessionToAgentPreflight(sessionID string) (MoveAgentPreflight, error) {
	var result MoveAgentPreflight

	store := sm.store
	if store == nil {
		return result, session.ErrSessionNotFound
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		return result, err
	}

	result.CurrentAgent = meta.ACPServer
	result.WorkingDir = meta.WorkingDir
	result.Archived = meta.Archived
	result.BaselineModel = meta.BaselineModel
	result.Busy, result.BusyReason = sm.moveAgentBusyReason(sessionID)

	if loop, loopErr := store.Loop(sessionID).Get(); loopErr == nil && loop != nil {
		result.IsLoop = true
		result.LoopPromptName = loop.PromptName
	}

	if descendantIDs, listErr := store.FindAllChildrenRecursive(sessionID); listErr == nil {
		for _, childID := range descendantIDs {
			childMeta, metaErr := store.GetMetadata(childID)
			if metaErr != nil {
				continue
			}
			if !childMeta.Archived && childMeta.ACPServer == meta.ACPServer && childMeta.WorkingDir == meta.WorkingDir {
				result.ChildrenCount++
			}
		}
	} else if sm.logger != nil {
		sm.logger.Warn("MoveSessionToAgentPreflight: failed to list descendants for children_count",
			"session_id", sessionID, "error", listErr)
	}

	_, mittoConfig := sm.GetGlobalRunnerInfo()
	seen := map[string]bool{meta.ACPServer: true}
	for _, ws := range sm.GetWorkspaces() {
		if ws.WorkingDir != meta.WorkingDir || ws.ACPServer == "" || seen[ws.ACPServer] {
			continue
		}
		seen[ws.ACPServer] = true

		candidate := MoveAgentCandidate{Name: ws.ACPServer}
		if mittoConfig != nil {
			if srv, srvErr := mittoConfig.GetServer(ws.ACPServer); srvErr == nil && srv != nil {
				candidate.Type = srv.GetType()
				candidate.Available = true
			}
		}
		result.Candidates = append(result.Candidates, candidate)
	}

	return result, nil
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
	busy, _ := sm.moveAgentBusyReason(sessionID)
	return busy
}

// moveAgentBusyReason is the single source of truth behind moveAgentIsBusy
// and MoveSessionToAgentPreflight's "busy"/"busy_reason" fields: it reports
// whether sessionID has a turn currently streaming or a loop run in flight,
// and — when busy — a short machine-readable reason distinguishing the two.
func (sm *SessionManager) moveAgentBusyReason(sessionID string) (bool, string) {
	if bs := sm.GetSession(sessionID); bs != nil && bs.IsPrompting() {
		return true, "turn_streaming"
	}
	if sm.IsWaitingForChildren(sessionID) {
		return true, "loop_run_in_flight"
	}
	return false, ""
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
