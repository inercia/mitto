/** True if `type` is a known backend->frontend event (current or legacy). */
export function isKnownEventType(type: any): boolean;
/** True if `type` is a known frontend->backend command (current or legacy). */
export function isCommandType(type: any): boolean;
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
export const EVENTS: Readonly<{
    CONNECTED: "connected";
    SESSION_SWITCHED: "session_switched";
    SESSION_PINNED: "session_pinned";
    SESSION_SETTINGS_UPDATED: "session_settings_updated";
    LOOP_STARTED: "loop_started";
    AGENT_MESSAGE: "agent_message";
    AGENT_THOUGHT: "agent_thought";
    TOOL_CALL: "tool_call";
    TOOL_UPDATE: "tool_update";
    PLAN: "plan";
    ERROR: "error";
    SESSION_LOADED: "session_loaded";
    PROMPT_RECEIVED: "prompt_received";
    USER_PROMPT: "user_prompt";
    PROMPT_COMPLETE: "prompt_complete";
    FILE_WRITE: "file_write";
    FILE_READ: "file_read";
    EVENTS_LOADED: "events_loaded";
    KEEPALIVE_ACK: "keepalive_ack";
    MEMORY_RECYCLED: "memory_recycled";
    AGENT_RECYCLED: "agent_recycled";
    AGENT_DEGRADED: "agent_degraded";
    MCP_INITIALIZING: "mcp_initializing";
    MCP_INIT_TIMED_OUT: "mcp_init_timed_out";
    PREWARM_PIN_ALERT: "prewarm_pin_alert";
    QUEUE_UPDATED: "queue_updated";
    QUEUE_MESSAGE_SENDING: "queue_message_sending";
    QUEUE_MESSAGE_SENT: "queue_message_sent";
    QUEUE_MESSAGE_TITLED: "queue_message_titled";
    QUEUE_REORDERED: "queue_reordered";
    ACTION_BUTTONS: "action_buttons";
    UI_PROMPT: "ui_prompt";
    UI_PROMPT_DISMISS: "ui_prompt_dismiss";
    SESSION_RESET: "session_reset";
    ACP_STOPPED: "acp_stopped";
    ACP_STARTED: "acp_started";
    ACP_START_FAILED: "acp_start_failed";
    SESSION_GONE: "session_gone";
    ACP_ERROR_PERMANENT: "acp_error_permanent";
    AVAILABLE_COMMANDS_UPDATED: "available_commands_updated";
    HOOK_FAILED: "hook_failed";
    PROMPTS_CHANGED: "prompts_changed";
    TASK_LABEL_COLORS_UPDATED: "task_label_colors_updated";
    BEADS_CHANGED: "beads_changed";
    BEADS_CLEANUP_PROGRESS: "beads_cleanup_progress";
    MCP_TOOLS_UNAVAILABLE: "mcp_tools_unavailable";
    NOTIFICATION: "notification";
    REQUIRED_TOOLS_STATUS: "required_tools_status";
    CONTEXT_USAGE_UPDATE: "context_usage_update";
    AGENT_WORKING: "agent_working";
    SESSION_CREATED: "session_created";
    SESSION_ARCHIVED: "session_archived";
    SESSION_DELETED: "session_deleted";
    SESSION_RENAMED: "session_renamed";
    SESSION_BEADS_ISSUE_UPDATED: "session_beads_issue_updated";
    LOOP_UPDATED: "loop_updated";
    SESSION_WAITING: "session_waiting";
    SESSION_STREAMING: "session_streaming";
    SESSION_UI_PROMPT: "session_ui_prompt";
    BACKGROUND_UI_PROMPT_TIMEOUT: "background_ui_prompt_timeout";
    CONFIG_OPTION_CHANGED: "config_option_changed";
    SESSION_CHANGE: "session_change";
    RUNNER_FALLBACK: "runner_fallback";
    MCP_TOOLS_AVAILABLE: "mcp_tools_available";
    /**
     * Emitted whenever a Slack app's Socket Mode connection status changes
     * (mitto-yn5). Carries the same credential-free `ConnectionStatus` shape
     * returned by `GET /api/slack/connections`.
     */
    SLACK_CONNECTION_STATUS: "slack_connection_status";
    /**
     * RESERVED — documented in the protocol spec's archive state diagram and
     * handled by the frontend's global-events switch, but not emitted by any
     * server code path today (see WSMsgTypeSessionArchivePending in
     * internal/web/ws_messages.go). Do not build new host logic around it
     * actually arriving.
     */
    SESSION_ARCHIVE_PENDING: "session_archive_pending";
}>;
/**
 * Frontend -> backend request types, sent on the session socket
 * (`/api/sessions/{id}/ws`).
 */
