package conversation

// Config management cluster for BackgroundSession.

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// constraintModelSwitchCallerBudget is the context timeout for the async ACP-server
// constraint auto-select model switch in applyConfigConstraints (mitto-f7q, Option 4).
// Budget reasoning (mirrors internal/web's setModelAsyncCallerBudget; this package must
// NOT import internal/web): the capacity-1 setModelSem may be held by up to ~3 concurrent
// callers, each taking at most ~25s (3×8s per-attempt + jitter). Semaphore wait ≤ 75s;
// adding slack for our own retries gives ~100s worst-case. 90s covers the expected
// wakeup contention (≤4 concurrent sessions). This widens ONLY the WAIT budget for a
// queued caller; it does NOT change the per-attempt 8s RPC deadline (Option 1 / widening
// per-attempt deadlines is explicitly discouraged by mitto-f7q because it lengthens the
// semaphore hold).
const constraintModelSwitchCallerBudget = 90 * time.Second

// constraintModelSwitchChildStartupJitter bounds a randomized startup delay applied to
// the constraint-driven main-session model switch for CHILD sessions only (mitto-x4e).
// When a periodic run spawns several children simultaneously (e.g. the Market Pulse
// 08:01 run spawns ~4 children at once) each child's ACP init fires a set_model RPC in
// the same instant, herding on the capacity-1 setModelSem so peers exhaust their caller
// budget before they can be served. Spreading these initial calls over a few seconds
// de-correlates the herd so they queue smoothly instead of colliding. This complements
// mitto-f7q (which widened the wait budget and jittered retries but left the FIRST
// attempts synchronized). Top-level (parent-less) sessions skip the jitter and switch
// immediately — a single interactive session never herds, so it pays no startup latency.
const constraintModelSwitchChildStartupJitter = 5 * time.Second

// childStartupJitter returns a randomized startup delay in [0, max) used to de-stagger
// concurrent child model switches (mitto-x4e). It returns 0 when max <= 0.
func childStartupJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}

// lookupACPServerConstraints returns the auto-selection constraints for the named
// ACP server in the given config, or nil if cfg is nil or no matching server is found.
func lookupACPServerConstraints(cfg *config.Config, serverName string) map[string]*config.ACPServerConstraint {
	if cfg == nil {
		return nil
	}
	for _, srv := range cfg.ACPServers {
		if srv.Name == serverName {
			return srv.Constraints
		}
	}
	return nil
}

// applyConfigConstraints checks ACP server constraints and auto-selects matching config option values.
// Called after config options (like models) become available during ACP initialization.
// Only applies constraints for config option categories that are present in the constraints map.
func (bs *BackgroundSession) applyConfigConstraints(category string) {
	if len(bs.acpServerConstraints) == 0 {
		return
	}

	constraint, ok := bs.acpServerConstraints[category]
	if !ok || constraint == nil || constraint.Pattern == "" {
		return
	}

	bs.configMu.RLock()
	var targetOption *SessionConfigOption
	for i := range bs.configOptions {
		if bs.configOptions[i].Category == category {
			targetOption = &bs.configOptions[i]
			break
		}
	}
	bs.configMu.RUnlock()

	if targetOption == nil || len(targetOption.Options) == 0 {
		return
	}

	matchedValue := MatchConstraintOption(constraint, targetOption.Options)

	if matchedValue == "" {
		if bs.logger != nil {
			bs.logger.Warn("ACP server constraint: no matching option found",
				"category", category,
				"match_mode", constraint.MatchMode,
				"pattern", constraint.Pattern,
				"available_count", len(targetOption.Options))
		}
		return
	}

	// Skip if the agent already has the matching value.
	// For the model category, compare against agentModels.CurrentModelId (the agent's actual
	// current model) rather than the local configOption.CurrentValue, which may have been
	// pre-applied optimistically in setAgentModels before the RPC completed. This ensures
	// the RPC still fires even when local state was eagerly set to the desired model.
	alreadySet := targetOption.CurrentValue == matchedValue
	if category == ConfigOptionCategoryModel && bs.agentModels != nil {
		alreadySet = string(bs.agentModels.CurrentModelId) == matchedValue
	}
	if alreadySet {
		if bs.logger != nil {
			bs.logger.Debug("ACP server constraint: already set to matching value",
				"category", category,
				"value", matchedValue)
		}
		return
	}

	if bs.logger != nil {
		bs.logger.Info("ACP server constraint: auto-selecting option",
			"category", category,
			"match_mode", constraint.MatchMode,
			"pattern", constraint.Pattern,
			"selected_value", matchedValue)
	}

	// De-stagger concurrent child startups (mitto-x4e): when a periodic run spawns
	// several children at once they would otherwise all hit the capacity-1 setModelSem
	// in the same instant. A small randomized delay (child sessions only) spreads the
	// initial set_model calls over a few seconds so they queue smoothly. Parent-less
	// (top-level/interactive) sessions skip this so they switch immediately. The wait
	// happens before the caller-budget context below, so it does not consume that budget.
	if bs.HasParent() {
		if jitter := childStartupJitter(constraintModelSwitchChildStartupJitter); jitter > 0 {
			if bs.logger != nil {
				bs.logger.Debug("ACP server constraint: staggering child startup model switch",
					"category", category,
					"jitter_ms", jitter.Milliseconds())
			}
			select {
			case <-time.After(jitter):
			case <-bs.ctx.Done():
				return
			}
		}
	}

	// Use a background context since this is called during initialization.
	// The caller budget accommodates set_model retries queued behind concurrent
	// callers on the capacity-1 setModelSem at server wakeup (mitto-f7q, Option 4).
	ctx, cancel := context.WithTimeout(context.Background(), constraintModelSwitchCallerBudget)
	defer cancel()

	if err := bs.SetConfigOption(ctx, category, matchedValue); err != nil {
		// Best-effort: the constraint auto-select is off the prompt critical path, so a
		// failure degrades gracefully — the session falls back to the current/baseline
		// model (consistent with the aux and per-prompt model-switch paths).
		if bs.logger != nil {
			bs.logger.Warn("ACP server constraint: failed to auto-select option (best-effort, falling back to current model)",
				"category", category,
				"value", matchedValue,
				"error", err)
		}
	}
}

