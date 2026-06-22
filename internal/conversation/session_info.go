package conversation

import "time"

// SessionInfo carries a snapshot of a running session's state for use by the
// ACP process GC. It is grouped by workspace UUID so the GC can decide which
// shared processes are still needed.
type SessionInfo struct {
	SessionID     string
	WorkspaceUUID string
	IsPrompting   bool
	HasObservers  bool
	// IsChild is true when this session was spawned by another session (has a parent).
	// Used by GC to apply ChildIdleTimeout instead of IdleTimeout.
	IsChild bool
	// HasConnectedClients is true when there are WebSocket connections that have not
	// yet registered as observers (i.e., connected but haven't sent load_events).
	HasConnectedClients bool
	QueueLength         int
	// NextPeriodicAt is when the next periodic prompt is due (nil = no periodic config).
	NextPeriodicAt *time.Time
	// ResumedAt is when the session was last started/resumed. Used by GC to give
	// freshly resumed sessions a grace period before considering them idle.
	ResumedAt time.Time
	// LastObserverRemovedAt is when the observer count last dropped to zero.
	// Used by GC to provide a grace period for reconnecting clients.
	LastObserverRemovedAt time.Time
	// LastActivityAt is when the session last had meaningful activity (keepalive,
	// prompt, or observer change). Used by GC idle timeout check.
	// Note: this is set at prompt START, so it is stale by the end of a long task.
	LastActivityAt time.Time
	// LastResponseCompleteAt is when the agent last finished a turn (completed a
	// response). Unlike LastActivityAt (set at prompt start), this marks the END of
	// work, making it the correct signal for the periodic-suspend grace window.
	// Zero if the agent has not completed a response since the session was resumed.
	LastResponseCompleteAt time.Time
}
