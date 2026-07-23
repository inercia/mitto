package conversation

// Config management cluster for BackgroundSession.
// All logic lives in config_manager.go (configManager collaborator).
// The methods below are thin delegators that pass bs as the configDeps seam.

import (
	"context"
	"fmt"
	"log/slog"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// =============================================================================
// Thin delegators
// =============================================================================

func (bs *BackgroundSession) applyConfigConstraints(category string) {
	bs.configMgr.applyConfigConstraints(bs, category)
}

// ConfigOptions returns a copy of all session config options.
func (bs *BackgroundSession) ConfigOptions() []SessionConfigOption {
	return bs.configMgr.configOptions(bs)
}

// GetConfigValue returns the current value for a specific config option.
func (bs *BackgroundSession) GetConfigValue(configID string) string {
	return bs.configMgr.getConfigValue(bs, configID)
}

// SetConfigOption changes a session config option value.
func (bs *BackgroundSession) SetConfigOption(ctx context.Context, configID, value string) error {
	return bs.configMgr.setConfigOption(bs, ctx, configID, value)
}

func (bs *BackgroundSession) flushPendingConfig() {
	bs.configMgr.flushPendingConfig(bs)
}

func (bs *BackgroundSession) persistConfigValue(configID, value string) {
	bs.configMgr.persistConfigValue(bs, configID, value)
}

func (bs *BackgroundSession) setActiveModelOnly(ctx context.Context, modelID string) error {
	return bs.configMgr.setActiveModelOnly(bs, ctx, modelID)
}

func (bs *BackgroundSession) restoreBaselineIfOverride() {
	bs.configMgr.restoreBaselineIfOverride(bs)
}

// ApplyModelTag resolves the given preferred-model tag against the agent's
// advertised model catalog (using the same SelectPreferredModel semantics as
// prompt-level preferredModels) and switches the session's active model via the
// same SetConfigOption path used by the user's manual model-dropdown click, so
// the change persists as the new baseline. An empty tag clears any transient
// prompt-level model override. Returns the resolved model id on success, "" when
// the tag was cleared, or an error when the agent has not advertised a model
// catalog, the tag does not resolve to any available model, or the underlying
// SetConfigOption call fails. Used by mcp tool handlers that would otherwise
// need to import conversation-package internals (avoids the mcpserver→conversation
// import cycle).
func (bs *BackgroundSession) ApplyModelTag(ctx context.Context, tag string) (string, error) {
	if tag == "" {
		bs.restoreBaselineIfOverride()
		return "", nil
	}
	models := bs.agentModels
	if models == nil {
		return "", fmt.Errorf("agent has not advertised a model catalog")
	}
	profiles := bs.mittoConfig.EffectiveModelProfiles()
	resolved := SelectPreferredModel(
		[]config.PromptPreferredModel{{ModelTag: tag}},
		profiles, models,
	)
	if resolved == "" {
		return "", fmt.Errorf("model_tag %q did not resolve to any available model", tag)
	}
	if resolved == models.CurrentModelId {
		return resolved, nil
	}
	if err := bs.SetConfigOption(ctx, string(ModelConfigId), resolved); err != nil {
		return "", fmt.Errorf("failed to apply model_tag %q: %w", tag, err)
	}
	return resolved, nil
}

// =============================================================================
// configDeps concrete implementation on *BackgroundSession
// =============================================================================

func (bs *BackgroundSession) cmSessionID() string           { return bs.persistedID }
func (bs *BackgroundSession) cmLogger() *slog.Logger        { return bs.logger }
func (bs *BackgroundSession) cmIsClosed() bool              { return bs.IsClosed() }
func (bs *BackgroundSession) cmHasParent() bool             { return bs.HasParent() }
func (bs *BackgroundSession) cmSessionCtx() context.Context { return bs.ctx }

func (bs *BackgroundSession) cmHasACPConn() bool {
	return bs.acpConn != nil || bs.sharedProcess != nil
}

func (bs *BackgroundSession) cmSetSessionMode(ctx context.Context, value string) error {
	if bs.sharedProcess != nil {
		return bs.sharedProcess.SetSessionMode(ctx, acp.SessionId(bs.acpID), value)
	}
	if bs.acpConn != nil {
		_, err := bs.acpConn.SetSessionMode(ctx, acp.SetSessionModeRequest{
			SessionId: acp.SessionId(bs.acpID),
			ModeId:    acp.SessionModeId(value),
		})
		return err
	}
	return fmt.Errorf("no ACP connection")
}

func (bs *BackgroundSession) cmSetSessionModel(ctx context.Context, modelID string) error {
	if bs.sharedProcess != nil {
		return bs.sharedProcess.SetSessionModel(ctx, acp.SessionId(bs.acpID), modelID)
	}
	if bs.acpConn != nil {
		cfgId := bs.modelConfigId
		if cfgId == "" {
			cfgId = ModelConfigId
		}
		_, err := bs.acpConn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
			ValueId: &acp.SetSessionConfigOptionValueId{
				SessionId: acp.SessionId(bs.acpID),
				ConfigId:  cfgId,
				Value:     acp.SessionConfigValueId(modelID),
			},
		})
		return err
	}
	return fmt.Errorf("no ACP connection")
}

func (bs *BackgroundSession) cmGetConfigOptions() []SessionConfigOption {
	bs.configMu.RLock()
	defer bs.configMu.RUnlock()
	if bs.configOptions == nil {
		return nil
	}
	result := make([]SessionConfigOption, len(bs.configOptions))
	copy(result, bs.configOptions)
	return result
}