// ConfigOptions returns a copy of all session config options.
func (bs *BackgroundSession) ConfigOptions() []SessionConfigOption {
	bs.configMu.RLock()
	defer bs.configMu.RUnlock()

	if bs.configOptions == nil {
		return nil
	}
	result := make([]SessionConfigOption, len(bs.configOptions))
	copy(result, bs.configOptions)
	return result
}

// GetConfigValue returns the current value for a specific config option.
func (bs *BackgroundSession) GetConfigValue(configID string) string {
	bs.configMu.RLock()
	defer bs.configMu.RUnlock()

	for _, opt := range bs.configOptions {
		if opt.ID == configID {
			return opt.CurrentValue
		}
	}
	return ""
}

// SetConfigOption changes a session config option value.
// For legacy modes (category "mode"), this calls SetSessionMode.
// For future configOptions API, it would call SetConfigOption.
func (bs *BackgroundSession) SetConfigOption(ctx context.Context, configID, value string) error {
	if bs.IsClosed() {
		return fmt.Errorf("session is closed")
	}

	if bs.acpConn == nil && bs.sharedProcess == nil {
		return fmt.Errorf("no ACP connection")
	}

	// Find the config option and validate the value
	bs.configMu.RLock()
	var found *SessionConfigOption
	for i := range bs.configOptions {
		if bs.configOptions[i].ID == configID {
			found = &bs.configOptions[i]
			break
		}
	}
	bs.configMu.RUnlock()

	if found == nil {
		return fmt.Errorf("unknown config option: %s", configID)
	}

	// Validate the value is one of the allowed options
	valid := false
	for _, opt := range found.Options {
		if opt.Value == value {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid value for %s: %s", configID, value)
	}

	// While the agent is prompting, defer the real ACP RPC to the prompting→idle
	// transition (flushPendingConfig). We still reflect the new value optimistically
	// in local state and broadcast it so the UI updates immediately. Last-write-wins
	// per configID. The isPrompting check and the pending-store write are performed
	// under promptMu (with pendingConfigMu nested) so a change racing turn-end is not
	// silently dropped: the completion path flips isPrompting under the same promptMu
	// before flushing, so either we record the pending value before the flip (flush
	// will drain it) or we observe the post-flip idle state and apply immediately.
	bs.promptMu.Lock()
	if bs.isPrompting {
		bs.pendingConfigMu.Lock()
		bs.pendingConfig[configID] = value
		bs.pendingConfigMu.Unlock()
		bs.promptMu.Unlock()

		// Optimistically reflect the pending value locally and broadcast it.
		bs.configMu.Lock()
		for i := range bs.configOptions {
			if bs.configOptions[i].ID == configID {
				bs.configOptions[i].CurrentValue = value
				break
			}
		}
		bs.configMu.Unlock()

		bs.persistConfigValue(configID, value)

		if bs.logger != nil {
			bs.logger.Info("Config option change deferred while prompting",
				"config_id", configID,
				"value", value)
		}

		// User-originated model change: update baseline immediately so that the restore-on-idle
		// path targets the new model, not the previously selected one.
		if found.Category == ConfigOptionCategoryModel {
			bs.modelMu.Lock()
			bs.baselineModel = value
			bs.overrideActive = false
			bs.modelMu.Unlock()
			bs.persistBaselineModel(value)
		}

		if bs.onConfigChanged != nil {
			bs.onConfigChanged(bs.persistedID, configID, value)
		}

		return nil
	}
	bs.promptMu.Unlock()

	// Idle: a fresh immediate change supersedes any value still parked in the pending
	// store from a just-finished turn, so it cannot be overwritten by a later flush.
	bs.pendingConfigMu.Lock()
	delete(bs.pendingConfig, configID)
	bs.pendingConfigMu.Unlock()

	return bs.applyConfigOption(ctx, configID, value)
}

// applyConfigOption issues the real ACP RPC for a config change, then updates local
// state, persists, and broadcasts. The value must already be validated by the caller.
// It is used both for the immediate (idle) path and the deferred flush path.
func (bs *BackgroundSession) applyConfigOption(ctx context.Context, configID, value string) error {
	bs.configMu.RLock()
	category := ""
	for i := range bs.configOptions {
		if bs.configOptions[i].ID == configID {
			category = bs.configOptions[i].Category
			break
		}
	}
	bs.configMu.RUnlock()

	// Determine how to set the value based on the category and API availability
	if category == ConfigOptionCategoryMode && bs.usesLegacyModes {
		// Use legacy SetSessionMode API
		var err error
		if bs.sharedProcess != nil {
			err = bs.sharedProcess.SetSessionMode(ctx, acp.SessionId(bs.acpID), value)
		} else if bs.acpConn != nil {
			_, err = bs.acpConn.SetSessionMode(ctx, acp.SetSessionModeRequest{
				SessionId: acp.SessionId(bs.acpID),
				ModeId:    acp.SessionModeId(value),
			})
		} else {
			return fmt.Errorf("no ACP connection")
		}
		if err != nil {
			if bs.logger != nil {
				bs.logger.Error("Failed to set session mode",
					"config_id", configID,
					"value", value,
					"error", err)
			}
			return fmt.Errorf("failed to set %s: %w", configID, err)
		}
	} else if category == ConfigOptionCategoryModel {
		// Use UNSTABLE SetSessionModel API
		var err error
		if bs.sharedProcess != nil {
			err = bs.sharedProcess.SetSessionModel(ctx, acp.SessionId(bs.acpID), value)
		} else if bs.acpConn != nil {
			_, err = bs.acpConn.UnstableSetSessionModel(ctx, acp.UnstableSetSessionModelRequest{
				SessionId: acp.SessionId(bs.acpID),
				ModelId:   acp.UnstableModelId(value),
			})
		} else {
			return fmt.Errorf("no ACP connection")
		}
		if err != nil {
			if bs.logger != nil {
				bs.logger.Error("Failed to set session model",
					"config_id", configID,
					"value", value,
					"error", err)
			}
			return fmt.Errorf("failed to set %s: %w", configID, err)
		}

		// Update the internal agentModels state to reflect the new current model
		if bs.agentModels != nil {
			bs.agentModels.CurrentModelId = acp.UnstableModelId(value)
		}

		// User-originated model change: update baseline so restore-on-idle targets the
		// right model. This covers both the immediate path and the deferred-flush path
		// (flushPendingConfig calls applyConfigOption after the prompt goroutine exits).
		bs.modelMu.Lock()
		bs.baselineModel = value
		bs.overrideActive = false
		bs.modelMu.Unlock()
		bs.persistBaselineModel(value)
	} else {
		// Future: Use SetConfigOption API when available in SDK
		return fmt.Errorf("config option %s is not supported by current agent", configID)
	}

	// Update local state
	bs.configMu.Lock()
	for i := range bs.configOptions {
		if bs.configOptions[i].ID == configID {
			bs.configOptions[i].CurrentValue = value
			break
		}
	}
	bs.configMu.Unlock()

	// Persist to metadata
	bs.persistConfigValue(configID, value)

	if bs.logger != nil {
		bs.logger.Info("Config option changed",
			"config_id", configID,
			"value", value)
	}

	// Notify callback
	if bs.onConfigChanged != nil {
		bs.onConfigChanged(bs.persistedID, configID, value)
	}

	return nil
}

// flushPendingConfig issues the real ACP RPC for any config changes that were
// deferred while the agent was prompting. It runs on the prompting→idle transition,
// BEFORE the next queued message is dispatched, so the queued prompt runs under the
// new configuration. Last-write-wins per configID (one value per option).
func (bs *BackgroundSession) flushPendingConfig() {
	bs.pendingConfigMu.Lock()
	if len(bs.pendingConfig) == 0 {
		bs.pendingConfigMu.Unlock()
		return
	}
	pending := bs.pendingConfig
	bs.pendingConfig = make(map[string]string)
	bs.pendingConfigMu.Unlock()

	// SetSessionModel can be slow; mirror the 30s budget used by the handler.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for configID, value := range pending {
		if err := bs.applyConfigOption(ctx, configID, value); err != nil {
			if bs.logger != nil {
				bs.logger.Error("Failed to flush deferred config option",
					"config_id", configID,
					"value", value,
					"error", err)
			}
		}
	}
}

