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

// =============================================================================
// Payload typedefs
// =============================================================================
// One `<PascalCase>Payload` typedef per entry in EVENTS, COMMANDS and
// LEGACY_EVENTS, describing the `data` object of that message's envelope.
// Types that carry no `data` are declared as `undefined` so the mapping stays
// total. These are `@typedef`-only — no runtime objects, no validation — so
// they cost nothing at runtime while still giving JS hosts editor completion.
// Field shapes come from the `WSMsgType*` Go doc comments (`Data: {...}`) and
// the envelopes in docs/devel/websockets/protocol-spec.md. `events.test.js`
// asserts the mapping stays total in both directions, so a new message type
// cannot be added without a payload typedef.

// --- Frontend -> backend commands --------------------------------------------

/**
 * Payload of {@link COMMANDS.PROMPT} (`prompt`).
 * @typedef {Object} PromptPayload
 * @property {string} message - User message text.
 * @property {string[]} [image_ids] - IDs of previously uploaded images.
 */

/**
 * Payload of {@link COMMANDS.CANCEL} (`cancel`) — carries no `data`.
 * @typedef {undefined} CancelPayload
 */

/**
 * Payload of {@link COMMANDS.FORCE_RESET} (`force_reset`) — carries no `data`.
 * @typedef {undefined} ForceResetPayload
 */

/**
 * Payload of {@link COMMANDS.RENAME_SESSION} (`rename_session`).
 * @typedef {Object} RenameSessionPayload
 * @property {string} name - New session name.
 */

/**
 * Payload of {@link COMMANDS.LOAD_EVENTS} (`load_events`).
 * `before_seq` and `after_seq` are mutually exclusive.
 * @typedef {Object} LoadEventsPayload
 * @property {number} [limit] - Maximum events to return (server default: 50).
 * @property {number} [before_seq] - Return events with seq < this ("load more").
 * @property {number} [after_seq] - Return events with seq > this (reconnect sync).
 */

/**
 * Payload of {@link COMMANDS.KEEPALIVE} (`keepalive`).
 * @typedef {Object} KeepalivePayload
 * @property {number} client_time - Client clock, Unix milliseconds.
 * @property {number} [last_seen_seq] - Highest seq this client has seen.
 */

/**
 * Payload of {@link COMMANDS.UI_PROMPT_ANSWER} (`ui_prompt_answer`).
 * @typedef {Object} UiPromptAnswerPayload
 * @property {string} request_id - Echo of the `ui_prompt` request ID.
 * @property {string} option_id - ID of the chosen option.
 * @property {string} label - Label of the chosen option.
 */

/**
 * Payload of {@link COMMANDS.ENSURE_RESUMED} (`ensure_resumed`) — carries no `data`.
 * @typedef {undefined} EnsureResumedPayload
 */

/**
 * Payload of {@link COMMANDS.SET_CONFIG_OPTION} (`set_config_option`).
 * @typedef {Object} SetConfigOptionPayload
 * @property {string} config_id - Option identifier (e.g. `"mode"`).
 * @property {string} value - New option value.
 */

/**
 * Payload of {@link COMMANDS.RUN_MCP_INSTALL_COMMAND} (`run_mcp_install_command`).
 * @typedef {Object} RunMcpInstallCommandPayload
 * @property {string} command - Install command to execute.
 */

// --- Connection & session lifecycle ------------------------------------------

/**
 * Payload of {@link EVENTS.CONNECTED} (`connected`).
 * @typedef {Object} ConnectedPayload
 * @property {string} session_id - Session identifier.
 * @property {string} client_id - Unique ID for this WebSocket client.
 * @property {string} acp_server - ACP server name.
 * @property {boolean} is_running - Whether the ACP process is active.
 * @property {boolean} is_prompting - Whether the agent is currently responding.
 * @property {string} last_user_prompt_id - Last prompt ID, for delivery verification.
 * @property {number} last_user_prompt_seq - Last prompt seq, for delivery verification.
 */

