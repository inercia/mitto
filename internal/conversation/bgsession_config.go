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

func (bs *BackgroundSession) applyConfigOption(ctx context.Context, configID, value string) error {
	return bs.configMgr.applyConfigOption(bs, ctx, configID, value)
}

func (bs *BackgroundSession) flushPendingConfig() {
	bs.configMgr.flushPendingConfig(bs)
}

func (bs *BackgroundSession) persistConfigValue(configID, value string) {
	bs.configMgr.persistConfigValue(bs, configID, value)
}

func (bs *BackgroundSession) persistBaselineModel(value string) {
	bs.configMgr.persistBaselineModel(bs, value)
}

func (bs *BackgroundSession) setActiveModelOnly(ctx context.Context, modelID string) error {
	return bs.configMgr.setActiveModelOnly(bs, ctx, modelID)
}

func (bs *BackgroundSession) restoreBaselineIfOverride() {
	bs.configMgr.restoreBaselineIfOverride(bs)
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
		_, err := bs.acpConn.UnstableSetSessionModel(ctx, acp.UnstableSetSessionModelRequest{
			SessionId: acp.SessionId(bs.acpID),
			ModelId:   acp.UnstableModelId(modelID),
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
	return string(bs.agentModels.CurrentModelId)
}
func (bs *BackgroundSession) cmSetCurrentModelID(id string) {
	if bs.agentModels != nil {
		bs.agentModels.CurrentModelId = acp.UnstableModelId(id)
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
