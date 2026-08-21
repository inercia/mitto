// tool_registration.go: MCP tool registration wiring for the Mitto MCP server.
// Contains registerGlobalTools and registerSessionScopedTools, plus the shared
// selfIDNote constant referenced by nearly every session-scoped tool description.
// Extracted from server.go for maintainability (mitto-90f.2).
package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerGlobalTools registers global MCP tools (always available, no session context needed).
func (s *Server) registerGlobalTools(mcpSrv *mcp.Server, deps Dependencies) {
	// mitto_list_conversations tool - always available (no permission check)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_list",
		Description: "List all existing Mitto conversations with metadata including title, dates, message count, prompting status, last sequence, and session folder. " +
			"Use this to find conversation IDs for other tools like 'mitto_conversation_get' or 'mitto_conversation_send_prompt'. " +
			"To CREATE a new conversation, use 'mitto_conversation_new' instead. Always available. " +
			"All parameters are optional filters — omit them to list all conversations. " +
			"Optionally filter by workspace UUID using the 'workspace' parameter to list only conversations in a specific workspace. " +
			"Optionally provide 'self_id' for permission-aware listing: without it, all conversations are returned (backward compatible); " +
			"with 'self_id' but without the 'Can interact with other workspaces' flag, only the caller's own workspace conversations are returned. " +
			selfIDNote,
	}, s.createListConversationsHandler(deps.SessionManager))

	// mitto_get_config tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "mitto_get_config",
		Description: "Get the current effective Mitto configuration",
	}, s.createGetConfigHandler())

	// mitto_get_runtime_info tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "mitto_get_runtime_info",
		Description: "Get runtime information including OS, architecture, log file paths, data directories, and process info",
	}, s.createGetRuntimeInfoHandler())

	// mitto_coldstart_recent tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "mitto_coldstart_recent",
		Description: "Return the most recent cold-start diagnostic summaries (phase timeline + durations) captured by the cold-start tracer (mitto-3mv). Useful for post-hoc analysis of cold-start latency without grepping logs. Pass by_workspace=true to also receive a per-workspace rollup (total, failures, failure rate, p50/p95, last outcome) sorted by failure rate descending.",
	}, s.createColdStartRecentHandler())

	// mitto_goroutine_gauge_recent tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "mitto_goroutine_gauge_recent",
		Description: "Return the most recent periodic goroutine gauge samples (mitto-x3x), newest first. Each sample carries the raw goroutine total plus per-category attribution (live ACP processes, connected WebSocket clients, open MCP SSE keepalive streams), sampled independently of cold-start frequency. Use this to answer 'is the goroutine count ratcheting?' without grepping logs or restarting for pprof — see docs/devel/web-interface.md 'Triaging goroutine counts'.",
	}, s.createGoroutineGaugeRecentHandler())

	// mitto_beads_cache_metrics tool - registered only when the beads read
	// cache is enabled (--beads-cache flag). Nil callback means the cache is
	// off in this process, so we skip registration to avoid a tool that would
	// always report zeroes.
	if deps.BeadsCacheMetrics != nil {
		mcp.AddTool(mcpSrv, &mcp.Tool{
			Name:        "mitto_beads_cache_metrics",
			Description: "Return a point-in-time snapshot of the beads read-cache counters (hits/misses/invalidations by reason/singleflight-shared/entries-current). Only registered when --beads-cache is enabled (mitto-is2).",
		}, s.createBeadsCacheMetricsHandler())
	}

	// mitto_workspace_list tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_workspace_list",
		Description: "List all configured workspaces with their settings and metadata. " +
			"Returns workspace UUID, display name, working directory, ACP server, " +
			"an is_default flag (the preferred workspace for its folder when several share the same directory), " +
			"and optional metadata from the workspace .mittorc file (description, URL, group, user data schema). " +
			"Optionally filter by activity: 'active' returns only workspaces with at least one non-archived conversation, " +
			"'archived' returns only workspaces where all conversations are archived (excludes workspaces with zero conversations). " +
			"Omit filter to return all workspaces. Always available.",
	}, s.createListWorkspacesHandler())

	// mitto_workspace_update tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_workspace_update",
		Description: "Update a workspace's .mittorc configuration: the descriptive metadata (description, url, group) " +
			"and/or the user_data schema (the field definitions for per-conversation user data). " +
			"Supports partial updates — only provided fields change. For description/url/group, omit a field to leave it unchanged, " +
			"or pass an empty string to clear it. " +
			"user_data_schema is a list of {name, description, type} where type is one of 'string' (default), 'url', or 'filename'. " +
			"Set user_data_schema_merge to true (default) to merge fields by name with the existing schema, " +
			"or false to replace the whole schema (an empty list clears it). " +
			"By default targets the caller's own workspace; specify 'workspace' (a UUID from mitto_workspace_list) " +
			"to target another workspace (requires the 'Can interact with other workspaces' flag and user confirmation). " +
			"Note: .mittorc is a version-controlled file in the workspace root. " +
			selfIDNote,
	}, s.handleWorkspaceUpdate)
}

