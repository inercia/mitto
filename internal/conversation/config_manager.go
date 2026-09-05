package conversation

// Session-config / model-baseline collaborator — stateless; state lives on BackgroundSession.

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	mittoAcp "github.com/inercia/mitto/internal/acp"
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
//
// When the server has a ModelProfile set (mitto-hke), the profile's Criteria replaces
// the "model" entry of the constraints map (a copy — srv.Constraints is never mutated),
// so downstream applyConfigConstraints resolves the model the same way it always has.
// When ModelProfile is empty but ModelTag is set, the first profile in
// EffectiveModelProfiles carrying that tag (case-insensitive) supplies the Criteria.
// If neither resolves to a usable profile Criteria, this falls back to the server's
// raw Constraints (legacy matchMode/pattern behaviour).
//
// Priority axis for the ModelTag path is profile-list order: cfg.ModelProfilesByTag
// (which wraps the shared config.ProfilesByTag core) walks EffectiveModelProfiles in
// Config.Models order — user profiles first in user-supplied order, then any unshadowed
// canonical defaults — and this helper picks matches[0]. Reordering profiles in
// Config.Models flips which profile wins for the same tag at the ACPServerSettings.ModelTag
// consumer site (mitto-ex7 "list order = priority" contract), mirroring the
// InitialModelPreference and AuxiliaryModelTag consumer sites.
//
// Note: matches[0] is picked unconditionally here — there is no session yet at config
// time, so per-model resolvability against agent-available models is deferred to
// applyConfigConstraints downstream.
func lookupACPServerConstraints(cfg *config.Config, serverName string) map[string]*config.ACPServerConstraint {
	if cfg == nil {
		return nil
	}
	for _, srv := range cfg.ACPServers {
		if srv.Name != serverName {
			continue
		}
		var profile *config.ModelProfile
		if srv.ModelProfile != "" {
			profile = cfg.FindModelProfile(srv.ModelProfile)
		} else if srv.ModelTag != "" {
			if matches := cfg.ModelProfilesByTag(srv.ModelTag); len(matches) > 0 {
				profile = &matches[0]
			}
		}
		if profile == nil || profile.Criteria == nil {
			return srv.Constraints
		}
		merged := make(map[string]*config.ACPServerConstraint, len(srv.Constraints)+1)
		for k, v := range srv.Constraints {
			merged[k] = v
		}
		merged["model"] = profile.Criteria
		return merged
	}
	return nil
}

// applyModelConstraintOverride merges a per-session "model" constraint override into an
// existing ACP-server-constraints map, returning the (possibly newly allocated) map. Used
// by auto-children to apply a per-child initial model profile (mitto-9x8) without mutating
// the server's shared constraints map. No-op (returns constraints unchanged) when override
// is nil or has an empty Pattern.
func applyModelConstraintOverride(constraints map[string]*config.ACPServerConstraint, override *config.ACPServerConstraint) map[string]*config.ACPServerConstraint {
	if override == nil || override.Pattern == "" {
		return constraints
	}
	if constraints == nil {
		constraints = make(map[string]*config.ACPServerConstraint)
	}
	constraints[ConfigOptionCategoryModel] = override
	return constraints
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
	// cmPutBackOverrideActive (mitto-1yo) re-arms overrideActive after
	// cmTakeBaselineIfOverride cleared it but the subsequent restore RPC
	// failed, so a LATER drain/turn-completion attempt retries instead of
	// silently abandoning the temporary-override contract. baselineModel is
	// left untouched (cmTakeBaselineIfOverride never mutates it).
	cmPutBackOverrideActive()   // modelMu.Lock + overrideActive=true + Unlock
	cmGetBaselineModel() string // modelMu.Lock + read + Unlock
	cmHasAgentModels() bool
	cmGetCurrentModelID() string   // reads agentModels.CurrentModelId under agentModelsMu
	cmSetCurrentModelID(id string) // writes agentModels.CurrentModelId under agentModelsMu; nil-safe

	// ACP server constraint lookup
	cmGetACPServerConstraint(category string) *config.ACPServerConstraint

	// Persistence helpers (no-ops when no store)
	cmPersistConfigValue(configID, value string)
	cmPersistBaselineModel(value string) // ignores superseded selections; never called under promptMu

	// Config changed notification (no-op when hook not set)
	cmNotifyConfigChanged(configID, value string)

	// Record a user-initiated session change to the timeline and push it live to
	// observers (no-op when no recorder). Generic: kind discriminates the change.
	// Returns the recorder's persistence error (if any) so callers can avoid
	// logging/broadcasting success when the timeline write failed (mitto-9zy1).
	cmRecordSessionChange(kind, value, previousValue string) error
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
	err := c.setConfigOptionWithOpts(d, ctx, configID, value, true, true)
	if err == nil {
		return nil
	}
	opt, ok := d.cmFindByID(configID)
	if ok && opt.Category == ConfigOptionCategoryModel && isRetryableModelPreferenceError(err) {
		// Preserve a failed user selection as the intended baseline without
		// optimistically changing the effective config value. The next prompt's
		// model-preference preflight will retry this baseline after the agent warms.
		d.cmSetBaselineAndClearOverride(value)
		c.persistBaselineModel(d, value)
		if l := d.cmLogger(); l != nil {
			l.Info("Preserving retryable model change as pending baseline",
				"session_id", d.cmSessionID(), "model", value)
		}
	}
	return err
}