// persistConfigValue saves a config option value to metadata.
func (bs *BackgroundSession) persistConfigValue(configID, value string) {
	if bs.store == nil {
		return
	}

	// For mode category, store in CurrentModeID for backward compatibility
	if configID == ConfigOptionCategoryMode {
		if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
			m.CurrentModeID = value
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to persist config value to metadata",
				"config_id", configID,
				"error", err)
		}
	}
	// Future: For other config options, store in a ConfigValues map
}

// persistBaselineModel persists the user's intended model to metadata so it survives
// suspend/resume cycles.
func (bs *BackgroundSession) persistBaselineModel(value string) {
	if bs.store == nil {
		return
	}
	if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
		m.BaselineModel = value
	}); err != nil && bs.logger != nil {
		bs.logger.Warn("Failed to persist baseline model", "model", value, "error", err)
	}
}

// setActiveModelOnly issues a SetSessionModel ACP call and updates local state, but does
// NOT update baselineModel or overrideActive. Used exclusively for per-prompt model
// overrides driven by preferredModels frontmatter.
func (bs *BackgroundSession) setActiveModelOnly(ctx context.Context, modelID string) error {
	var err error
	if bs.sharedProcess != nil {
		err = bs.sharedProcess.SetSessionModel(ctx, acp.SessionId(bs.acpID), modelID)
	} else if bs.acpConn != nil {
		_, err = bs.acpConn.UnstableSetSessionModel(ctx, acp.UnstableSetSessionModelRequest{
			SessionId: acp.SessionId(bs.acpID),
			ModelId:   acp.UnstableModelId(modelID),
		})
	} else {
		return fmt.Errorf("no ACP connection")
	}
	if err != nil {
		return fmt.Errorf("failed to set model: %w", err)
	}

	// Update agentModels and local config option state (mirrors applyConfigOption for model).
	if bs.agentModels != nil {
		bs.agentModels.CurrentModelId = acp.UnstableModelId(modelID)
	}
	bs.configMu.Lock()
	for i := range bs.configOptions {
		if bs.configOptions[i].Category == ConfigOptionCategoryModel {
			bs.configOptions[i].CurrentValue = modelID
			break
		}
	}
	bs.configMu.Unlock()

	if bs.onConfigChanged != nil {
		bs.onConfigChanged(bs.persistedID, ConfigOptionCategoryModel, modelID)
	}
	return nil
}

// restoreBaselineIfOverride restores the session model to baselineModel when an override
// is active (set by a prior preferredModels prompt). Called in processNextQueuedMessage
// when the queue drains so the UI always reflects the user's intended model while idle.
func (bs *BackgroundSession) restoreBaselineIfOverride() {
	bs.modelMu.Lock()
	if !bs.overrideActive {
		bs.modelMu.Unlock()
		return
	}
	baseline := bs.baselineModel
	bs.overrideActive = false
	bs.modelMu.Unlock()

	if baseline == "" || bs.agentModels == nil {
		return
	}
	if string(bs.agentModels.CurrentModelId) == baseline {
		return // Already at baseline, no RPC needed
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if setErr := bs.setActiveModelOnly(ctx, baseline); setErr != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to restore baseline model after queue drain",
				"baseline", baseline, "error", setErr)
		}
	} else if bs.logger != nil {
		bs.logger.Info("Restored baseline model after queue drain", "model", baseline)
	}
}