/**
 * Payload of {@link EVENTS.SESSION_SWITCHED} (`session_switched`).
 * @typedef {Object} SessionSwitchedPayload
 * @property {string} session_id - Session that is now active.
 */

/**
 * Payload of {@link EVENTS.SESSION_PINNED} (`session_pinned`).
 * @typedef {Object} SessionPinnedPayload
 * @property {string} session_id - Affected session.
 * @property {boolean} pinned - New pinned state.
 */

/**
 * Payload of {@link EVENTS.SESSION_SETTINGS_UPDATED} (`session_settings_updated`).
 * @typedef {Object} SessionSettingsUpdatedPayload
 * @property {string} session_id - Affected session.
 * @property {Object<string, boolean>} settings - Advanced-setting flags by name.
 */

/**
 * Payload of {@link EVENTS.SESSION_CREATED} (`session_created`).
 * @typedef {Object} SessionCreatedPayload
 * @property {string} session_id - New session identifier.
 * @property {string} name - Session name.
 * @property {string} working_dir - Session working directory.
 */

/**
 * Payload of {@link EVENTS.SESSION_ARCHIVED} (`session_archived`).
 * @typedef {Object} SessionArchivedPayload
 * @property {string} session_id - Affected session.
 * @property {boolean} archived - New archived state.
 */

/**
 * Payload of {@link EVENTS.SESSION_ARCHIVE_PENDING} (`session_archive_pending`).
 * Reserved — no server emitter today; see the note on the constant.
 * @typedef {Object} SessionArchivePendingPayload
 * @property {string} session_id - Affected session.
 * @property {boolean} archive_pending - Whether archiving is in progress.
 */

/**
 * Payload of {@link EVENTS.SESSION_DELETED} (`session_deleted`).
 * @typedef {Object} SessionDeletedPayload
 * @property {string} session_id - Deleted session.
 */

/**
 * Payload of {@link EVENTS.SESSION_RENAMED} (`session_renamed`).
 * @typedef {Object} SessionRenamedPayload
 * @property {string} session_id - Affected session.
 * @property {string} name - New session name.
 */

/**
 * Payload of {@link EVENTS.SESSION_BEADS_ISSUE_UPDATED} (`session_beads_issue_updated`).
 * @typedef {Object} SessionBeadsIssueUpdatedPayload
 * @property {string} session_id - Affected session.
 * @property {string} beads_issue - Linked issue ID; empty string means unlinked.
 */

/**
 * Payload of {@link EVENTS.SESSION_RESET} (`session_reset`).
 * @typedef {Object} SessionResetPayload
 * @property {string} session_id - Session that was force-reset.
 */

/**
 * Payload of {@link EVENTS.SESSION_GONE} (`session_gone`). Terminal: the client
 * must stop reconnecting to this session.
 * @typedef {Object} SessionGonePayload
 * @property {string} session_id - Session that no longer exists.
 * @property {string} reason - Human-readable reason.
 */

/**
 * Payload of {@link EVENTS.SESSION_LOADED} (`session_loaded`).
 * @typedef {Object} SessionLoadedPayload
 * @property {string} session_id - Loaded session.
 * @property {Object[]} events - Session timeline events.
 */

/**
 * Payload of {@link EVENTS.SESSION_WAITING} (`session_waiting`).
 * @typedef {Object} SessionWaitingPayload
 * @property {string} session_id - Affected session.
 * @property {boolean} is_waiting - Whether it is blocking on child conversations.
 */

/**
 * Payload of {@link EVENTS.SESSION_STREAMING} (`session_streaming`).
 * @typedef {Object} SessionStreamingPayload
 * @property {string} session_id - Affected session.
 * @property {boolean} is_streaming - Whether the session is streaming output.
 */

