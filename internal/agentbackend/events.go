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
	// ToolCall carries the payload for EventToolCall (tool call announcement
	// or status/title update). Added by mitto-lrt.8; nil for a bare marker.
	ToolCall *ToolCallPayload
	// Plan carries the payload for EventPlan. Added by mitto-lrt.8; nil for
	// a bare marker.
	Plan *PlanPayload

	// UpstreamCursor is an opaque, backend-assigned position marker (e.g. a
	// sequence number) used for gap detection across reconnects. It is kept
	// separate from any Mitto-owned sequence number.
	UpstreamCursor string
}

// ToolCallPayload is the neutral payload for EventToolCall, covering both a
// tool call's initial announcement and later status/title updates (see
// Update). A neutral mirror of conversation.StreamEvent's tool-call fields.
type ToolCallPayload struct {
	// ID uniquely identifies this tool call within the session.
	ID string
	// Title is a human-readable description of what the tool is doing. Left
	// empty on an Update that does not change the title.
	Title string
	// Status is the tool call's current execution status (e.g. "pending",
	// "in_progress", "completed", "failed"), backend-defined.
	Status string
	// Kind categorizes the tool being invoked (backend-defined), when known.
	Kind string
	// Update reports whether this payload is a status/title update to a
	// previously-announced tool call, as opposed to the call's initial
	// announcement.
	Update bool
}

// PlanEntry is a single neutral plan entry, a mirror of conversation.PlanEntry.
type PlanEntry struct {
	// Content is a human-readable description of what this task aims to accomplish.
	Content string
	// Priority indicates the relative importance of this task (backend-defined,
	// e.g. "high", "medium", "low").
	Priority string
	// Status is the current execution status (backend-defined, e.g. "pending",
	// "in_progress", "completed").
	Status string
}

// PlanPayload is the neutral payload for EventPlan. Per the ACP plan-update
// contract (mirrored here), Entries is always the complete, current plan —
// each update replaces the whole list, it is never a delta.
type PlanPayload struct {
	Entries []PlanEntry
}