export const COMMANDS: Readonly<{
    PROMPT: "prompt";
    CANCEL: "cancel";
    FORCE_RESET: "force_reset";
    RENAME_SESSION: "rename_session";
    LOAD_EVENTS: "load_events";
    KEEPALIVE: "keepalive";
    UI_PROMPT_ANSWER: "ui_prompt_answer";
    ENSURE_RESUMED: "ensure_resumed";
    SET_CONFIG_OPTION: "set_config_option";
    RUN_MCP_INSTALL_COMMAND: "run_mcp_install_command";
}>;
/**
 * Deprecated aliases, kept only so hosts reading old persisted events (or
 * talking to an old server) can still recognize them. See
 * docs/devel/websockets/protocol-spec.md "Legacy Messages".
 *   - PERMISSION / PERMISSION_ANSWER -> UI_PROMPT / UI_PROMPT_ANSWER
 *   - SYNC_SESSION -> LOAD_EVENTS (with after_seq)
 *   - SESSION_SYNC -> EVENTS_LOADED
 */
export const LEGACY_EVENTS: Readonly<{
    PERMISSION: "permission";
    PERMISSION_ANSWER: "permission_answer";
    SYNC_SESSION: "sync_session";
    SESSION_SYNC: "session_sync";
}>;
/**
 * Payload of {@link COMMANDS.PROMPT} (`prompt`).
 */
export type PromptPayload = {
    /**
     * - User message text.
     */
    message: string;
    /**
     * - IDs of previously uploaded images.
     */
    image_ids?: string[];
};
/**
 * Payload of {@link COMMANDS.CANCEL} (`cancel`) — carries no `data`.
 */
export type CancelPayload = undefined;
/**
 * Payload of {@link COMMANDS.FORCE_RESET} (`force_reset`) — carries no `data`.
 */
export type ForceResetPayload = undefined;
/**
 * Payload of {@link COMMANDS.RENAME_SESSION} (`rename_session`).
 */
export type RenameSessionPayload = {
    /**
     * - New session name.
     */
    name: string;
};
/**
 * Payload of {@link COMMANDS.LOAD_EVENTS} (`load_events`).
 * `before_seq` and `after_seq` are mutually exclusive.
 */
export type LoadEventsPayload = {
    /**
     * - Maximum events to return (server default: 50).
     */
    limit?: number;
    /**
     * - Return events with seq < this ("load more").
     */
    before_seq?: number;
    /**
     * - Return events with seq > this (reconnect sync).
     */
    after_seq?: number;
};
/**
 * Payload of {@link COMMANDS.KEEPALIVE} (`keepalive`).
 */
export type KeepalivePayload = {
    /**
     * - Client clock, Unix milliseconds.
     */
    client_time: number;
    /**
     * - Highest seq this client has seen.
     */
    last_seen_seq?: number;
};
/**
 * Payload of {@link COMMANDS.UI_PROMPT_ANSWER} (`ui_prompt_answer`).
 */
export type UiPromptAnswerPayload = {
    /**
     * - Echo of the `ui_prompt` request ID.
     */
    request_id: string;
    /**
     * - ID of the chosen option.
     */
    option_id: string;
    /**
     * - Label of the chosen option.
     */
    label: string;
};
/**
 * Payload of {@link COMMANDS.ENSURE_RESUMED} (`ensure_resumed`) — carries no `data`.
 */
export type EnsureResumedPayload = undefined;
/**
 * Payload of {@link COMMANDS.SET_CONFIG_OPTION} (`set_config_option`).
 */
export type SetConfigOptionPayload = {
    /**
     * - Option identifier (e.g. `"mode"`).
     */
    config_id: string;
    /**
     * - New option value.
     */
    value: string;
};
/**
 * Payload of {@link COMMANDS.RUN_MCP_INSTALL_COMMAND} (`run_mcp_install_command`).
 */
export type RunMcpInstallCommandPayload = {
    /**
     * - Install command to execute.
     */
    command: string;
};
/**
 * Payload of {@link EVENTS.CONNECTED} (`connected`).
 */
