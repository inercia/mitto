package conversation

// acpCallbackSink owns the WebClient callback cluster for BackgroundSession.
// It is a stateless collaborator of BackgroundSession (held by composition,
// zero value is ready to use) and is unit-testable in isolation via the
// acpCallbackDeps seam.

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// acpCallbackDeps supplies the live, side-effecting primitives the
// acpCallbackSink orchestrates. BackgroundSession satisfies it in production;
// tests use a fake.
type acpCallbackDeps interface {
	// --- Lifecycle / identity ---

	// cbIsClosed reports whether the session has been closed.
	cbIsClosed() bool
	// cbSessionID returns the persisted session ID.
	cbSessionID() string
	// cbLogger returns the session-scoped logger (may be nil).
	cbLogger() *slog.Logger

	// --- Observers ---

	// cbNotifyObservers broadcasts a callback to all registered session observers.
	cbNotifyObservers(func(SessionObserver))
	// cbObserverCount returns the number of currently registered observers.
	cbObserverCount() int
	// cbHasObservers reports whether any observer is currently registered.
	cbHasObservers() bool

	// --- Recorder ---

	// cbRecordEventWithSeq persists an event with a pre-assigned sequence number.
	// No-op if no recorder is configured. Logs errors via cbLogger.
	cbRecordEventWithSeq(event session.Event, kind string)
	// cbRecordPermission records a permission decision via the recorder.
	// No-op if no recorder is configured.
	cbRecordPermission(title, selectedOption, outcome string)

	// --- Context usage state ---

	// cbSetContextUsage stores the latest context window usage atomically.
	cbSetContextUsage(size, used int)

	// --- Available commands state ---

	// cbSetAvailableCommands stores the current list of available commands.
	cbSetAvailableCommands(cmds []AvailableCommand)
	// cbGetAvailableCommands returns a defensive copy of the current commands.
	cbGetAvailableCommands() []AvailableCommand

	// --- MCP correlation ---

	// cbRegisterPendingMCPRequest associates a mitto_* tool request with this
	// session via the global MCP server. Returns false if no MCP server is wired.
	cbRegisterPendingMCPRequest(requestID string) bool

	// --- Plan state cache ---

	// cbNotifyPlanStateChanged invokes the SessionManager plan-state cache callback
	// if one was configured (no-op otherwise).
	cbNotifyPlanStateChanged(entries []PlanEntry)

	// --- Permissions ---

	// cbAutoApprove reports whether the global auto-approve flag is set.
	cbAutoApprove() bool
	// cbSessionAutoApprovePermissions reports whether the per-session
	// auto-approve flag is enabled in metadata. Returns false on any error.
	cbSessionAutoApprovePermissions() bool
	// cbUIPrompt forwards to the unified UI prompt system.
	cbUIPrompt(ctx context.Context, req UIPromptRequest) (UIPromptResponse, error)

	// --- Mode / config changes ---

	// cbSetModeCurrentValue updates the mode config option's CurrentValue
	// under the proper mutex.
	cbSetModeCurrentValue(modeID string)
	// cbPersistConfigValue persists a config-option value to metadata.
	cbPersistConfigValue(configID, value string)
	// cbNotifyConfigChanged invokes the on-config-changed callback if configured.
	cbNotifyConfigChanged(configID, value string)

	// --- Legacy modes ---

	// cbSetLegacyModes replaces configOptions with a single mode entry and
	// flips usesLegacyModes to true, under the proper mutex.
	cbSetLegacyModes(modeOption SessionConfigOption)

	// --- Model state ---

	// cbStoreAgentModels stores the raw agent model state reference.
	cbStoreAgentModels(models *acp.UnstableSessionModelState)
	// cbACPServerConstraint returns the constraint for a category (may be nil).
	cbACPServerConstraint(category string) *config.ACPServerConstraint
	// cbReplaceModelConfigOption removes any existing model config option
	// and appends the new one, under the proper mutex.
	cbReplaceModelConfigOption(modelOption SessionConfigOption)
	// cbInitBaselineModelIfEmpty initialises baselineModel under modelMu if it
	// is still empty, preferring persisted metadata over the supplied default.
	cbInitBaselineModelIfEmpty(defaultModel string)
	// cbApplyConfigConstraintsAsync kicks off the async constraint-application
	// goroutine for a category (matches the legacy `go bs.applyConfigConstraints(...)`).
	cbApplyConfigConstraintsAsync(category string)
}

