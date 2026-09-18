package agentbackend

// StopReason is a neutral enumeration of why a prompt turn ended, replacing
// protocol-specific stop-reason types (e.g. acp.StopReason).
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonCancelled StopReason = "cancelled"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonRefusal   StopReason = "refusal"
	StopReasonError     StopReason = "error"
)

// PromptOutcome is the neutral result of a completed (or cancelled) prompt
// turn. Streamed updates during the turn are delivered separately via
// EventDelivery; PromptOutcome only carries the terminal result.
type PromptOutcome struct {
	StopReason StopReason
	// Content is the assembled response content, when the backend returns it
	// as part of the outcome rather than purely via streamed events.
	Content []ContentBlock
	// Usage is the per-turn token-usage snapshot, when the backend reports
	// one. Nil when the backend did not report usage for this turn (mitto-mx9.1.1).
	Usage *PromptUsage
}

// PromptUsage is the neutral per-turn token-usage snapshot attached to a
// completed PromptOutcome (mitto-mx9.1.1), mirroring the subset of ACP's
// Usage struct that Mitto's token-accounting pipeline actually consumes.
// InputTokens/OutputTokens/TotalTokens are cumulative per-session snapshots
// as ACP reports them (not per-turn deltas) — callers that need a per-turn
// delta must normalize themselves (see BackgroundSession.pdTokenUsageDelta).
// ACP's optional CachedReadTokens/CachedWriteTokens/ThoughtTokens fields are
// not modeled here since no current consumer reads them; extend this struct
// if one needs to.
type PromptUsage struct {
	InputTokens  uint64
	OutputTokens uint64
	TotalTokens  uint64
}