/**
 * Payload of {@link EVENTS.SESSION_UI_PROMPT} (`session_ui_prompt`).
 * @typedef {Object} SessionUiPromptPayload
 * @property {string} session_id - Affected session.
 * @property {boolean} is_waiting - Whether a UI-prompt answer is awaited.
 * @property {string} [acked_request_id] - Present only on acknowledgment broadcasts.
 */

/**
 * Payload of {@link EVENTS.SESSION_CHANGE} (`session_change`). Kind-agnostic
 * timeline entry; `label`/`value`/`previous_value`/`items` depend on `kind`.
 * @typedef {Object} SessionChangePayload
 * @property {number} seq - Sequence number of this event.
 * @property {number} max_seq - Highest seq on the server.
 * @property {string} session_id - Affected session.
 * @property {string} kind - Change kind (e.g. `"model_change"`).
 * @property {string} [label] - Display label for the changed field.
 * @property {string} [value] - New value.
 * @property {string} [previous_value] - Prior value.
 * @property {Object[]} [items] - Structured detail entries, when relevant.
 */

/**
 * Payload of {@link EVENTS.CONFIG_OPTION_CHANGED} (`config_option_changed`).
 * @typedef {Object} ConfigOptionChangedPayload
 * @property {string} session_id - Affected session.
 * @property {string} config_id - Option identifier.
 * @property {string} value - New option value.
 */

/**
 * Payload of {@link EVENTS.LOOP_STARTED} (`loop_started`).
 * @typedef {Object} LoopStartedPayload
 * @property {string} session_id - Loop conversation.
 * @property {string} session_name - Loop conversation title.
 */

/**
 * Payload of {@link EVENTS.LOOP_UPDATED} (`loop_updated`).
 * @typedef {Object} LoopUpdatedPayload
 * @property {string} session_id - Loop conversation.
 * @property {string} session_name - Loop conversation title.
 * @property {boolean} loop_enabled - Whether the loop is armed.
 * @property {string} loop_frequency - Schedule description (e.g. `"daily"`).
 * @property {string} next_scheduled_at - Next run, ISO 8601.
 * @property {string[]} [triggers] - Full armed trigger set.
 * @property {string} [trigger] - Back-compat singular trigger that last fired.
 */

// --- Agent output ------------------------------------------------------------

/**
 * Payload of {@link EVENTS.AGENT_MESSAGE} (`agent_message`).
 * @typedef {Object} AgentMessagePayload
 * @property {string} html - Rendered message HTML chunk.
 */

/**
 * Payload of {@link EVENTS.AGENT_THOUGHT} (`agent_thought`).
 * @typedef {Object} AgentThoughtPayload
 * @property {string} text - Reasoning text chunk.
 */

/**
 * Payload of {@link EVENTS.TOOL_CALL} (`tool_call`).
 * @typedef {Object} ToolCallPayload
 * @property {string} id - Tool call identifier.
 * @property {string} title - Human-readable tool call title.
 * @property {string} status - Tool call status.
 */

/**
 * Payload of {@link EVENTS.TOOL_UPDATE} (`tool_update`).
 * @typedef {Object} ToolUpdatePayload
 * @property {string} id - Tool call identifier being updated.
 * @property {string} [status] - New status, when it changed.
 */

/**
 * Payload of {@link EVENTS.PLAN} (`plan`).
 * @typedef {Object} PlanPayload
 * @property {Object} plan - Agent plan document.
 */

/**
 * Payload of {@link EVENTS.ERROR} (`error`).
 * @typedef {Object} ErrorPayload
 * @property {string} message - Error message.
 * @property {string} [code] - Canonical error code, when available.
 */

/**
 * Payload of {@link EVENTS.PROMPT_RECEIVED} (`prompt_received`).
 * @typedef {Object} PromptReceivedPayload
 * @property {string} prompt_id - Server-assigned prompt ID (delivery ACK).
 */