// acpCallbackSink is stateless; all dependencies are passed per call,
// mirroring queueDispatcher/titleCoordinator.
type acpCallbackSink struct{}

// --- Telemetry helper ---

// logAgentModels logs the agent's model state at DEBUG level.
func (acpCallbackSink) logAgentModels(d acpCallbackDeps, models *acp.UnstableSessionModelState) {
	lg := d.cbLogger()
	if lg == nil || models == nil {
		return
	}
	modelNames := make([]string, len(models.AvailableModels))
	for i, m := range models.AvailableModels {
		modelNames[i] = m.Name
	}
	lg.Debug("Agent model state (UNSTABLE)",
		"current_model", string(models.CurrentModelId),
		"available_models", modelNames,
		"model_count", len(models.AvailableModels))
}

// --- Context usage ---

// onContextUsageUpdate stores the latest context window usage and notifies all observers.
func (acpCallbackSink) onContextUsageUpdate(d acpCallbackDeps, size, used int) {
	d.cbSetContextUsage(size, used)
	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnContextUsageUpdate(size, used)
	})
}

// --- Stream callbacks ---

func (acpCallbackSink) onAgentMessage(d acpCallbackDeps, seq int64, html string) {
	if d.cbIsClosed() {
		return
	}

	htmlLen := len(html)

	// Persist immediately with pre-assigned seq
	d.cbRecordEventWithSeq(session.Event{
		Seq:       seq,
		Type:      session.EventTypeAgentMessage,
		Timestamp: time.Now(),
		Data:      session.AgentMessageData{Text: html},
	}, "agent message")

	// Notify all observers
	observerCount := d.cbObserverCount()

	// Enhanced logging for debugging message content issues
	if lg := d.cbLogger(); lg != nil {
		if htmlLen > 1000 {
			// Large message - log with preview
			preview := html
			if len(preview) > 200 {
				preview = html[:100] + "..." + html[htmlLen-100:]
			}
			lg.Debug("agent_message_to_observers_large",
				"seq", seq,
				"html_len", htmlLen,
				"observer_count", observerCount,
				"session_id", d.cbSessionID(),
				"preview", preview)
		} else if observerCount > 1 {
			lg.Debug("Notifying multiple observers of agent message",
				"observer_count", observerCount,
				"html_len", htmlLen,
				"seq", seq)
		}
	}

	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnAgentMessage(seq, html)
	})
}

func (acpCallbackSink) onAgentThought(d acpCallbackDeps, seq int64, text string) {
	if d.cbIsClosed() {
		return
	}

	d.cbRecordEventWithSeq(session.Event{
		Seq:       seq,
		Type:      session.EventTypeAgentThought,
		Timestamp: time.Now(),
		Data:      session.AgentThoughtData{Text: text},
	}, "agent thought")

	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnAgentThought(seq, text)
	})
}

func (acpCallbackSink) onToolCall(d acpCallbackDeps, seq int64, id, title, status string) {
	if d.cbIsClosed() {
		return
	}

	d.cbRecordEventWithSeq(session.Event{
		Seq:       seq,
		Type:      session.EventTypeToolCall,
		Timestamp: time.Now(),
		Data: session.ToolCallData{
			ToolCallID: id,
			Title:      title,
			Status:     status,
		},
	}, "tool call")

	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnToolCall(seq, id, title, status)
	})
}

// onMittoToolCall is called when any mitto_* tool call is detected.
// It registers a correlation ID (requestID) with the global MCP server to associate
// MCP tool requests with this ACP session. This enables session-aware tool behavior
// even when the MCP client doesn't know which session it's operating in.
// Note: requestID here is a correlation ID, not to be confused with session_id.
func (acpCallbackSink) onMittoToolCall(d acpCallbackDeps, requestID string) {
	if d.cbIsClosed() {
		return
	}

	if !d.cbRegisterPendingMCPRequest(requestID) {
		if lg := d.cbLogger(); lg != nil {
			lg.Debug("Cannot register mitto tool request: no global MCP server",
				"request_id", requestID,
				"session_id", d.cbSessionID())
		}
		return
	}

	if lg := d.cbLogger(); lg != nil {
		lg.Debug("Registered mitto tool request",
			"request_id", requestID,
			"session_id", d.cbSessionID())
	}
}

