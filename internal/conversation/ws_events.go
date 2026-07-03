package conversation

// Domain lifecycle WebSocket event types emitted by SessionManager/BackgroundSession.
const (
	// WSMsgTypeSessionCreated notifies that a new session was created.
	WSMsgTypeSessionCreated = "session_created"

	// WSMsgTypeSessionArchived notifies that a session's archived state changed.
	WSMsgTypeSessionArchived = "session_archived"

	// WSMsgTypeSessionDeleted notifies that a session was deleted.
	WSMsgTypeSessionDeleted = "session_deleted"

	// WSMsgTypeSessionRenamed notifies that a session was renamed.
	WSMsgTypeSessionRenamed = "session_renamed"

	// WSMsgTypePeriodicUpdated notifies that a session's periodic prompt state changed.
	WSMsgTypePeriodicUpdated = "periodic_updated"

	// WSMsgTypeSessionWaiting notifies that a session's waiting-for-children state changed.
	WSMsgTypeSessionWaiting = "session_waiting"

	// WSMsgTypeSessionStreaming notifies that a session's streaming state changed.
	WSMsgTypeSessionStreaming = "session_streaming"

	// WSMsgTypeSessionUIPrompt notifies that a session's UI prompt state changed.
	WSMsgTypeSessionUIPrompt = "session_ui_prompt"

	// WSMsgTypeBackgroundUIPromptTimeout notifies all clients that a blocking UI prompt
	// timed out in a background session.
	WSMsgTypeBackgroundUIPromptTimeout = "background_ui_prompt_timeout"

	// WSMsgTypeConfigOptionChanged notifies that a session's config option changed.
	WSMsgTypeConfigOptionChanged = "config_option_changed"

	// WSMsgTypeSessionChange notifies clients of a first-class session_change timeline event.
	WSMsgTypeSessionChange = "session_change"

	// WSMsgTypeRunnerFallback notifies that the runner fell back to a different type.
	WSMsgTypeRunnerFallback = "runner_fallback"

	// WSMsgTypeMCPToolsAvailable notifies that MCP tools are now available.
	WSMsgTypeMCPToolsAvailable = "mcp_tools_available"
)
