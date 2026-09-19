package conversation

// automation_origin.go gates local automation (loop onCompletion re-fire,
// follow-up/title generation, after-phase processors, child dispatch) on
// event Origin so a host-echoed turn cannot cause duplicate local automation
// (mitto-lrt.10 acceptance criteria: "No host-echoed turn causes duplicate
// local automation").
//
// ACP today never produces agentbackend.OriginRemote events —
// internal/acpbackend/events.go tags every inbound event OriginLocal because
// Mitto is the sole client driving each ACP session. This predicate is
// therefore always true in production today, until a remote-echoing backend
// exists (e.g. an AHP host where another client's turn is echoed back to
// this conversation).
//
// mitto-mx9.3: ShouldTriggerLocalAutomation is now a real production
// consumer, not just this file's own test. promptDispatcher.handlePromptSuccess
// and promptDispatcher.finalizeTurn (prompt_dispatcher.go) call it — hardcoded
// to agentbackend.OriginLocal, since the ACP prompt-completion pipeline is
// driven entirely by this session's own Prompt() RPC response, never by an
// inbound event notification, so Origin is always local there by
// construction. Gating handlePromptSuccess covers title generation,
// follow-up analysis, and after-phase processors directly; gating
// finalizeTurn's call to pdOnTurnIdle() covers loop onCompletion re-fire
// (LoopRunner.OnConversationIdle) and child dispatch (OnChildEndResponse)
// transitively, since both have exactly one production caller each and both
// are reached only through pdOnTurnIdle. A future event-delivery-driven
// backend must replace the hardcoded OriginLocal at those two call sites
// with the real inbound event's Origin. This file's own test continues to
// exercise the predicate directly via agentbackend.NewFakeHost +
// InjectRemoteUpdate.

import "github.com/inercia/mitto/internal/agentbackend"

// ShouldTriggerLocalAutomation reports whether an event/turn with the given
// Origin may trigger this conversation's own local automation. Only
// locally-originated activity (Origin == OriginLocal, i.e. this conversation
// itself issued the turn) may do so; an externally-originated
// (OriginRemote) update — for example another client's turn being echoed
// back by a shared remote host — must not redundantly re-run automation
// that already ran (or will run) for the client that actually caused it.
func ShouldTriggerLocalAutomation(origin agentbackend.Origin) bool {
	return origin == agentbackend.OriginLocal
}
