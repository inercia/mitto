package conversation

// ACP callback methods cluster for BackgroundSession: thin delegators to the
// acpCallbackSink collaborator, plus the acpCallbackDeps implementation that
// supplies it with the session's live dependencies.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// --- Thin delegators (preserve all method signatures: WebClient wires these directly) ---

// logAgentModels logs the agent's model state at DEBUG level.
func (bs *BackgroundSession) logAgentModels(models *SessionModelState) {
	bs.callbackSink.logAgentModels(bs, models)
}

// onContextUsageUpdate stores the latest context window usage and notifies all observers.
func (bs *BackgroundSession) onContextUsageUpdate(size, used int) {
	bs.callbackSink.onContextUsageUpdate(bs, size, used)
}

func (bs *BackgroundSession) onAgentMessage(seq int64, html, markdown string) {
	bs.callbackSink.onAgentMessage(bs, seq, html, markdown)
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
func (bs *BackgroundSession) setAgentModels(models *SessionModelState) {
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

// wireProcessorRunRecorder installs a processors.RunRecorder on this session's
// processorManager (if any) that appends each processor invocation as a
// session.EventTypeProcessorRun event (mitto-fm89 Stats tab). No-op if there
// is no processor manager. Safe to call multiple times (idempotent overwrite);
// callers invoke it once per BackgroundSession construction/resume, after the
// recorder is set up, so seq assignment and persistence are both available.
func (bs *BackgroundSession) wireProcessorRunRecorder() {
	if bs.processorManager == nil {
		return
	}
	bs.processorManager.SetRunRecorder(func(run processors.ProcessorRun) {
		seq := bs.getNextSeq()
		recordEventWithSeqHelper(bs.recorder, bs.logger, session.Event{
			Seq:       seq,
			Type:      session.EventTypeProcessorRun,
			Timestamp: time.Now(),
			Data: session.ProcessorRunData{
				Name:       run.Name,
				Phase:      run.Phase,
				Outcome:    run.Outcome,
				DurationMs: run.Duration.Milliseconds(),
				Error:      run.Error,
			},
		}, "processor run")
	})
}

// wireProcessorPendingDispatch installs the mitto-3421 durable pending-dispatch
// spool plus the mitto-exr/mitto-yfv8 notify seams on this session's
// processorManager (if any), mirroring the wiring
// SessionManager.ApplyOnCloseProcessors already does for the close-phase
// manager it builds locally. Without this, a LIVE session's after-phase
// (agentResponded/agentIdle) prompt-mode processor — driven by ApplyAfter,
// see fuApplyAfterProcessors — has nowhere to persist an undelivered batch
// once dispatchWithRetry exhausts its saturation retry budget: the batch is
// neither spooled for later retry nor surfaced to the user, it is simply
// gone (mitto-q95p). No-op if there is no processor manager. Safe to call
// multiple times (idempotent overwrite); callers invoke it once per
// BackgroundSession construction/resume, alongside wireProcessorRunRecorder.
//
// Also opportunistically flushes any batches spooled by an earlier saturated
// dispatch for this session's workspace (mitto-yfv8), fire-and-forget, so
// work stranded before this session existed (or during a prior saturation
// window on this same session) is retried as soon as a live session is
// available again instead of waiting for the workspace to close.
func (bs *BackgroundSession) wireProcessorPendingDispatch() {
	if bs.processorManager == nil {
		return
	}
	bs.processorManager.SetPendingDispatchStore(&processors.FilePendingDispatchStore{})
	bs.processorManager.SetNotifyFunc(func(_, name string, lastErr error) {
		_ = bs.UINotify(UINotifyRequest{
			Title:   "After-phase processor failed",
			Message: fmt.Sprintf("%q could not be dispatched after retries: %v", name, lastErr),
			Style:   "warning",
		})
	})
	bs.processorManager.SetLateDeliveryFunc(func(_ string, names []string) {
		_ = bs.UINotify(UINotifyRequest{
			Title:   "Deferred processor work delivered",
			Message: fmt.Sprintf("%d previously undelivered batch(es) were dispatched: %s", len(names), strings.Join(names, ", ")),
			Style:   "info",
		})
	})

	if bs.workspaceUUID != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			bs.processorManager.FlushPendingDispatches(ctx, bs.workspaceUUID)
		}()
	}
}