export type ConnectedPayload = {
    /**
     * - Session identifier.
     */
    session_id: string;
    /**
     * - Unique ID for this WebSocket client.
     */
    client_id: string;
    /**
     * - ACP server name.
     */
    acp_server: string;
    /**
     * - Whether the ACP process is active.
     */
    is_running: boolean;
    /**
     * - Whether the agent is currently responding.
     */
    is_prompting: boolean;
    /**
     * - Last prompt ID, for delivery verification.
     */
    last_user_prompt_id: string;
    /**
     * - Last prompt seq, for delivery verification.
     */
    last_user_prompt_seq: number;
};
/**
 * Payload of {@link EVENTS.SESSION_SWITCHED} (`session_switched`).
 */
export type SessionSwitchedPayload = {
    /**
     * - Session that is now active.
     */
    session_id: string;
};
/**
 * Payload of {@link EVENTS.SESSION_PINNED} (`session_pinned`).
 */
export type SessionPinnedPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - New pinned state.
     */
    pinned: boolean;
};
/**
 * Payload of {@link EVENTS.SESSION_SETTINGS_UPDATED} (`session_settings_updated`).
 */
export type SessionSettingsUpdatedPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Advanced-setting flags by name.
     */
    settings: {
        [x: string]: boolean;
    };
};
/**
 * Payload of {@link EVENTS.SESSION_CREATED} (`session_created`).
 */
export type SessionCreatedPayload = {
    /**
     * - New session identifier.
     */
    session_id: string;
    /**
     * - Session name.
     */
    name: string;
    /**
     * - Session working directory.
     */
    working_dir: string;
};
/**
 * Payload of {@link EVENTS.SESSION_ARCHIVED} (`session_archived`).
 */
export type SessionArchivedPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - New archived state.
     */
    archived: boolean;
};
/**
 * Payload of {@link EVENTS.SESSION_ARCHIVE_PENDING} (`session_archive_pending`).
 * Reserved — no server emitter today; see the note on the constant.
 */
export type SessionArchivePendingPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Whether archiving is in progress.
     */
    archive_pending: boolean;
};
/**
 * Payload of {@link EVENTS.SESSION_DELETED} (`session_deleted`).
 */
export type SessionDeletedPayload = {
    /**
     * - Deleted session.
     */
    session_id: string;
};
/**
 * Payload of {@link EVENTS.SESSION_RENAMED} (`session_renamed`).
 */
export type SessionRenamedPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - New session name.
     */
    name: string;
};
/**
 * Payload of {@link EVENTS.SESSION_BEADS_ISSUE_UPDATED} (`session_beads_issue_updated`).
 */
export type SessionBeadsIssueUpdatedPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Linked issue ID; empty string means unlinked.
     */
    beads_issue: string;
};
/**
 * Payload of {@link EVENTS.SESSION_RESET} (`session_reset`).
 */
export type SessionResetPayload = {
    /**
     * - Session that was force-reset.
     */
    session_id: string;
};
/**
 * Payload of {@link EVENTS.SESSION_GONE} (`session_gone`). Terminal: the client
 * must stop reconnecting to this session.
 */
export type SessionGonePayload = {
    /**
     * - Session that no longer exists.
     */
    session_id: string;
    /**
     * - Human-readable reason.
     */
    reason: string;
};
/**
 * Payload of {@link EVENTS.SESSION_LOADED} (`session_loaded`).
 */
export type SessionLoadedPayload = {
    /**
     * - Loaded session.
     */
    session_id: string;
    /**
     * - Session timeline events.
     */
    events: any[];
};
/**
 * Payload of {@link EVENTS.SESSION_WAITING} (`session_waiting`).
 */
export type SessionWaitingPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Whether it is blocking on child conversations.
     */
    is_waiting: boolean;
};
/**
 * Payload of {@link EVENTS.SESSION_STREAMING} (`session_streaming`).
 */
export type SessionStreamingPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Whether the session is streaming output.
     */
    is_streaming: boolean;
};
/**
 * Payload of {@link EVENTS.SESSION_UI_PROMPT} (`session_ui_prompt`).
 */
export type SessionUiPromptPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Whether a UI-prompt answer is awaited.
     */
    is_waiting: boolean;
    /**
     * - Present only on acknowledgment broadcasts.
     */
    acked_request_id?: string;
};
/**
 * Payload of {@link EVENTS.SESSION_CHANGE} (`session_change`). Kind-agnostic
 * timeline entry; `label`/`value`/`previous_value`/`items` depend on `kind`.
 */
