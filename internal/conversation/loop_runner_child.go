package conversation

import (
	"errors"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// OnChildEndResponse notifies the onChild loop leg that a child conversation
// (sessionID childID) has finished an agent response and gone idle. It is
// invoked from the SessionManager idle bridge (mitto-987y.5) alongside the
// existing onCompletion leg (LoopRunner.OnConversationIdle). It resolves the
// child's ParentSessionID from its own metadata (unlike OnChildDeleted, the
// child's metadata still exists at this point) and, if the child does have a
// parent, forwards to fireOnChild.
func (r *LoopRunner) OnChildEndResponse(childID string) {
	if r.store == nil {
		return
	}
	meta, err := r.store.GetMetadata(childID)
	if err != nil {
		return
	}
	if meta.ParentSessionID == "" {
		// Not a child conversation (or a top-level session) — nothing to fire.
		return
	}
	r.fireOnChild(meta.ParentSessionID, session.ChildEventAnyEndResponse, childID)
}

// OnChildDeleted notifies the onChild loop leg that a child conversation
// (childID) has been deleted. Unlike OnChildEndResponse, parentID must be
// supplied by the caller (the session.Store delete observer, mitto-987y.3):
// by the time the observer fires, the child's own metadata has already been
// removed, so it cannot be resolved here. A cascade delete that also removes
// the parent itself is handled by fireOnChild's parent-lookup guard (silent
// no-op), matching the "parent-deleted cascade" design decision on mitto-987y.
func (r *LoopRunner) OnChildDeleted(childID, parentID string) {
	if parentID == "" {
		return
	}
	r.fireOnChild(parentID, session.ChildEventAnyDeleted, childID)
}

// fireOnChild applies the onChild trigger's guards, in order, then dispatches
// a run of the parent conversation's loop via TriggerNowFrom. Every drop
// path is logged at Debug (never Warn/Error) — dropped fires are the expected
// steady state (loop not armed for onChild, parent busy elsewhere, cooldown,
// or a losing coalesce), not failures.
//
// Precedence within a single tick (onTasks > onChild > onCompletion >
// schedule, per mitto-987y) is realised entirely by the existing single-slot
// claimDispatch inside TriggerNowFrom/triggerNowFull: onChild fires
// synchronously off the child-lifecycle event, ahead of the onCompletion
// timer (floored at DefaultMinLoopCompletionDelaySeconds) and the schedule
// poll tick, so it naturally wins against those two but loses to an
// already-in-flight onTasks dispatch for the same parent session. No
// additional ordering machinery is introduced here.
func (r *LoopRunner) fireOnChild(parentID string, event session.ChildEvent, childID string) {
	if r.store == nil {
		return
	}
	// Belt-and-suspenders shutdown guard, mirroring OnBeadsChanged's mitto-cbx
	// guard: a delete notification (fired from the store's observer, outside
	// any LoopRunner lock) can race Stop().
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return
	}

	meta, err := r.store.GetMetadata(parentID)
	if err != nil || meta.Archived {
		// Covers both "parent metadata missing" (e.g. a cascade delete that
		// also removed the parent) and "parent archived" (archiving stops the
		// loop, mitto-efnb) — both are silent no-ops.
		if r.logger != nil {
			r.logger.Debug("onChild: parent missing or archived, dropping",
				"parent_id", parentID, "child_id", childID, "event", string(event))
		}
		return
	}

	loopStore := r.store.Loop(parentID)
	loop, err := loopStore.Get()
	if err != nil || loop == nil || !loop.Enabled || !loop.IsOnChild() || !loop.HasChildEvent(event) {
		if r.logger != nil {
			r.logger.Debug("onChild: not armed for this event, dropping",
				"parent_id", parentID, "child_id", childID, "event", string(event))
		}
		return
	}

	if r.autoStopIfMaxDurationReached(parentID, loop, loopStore, time.Now()) {
		return
	}

	if r.eventCooldownActive(loop) {
		if r.logger != nil {
			r.logger.Debug("onChild: cooldown active, dropping",
				"parent_id", parentID, "child_id", childID, "event", string(event))
		}
		return
	}

	err = r.TriggerNowFrom(parentID, true, session.TriggerOnChild)
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, ErrLoopDispatchCoalesced), errors.Is(err, ErrSessionBusy):
		// Expected, non-noisy outcomes: another trigger already owns the
		// in-flight dispatch, or the parent is mid-turn. Not an error.
		if r.logger != nil {
			r.logger.Debug("onChild: fire coalesced or session busy",
				"parent_id", parentID, "child_id", childID, "event", string(event), "error", err)
		}
	case errors.Is(err, ErrPromptResolveFailed):
		// Route through the shared 3-strike auto-pause path for parity with
		// the onTasks and scheduled fire paths (mitto-uhnc).
		r.handlePromptResolveFailure(parentID, meta.Name, loop, loopStore, err)
	default:
		if r.logger != nil {
			r.logger.Warn("onChild: fire failed",
				"parent_id", parentID, "child_id", childID, "event", string(event), "error", err)
		}
	}
}