// cbRecordPermission records a permission decision via the recorder, using a
// seq reserved from the same authoritative counter (getNextSeq()) that
// streamed events use so it cannot collide with a seq reserved-but-not-yet-
// persisted for a concurrently streaming event (mitto-t7xv).
func (bs *BackgroundSession) cbRecordPermission(title, selectedOption, outcome string) {
	if bs.recorder == nil {
		return
	}
	bs.recorder.RecordPermissionWithSeq(bs.getNextSeq(), title, selectedOption, outcome)
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
func (bs *BackgroundSession) cbStoreAgentModels(models *SessionModelState) {
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
// preferring persisted metadata over the supplied default. When the baseline
// is seeded from defaultModel (no persisted value), it is also written back
// to session metadata so backfill and resume see the same value (mitto-9yl).
func (bs *BackgroundSession) cbInitBaselineModelIfEmpty(defaultModel string) {
	bs.modelMu.Lock()
	if bs.baselineModel != "" {
		bs.modelMu.Unlock()
		return
	}
	baseline := defaultModel
	fromPersisted := false
	if bs.store != nil && bs.persistedID != "" {
		if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil && meta.BaselineModel != "" {
			baseline = meta.BaselineModel
			fromPersisted = true
		}
	}
	bs.baselineModel = baseline
	bs.modelMu.Unlock()

	// Persist only when we seeded from the agent's currently-active model
	// (no prior persisted value) and it is non-empty. cmPersistBaselineModel
	// takes the store's own lock, so it must be called without holding modelMu.
	if !fromPersisted && baseline != "" {
		bs.cmPersistBaselineModel(baseline)
	}
}

// cbApplyConfigConstraintsAsync kicks off the async constraint-application
// goroutine for a category.
func (bs *BackgroundSession) cbApplyConfigConstraintsAsync(category string) {
	bs.startupConstraintPending.Add(1)
	generation, generationAware := bs.beginStartupConstraintAttempt()
	bs.startupConstraintWG.Add(1)
	go func() {
		defer bs.startupConstraintWG.Done()
		err := bs.applyConfigConstraints(category)
		bs.finishStartupConstraintAttempt(generation, generationAware, err)
		bs.startupConstraintPending.Add(-1)
	}()
}

// beginStartupConstraintAttempt opens a fresh failure epoch when a restarted
// shared ACP process advertises a newer generation. Failures remain sticky
// within one generation so a later successful callback cannot release queued
// turns past another unmet startup constraint (mitto-qori).
func (bs *BackgroundSession) beginStartupConstraintAttempt() (int, bool) {
	if bs.sharedProcess == nil {
		return 0, false
	}
	generation := bs.sharedProcess.Generation()
	bs.startupConstraintMu.Lock()
	if !bs.startupConstraintGenSet || generation > bs.startupConstraintGen {
		bs.startupConstraintGen = generation
		bs.startupConstraintGenSet = true
		bs.startupConstraintFailed.Store(false)
	}
	bs.startupConstraintMu.Unlock()
	return generation, true
}

func (bs *BackgroundSession) finishStartupConstraintAttempt(generation int, generationAware bool, err error) {
	if err == nil {
		return
	}
	bs.startupConstraintMu.Lock()
	defer bs.startupConstraintMu.Unlock()
	// Ignore a late failure from the replaced process after a newer generation
	// has already begun applying its own startup constraint.
	if !generationAware || generation == bs.startupConstraintGen {
		bs.startupConstraintFailed.Store(true)
	}
}

// initialModelApplyBudget bounds the SetSessionModel RPC issued to apply the
// workspace initial-model preference on fresh conversations. Kept generous so a
// cold agent still lands the switch, but capped so the goroutine does not
// linger indefinitely on a stuck ACP.
var initialModelApplyBudget = 90 * time.Second

// cbMaybeApplyInitialModelAsync applies the per-workspace initial-model
// preference (WorkspaceSettings → Initial Model) as the session's persistent
// baseline for FRESH conversations only. Skipped when:
//   - no preference is configured on the workspace;
//   - the session was resumed and already has a persisted BaselineModel;
//   - the workspace has an ACP server constraint on the model category (it wins
//     and would fight with our change on every resume);
//   - the preference cannot be resolved against the agent's available models.
//
// Applies via SetConfigOption so the change updates the baseline, persists to
// metadata, and emits a session_change timeline entry — identical to a manual
// UI selection.
//
// Priority axis is profile-list order: SelectPreferredModel walks each entry
// in initialModelPreference in order, and for each modelTag entry it walks
// config.ProfilesByTag(profiles, tag) — which preserves Config.Models order —
// picking the FIRST profile whose Criteria resolves against the session's
// available models. Reordering profiles in Config.Models flips which model
// wins for the same tag (mitto-ex7 "list order = priority" contract).
func (bs *BackgroundSession) cbMaybeApplyInitialModelAsync() {
	if len(bs.initialModelPreference) == 0 {
		return
	}
	// Skip resumed sessions: they already have a persisted baseline that reflects
	// prior manual selections (or a prior application of this same preference).
	if bs.store != nil && bs.persistedID != "" {
		if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil && meta.BaselineModel != "" {
			return
		}
	}
	// Skip when a workspace ACP server constraint already governs the model
	// category — it wins (see applyConfigConstraints) and re-runs on every
	// resume, so any change we make here would be immediately reverted.
	if constraint := bs.cbACPServerConstraint(ConfigOptionCategoryModel); constraint != nil && constraint.Pattern != "" {
		return
	}

	prefs := bs.initialModelPreference
	go func() {
		models := bs.agentModels
		if models == nil {
			return
		}
		var profiles []config.ModelProfile
		if bs.mittoConfig != nil {
			profiles = bs.mittoConfig.EffectiveModelProfiles()
		}
		resolved := SelectPreferredModel(prefs, profiles, models)
		if resolved == "" {
			if bs.logger != nil {
				bs.logger.Debug("initial model preference: no matching available model",
					"session_id", bs.persistedID,
					"preference", prefs)
			}
			return
		}
		if models.CurrentModelId == resolved {
			// Baseline is already the desired model — still record it in the persisted
			// baseline metadata so future resumes skip the constraint check above.
			bs.cmPersistBaselineModel(resolved)
			return
		}
		ctx, cancel := context.WithTimeout(bs.ctx, initialModelApplyBudget)
		defer cancel()
		if err := bs.configMgr.applyConfigOption(bs, ctx, ConfigOptionCategoryModel, resolved); err != nil {
			if bs.logger != nil {
				bs.logger.Warn("initial model preference: failed to apply",
					"session_id", bs.persistedID,
					"model", resolved,
					"error", err)
			}
			return
		}
		if bs.logger != nil {
			bs.logger.Info("initial model preference applied",
				"session_id", bs.persistedID,
				"model", resolved,
				"preference", prefs)
		}
	}()
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
