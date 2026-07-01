package conversation

// ACP callback methods cluster for BackgroundSession: thin delegators to the
// acpCallbackSink collaborator, plus the acpCallbackDeps implementation that
// supplies it with the session's live dependencies.

import (
	"context"
	"log/slog"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// --- Thin delegators (preserve all method signatures: WebClient wires these directly) ---

// logAgentModels logs the agent's model state at DEBUG level.
func (bs *BackgroundSession) logAgentModels(models *acp.UnstableSessionModelState) {
	bs.callbackSink.logAgentModels(bs, models)
}

// onContextUsageUpdate stores the latest context window usage and notifies all observers.
func (bs *BackgroundSession) onContextUsageUpdate(size, used int) {
	bs.callbackSink.onContextUsageUpdate(bs, size, used)
}

func (bs *BackgroundSession) onAgentMessage(seq int64, html string) {
	bs.callbackSink.onAgentMessage(bs, seq, html)
}

func (bs *BackgroundSession) onAgentThought(seq int64, text string) {
	bs.callbackSink.onAgentThought(bs, seq, text)
}

func (bs *BackgroundSession) onToolCall(seq int64, id, title, status string) {
	bs.trackToolCallStatus(id, title, status)
	bs.callbackSink.onToolCall(bs, seq, id, title, status)
}

func (bs *BackgroundSession) onMittoToolCall(requestID string) {
	bs.callbackSink.onMittoToolCall(bs, requestID)
}

func (bs *BackgroundSession) onToolUpdate(seq int64, id string, status *string) {
	if status != nil {
		bs.trackToolCallStatus(id, "", *status)
	}
	bs.callbackSink.onToolUpdate(bs, seq, id, status)
}

func (bs *BackgroundSession) onPlan(seq int64, entries []PlanEntry) {
	bs.callbackSink.onPlan(bs, seq, entries)
}

func (bs *BackgroundSession) onFileWrite(seq int64, path string, size int) {
	bs.callbackSink.onFileWrite(bs, seq, path, size)
}

func (bs *BackgroundSession) onFileRead(seq int64, path string, size int) {
	bs.callbackSink.onFileRead(bs, seq, path, size)
}

func (bs *BackgroundSession) onPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return bs.callbackSink.onPermission(bs, ctx, params)
}

// onAvailableCommands handles the available slash commands update from the agent.
// It stores the commands and notifies all observers.
func (bs *BackgroundSession) onAvailableCommands(commands []AvailableCommand) {
	bs.callbackSink.onAvailableCommands(bs, commands)
}

// AvailableCommands returns the current list of available slash commands.
// The commands are sorted alphabetically by name.
func (bs *BackgroundSession) AvailableCommands() []AvailableCommand {
	return bs.callbackSink.availableCommands(bs)
}

// onCurrentModeChanged handles the session mode change notification from the agent.
// This updates the stored config option and notifies observers.
// This is called for legacy modes API - converts to config option format internally.
func (bs *BackgroundSession) onCurrentModeChanged(modeID string) {
	bs.callbackSink.onCurrentModeChanged(bs, modeID)
}

// setSessionModes converts legacy modes API response to config options format.
// This allows transparent support for both legacy modes and newer configOptions.
func (bs *BackgroundSession) setSessionModes(modes *acp.SessionModeState) {
	bs.callbackSink.setSessionModes(bs, modes)
}

// setAgentModels converts agent model state to a "model" config option.
// This allows model switching to reuse the config option infrastructure.
func (bs *BackgroundSession) setAgentModels(models *acp.UnstableSessionModelState) {
	bs.callbackSink.setAgentModels(bs, models)
}

// --- acpCallbackDeps implementation (live deps for acpCallbackSink) ---

// cbIsClosed reports whether the session has been closed.
func (bs *BackgroundSession) cbIsClosed() bool { return bs.IsClosed() }