/**
 * Payload of {@link EVENTS.USER_PROMPT} (`user_prompt`).
 * @typedef {Object} UserPromptPayload
 * @property {string} sender_id - Client ID that sent the prompt.
 * @property {string} prompt_id - Prompt identifier.
 * @property {string} message - Prompt text.
 * @property {string[]} image_ids - Attached image IDs.
 */

/**
 * Payload of {@link EVENTS.PROMPT_COMPLETE} (`prompt_complete`).
 * @typedef {Object} PromptCompletePayload
 * @property {number} event_count - Events emitted during this turn.
 */

/**
 * Payload of {@link EVENTS.AGENT_WORKING} (`agent_working`).
 * @typedef {Object} AgentWorkingPayload
 * @property {string} session_id - Active session.
 * @property {number} idle_ms - Milliseconds since the last output.
 * @property {string} [tool_title] - Currently running tool, when known.
 * @property {boolean} is_prompting - Always true while the agent is working.
 */

/**
 * Payload of {@link EVENTS.CONTEXT_USAGE_UPDATE} (`context_usage_update`).
 * @typedef {Object} ContextUsageUpdatePayload
 * @property {string} session_id - Affected session.
 * @property {number} size - Total context window size, in tokens.
 * @property {number} used - Context currently consumed, in tokens.
 */

/**
 * Payload of {@link EVENTS.FILE_READ} (`file_read`).
 * @typedef {Object} FileReadPayload
 * @property {string} path - File path read by the agent.
 * @property {number} size - Bytes read.
 */

/**
 * Payload of {@link EVENTS.FILE_WRITE} (`file_write`).
 * @typedef {Object} FileWritePayload
 * @property {string} path - File path written by the agent.
 * @property {number} size - Bytes written.
 */

/**
 * Payload of {@link EVENTS.AVAILABLE_COMMANDS_UPDATED} (`available_commands_updated`).
 * @typedef {Object} AvailableCommandsUpdatedPayload
 * @property {string} session_id - Affected session.
 * @property {Array<{name: string, description: string, input_hint?: string}>} commands
 *   Slash commands the agent currently advertises.
 */

// --- Event loading & health ---------------------------------------------------

/**
 * Payload of {@link EVENTS.EVENTS_LOADED} (`events_loaded`).
 * @typedef {Object} EventsLoadedPayload
 * @property {Object[]} events - Returned event objects.
 * @property {boolean} has_more - Whether older events remain (pagination).
 * @property {number} first_seq - Lowest seq in `events`.
 * @property {number} last_seq - Highest seq in `events`.
 * @property {number} total_count - Total events in the session.
 * @property {boolean} [prepend] - True when these are older events to prepend.
 */

/**
 * Payload of {@link EVENTS.KEEPALIVE_ACK} (`keepalive_ack`).
 * @typedef {Object} KeepaliveAckPayload
 * @property {number} client_time - Echo of the client clock, for RTT.
 * @property {number} server_time - Server clock, Unix milliseconds.
 * @property {number} max_seq - Highest seq on the server, for gap detection.
 * @property {boolean} is_prompting - Whether the agent is responding.
 * @property {boolean} is_running - Whether the ACP process is active.
 * @property {number} queue_length - Messages waiting in the queue.
 * @property {string} status - Coarse session status.
 */

/**
 * Payload of {@link EVENTS.HOOK_FAILED} (`hook_failed`).
 * @typedef {Object} HookFailedPayload
 * @property {string} name - Processor/hook name.
 * @property {number} exit_code - Process exit code.
 * @property {string} error - Failure detail.
 */

// --- Queue --------------------------------------------------------------------

/**
 * Payload of {@link EVENTS.QUEUE_UPDATED} (`queue_updated`).
 * @typedef {Object} QueueUpdatedPayload
 * @property {number} queue_length - Messages remaining in the queue.
 * @property {string} action - What changed (e.g. `"added"`, `"removed"`).
 * @property {string} message_id - Affected queued message.
 */

