package conversation

// Session-config / model-baseline collaborator — stateless; state lives on BackgroundSession.

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/inercia/mitto/internal/config"
)

// constraintModelSwitchCallerBudget is the context timeout for the async ACP-server
// constraint auto-select model switch (mitto-f7q, Option 4).
const constraintModelSwitchCallerBudget = 90 * time.Second

// constraintModelSwitchChildStartupJitter bounds the randomized startup delay for child sessions.
const constraintModelSwitchChildStartupJitter = 5 * time.Second

// childStartupJitter returns a randomized startup delay in [0, max) (mitto-x4e).
func childStartupJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}

// lookupACPServerConstraints returns the auto-selection constraints for the named ACP server.
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

// lookupContextFlushCommand returns the agent-native context-flush command (e.g.
// "/clear") configured for the named ACP server, or "" when none is configured.
func lookupContextFlushCommand(cfg *config.Config, serverName string) string {
	if cfg == nil {
		return ""
	}
	for _, srv := range cfg.ACPServers {
		if srv.Name == serverName {
			return srv.ContextFlushCommand
		}
	}
	return ""
}

// configDeps is the minimal interface configManager needs from BackgroundSession.
// All methods are prefixed with "cm" to avoid clashes with BackgroundSession's public API.
type configDeps interface {
	// Identity / lifecycle
	cmSessionID() string
	cmLogger() *slog.Logger
	cmIsClosed() bool
	cmHasParent() bool
	cmSessionCtx() context.Context

	// ACP connection check (true if any ACP pathway is available)
	cmHasACPConn() bool

	// ACP RPCs — dispatch to sharedProcess or direct conn, both nil-guarded
	cmSetSessionMode(ctx context.Context, value string) error
	cmSetSessionModel(ctx context.Context, modelID string) error

	// Config options — locked reads (RLock/RUnlock inside impl)
	cmGetConfigOptions() []SessionConfigOption
	cmFindByID(id string) (SessionConfigOption, bool)
	cmFindByCategory(cat string) (SessionConfigOption, bool)
	cmUsesLegacyModes() bool

	// Config options — locked write (Lock/Unlock inside impl)
	cmUpdateConfigOptionValue(id, value string)

	// Pending config — individual ops for exact promptMu→pendingConfigMu ordering
	cmLockPendingConfig()
	cmUnlockPendingConfig()
	cmSetPendingEntry(id, value string)      // caller holds pendingConfigMu
	cmDeletePendingEntry(id string)          // caller holds pendingConfigMu
	cmDrainPendingConfig() map[string]string // Lock + drain + Unlock (for flushPendingConfig)

	// Prompting check — individual ops for exact promptMu ordering
	cmLockPromptMu()
	cmUnlockPromptMu()
	cmIsPrompting() bool // caller holds promptMu

	// Model state — atomic ops
	cmSetBaselineAndClearOverride(baseline string)                   // modelMu.Lock + update + Unlock
	cmTakeBaselineIfOverride() (baseline string, wasOverriding bool) // modelMu.Lock + check + drain + Unlock
	cmHasAgentModels() bool
	cmGetCurrentModelID() string   // reads agentModels.CurrentModelId; no extra lock
	cmSetCurrentModelID(id string) // writes agentModels.CurrentModelId; no extra lock; nil-safe

	// ACP server constraint lookup
	cmGetACPServerConstraint(category string) *config.ACPServerConstraint

	// Persistence helpers (no-ops when no store)
	cmPersistConfigValue(configID, value string)
	cmPersistBaselineModel(value string)

	// Config changed notification (no-op when hook not set)
	cmNotifyConfigChanged(configID, value string)

	// Record a user-initiated session change to the timeline and push it live to
	// observers (no-op when no recorder). Generic: kind discriminates the change.
	cmRecordSessionChange(kind, value, previousValue string)
}

// configManager is a stateless collaborator owning session-config + model-baseline logic.
type configManager struct{}

func (c configManager) configOptions(d configDeps) []SessionConfigOption {
	return d.cmGetConfigOptions()
}

func (c configManager) getConfigValue(d configDeps, configID string) string {
	opt, ok := d.cmFindByID(configID)
	if !ok {
		return ""
	}
	return opt.CurrentValue
}

