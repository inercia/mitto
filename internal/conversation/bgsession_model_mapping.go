package conversation

// Model mapping after an agent move (mitto-f7yo.4).
//
// MoveSessionToAgent (mitto-f7yo.1, session_manager_move_agent.go) clears a
// moved conversation's BaselineModel — model IDs are agent-specific and
// cannot be carried over verbatim — and, in the SAME store.UpdateMetadata
// call, stashes the pre-move value into session.Metadata.PendingModelMappingFrom
// so this hook can resolve a comparable model on the new agent once its
// catalog becomes known. Persisted (not held only in memory) because the
// resume can happen asynchronously, or after a full process restart.

import (
	"context"
	"strings"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// resolveClosestModel picks the closest model to pendingFrom (the previous
// agent's baseline model ID) among models' currently available catalog.
//
// Deliberately built directly on constraints.go's raw string-matching engine
// (MatchConstraintOption / config.ConstraintMatchesName / ACPServerConstraint),
// NOT SelectPreferredModel/config.PromptPreferredModel: the latter resolves a
// *named global Model profile* (Config.Models, matched by exact profile Name
// or Tag) — the wrong tool here, since pendingFrom is an opaque agent-specific
// model id with no relationship to any configured profile name. Match order:
//  1. Exact, case-insensitive equality against a candidate's raw model id.
//  2. Exact match (via the shared "exact" MatchMode) against a candidate's
//     display name.
//  3. lookAlike token match (constraints.go's bounded-token algorithm)
//     against a candidate's display name, using a hyphen/dot/underscore-
//     normalized copy of pendingFrom as the pattern.
//
// Normalization for (3): lookAlike's MatchMode splits its Pattern into
// tokens on WHITESPACE only (strings.Fields) — it is designed for
// human-authored, space-separated patterns such as a ModelProfile's
// "Claude Sonnet 4.5" (see config.ModelProfile's own doc example, "Opus
// 4.6"). A raw model id such as "claude-sonnet-4.5" is a single hyphen/dot
// joined blob under that splitting rule and would never tokenize into
// ["claude","sonnet","4.5"] on its own. To let a previous id like
// "claude-sonnet-4-5" still lookAlike-match a target displayed as "Claude
// Sonnet 4.5" (or vice versa), hyphens/dots/underscores/slashes in
// pendingFrom are first replaced with spaces, e.g. "claude sonnet 4 5" —
// each resulting token is then checked as an independently bounded
// substring of the candidate's name, exactly like a hand-authored pattern.
//
// Returns "" when nothing matches at any level, signalling the caller to
// leave the agent default in place without treating it as an error.
func resolveClosestModel(pendingFrom string, models *SessionModelState) string {
	if pendingFrom == "" || models == nil || len(models.AvailableModels) == 0 {
		return ""
	}
	options := ModelsToConfigOptions(models)

	for _, opt := range options {
		if strings.EqualFold(opt.Value, pendingFrom) {
			return opt.Value
		}
	}
	if matched := MatchConstraintOption(&config.ACPServerConstraint{MatchMode: "exact", Pattern: pendingFrom}, options); matched != "" {
		return matched
	}
	lookAlikePattern := strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.', '/':
			return ' '
		default:
			return r
		}
	}, pendingFrom)
	if matched := MatchConstraintOption(&config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: lookAlikePattern}, options); matched != "" {
		return matched
	}
	return ""
}

