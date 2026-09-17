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
	// FeatureMCPHttp reports whether the agent advertised support for the MCP
	// HTTP transport (ACP's AgentCapabilities.McpCapabilities.Http), used to
	// choose between a native HTTP MCP server entry and the stdio proxy
	// fallback (mitto-mx9.1.3).
	FeatureMCPHttp Feature = "mcp_http"
	// FeatureSessionResume reports whether the agent advertised the UNSTABLE
	// session/resume RPC (ACP's AgentCapabilities.SessionCapabilities.Resume),
	// used by the shared-process resume handshake (mitto-mx9.1.3).
	FeatureSessionResume Feature = "session_resume"
	// FeatureSessionLoad reports whether the agent advertised session/load
	// (ACP's AgentCapabilities.LoadSession), used by the shared-process resume
	// handshake's fallback probe (mitto-mx9.1.3).
	FeatureSessionLoad Feature = "session_load"
)

// Capabilities reports the current capability state for each known feature
// of a session or host. Implementations must return CapabilityUnknown rather
// than guessing when support hasn't been advertised one way or the other.
type Capabilities interface {
	Query(feature Feature) CapabilityState
}