func (c configManager) setConfigOption(d configDeps, ctx context.Context, configID, value string) error {
	return c.setConfigOptionWithOpts(d, ctx, configID, value, true)
}

// setConfigOptionWithOpts is the core of setConfigOption. recordTimeline controls
// whether a model change emits a user-facing session_change timeline pill. The
// startup/constraint auto-select path passes false so re-selecting the configured
// model on every session resume does not repeat an identical "Model changed" pill.
func (c configManager) setConfigOptionWithOpts(d configDeps, ctx context.Context, configID, value string, recordTimeline bool) error {
	if d.cmIsClosed() {
		return fmt.Errorf("session is closed")
	}
	if !d.cmHasACPConn() {
		return fmt.Errorf("no ACP connection")
	}

	found, ok := d.cmFindByID(configID)
	if !ok {
		return fmt.Errorf("unknown config option: %s", configID)
	}
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

	// Under promptMu: if prompting, defer to pending store; otherwise proceed immediately.
	// Lock ordering: promptMu → pendingConfigMu (never the reverse).
	d.cmLockPromptMu()
	if d.cmIsPrompting() {
		d.cmLockPendingConfig()
		d.cmSetPendingEntry(configID, value)
		d.cmUnlockPendingConfig()
		d.cmUnlockPromptMu()

		// Optimistically reflect and broadcast pending value.
		d.cmUpdateConfigOptionValue(configID, value)
		c.persistConfigValue(d, configID, value)

		if l := d.cmLogger(); l != nil {
			l.Info("Config option change deferred while prompting", "config_id", configID, "value", value)
		}
		if found.Category == ConfigOptionCategoryModel {
			d.cmSetBaselineAndClearOverride(value)
			c.persistBaselineModel(d, value)
		}
		d.cmNotifyConfigChanged(configID, value)
		return nil
	}
	d.cmUnlockPromptMu()

	// Idle: supersede any pending value to prevent the flush from overwriting this immediate change.
	d.cmLockPendingConfig()
	d.cmDeletePendingEntry(configID)
	d.cmUnlockPendingConfig()

	return c.applyConfigOptionWithOpts(d, ctx, configID, value, recordTimeline)
}

func (c configManager) applyConfigOption(d configDeps, ctx context.Context, configID, value string) error {
	return c.applyConfigOptionWithOpts(d, ctx, configID, value, true)
}

// applyConfigOptionWithOpts is the core of applyConfigOption. When recordTimeline
// is false, a model change is applied (RPC + baseline + persistence + live config
// broadcast) WITHOUT emitting a session_change timeline pill. Used by the ACP-server
// constraint auto-select path, which re-selects the configured model on every
// session resume and would otherwise repeat an identical "Model changed" pill.
func (c configManager) applyConfigOptionWithOpts(d configDeps, ctx context.Context, configID, value string, recordTimeline bool) error {
	opt, ok := d.cmFindByID(configID)
	if !ok {
		return fmt.Errorf("unknown config option: %s", configID)
	}
	category := opt.Category

	if category == ConfigOptionCategoryMode && d.cmUsesLegacyModes() {
		if err := d.cmSetSessionMode(ctx, value); err != nil {
			if l := d.cmLogger(); l != nil {
				l.Error("Failed to set session mode", "config_id", configID, "value", value, "error", err)
			}
			return fmt.Errorf("failed to set %s: %w", configID, err)
		}
	} else if category == ConfigOptionCategoryModel {
		previousModel := d.cmGetCurrentModelID()
		if err := d.cmSetSessionModel(ctx, value); err != nil {
			if l := d.cmLogger(); l != nil {
				l.Error("Failed to set session model", "config_id", configID, "value", value, "error", err)
			}
			return fmt.Errorf("failed to set %s: %w", configID, err)
		}
		d.cmSetCurrentModelID(value)
		d.cmSetBaselineAndClearOverride(value)
		c.persistBaselineModel(d, value)
		if recordTimeline {
			d.cmRecordSessionChange(ConfigOptionCategoryModel, value, previousModel)
		}
	} else {
		return fmt.Errorf("config option %s is not supported by current agent", configID)
	}

	d.cmUpdateConfigOptionValue(configID, value)
	c.persistConfigValue(d, configID, value)

	if l := d.cmLogger(); l != nil {
		l.Info("Config option changed", "config_id", configID, "value", value)
	}
	d.cmNotifyConfigChanged(configID, value)
	return nil
}