export type SessionChangePayload = {
    /**
     * - Sequence number of this event.
     */
    seq: number;
    /**
     * - Highest seq on the server.
     */
    max_seq: number;
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Change kind (e.g. `"model_change"`).
     */
    kind: string;
    /**
     * - Display label for the changed field.
     */
    label?: string;
    /**
     * - New value.
     */
    value?: string;
    /**
     * - Prior value.
     */
    previous_value?: string;
    /**
     * - Structured detail entries, when relevant.
     */
    items?: any[];
};
/**
 * Payload of {@link EVENTS.CONFIG_OPTION_CHANGED} (`config_option_changed`).
 */
export type ConfigOptionChangedPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Option identifier.
     */
    config_id: string;
    /**
     * - New option value.
     */
    value: string;
};
/**
 * Payload of {@link EVENTS.LOOP_STARTED} (`loop_started`).
 */
export type LoopStartedPayload = {
    /**
     * - Loop conversation.
     */
    session_id: string;
    /**
     * - Loop conversation title.
     */
    session_name: string;
};
/**
 * Payload of {@link EVENTS.LOOP_UPDATED} (`loop_updated`).
 */
export type LoopUpdatedPayload = {
    /**
     * - Loop conversation.
     */
    session_id: string;
    /**
     * - Whether a loop configuration exists.
     */
    loop_configured: boolean;
    /**
     * - Whether the loop is armed.
     */
    loop_enabled: boolean;
    /**
     * - Authoritative complete loop resource;
     * `null` when the loop was deleted. Uses the same wire shape as the session
     * loop REST endpoint, including prompt, arguments, all trigger-specific
     * settings, limits, counters, timestamps, and stopped-state metadata.
     */
    loop_config: any | null;
    /**
     * - Schedule.
     */
    frequency?: {
        value: number;
        unit: string;
        at?: string;
    };
    /**
     * - Next run, ISO 8601.
     */
    next_scheduled_at?: string;
    /**
     * - Whether each run gets a fresh context.
     */
    fresh_context?: boolean;
    /**
     * - Runs delivered so far.
     */
    iteration_count?: number;
    /**
     * - Maximum runs; zero means unlimited.
     */
    max_iterations?: number;
    /**
     * - Automatic stop reason.
     */
    loop_stopped_reason?: string;
    /**
     * - Dismissed stop reason.
     */
    loop_acknowledged_stopped_reason?: string;
    /**
     * - Full armed trigger set.
     */
    triggers?: string[];
    /**
     * - Back-compat primary trigger.
     */
    trigger?: string;
    /**
     * - Child lifecycle events that fire onChild.
     */
    child_events?: string[];
    /**
     * - onCompletion delay.
     */
    delay_seconds?: number;
    /**
     * - Wall-clock cap; zero is unlimited.
     */
    max_duration_seconds?: number;
    /**
     * - Whether the loop has deliverable content.
     */
    loop_has_prompt?: boolean;
    /**
     * - Free-text prompt preview.
     */
    loop_prompt_preview?: string;
};
/**
 * Payload of {@link EVENTS.AGENT_MESSAGE} (`agent_message`).
 */
export type AgentMessagePayload = {
    /**
     * - Rendered message HTML chunk.
     */
    html: string;
    /**
     * - Same chunk as raw pre-conversion markdown, when available.
     */
    text?: string;
};
/**
 * Payload of {@link EVENTS.AGENT_THOUGHT} (`agent_thought`).
 */
export type AgentThoughtPayload = {
    /**
     * - Reasoning text chunk.
     */
    text: string;
};
/**
 * Payload of {@link EVENTS.TOOL_CALL} (`tool_call`).
 */
export type ToolCallPayload = {
    /**
     * - Tool call identifier.
     */
    id: string;
    /**
     * - Human-readable tool call title.
     */
    title: string;
    /**
     * - Tool call status.
     */
    status: string;
};
/**
 * Payload of {@link EVENTS.TOOL_UPDATE} (`tool_update`).
 */
export type ToolUpdatePayload = {
    /**
     * - Tool call identifier being updated.
     */
    id: string;
    /**
     * - New status, when it changed.
     */
    status?: string;
};
/**
 * Payload of {@link EVENTS.PLAN} (`plan`).
 */
export type PlanPayload = {
    /**
     * - Agent plan document.
     */
    plan: any;
};
/**
 * Payload of {@link EVENTS.ERROR} (`error`).
 */
export type ErrorPayload = {
    /**
     * - Error message.
     */
    message: string;
    /**
     * - Canonical error code, when available.
     */
    code?: string;
};
/**
 * Payload of {@link EVENTS.PROMPT_RECEIVED} (`prompt_received`).
 */