func (acpCallbackSink) onToolUpdate(d acpCallbackDeps, seq int64, id string, status *string) {
	if d.cbIsClosed() {
		return
	}

	d.cbRecordEventWithSeq(session.Event{
		Seq:       seq,
		Type:      session.EventTypeToolCallUpdate,
		Timestamp: time.Now(),
		Data: session.ToolCallUpdateData{
			ToolCallID: id,
			Status:     status,
		},
	}, "tool call update")

	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnToolUpdate(seq, id, status)
	})
}

func (acpCallbackSink) onPlan(d acpCallbackDeps, seq int64, entries []PlanEntry) {
	if d.cbIsClosed() {
		return
	}

	// Convert web.PlanEntry to session.PlanEntry for persistence
	sessionEntries := make([]session.PlanEntry, len(entries))
	for i, entry := range entries {
		sessionEntries[i] = session.PlanEntry{
			Content:  entry.Content,
			Priority: entry.Priority,
			Status:   entry.Status,
		}
	}
	d.cbRecordEventWithSeq(session.Event{
		Seq:       seq,
		Type:      session.EventTypePlan,
		Timestamp: time.Now(),
		Data:      session.PlanData{Entries: sessionEntries},
	}, "plan")

	// Cache plan state in SessionManager for restoration on conversation switch
	d.cbNotifyPlanStateChanged(entries)

	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnPlan(seq, entries)
	})
}

func (acpCallbackSink) onFileWrite(d acpCallbackDeps, seq int64, path string, size int) {
	if d.cbIsClosed() {
		return
	}

	d.cbRecordEventWithSeq(session.Event{
		Seq:       seq,
		Type:      session.EventTypeFileWrite,
		Timestamp: time.Now(),
		Data:      session.FileOperationData{Path: path, Size: size},
	}, "file write")

	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnFileWrite(seq, path, size)
	})
}

func (acpCallbackSink) onFileRead(d acpCallbackDeps, seq int64, path string, size int) {
	if d.cbIsClosed() {
		return
	}

	d.cbRecordEventWithSeq(session.Event{
		Seq:       seq,
		Type:      session.EventTypeFileRead,
		Timestamp: time.Now(),
		Data:      session.FileOperationData{Path: path, Size: size},
	}, "file read")

	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnFileRead(seq, path, size)
	})
}

// --- Permission handling ---

