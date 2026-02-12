// Package session provides session persistence and management for Mitto.
package session

import (
	"fmt"
	"time"
)

// SessionStore defines the interface for session persistence operations.
// This interface allows for easier testing by enabling mock implementations.
type SessionStore interface {
	// Create creates a new session with the given metadata.
	Create(meta Metadata) error

	// AppendEvent appends an event to the session's event log.
	// The event's Seq field is automatically assigned based on the current event count.
	AppendEvent(sessionID string, event Event) error

	// RecordEvent persists an event with its pre-assigned sequence number.
	// Unlike AppendEvent, this method does NOT reassign the seq field.
	// The event.Seq must be > 0 (assigned by the caller).
	// This is used for immediate persistence where seq is assigned at streaming time.
	RecordEvent(sessionID string, event Event) error

	// GetMetadata retrieves the metadata for a session.
	GetMetadata(sessionID string) (Metadata, error)

	// UpdateMetadata updates the metadata for a session using the provided update function.
	UpdateMetadata(sessionID string, updateFn func(*Metadata)) error

	// ReadEvents reads all events from a session's event log.
	ReadEvents(sessionID string) ([]Event, error)

	// ReadEventsFrom reads events from a session's event log starting after the given sequence number.
	// If afterSeq is 0, all events are returned.
	// If afterSeq is 5, only events with seq > 5 are returned.
	ReadEventsFrom(sessionID string, afterSeq int64) ([]Event, error)

	// ReadEventsLast reads the last N events from a session's event log.
	// If beforeSeq > 0, only events with seq < beforeSeq are considered.
	// Returns events in chronological order (oldest first).
	ReadEventsLast(sessionID string, limit int, beforeSeq int64) ([]Event, error)

	// ReadEventsLastReverse reads the last N events from a session's event log in reverse order.
	// If beforeSeq > 0, only events with seq < beforeSeq are considered.
	// Returns events in reverse chronological order (newest first).
	// This is optimized for UIs that render newest messages first.
	ReadEventsLastReverse(sessionID string, limit int, beforeSeq int64) ([]Event, error)

	// List returns metadata for all sessions.
	List() ([]Metadata, error)

	// Delete removes a session and all its data.
	Delete(sessionID string) error

	// Exists checks if a session exists.
	Exists(sessionID string) bool

	// Close closes the store and releases any resources.
	Close() error
}

// EventType represents the type of event in a session log.
type EventType string

const (
	EventTypeUserPrompt     EventType = "user_prompt"
	EventTypeAgentMessage   EventType = "agent_message"
	EventTypeAgentThought   EventType = "agent_thought"
	EventTypeToolCall       EventType = "tool_call"
	EventTypeToolCallUpdate EventType = "tool_call_update"
	EventTypePlan           EventType = "plan"
	EventTypePermission     EventType = "permission"
	EventTypeFileRead       EventType = "file_read"
	EventTypeFileWrite      EventType = "file_write"
	EventTypeError          EventType = "error"
	EventTypeSessionStart   EventType = "session_start"
	EventTypeSessionEnd     EventType = "session_end"
)

// SessionStatus represents the status of a session.
type SessionStatus string

const (
	// SessionStatusActive indicates the session is active and can receive prompts.
	SessionStatusActive SessionStatus = "active"
	// SessionStatusCompleted indicates the session has been completed normally.
	SessionStatusCompleted SessionStatus = "completed"
	// SessionStatusError indicates the session ended with an error.
	SessionStatusError SessionStatus = "error"
)

// Event represents a single event in the session log.
type Event struct {
	Seq       int64       `json:"seq"` // Sequence number (1-based, monotonically increasing per session)
	Type      EventType   `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// UserPromptData contains data for a user prompt event.
type UserPromptData struct {
	Message  string     `json:"message"`
	Images   []ImageRef `json:"images,omitempty"`
	Files    []FileRef  `json:"files,omitempty"`
	PromptID string     `json:"prompt_id,omitempty"` // Client-generated ID for delivery confirmation
}

// AgentMessageData contains data for an agent message event.
// Note: The field is named "Text" for historical reasons but actually contains
// HTML (converted from markdown by the web layer's MarkdownBuffer).
// The JSON field name "html" is used for consistency with frontend expectations.
type AgentMessageData struct {
	Text string `json:"html"` // Contains HTML content (despite field name)
}

// AgentThoughtData contains data for an agent thought event.
type AgentThoughtData struct {
	Text string `json:"text"`
}

// ToolCallData contains data for a tool call event.
type ToolCallData struct {
	ToolCallID string `json:"tool_call_id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Kind       string `json:"kind,omitempty"`
	RawInput   any    `json:"raw_input,omitempty"`
	RawOutput  any    `json:"raw_output,omitempty"`
}

// ToolCallUpdateData contains data for a tool call update event.
type ToolCallUpdateData struct {
	ToolCallID string  `json:"tool_call_id"`
	Status     *string `json:"status,omitempty"`
	Title      *string `json:"title,omitempty"`
}

// PlanData contains data for a plan event.
type PlanData struct {
	Entries []PlanEntry `json:"entries"`
}

// PlanEntry represents a single entry in a plan.
// This mirrors the ACP protocol's PlanEntry structure.
type PlanEntry struct {
	// Content is a human-readable description of what this task aims to accomplish.
	Content string `json:"content"`
	// Priority indicates the relative importance of this task (high, medium, low).
	Priority string `json:"priority"`
	// Status is the current execution status (pending, in_progress, completed).
	Status string `json:"status"`
}

