package conversation

// Session-config / model-baseline collaborator — stateless; state lives on BackgroundSession.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/config"
)

// constraintModelSwitchCallerBudget is the context timeout for the async ACP-server
// constraint auto-select model switch (mitto-f7q, Option 4).
const constraintModelSwitchCallerBudget = 90 * time.Second

// errModelPermanentlyUnavailable marks a startup model-constraint failure as
// PERMANENT: the pinned baseline model is confirmed absent from the live ACP
// model catalog (model-catalog drift), as opposed to the TRANSIENT failures
// isRetryableModelPreferenceError already recognizes (deadline/saturation/
// connection), which can succeed on a later retry. recoverStartupConstraintAfterRestart
// (bgsession_callbacks.go) uses isModelPermanentlyUnavailableError to give up
// its retry loop instead of retrying forever, since the pinned value can never
// reappear in opt.Options (mitto-uex).
var errModelPermanentlyUnavailable = errors.New("model permanently unavailable")

// isModelPermanentlyUnavailableError reports whether err wraps
// errModelPermanentlyUnavailable.
func isModelPermanentlyUnavailableError(err error) bool {
	return errors.Is(err, errModelPermanentlyUnavailable)
}

// errModelCatalogSynthesized marks a fallback attempt refused because the
// current agent model catalog is Mitto-synthesized (see
// SynthesizeModelStateFromProfiles / SessionModelState.Synthesized). A
// synthesized catalog's ModelId values are Mitto profile display names, never
// confirmed by the agent or backend — treating "baseline not found among
// synthesized options" as proof of drift and picking one of those display
// names as a replacement is exactly the mitto-a7wm corruption: a bogus id
// (e.g. "Claude Opus 5.5") gets sent to session/set_model (accepted blindly
// by some agents) and persisted as the new baseline, wedging every future
// prompt behind a 404 "selected model is not available" that never self-heals.
var errModelCatalogSynthesized = errors.New("refusing model fallback: catalog is synthesized")

// findOptionValueByName looks up options for an entry whose display Name
// matches needle case-insensitively (after trimming surrounding whitespace),
// returning its Value. Used to self-heal a persisted baseline that holds a
// display Name instead of a real model id (mitto-a7wm root cause: the
// mitto-886 synthesized-catalog fallback used to seed/persist profile names
// as if they were confirmed ids). Returns ("", false) when needle is empty or
// no option's Name matches.
func findOptionValueByName(options []SessionConfigOptionValue, needle string) (string, bool) {
	trimmed := strings.TrimSpace(needle)
	if trimmed == "" {
		return "", false
	}
	for _, o := range options {
		if strings.EqualFold(strings.TrimSpace(o.Name), trimmed) {
			return o.Value, true
		}
	}
	return "", false
}

// constraintModelSwitchChildStartupJitter bounds the randomized startup delay for child sessions.
const constraintModelSwitchChildStartupJitter = 5 * time.Second

// childStartupJitter returns a randomized startup delay in [0, max) (mitto-x4e).
func childStartupJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}

// lookupACPServerConstraints returns the raw auto-selection constraints for the
// named ACP server (the server's Constraints map, e.g. matchMode/pattern rules
// under Constraints["model"]), or nil when the server is not found. These feed
// downstream applyConfigConstraints for session-start config-option selection.
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
	// cmModelCatalogSynthesized reports whether the current agent model
	// catalog was built locally by SynthesizeModelStateFromProfiles rather
	// than reported by the agent (reads agentModels.Synthesized under
	// agentModelsMu; false when there is no catalog at all). See
	// SessionModelState.Synthesized and errModelCatalogSynthesized (mitto-a7wm).
	cmModelCatalogSynthesized() bool

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
	if found.Category == ConfigOptionCategoryModel {
		// mitto-a7wm: refuse BEFORE admitting the value into the pending
		// store or persisting it as the baseline — the deferred-while-
		// prompting branch below persists immediately at admission, ahead of
		// any RPC, so this must run before either branch does any work.
		if err := c.refuseSynthesizedModelID(d, value); err != nil {
			return err
		}
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
	return c.applyConfigConstraintsWithParentCtx(d, category, nil)
}