// cbApplyPendingModelMapping consumes bs's persisted PendingModelMappingFrom
// (set by MoveSessionToAgent) exactly once — the first time this
// BackgroundSession sees the target agent's model catalog after a move —
// resolving the closest available model and applying it through the
// persistent SetConfigOption path (applyConfigOption -> cmRecordSessionChange)
// so the new baseline is recorded as a session_change event and the UI
// selector updates via the existing config_option_changed broadcast. Do NOT
// use setActiveModelOnly here: that path is silent and conversation-baseline-
// preserving, the opposite of what a permanent agent move needs.
//
// Called from acpCallbackSink.setAgentModels, once per session start/resume;
// it is a cheap no-op (one metadata read) whenever nothing is pending, so it
// costs nothing on the vastly more common non-moved-session path.
//
// Consumption is atomic and durable: the pending value is read AND cleared in
// the same store.UpdateMetadata call, so a crash/restart between "read" and
// "clear" cannot cause a double-apply, and the attempt runs exactly once
// regardless of how many times this session is subsequently resumed. If the
// user manually changes the model before this hook has run (e.g. the move
// leaves the conversation idle for a while before it is next opened), the
// manual SetConfigOption call does not touch PendingModelMappingFrom — this
// hook still fires the following resume and would silently overwrite that
// manual choice. That narrow window is accepted here: the field is metadata-
// scoped, not model-mutation-scoped, and closing it fully would require
// threading a "user has since touched the model" signal through the manual
// SetConfigOption path, which is out of scope for this bead (see the
// file-ownership constraint on config_manager.go/bgsession_callbacks.go).
//
// Ordering vs ApplyModelTag (mitto-9eci, strict tag pin, bgsession_config.go):
// ApplyModelTag is only ever invoked by an EXPLICIT, externally-triggered
// mitto_conversation_new/_update(model_tag) call, and it requires
// bs.AgentModels() != nil as a precondition — so it can only run at or after
// the point models are first known, i.e. no earlier than this hook's own
// invocation (which fires synchronously as soon as models are known, on the
// very first models callback after resume). Both this hook and ApplyModelTag
// end at the SAME persistent SetConfigOption path, so whichever RPC's
// response is recorded LAST wins the final baseline — there is no separate
// priority flag between them. In the overwhelmingly common case (a caller
// pins model_tag as a synchronous follow-up call after confirming the
// move/resume completed) that call is issued well after this hook has
// already run and finished, so the explicit tag naturally wins simply by
// running later. A pathological race where a model_tag update lands in the
// same few milliseconds as this hook's own SetConfigOption RPC is not
// specially arbitrated: introducing a cross-cutting "explicit pin in
// flight" flag would require touching the generation-tracking machinery in
// bgsession_callbacks.go/config_manager.go, which this bead's file-ownership
// constraint (concurrent WIP in those files) puts out of scope.
func (bs *BackgroundSession) cbApplyPendingModelMapping(models *SessionModelState) {
	if models == nil || len(models.AvailableModels) == 0 {
		return
	}
	if bs.store == nil || bs.persistedID == "" {
		return
	}

	var pendingFrom string
	if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
		pendingFrom = m.PendingModelMappingFrom
		m.PendingModelMappingFrom = ""
	}); err != nil {
		if bs.logger != nil {
			bs.logger.Debug("model-mapping: failed to consume PendingModelMappingFrom",
				"session_id", bs.persistedID, "error", err)
		}
		return
	}
	if pendingFrom == "" {
		return
	}

	resolved := resolveClosestModel(pendingFrom, models)
	if resolved == "" {
		if bs.logger != nil {
			bs.logger.Info("model-mapping: no matching model found on target agent after move; keeping agent default",
				"session_id", bs.persistedID, "previous_model", pendingFrom)
		}
		return
	}

	if resolved == models.CurrentModelId {
		// Already the active model: still promote it to the persisted
		// baseline (mirrors ApplyModelTag's mitto-1yo already-matches
		// short-circuit) so a later resume doesn't drift back to an empty/
		// stale baseline, but skip the RPC + session_change since nothing
		// observably changes for the user.
		bs.cmSetBaselineAndClearOverride(resolved)
		bs.cmPersistBaselineModel(resolved)
		if bs.logger != nil {
			bs.logger.Debug("model-mapping: resolved model already active; promoted to baseline without RPC",
				"session_id", bs.persistedID, "previous_model", pendingFrom, "resolved_model", resolved)
		}
		return
	}

	// SetConfigOption issues an RPC; run it off the callback-delivery
	// goroutine, mirroring cbApplyConfigConstraintsAsync's async pattern so a
	// slow/unreachable agent cannot stall ACP callback processing.
	go func() {
		ctx := bs.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := bs.SetConfigOption(ctx, string(ModelConfigId), resolved); err != nil {
			if bs.logger != nil {
				bs.logger.Warn("model-mapping: failed to apply mapped model after agent move",
					"session_id", bs.persistedID, "previous_model", pendingFrom, "resolved_model", resolved, "error", err)
			}
			return
		}
		if bs.logger != nil {
			bs.logger.Info("model-mapping: applied closest model after agent move",
				"session_id", bs.persistedID, "previous_model", pendingFrom, "resolved_model", resolved)
		}
	}()
}
