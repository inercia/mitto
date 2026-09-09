package eventprojection

import "github.com/inercia/mitto/internal/agentbackend"

// SourceID identifies the specific upstream backend session a Projector
// consumes events from and a Checkpoint is durably keyed by. It is built
// from an explicit agentbackend.BackendID (agentbackend.SessionRef alone
// does not carry backend identity, only provider + upstream session id) so
// two different backends that happen to reuse the same ProviderID/
// ProviderSessionID pair are never confused with one another.
type SourceID struct {
	Backend         agentbackend.BackendID
	Provider        agentbackend.ProviderID
	ProviderSession agentbackend.ProviderSessionID
}

// SourceIDFromSession builds a SourceID for ref on the given backend.
func SourceIDFromSession(backend agentbackend.BackendID, ref agentbackend.SessionRef) SourceID {
	return SourceID{Backend: backend, Provider: ref.Provider, ProviderSession: ref.ProviderSession}
}
