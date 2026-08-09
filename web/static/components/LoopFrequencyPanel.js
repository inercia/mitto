// Mitto Web Interface - Loop Frequency Panel Component
// Single merged card: compact header (always visible) + collapsible body (settings).

const { useState, useEffect, useCallback, useMemo, useRef, html, Fragment } =
  window.preact;

import {
  LoopFilledIcon,
  PlayFilledIcon,
  PauseFilledIcon,
  ChatBubbleIcon,
  SlidersIcon,
} from "./Icons.js";
import { promptDialogParameters } from "../utils/prompts.js";
import { LoopPromptSelector } from "./LoopPromptSelector.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { apiUrl } from "../utils/api.js";
import { getSdkClient } from "../utils/sdkClient.js";
import {
  errorStatus,
  errorMessage as sdkErrorMessage,
} from "../utils/sdkErrors.js";
import { PortalTooltip } from "./ContextMenu.js";
import {
  CONDITION_PRESETS,
  presetConditionFor,
  extractPresetParam,
  resolveConditionPresetId,
} from "../lib.js";

/** Minimum delay for on-completion trigger (seconds). Used for client-side clamp helper text. */
const MIN_COMPLETION_DELAY_SECONDS = 5;

/**
 * Trigger names this panel has dedicated UI/state for, in canonical wire
 * order. Used only to order the stable trigger list on save — NOT as an
 * allow-list. Any other armed trigger must still be preserved (mitto-987y.7).
 */
const KNOWN_TRIGGERS = ["schedule", "onCompletion", "onTasks"];

/**
 * Schedules that repeat more frequently than this (in seconds) are considered
 * "too frequent" for an unbounded loop conversation and trigger the
 * dangerous-config warning on save. 5 minutes.
 */
const DANGEROUS_FREQUENCY_SECONDS = 5 * 60;

// Hover-only tooltips are pointless on touch devices (no hover); gate the portal
// header tooltips the same way daisyUI gates its CSS tooltips so taps never
// trigger a stuck bubble.
const LOOP_SUPPORTS_HOVER =
  typeof window !== "undefined" &&
  typeof window.matchMedia === "function" &&
  window.matchMedia("(hover: hover)").matches;

// Delay before a header tooltip appears on hover (ms).
const LOOP_TOOLTIP_DELAY_MS = 250;

/**
 * Convert a numeric value + unit string into total seconds.
 * unit is one of "minutes" | "hours" | "days"; anything else is treated as seconds.
 */
function valueUnitToSeconds(value, unit) {
  const v = Number(value) || 0;
  switch (unit) {
    case "minutes":
      return v * 60;
    case "hours":
      return v * 3600;
    case "days":
      return v * 86400;
    default:
      return v;
  }
}

/**
 * Convert a total-seconds count into the largest whole value+unit pair.
 * 0 → { value: 0, unit: "hours" }.
 */
function secondsToValueUnit(sec) {
  const s = Number(sec) || 0;
  if (s === 0) return { value: 0, unit: "hours" };
  if (s % 86400 === 0) return { value: s / 86400, unit: "days" };
  if (s % 3600 === 0) return { value: s / 3600, unit: "hours" };
  if (s % 60 === 0) return { value: s / 60, unit: "minutes" };
  return { value: s, unit: "minutes" };
}

/**
 * Convert UTC time (HH:MM) to local time (HH:MM).
 * @param {string} utcTime - Time in HH:MM format (UTC)
 * @returns {string} Time in HH:MM format (local)
 */
function utcToLocalTime(utcTime) {
  if (!utcTime) return "";
  const [hours, minutes] = utcTime.split(":").map(Number);
  // Create a Date object for today at the UTC time
  const now = new Date();
  const utcDate = new Date(
    Date.UTC(
      now.getUTCFullYear(),
      now.getUTCMonth(),
      now.getUTCDate(),
      hours,
      minutes,
      0,
    ),
  );
  // Format in local time
  return utcDate.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

/**
 * Normalise the LoopFrequencyPanel `triggers` / legacy `trigger` props into a
 * non-empty ordered array. Priority: array `triggers` prop first, then legacy
 * scalar `trigger`, then a "schedule" fallback (mitto-r6j).
 * @param {string[]|null|undefined} triggersProp
 * @param {string|null|undefined} triggerProp
 * @returns {string[]}
 */
function normaliseTriggersProp(triggersProp, triggerProp) {
  if (Array.isArray(triggersProp)) {
    const cleaned = triggersProp.filter(
      (t) => typeof t === "string" && t.length > 0,
    );
    if (cleaned.length > 0) return cleaned;
  }
  if (typeof triggerProp === "string" && triggerProp.length > 0) {
    return [triggerProp];
  }
  return ["schedule"];
}

/**
 * Convert local time (HH:MM) to UTC time (HH:MM).
 * @param {string} localTime - Time in HH:MM format (local)
 * @returns {string} Time in HH:MM format (UTC)
 */
function localToUtcTime(localTime) {
  if (!localTime) return "";
  const [hours, minutes] = localTime.split(":").map(Number);
  // Create a Date object for today at the local time
  const now = new Date();
  const localDate = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
    hours,
    minutes,
    0,
  );
  // Get UTC hours and minutes
  const utcHours = localDate.getUTCHours().toString().padStart(2, "0");
  const utcMinutes = localDate.getUTCMinutes().toString().padStart(2, "0");
  return `${utcHours}:${utcMinutes}`;
}

/**
 * LoopFrequencyPanel component - merged loop settings card.
 * Header (always visible when isOpen): run-now, prompt selector, status, pause/resume, expand toggle.
 * Body (collapsed by default): frequency inputs, fresh-context, max-runs.
 *
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the card is visible (shown when loop is enabled)
 * @param {boolean} props.disabled - true when loop is active/enabled (controls pause vs resume label)
 * @param {string} props.sessionId - Current session ID
 * @param {Object} props.frequency - Current frequency config { value, unit, at } (at is in UTC)
 * @param {Function} props.onFrequencyChange - Callback when frequency is updated
 * @param {string} props.nextScheduledAt - ISO timestamp of next scheduled run
 * @param {boolean} props.isStreaming - Whether the agent is currently responding
 * @param {boolean} props.freshContext - Whether each run starts with a fresh agent context
 * @param {Function} props.onFreshContextChange - Callback when freshContext is toggled
 * @param {number} props.maxIterations - Maximum number of runs (0 = unlimited)
 * @param {number} props.iterationCount - Number of runs delivered so far
 * @param {Function} props.onMaxIterationsChange - Callback when max iterations is updated
 * @param {Function} props.onLoopEnabledChange - Callback when loop is paused/resumed
 * @param {Array} props.prompts - Available workspace prompts for the inline selector
 * @param {Array} props.allPrompts - Full workspace prompts list; used for arg-edit lookup so menu-scoped loop prompts (e.g. beadsList) can still have their arguments edited.
 * @param {string} props.selectedPromptName - Currently selected loop prompt name
 * @param {string} props.selectedPromptBody - Free-text loop prompt body (used when no named prompt is set)
 * @param {Function} props.onPromptSelect - Callback when a prompt is selected: (promptName) => void
 * @param {boolean} props.isPromptAreaVisible - Whether the prompt composition area is visible
 * @param {Function} props.onTogglePromptArea - Callback to toggle prompt composition area visibility
 */
