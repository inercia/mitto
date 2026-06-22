package conversation

// ACP callback methods cluster for BackgroundSession.
// These methods receive events from the ACP agent via WebClient.

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/session"
)

// logAgentModels logs the agent's model state at DEBUG level.
func (bs *BackgroundSession) logAgentModels(models *acp.UnstableSessionModelState) {
	if bs.logger == nil || models == nil {
		return
	}
	modelNames := make([]string, len(models.AvailableModels))
	for i, m := range models.AvailableModels {
		modelNames[i] = m.Name
	}
	bs.logger.Debug("Agent model state (UNSTABLE)",
		"current_model", string(models.CurrentModelId),
		"available_models", modelNames,
		"model_count", len(models.AvailableModels))
}

// onContextUsageUpdate stores the latest context window usage and notifies all observers.
func (bs *BackgroundSession) onContextUsageUpdate(size, used int) {
	bs.contextUsageMu.Lock()
	bs.contextSize = size
	bs.contextUsed = used
	bs.contextUsageMu.Unlock()

	bs.notifyObservers(func(o SessionObserver) {
		o.OnContextUsageUpdate(size, used)
	})
}

// --- Callback methods for WebClient ---

func (bs *BackgroundSession) onAgentMessage(seq int64, html string) {
	if bs.IsClosed() {
		return
	}

	htmlLen := len(html)

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeAgentMessage,
			Timestamp: time.Now(),
			Data:      session.AgentMessageData{Text: html},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist agent message", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist agent message", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	observerCount := bs.ObserverCount()

	// Enhanced logging for debugging message content issues
	if bs.logger != nil {
		if htmlLen > 1000 {
			// Large message - log with preview
			preview := html
			if len(preview) > 200 {
				preview = html[:100] + "..." + html[htmlLen-100:]
			}
			bs.logger.Debug("agent_message_to_observers_large",
				"seq", seq,
				"html_len", htmlLen,
				"observer_count", observerCount,
				"session_id", bs.persistedID,
				"preview", preview)
		} else if observerCount > 1 {
			bs.logger.Debug("Notifying multiple observers of agent message",
				"observer_count", observerCount,
				"html_len", htmlLen,
				"seq", seq)
		}
	}

	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentMessage(seq, html)
	})
}

func (bs *BackgroundSession) onAgentThought(seq int64, text string) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeAgentThought,
			Timestamp: time.Now(),
			Data:      session.AgentThoughtData{Text: text},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist agent thought", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist agent thought", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentThought(seq, text)
	})
}

func (bs *BackgroundSession) onToolCall(seq int64, id, title, status string) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeToolCall,
			Timestamp: time.Now(),
			Data: session.ToolCallData{
				ToolCallID: id,
				Title:      title,
				Status:     status,
			},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist tool call", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist tool call", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolCall(seq, id, title, status)
	})
}

// onMittoToolCall is called when any mitto_* tool call is detected.
// It registers a correlation ID (requestID) with the global MCP server to associate
// MCP tool requests with this ACP session. This enables session-aware tool behavior
// even when the MCP client doesn't know which session it's operating in.
// Note: requestID here is a correlation ID, not to be confused with session_id.

func (bs *BackgroundSession) onMittoToolCall(requestID string) {
	if bs.IsClosed() {
		return
	}

	if bs.globalMcpServer == nil {
		if bs.logger != nil {
			bs.logger.Debug("Cannot register mitto tool request: no global MCP server",
				"request_id", requestID,
				"session_id", bs.persistedID)
		}
		return
	}

	// Register the pending request with the global MCP server
	// This allows the MCP handler to correlate the request_id with this session
	bs.globalMcpServer.RegisterPendingRequest(requestID, bs.persistedID)

	if bs.logger != nil {
		bs.logger.Debug("Registered mitto tool request",
			"request_id", requestID,
			"session_id", bs.persistedID)
	}
}

func (bs *BackgroundSession) onToolUpdate(seq int64, id string, status *string) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeToolCallUpdate,
			Timestamp: time.Now(),
			Data: session.ToolCallUpdateData{
				ToolCallID: id,
				Status:     status,
			},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist tool call update", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist tool call update", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolUpdate(seq, id, status)
	})
}

func (bs *BackgroundSession) onPlan(seq int64, entries []PlanEntry) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		// Convert web.PlanEntry to session.PlanEntry
		sessionEntries := make([]session.PlanEntry, len(entries))
		for i, entry := range entries {
			sessionEntries[i] = session.PlanEntry{
				Content:  entry.Content,
				Priority: entry.Priority,
				Status:   entry.Status,
			}
		}
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypePlan,
			Timestamp: time.Now(),
			Data:      session.PlanData{Entries: sessionEntries},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist plan", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist plan", "seq", seq, "error", err)
			}
		}
	}

	// Cache plan state in SessionManager for restoration on conversation switch
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, entries)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPlan(seq, entries)
	})
}

