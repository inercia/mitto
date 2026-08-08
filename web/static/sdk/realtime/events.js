/**
 * Typed event surface for the WebSocket protocol (mitto-7gta.16).
 *
 * `EVENTS` names every backend->frontend message type (session socket +
 * global `/api/events` bus); `COMMANDS` names every frontend->backend
 * request type sent on the session socket; `LEGACY_EVENTS` names the four
 * documented-deprecated aliases. All three are frozen, keyed by
 * SCREAMING_SNAKE_CASE name, valued by the wire string.
 *
 * Source of truth: the `WSMsgType*` Go constants in
 * `internal/web/ws_messages.go` and `internal/conversation/ws_events.go` —
 * NOT `docs/devel/websockets/protocol-spec.md`, which lags the code (see the
 * mitto-7gta.16 plan-phase audit: 23 Go types were undocumented, one spec
 * type had no Go emitter). `events.test.js` pins this file against BOTH
 * upstreams so neither can drift silently: (A) every Go constant value must
 * appear here and vice versa, (B) every spec heading must be a known type
 * here and vice versa. Adding a new WSMsgType* constant in Go without
 * updating this file — or vice versa — fails that test.
 *
 * Pure data + two pure predicates. No imports, so this file trivially
 * satisfies the sdk/realtime purity rule (no window/document/localStorage/
 * console) enforced by session-stream.test.js's whole-directory scan.
 */

/**
 * Backend -> frontend message types, sent on the session socket
 * (`/api/sessions/{id}/ws`) and/or the global bus (`/api/events`). See each
 * `WSMsgType*` Go constant's doc comment for the exact `data` payload shape.
 */
export const EVENTS = Object.freeze({
  CONNECTED: "connected",
  SESSION_SWITCHED: "session_switched",
  SESSION_PINNED: "session_pinned",
  SESSION_SETTINGS_UPDATED: "session_settings_updated",
  LOOP_STARTED: "loop_started",
  AGENT_MESSAGE: "agent_message",
  AGENT_THOUGHT: "agent_thought",
  TOOL_CALL: "tool_call",
  TOOL_UPDATE: "tool_update",
  PLAN: "plan",
  ERROR: "error",
  SESSION_LOADED: "session_loaded",
  PROMPT_RECEIVED: "prompt_received",
  USER_PROMPT: "user_prompt",
  PROMPT_COMPLETE: "prompt_complete",
  FILE_WRITE: "file_write",
  FILE_READ: "file_read",
  EVENTS_LOADED: "events_loaded",
  KEEPALIVE_ACK: "keepalive_ack",
  MEMORY_RECYCLED: "memory_recycled",
  AGENT_RECYCLED: "agent_recycled",
  AGENT_DEGRADED: "agent_degraded",
  MCP_INITIALIZING: "mcp_initializing",
  MCP_INIT_TIMED_OUT: "mcp_init_timed_out",
  PREWARM_PIN_ALERT: "prewarm_pin_alert",
  QUEUE_UPDATED: "queue_updated",
  QUEUE_MESSAGE_SENDING: "queue_message_sending",
  QUEUE_MESSAGE_SENT: "queue_message_sent",
  QUEUE_MESSAGE_TITLED: "queue_message_titled",
  QUEUE_REORDERED: "queue_reordered",
  ACTION_BUTTONS: "action_buttons",
  UI_PROMPT: "ui_prompt",
  UI_PROMPT_DISMISS: "ui_prompt_dismiss",
  SESSION_RESET: "session_reset",
  ACP_STOPPED: "acp_stopped",
  ACP_STARTED: "acp_started",
  ACP_START_FAILED: "acp_start_failed",
  SESSION_GONE: "session_gone",
  ACP_ERROR_PERMANENT: "acp_error_permanent",
  AVAILABLE_COMMANDS_UPDATED: "available_commands_updated",
  HOOK_FAILED: "hook_failed",
  PROMPTS_CHANGED: "prompts_changed",
  BEADS_CHANGED: "beads_changed",
  BEADS_CLEANUP_PROGRESS: "beads_cleanup_progress",
  MCP_TOOLS_UNAVAILABLE: "mcp_tools_unavailable",
  NOTIFICATION: "notification",
  REQUIRED_TOOLS_STATUS: "required_tools_status",
  CONTEXT_USAGE_UPDATE: "context_usage_update",
  AGENT_WORKING: "agent_working",
  SESSION_CREATED: "session_created",
  SESSION_ARCHIVED: "session_archived",
  SESSION_DELETED: "session_deleted",
  SESSION_RENAMED: "session_renamed",
  SESSION_BEADS_ISSUE_UPDATED: "session_beads_issue_updated",
  LOOP_UPDATED: "loop_updated",
  SESSION_WAITING: "session_waiting",
  SESSION_STREAMING: "session_streaming",
  SESSION_UI_PROMPT: "session_ui_prompt",
  BACKGROUND_UI_PROMPT_TIMEOUT: "background_ui_prompt_timeout",
  CONFIG_OPTION_CHANGED: "config_option_changed",
  SESSION_CHANGE: "session_change",
  RUNNER_FALLBACK: "runner_fallback",
  MCP_TOOLS_AVAILABLE: "mcp_tools_available",
  /**
   * RESERVED — documented in the protocol spec's archive state diagram and
   * handled by the frontend's global-events switch, but not emitted by any
   * server code path today (see WSMsgTypeSessionArchivePending in
   * internal/web/ws_messages.go). Do not build new host logic around it
   * actually arriving.
   */
  SESSION_ARCHIVE_PENDING: "session_archive_pending",
});

/**
 * Frontend -> backend request types, sent on the session socket
 * (`/api/sessions/{id}/ws`).
 */
export const COMMANDS = Object.freeze({
  PROMPT: "prompt",
  CANCEL: "cancel",
  FORCE_RESET: "force_reset",
  RENAME_SESSION: "rename_session",
  LOAD_EVENTS: "load_events",
  KEEPALIVE: "keepalive",
  UI_PROMPT_ANSWER: "ui_prompt_answer",
  ENSURE_RESUMED: "ensure_resumed",
  SET_CONFIG_OPTION: "set_config_option",
  RUN_MCP_INSTALL_COMMAND: "run_mcp_install_command",
});

/**
 * Deprecated aliases, kept only so hosts reading old persisted events (or
 * talking to an old server) can still recognize them. See
 * docs/devel/websockets/protocol-spec.md "Legacy Messages".
 *   - PERMISSION / PERMISSION_ANSWER -> UI_PROMPT / UI_PROMPT_ANSWER
 *   - SYNC_SESSION -> LOAD_EVENTS (with after_seq)
 *   - SESSION_SYNC -> EVENTS_LOADED
 */
export const LEGACY_EVENTS = Object.freeze({
  PERMISSION: "permission",
  PERMISSION_ANSWER: "permission_answer",
  SYNC_SESSION: "sync_session",
  SESSION_SYNC: "session_sync",
});

/** True if `type` is a known backend->frontend event (current or legacy). */
export function isKnownEventType(type) {
  return Object.values(EVENTS).includes(type) || Object.values(LEGACY_EVENTS).includes(type);
}

/** True if `type` is a known frontend->backend command (current or legacy). */
export function isCommandType(type) {
  return Object.values(COMMANDS).includes(type) || Object.values(LEGACY_EVENTS).includes(type);
}
