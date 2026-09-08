package api

import "encoding/json"

// Optional, additive protocol-neutral descriptor types (mitto-lrt.12),
// decode-side mirrors of internal/web/handlers.NeutralBackendDescriptor and
// friends. These are purely additive: legacy REST/WS payloads that omit
// "backend" decode to a nil *BackendDescriptor everywhere below, and every
// existing field (ACPSessionID, ACPServer, etc.) keeps its current meaning.

// AgentRef identifies a single agent/provider on a specific backend.
type AgentRef struct {
	Backend  string `json:"backend"`
	Provider string `json:"provider"`
}

// SessionRef pairs a Mitto-owned conversation id with the upstream provider
// and its upstream-assigned session id, without collapsing the two
// identifier spaces into one.
type SessionRef struct {
	ConversationID  string `json:"conversation_id"`
	Provider        string `json:"provider"`
	ProviderSession string `json:"provider_session,omitempty"`
}

// ModelOption describes a single selectable model.
type ModelOption struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// ModelState is a neutral view of a session's available models and the
// currently selected one.
type ModelState struct {
	CurrentID string        `json:"current_id,omitempty"`
	Available []ModelOption `json:"available,omitempty"`
}

// ConfigOptionValue is one selectable value of a ConfigOption.
type ConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// ConfigOption is a neutral mirror of a session-level configuration knob
// (e.g. model or mode selection exposed as a generic option).
type ConfigOption struct {
	ID       string              `json:"id"`
	Category string              `json:"category,omitempty"`
	Current  string              `json:"current,omitempty"`
	Values   []ConfigOptionValue `json:"values,omitempty"`
}

// BackendDescriptor is the optional "backend" block that may ride inside
// GET /api/sessions (list/single) responses and the WebSocket
// "connected"/"acp_started" snapshots. Every field is optional: only the
// sub-blocks the server could actually compute for a given session are
// populated. A nil *BackendDescriptor (the zero value when the field is
// absent from a legacy payload) is always a valid, meaningful state — never
// synthesize one.
type BackendDescriptor struct {
	AgentRef      *AgentRef         `json:"agent_ref,omitempty"`
	SessionRef    *SessionRef       `json:"session_ref,omitempty"`
	Capabilities  map[string]string `json:"capabilities,omitempty"`
	Model         *ModelState       `json:"model,omitempty"`
	ConfigOptions []ConfigOption    `json:"config_options,omitempty"`
}

// DecodeBackendDescriptor extracts the optional "backend" block from a raw
// WebSocket payload map, such as the one delivered to
// SessionCallbacks.OnConnectedFull, or from OnRawMessage's data for
// "acp_started" (which has no full-payload callback yet). Returns nil, nil
// when the key is absent (legacy payload or nothing computable server-side)
// — this is the expected, common case and not an error.
func DecodeBackendDescriptor(raw map[string]interface{}) (*BackendDescriptor, error) {
	v, ok := raw["backend"]
	if !ok || v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var desc BackendDescriptor
	if err := json.Unmarshal(b, &desc); err != nil {
		return nil, err
	}
	return &desc, nil
}
