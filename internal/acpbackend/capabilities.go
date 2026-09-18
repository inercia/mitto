package acpbackend

import (
	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

// sessionCapabilities is a Capabilities snapshot for one ACP session, derived
// from the agent's advertised process-level capabilities plus whether this
// particular session actually reported a model/mode catalog
// (SessionHandle.Models / .Modes). Model/mode support is a per-session fact
// (it depends on what this agent process actually advertised for this
// session), not a fixed agent-level flag, so it cannot be read off the
// process-level capabilities alone.
type sessionCapabilities struct {
	agent     agentbackend.Capabilities
	hasModels bool
	hasModes  bool
}

// newSessionCapabilities builds a sessionCapabilities snapshot from the
// process-level Capabilities captured at Initialize (SessionHandle.
// Capabilities, mitto-mx9.1.2) plus this session's model/mode state (from
// conversation.SessionHandle.Models/.Modes).
func newSessionCapabilities(agentCaps agentbackend.Capabilities, models *conversation.SessionModelState, modes *agentbackend.ModeState) *sessionCapabilities {
	return &sessionCapabilities{
		agent:     agentCaps,
		hasModels: models != nil && len(models.AvailableModels) > 0,
		hasModes:  modes != nil && len(modes.Available) > 0,
	}
}

// Query implements agentbackend.Capabilities.
func (c *sessionCapabilities) Query(feature agentbackend.Feature) agentbackend.CapabilityState {
	switch feature {
	case agentbackend.FeatureFiles:
		// Mitto's ACP Initialize handshake always advertises
		// ClientCapabilities.Fs{ReadTextFile,WriteTextFile}=true (see
		// internal/acpproc.SharedACPProcess's Initialize call) independent of
		// which agent is connected, so file support is a constant fact about
		// this host, not something to read off the agent's capabilities.
		return agentbackend.CapabilitySupported
	case agentbackend.FeaturePermissions:
		// Mitto always wires a permission handler
		// (SessionCallbacks.OnRequestPermission), auto-approving when no
		// interactive client is present — likewise a constant fact about this
		// host rather than an agent-advertised capability.
		return agentbackend.CapabilitySupported
	case agentbackend.FeatureModelSelection:
		if c.hasModels {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	case agentbackend.FeatureModeSelection:
		if c.hasModes {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
	default:
		// FeatureImages and any other feature this layer doesn't answer
		// directly (e.g. FeatureTerminals): delegate to the process-level
		// Capabilities snapshot (mitto-mx9.1.2), which already answers
		// FeatureImages from the raw AgentCapabilities (see
		// acpProcessCapabilities.Query in backend_provider_acp.go). Reports
		// Unknown rather than guessing when agent is nil (ADR
		// agent-backend-architecture.md §5).
		if c.agent == nil {
			return agentbackend.CapabilityUnknown
		}
		return c.agent.Query(feature)
	}
}

var _ agentbackend.Capabilities = (*sessionCapabilities)(nil)