func (acpCallbackSink) onPermission(d acpCallbackDeps, ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	lg := d.cbLogger()
	if d.cbIsClosed() {
		if lg != nil {
			lg.Debug("permission_request_rejected", "reason", "session_closed")
		}
		return acp.RequestPermissionResponse{}, &sessionError{"session is closed"}
	}

	// Get title from tool call
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	if lg != nil {
		lg.Debug("permission_request_received",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"auto_approve", d.cbAutoApprove(),
			"has_observers", d.cbHasObservers(),
			"options_count", len(params.Options))
	}

	// Check if auto-approve is enabled (global flag OR per-session setting)
	autoApprove := d.cbAutoApprove()
	if !autoApprove && d.cbSessionAutoApprovePermissions() {
		autoApprove = true
		if lg != nil {
			lg.Debug("permission_using_session_auto_approve",
				"title", title,
				"tool_call_id", params.ToolCall.ToolCallId,
				"session_id", d.cbSessionID())
		}
	}

	if autoApprove {
		resp := mittoAcp.AutoApprovePermission(params.Options)
		selectedOption := ""
		if resp.Outcome.Selected != nil {
			selectedOption = string(resp.Outcome.Selected.OptionId)
		}
		if lg != nil {
			lg.Info("permission_auto_approved",
				"title", title,
				"tool_call_id", params.ToolCall.ToolCallId,
				"selected_option", selectedOption)
		}
		if resp.Outcome.Selected != nil {
			d.cbRecordPermission(title, string(resp.Outcome.Selected.OptionId), "auto_approved")
		}
		return resp, nil
	}

	// Check if we have any observers to show the permission dialog
	if !d.cbHasObservers() {
		if lg != nil {
			lg.Warn("permission_cancelled",
				"title", title,
				"tool_call_id", params.ToolCall.ToolCallId,
				"reason", "no_observers")
		}
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Convert ACP permission options to unified UIPromptOptions
	options := make([]UIPromptOption, len(params.Options))
	for i, opt := range params.Options {
		var style UIPromptOptionStyle
		switch opt.Kind {
		case acp.PermissionOptionKindAllowOnce, acp.PermissionOptionKindAllowAlways:
			style = UIPromptOptionStyleSuccess
		case acp.PermissionOptionKindRejectOnce:
			style = UIPromptOptionStyleDanger
		default:
			style = UIPromptOptionStyleSecondary
		}

		options[i] = UIPromptOption{
			ID:    string(opt.OptionId),
			Label: opt.Name,
			Kind:  string(opt.Kind),
			Style: style,
		}
	}

	toolCallID := string(params.ToolCall.ToolCallId)
	promptReq := UIPromptRequest{
		RequestID:      toolCallID,
		Type:           UIPromptTypePermission,
		Question:       "Permission requested",
		Title:          title,
		Options:        options,
		TimeoutSeconds: 300, // 5 minute timeout for permissions
		Blocking:       true,
		ToolCallID:     toolCallID,
	}

	if lg != nil {
		lg.Debug("permission_showing_ui_prompt",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"option_count", len(options))
	}

	resp, err := d.cbUIPrompt(ctx, promptReq)
	if err != nil {
		if lg != nil {
			lg.Warn("permission_prompt_error",
				"title", title,
				"tool_call_id", params.ToolCall.ToolCallId,
				"error", err)
		}
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	if resp.TimedOut {
		if lg != nil {
			lg.Warn("permission_timed_out",
				"title", title,
				"tool_call_id", params.ToolCall.ToolCallId)
		}
		d.cbRecordPermission(title, "", "timed_out")
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	if lg != nil {
		lg.Info("permission_user_selected",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"selected_option", resp.OptionID)
	}

	d.cbRecordPermission(title, resp.OptionID, "user_selected")

	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Selected: &acp.RequestPermissionOutcomeSelected{
				OptionId: acp.PermissionOptionId(resp.OptionID),
			},
		},
	}, nil
}

// --- Available commands ---

// onAvailableCommands handles the available slash commands update from the agent.
// It stores the commands (sorted alphabetically by name) and notifies all observers.
func (acpCallbackSink) onAvailableCommands(d acpCallbackDeps, commands []AvailableCommand) {
	if d.cbIsClosed() {
		return
	}

	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})

	d.cbSetAvailableCommands(commands)

	if lg := d.cbLogger(); lg != nil {
		commandNames := make([]string, len(commands))
		for i, cmd := range commands {
			commandNames[i] = "/" + cmd.Name
		}
		lg.Debug("Available slash commands updated",
			"count", len(commands),
			"commands", commandNames)
	}

	d.cbNotifyObservers(func(o SessionObserver) {
		o.OnAvailableCommandsUpdated(commands)
	})
}

// availableCommands returns the current list of available slash commands
// (sorted alphabetically by name), as a defensive copy.
func (acpCallbackSink) availableCommands(d acpCallbackDeps) []AvailableCommand {
	return d.cbGetAvailableCommands()
}

// --- Mode / model setters ---

// onCurrentModeChanged handles the session mode change notification from the agent.
// This updates the stored config option and notifies observers.
// Called for the legacy modes API; converts to config option format internally.
func (acpCallbackSink) onCurrentModeChanged(d acpCallbackDeps, modeID string) {
	if d.cbIsClosed() {
		return
	}

	d.cbSetModeCurrentValue(modeID)
	d.cbPersistConfigValue(ConfigOptionCategoryMode, modeID)

	if lg := d.cbLogger(); lg != nil {
		lg.Debug("Session mode changed (via agent)",
			"mode_id", modeID)
	}

	d.cbNotifyConfigChanged(ConfigOptionCategoryMode, modeID)
}