/**
 * Payload of {@link EVENTS.QUEUE_MESSAGE_SENDING} (`queue_message_sending`).
 * @typedef {Object} QueueMessageSendingPayload
 * @property {string} message_id - Queued message being dispatched.
 */

/**
 * Payload of {@link EVENTS.QUEUE_MESSAGE_SENT} (`queue_message_sent`).
 * @typedef {Object} QueueMessageSentPayload
 * @property {string} message_id - Queued message that was dispatched.
 */

/**
 * Payload of {@link EVENTS.QUEUE_MESSAGE_TITLED} (`queue_message_titled`).
 * @typedef {Object} QueueMessageTitledPayload
 * @property {string} session_id - Owning session.
 * @property {string} message_id - Queued message that was titled.
 * @property {string} title - Generated title.
 */

/**
 * Payload of {@link EVENTS.QUEUE_REORDERED} (`queue_reordered`).
 * @typedef {Object} QueueReorderedPayload
 * @property {string} session_id - Owning session.
 * @property {Object[]} messages - Queued messages in their new order.
 */

// --- UI prompts & follow-ups --------------------------------------------------

/**
 * Payload of {@link EVENTS.UI_PROMPT} (`ui_prompt`). Unified surface for MCP tool
 * questions, permission requests and follow-up actions.
 * @typedef {Object} UiPromptPayload
 * @property {string} request_id - ID to echo back in `ui_prompt_answer`.
 * @property {string} prompt_type - Prompt kind (e.g. `"permission"`).
 * @property {string} question - Question text.
 * @property {string} title - Prompt title.
 * @property {Array<{id: string, label: string, kind?: string, style?: string}>} options
 *   Selectable answers.
 * @property {number} [timeout_seconds] - Auto-dismiss budget.
 * @property {boolean} [blocking] - Whether the agent is blocked on an answer.
 * @property {string} [tool_call_id] - Originating tool call, when applicable.
 * @property {string} [session_id] - Owning session.
 */

/**
 * Payload of {@link EVENTS.UI_PROMPT_DISMISS} (`ui_prompt_dismiss`).
 * @typedef {Object} UiPromptDismissPayload
 * @property {string} session_id - Owning session.
 * @property {string} request_id - Prompt being dismissed.
 * @property {string} reason - Why it was dismissed.
 */

/**
 * Payload of {@link EVENTS.BACKGROUND_UI_PROMPT_TIMEOUT} (`background_ui_prompt_timeout`).
 * @typedef {Object} BackgroundUiPromptTimeoutPayload
 * @property {string} session_id - Background session whose prompt timed out.
 * @property {string} request_id - Prompt that timed out.
 */

/**
 * Payload of {@link EVENTS.ACTION_BUTTONS} (`action_buttons`).
 * @typedef {Object} ActionButtonsPayload
 * @property {string} session_id - Owning session.
 * @property {Array<{label: string, response: string}>} buttons
 *   Follow-up suggestions to offer the user.
 */

/**
 * Payload of {@link EVENTS.NOTIFICATION} (`notification`).
 * @typedef {Object} NotificationPayload
 * @property {string} session_id - Originating session.
 * @property {string} title - Notification title.
 * @property {string} message - Notification body.
 * @property {string} style - One of `info`, `success`, `warning`, `error`.
 * @property {boolean} sound - Whether to play a sound.
 * @property {boolean} native - Whether to raise a native OS notification.
 * @property {boolean} sticky - Whether the native notification persists.
 */

// --- ACP process lifecycle ----------------------------------------------------

/**
 * Payload of {@link EVENTS.ACP_STARTED} (`acp_started`).
 * @typedef {Object} AcpStartedPayload
 * @property {string} session_id - Session whose ACP process started.
 */