func (bs *BackgroundSession) cmFindByID(id string) (SessionConfigOption, bool) {
	bs.configMu.RLock()
	defer bs.configMu.RUnlock()
	for _, opt := range bs.configOptions {
		if opt.ID == id {
			return opt, true
		}
	}
	return SessionConfigOption{}, false
}

func (bs *BackgroundSession) cmFindByCategory(cat string) (SessionConfigOption, bool) {
	bs.configMu.RLock()
	defer bs.configMu.RUnlock()
	for _, opt := range bs.configOptions {
		if opt.Category == cat {
			return opt, true
		}
	}
	return SessionConfigOption{}, false
}

func (bs *BackgroundSession) cmUsesLegacyModes() bool {
	bs.configMu.RLock()
	defer bs.configMu.RUnlock()
	return bs.usesLegacyModes
}

func (bs *BackgroundSession) cmUpdateConfigOptionValue(id, value string) {
	bs.configMu.Lock()
	defer bs.configMu.Unlock()
	for i := range bs.configOptions {
		if bs.configOptions[i].ID == id {
			bs.configOptions[i].CurrentValue = value
			return
		}
	}
}

func (bs *BackgroundSession) cmLockPendingConfig()   { bs.pendingConfigMu.Lock() }
func (bs *BackgroundSession) cmUnlockPendingConfig() { bs.pendingConfigMu.Unlock() }

func (bs *BackgroundSession) cmSetPendingEntry(id, value string) { bs.pendingConfig[id] = value }
func (bs *BackgroundSession) cmDeletePendingEntry(id string)     { delete(bs.pendingConfig, id) }

func (bs *BackgroundSession) cmDrainPendingConfig() map[string]string {
	bs.pendingConfigMu.Lock()
	defer bs.pendingConfigMu.Unlock()
	if len(bs.pendingConfig) == 0 {
		return nil
	}
	pending := bs.pendingConfig
	bs.pendingConfig = make(map[string]string)
	return pending
}

func (bs *BackgroundSession) cmLockPromptMu()     { bs.promptMu.Lock() }
func (bs *BackgroundSession) cmUnlockPromptMu()   { bs.promptMu.Unlock() }
func (bs *BackgroundSession) cmIsPrompting() bool { return bs.isPrompting }

func (bs *BackgroundSession) cmSetBaselineAndClearOverride(baseline string) {
	bs.modelMu.Lock()
	bs.baselineModel = baseline
	bs.overrideActive = false
	bs.modelMu.Unlock()
}

func (bs *BackgroundSession) cmTakeBaselineIfOverride() (string, bool) {
	bs.modelMu.Lock()
	defer bs.modelMu.Unlock()
	if !bs.overrideActive {
		return "", false
	}
	baseline := bs.baselineModel
	bs.overrideActive = false
	return baseline, true
}

func (bs *BackgroundSession) cmHasAgentModels() bool { return bs.agentModels != nil }
func (bs *BackgroundSession) cmGetCurrentModelID() string {
	if bs.agentModels == nil {
		return ""
	}
	return bs.agentModels.CurrentModelId
}
func (bs *BackgroundSession) cmSetCurrentModelID(id string) {
	if bs.agentModels != nil {
		bs.agentModels.CurrentModelId = id
	}
}

func (bs *BackgroundSession) cmGetACPServerConstraint(category string) *config.ACPServerConstraint {
	return bs.acpServerConstraints[category]
}

func (bs *BackgroundSession) cmPersistConfigValue(configID, value string) {
	if bs.store == nil {
		return
	}
	if configID == ConfigOptionCategoryMode {
		if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
			m.CurrentModeID = value
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to persist config value to metadata", "config_id", configID, "error", err)
		}
	}
}

func (bs *BackgroundSession) cmPersistBaselineModel(value string) {
	if bs.store == nil {
		return
	}
	if err := bs.store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
		m.BaselineModel = value
	}); err != nil && bs.logger != nil {
		bs.logger.Warn("Failed to persist baseline model", "model", value, "error", err)
	}
}

func (bs *BackgroundSession) cmNotifyConfigChanged(configID, value string) {
	if bs.onConfigChanged != nil {
		bs.onConfigChanged(bs.persistedID, configID, value)
	}
}

func (bs *BackgroundSession) cmRecordSessionChange(kind, value, previousValue string) {
	if bs.recorder == nil {
		return
	}
	bs.cmRecordSessionChangeWithSeq(bs.getNextSeq(), kind, value, previousValue)
}

// cmRecordSessionChangeWithSeq is the seq-aware variant of cmRecordSessionChange
// (mitto-c36). It persists a session-change timeline event using the caller-supplied
// seq (obtained from getNextSeq() upstream) and notifies observers with the same seq.
// Used to pre-reserve a "context_cleared" pill seq BEFORE the user-prompt seq so the
// flush pill orders before the user prompt in the persisted transcript.
func (bs *BackgroundSession) cmRecordSessionChangeWithSeq(seq int64, kind, value, previousValue string) {
	if bs.recorder == nil {
		return
	}
	data := session.SessionChangeData{Kind: kind, Value: value, PreviousValue: previousValue}
	if err := bs.recorder.RecordSessionChangeWithSeq(seq, data); err != nil {
		if bs.logger != nil {
			bs.logger.Error("Failed to record session change", "kind", kind, "value", value, "error", err)
		}
	}
	bs.notifyObservers(func(o SessionObserver) {
		if sc, ok := o.(SessionChangeObserver); ok {
			sc.OnSessionChange(seq, data)
		}
	})
}