// setSessionModes converts the legacy modes API response to a single mode
// config option, enabling transparent support for both legacy modes and the
// newer configOptions API.
func (acpCallbackSink) setSessionModes(d acpCallbackDeps, modes *acp.SessionModeState) {
	if modes == nil {
		return
	}

	options := make([]SessionConfigOptionValue, len(modes.AvailableModes))
	for i, m := range modes.AvailableModes {
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		options[i] = SessionConfigOptionValue{
			Value:       string(m.Id),
			Name:        m.Name,
			Description: desc,
		}
	}

	modeOption := SessionConfigOption{
		ID:           ConfigOptionCategoryMode, // Use "mode" as ID for legacy modes
		Name:         "Mode",
		Description:  "Session operating mode",
		Category:     ConfigOptionCategoryMode,
		Type:         ConfigOptionTypeSelect,
		CurrentValue: string(modes.CurrentModeId),
		Options:      options,
	}

	d.cbSetLegacyModes(modeOption)
	d.cbPersistConfigValue(ConfigOptionCategoryMode, string(modes.CurrentModeId))
}

// setAgentModels converts agent model state to a "model" config option, enabling
// model switching to reuse the config option infrastructure.
func (acpCallbackSink) setAgentModels(d acpCallbackDeps, models *acp.UnstableSessionModelState) {
	d.cbStoreAgentModels(models)
	if models == nil || len(models.AvailableModels) == 0 {
		return
	}

	options := ModelsToConfigOptions(models)

	// Start with the agent's reported current model.
	// Pre-apply any matching constraint to local state immediately, so the UI shows
	// the desired model from the very first acp_started message — before the async
	// RPC in applyConfigConstraints completes. agentModels.CurrentModelId is NOT
	// updated here; applyConfigConstraints compares against it to know whether the
	// agent-side change still needs to happen.
	currentValue := string(models.CurrentModelId)
	if constraint := d.cbACPServerConstraint(ConfigOptionCategoryModel); constraint != nil && constraint.Pattern != "" {
		if matched := MatchConstraintOption(constraint, options); matched != "" && matched != currentValue {
			if lg := d.cbLogger(); lg != nil {
				lg.Debug("ACP server constraint: pre-applying model to local state",
					"category", ConfigOptionCategoryModel,
					"agent_model", currentValue,
					"desired_model", matched)
			}
			currentValue = matched
		}
	}

	modelOption := SessionConfigOption{
		ID:           ConfigOptionCategoryModel,
		Name:         "Model",
		Description:  "AI model for this session (UNSTABLE)",
		Category:     ConfigOptionCategoryModel,
		Type:         ConfigOptionTypeSelect,
		CurrentValue: currentValue,
		Options:      options,
	}

	d.cbReplaceModelConfigOption(modelOption)

	// Initialize baselineModel from persisted metadata (survive suspend/resume) or
	// from the agent's reported current model. Only set when empty so a prior call
	// isn't overwritten. applyConfigConstraints (called async below) will update
	// baseline via SetConfigOption if a constraint selects a different model.
	d.cbInitBaselineModelIfEmpty(string(models.CurrentModelId))

	d.cbApplyConfigConstraintsAsync(ConfigOptionCategoryModel)
}

// recordEventWithSeqHelper is a small helper used by BackgroundSession's
// cbRecordEventWithSeq implementation to preserve the original warn/error
// classification on "session not started" errors.
func recordEventWithSeqHelper(rec *session.Recorder, lg *slog.Logger, event session.Event, kind string) {
	if rec == nil {
		return
	}
	if err := rec.RecordEventWithSeq(event); err != nil && lg != nil {
		if strings.Contains(err.Error(), "session not started") {
			lg.Warn("Failed to persist "+kind, "seq", event.Seq, "error", err)
		} else {
			lg.Error("Failed to persist "+kind, "seq", event.Seq, "error", err)
		}
	}
}
