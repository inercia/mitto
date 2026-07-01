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
	// Models are the available models (UNSTABLE, from NewSession/LoadSession/ResumeSession).
	// Uses UnstableSessionModelState to unify both stable and unstable response variants.
	Models *acp.UnstableSessionModelState
	// ConfigOptions are the session config options (from NewSession/LoadSession).
	ConfigOptions []SessionConfigOption
	// Process is a reference to the parent SharedProcess (interface).
	Process SharedProcess
}
