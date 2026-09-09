package agentbackend

import "fmt"

// AgentRef identifies a single agent/provider on a specific backend. It
// pairs a BackendID with the ProviderID that backend advertises, so
// resolving "which agent" never depends on a mutable display name alone —
// a backend rename or provider re-advertisement must not silently change
// which agent an AgentRef points at (see
// docs/devel/agent-backend-architecture.md, mitto-lrt.5).
type AgentRef struct {
	// Backend identifies the configured backend instance hosting Provider.
	Backend BackendID
	// Provider identifies the specific agent/provider on Backend.
	Provider ProviderID
}

// Validate reports whether r names both a Backend and a Provider.
func (r AgentRef) Validate() error {
	if r.Backend == "" {
		return fmt.Errorf("agentbackend: AgentRef.Backend must not be empty")
	}
	if r.Provider == "" {
		return fmt.Errorf("agentbackend: AgentRef.Provider must not be empty")
	}
	return nil
}