// PermissionData contains data for a permission event.
type PermissionData struct {
	Title          string `json:"title"`
	SelectedOption string `json:"selected_option"`
	Outcome        string `json:"outcome"` // "approved", "denied", "cancelled"
}

// FileOperationData contains data for file read/write events.
type FileOperationData struct {
	Path    string `json:"path"`
	Size    int    `json:"size"`
	Content string `json:"content,omitempty"` // Only for small files, optional
}

// ErrorData contains data for an error event.
type ErrorData struct {
	Message string `json:"message"`
	Code    int    `json:"code,omitempty"`
}

// SessionStartData contains data for session start event.
type SessionStartData struct {
	SessionID  string `json:"session_id"`
	ACPServer  string `json:"acp_server"`
	WorkingDir string `json:"working_dir"`
}

// SessionEndData contains data for session end event.
type SessionEndData struct {
	Reason string `json:"reason"` // "user_quit", "error", "timeout", etc.
}

// Metadata contains session metadata stored separately from the event log.
type Metadata struct {
	SessionID         string        `json:"session_id"`
	Name              string        `json:"name,omitempty"` // User-friendly session name
	ACPServer         string        `json:"acp_server"`
	ACPSessionID      string        `json:"acp_session_id,omitempty"` // ACP-assigned session ID for resumption
	WorkingDir        string        `json:"working_dir"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	LastUserMessageAt time.Time     `json:"last_user_message_at,omitempty"` // Time of last user prompt
	EventCount        int           `json:"event_count"`
	MaxSeq            int64         `json:"max_seq,omitempty"` // Highest sequence number persisted (for immediate persistence)
	Status            SessionStatus `json:"status"`
	Description       string        `json:"description,omitempty"`
	Pinned            bool          `json:"pinned,omitempty"`            // Deprecated: use Archived instead. If true, session cannot be deleted
	Archived          bool          `json:"archived,omitempty"`          // If true, session is archived (hidden from main list by default)
	ArchivedAt        time.Time     `json:"archived_at,omitempty"`       // Time when session was archived (cleared when unarchived)
	RunnerType        string        `json:"runner_type,omitempty"`       // Type of runner used (exec, sandbox-exec, firejail, docker)
	RunnerRestricted  bool          `json:"runner_restricted,omitempty"` // Whether the runner has restrictions enabled
}

// LockStatus represents the current activity status of a locked session.
type LockStatus string

const (
	// LockStatusIdle indicates the session is idle and safe to steal.
	LockStatusIdle LockStatus = "idle"
	// LockStatusProcessing indicates the agent is actively processing a prompt.
	LockStatusProcessing LockStatus = "processing"
	// LockStatusWaitingPermission indicates waiting for user permission approval.
	LockStatusWaitingPermission LockStatus = "waiting_for_permission"
)

// LockInfo contains information about the session lock.
type LockInfo struct {
	// Process identification
	PID        int    `json:"pid"`
	Hostname   string `json:"hostname"`
	InstanceID string `json:"instance_id"` // Unique ID for this mitto instance

	// Client information
	ClientType string `json:"client_type"` // "cli", "web", "api", etc.

	// Timestamps
	StartedAt    time.Time `json:"started_at"`
	Heartbeat    time.Time `json:"heartbeat"`
	LastActivity time.Time `json:"last_activity"`

	// Activity status
	Status        LockStatus `json:"status"`
	StatusMessage string     `json:"status_message,omitempty"` // Human-readable status detail
}

// IsStale returns true if the lock appears to be stale (no recent heartbeat).
func (l *LockInfo) IsStale(timeout time.Duration) bool {
	return time.Since(l.Heartbeat) > timeout
}

// IsProcessDead checks if the process that holds the lock is no longer running.
// This is used to detect crashed processes that didn't clean up their locks.
// Note: This only works reliably on the same host.
func (l *LockInfo) IsProcessDead(currentHostname string) bool {
	// Can only check if we're on the same host
	if l.Hostname != currentHostname {
		return false // Can't determine, assume alive
	}
	return !isPIDRunning(l.PID)
}

// IsSafeToSteal returns true if the session can be safely stolen.
func (l *LockInfo) IsSafeToSteal(staleTimeout time.Duration) bool {
	// Safe to steal if lock is stale
	if l.IsStale(staleTimeout) {
		return true
	}
	// Safe to steal if session is idle
	return l.Status == LockStatusIdle
}

// StealabilityReason returns a human-readable reason for why the session can or cannot be stolen.
func (l *LockInfo) StealabilityReason(staleTimeout time.Duration) string {
	if l.IsStale(staleTimeout) {
		staleDuration := time.Since(l.Heartbeat).Round(time.Second)
		return fmt.Sprintf("Session appears stale (no heartbeat for %v) - safe to force resume", staleDuration)
	}

	switch l.Status {
	case LockStatusIdle:
		return "Session is idle and can be resumed"
	case LockStatusProcessing:
		return "Session is currently processing - cannot steal (agent is working)"
	case LockStatusWaitingPermission:
		return "Session is waiting for permission approval - cannot steal"
	default:
		return fmt.Sprintf("Session has unknown status: %s", l.Status)
	}
}
