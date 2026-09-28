package conversation

// Richer context handoff on the first prompt after an agent move (mitto-f7yo.5).
//
// MoveSessionToAgent (mitto-f7yo.1) stashes the previous agent's name into
// session.Metadata.PendingAgentHandoffFrom, in the very same UpdateMetadata
// call that clears BaselineModel/sets PendingModelMappingFrom for mitto-f7yo.4
// — so it's set for a moved session AND for every descendant moved via
// IncludeChildren (moveAgentChildren reuses the same rebind function).
//
// The flag is consumed by buildPromptWithHistory (bgsession_prompt.go), which
// is only ever invoked when shouldInjectHistory is true — i.e. under the
// existing `bs.isResumed && !bs.historyInjected && !meta.FreshContext` gate.
// That means:
//   - A FreshContext loop run never even calls buildPromptWithHistory, so the
//     flag is left untouched (not cleared) rather than being silently
//     dropped — it correctly fires on a later, non-FreshContext prompt
//     instead of being lost to a loop run the operator didn't initiate.
//   - Once a real (non-FreshContext) history-injecting prompt does run, the
//     flag is consumed exactly once (atomically read-and-cleared via the same
//     store.UpdateMetadata pattern mitto-f7yo.4 uses for
//     PendingModelMappingFrom), so a second prompt never repeats the
//     preamble/larger budget even across process restarts.
//   - Normal resumes that were never moved never have this field set, so
//     their behaviour (session.BuildConversationHistory(events, 5), no
//     preamble) is completely unchanged.

import "github.com/inercia/mitto/internal/session"

const (
	// agentHandoffMaxTurns bounds the number of turns considered for the
	// larger history budget injected on the first prompt after an agent
	// move — well above the normal 5-turn window, since the new agent has
	// no other context about the conversation at all.
	agentHandoffMaxTurns = 20
	// agentHandoffMaxChars bounds the total rendered size of that history,
	// in characters, regardless of how many turns agentHandoffMaxTurns
	// would otherwise allow. session.BuildConversationHistoryCapped trims
	// the OLDEST turns first to fit this budget, always keeping the most
	// recent context.
	agentHandoffMaxChars = 24000
)

// buildAgentHandoffPreamble returns the short explanatory text prepended
// ahead of the capped history on the first prompt after an agent move,
// naming the previous agent so the new one understands why tools, MCP
// servers, and prompt behaviour may differ from what came before.
func buildAgentHandoffPreamble(previousAgent string) string {
	return "This conversation was previously handled by agent \"" + previousAgent +
		"\" and has been moved to you. Tools, MCP servers and prompts may differ " +
		"from what the previous agent had. Below is the recent transcript for context.\n\n"
}

// consumePendingAgentHandoff atomically reads and clears
// session.Metadata.PendingAgentHandoffFrom for this session, returning the
// previous agent's name and true when a handoff was pending. It is a no-op
// (returns "", false) when there's no store/persisted ID, on a store error
// (logged at Warn), or when nothing was pending — in every one of those
// cases the caller should fall back to the normal history-injection path.
//
// Must only be called from a context that is ALREADY known to be performing
// history injection (i.e. behind the same isResumed/!historyInjected/
// !FreshContext gate buildPromptWithHistory uses) — calling it from anywhere
// else would consume (and lose) the pending flag without ever using it.
func (bs *BackgroundSession) consumePendingAgentHandoff() (previousAgent string, ok bool) {
	if bs.store == nil || bs.persistedID == "" {
		return "", false
	}
	if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
		previousAgent = m.PendingAgentHandoffFrom
		m.PendingAgentHandoffFrom = ""
	}); err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to read/clear pending agent handoff metadata", "error", err)
		}
		return "", false
	}
	return previousAgent, previousAgent != ""
}