// selfIDNote is the standard note about self_id for tools that require session identification.
// For ACP-routed agents (like Auggie), the self_id is automatically correlated via the ACP layer,
// so any stable value works. Uncorrelated external MCP clients cannot impersonate a conversation
// by supplying its registered session ID.
const selfIDNote = "The self_id parameter identifies YOUR current session (not the target conversation). " +
	"If your session_id was already provided in the conversation context (e.g., in a '[Session Context]' block), use that value directly — " +
	"do NOT call 'mitto_conversation_get_current' first. " +
	"Only call 'mitto_conversation_get_current' if you do not already know your session_id."

// registerSessionScopedTools registers session-scoped MCP tools.
// These tools operate on specific conversations using automatic session detection via session_id correlation.
// Permission checks are done at execution time based on the session's flags.
func (s *Server) registerSessionScopedTools(mcpSrv *mcp.Server) {
	// mitto_get_current_session - Get info about the current session (auto-detected via session_id)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_get_current",
		Description: "Get information about YOUR current conversation/session, including your real session ID, title, working directory, and message count. " +
			"Only call this if you do NOT already know your session_id (e.g., it was not provided as part of the prompt). " +
			"You can pass any value for self_id (e.g., 'init') - this tool auto-detects your session and returns the real session_id. " +
			selfIDNote,
	}, s.handleGetCurrentSession)

	// mitto_conversation_send_prompt - Send a prompt to another conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_send_prompt",
		Description: "Send a message/prompt to an EXISTING conversation (identified by conversation_id). " +
			"The prompt is added to that conversation's queue and will be processed when the target agent becomes idle. " +
			"Use 'mitto_conversation_list' first to find existing conversation IDs, or use an ID returned by 'mitto_conversation_new'. " +
			"To enqueue on YOUR OWN conversation (self-dispatch, e.g. queueing your next phase), pass \"self\" (or your own conversation ID) as conversation_id. " +
			"Optionally specify a 'workspace' UUID when sending to a conversation in a different workspace (requires user confirmation). " +
			"Optionally provide a 'schedule_time' parameter (ISO 8601 / RFC 3339 timestamp) to schedule the message for future delivery instead of immediate processing. " +
			"Supports both absolute timestamps (e.g., '2024-01-15T10:30:00Z') and relative durations from now (e.g., '5m', '1h', '2h30m'). " +
			"Optionally provide an 'arguments' map (string keys to string values) to fill Go-template placeholders in the prompt text when it is sent: a '.Args.VAR' field is replaced with the value (or empty string if absent), and the Arg helper with a default uses the value when set and non-empty, otherwise the default. " +
			"Optionally provide 'prompt_name' to enqueue a predefined workspace prompt by name instead of free text; the name is resolved to its full body at dispatch in the TARGET conversation's context. Provide either 'prompt' (free text) or 'prompt_name'. " +
			"Requires 'Can Send Prompt' flag to be enabled. " +
			selfIDNote,
	}, s.handleSendPromptToConversation)

	// mitto_ui_options - Unified options menu
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_options",
		Description: "Present a list of options to the user as an expandable menu and wait for their selection. " +
			"Each option can have a short label and an optional longer description. " +
			"Option labels should be short (max 80 characters) and descriptions concise (max 200 characters); longer values will be truncated. " +
			"The question text must be concise (max 500 characters); if you need to provide detailed context, print it as a regular message first. " +
			"Optionally allows the user to type free text instead of selecting a predefined option. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleUIOptions)

	// mitto_ui_textbox - Present editable text to user
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_textbox",
		Description: "Present a text editing dialog to the user and wait for their changes. " +
			"Shows a modal with a title and a large editable textarea pre-filled with the provided text. " +
			"The user can edit the text and submit, or abort if allowed. " +
			"Returns the edited text (or a unified diff of changes). " +
			"Text is limited to 16KB. For short-to-medium text snippets only, not full files. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleUITextbox)

	// mitto_ui_form - Present a sanitized HTML form to the user
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_form",
		Description: "Present an HTML form to the user and wait for their submission. " +
			"Provide simple HTML with form elements (input, select, textarea, checkbox, radio) and labels. " +
			"The HTML is strictly sanitized — only form-related elements are allowed (no scripts, styles, " +
			"images, links, or event handlers). Submit/cancel buttons are added automatically. " +
			"Returns the submitted form field values as key-value pairs (keyed by the 'name' attribute). " +
			"For radio/checkbox groups, put the question in its own block element (e.g. a <p>, or a " +
			"<fieldset> with a <legend>) and wrap EACH option in its own <label> so every option — " +
			"including the first — renders on its own line. Example: " +
			"<p>Pick one:</p>" +
			"<label><input type='radio' name='q' value='a' checked> Option A</label>" +
			"<label><input type='radio' name='q' value='b'> Option B</label>. " +
			"Do NOT place the question and the first option in the same line/element, and do NOT " +
			"separate bare <input> options with <br> (the first option will render glued to the question). " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleUIForm)

	// mitto_ui_notify - Send a non-blocking notification to the user
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_notify",
		Description: "Send a notification to the user. Unlike other UI tools, this is non-blocking — " +
			"it sends the notification and returns immediately without waiting for user interaction. " +
			"Useful for informing the user about progress, completion, errors, or other events. " +
			"style can be: 'info' (default, blue), 'success' (green), 'warning' (amber), 'error' (red). " +
			"native=true shows a native OS notification (macOS only) in addition to the in-app toast. " +
			"sound=true plays a notification sound. " +
			"sticky=true keeps the native notification in Notification Center until the user dismisses it (default: false, auto-removes after 5s). " +
			"beads_issue is an optional bead ID (e.g. 'mitto-abc') — when set, clicking the toast opens the beads viewer for that issue (takes precedence over session-focus fallback; no-op if the frontend cannot resolve the id). " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleUINotify)

	// mitto_workspace_ui_notify - Workspace-scoped fire-and-forget notification.
	// Targets a workspace UUID rather than a registered session, so callers
	// running in contexts without a live MCP session — notably auxiliary
	// sessions executing close-phase (conversationClosed) processors — can
	// still surface toasts to the user (mitto-6bn).
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_workspace_ui_notify",
		Description: "Send a workspace-scoped notification to the user. Like mitto_ui_notify, this is non-blocking " +
			"— it sends the notification and returns immediately without waiting for user interaction. " +
			"Unlike mitto_ui_notify (which requires a live registered session), this tool targets a workspace by UUID " +
			"and broadcasts to all connected clients; the frontend filters by workspace so only users currently viewing " +
			"the matching workspace see the toast. Intended for callers that lack a live registered session — notably " +
			"auxiliary sessions running close-phase (conversationClosed) processors. " +
			"'workspace_uuid' is required (obtain it from mitto_workspace_list). " +
			"style can be: 'info' (default, blue), 'success' (green), 'warning' (amber), 'error' (red). " +
			"native=true shows a native OS notification (macOS only) in addition to the in-app toast. " +
			"sound=true plays a notification sound. " +
			"sticky=true keeps the native notification in Notification Center until the user dismisses it (default: false, auto-removes after 5s). " +
			"beads_issue is an optional bead ID (e.g. 'mitto-abc') — when set, clicking the toast opens the beads viewer for that issue (takes precedence over session-focus fallback; no-op if the frontend cannot resolve the id). " +
			"Requires 'Can prompt user' flag to be enabled on the caller session when the caller has a registered session; " +
			"unregistered callers (auxiliary sessions) are allowed as long as they supply a valid workspace_uuid. " +
			selfIDNote,
	}, s.handleWorkspaceUINotify)

	// mitto_conversation_new - Start a new conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_new",
		Description: "USE THIS TOOL TO CREATE A NEW CONVERSATION - no browser or UI interaction needed! " +
			"This tool programmatically creates and starts a NEW agent conversation that runs in parallel with your current session. " +
			"When a user asks you to 'create a new conversation', 'start a new session', or 'investigate something in a new conversation', " +
			"call this tool directly instead of trying to click buttons or navigate a UI. " +
			"This spawns a separate AI agent that can work independently on the task you specify. " +
			"Use this to delegate work, run background tasks, or parallelize complex work across multiple agents. " +
			"The new conversation inherits your workspace configuration. By default it also inherits your ACP server, " +
			"but you can specify a different one via the optional 'acp_server' parameter (must have a workspace configured for the current folder) " +
			"(use 'mitto_conversation_get_current' to see available ACP servers in the 'available_acp_servers' field). " +
			"Optionally provide a 'title' for the conversation and an 'initial_prompt' to start the agent working immediately. " +
			"Instead of an inline 'initial_prompt', you may provide 'prompt_name' to use a predefined prompt by name (resolved the same way as 'mitto_prompt_get', case-insensitive) as the initial prompt — 'prompt_name' and 'initial_prompt' are mutually exclusive. " +
			"Optionally provide an 'arguments' map (string keys to string values) to fill Go-template placeholders in the initial prompt when it is sent: a '.Args.VAR' field is replaced with the value (or empty string if absent), and the Arg helper with a default uses the value when set and non-empty, otherwise the default. This pairs with 'prompt_name' to fill a predefined prompt's parameters without fetching it first. " +
			"Optionally provide 'initial_prompt_delay' to delay the initial prompt delivery instead of sending it immediately. " +
			"Supports both absolute timestamps (e.g., '2024-01-15T10:30:00Z') and relative durations from now (e.g., '5m', '1h', '2h30m'). " +
			"Requires 'initial_prompt' or 'prompt_name' to be set. " +
			"Optionally specify a 'workspace' UUID to create the conversation in a different workspace (requires user confirmation). " +
			"Optionally provide 'beads_issue' to link the new conversation to a beads issue ID (e.g. 'mitto-123'). " +
			"Set 'model_tag' to pin the new conversation's active model from the first turn to the first available profile carrying this tag (same tag-resolution semantics as prompt-level preferredModels). Requires the started agent to have advertised a model catalog; if no available model matches, spawn fails loudly so callers can retry or spawn without pinning. " +
			"Optionally configure the conversation as a loop by providing 'loop_prompt', 'loop_frequency_value', and 'loop_frequency_unit'. " +
			"Instead of an inline 'loop_prompt', you may provide 'loop_prompt_name' to drive the loop from a predefined workspace prompt (resolved the same way as 'prompt_name', case-insensitive) — 'loop_prompt' and 'loop_prompt_name' are mutually exclusive. " +
			"Optionally provide 'loop_arguments' (map of string keys to string values) to fill Go-template '.Args' placeholders in the resolved loop prompt at each execution. " +
			"This is equivalent to configuring the loop via 'mitto_conversation_update' after creation, but done in one step. " +
			"When 'prompt_name' resolves to a prompt that carries a 'loop:' frontmatter block, its fields (trigger, delay, frequency, maxIterations, maxDuration, condition) fill any loop_* arguments the caller did not pass explicitly, and — if the caller passed no 'loop_prompt' / 'loop_prompt_name' — the seed prompt itself becomes the loop body (self-referential loop). Pass 'loop_apply_prompt_defaults': false to disable this merge. " +
			"For a daily loop, optionally specify 'loop_frequency_at' (HH:MM in UTC). " +
			"Set 'loop_enabled' to false to create the loop configuration in a paused state. " +
			"Set 'loop_fresh_context' to true to start each run with a clean agent context (no history injection, new ACP session). " +
			"Set 'loop_max_iterations' to limit the number of scheduled runs (0 = unlimited). " +
			"Set 'loop_trigger' to 'onCompletion' to fire the next run after the agent stops responding (event-driven), 'onTasks' to fire when beads/tasks in the workspace change (event-driven), or 'onChild' to fire when a child conversation of this loop finishes a response or is deleted (event-driven), instead of on a fixed 'schedule'; neither onCompletion, onTasks, nor onChild requires a frequency. 'onChild' can never be armed alone (it is purely reactive to a child's lifecycle) — arm it alongside at least one other trigger. " +
			"Several triggers can be armed at once by passing a comma-separated list (e.g. 'schedule,onCompletion') — each arms independently and stays armed for the loop's lifetime, and each trigger's own settings (frequency, completion delay, task condition, child events) apply independently. " +
			"If two armed triggers want to fire in the same narrow window, only ONE run is delivered — the other is dropped, not queued (precedence within a tick: onTasks > onChild > onCompletion > schedule). 'loop_max_iterations' and 'loop_max_duration_seconds' are shared across every armed trigger, decremented once per delivered run regardless of which trigger fired it. " +
			"For 'onCompletion', set 'loop_completion_delay_seconds' to the wait after the agent stops (clamped to the global floor). " +
			"For 'onTasks', optionally set 'loop_condition' to a CEL expression gating which task changes fire the run (empty = fire on ANY beads/task change); 'loop_condition_preset' records an optional UI preset id compiled into the condition. " +
			"For 'onChild', optionally set 'loop_child_events' to a list of 'anyEndResponse'/'anyDeleted'/'anyLoopStopped' to restrict which child lifecycle events fire the run (empty/absent = anyEndResponse + anyDeleted; anyLoopStopped — fires once when a child's own loop stops, a real 'child driver is done' signal — is opt-in only). " +
			"Set 'loop_max_duration_seconds' to auto-stop the conversation after a wall-clock cap since iterating started (0 = unlimited). " +
			"Cannot be used together with 'acp_server'. " +
			"Requires 'Can start conversation' flag to be enabled in Advanced Settings (disabled by default for security). " +
			"Note: Conversations created by this tool cannot spawn further conversations (to prevent infinite recursion). " +
			selfIDNote,
	}, s.handleConversationStart)

	// mitto_conversation_get - Get properties of a specific conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_get",
		Description: "Get detailed properties of a specific conversation by conversation_id. " +
			"Returns metadata, status, runtime info including whether the agent is currently replying, " +
			"and the list of queued prompts (with scheduled delivery times, if any). " +
			"Also returns parent-child relationship info (parent_session_id, child_origin). " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			"Optionally specify a 'workspace' UUID to access a conversation in a different workspace (requires user confirmation). " +
			selfIDNote,
	}, s.handleGetConversation)

	// mitto_conversation_run_loop_now - Trigger immediate loop run
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_run_loop_now",
		Description: "Trigger an immediate run of a loop conversation's configured prompt, bypassing the normal schedule. " +
			"The conversation must have loop prompts configured and enabled. " +
			"Use 'reset_timer' to control whether the countdown for the next scheduled run resets (default: true). " +
			"When reset_timer is true, the next run is scheduled from now (as if a normal run just occurred). " +
			"When reset_timer is false, the existing next-run schedule is preserved unchanged. " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			selfIDNote,
	}, s.handleRunLoopNow)

	// mitto_conversation_archive - Archive or unarchive a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_archive",
		Description: "Archive or unarchive a conversation. " +
			"Archiving a conversation gracefully stops any active agent response, closes the ACP connection, " +
			"and marks the conversation as archived. Archived conversations are read-only but can be unarchived later. " +
			"Set archived=false to unarchive a conversation and resume the ACP connection. " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			selfIDNote,
	}, s.handleArchiveConversation)

	// mitto_conversation_delete - Permanently delete a child conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_delete",
		Description: "Delete a conversation. " +
			"This permanently deletes the conversation, gracefully stopping any active agent response and closing its ACP connection. " +
			"To delete a CHILD conversation, pass its conversation_id; the caller MUST be the parent of the target conversation (verified via the parent-child relationship). " +
			"To delete YOUR OWN conversation (self-destruct), pass \"self\" (or your own conversation ID) as conversation_id; the deletion happens automatically once your current response finishes. " +
			"Deleted conversations are permanently removed and cannot be recovered. " +
			selfIDNote,
	}, s.handleDeleteConversation)

	// mitto_conversation_update - Update properties of a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_update",
		Description: "Update properties of a conversation. " +
			"Supports partial updates — only specified fields are changed, others are left untouched. " +
			"To update YOUR OWN conversation (e.g. a loop conversation disabling its own loop), " +
			"pass \"self\" (or your own conversation ID) as conversation_id. " +
			"Updatable properties: 'name' (conversation title), 'user_data' (workspace-defined metadata attributes), " +
			"'beads_issue' (linked beads issue ID, e.g. \"mitto-123\"; empty string clears it), " +
			"'model_tag' (switch the target conversation's active model to the first available model whose profile carries this tag; empty string restores the baseline; requires the target conversation to be running and its agent to have advertised a model catalog), " +
			"'loop' (loop prompt configuration). " +
			"User data is validated against the workspace's schema defined in .mittorc. " +
			"Set 'user_data_merge' to true (default) to merge with existing attributes, or false to replace all. " +
			"Loop configuration: provide 'loop_prompt', 'loop_frequency_value', and 'loop_frequency_unit' " +
			"to configure or update loop prompts. Use 'loop_frequency_at' (HH:MM UTC) for daily schedules. " +
			"Instead of an inline 'loop_prompt', you may provide 'loop_prompt_name' to drive the loop from a predefined workspace prompt (resolved the same way as 'prompt_name', case-insensitive) — 'loop_prompt' and 'loop_prompt_name' are mutually exclusive; sending an empty 'loop_prompt_name' clears the previously-set named prompt. " +
			"Optionally provide 'loop_arguments' (map of string keys to string values) to fill Go-template '.Args' placeholders in the resolved loop prompt at each execution. " +
			"When 'loop_prompt_name' resolves to a prompt that carries a 'loop:' frontmatter block, its fields (trigger, delay, frequency, maxIterations, maxDuration, condition) fill any loop_* arguments the caller did not pass explicitly. Pass 'loop_apply_prompt_defaults': false to disable this merge. " +
			"Set 'loop_enabled' to false to pause loop execution without deleting the configuration. " +
			"To disable loop entirely, set 'loop_enabled' to false. " +
			"Set 'loop_fresh_context' to true to start each run with a clean agent context (no history injection, new ACP session). " +
			"Set 'loop_max_iterations' to limit the number of scheduled runs (0 = unlimited). " +
			"Set 'loop_trigger' to 'onCompletion' (event-driven: fire after the agent stops), 'onTasks' (event-driven: fire when beads/tasks in the workspace change), 'onChild' (event-driven: fire when a child conversation of this loop finishes a response or is deleted), or 'schedule' (frequency-based, default); neither onCompletion, onTasks, nor onChild requires a frequency. 'onChild' can never be armed alone — arm it alongside at least one other trigger. " +
			"Several triggers can be armed at once by passing a comma-separated list (e.g. 'schedule,onCompletion'); the list REPLACES the currently armed set, each arms independently, and — if two armed triggers want to fire in the same narrow window — only ONE run is delivered (the other is dropped, not queued; precedence within a tick: onTasks > onChild > onCompletion > schedule). " +
			"'loop_max_iterations' and 'loop_max_duration_seconds' are shared across every armed trigger, decremented once per delivered run regardless of which trigger fired it. " +
			"For 'onCompletion', set 'loop_completion_delay_seconds' to the wait after the agent stops (clamped to the global floor). " +
			"For 'onTasks', optionally set 'loop_condition' to a CEL expression gating which task changes fire the run (empty = fire on ANY beads/task change); 'loop_condition_preset' records an optional UI preset id compiled into the condition. " +
			"For 'onChild', optionally set 'loop_child_events' to a list of 'anyEndResponse'/'anyDeleted'/'anyLoopStopped' to restrict which child lifecycle events fire the run (nil = unchanged, non-nil replaces the stored list wholesale; empty/absent once applied = anyEndResponse + anyDeleted; anyLoopStopped — fires once when a child's own loop stops — is opt-in only). " +
			"Set 'loop_max_duration_seconds' to auto-stop the conversation after a wall-clock cap since iterating started (0 = unlimited). " +
			selfIDNote,
	}, s.handleConversationUpdate)

	// mitto_conversation_wait - Wait until something happens in a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_wait",
		Description: "Wait until something happens in a conversation. " +
			"Supports two 'what' values: " +
			"'agent_responded' — blocks until the agent finishes responding (default timeout: 10 min); " +
			"'beads_issues_reached_state' — blocks until one or more bd issues reach a target status. " +
			"For 'beads_issues_reached_state', provide 'beads_issues' (list of bd IDs, e.g. [\"mitto-1ac\"]), " +
			"'beads_target_state' (e.g. \"closed\", case-insensitive), and optionally 'beads_match' " +
			"(\"all\" (default) or \"any\"). Default timeout for this mode is 4 hours. " +
			"The output includes 'reached_issues', 'pending_issues', and 'current_states' (id → status snapshot). " +
			"Returns immediately if the condition is already met (e.g., agent is not currently responding, or all listed beads already at the target state). " +
			"Optionally specify a 'workspace' UUID when waiting on a conversation in a different workspace (requires user confirmation). " +
			"If the wait times out, the result includes 'timed_out: true' and 'still_prompting' indicating whether the agent is still responding — you do NOT need to separately check the prompting status. " +
			"Note: each physical call blocks at most a few minutes internally regardless of timeout_seconds or the mode's default, to avoid tripping a client-side HTTP transport timeout on very long waits — if 'timed_out' comes back true well before your requested duration has elapsed, simply call this tool again with the same parameters to keep waiting. " +
			selfIDNote,
	}, s.handleConversationWait)

	// mitto_children_tasks_wait - Wait for children to report progress
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_children_tasks_wait",
		Description: "Send a progress inquiry to multiple child conversations and BLOCK until all of them report back. " +
			"For each child, the provided prompt (plus reporting instructions) is enqueued. " +
			"If prompt is empty or omitted, no message is sent — the tool just waits for children to report " +
			"(useful for retrying after a timeout without re-enqueuing duplicate messages). " +
			"Duplicate messages are also prevented: if a child already has a pending message from this parent " +
			"in its queue, the prompt is skipped for that child. " +
			"This tool blocks until all children have reported or the timeout expires. " +
			"Returns a consolidated report from all children. " +
			"Requires 'Can Send Prompt' flag to be enabled. " +
			"Use task_id to scope reports: when retrying the same task after a timeout, pass the same task_id " +
			"so that reports already received are preserved. When starting a different task, use a new task_id " +
			"to clear stale reports from the previous task. " +
			selfIDNote,
	}, s.handleChildrenTasksWait)

	// mitto_children_tasks_report - Report task progress back to parent
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_children_tasks_report",
		Description: "Report task completion or progress back to a waiting parent conversation. " +
			"The parent must have previously called mitto_children_tasks_wait with this conversation's ID in the children_list. " +
			"Provide a status (e.g. 'completed', 'in_progress', 'failed'), a summary of your findings, " +
			"and optionally details with additional information. " +
			"Keep reports concise: summary is limited to ~8KB and details to ~16KB. " +
			"If the parent provided a task_id in the wait call, include the same task_id in your report. " +
			selfIDNote,
	}, s.handleChildrenTasksReport)

	// mitto_conversation_history - Search and retrieve conversation history
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_history",
		Description: "Get and search through the conversation history of a session. " +
			"Returns events (user prompts, agent messages, tool calls, etc.) with powerful filtering. " +
			"Useful for recalling past decisions, finding specific tool calls, searching for errors, " +
			"or reviewing what happened in a conversation. " +
			"Filter by time range with 'since' and 'until': both accept an absolute RFC 3339 timestamp " +
			"(e.g., '2024-01-15T10:30:00Z') or a relative duration meaning ago (e.g., '3m', '1h', '2h30m'). " +
			"Defaults to your own conversation if conversation_id is omitted. " +
			selfIDNote,
	}, s.handleConversationHistory)

	// mitto_prompt_list - List all prompts in a workspace
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_prompt_list",
		Description: "List all prompts available in a workspace, returning basic metadata for each (name, origin/source, enabled status, group, etc.) but NOT the full prompt text. " +
			"This reflects the merged/effective prompt list from all sources (global, settings, ACP-specific, workspace directory, workspace inline). " +
			"Defaults to the caller's workspace. " + selfIDNote,
	}, s.handlePromptList)

	// mitto_prompt_get - Get full details for a specific prompt
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_prompt_get",
		Description: "Get full details for a specific prompt in a workspace, including the complete prompt text and all metadata (origin, enabled status, group, etc.). " +
			"Prompt name matching is case-insensitive. " + selfIDNote,
	}, s.handlePromptGet)

	// mitto_prompt_update - Update a prompt's details
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_prompt_update",
		Description: "Update a prompt's details including its full text, description, group, color, and enabled status. " +
			"If the prompt originates from a global source, the update is saved to the workspace-local .mitto/prompts/ folder (creating a workspace-level override). " +
			"If only the enabled field is provided, uses the optimized toggle-enabled logic (updates frontmatter or .mittorc). " +
			"Can also create new prompts by specifying a name that doesn't exist yet. " + selfIDNote,
	}, s.handlePromptUpdate)
}
