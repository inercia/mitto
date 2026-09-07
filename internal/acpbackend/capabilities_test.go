package acpbackend

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

func TestSessionCapabilities_Images(t *testing.T) {
	supported := newSessionCapabilities(acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{Image: true}}, nil, nil)
	if got := supported.Query(agentbackend.FeatureImages); got != agentbackend.CapabilitySupported {
		t.Errorf("Images = %v, want Supported", got)
	}
	unsupported := newSessionCapabilities(acp.AgentCapabilities{}, nil, nil)
	if got := unsupported.Query(agentbackend.FeatureImages); got != agentbackend.CapabilityUnsupported {
		t.Errorf("Images = %v, want Unsupported", got)
	}
}

func TestSessionCapabilities_FilesAndPermissions_AlwaysSupported(t *testing.T) {
	caps := newSessionCapabilities(acp.AgentCapabilities{}, nil, nil)
	if got := caps.Query(agentbackend.FeatureFiles); got != agentbackend.CapabilitySupported {
		t.Errorf("Files = %v, want Supported (constant host fact)", got)
	}
	if got := caps.Query(agentbackend.FeaturePermissions); got != agentbackend.CapabilitySupported {
		t.Errorf("Permissions = %v, want Supported (constant host fact)", got)
	}
}

func TestSessionCapabilities_ModelSelection(t *testing.T) {
	withModels := newSessionCapabilities(acp.AgentCapabilities{}, &conversation.SessionModelState{
		AvailableModels: []conversation.ModelInfo{{ModelId: "m-1", Name: "Model 1"}},
	}, nil)
	if got := withModels.Query(agentbackend.FeatureModelSelection); got != agentbackend.CapabilitySupported {
		t.Errorf("ModelSelection = %v, want Supported when a catalog is present", got)
	}

	noModels := newSessionCapabilities(acp.AgentCapabilities{}, &conversation.SessionModelState{}, nil)
	if got := noModels.Query(agentbackend.FeatureModelSelection); got != agentbackend.CapabilityUnsupported {
		t.Errorf("ModelSelection = %v, want Unsupported for an empty catalog", got)
	}

	nilModels := newSessionCapabilities(acp.AgentCapabilities{}, nil, nil)
	if got := nilModels.Query(agentbackend.FeatureModelSelection); got != agentbackend.CapabilityUnsupported {
		t.Errorf("ModelSelection = %v, want Unsupported for a nil state", got)
	}
}

func TestSessionCapabilities_ModeSelection(t *testing.T) {
	withModes := newSessionCapabilities(acp.AgentCapabilities{}, nil, &acp.SessionModeState{
		AvailableModes: []acp.SessionMode{{Id: "code", Name: "Code"}},
	})
	if got := withModes.Query(agentbackend.FeatureModeSelection); got != agentbackend.CapabilitySupported {
		t.Errorf("ModeSelection = %v, want Supported when modes are present", got)
	}

	nilModes := newSessionCapabilities(acp.AgentCapabilities{}, nil, nil)
	if got := nilModes.Query(agentbackend.FeatureModeSelection); got != agentbackend.CapabilityUnsupported {
		t.Errorf("ModeSelection = %v, want Unsupported for a nil state", got)
	}
}

func TestSessionCapabilities_Terminals_Unknown(t *testing.T) {
	// Terminals must report Unknown, never guessed Supported/Unsupported —
	// three-state ADR rule (agent-backend-architecture.md §5).
	caps := newSessionCapabilities(acp.AgentCapabilities{}, nil, nil)
	if got := caps.Query(agentbackend.FeatureTerminals); got != agentbackend.CapabilityUnknown {
		t.Errorf("Terminals = %v, want Unknown", got)
	}
	if got := caps.Query(agentbackend.Feature("some-future-feature")); got != agentbackend.CapabilityUnknown {
		t.Errorf("unrecognized feature = %v, want Unknown", got)
	}
}