func (c configManager) applyConfigConstraints(d configDeps, category string) {
	constraint := d.cmGetACPServerConstraint(category)
	if constraint == nil || constraint.Pattern == "" {
		return
	}

	opt, ok := d.cmFindByCategory(category)
	if !ok || len(opt.Options) == 0 {
		return
	}

	matchedValue := MatchConstraintOption(constraint, opt.Options)
	if matchedValue == "" {
		if l := d.cmLogger(); l != nil {
			l.Warn("ACP server constraint: no matching option found",
				"category", category, "match_mode", constraint.MatchMode,
				"pattern", constraint.Pattern, "available_count", len(opt.Options))
		}
		return
	}

	alreadySet := opt.CurrentValue == matchedValue
	if category == ConfigOptionCategoryModel && d.cmHasAgentModels() {
		alreadySet = d.cmGetCurrentModelID() == matchedValue
	}
	if alreadySet {
		if l := d.cmLogger(); l != nil {
			l.Debug("ACP server constraint: already set to matching value", "category", category, "value", matchedValue)
		}
		return
	}

	if l := d.cmLogger(); l != nil {
		l.Info("ACP server constraint: auto-selecting option",
			"category", category, "match_mode", constraint.MatchMode,
			"pattern", constraint.Pattern, "selected_value", matchedValue)
	}

	if d.cmHasParent() {
		if jitter := childStartupJitter(constraintModelSwitchChildStartupJitter); jitter > 0 {
			if l := d.cmLogger(); l != nil {
				l.Debug("ACP server constraint: staggering child startup model switch",
					"category", category, "jitter_ms", jitter.Milliseconds())
			}
			select {
			case <-time.After(jitter):
			case <-d.cmSessionCtx().Done():
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), constraintModelSwitchCallerBudget)
	defer cancel()

	if err := c.setConfigOptionWithOpts(d, ctx, category, matchedValue, false); err != nil {
		if l := d.cmLogger(); l != nil {
			l.Warn("ACP server constraint: failed to auto-select option (best-effort, falling back to current model)",
				"category", category, "value", matchedValue, "error", err)
		}
	}
}

func (c configManager) flushPendingConfig(d configDeps) {
	pending := d.cmDrainPendingConfig()
	if len(pending) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for configID, value := range pending {
		if err := c.applyConfigOption(d, ctx, configID, value); err != nil {
			if l := d.cmLogger(); l != nil {
				l.Error("Failed to flush deferred config option", "config_id", configID, "value", value, "error", err)
			}
		}
	}
}

func (c configManager) persistConfigValue(d configDeps, configID, value string) {
	d.cmPersistConfigValue(configID, value)
}

func (c configManager) persistBaselineModel(d configDeps, value string) {
	d.cmPersistBaselineModel(value)
}

func (c configManager) setActiveModelOnly(d configDeps, ctx context.Context, modelID string) error {
	if err := d.cmSetSessionModel(ctx, modelID); err != nil {
		return fmt.Errorf("failed to set model: %w", err)
	}
	d.cmSetCurrentModelID(modelID)
	d.cmUpdateConfigOptionValue(ConfigOptionCategoryModel, modelID)
	d.cmNotifyConfigChanged(ConfigOptionCategoryModel, modelID)
	return nil
}

func (c configManager) restoreBaselineIfOverride(d configDeps) {
	baseline, wasOverriding := d.cmTakeBaselineIfOverride()
	if !wasOverriding {
		return
	}
	if baseline == "" || !d.cmHasAgentModels() {
		return
	}
	if d.cmGetCurrentModelID() == baseline {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.setActiveModelOnly(d, ctx, baseline); err != nil {
		if l := d.cmLogger(); l != nil {
			l.Warn("Failed to restore baseline model after queue drain", "baseline", baseline, "error", err)
		}
	} else if l := d.cmLogger(); l != nil {
		l.Info("Restored baseline model after queue drain", "model", baseline)
	}
}
