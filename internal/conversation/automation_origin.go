package conversation

// automation_origin.go gates local automation (loop onCompletion re-fire,
// follow-up/title generation, child dispatch) on event Origin so a
// host-echoed turn cannot cause duplicate local automation (mitto-lrt.10
// acceptance criteria: "No host-echoed turn causes duplicate local
// automation").
//
// ACP today never produces agentbackend.OriginRemote events —
// internal/acpbackend/events.go tags every inbound event OriginLocal because
// Mitto is the sole client driving each ACP session. This predicate is
// therefore inert in production until a remote-echoing backend exists (e.g.
// an AHP host where another client's turn is echoed back to this
// conversation); its only current consumer is this file's own test,
// exercised via agentbackend.NewFakeHost + InjectRemoteUpdate.

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