func (bs *BackgroundSession) onFileWrite(seq int64, path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeFileWrite,
			Timestamp: time.Now(),
			Data:      session.FileOperationData{Path: path, Size: size},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist file write", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist file write", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileWrite(seq, path, size)
	})
}

func (bs *BackgroundSession) onFileRead(seq int64, path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Persist immediately with pre-assigned seq
	if bs.recorder != nil {
		event := session.Event{
			Seq:       seq,
			Type:      session.EventTypeFileRead,
			Timestamp: time.Now(),
			Data:      session.FileOperationData{Path: path, Size: size},
		}
		if err := bs.recorder.RecordEventWithSeq(event); err != nil && bs.logger != nil {
			if strings.Contains(err.Error(), "session not started") {
				bs.logger.Warn("Failed to persist file read", "seq", seq, "error", err)
			} else {
				bs.logger.Error("Failed to persist file read", "seq", seq, "error", err)
			}
		}
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileRead(seq, path, size)
	})
}

func (bs *BackgroundSession) onPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if bs.IsClosed() {
		bs.logger.Debug("permission_request_rejected", "reason", "session_closed")
		return acp.RequestPermissionResponse{}, &sessionError{"session is closed"}
	}

	// Get title from tool call
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	bs.logger.Debug("permission_request_received",
		"title", title,
		"tool_call_id", params.ToolCall.ToolCallId,
		"auto_approve", bs.autoApprove,
		"has_observers", bs.HasObservers(),
		"options_count", len(params.Options))

	// Check if auto-approve is enabled (global flag OR per-session setting)
	autoApprove := bs.autoApprove
	if !autoApprove && bs.store != nil && bs.persistedID != "" {
		// Check per-session auto-approve flag
		if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil {
			autoApprove = session.GetFlagValue(meta.AdvancedSettings, session.FlagAutoApprovePermissions)
			if autoApprove {
				bs.logger.Debug("permission_using_session_auto_approve",
					"title", title,
					"tool_call_id", params.ToolCall.ToolCallId,
					"session_id", bs.persistedID)
			}
		}
	}

	if autoApprove {
		resp := mittoAcp.AutoApprovePermission(params.Options)
		selectedOption := ""
		if resp.Outcome.Selected != nil {
			selectedOption = string(resp.Outcome.Selected.OptionId)
		}
		bs.logger.Info("permission_auto_approved",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"selected_option", selectedOption)
		// Record the permission decision
		if bs.recorder != nil && resp.Outcome.Selected != nil {
			bs.recorder.RecordPermission(title, string(resp.Outcome.Selected.OptionId), "auto_approved")
		}
		return resp, nil
	}

	// Check if we have any observers to show the permission dialog
	hasObservers := bs.HasObservers()
	if !hasObservers {
		bs.logger.Warn("permission_cancelled",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"reason", "no_observers")
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Convert ACP permission options to unified UIPromptOptions
	options := make([]UIPromptOption, len(params.Options))
	for i, opt := range params.Options {
		// Determine button style based on option kind
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

	// Create a UIPromptRequest for the permission dialog
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

	bs.logger.Debug("permission_showing_ui_prompt",
		"title", title,
		"tool_call_id", params.ToolCall.ToolCallId,
		"option_count", len(options))

	// Use the unified UIPrompt system to show the permission dialog and wait for response
	resp, err := bs.UIPrompt(ctx, promptReq)
	if err != nil {
		bs.logger.Warn("permission_prompt_error",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId,
			"error", err)
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Handle timeout
	if resp.TimedOut {
		bs.logger.Warn("permission_timed_out",
			"title", title,
			"tool_call_id", params.ToolCall.ToolCallId)
		if bs.recorder != nil {
			bs.recorder.RecordPermission(title, "", "timed_out")
		}
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Convert the UIPromptResponse back to ACP permission response
	bs.logger.Info("permission_user_selected",
		"title", title,
		"tool_call_id", params.ToolCall.ToolCallId,
		"selected_option", resp.OptionID)

	// Record the permission decision
	if bs.recorder != nil {
		bs.recorder.RecordPermission(title, resp.OptionID, "user_selected")
	}

	// Build ACP response
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Selected: &acp.RequestPermissionOutcomeSelected{
				OptionId: acp.PermissionOptionId(resp.OptionID),
			},
		},
	}, nil
}

// onAvailableCommands handles the available slash commands update from the agent.
// It stores the commands and notifies all observers.
func (bs *BackgroundSession) onAvailableCommands(commands []AvailableCommand) {
	if bs.IsClosed() {
		return
	}

	// Store the commands (sorted alphabetically by name)
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})

	bs.availableCommandsMu.Lock()
	bs.availableCommands = commands
	bs.availableCommandsMu.Unlock()

	if bs.logger != nil {
		// Build list of command names for logging
		commandNames := make([]string, len(commands))
		for i, cmd := range commands {
			commandNames[i] = "/" + cmd.Name
		}
		bs.logger.Debug("Available slash commands updated",
			"count", len(commands),
			"commands", commandNames)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAvailableCommandsUpdated(commands)
	})
}