export function LoopFrequencyPanel({
  isOpen,
  disabled = false,
  sessionId,
  frequency = { value: 1, unit: "hours" },
  onFrequencyChange,
  nextScheduledAt,
  isStreaming = false,
  freshContext = false,
  onFreshContextChange,
  maxIterations = 0,
  iterationCount = 0,
  onMaxIterationsChange,
  onLoopEnabledChange,
  prompts = [],
  allPrompts = [],
  selectedPromptName = "",
  selectedPromptBody = "",
  onPromptSelect,
  isPromptAreaVisible = false,
  onTogglePromptArea,
  // Expand/collapse of the settings body (controlled by parent so it stays
  // mutually exclusive with the prompt composition area).
  expanded = false,
  onToggleExpanded,
  // Trigger set (mitto-r6j). `triggers` is the canonical armed-triggers list
  // from the server; `trigger` remains for backward-compat with parents that
  // still pass only the legacy scalar (primary trigger). At least one entry
  // is guaranteed by normaliseTriggersProp below.
  trigger = "schedule",
  triggers,
  delaySeconds = 5,
  maxDurationSeconds = 0,
  // onTasks trigger fields: CEL condition gating firing (empty = fire on any
  // beads/task change) + the UI preset id that was compiled into it.
  condition = "",
  conditionPreset = "",
  // Whether the active workspace has beads (`.beads` + `bd`). Gates the "On
  // tasks" tab's visibility — a workspace without beads has nothing to fire on.
  hasBeadsWorkspace = false,
  // Reason the loop was auto-stopped (e.g. "maxDuration", "maxIterations",
  // "iterationSafeguard"); empty when running. Drives the restore-dialog wording.
  stoppedReason = "",
  minDelaySeconds = MIN_COMPLETION_DELAY_SECONDS,
  onTriggerChange,
  onTriggersChange,
  onDelayChange,
  onMaxDurationChange,
  onConditionChange,
  onConditionPresetChange,
  onEditArguments,
}) {
  // Local state for editing
  const [localValue, setLocalValue] = useState(frequency.value || 1);
  const [localUnit, setLocalUnit] = useState(frequency.unit || "hours");
  // localAt is stored in LOCAL time for display/editing (converted from UTC when syncing from props)
  const [localAt, setLocalAt] = useState(utcToLocalTime(frequency.at) || "");
  const [isSaving, setIsSaving] = useState(false);
  // Local estimated next run time, kept in sync and propagated to the parent on
  // save. The live countdown + next-run display now live in the conversation
  // header subtitle, so the value is written but no longer read here.
  const [, setLocalNextScheduledAt] = useState(nextScheduledAt);
  // Triggering immediate delivery
  const [isTriggering, setIsTriggering] = useState(false);
  // Confirmation dialog state
  const [showConfirmDialog, setShowConfirmDialog] = useState(false);
  // Restore-loop confirmation dialog state (shown when re-enabling a paused schedule)
  const [showRestoreDialog, setShowRestoreDialog] = useState(false);
  // Dangerous-config confirmation dialog state (shown on Save for new, unbounded loops)
  const [showDangerDialog, setShowDangerDialog] = useState(false);
  // Reset timer checkbox state (default true = reset the countdown after manual run)
  const [resetTimer, setResetTimer] = useState(true);
  // Reset counters checkbox state in the restore dialog (default true = reset the
  // elapsed iterations + elapsed time when restoring a loop that hit its cap).
  const [resetCounters, setResetCounters] = useState(true);
  // Error dialog state (for showing errors like "session busy")
  const [errorMessage, setErrorMessage] = useState(null);
  // Local max iterations (synced from props)
  const [localMaxIterations, setLocalMaxIterations] = useState(maxIterations);
  // Local fresh-context (staged; synced from props)
  const [localFreshContext, setLocalFreshContext] = useState(freshContext);
  // Armed triggers (mitto-r6j). Set of {"schedule", "onCompletion", "onTasks"}.
  // Kept as a Set for O(1) has()/add()/delete() from checkbox handlers; the
  // wire payload is a stable-ordered array (see toStableTriggersList below).
  const [localTriggers, setLocalTriggers] = useState(
    () => new Set(normaliseTriggersProp(triggers, trigger)),
  );
  const [localDelay, setLocalDelay] = useState(delaySeconds || minDelaySeconds);
  const [localMaxDurValue, setLocalMaxDurValue] = useState(
    () => secondsToValueUnit(maxDurationSeconds).value,
  );
  const [localMaxDurUnit, setLocalMaxDurUnit] = useState(
    () => secondsToValueUnit(maxDurationSeconds).unit,
  );
  // onTasks trigger local state: the staged CEL condition text (source of truth
  // sent to the backend), the selected preset dropdown id, the single param
  // value for param-needing presets, and an inline error from a rejected save.
  const [localCondition, setLocalCondition] = useState(condition || "");
  const [localPresetId, setLocalPresetId] = useState(() =>
    resolveConditionPresetId(condition, conditionPreset),
  );
  const [localPresetParam, setLocalPresetParam] = useState(() =>
    extractPresetParam(
      resolveConditionPresetId(condition, conditionPreset),
      condition,
    ),
  );
  const [conditionError, setConditionError] = useState(null);
  // Advanced (CEL) textarea collapse: auto-opens for hand-edited ("custom")
  // conditions; otherwise starts collapsed but stays open once toggled.
  const [advancedCelExpanded, setAdvancedCelExpanded] = useState(
    () => resolveConditionPresetId(condition, conditionPreset) === "custom",
  );
  // Saving enabled state (pause/resume)
  const [isSavingEnabled, setIsSavingEnabled] = useState(false);
  // Tracks previous expanded value to detect collapse (for discarding staged edits)
  const prevExpandedRef = useRef(expanded);

  // Header tooltips can't use daisyUI's CSS tooltip: the play/pause buttons sit
  // at the panel's left edge, and a centered tooltip-bottom bubble extends left
  // into the conversations side panel, which sits in a higher stacking context
  // and paints over it (a z-index bump can't escape that context). Render those
  // through a body-level PortalTooltip instead, anchored at the cursor and
  // clamped to the viewport — same approach as the SessionItem/Beads tooltips.
  // `data-tip`/`aria-label` are kept on the buttons (test selectors and a11y),
  // but the `tooltip` classes are dropped so the occluded CSS bubble no longer
  // renders.
  const [headerTip, setHeaderTip] = useState(null);
  const headerTipTimerRef = useRef(null);
  const showHeaderTip = useCallback((e, text) => {
    if (!LOOP_SUPPORTS_HOVER || !text) return;
    const x = e.clientX;
    const y = e.clientY;
    clearTimeout(headerTipTimerRef.current);
    headerTipTimerRef.current = setTimeout(
      () => setHeaderTip({ x, y, text }),
      LOOP_TOOLTIP_DELAY_MS,
    );
  }, []);
  const hideHeaderTip = useCallback(() => {
    clearTimeout(headerTipTimerRef.current);
    setHeaderTip(null);
  }, []);
  useEffect(() => () => clearTimeout(headerTipTimerRef.current), []);

  // Calculate estimated next run time based on frequency
  const calculateNextRun = useCallback((value, unit) => {
    const now = new Date();
    let nextRun = new Date(now);

    switch (unit) {
      case "minutes":
        nextRun.setMinutes(nextRun.getMinutes() + value);
        break;
      case "hours":
        nextRun.setHours(nextRun.getHours() + value);
        break;
      case "days":
        nextRun.setDate(nextRun.getDate() + value);
        break;
      default:
        nextRun.setHours(nextRun.getHours() + value);
    }

    return nextRun.toISOString();
  }, []);

  // Sync local state when props change (e.g., from WebSocket update)
  // Convert UTC 'at' time to local time for display
  useEffect(() => {
    setLocalValue(frequency.value || 30);
    setLocalUnit(frequency.unit || "minutes");
    setLocalAt(utcToLocalTime(frequency.at) || "");
  }, [frequency.value, frequency.unit, frequency.at]);

  // Sync next scheduled time from props (server-authoritative)
  useEffect(() => {
    if (nextScheduledAt) {
      setLocalNextScheduledAt(nextScheduledAt);
    }
  }, [nextScheduledAt]);

  // Reset the resetTimer checkbox to default (true) each time the confirm dialog opens
  useEffect(() => {
    if (showConfirmDialog) {
      setResetTimer(true);
    }
  }, [showConfirmDialog]);

  // Sync localMaxIterations from props (server-authoritative updates)
  useEffect(() => {
    setLocalMaxIterations(maxIterations);
  }, [maxIterations]);

  // Sync localFreshContext from props (server-authoritative updates)
  useEffect(() => {
    setLocalFreshContext(freshContext);
  }, [freshContext]);

  // Sync triggers/delay/maxDuration from props (server-authoritative updates).
  // triggers/trigger are joined into a stable string key so the effect re-runs
  // only when the effective set actually changes (arrays would compare by
  // reference otherwise).
  const triggersPropKey = normaliseTriggersProp(triggers, trigger).join(",");
  useEffect(() => {
    setLocalTriggers(new Set(normaliseTriggersProp(triggers, trigger)));
    // triggersPropKey is a stable string join of the effective triggers list —
    // safely captures both `triggers` and legacy scalar `trigger` in one dep.
  }, [triggersPropKey]);
  useEffect(() => {
    setLocalDelay(delaySeconds || minDelaySeconds);
  }, [delaySeconds, minDelaySeconds]);
  useEffect(() => {
    const { value, unit } = secondsToValueUnit(maxDurationSeconds);
    setLocalMaxDurValue(value);
    setLocalMaxDurUnit(unit);
  }, [maxDurationSeconds]);
  // Sync onTasks condition/preset from props (server-authoritative updates,
  // e.g. GET on load or a loop_updated broadcast from another client).
  useEffect(() => {
    const id = resolveConditionPresetId(condition, conditionPreset);
    setLocalCondition(condition || "");
    setLocalPresetId(id);
    setLocalPresetParam(extractPresetParam(id, condition));
    if (id === "custom") setAdvancedCelExpanded(true);
  }, [condition, conditionPreset]);

  // Discard staged edits when the settings body collapses without saving.
  // Reverts every local field back to the server-authoritative props.
  useEffect(() => {
    const wasExpanded = prevExpandedRef.current;
    prevExpandedRef.current = expanded;
    if (wasExpanded && !expanded) {
      setLocalValue(frequency.value || 1);
      setLocalUnit(frequency.unit || "hours");
      setLocalAt(utcToLocalTime(frequency.at) || "");
      setLocalFreshContext(freshContext);
      setLocalMaxIterations(maxIterations);
      setLocalTriggers(new Set(normaliseTriggersProp(triggers, trigger)));
      setLocalDelay(delaySeconds || minDelaySeconds);
      const { value, unit } = secondsToValueUnit(maxDurationSeconds);
      setLocalMaxDurValue(value);
      setLocalMaxDurUnit(unit);
      const presetId = resolveConditionPresetId(condition, conditionPreset);
      setLocalCondition(condition || "");
      setLocalPresetId(presetId);
      setLocalPresetParam(extractPresetParam(presetId, condition));
      setConditionError(null);
    }
  }, [
    expanded,
    frequency.value,
    frequency.unit,
    frequency.at,
    freshContext,
    maxIterations,
    triggersPropKey,
    delaySeconds,
    minDelaySeconds,
    maxDurationSeconds,
    condition,
    conditionPreset,
  ]);

  // Derived: which triggers are currently armed. Panels for unarmed triggers
  // are hidden; a loop with only "schedule" armed looks exactly like it did
  // pre-r6j (single-trigger UX preserved).
  const armsSchedule = localTriggers.has("schedule");
  const armsOnCompletion = localTriggers.has("onCompletion");
  const armsOnTasks = localTriggers.has("onTasks");

  // A "new" loop conversation is one that has never delivered a run yet
  // (iteration_count is incremented only on actual delivery). Safety pre-fills
  // and the dangerous-config warning apply only while it is still new — once it
  // has started running we respect whatever the user has configured.
  const isNewLoop = iterationCount === 0;

  // Staged config has no upper bound on runs or wall-clock time.
  const stagedHasNoLimits = useMemo(
    () =>
      localMaxIterations <= 0 &&
      valueUnitToSeconds(localMaxDurValue, localMaxDurUnit) <= 0,
    [localMaxIterations, localMaxDurValue, localMaxDurUnit],
  );

  // Staged cadence is "dangerous": fires after every agent completion, fires
  // on every qualifying task change (event-driven, unbounded), or repeats
  // more frequently than DANGEROUS_FREQUENCY_SECONDS on a schedule. Any armed
  // event-driven trigger is enough to flag the config as dangerous.
  const stagedHasDangerousCadence = useMemo(() => {
    if (armsOnCompletion || armsOnTasks) return true;
    if (!armsSchedule) return false;
    return (
      valueUnitToSeconds(localValue, localUnit) < DANGEROUS_FREQUENCY_SECONDS
    );
  }, [armsSchedule, armsOnCompletion, armsOnTasks, localValue, localUnit]);

  // Warn before saving a brand-new loop conversation that could loop
  // indefinitely (dangerous cadence with no run/time limit).
  const needsDangerWarning =
    isNewLoop && stagedHasDangerousCadence && stagedHasNoLimits;

  // Human-readable reason shown in the dangerous-config confirmation dialog.
  // A single-trigger loop keeps its pre-r6j wording; multi-trigger loops
  // synthesise a compact "and" phrase so the warning stays specific.
  const dangerReasons = [];
  if (armsSchedule) {
    dangerReasons.push(`it repeats every ${localValue} ${localUnit}`);
  }
  if (armsOnCompletion) {
    dangerReasons.push("it starts again every time the agent finishes");
  }
  if (armsOnTasks) {
    dangerReasons.push("it fires every time a matching task change occurs");
  }
  const dangerReason =
    dangerReasons.length === 0
      ? "no trigger is armed"
      : dangerReasons.join(" and ");
  const dangerMessage =
    `This loop conversation has no limit on the number of runs or total ` +
    `time, and ${dangerReason}. It could keep running indefinitely. ` +
    `Set a "Max runs" or "Max time" limit, or save anyway?`;

  // Canonical, stable-ordered list of armed triggers for the wire payload
  // (schedule → onCompletion → onTasks). Independent of user click order.
  // Any other armed trigger (e.g. a future onChild) is appended afterward
  // instead of dropped — a non-nil `triggers` PATCH field REPLACES the
  // stored list wholesale server-side (internal/session/loop.go), so
  // silently filtering an unrecognized-but-armed trigger here would
  // permanently disarm it (mitto-987y.7).
  const toStableTriggersList = useCallback((set) => {
    const known = KNOWN_TRIGGERS.filter((t) => set.has(t));
    const unknown = [...set].filter((t) => !KNOWN_TRIGGERS.includes(t));
    return [...known, ...unknown];
  }, []);

  // Persist all staged settings in a single PATCH. Invoked by handleSaveAll
  // (directly, or after the dangerous-config warning is confirmed).
  const performSave = useCallback(async () => {
    if (!sessionId || isSaving) return;
    if (localTriggers.size === 0) return; // Save disabled anyway

    // Optimistic next-run estimate for schedule mode (server value overrides
    // below). Only applies when schedule is one of the armed triggers.
    if (armsSchedule) {
      setLocalNextScheduledAt(calculateNextRun(localValue, localUnit));
    }

    setIsSaving(true);
    setConditionError(null);
    try {
      const clampedDelay = Math.max(minDelaySeconds, localDelay);
      const maxDurSecs = valueUnitToSeconds(localMaxDurValue, localMaxDurUnit);
      const triggersList = toStableTriggersList(localTriggers);
      const payload = {
        triggers: triggersList,
        frequency: { value: localValue, unit: localUnit },
        fresh_context: localFreshContext,
        max_iterations: localMaxIterations,
        delay_seconds: clampedDelay,
        max_duration_seconds: maxDurSecs,
      };
      // Only include 'at' for daily schedules - convert local time to UTC
      if (localUnit === "days" && localAt) {
        payload.frequency.at = localToUtcTime(localAt);
      }
      // onTasks: send the staged CEL condition + the preset id it was
      // compiled from ("" for a hand-edited/custom condition). Only sent when
      // onTasks is one of the armed triggers.
      if (armsOnTasks) {
        payload.condition = localCondition || "";
        payload.condition_preset =
          localPresetId === "custom" ? "" : localPresetId;
      }

      let data;
      try {
        data = await getSdkClient().sessions.loop.update(sessionId, payload);
      } catch (err) {
        const msg = sdkErrorMessage(err, "Failed to save loop settings");
        console.error("Failed to save loop settings:", msg);
        // Surface invalid-CEL (and other onTasks) rejections inline near the
        // condition editor instead of failing silently.
        if (armsOnTasks) {
          setConditionError(msg);
        }
        return;
      }
      // Server response: canonical `triggers` list + legacy `trigger`
      // scalar (primary). Prefer the list; fall back to the scalar for
      // servers that pre-date the mitto-r6j response shape.
      const respTriggers = normaliseTriggersProp(data.triggers, data.trigger);
      const serverDelay = data.delay_seconds ?? clampedDelay;
      // Update with server-authoritative values
      setLocalNextScheduledAt(data.next_scheduled_at);
      setLocalTriggers(new Set(respTriggers));
      setLocalDelay(serverDelay);
      setLocalCondition(data.condition ?? localCondition);
      // Propagate to parent so props stay in sync. Both callbacks are
      // called so parents that hold either the scalar or the array stay
      // consistent (mitto-r6j).
      onFrequencyChange?.(data.frequency, data.next_scheduled_at);
      onFreshContextChange?.(data.fresh_context ?? localFreshContext);
      onMaxIterationsChange?.(data.max_iterations ?? localMaxIterations);
      onTriggerChange?.(respTriggers[0]);
      onTriggersChange?.(respTriggers);
      onDelayChange?.(serverDelay);
      onMaxDurationChange?.(data.max_duration_seconds ?? maxDurSecs);
      onConditionChange?.(data.condition ?? localCondition);
      onConditionPresetChange?.(
        data.condition_preset ??
          (localPresetId === "custom" ? "" : localPresetId),
      );
    } finally {
      setIsSaving(false);
    }
  }, [
    sessionId,
    isSaving,
    localTriggers,
    armsSchedule,
    armsOnTasks,
    localValue,
    localUnit,
    localAt,
    localFreshContext,
    localMaxIterations,
    localDelay,
    localMaxDurValue,
    localMaxDurUnit,
    localCondition,
    localPresetId,
    minDelaySeconds,
    calculateNextRun,
    toStableTriggersList,
    onFrequencyChange,
    onFreshContextChange,
    onMaxIterationsChange,
    onTriggerChange,
    onTriggersChange,
    onDelayChange,
    onMaxDurationChange,
    onConditionChange,
    onConditionPresetChange,
  ]);

  // Save entry point (Save button). For a brand-new loop conversation with
  // a dangerous, unbounded cadence, confirm first; otherwise persist directly.
  const handleSaveAll = useCallback(() => {
    if (isSaving) return;
    if (needsDangerWarning) {
      setShowDangerDialog(true);
      return;
    }
    performSave();
  }, [isSaving, needsDangerWarning, performSave]);

  // Confirm saving despite the dangerous-config warning.
  const handleConfirmDanger = useCallback(() => {
    setShowDangerDialog(false);
    performSave();
  }, [performSave]);

  // Dismiss the dangerous-config warning without saving.
  const handleCancelDanger = useCallback(() => {
    setShowDangerDialog(false);
  }, []);

  // Handle value change
  const handleValueChange = useCallback((e) => {
    const newValue = parseInt(e.target.value, 10) || 1;
    const clampedValue = Math.max(1, Math.min(999, newValue));
    setLocalValue(clampedValue);
  }, []);

  // Handle unit change (staged; clears 'at' when switching away from days)
  const handleUnitChange = useCallback((e) => {
    const newUnit = e.target.value;
    setLocalUnit(newUnit);
    if (newUnit !== "days") {
      setLocalAt("");
    }
  }, []);

  // Handle time change
  const handleAtChange = useCallback((e) => {
    setLocalAt(e.target.value);
  }, []);

  // Handle click on the run-now button - show confirmation dialog
  const handleIconClick = useCallback(() => {
    if (isTriggering || !sessionId || isStreaming) return;
    setShowConfirmDialog(true);
  }, [isTriggering, sessionId, isStreaming]);

  // Handle confirmation of immediate delivery
  const handleConfirmImmediateDelivery = useCallback(async () => {
    if (!sessionId) return;

    setIsTriggering(true);
    try {
      await getSdkClient().sessions.loop.runNow(sessionId, resetTimer);
      // Success - the WebSocket will notify us of the loop_started event
      setShowConfirmDialog(false);
    } catch (err) {
      console.error("Failed to trigger immediate delivery:", err);
      // Show error to user
      if (errorStatus(err) === 409) {
        setErrorMessage(
          "Session is currently processing a prompt. Please wait and try again.",
        );
      } else {
        setErrorMessage(
          "Failed to trigger immediate delivery. Please try again.",
        );
      }
    } finally {
      setIsTriggering(false);
    }
  }, [sessionId, resetTimer]);

  // Handle cancellation of the confirmation dialog
  const handleCancelConfirmDialog = useCallback(() => {
    if (!isTriggering) {
      setShowConfirmDialog(false);
    }
  }, [isTriggering]);

  // Handle closing the error dialog
  const handleCloseErrorDialog = useCallback(() => {
    setErrorMessage(null);
  }, []);

  // Handle fresh context toggle (staged)
  const handleFreshContextChange = useCallback((e) => {
    setLocalFreshContext(e.target.checked);
  }, []);

  // Handle max iterations input change (staged)
  const handleMaxIterationsChange = useCallback((e) => {
    setLocalMaxIterations(Math.max(0, parseInt(e.target.value, 10) || 0));
  }, []);

  // Toggle a trigger in the armed-set (mitto-r6j). Refuses to remove the last
  // remaining trigger (the panel's invariant: at least one armed trigger).
  // Arming an event-driven trigger pre-fills the safety-limits (5 runs / 1h
  // max time) for a brand-new loop conversation and enforces the minimum
  // on-completion delay; never overrides an established config.
  const handleTriggerToggle = useCallback(
    (name) => {
      setLocalTriggers((prev) => {
        const next = new Set(prev);
        if (next.has(name)) {
          if (next.size <= 1) return prev; // preserve invariant
          next.delete(name);
        } else {
          next.add(name);
        }
        return next;
      });
      setConditionError(null);
      // Detect an ARM transition (was not armed, is now armed) so we can
      // pre-fill safety limits and minimum delay only when transitioning
      // OFF→ON. localTriggers here is the pre-update value; !has(name) means
      // we just added it. Toggles the other way (ARM→OFF) skip the pre-fill.
      const nowArmed = !localTriggers.has(name);
      if (name === "onCompletion" && nowArmed) {
        setLocalDelay((prev) =>
          Math.max(minDelaySeconds, prev || minDelaySeconds),
        );
      }
      if (
        (name === "onCompletion" || name === "onTasks") &&
        nowArmed &&
        isNewLoop
      ) {
        setLocalMaxIterations((prev) => (prev > 0 ? prev : 5));
        if (valueUnitToSeconds(localMaxDurValue, localMaxDurUnit) === 0) {
          setLocalMaxDurValue(1);
          setLocalMaxDurUnit("hours");
        }
      }
    },
    [
      isNewLoop,
      localMaxDurValue,
      localMaxDurUnit,
      localTriggers,
      minDelaySeconds,
    ],
  );

  // Clamp the on-completion delay to the minimum on blur (staged)
  const handleDelayBlur = useCallback(() => {
    setLocalDelay((prev) => Math.max(minDelaySeconds, prev));
  }, [minDelaySeconds]);

  // Handle condition-preset dropdown selection (staged). Selecting a known
  // preset compiles it (with the current param, if any) into localCondition;
  // selecting "custom" leaves whatever is already in the Advanced textarea.
  const handlePresetSelect = useCallback(
    (e) => {
      const id = e.target.value;
      setLocalPresetId(id);
      setConditionError(null);
      if (id === "custom") {
        setAdvancedCelExpanded(true);
        return;
      }
      const preset = CONDITION_PRESETS.find((p) => p.id === id);
      setLocalCondition(
        presetConditionFor(id, preset?.needsParam ? localPresetParam : ""),
      );
    },
    [localPresetParam],
  );

  // Handle the preset's single parameter input (issue type or label; staged).
  const handlePresetParamChange = useCallback(
    (e) => {
      const val = e.target.value;
      setLocalPresetParam(val);
      setConditionError(null);
      setLocalCondition(presetConditionFor(localPresetId, val));
    },
    [localPresetId],
  );

  // Handle direct edits to the Advanced (CEL) textarea (staged). Hand-editing
  // switches the preset dropdown to "custom" since the text may no longer
  // match any canonical preset shape.
  const handleConditionTextareaInput = useCallback((e) => {
    setLocalCondition(e.target.value);
    setLocalPresetId("custom");
    setConditionError(null);
  }, []);

  // Toggle the Advanced (CEL) collapse open/closed.
  const toggleAdvancedCel = useCallback(() => {
    setAdvancedCelExpanded((v) => !v);
  }, []);

  // Handle pause/resume toggle
  const handlePauseResume = useCallback(async () => {
    if (!sessionId || isSavingEnabled) return;
    const newEnabled = !disabled;
    setIsSavingEnabled(true);
    try {
      await getSdkClient().sessions.loop.update(sessionId, {
        enabled: newEnabled,
      });
      if (onLoopEnabledChange) onLoopEnabledChange(newEnabled);
    } catch (err) {
      console.error("Failed to update loop enabled:", err);
    } finally {
      setIsSavingEnabled(false);
    }
  }, [sessionId, disabled, isSavingEnabled, onLoopEnabledChange]);

  // Handle click on the play button while paused - show restore confirmation
  const handleRestoreClick = useCallback(() => {
    if (isSavingEnabled || !sessionId) return;
    setShowRestoreDialog(true);
  }, [isSavingEnabled, sessionId]);

  // Handle confirmation of restoring (re-enabling) the loop schedule
  const handleConfirmRestore = useCallback(async () => {
    if (!sessionId) return;
    setIsSavingEnabled(true);
    try {
      // When the loop was auto-stopped by a cap, optionally reset the elapsed
      // iterations/time so it can resume instead of immediately re-stopping.
      const limitWasStopped =
        stoppedReason === "maxDuration" ||
        stoppedReason === "maxIterations" ||
        stoppedReason === "iterationSafeguard";
      const body = { enabled: true };
      if (limitWasStopped && resetCounters) {
        body.reset_counters = true;
      }
      await getSdkClient().sessions.loop.update(sessionId, body);
      if (onLoopEnabledChange) onLoopEnabledChange(true);
      // mitto-5cj: after re-enabling the loop, also POST run-now so the
      // user's play click actually fires the prompt on the FIRST click
      // (previously they had to click play twice — once to re-arm, once
      // to fire). Guards: skip when the loop was cap-stopped and the
      // user did NOT tick reset_counters (the fire would immediately
      // re-stop), and swallow the 409 "agent streaming" case silently
      // since the loop is now enabled either way.
      const wouldImmediatelyReStop = limitWasStopped && !resetCounters;
      if (!wouldImmediatelyReStop) {
        try {
          await getSdkClient().sessions.loop.runNow(sessionId, true);
        } catch (_runNowErr) {
          // Non-fatal: the loop is enabled; the next tick will fire.
        }
      }
      setShowRestoreDialog(false);
    } catch (err) {
      console.error("Failed to restore loop schedule:", err);
      setErrorMessage("Failed to restore the loop schedule. Please try again.");
    } finally {
      setIsSavingEnabled(false);
    }
  }, [sessionId, onLoopEnabledChange, stoppedReason, resetCounters]);

  // Handle cancellation of the restore confirmation dialog
  const handleCancelRestore = useCallback(() => {
    if (!isSavingEnabled) setShowRestoreDialog(false);
  }, [isSavingEnabled]);

  // Panel classes - part of normal document flow (not absolute positioned).
  // overflow-visible allows the prompt-selector dropdown to escape the card boundary upward.
  const panelClasses = `loop-frequency-panel w-full bg-mitto-surface-hover dark:bg-mitto-surface-3/95 backdrop-blur-sm border border-mitto-border dark:border-mitto-border-2 rounded-lg overflow-visible transition-all duration-300 ease-out ${
    isOpen
      ? "opacity-100 mb-3"
      : "opacity-0 pointer-events-none h-0 border-0 mb-0"
  }`;

  const panelStyle = isOpen ? "" : "height: 0px;";

  // The `disabled` prop is true when loop is ACTIVE/enabled. When the schedule has
  // been paused (e.g. the conversation disabled its own loop via MCP), the
  // play button restores the schedule and the pause button is greyed out.
  const loopPaused = !disabled;

  // When the loop was auto-stopped by a cap (max-duration / max-iterations), the
  // restore dialog offers to reset the elapsed iterations and elapsed time so the
  // loop can actually resume (otherwise it would immediately re-stop at the cap).
  const limitStopped =
    stoppedReason === "maxDuration" ||
    stoppedReason === "maxIterations" ||
    stoppedReason === "iterationSafeguard";
  const hasMaxDuration = (maxDurationSeconds || 0) > 0;
  const hasMaxIterations = (maxIterations || 0) > 0;
  // Pick a checkbox label reflecting which caps are configured.
  let resetCountersLabel = "Reset elapsed time and iteration count";
  if (hasMaxDuration && !hasMaxIterations) {
    resetCountersLabel = "Reset elapsed time";
  } else if (!hasMaxDuration && hasMaxIterations) {
    resetCountersLabel = "Reset iteration count";
  }
  const stoppedReasonText =
    stoppedReason === "maxDuration"
      ? "maximum run time"
      : "maximum number of iterations";
  const restoreMessage = limitStopped
    ? `This conversation stopped because it reached its ${stoppedReasonText}. Restore it to keep iterating.`
    : "Do you want to restore the loop schedule for this conversation?";

  // Compute whether the edit-arguments button should be enabled. Prefer
  // allPrompts (full workspace list) so menu-scoped loop prompts (e.g.
  // `menus: beadsList`) — which are filtered out of `prompts` by
  // useWorkspacePrompts and correctly greyed in the LoopPromptSelector picker —
  // still resolve here for the sliders/edit-args button.
  const selectedPrompt = selectedPromptName
    ? (allPrompts || []).find((p) => p.name === selectedPromptName) ||
      (prompts || []).find((p) => p.name === selectedPromptName)
    : null;
  const selectedPromptParams = selectedPrompt
    ? promptDialogParameters(selectedPrompt)
    : [];
  const canEditArgs = !!selectedPromptName && selectedPromptParams.length > 0;

  return html`
    <${Fragment}>
      <!-- Confirmation dialog for immediate delivery -->
      <${ConfirmDialog}
        isOpen=${showConfirmDialog}
        title="Run Now"
        message="Do you want to send this message now?"
        confirmLabel="Send"
        cancelLabel="Cancel"
        confirmVariant="primary"
        isLoading=${isTriggering}
        onConfirm=${handleConfirmImmediateDelivery}
        onCancel=${handleCancelConfirmDialog}
      >
        <label class="flex items-center gap-2 mt-3 text-sm text-mitto-text-secondary cursor-pointer select-none">
          <input
            type="checkbox"
            checked=${resetTimer}
            onInput=${(e) => setResetTimer(e.target.checked)}
            class="w-4 h-4 rounded border-mitto-border-3 text-mitto-accent focus:ring-mitto-accent-500 cursor-pointer"
            data-testid="reset-timer-checkbox"
          />
          Reset countdown for next scheduled run
        </label>
      </${ConfirmDialog}>

      <!-- Confirmation dialog for restoring a paused loop schedule -->
      <${ConfirmDialog}
        isOpen=${showRestoreDialog}
        title="Restore loop schedule"
        message=${restoreMessage}
        confirmLabel="Restore"
        cancelLabel="Cancel"
        confirmVariant="primary"
        isLoading=${isSavingEnabled}
        onConfirm=${handleConfirmRestore}
        onCancel=${handleCancelRestore}
      >
        ${
          limitStopped
            ? html`<label
                class="flex items-center gap-2 mt-3 text-sm text-mitto-text-secondary cursor-pointer select-none"
              >
                <input
                  type="checkbox"
                  checked=${resetCounters}
                  onInput=${(e) => setResetCounters(e.target.checked)}
                  class="w-4 h-4 rounded border-mitto-border-3 text-mitto-accent focus:ring-mitto-accent-500 cursor-pointer"
                  data-testid="reset-counters-checkbox"
                />
                ${resetCountersLabel}
              </label>`
            : null
        }
      </${ConfirmDialog}>

      <!-- Error dialog for showing errors -->
      <${ConfirmDialog}
        isOpen=${errorMessage !== null}
        title="Error"
        message=${errorMessage || ""}
        confirmLabel="OK"
        confirmVariant="primary"
        onConfirm=${handleCloseErrorDialog}
        onCancel=${handleCloseErrorDialog}
      />

      <!-- Dangerous-config warning: new, unbounded, high-frequency/on-completion -->
      <${ConfirmDialog}
        isOpen=${showDangerDialog}
        title="Are you sure?"
        message=${dangerMessage}
        confirmLabel="Save anyway"
        cancelLabel="Cancel"
        confirmVariant="danger"
        onConfirm=${handleConfirmDanger}
        onCancel=${handleCancelDanger}
      />

      <div
        class="${panelClasses}"
        style="${panelStyle}"
        data-testid="loop-frequency-panel"
      >
        <!-- HEADER: always visible when isOpen (single ~44px row) -->
        <div class="h-11 px-3 flex items-center gap-2 text-sm">
          <!-- Play button: runs the prompt now when loop is active, or
               restores (re-enables) the schedule when loop is paused. -->
          <button
            type="button"
            onClick=${loopPaused ? handleRestoreClick : handleIconClick}
            onMouseEnter=${(e) => showHeaderTip(e, loopPaused ? "Restore loop schedule" : isStreaming ? "Wait for agent to finish responding" : "Run this loop prompt now")}
            onMouseLeave=${hideHeaderTip}
            onMouseDown=${hideHeaderTip}
            disabled=${loopPaused ? isSavingEnabled : isTriggering || isStreaming}
            class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 transition-colors ${(loopPaused ? isSavingEnabled : isTriggering || isStreaming) ? "opacity-50 cursor-not-allowed" : "cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3"}"
            data-tip=${loopPaused ? "Restore loop schedule" : isStreaming ? "Wait for agent to finish responding" : "Run this loop prompt now"}
            aria-label=${loopPaused ? "Restore loop schedule" : isStreaming ? "Wait for agent to finish responding" : "Run this loop prompt now"}
            data-testid="loop-run-now-button"
          >
            ${
              (loopPaused ? isSavingEnabled : isTriggering)
                ? html`<span
                    class="loading loading-spinner w-4 h-4 text-mitto-text-secondary"
                  ></span>`
                : html`<${PlayFilledIcon}
                    className="w-4 h-4 text-mitto-text-secondary"
                  />`
            }
          </button>

          <!-- Pause button: pauses loop runs when active; greyed out when
               already paused (use the play button to restore the schedule). -->
          <button
            type="button"
            onClick=${handlePauseResume}
            onMouseEnter=${(e) => showHeaderTip(e, loopPaused ? "Loop runs are paused" : "Pause loop runs")}
            onMouseLeave=${hideHeaderTip}
            onMouseDown=${hideHeaderTip}
            disabled=${loopPaused || isSavingEnabled}
            class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 transition-colors ${loopPaused || isSavingEnabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3"}"
            data-tip=${loopPaused ? "Loop runs are paused" : "Pause loop runs"}
            aria-label=${loopPaused ? "Loop runs are paused" : "Pause loop runs"}
            data-testid="loop-pause-resume-button"
          >
            ${
              !loopPaused && isSavingEnabled
                ? html`<span
                    class="loading loading-spinner w-4 h-4 text-mitto-text-secondary"
                  ></span>`
                : html`<${PauseFilledIcon}
                    className="w-4 h-4 text-mitto-text-secondary"
                  />`
            }
          </button>

          <!-- Inline prompt selector (header placement). Always visible across
               breakpoints so the prompt stays reachable without expanding the
               properties section. -->
          <div class="min-w-0">
            <${LoopPromptSelector}
              prompts=${prompts}
              selectedPromptName=${selectedPromptName}
              selectedPromptBody=${selectedPromptBody}
              disabled=${false}
              onSelect=${onPromptSelect}
            />
          </div>

          <!-- Edit prompt arguments button: opens PromptParameterDialog pre-filled
               with the current stored arguments. Disabled when no named prompt is
               selected or when the selected prompt declares no parameters. -->
          <button
            type="button"
            onClick=${() => onEditArguments && onEditArguments()}
            onMouseEnter=${(e) => showHeaderTip(e, "Set prompt arguments")}
            onMouseLeave=${hideHeaderTip}
            onMouseDown=${hideHeaderTip}
            disabled=${!canEditArgs}
            class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 transition-colors ${!canEditArgs ? "opacity-50 cursor-not-allowed" : "cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3"}"
            data-tip="Set prompt arguments"
            aria-label="Set prompt arguments"
            data-testid="loop-edit-args-button"
          >
            <${SlidersIcon} className="w-4 h-4 text-mitto-text-secondary" />
          </button>

          <!-- Flex spacer -->
          <div class="flex-1 min-w-0"></div>

          <!-- While expanded: staged-edit Save button. While collapsed: nothing
               (trigger, run-count, and max-time glance info now live in the
               always-visible conversation-header subtitle). -->
          ${
            expanded &&
            html`<button
              type="button"
              onClick=${handleSaveAll}
              disabled=${isSaving}
              class="btn btn-primary btn-sm shrink-0"
              data-testid="loop-save-button"
            >
              ${isSaving
                ? html`<span class="loading loading-spinner w-4 h-4"></span>`
                : "Save"}
            </button>`
          }

          <!-- Toggle message input area button (Mitto bubble). Sits next to the
               expand/collapse chevron on the right edge of the header. Hidden
               while the properties body is expanded to avoid crowding the Save
               button. -->
          ${
            onTogglePromptArea &&
            !expanded &&
            html`<button
              type="button"
              onClick=${onTogglePromptArea}
              onMouseEnter=${(e) =>
                showHeaderTip(
                  e,
                  isPromptAreaVisible
                    ? "Hide message input"
                    : "Show message input",
                )}
              onMouseLeave=${hideHeaderTip}
              onMouseDown=${hideHeaderTip}
              class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3 transition-colors"
              data-tip=${isPromptAreaVisible
                ? "Hide message input"
                : "Show message input"}
              aria-label=${isPromptAreaVisible
                ? "Hide message input"
                : "Show message input"}
              data-testid="loop-toggle-prompt-area"
            >
              <${ChatBubbleIcon}
                className="w-4 h-4 text-mitto-text-secondary"
              />
            </button>`
          }

          <!-- Expand/collapse chevron button -->
          <button
            type="button"
            onClick=${onToggleExpanded}
            onMouseEnter=${(e) => showHeaderTip(e, expanded ? "Collapse settings" : "Expand settings")}
            onMouseLeave=${hideHeaderTip}
            onMouseDown=${hideHeaderTip}
            class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3 transition-colors"
            data-tip=${expanded ? "Collapse settings" : "Expand settings"}
            aria-label=${expanded ? "Collapse settings" : "Expand settings"}
            data-testid="loop-expand-toggle"
          >
            <svg
              class="w-4 h-4 text-mitto-text-secondary transition-transform duration-200 ${expanded ? "rotate-180" : ""}"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
            </svg>
          </button>
        </div>

        ${
          headerTip &&
          html`
            <${PortalTooltip}
              x=${headerTip.x}
              y=${headerTip.y}
              text=${headerTip.text}
            />
          `
        }

        <!-- BODY: collapsed by default; expands when user clicks the chevron -->
        <div
          class="transition-all duration-300 ease-out border-t border-mitto-border dark:border-mitto-border-2 ${
            expanded
              ? "max-h-[32rem] opacity-100"
              : "max-h-0 opacity-0 overflow-hidden pointer-events-none"
          }"
        >
          <!-- Trigger checkboxes (mitto-r6j): armable set of Schedule | On
               completion | On tasks (last one only on beads workspaces). At
               least one entry stays armed — the toggle handler refuses to
               remove the last remaining trigger. -->
          <fieldset
            class="px-4 pt-2 flex flex-wrap items-center gap-4 text-sm"
            data-testid="loop-triggers"
          >
            <legend class="sr-only">Loop triggers</legend>
            <label
              class="label cursor-pointer gap-2 py-0"
              data-testid="loop-trigger-label-schedule"
            >
              <input
                type="checkbox"
                class="checkbox checkbox-sm"
                checked=${armsSchedule}
                onChange=${() => handleTriggerToggle("schedule")}
                data-testid="loop-trigger-check-schedule"
              />
              <span class="label-text">Schedule</span>
            </label>
            <label
              class="label cursor-pointer gap-2 py-0"
              data-testid="loop-trigger-label-oncompletion"
            >
              <input
                type="checkbox"
                class="checkbox checkbox-sm"
                checked=${armsOnCompletion}
                onChange=${() => handleTriggerToggle("onCompletion")}
                data-testid="loop-trigger-check-oncompletion"
              />
              <span class="label-text">On completion</span>
            </label>
            ${
              hasBeadsWorkspace &&
              html`
                <label
                  class="label cursor-pointer gap-2 py-0"
                  data-testid="loop-trigger-label-ontasks"
                >
                  <input
                    type="checkbox"
                    class="checkbox checkbox-sm"
                    checked=${armsOnTasks}
                    onChange=${() => handleTriggerToggle("onTasks")}
                    data-testid="loop-trigger-check-ontasks"
                  />
                  <span class="label-text">On tasks</span>
                </label>
              `
            }
          </fieldset>

          <!-- Per-trigger sub-panels: shown only for armed triggers, in the
               canonical schedule → onCompletion → onTasks order. -->
          ${
            armsSchedule &&
            html`
              <!-- Schedule: Run every N units -->
              <div
                class="px-4 pt-2 pb-2 flex items-center gap-3 text-sm"
                data-testid="loop-panel-schedule"
              >
                <span
                  class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
                  >Run every</span
                >

                <input
                  type="number"
                  min="1"
                  max="999"
                  value=${localValue}
                  onInput=${handleValueChange}
                  disabled=${isSaving}
                  class="input input-sm w-16 shrink-0 text-center"
                />

                <!-- shrink-0 + fixed width: daisyUI .select has flex-shrink:1 and overflow:hidden,
                     which lets it collapse to just the chevron (hiding the unit text) in tight rows -->
                <select
                  value=${localUnit}
                  onChange=${handleUnitChange}
                  disabled=${isSaving}
                  class="select select-sm shrink-0 w-24"
                >
                  <option value="minutes">minutes</option>
                  <option value="hours">hours</option>
                  <option value="days">days</option>
                </select>

                <!-- Time picker (only shown for daily schedules) -->
                ${localUnit === "days" &&
                html`
                  <span
                    class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
                    >at</span
                  >
                  <input
                    type="time"
                    value=${localAt}
                    onInput=${handleAtChange}
                    disabled=${isSaving}
                    class="h-8 px-2 min-w-16 shrink-0 bg-white dark:bg-mitto-surface-2 border border-mitto-border dark:border-mitto-border-2 rounded text-mitto-text-strong text-sm focus:outline-none focus:ring-1 focus:ring-mitto-accent-500 ${isSaving
                      ? "opacity-50 cursor-not-allowed"
                      : ""}"
                    placeholder="HH:MM"
                  />
                `}
              </div>
            `
          }
          ${
            armsOnCompletion &&
            html`
              <!-- On-completion: delay after agent finishes -->
              <div
                class="px-4 pt-2 pb-2 flex items-center gap-3 text-sm"
                data-testid="loop-panel-oncompletion"
              >
                <span
                  class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
                  >Wait</span
                >
                <input
                  type="number"
                  min="${minDelaySeconds}"
                  value=${localDelay}
                  onInput=${(e) =>
                    setLocalDelay(
                      Math.max(0, parseInt(e.target.value, 10) || 0),
                    )}
                  onBlur=${handleDelayBlur}
                  class="input input-sm w-20 shrink-0 text-center"
                  data-testid="loop-delay-input"
                />
                <span
                  class="text-xs text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
                >
                  seconds after the agent finishes (min ${minDelaySeconds}s)
                </span>
              </div>
            `
          }
          ${
            armsOnTasks &&
            html`
              <!-- On-tasks: condition editor (preset + advanced CEL) -->
              <div
                class="px-4 pt-2 pb-2 text-sm"
                data-testid="loop-panel-ontasks"
              >
                <div class="flex items-center gap-3">
                  <span
                    class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
                    >Fire when</span
                  >
                  <select
                    value=${localPresetId}
                    onChange=${handlePresetSelect}
                    class="select select-sm shrink-0 flex-1"
                    data-testid="loop-condition-preset-select"
                  >
                    ${CONDITION_PRESETS.map(
                      (p) => html`<option value=${p.id}>${p.label}</option>`,
                    )}
                    <option value="custom">Custom (advanced)</option>
                  </select>
                </div>
                ${(() => {
                  const preset = CONDITION_PRESETS.find(
                    (p) => p.id === localPresetId,
                  );
                  return (
                    preset?.needsParam &&
                    html`
                      <div class="flex items-center gap-3 mt-2">
                        <span
                          class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0 w-24"
                          >${preset.paramLabel}</span
                        >
                        <input
                          type="text"
                          value=${localPresetParam}
                          onInput=${handlePresetParamChange}
                          placeholder=${preset.paramPlaceholder}
                          class="input input-sm flex-1"
                          data-testid="loop-condition-preset-param"
                        />
                      </div>
                    `
                  );
                })()}

                <div
                  class="collapse collapse-arrow mt-2 bg-mitto-surface-2 dark:bg-mitto-surface-3 border border-mitto-border dark:border-mitto-border-2"
                >
                  <input
                    type="checkbox"
                    checked=${advancedCelExpanded}
                    onChange=${toggleAdvancedCel}
                  />
                  <div class="collapse-title text-xs font-medium py-2 min-h-0">
                    Advanced (CEL)
                  </div>
                  <div class="collapse-content text-xs">
                    <textarea
                      value=${localCondition}
                      onInput=${handleConditionTextareaInput}
                      placeholder="Empty = fire on any task change"
                      rows="2"
                      class="textarea textarea-sm w-full font-mono"
                      data-testid="loop-condition-textarea"
                    ></textarea>
                    <div
                      class="mt-2 text-mitto-text-muted dark:text-mitto-text-300"
                    >
                      Variables:
                      <code>Tasks</code> (current snapshot),
                      <code>Prev</code> (previous snapshot),
                      <code>Changes</code> (added/updated/removed/touched since
                      last run). Example:
                      <code
                        >Tasks.OpenByType["bug"] &gt;
                        Prev.OpenByType["bug"]</code
                      >
                    </div>
                  </div>
                </div>

                ${conditionError &&
                html`
                  <div
                    class="mt-2 text-xs text-mitto-danger"
                    data-testid="loop-condition-error"
                  >
                    ${conditionError}
                  </div>
                `}
              </div>
            `
          }

          <!-- Fresh-context row (applies to schedule, onCompletion, and onTasks) -->
          <div class="px-4 pb-2 flex items-center gap-2 text-sm border-t border-mitto-border dark:border-mitto-border-2 pt-2">
            <input
              type="checkbox"
              id="fresh-context-checkbox-${sessionId}"
              checked=${localFreshContext}
              onInput=${handleFreshContextChange}
              class="w-4 h-4 rounded border-mitto-border-3 text-mitto-accent focus:ring-mitto-accent-500 cursor-pointer shrink-0"
              data-testid="fresh-context-checkbox"
            />
            <label
              for="fresh-context-checkbox-${sessionId}"
              class="text-mitto-text-muted dark:text-mitto-text-300 cursor-pointer select-none flex-1"
            >
              Start each run with a fresh context
            </label>
          </div>

          <!-- Max runs row -->
          <div class="px-4 pb-2 flex items-center gap-3 text-sm border-t border-mitto-border dark:border-mitto-border-2 pt-2">
            <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0">Max runs</span>
            <input
              type="number"
              min="0"
              max="9999"
              value=${localMaxIterations}
              onInput=${handleMaxIterationsChange}
              class="input input-sm w-20 text-center shrink-0"
              data-testid="loop-panel-max-iterations"
            />
            <span class="text-xs text-mitto-text-muted dark:text-mitto-text-300 shrink-0">(0 =${" "}
              <span class="text-lg leading-none align-middle">∞</span>)</span>
            <div class="flex-1"></div>
            <!-- Run progress (repeated here in expanded body for full detail) -->
            ${
              maxIterations > 0
                ? html`<span class="text-xs text-mitto-text-500 shrink-0"
                    >Run ${iterationCount} of ${maxIterations}</span
                  >`
                : html`<span class="text-xs text-mitto-text-500 shrink-0"
                    >${iterationCount} run${iterationCount !== 1 ? "s" : ""}
                    ·${" "}
                    <span class="text-lg leading-none align-middle"
                      >∞</span
                    ></span
                  >`
            }
          </div>

          <!-- Max time row -->
          <div class="px-4 pb-2 flex items-center gap-3 text-sm border-t border-mitto-border dark:border-mitto-border-2 pt-2">
            <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0">Max time</span>
            <input
              type="number"
              min="0"
              max="9999"
              value=${localMaxDurValue}
              onInput=${(e) => setLocalMaxDurValue(Math.max(0, parseInt(e.target.value, 10) || 0))}
              class="input input-sm w-20 text-center shrink-0"
              data-testid="loop-max-duration-value"
            />
            <select
              value=${localMaxDurUnit}
              onChange=${(e) => setLocalMaxDurUnit(e.target.value)}
              class="select select-sm shrink-0 w-24"
              data-testid="loop-max-duration-unit"
            >
              <option value="minutes">minutes</option>
              <option value="hours">hours</option>
              <option value="days">days</option>
            </select>
            <span class="text-xs text-mitto-text-muted dark:text-mitto-text-300 shrink-0">(0 =${" "}
              <span class="text-lg leading-none align-middle">∞</span>)</span>
          </div>
        </div>
      </div>
    </${Fragment}>
  `;
}