export type PromptReceivedPayload = {
    /**
     * - Server-assigned prompt ID (delivery ACK).
     */
    prompt_id: string;
};
/**
 * Payload of {@link EVENTS.USER_PROMPT} (`user_prompt`).
 */
export type UserPromptPayload = {
    /**
     * - Client ID that sent the prompt.
     */
    sender_id: string;
    /**
     * - Prompt identifier.
     */
    prompt_id: string;
    /**
     * - Prompt text.
     */
    message: string;
    /**
     * - Attached image IDs.
     */
    image_ids: string[];
};
/**
 * Payload of {@link EVENTS.PROMPT_COMPLETE} (`prompt_complete`).
 */
export type PromptCompletePayload = {
    /**
     * - Events emitted during this turn.
     */
    event_count: number;
};
/**
 * Payload of {@link EVENTS.AGENT_WORKING} (`agent_working`).
 */
export type AgentWorkingPayload = {
    /**
     * - Active session.
     */
    session_id: string;
    /**
     * - Milliseconds since the last output.
     */
    idle_ms: number;
    /**
     * - Currently running tool, when known.
     */
    tool_title?: string;
    /**
     * - Always true while the agent is working.
     */
    is_prompting: boolean;
};
/**
 * Payload of {@link EVENTS.CONTEXT_USAGE_UPDATE} (`context_usage_update`).
 */
export type ContextUsageUpdatePayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Total context window size, in tokens.
     */
    size: number;
    /**
     * - Context currently consumed, in tokens.
     */
    used: number;
};
/**
 * Payload of {@link EVENTS.FILE_READ} (`file_read`).
 */
export type FileReadPayload = {
    /**
     * - File path read by the agent.
     */
    path: string;
    /**
     * - Bytes read.
     */
    size: number;
};
/**
 * Payload of {@link EVENTS.FILE_WRITE} (`file_write`).
 */
export type FileWritePayload = {
    /**
     * - File path written by the agent.
     */
    path: string;
    /**
     * - Bytes written.
     */
    size: number;
};
/**
 * Payload of {@link EVENTS.AVAILABLE_COMMANDS_UPDATED} (`available_commands_updated`).
 */
export type AvailableCommandsUpdatedPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     *   Slash commands the agent currently advertises.
     */
    commands: Array<{
        name: string;
        description: string;
        input_hint?: string;
    }>;
};
/**
 * Payload of {@link EVENTS.EVENTS_LOADED} (`events_loaded`).
 */
export type EventsLoadedPayload = {
    /**
     * - Returned event objects.
     */
    events: any[];
    /**
     * - Whether older events remain (pagination).
     */
    has_more: boolean;
    /**
     * - Lowest seq in `events`.
     */
    first_seq: number;
    /**
     * - Highest seq in `events`.
     */
    last_seq: number;
    /**
     * - Total events in the session.
     */
    total_count: number;
    /**
     * - True when these are older events to prepend.
     */
    prepend?: boolean;
};
/**
 * Payload of {@link EVENTS.KEEPALIVE_ACK} (`keepalive_ack`).
 */
export type KeepaliveAckPayload = {
    /**
     * - Echo of the client clock, for RTT.
     */
    client_time: number;
    /**
     * - Server clock, Unix milliseconds.
     */
    server_time: number;
    /**
     * - Highest seq on the server, for gap detection.
     */
    max_seq: number;
    /**
     * - Whether the agent is responding.
     */
    is_prompting: boolean;
    /**
     * - Whether the ACP process is active.
     */
    is_running: boolean;
    /**
     * - Messages waiting in the queue.
     */
    queue_length: number;
    /**
     * - Coarse session status.
     */
    status: string;
};
/**
 * Payload of {@link EVENTS.HOOK_FAILED} (`hook_failed`).
 */
export type HookFailedPayload = {
    /**
     * - Processor/hook name.
     */
    name: string;
    /**
     * - Process exit code.
     */
    exit_code: number;
    /**
     * - Failure detail.
     */
    error: string;
};
/**
 * Payload of {@link EVENTS.QUEUE_UPDATED} (`queue_updated`).
 */
export type QueueUpdatedPayload = {
    /**
     * - Messages remaining in the queue.
     */
    queue_length: number;
    /**
     * - What changed (e.g. `"added"`, `"removed"`).
     */
    action: string;
    /**
     * - Affected queued message.
     */
    message_id: string;
};
/**
 * Payload of {@link EVENTS.QUEUE_MESSAGE_SENDING} (`queue_message_sending`).
 */
