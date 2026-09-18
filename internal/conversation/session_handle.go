package conversation

import (
	"github.com/inercia/mitto/internal/agentbackend"
)

// SessionHandle is returned when creating, loading, or resuming a session on a SharedProcess.
// It carries the ACP-assigned session ID and per-session state.
type SessionHandle struct {
	// SessionID is the ACP-assigned session ID.
	SessionID string
	// Capabilities are the agent's capabilities (from Initialize), as a
	// protocol-neutral value (mitto-mx9.1.2). Populated via
	// NewProcessCapabilities by SharedProcess implementations.
	Capabilities agentbackend.Capabilities
	// Modes are the session mode state (from NewSession/LoadSession), as a
	// protocol-neutral value (mitto-mx9.1.2). Populated via ModeStateFromACP.
	Modes *agentbackend.ModeState
	// Models are the available models (derived from ConfigOptions with
	// Category="model" on NewSession/LoadSession/ResumeSession, v0.13.5+).
	Models *SessionModelState
	// ModelConfigId is the opaque wire id the agent advertised for the model
	// selection option, captured from ConfigOptions (mitto-mx9.1.2: a plain
	// string, no longer acp.SessionConfigId). Callers use it when issuing
	// session/set_config_option so they match the agent-declared id. Empty when
	// the agent did not advertise a model config option.
	ModelConfigId string
	// ConfigOptions are the session config options (from NewSession/LoadSession).
	ConfigOptions []SessionConfigOption
	// Process is a reference to the parent SharedProcess (interface).
	Process SharedProcess
}