// setConfigOptionWithOpts is the core of setConfigOption. recordTimeline controls
// whether a model change emits a user-facing session_change timeline pill. The
// startup/constraint auto-select path passes false so re-selecting the configured
// model on every session resume does not repeat an identical "Model changed" pill.
// deferWhilePrompting is false for startup constraints: a deferred handshake reserves
// isPrompting before it reports models, but the constraint must land before that turn
// reaches the agent rather than being deferred behind it (mitto-qori).
func (c configManager) setConfigOptionWithOpts(d configDeps, ctx context.Context, configID, value string, recordTimeline, deferWhilePrompting bool) error {
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
	if d.cmIsPrompting() && deferWhilePrompting {
		d.cmLockPendingConfig()
		d.cmSetPendingEntry(configID, value)
		d.cmUnlockPendingConfig()
		// Publish the pending request and its baseline in the same admission
		// critical section. Completion cannot drain it or open the next turn
		// between those updates. Only in-memory work belongs under promptMu.
		if found.Category == ConfigOptionCategoryModel {
			d.cmSetBaselineAndClearOverride(value)
		}
		d.cmUpdateConfigOptionValue(configID, value)
		d.cmUnlockPromptMu()

		// Persistence and callbacks may block. Baseline persistence rejects a
		// stale selection if another setter wins after we release promptMu.
		c.persistConfigValue(d, configID, value)

		if l := d.cmLogger(); l != nil {
			l.Info("Config option change deferred while prompting", "config_id", configID, "value", value)
		}
		if found.Category == ConfigOptionCategoryModel {
			c.persistBaselineModel(d, value)
		}
		d.cmNotifyConfigChanged(configID, value)
		return nil
	}

	// Idle: supersede any pending value to prevent the flush from overwriting this immediate change.
	d.cmLockPendingConfig()
	d.cmDeletePendingEntry(configID)
	d.cmUnlockPendingConfig()
	d.cmUnlockPromptMu()

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
	return c.applyConfigOptionWithBaseline(d, ctx, configID, value, recordTimeline, true)
}

// Deferred model selections already published and persisted their baseline at
// admission. Their RPC completion must only change active state, never promote
// the old request over a newer selection accepted while the RPC was in flight.
func (c configManager) applyConfigOptionWithBaseline(d configDeps, ctx context.Context, configID, value string, recordTimeline, updateBaseline bool) error {
	opt, ok := d.cmFindByID(configID)
	if !ok {
		return fmt.Errorf("unknown config option: %s", configID)
	}
	category := opt.Category

	var recordErr error
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
				if mittoAcp.IsACPConnectionError(err) {
					// Dead/restarting process (e.g. agent heap-OOM crash, mitto-5q8): this is a
					// transient restart-gap condition, not a genuine model-selection failure.
					l.Warn("Skipping model change; agent process is restarting", "config_id", configID, "value", value, "error", err)
				} else {
					l.Error("Failed to set session model", "config_id", configID, "value", value, "error", err)
				}
			}
			return fmt.Errorf("failed to set %s: %w", configID, err)
		}
		d.cmSetCurrentModelID(value)
		if updateBaseline {
			d.cmSetBaselineAndClearOverride(value)
			c.persistBaselineModel(d, value)
		}
		if recordTimeline {
			recordErr = d.cmRecordSessionChange(ConfigOptionCategoryModel, value, previousModel)
		}
	} else {
		return fmt.Errorf("config option %s is not supported by current agent", configID)
	}

	d.cmUpdateConfigOptionValue(configID, value)
	c.persistConfigValue(d, configID, value)

	// mitto-9zy1 defect 2a: when the session-change timeline event failed to
	// persist, do not claim success — skip the "Config option changed" INFO log
	// and the live config-changed notification, and surface the failure to the
	// caller instead. The RPC-applied model change and local config-option state
	// above are still reflected; only the misleading success signal is suppressed.
	if recordErr != nil {
		return fmt.Errorf("failed to record session change for %s: %w", configID, recordErr)
	}

	if l := d.cmLogger(); l != nil {
		l.Info("Config option changed", "config_id", configID, "value", value)
	}
	d.cmNotifyConfigChanged(configID, value)
	return nil
}

