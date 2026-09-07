package acpbackend

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

// sessionCapabilities is a Capabilities snapshot for one ACP session, derived
// from the agent's advertised AgentCapabilities plus whether this particular
// session actually reported a model/mode catalog (SessionHandle.Models /
// .Modes). Model/mode support is a per-session fact (it depends on what this
// agent process actually advertised for this session), not a fixed
// agent-level flag, so it cannot be read off AgentCapabilities alone.
type sessionCapabilities struct {
	agent     acp.AgentCapabilities
	hasModels bool
	hasModes  bool
}

// newSessionCapabilities builds a sessionCapabilities snapshot from the
// AgentCapabilities returned at Initialize plus this session's model/mode
// state (from conversation.SessionHandle.Models/.Modes).
func newSessionCapabilities(agentCaps acp.AgentCapabilities, models *conversation.SessionModelState, modes *acp.SessionModeState) *sessionCapabilities {
	return &sessionCapabilities{
		agent:     agentCaps,
		hasModels: models != nil && len(models.AvailableModels) > 0,
		hasModes:  modes != nil && len(modes.AvailableModes) > 0,
	}
}

// Query implements agentbackend.Capabilities.
func (c *sessionCapabilities) Query(feature agentbackend.Feature) agentbackend.CapabilityState {
	switch feature {
	case agentbackend.FeatureImages:
		if c.agent.PromptCapabilities.Image {
			return agentbackend.CapabilitySupported
		}
		return agentbackend.CapabilityUnsupported
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
		// FeatureTerminals and any future feature: not modeled by ACP's
		// AgentCapabilities/ClientCapabilities in a queryable way here —
		// report Unknown rather than guessing (ADR
		// agent-backend-architecture.md §5).
		return agentbackend.CapabilityUnknown
	}
}

var _ agentbackend.Capabilities = (*sessionCapabilities)(nil)
