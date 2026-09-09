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
}