func (c configManager) applyConfigConstraints(d configDeps, category string) error {
	constraint := d.cmGetACPServerConstraint(category)

	opt, ok := d.cmFindByCategory(category)
	if !ok || len(opt.Options) == 0 {
		return nil
	}

	var matchedValue string
	switch {
	case category == ConfigOptionCategoryModel && d.cmGetBaselineModel() != "":
		// Startup configuration seeds the baseline once; it must never override a
		// conversation's own current model on resume or after a manual change.
		matchedValue = d.cmGetBaselineModel()
	case constraint != nil && constraint.Pattern != "":
		matchedValue = MatchConstraintOption(constraint, opt.Options)
		if matchedValue == "" {
			if l := d.cmLogger(); l != nil {
				l.Warn("ACP server constraint: no matching option found",
					"category", category, "match_mode", constraint.MatchMode,
					"pattern", constraint.Pattern, "available_count", len(opt.Options))
			}
			return nil
		}
	default:
		return nil
	}

	alreadySet := opt.CurrentValue == matchedValue
	if category == ConfigOptionCategoryModel && d.cmHasAgentModels() {
		alreadySet = d.cmGetCurrentModelID() == matchedValue
	}
	if alreadySet {
		if l := d.cmLogger(); l != nil {
			l.Debug("ACP server constraint: already set to matching value", "category", category, "value", matchedValue)
		}
		return nil
	}

	if l := d.cmLogger(); l != nil {
		if constraint != nil && constraint.Pattern != "" {
			l.Info("ACP server constraint: auto-selecting option",
				"category", category, "match_mode", constraint.MatchMode,
				"pattern", constraint.Pattern, "selected_value", matchedValue)
		} else {
			l.Info("Restoring persisted baseline model on resume",
				"category", category, "selected_value", matchedValue)
		}
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
				return d.cmSessionCtx().Err()
			}
		}
	}

	parentCtx := d.cmSessionCtx()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, constraintModelSwitchCallerBudget)
	defer cancel()

	apply := func() error {
		if d.cmIsClosed() {
			return fmt.Errorf("session is closed")
		}
		if category == ConfigOptionCategoryModel && d.cmGetBaselineModel() != "" {
			// Re-read after jitter/retries: a newer manual selection wins. Restoring
			// active state must not persist an old snapshot over that selection.
			target := d.cmGetBaselineModel()
			for _, option := range opt.Options {
				if option.Value == target {
					return c.setActiveModelOnly(d, ctx, target)
				}
			}
			return fmt.Errorf("conversation model %q is no longer available", target)
		}
		return c.setConfigOptionWithOpts(d, ctx, opt.ID, matchedValue, false, false)
	}
	err := apply()
	if category == ConfigOptionCategoryModel && err != nil && isRetryableModelPreferenceError(err) {
		timer := time.NewTimer(modelSwitchWarmRetryDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return err
		}
		if l := d.cmLogger(); l != nil {
			l.Info("Retrying startup model constraint after cold-agent cooldown",
				"session_id", d.cmSessionID(), "model", matchedValue)
		}
		err = apply()
	}
	if err != nil {
		if l := d.cmLogger(); l != nil {
			l.Warn("ACP server constraint: failed to auto-select option; queued prompts remain pending",
				"category", category, "value", matchedValue, "error", err)
		}
		return err
	}
	return nil
}

func (c configManager) flushPendingConfig(d configDeps) {
	if d.cmIsClosed() {
		// mitto-9zy1 defect 2b: unlike setConfigOptionWithOpts, this deferred-flush
		// call site had no liveness gate at all, so a config change deferred while
		// prompting could still be applied after the session was closed (e.g. from
		// the prompt-completion tail racing teardown). Leave the pending map
		// undrained; there is no live session left to apply it to.
		if l := d.cmLogger(); l != nil {
			l.Debug("Skipping deferred config flush; session is closed", "session_id", d.cmSessionID())
		}
		return
	}
	// Pair the drain with the setter's pending+baseline publication. The turn
	// owner must recheck pending config under promptMu before becoming idle.
	d.cmLockPromptMu()
	pending := d.cmDrainPendingConfig()
	d.cmUnlockPromptMu()
	if len(pending) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for configID, value := range pending {
		if err := c.applyConfigOptionWithBaseline(d, ctx, configID, value, true, false); err != nil {
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
		// mitto-1yo: the restore RPC failed (agent cold/saturated/dead) — put
		// the override flag back so a LATER drain/turn-completion attempt
		// retries instead of silently abandoning the temporary-override
		// contract. Previously cmTakeBaselineIfOverride cleared the flag
		// unconditionally before the RPC outcome was known, so a failed
		// restore was never retried.
		d.cmPutBackOverrideActive()
		if l := d.cmLogger(); l != nil {
			l.Warn("Failed to restore baseline model after queue drain; will retry on next drain",
				"baseline", baseline, "error", err)
		}
	} else if l := d.cmLogger(); l != nil {
		l.Info("Restored baseline model after queue drain", "model", baseline)
	}
}