export type QueueMessageSendingPayload = {
    /**
     * - Queued message being dispatched.
     */
    message_id: string;
};
/**
 * Payload of {@link EVENTS.QUEUE_MESSAGE_SENT} (`queue_message_sent`).
 */
export type QueueMessageSentPayload = {
    /**
     * - Queued message that was dispatched.
     */
    message_id: string;
};
/**
 * Payload of {@link EVENTS.QUEUE_MESSAGE_TITLED} (`queue_message_titled`).
 */
export type QueueMessageTitledPayload = {
    /**
     * - Owning session.
     */
    session_id: string;
    /**
     * - Queued message that was titled.
     */
    message_id: string;
    /**
     * - Generated title.
     */
    title: string;
};
/**
 * Payload of {@link EVENTS.QUEUE_REORDERED} (`queue_reordered`).
 */
export type QueueReorderedPayload = {
    /**
     * - Owning session.
     */
    session_id: string;
    /**
     * - Queued messages in their new order.
     */
    messages: any[];
};
/**
 * Payload of {@link EVENTS.UI_PROMPT} (`ui_prompt`). Unified surface for MCP tool
 * questions, permission requests and follow-up actions.
 */
export type UiPromptPayload = {
    /**
     * - ID to echo back in `ui_prompt_answer`.
     */
    request_id: string;
    /**
     * - Prompt kind (e.g. `"permission"`).
     */
    prompt_type: string;
    /**
     * - Question text.
     */
    question: string;
    /**
     * - Prompt title.
     */
    title: string;
    /**
     *   Selectable answers.
     */
    options: Array<{
        id: string;
        label: string;
        kind?: string;
        style?: string;
    }>;
    /**
     * - Auto-dismiss budget.
     */
    timeout_seconds?: number;
    /**
     * - Whether the agent is blocked on an answer.
     */
    blocking?: boolean;
    /**
     * - Originating tool call, when applicable.
     */
    tool_call_id?: string;
    /**
     * - Owning session.
     */
    session_id?: string;
};
/**
 * Payload of {@link EVENTS.UI_PROMPT_DISMISS} (`ui_prompt_dismiss`).
 */
export type UiPromptDismissPayload = {
    /**
     * - Owning session.
     */
    session_id: string;
    /**
     * - Prompt being dismissed.
     */
    request_id: string;
    /**
     * - Why it was dismissed.
     */
    reason: string;
};
/**
 * Payload of {@link EVENTS.BACKGROUND_UI_PROMPT_TIMEOUT} (`background_ui_prompt_timeout`).
 */
export type BackgroundUiPromptTimeoutPayload = {
    /**
     * - Background session whose prompt timed out.
     */
    session_id: string;
    /**
     * - Prompt that timed out.
     */
    request_id: string;
};
/**
 * Payload of {@link EVENTS.ACTION_BUTTONS} (`action_buttons`).
 */
export type ActionButtonsPayload = {
    /**
     * - Owning session.
     */
    session_id: string;
    /**
     *   Follow-up suggestions to offer the user.
     */
    buttons: Array<{
        label: string;
        response: string;
    }>;
};
/**
 * Payload of {@link EVENTS.NOTIFICATION} (`notification`).
 */
export type NotificationPayload = {
    /**
     * - Originating session.
     */
    session_id: string;
    /**
     * - Notification title.
     */
    title: string;
    /**
     * - Notification body.
     */
    message: string;
    /**
     * - One of `info`, `success`, `warning`, `error`.
     */
    style: string;
    /**
     * - Whether to play a sound.
     */
    sound: boolean;
    /**
     * - Whether to raise a native OS notification.
     */
    native: boolean;
    /**
     * - Whether the native notification persists.
     */
    sticky: boolean;
};
/**
 * Payload of {@link EVENTS.ACP_STARTED} (`acp_started`).
 */
export type AcpStartedPayload = {
    /**
     * - Session whose ACP process started.
     */
    session_id: string;
};
/**
 * Payload of {@link EVENTS.ACP_STOPPED} (`acp_stopped`).
 */
export type AcpStoppedPayload = {
    /**
     * - Session whose ACP process stopped.
     */
    session_id: string;
    /**
     * - Why it stopped.
     */
    reason: string;
};
/**
 * Payload of {@link EVENTS.ACP_START_FAILED} (`acp_start_failed`).
 */