// AvailableCommands returns the current list of available slash commands.
// The commands are sorted alphabetically by name.
func (bs *BackgroundSession) AvailableCommands() []AvailableCommand {
	bs.availableCommandsMu.RLock()
	defer bs.availableCommandsMu.RUnlock()

	// Return a copy to avoid mutation
	if bs.availableCommands == nil {
		return nil
	}
	result := make([]AvailableCommand, len(bs.availableCommands))
	copy(result, bs.availableCommands)
	return result
}

// onCurrentModeChanged handles the session mode change notification from the agent.
// This updates the stored config option and notifies observers.
// This is called for legacy modes API - converts to config option format internally.
func (bs *BackgroundSession) onCurrentModeChanged(modeID string) {
	if bs.IsClosed() {
		return
	}

	// Update the mode config option's current value
	bs.configMu.Lock()
	for i := range bs.configOptions {
		if bs.configOptions[i].Category == ConfigOptionCategoryMode {
			bs.configOptions[i].CurrentValue = modeID
			break
		}
	}
	bs.configMu.Unlock()

	// Persist to metadata
	bs.persistConfigValue(ConfigOptionCategoryMode, modeID)

	if bs.logger != nil {
		bs.logger.Debug("Session mode changed (via agent)",
			"mode_id", modeID)
	}

	// Notify callback - use "mode" as the configID for legacy mode changes
	if bs.onConfigChanged != nil {
		bs.onConfigChanged(bs.persistedID, ConfigOptionCategoryMode, modeID)
	}
}

// setSessionModes converts legacy modes API response to config options format.
// This allows transparent support for both legacy modes and newer configOptions.
func (bs *BackgroundSession) setSessionModes(modes *acp.SessionModeState) {
	if modes == nil {
		return
	}

	// Convert legacy modes to a single "mode" config option
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

	bs.configMu.Lock()
	bs.configOptions = []SessionConfigOption{modeOption}
	bs.usesLegacyModes = true
	bs.configMu.Unlock()

	// Persist initial value to metadata
	bs.persistConfigValue(ConfigOptionCategoryMode, string(modes.CurrentModeId))
}

// setAgentModels converts agent model state to a "model" config option.
// This allows model switching to reuse the config option infrastructure.
func (bs *BackgroundSession) setAgentModels(models *acp.UnstableSessionModelState) {
	bs.agentModels = models
	if models == nil || len(models.AvailableModels) == 0 {
		return
	}

	// Convert models to config option values
	options := ModelsToConfigOptions(models)

	// Start with the agent's reported current model.
	// Pre-apply any matching constraint to local state immediately, so the UI shows
	// the desired model from the very first acp_started message — before the async
	// RPC in applyConfigConstraints completes. agentModels.CurrentModelId is NOT
	// updated here; applyConfigConstraints compares against it to know whether the
	// agent-side change still needs to happen.
	currentValue := string(models.CurrentModelId)
	if constraint, ok := bs.acpServerConstraints[ConfigOptionCategoryModel]; ok && constraint != nil && constraint.Pattern != "" {
		if matched := MatchConstraintOption(constraint, options); matched != "" && matched != currentValue {
			if bs.logger != nil {
				bs.logger.Debug("ACP server constraint: pre-applying model to local state",
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

	bs.configMu.Lock()
	// Remove any existing model option, then append the new one
	filtered := make([]SessionConfigOption, 0, len(bs.configOptions)+1)
	for _, opt := range bs.configOptions {
		if opt.Category != ConfigOptionCategoryModel {
			filtered = append(filtered, opt)
		}
	}
	bs.configOptions = append(filtered, modelOption)
	bs.configMu.Unlock()

	// Initialize baselineModel from persisted metadata (survive suspend/resume) or from the
	// agent's reported current model. Only set when empty so a prior call isn't overwritten.
	// applyConfigConstraints (called async below) will update baseline via SetConfigOption
	// if a constraint selects a different model.
	bs.modelMu.Lock()
	if bs.baselineModel == "" {
		baseline := string(models.CurrentModelId)
		if bs.store != nil && bs.persistedID != "" {
			if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil && meta.BaselineModel != "" {
				baseline = meta.BaselineModel
			}
		}
		bs.baselineModel = baseline
	}
	bs.modelMu.Unlock()

	// Apply any ACP server constraints for the model category
	go bs.applyConfigConstraints(ConfigOptionCategoryModel)
}