// cbSessionID returns the persisted session ID.
func (bs *BackgroundSession) cbSessionID() string { return bs.persistedID }

// cbLogger returns the session-scoped logger.
func (bs *BackgroundSession) cbLogger() *slog.Logger { return bs.logger }

// cbNotifyObservers broadcasts a callback to all registered session observers.
func (bs *BackgroundSession) cbNotifyObservers(fn func(SessionObserver)) {
	bs.notifyObservers(fn)
}

// cbObserverCount returns the number of currently registered observers.
func (bs *BackgroundSession) cbObserverCount() int { return bs.ObserverCount() }

// cbHasObservers reports whether any observer is currently registered.
func (bs *BackgroundSession) cbHasObservers() bool { return bs.HasObservers() }

// cbRecordEventWithSeq persists an event with a pre-assigned sequence number.
func (bs *BackgroundSession) cbRecordEventWithSeq(event session.Event, kind string) {
	recordEventWithSeqHelper(bs.recorder, bs.logger, event, kind)
}

// cbRecordPermission records a permission decision via the recorder.
func (bs *BackgroundSession) cbRecordPermission(title, selectedOption, outcome string) {
	if bs.recorder == nil {
		return
	}
	bs.recorder.RecordPermission(title, selectedOption, outcome)
}

// cbSetContextUsage stores the latest context window usage atomically.
func (bs *BackgroundSession) cbSetContextUsage(size, used int) {
	bs.contextUsageMu.Lock()
	bs.contextSize = size
	bs.contextUsed = used
	bs.contextUsageMu.Unlock()
}

// cbSetAvailableCommands stores the current list of available commands.
func (bs *BackgroundSession) cbSetAvailableCommands(cmds []AvailableCommand) {
	bs.availableCommandsMu.Lock()
	bs.availableCommands = cmds
	bs.availableCommandsMu.Unlock()
}

// cbGetAvailableCommands returns a defensive copy of the current commands.
func (bs *BackgroundSession) cbGetAvailableCommands() []AvailableCommand {
	bs.availableCommandsMu.RLock()
	defer bs.availableCommandsMu.RUnlock()
	if bs.availableCommands == nil {
		return nil
	}
	result := make([]AvailableCommand, len(bs.availableCommands))
	copy(result, bs.availableCommands)
	return result
}

// cbRegisterPendingMCPRequest associates a mitto_* tool request with this
// session via the global MCP server. Returns false if no MCP server is wired.
func (bs *BackgroundSession) cbRegisterPendingMCPRequest(requestID string) bool {
	if bs.globalMcpServer == nil {
		return false
	}
	bs.globalMcpServer.RegisterPendingRequest(requestID, bs.persistedID)
	return true
}

// cbNotifyPlanStateChanged invokes the SessionManager plan-state cache callback
// if one was configured.
func (bs *BackgroundSession) cbNotifyPlanStateChanged(entries []PlanEntry) {
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, entries)
	}
}

// cbAutoApprove reports whether the global auto-approve flag is set.
func (bs *BackgroundSession) cbAutoApprove() bool { return bs.autoApprove }

// cbSessionAutoApprovePermissions reports whether the per-session
// auto-approve flag is enabled in metadata.
func (bs *BackgroundSession) cbSessionAutoApprovePermissions() bool {
	if bs.store == nil || bs.persistedID == "" {
		return false
	}
	meta, err := bs.store.GetMetadata(bs.persistedID)
	if err != nil {
		return false
	}
	return session.GetFlagValue(meta.AdvancedSettings, session.FlagAutoApprovePermissions)
}

// cbUIPrompt forwards to the unified UI prompt system.
func (bs *BackgroundSession) cbUIPrompt(ctx context.Context, req UIPromptRequest) (UIPromptResponse, error) {
	return bs.UIPrompt(ctx, req)
}