/**
 * Payload of {@link EVENTS.ACP_STOPPED} (`acp_stopped`).
 * @typedef {Object} AcpStoppedPayload
 * @property {string} session_id - Session whose ACP process stopped.
 * @property {string} reason - Why it stopped.
 */

/**
 * Payload of {@link EVENTS.ACP_START_FAILED} (`acp_start_failed`).
 * @typedef {Object} AcpStartFailedPayload
 * @property {string} session_id - Session that failed to start.
 * @property {string} error - Failure detail.
 * @property {string} command - Command that was attempted.
 */

/**
 * Payload of {@link EVENTS.ACP_ERROR_PERMANENT} (`acp_error_permanent`).
 * Non-retryable; carries actionable user guidance.
 * @typedef {Object} AcpErrorPermanentPayload
 * @property {string} session_id - Affected session.
 * @property {string} error - Error detail.
 * @property {string} resolution - Suggested user action.
 * @property {string} command - Command that produced the error.
 */

/**
 * Payload of {@link EVENTS.RUNNER_FALLBACK} (`runner_fallback`).
 * @typedef {Object} RunnerFallbackPayload
 * @property {string} session_id - Affected session.
 * @property {string} requested_type - Runner originally requested.
 * @property {string} fallback_type - Runner actually used.
 * @property {string} reason - Why the fallback happened.
 */

// --- Workspace agent health (global bus) --------------------------------------

/**
 * Payload of {@link EVENTS.MEMORY_RECYCLED} (`memory_recycled`).
 * @typedef {Object} MemoryRecycledPayload
 * @property {string} workspace_uuid - Workspace whose process was recycled.
 * @property {string} workspace_name - Workspace display name.
 * @property {string} working_dir - Workspace working directory.
 * @property {number} rss_bytes - Resident memory at recycle time.
 * @property {number} threshold_bytes - Threshold that was exceeded.
 * @property {number} session_count - Sessions sharing the recycled process.
 */

/**
 * Payload of {@link EVENTS.AGENT_RECYCLED} (`agent_recycled`).
 * @typedef {Object} AgentRecycledPayload
 * @property {string} workspace_uuid - Affected workspace.
 * @property {string} workspace_name - Workspace display name.
 * @property {string} working_dir - Workspace working directory.
 * @property {string} reason - One of `saturated_idle`, `confirmed_degraded`.
 * @property {number} saturation_level - Observed saturation level.
 * @property {number} session_count - Sessions sharing the recycled process.
 */

/**
 * Payload of {@link EVENTS.AGENT_DEGRADED} (`agent_degraded`).
 * @typedef {Object} AgentDegradedPayload
 * @property {string} workspace_uuid - Affected workspace.
 * @property {string} workspace_name - Workspace display name.
 * @property {string} working_dir - Workspace working directory.
 * @property {string} state - One of `process_saturated`, `mcp_init_gated`,
 *   `mcp_init_wedged`, or `""` on recovery.
 * @property {boolean} degraded - Whether the agent is currently degraded.
 */

/**
 * Payload of {@link EVENTS.MCP_INITIALIZING} (`mcp_initializing`).
 * @typedef {Object} McpInitializingPayload
 * @property {string} workspace_uuid - Affected workspace.
 * @property {string} workspace_name - Workspace display name.
 * @property {string} working_dir - Workspace working directory.
 */

/**
 * Payload of {@link EVENTS.MCP_INIT_TIMED_OUT} (`mcp_init_timed_out`).
 * @typedef {Object} McpInitTimedOutPayload
 * @property {string} workspace_uuid - Affected workspace.
 * @property {string} workspace_name - Workspace display name.
 * @property {string} working_dir - Workspace working directory.
 * @property {string[]} mcp_servers - Servers that were still initializing.
 */

/**
 * Payload of {@link EVENTS.PREWARM_PIN_ALERT} (`prewarm_pin_alert`).
 * @typedef {Object} PrewarmPinAlertPayload
 * @property {string} workspace_uuid - Pinned workspace.
 * @property {string} workspace_name - Workspace display name.
 * @property {string} working_dir - Workspace working directory.
 * @property {string} reason - Why the workspace was pinned.
 * @property {boolean} expired - Whether the pin has since expired.
 */