export type AcpStartFailedPayload = {
    /**
     * - Session that failed to start.
     */
    session_id: string;
    /**
     * - Failure detail.
     */
    error: string;
    /**
     * - Command that was attempted.
     */
    command: string;
};
/**
 * Payload of {@link EVENTS.ACP_ERROR_PERMANENT} (`acp_error_permanent`).
 * Non-retryable; carries actionable user guidance.
 */
export type AcpErrorPermanentPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Error detail.
     */
    error: string;
    /**
     * - Suggested user action.
     */
    resolution: string;
    /**
     * - Command that produced the error.
     */
    command: string;
};
/**
 * Payload of {@link EVENTS.RUNNER_FALLBACK} (`runner_fallback`).
 */
export type RunnerFallbackPayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Runner originally requested.
     */
    requested_type: string;
    /**
     * - Runner actually used.
     */
    fallback_type: string;
    /**
     * - Why the fallback happened.
     */
    reason: string;
};
/**
 * Payload of {@link EVENTS.MEMORY_RECYCLED} (`memory_recycled`).
 */
export type MemoryRecycledPayload = {
    /**
     * - Workspace whose process was recycled.
     */
    workspace_uuid: string;
    /**
     * - Workspace display name.
     */
    workspace_name: string;
    /**
     * - Workspace working directory.
     */
    working_dir: string;
    /**
     * - Resident memory at recycle time.
     */
    rss_bytes: number;
    /**
     * - Threshold that was exceeded.
     */
    threshold_bytes: number;
    /**
     * - Sessions sharing the recycled process.
     */
    session_count: number;
};
/**
 * Payload of {@link EVENTS.AGENT_RECYCLED} (`agent_recycled`).
 */
export type AgentRecycledPayload = {
    /**
     * - Affected workspace.
     */
    workspace_uuid: string;
    /**
     * - Workspace display name.
     */
    workspace_name: string;
    /**
     * - Workspace working directory.
     */
    working_dir: string;
    /**
     * - One of `saturated_idle`, `confirmed_degraded`.
     */
    reason: string;
    /**
     * - Observed saturation level.
     */
    saturation_level: number;
    /**
     * - Sessions sharing the recycled process.
     */
    session_count: number;
};
/**
 * Payload of {@link EVENTS.AGENT_DEGRADED} (`agent_degraded`).
 */
export type AgentDegradedPayload = {
    /**
     * - Affected workspace.
     */
    workspace_uuid: string;
    /**
     * - Workspace display name.
     */
    workspace_name: string;
    /**
     * - Workspace working directory.
     */
    working_dir: string;
    /**
     * - One of `process_saturated`, `mcp_init_gated`,
     * `mcp_init_wedged`, or `""` on recovery.
     */
    state: string;
    /**
     * - Whether the agent is currently degraded.
     */
    degraded: boolean;
};
/**
 * Payload of {@link EVENTS.MCP_INITIALIZING} (`mcp_initializing`).
 */
export type McpInitializingPayload = {
    /**
     * - Affected workspace.
     */
    workspace_uuid: string;
    /**
     * - Workspace display name.
     */
    workspace_name: string;
    /**
     * - Workspace working directory.
     */
    working_dir: string;
};
/**
 * Payload of {@link EVENTS.MCP_INIT_TIMED_OUT} (`mcp_init_timed_out`).
 */
export type McpInitTimedOutPayload = {
    /**
     * - Affected workspace.
     */
    workspace_uuid: string;
    /**
     * - Workspace display name.
     */
    workspace_name: string;
    /**
     * - Workspace working directory.
     */
    working_dir: string;
    /**
     * - Servers that were still initializing.
     */
    mcp_servers: string[];
};
/**
 * Payload of {@link EVENTS.PREWARM_PIN_ALERT} (`prewarm_pin_alert`).
 */
export type PrewarmPinAlertPayload = {
    /**
     * - Pinned workspace.
     */
    workspace_uuid: string;
    /**
     * - Workspace display name.
     */
    workspace_name: string;
    /**
     * - Workspace working directory.
     */
    working_dir: string;
    /**
     * - Why the workspace was pinned.
     */
    reason: string;
    /**
     * - Whether the pin has since expired.
     */
    expired: boolean;
};
/**
 * Payload of {@link EVENTS.MCP_TOOLS_AVAILABLE} (`mcp_tools_available`).
 */
export type McpToolsAvailablePayload = {
    /**
     * - Workspace whose tools were discovered.
     */
    workspace_uuid: string;
    /**
     * - Discovered MCP tool descriptors.
     */
    tools: any[];
};
/**
 * Payload of {@link EVENTS.SLACK_CONNECTION_STATUS} (`slack_connection_status`).
 * Credential-free: no tokens or message content. Mirrors the Go
 * `slackbridge.ConnectionStatus` struct.
 */