// cbSetModeCurrentValue updates the mode config option's CurrentValue.
func (bs *BackgroundSession) cbSetModeCurrentValue(modeID string) {
	bs.configMu.Lock()
	for i := range bs.configOptions {
		if bs.configOptions[i].Category == ConfigOptionCategoryMode {
			bs.configOptions[i].CurrentValue = modeID
			break
		}
	}
	bs.configMu.Unlock()
}

// cbPersistConfigValue persists a config-option value to metadata.
func (bs *BackgroundSession) cbPersistConfigValue(configID, value string) {
	bs.persistConfigValue(configID, value)
}

// cbNotifyConfigChanged invokes the on-config-changed callback if configured.
func (bs *BackgroundSession) cbNotifyConfigChanged(configID, value string) {
	if bs.onConfigChanged != nil {
		bs.onConfigChanged(bs.persistedID, configID, value)
	}
}

// cbSetLegacyModes replaces configOptions with a single mode entry and flips
// usesLegacyModes to true.
func (bs *BackgroundSession) cbSetLegacyModes(modeOption SessionConfigOption) {
	bs.configMu.Lock()
	bs.configOptions = []SessionConfigOption{modeOption}
	bs.usesLegacyModes = true
	bs.configMu.Unlock()
}

// cbStoreAgentModels stores the raw agent model state reference.
func (bs *BackgroundSession) cbStoreAgentModels(models *acp.UnstableSessionModelState) {
	bs.agentModels = models
}

// cbACPServerConstraint returns the constraint for a category (may be nil).
func (bs *BackgroundSession) cbACPServerConstraint(category string) *config.ACPServerConstraint {
	if bs.acpServerConstraints == nil {
		return nil
	}
	return bs.acpServerConstraints[category]
}

// cbReplaceModelConfigOption removes any existing model config option and
// appends the new one.
func (bs *BackgroundSession) cbReplaceModelConfigOption(modelOption SessionConfigOption) {
	bs.configMu.Lock()
	filtered := make([]SessionConfigOption, 0, len(bs.configOptions)+1)
	for _, opt := range bs.configOptions {
		if opt.Category != ConfigOptionCategoryModel {
			filtered = append(filtered, opt)
		}
	}
	bs.configOptions = append(filtered, modelOption)
	bs.configMu.Unlock()
}

// cbInitBaselineModelIfEmpty initialises baselineModel if it is still empty,
// preferring persisted metadata over the supplied default.
func (bs *BackgroundSession) cbInitBaselineModelIfEmpty(defaultModel string) {
	bs.modelMu.Lock()
	defer bs.modelMu.Unlock()
	if bs.baselineModel != "" {
		return
	}
	baseline := defaultModel
	if bs.store != nil && bs.persistedID != "" {
		if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil && meta.BaselineModel != "" {
			baseline = meta.BaselineModel
		}
	}
	bs.baselineModel = baseline
}

// cbApplyConfigConstraintsAsync kicks off the async constraint-application
// goroutine for a category.
func (bs *BackgroundSession) cbApplyConfigConstraintsAsync(category string) {
	go bs.applyConfigConstraints(category)
}

// cbStreamingSuppressed reports whether streaming callbacks are currently suppressed
// (i.e. during an in-place context flush). Used by acpCallbackSink to short-circuit.
func (bs *BackgroundSession) cbStreamingSuppressed() bool {
	bs.streamingSuppressedMu.Lock()
	defer bs.streamingSuppressedMu.Unlock()
	return bs.streamingSuppressed
}

// setStreamingSuppressed sets the streaming-suppression flag. When true all
// streaming callbacks (onAgentMessage, onToolCall, etc.) are no-ops so the
// flush turn stays out of the recorder, observers, and the transcript.
func (bs *BackgroundSession) setStreamingSuppressed(v bool) {
	bs.streamingSuppressedMu.Lock()
	bs.streamingSuppressed = v
	bs.streamingSuppressedMu.Unlock()
}