/**
 * Payload of {@link EVENTS.MCP_TOOLS_AVAILABLE} (`mcp_tools_available`).
 * @typedef {Object} McpToolsAvailablePayload
 * @property {string} workspace_uuid - Workspace whose tools were discovered.
 * @property {Object[]} tools - Discovered MCP tool descriptors.
 */

/**
 * Payload of {@link EVENTS.MCP_TOOLS_UNAVAILABLE} (`mcp_tools_unavailable`).
 * @typedef {Object} McpToolsUnavailablePayload
 * @property {string} session_id - Affected session.
 * @property {string} suggested_command - Command that would install the tools.
 * @property {string} suggested_instructions - Guidance to show the user.
 */

/**
 * Payload of {@link EVENTS.REQUIRED_TOOLS_STATUS} (`required_tools_status`).
 * @typedef {Object} RequiredToolsStatusPayload
 * @property {string} workspace_uuid - Affected workspace.
 * @property {Object<string, boolean>} patterns - Availability by tool pattern.
 */

// --- Workspace content changes ------------------------------------------------

/**
 * Payload of {@link EVENTS.PROMPTS_CHANGED} (`prompts_changed`).
 * @typedef {Object} PromptsChangedPayload
 * @property {string[]} changed_dirs - Directories whose prompts changed.
 * @property {string} timestamp - Change time, ISO 8601.
 */

/**
 * Payload of {@link EVENTS.BEADS_CHANGED} (`beads_changed`).
 * @typedef {Object} BeadsChangedPayload
 * @property {string[]} working_dirs - Workspaces affected by the change.
 * @property {string[]} changed_dirs - `.beads/` directories that changed.
 * @property {string} timestamp - Change time, ISO 8601.
 */

/**
 * Payload of {@link EVENTS.BEADS_CLEANUP_PROGRESS} (`beads_cleanup_progress`).
 * @typedef {Object} BeadsCleanupProgressPayload
 * @property {string} working_dir - Workspace being cleaned.
 * @property {number} deleted - Issues deleted so far.
 * @property {number} total - Issues to delete in total.
 * @property {boolean} done - Whether cleanup finished.
 * @property {string} error - Failure detail, empty when successful.
 */

// --- Legacy aliases -----------------------------------------------------------

/**
 * Payload of {@link LEGACY_EVENTS.PERMISSION} (`permission`).
 * @deprecated Superseded by {@link UiPromptPayload}.
 * @typedef {Object} PermissionPayload
 * @property {string} request_id - ID to echo back in `permission_answer`.
 * @property {string} title - Permission title.
 * @property {string} description - Permission detail.
 */

/**
 * Payload of {@link LEGACY_EVENTS.PERMISSION_ANSWER} (`permission_answer`).
 * @deprecated Superseded by {@link UiPromptAnswerPayload}.
 * @typedef {Object} PermissionAnswerPayload
 * @property {string} request_id - Echo of the `permission` request ID.
 * @property {boolean} approved - Whether the user approved.
 */

/**
 * Payload of {@link LEGACY_EVENTS.SYNC_SESSION} (`sync_session`).
 * @deprecated Superseded by {@link LoadEventsPayload}.
 * @typedef {Object} SyncSessionPayload
 * @property {string} session_id - Session to sync.
 * @property {number} after_seq - Return events with seq > this.
 */

/**
 * Payload of {@link LEGACY_EVENTS.SESSION_SYNC} (`session_sync`).
 * @deprecated Superseded by {@link EventsLoadedPayload}.
 * @typedef {Object} SessionSyncPayload
 * @property {string} session_id - Synced session.
 * @property {Object[]} events - Events the client had missed.
 */
