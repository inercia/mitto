package conversation

import (
	acp "github.com/coder/acp-go-sdk"
)

// SessionHandle is returned when creating, loading, or resuming a session on a SharedProcess.
// It carries the ACP-assigned session ID and per-session state.
type SessionHandle struct {
	// SessionID is the ACP-assigned session ID.
	SessionID string
	// Capabilities are the agent's capabilities (from Initialize).
	Capabilities acp.AgentCapabilities
	// Modes are the session mode state (from NewSession/LoadSession).
	Modes *acp.SessionModeState
	// Models are the available models (derived from ConfigOptions with
	// Category="model" on NewSession/LoadSession/ResumeSession, v0.13.5+).
	Models *SessionModelState
	// ModelConfigId is the SessionConfigId the agent advertised for the model
	// selection option, captured from ConfigOptions. Callers use it when issuing
	// session/set_config_option so they match the agent-declared id. Empty when
	// the agent did not advertise a model config option.
	ModelConfigId acp.SessionConfigId
	// ConfigOptions are the session config options (from NewSession/LoadSession).
	ConfigOptions []SessionConfigOption
	// Process is a reference to the parent SharedProcess (interface).
	Process SharedProcess
}
