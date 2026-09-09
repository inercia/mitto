package acpbackend

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// ToNeutralStopReason translates an ACP stop reason into its neutral
// counterpart. ACP's StopReasonMaxTurnRequests (the turn ran out of request
// budget) has no dedicated neutral state; it is mapped to StopReasonMaxTokens
// since both represent a turn ending due to an exhausted budget rather than a
// definite error, refusal, or cancellation. Any other/unrecognized value maps
// to StopReasonError so callers never observe an undefined stop reason.
func ToNeutralStopReason(r acp.StopReason) agentbackend.StopReason {
	switch r {
	case acp.StopReasonEndTurn:
		return agentbackend.StopReasonEndTurn
	case acp.StopReasonCancelled:
		return agentbackend.StopReasonCancelled
	case acp.StopReasonMaxTokens, acp.StopReasonMaxTurnRequests:
		return agentbackend.StopReasonMaxTokens
	case acp.StopReasonRefusal:
		return agentbackend.StopReasonRefusal
	default:
		return agentbackend.StopReasonError
	}
}

// ToNeutralPromptOutcome translates an ACP PromptResponse into the neutral
// PromptOutcome. content is the assembled response content to attach, when
// the caller has one — ACP's PromptResponse itself carries no content (the
// turn's content arrives via streamed SessionNotification updates, translated
// separately by EventDelivery); pass nil when there is none to attach.
func ToNeutralPromptOutcome(resp acp.PromptResponse, content []agentbackend.ContentBlock) agentbackend.PromptOutcome {
	return agentbackend.PromptOutcome{
		StopReason: ToNeutralStopReason(resp.StopReason),
		Content:    content,
	}
}