// applyConfigConstraintsWithParentCtx is applyConfigConstraints with an
// optional override for the parent context used to bound the RPC's timeout.
// When parentCtxOverride is nil, d.cmSessionCtx() is used exactly as before.
//
// A non-nil override exists for mitto-c6j.1's last-chance retry in
// recoverStartupConstraintAfterRestart's ctx.Done() branch: that branch fires
// precisely because d.cmSessionCtx() (bs.ctx) has already been canceled — by
// a racing GC-recycle close (SessionManager.CloseIdleSession ->
// bs.Close("gc_suspended")) that beat the mitto-3ml live-retry timer — so
// deriving the RPC budget from it there would produce an already-Done
// context and the attempt would fail before ever reaching the wire,
// regardless of whether the (possibly still-alive, about-to-be-replaced)
// shared process would otherwise have accepted the call.
func (c configManager) applyConfigConstraintsWithParentCtx(d configDeps, category string, parentCtxOverride context.Context) error {
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

	parentCtx := parentCtxOverride
	if parentCtx == nil {
		parentCtx = d.cmSessionCtx()
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}

	if d.cmHasParent() {
		if jitter := childStartupJitter(constraintModelSwitchChildStartupJitter); jitter > 0 {
			if l := d.cmLogger(); l != nil {
				l.Debug("ACP server constraint: staggering child startup model switch",
					"category", category, "jitter_ms", jitter.Milliseconds())
			}
			select {
			case <-time.After(jitter):
			case <-parentCtx.Done():
				return parentCtx.Err()
			}
		}
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
					if d.cmModelCatalogSynthesized() {
						// mitto-a7wm: the baseline is itself a synthesized
						// profile name (a pre-fix corrupted baseline). Sending
						// it would 404 every prompt, and failing here would
						// strand queued prompts behind an unready startup
						// constraint. Leave the agent on its own default; the
						// name→id remap below heals the baseline once a real
						// catalog is advertised.
						if l := d.cmLogger(); l != nil {
							l.Warn("mitto-a7wm: baseline is a synthesized model name; keeping agent default",
								"session_id", d.cmSessionID(), "model", target)
						}
						return nil
					}
					return c.setActiveModelOnly(d, ctx, target)
				}
			}
			if d.cmModelCatalogSynthesized() {
				// mitto-a7wm: a synthesized catalog (see
				// SynthesizeModelStateFromProfiles) is a Mitto-local UI aid
				// built from profile display names — it cannot judge whether
				// a REAL agent-confirmed baseline id is still valid, so
				// "not found among these synthesized entries" is NOT proof
				// of catalog drift. Restore the real baseline as-is instead
				// of falling through to errModelPermanentlyUnavailable,
				// which would otherwise route into fallbackToAvailableModel
				// and pick one of the synthesized display names as a
				// replacement — the mitto-a7wm corruption itself.
				if l := d.cmLogger(); l != nil {
					l.Warn("mitto-a7wm: restoring real baseline against a synthesized model catalog",
						"session_id", d.cmSessionID(), "model", target)
				}
				return c.setActiveModelOnly(d, ctx, target)
			}
			if remapped, ok := findOptionValueByName(opt.Options, target); ok {
				// mitto-a7wm: self-heal a persisted baseline that holds a
				// display Name instead of a real model id.
				return c.remapCorruptedBaselineName(d, ctx, target, remapped)
			}
			return fmt.Errorf("conversation model %q is no longer available: %w", target, errModelPermanentlyUnavailable)
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
	if category == ConfigOptionCategoryModel && err != nil && isModelPermanentlyUnavailableError(err) {
		if ferr := c.fallbackToAvailableModel(d, ctx, opt, constraint); ferr == nil {
			return nil
		} else if l := d.cmLogger(); l != nil {
			l.Warn("Best-effort model fallback failed; leaving pinned-model recovery to terminal handler",
				"session_id", d.cmSessionID(), "error", ferr)
		}
		// On fallback failure, fall through and return the original permanent err
		// so the recovery goroutine's existing terminal OnError branch still fires.
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

// fallbackToAvailableModel performs best-effort recovery when the conversation's
// pinned baseline model has dropped out of the live ACP catalog (model-catalog
// drift, mitto-qst). Rather than stranding the conversation/loop with an
// unready startup constraint (which produces the "model is still initializing" /
// "Failed to deliver loop prompt" storm), it picks the best available fallback
// model, switches to it, self-heals the persisted baseline so later resumes do
// not re-trigger this path, and records two persistent session_change timeline
// events: a "model_unavailable" notice (explaining the drift) and a standard
// "model" change (which drives the "Model changed to X" pill AND keeps stats
// token-attribution retagging correct — the aggregator keys retag on
// SessionChangeData.Kind == "model"). Returns nil on success (caller returns nil
// so queued prompts release); returns a non-nil error only when no usable
// fallback model exists (opt.Options is guaranteed non-empty by the caller, so
// this is effectively unreachable in normal operation and left as a safety net).
//
// Fallback selection order (best-effort):
//  1. ACP-server model constraint pattern match (respects configured policy).
//  2. The agent's current/default model (cmGetCurrentModelID) when advertised
//     and present in opt.Options — "leave the default model".
//  3. The first available option.
func (c configManager) fallbackToAvailableModel(d configDeps, ctx context.Context, opt SessionConfigOption, constraint *config.ACPServerConstraint) error {
	if d.cmModelCatalogSynthesized() {
		// mitto-a7wm: a synthesized catalog's ModelId values are Mitto
		// profile display names, never confirmed by the agent or backend —
		// picking one as a "fallback" is exactly the corruption this fallback
		// exists to prevent. Refuse outright: persist nothing, record no
		// events.
		if l := d.cmLogger(); l != nil {
			l.Warn("mitto-a7wm: refusing startup model fallback against a synthesized catalog",
				"session_id", d.cmSessionID())
		}
		return errModelCatalogSynthesized
	}
	unavailable := d.cmGetBaselineModel()

	optionExists := func(value string) bool {
		for _, o := range opt.Options {
			if o.Value == value {
				return true
			}
		}
		return false
	}

	var target string
	if constraint != nil && constraint.Pattern != "" {
		if m := MatchConstraintOption(constraint, opt.Options); m != "" {
			target = m
		}
	}
	if target == "" {
		if cur := d.cmGetCurrentModelID(); cur != "" && optionExists(cur) {
			target = cur
		}
	}
	if target == "" && len(opt.Options) > 0 {
		target = opt.Options[0].Value
	}
	if target == "" || target == unavailable {
		return errModelPermanentlyUnavailable
	}

	return c.applyModelFallbackTarget(d, ctx, unavailable, target)
}

// applyModelFallbackTarget is the shared tail of fallbackToAvailableModel and
// tryRuntimeFallbackToAvailableModel's swap path: it switches the active
// model to target (skipping the RPC when it is already active), self-heals
// the persisted baseline so subsequent resumes/turns do not re-enter the
// fallback path, and records the standard "model_unavailable" (naming the
// refused model) + "model" (naming the swap target) session_change events
// that drive the timeline pill and stats token-attribution retagging.
func (c configManager) applyModelFallbackTarget(d configDeps, ctx context.Context, unavailable, target string) error {
	// Apply the fallback to the agent only when it differs from the currently
	// active model. When we are simply adopting the agent's own default
	// (target == current) no RPC is needed, but the UI config-option value and
	// baseline must still be updated below.
	if d.cmGetCurrentModelID() != target {
		if err := c.setActiveModelOnly(d, ctx, target); err != nil {
			return err
		}
	} else {
		d.cmUpdateConfigOptionValue(ConfigOptionCategoryModel, target)
		d.cmNotifyConfigChanged(ConfigOptionCategoryModel, target)
	}

	// Self-heal the persisted baseline so subsequent resumes do not re-enter
	// this fallback path.
	d.cmSetBaselineAndClearOverride(target)
	c.persistBaselineModel(d, target)

	if l := d.cmLogger(); l != nil {
		l.Warn("Pinned model unavailable; fell back to an available model",
			"session_id", d.cmSessionID(), "unavailable_model", unavailable, "fallback_model", target)
	}

	// Persistent, reload-safe in-conversation messages. Record the explanatory
	// notice first, then the standard model change (order = timeline order).
	if unavailable != "" {
		_ = d.cmRecordSessionChange("model_unavailable", unavailable, "")
	}
	_ = d.cmRecordSessionChange(ConfigOptionCategoryModel, target, unavailable)
	return nil
}

// tryRuntimeFallbackToAvailableModel is the runtime counterpart of the
// startup fallback path in applyConfigConstraintsWithParentCtx: given a live
// conversation whose pinned model was refused mid-session (mitto-a7wm —
// Augment /chat-stream returning httpStatus:404 + apiStatus:"unimplemented"
// for a model that WAS in the ACP catalog at startup but was retired or
// renamed backend-side, OR whose persisted baseline was corrupted into a
// display Name by the mitto-886 synthesized-catalog fallback), it self-heals
// the conversation so it does not keep repeating the same failure on every
// prompt or loop cadence.
//
// Unlike the startup fallback, the failed model here is the CURRENTLY ACTIVE
// one (current == baseline in the common case, since both point at whatever
// was last successfully applied) — so simply "adopting the current model"
// (the startup fallback's step 2) would just re-select the very model that
// was just refused. Selection order:
//  1. Name→id remap: if the failed value is actually a display Name (the
//     mitto-a7wm corruption pattern) rather than a real id, self-heal by
//     switching to and persisting the matching id — no catalog drift assumed.
//  2. ACP-server model constraint pattern match, when it resolves to
//     something OTHER than the failed model.
//  3. The first available option whose Value differs from BOTH the failed
//     model and the persisted baseline (defensive: covers baseline/current
//     disagreement) — i.e. never the failed model.
//
// Refuses outright (persists nothing, records no events) when the catalog is
// synthesized — see fallbackToAvailableModel's identical guard. Returns nil
// when there is no model option present (nothing to fall back FROM — e.g. an
// agent that never advertised a model catalog). Never blocks longer than
// constraintModelSwitchCallerBudget.
func (c configManager) tryRuntimeFallbackToAvailableModel(d configDeps, parentCtx context.Context) error {
	if d.cmIsClosed() {
		return fmt.Errorf("session is closed")
	}
	opt, ok := d.cmFindByCategory(ConfigOptionCategoryModel)
	if !ok || len(opt.Options) == 0 {
		return nil
	}
	if d.cmModelCatalogSynthesized() {
		if l := d.cmLogger(); l != nil {
			l.Warn("mitto-a7wm: refusing runtime model fallback against a synthesized catalog",
				"session_id", d.cmSessionID())
		}
		return errModelCatalogSynthesized
	}
	if parentCtx == nil {
		parentCtx = d.cmSessionCtx()
	}
	ctx, cancel := context.WithTimeout(parentCtx, constraintModelSwitchCallerBudget)
	defer cancel()

	baseline := d.cmGetBaselineModel()
	failed := d.cmGetCurrentModelID()
	if failed == "" {
		failed = baseline
	}

	if remapped, ok := findOptionValueByName(opt.Options, failed); ok {
		if err := c.remapCorruptedBaselineName(d, ctx, failed, remapped); err == nil {
			return nil
		} else if l := d.cmLogger(); l != nil {
			l.Warn("mitto-a7wm: name->id remap failed during runtime fallback; falling back to model swap",
				"session_id", d.cmSessionID(), "error", err)
		}
	}

	constraint := d.cmGetACPServerConstraint(ConfigOptionCategoryModel)
	var target string
	if constraint != nil && constraint.Pattern != "" {
		if m := MatchConstraintOption(constraint, opt.Options); m != "" && m != failed {
			target = m
		}
	}
	if target == "" {
		for _, o := range opt.Options {
			if o.Value != failed && o.Value != baseline {
				target = o.Value
				break
			}
		}
	}
	if target == "" {
		return errModelPermanentlyUnavailable
	}
	return c.applyModelFallbackTarget(d, ctx, failed, target)
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

// refuseSynthesizedModelID is the central guard (mitto-a7wm) preventing any
// caller from sending a Mitto-synthesized profile-name placeholder to the
// agent's set_model RPC or persisting it as the baseline. A synthesized
// catalog (see SynthesizeModelStateFromProfiles) exists purely as a display
// aid for agents that never advertise a real model catalog; its ModelId
// values are Mitto profile display names (e.g. "Claude Opus 5.5"), never
// confirmed by the agent or backend. Some agents (Auggie 0.36.0) accept an
// arbitrary set_model string without validating it, so without this guard a
// synthesized id can silently become the persisted baseline and every later
// prompt then 404s with "the selected model is not available for this
// session" — the mitto-a7wm root cause.
//
// Returns nil (no-op) when modelID is empty, the catalog is not synthesized,
// or modelID does not match any of the catalog's synthesized entries (e.g. a
// REAL agent-confirmed id being restored/remapped, which must always be
// allowed through — see the synthesized-catalog restore branch in
// applyConfigConstraintsWithParentCtx's apply()). Logs a WARN with a distinct
// literal when refusing so the corruption path is diagnosable.
func (c configManager) refuseSynthesizedModelID(d configDeps, modelID string) error {
	if modelID == "" || !d.cmModelCatalogSynthesized() {
		return nil
	}
	opt, ok := d.cmFindByCategory(ConfigOptionCategoryModel)
	if !ok {
		return nil
	}
	for _, o := range opt.Options {
		if o.Value == modelID {
			if l := d.cmLogger(); l != nil {
				l.Warn("mitto-a7wm: refusing to apply synthesized model id",
					"session_id", d.cmSessionID(), "model_id", modelID)
			}
			return fmt.Errorf("model id %q comes from a synthesized (unconfirmed) model catalog: %w",
				modelID, errModelCatalogSynthesized)
		}
	}
	return nil
}

// remapCorruptedBaselineName self-heals a persisted baseline that holds a
// display Name instead of a real model id — the mitto-a7wm corruption
// pattern, but this helper also covers any other way a Name could have ended
// up in the baseline. Switches the active model to resolvedID, self-heals the
// persisted baseline, and records a plain "model" session_change (no
// "model_unavailable" notice: nothing was actually retired, only mis-tagged).
// Logs INFO with a distinct literal on success.
func (c configManager) remapCorruptedBaselineName(d configDeps, ctx context.Context, badName, resolvedID string) error {
	if err := c.setActiveModelOnly(d, ctx, resolvedID); err != nil {
		return err
	}
	d.cmSetBaselineAndClearOverride(resolvedID)
	c.persistBaselineModel(d, resolvedID)
	if l := d.cmLogger(); l != nil {
		l.Info("mitto-a7wm: remapped corrupted baseline model name to id",
			"session_id", d.cmSessionID(), "name", badName, "id", resolvedID)
	}
	_ = d.cmRecordSessionChange(ConfigOptionCategoryModel, resolvedID, badName)
	return nil
}

func (c configManager) setActiveModelOnly(d configDeps, ctx context.Context, modelID string) error {
	if err := c.refuseSynthesizedModelID(d, modelID); err != nil {
		return err
	}
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
