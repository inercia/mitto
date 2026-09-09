package agentbackend

// CapabilityState is a three-state capability flag. Unknown is a distinct
// state from both Supported and Unsupported and must never be silently
// collapsed into either — callers that need a definite answer should treat
// Unknown as "not yet safe to assume support" (see ADR
// agent-backend-architecture.md §5).
type CapabilityState int

const (
	CapabilityUnknown CapabilityState = iota
	CapabilitySupported
	CapabilityUnsupported
)

// String returns a lowercase, stable string form for logging/debugging.
func (s CapabilityState) String() string {
	switch s {
	case CapabilitySupported:
		return "supported"
	case CapabilityUnsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

// Feature identifies a queryable backend/session capability.
type Feature string

const (
	FeatureImages         Feature = "images"
	FeatureFiles          Feature = "files"
	FeatureTerminals      Feature = "terminals"
	FeaturePermissions    Feature = "permissions"
	FeatureModelSelection Feature = "model_selection"
	FeatureModeSelection  Feature = "mode_selection"
)

// Capabilities reports the current capability state for each known feature
// of a session or host. Implementations must return CapabilityUnknown rather
// than guessing when support hasn't been advertised one way or the other.
type Capabilities interface {
	Query(feature Feature) CapabilityState
}