export type SlackConnectionStatusPayload = {
    /**
     * - Slack app profile this status belongs to.
     */
    app_id: string;
    /**
     * - Connection state (e.g. `"connected"`, `"backoff"`).
     */
    state: string;
    /**
     * - Active onSlack subscriptions referencing this app.
     */
    subscription_count: number;
    /**
     * - Total Events API envelopes received.
     */
    events_api_received: number;
    /**
     * - Envelopes accepted for dispatch.
     */
    accepted_count: number;
    /**
     * - Envelopes ignored (no matching subscription).
     */
    ignored_count: number;
    /**
     * - When the current connection was established, ISO 8601.
     */
    connected_at?: string;
    /**
     * - Last envelope received, ISO 8601.
     */
    last_envelope_at?: string;
    /**
     * - Failure classification, when in backoff.
     */
    error_class?: string;
};
/**
 * Payload of {@link EVENTS.MCP_TOOLS_UNAVAILABLE} (`mcp_tools_unavailable`).
 */
export type McpToolsUnavailablePayload = {
    /**
     * - Affected session.
     */
    session_id: string;
    /**
     * - Command that would install the tools.
     */
    suggested_command: string;
    /**
     * - Guidance to show the user.
     */
    suggested_instructions: string;
};
/**
 * Payload of {@link EVENTS.REQUIRED_TOOLS_STATUS} (`required_tools_status`).
 */
export type RequiredToolsStatusPayload = {
    /**
     * - Affected workspace.
     */
    workspace_uuid: string;
    /**
     * - Availability by tool pattern.
     */
    patterns: {
        [x: string]: boolean;
    };
};
/**
 * Payload of {@link EVENTS.TASK_LABEL_COLORS_UPDATED}
 * (`task_label_colors_updated`). The empty object signals clients to refetch.
 */
export type TaskLabelColorsUpdatedPayload = any;
/**
 * Payload of {@link EVENTS.PROMPTS_CHANGED} (`prompts_changed`).
 */
export type PromptsChangedPayload = {
    /**
     * - Directories whose prompts changed.
     */
    changed_dirs: string[];
    /**
     * - Change time, ISO 8601.
     */
    timestamp: string;
};
/**
 * Payload of {@link EVENTS.BEADS_CHANGED} (`beads_changed`).
 */
export type BeadsChangedPayload = {
    /**
     * - Workspaces affected by the change.
     */
    working_dirs: string[];
    /**
     * - `.beads/` directories that changed.
     */
    changed_dirs: string[];
    /**
     * - Change time, ISO 8601.
     */
    timestamp: string;
};
/**
 * Payload of {@link EVENTS.BEADS_CLEANUP_PROGRESS} (`beads_cleanup_progress`).
 */
export type BeadsCleanupProgressPayload = {
    /**
     * - Workspace being cleaned.
     */
    working_dir: string;
    /**
     * - Issues deleted so far.
     */
    deleted: number;
    /**
     * - Issues to delete in total.
     */
    total: number;
    /**
     * - Whether cleanup finished.
     */
    done: boolean;
    /**
     * - Failure detail, empty when successful.
     */
    error: string;
};
/**
 * Payload of {@link LEGACY_EVENTS.PERMISSION} (`permission`).
 */
export type PermissionPayload = {
    /**
     * - ID to echo back in `permission_answer`.
     */
    request_id: string;
    /**
     * - Permission title.
     */
    title: string;
    /**
     * - Permission detail.
     */
    description: string;
};
/**
 * Payload of {@link LEGACY_EVENTS.PERMISSION_ANSWER} (`permission_answer`).
 */
export type PermissionAnswerPayload = {
    /**
     * - Echo of the `permission` request ID.
     */
    request_id: string;
    /**
     * - Whether the user approved.
     */
    approved: boolean;
};
/**
 * Payload of {@link LEGACY_EVENTS.SYNC_SESSION} (`sync_session`).
 */
export type SyncSessionPayload = {
    /**
     * - Session to sync.
     */
    session_id: string;
    /**
     * - Return events with seq > this.
     */
    after_seq: number;
};
/**
 * Payload of {@link LEGACY_EVENTS.SESSION_SYNC} (`session_sync`).
 */
export type SessionSyncPayload = {
    /**
     * - Synced session.
     */
    session_id: string;
    /**
     * - Events the client had missed.
     */
    events: any[];
};
