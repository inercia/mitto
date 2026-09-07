package agentbackend

import "time"

// Origin distinguishes events caused by a local action (e.g. this process
// issued the Prompt call) from ones surfaced by the backend independently
// (e.g. another client/session updated shared remote state). Callers rely on
// this to de-duplicate self-caused updates from externally-originated ones
// (see ADR agent-backend-architecture.md §3).
type Origin int

const (
	OriginLocal Origin = iota
	OriginRemote
)

// LifecycleState is a neutral connection/session lifecycle state.
type LifecycleState string

const (
	LifecycleConnecting   LifecycleState = "connecting"
	LifecycleConnected    LifecycleState = "connected"
	LifecycleReconnecting LifecycleState = "reconnecting"
	LifecycleReconnected  LifecycleState = "reconnected"
	LifecycleDisconnected LifecycleState = "disconnected"
	LifecycleStopped      LifecycleState = "stopped"
)

// EventKind identifies the kind of update carried by an Event.
type EventKind string

const (
	EventAgentMessage     EventKind = "agent_message"
	EventAgentThought     EventKind = "agent_thought"
	EventToolCall         EventKind = "tool_call"
	EventPlan             EventKind = "plan"
	EventFile             EventKind = "file"
	EventLifecycle        EventKind = "lifecycle"
	EventCapabilityChange EventKind = "capability_change"
	EventModelChange      EventKind = "model_change"
	EventModeChange       EventKind = "mode_change"
)

// Event is a neutral session update or lifecycle notification delivered via
// EventDelivery.Subscribe.
type Event struct {
	Kind EventKind
	// Session identifies which session this event belongs to. Zero value for
	// host-level (not session-scoped) lifecycle events.
	Session SessionRef
	// Origin distinguishes self-caused updates from externally-originated ones.
	Origin Origin
	// Time is when the backend observed this event.
	Time time.Time

	// Content carries the payload for content-bearing kinds (AgentMessage,
	// AgentThought, File).
	Content []ContentBlock
	// Lifecycle carries the new state for EventLifecycle.
	Lifecycle LifecycleState
	// Capabilities carries the updated capability state for EventCapabilityChange.
	Capabilities Capabilities
	// Models carries the updated model state for EventModelChange.
	Models *ModelState
	// Modes carries the updated mode state for EventModeChange.
	Modes *ModeState

	// UpstreamCursor is an opaque, backend-assigned position marker (e.g. a
	// sequence number) used for gap detection across reconnects. It is kept
	// separate from any Mitto-owned sequence number.
	UpstreamCursor string
}
