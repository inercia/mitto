// Mitto Web Interface - Periodic Frequency Panel Component
// Single merged card: compact header (always visible) + collapsible body (settings).

const { useState, useEffect, useCallback, useMemo, html, Fragment } =
  window.preact;

import {
  PeriodicFilledIcon,
  PlayFilledIcon,
  PauseFilledIcon,
} from "./Icons.js";
import { PeriodicPromptSelector } from "./PeriodicPromptSelector.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { secureFetch, authFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";
import { CountdownDisplay } from "./CountdownDisplay.js";

/** Minimum delay for on-completion trigger (seconds). Used for client-side clamp helper text. */
const MIN_COMPLETION_DELAY_SECONDS = 5;

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
 * PeriodicFrequencyPanel component - merged periodic settings card.
 * Header (always visible when isOpen): run-now, prompt selector, status, pause/resume, expand toggle.
 * Body (collapsed by default): frequency inputs, fresh-context, max-runs.
 *
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the card is visible (shown when periodic is enabled)
 * @param {boolean} props.disabled - true when periodic is active/enabled (controls pause vs resume label)
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
 * @param {Function} props.onPeriodicEnabledChange - Callback when periodic is paused/resumed
 * @param {Array} props.prompts - Available workspace prompts for the inline selector
 * @param {string} props.selectedPromptName - Currently selected periodic prompt name
 * @param {Function} props.onPromptSelect - Callback when a prompt is selected: (promptName) => void
 * @param {boolean} props.isPromptAreaVisible - Whether the prompt composition area is visible
 * @param {Function} props.onTogglePromptArea - Callback to toggle prompt composition area visibility
 */
export function PeriodicFrequencyPanel({
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
  onPeriodicEnabledChange,
  prompts = [],
  selectedPromptName = "",
  onPromptSelect,
  isPromptAreaVisible = false,
  onTogglePromptArea,
  // Expand/collapse of the settings body (controlled by parent so it stays
  // mutually exclusive with the prompt composition area).
  expanded = false,
  onToggleExpanded,
  // On-completion trigger fields
  trigger = "schedule",
  delaySeconds = 5,
  maxDurationSeconds = 0,
  minDelaySeconds = MIN_COMPLETION_DELAY_SECONDS,
  onTriggerChange,
  onDelayChange,
  onMaxDurationChange,
}) {
  // Local state for editing
  const [localValue, setLocalValue] = useState(frequency.value || 1);
  const [localUnit, setLocalUnit] = useState(frequency.unit || "hours");
  // localAt is stored in LOCAL time for display/editing (converted from UTC when syncing from props)
  const [localAt, setLocalAt] = useState(utcToLocalTime(frequency.at) || "");
  const [isSaving, setIsSaving] = useState(false);
  // Local estimated next run time (updated immediately on frequency change)
  const [localNextScheduledAt, setLocalNextScheduledAt] =
    useState(nextScheduledAt);
  // Triggering immediate delivery
  const [isTriggering, setIsTriggering] = useState(false);
  // Confirmation dialog state
  const [showConfirmDialog, setShowConfirmDialog] = useState(false);
  // Reset timer checkbox state (default true = reset the countdown after manual run)
  const [resetTimer, setResetTimer] = useState(true);
  // Error dialog state (for showing errors like "session busy")
  const [errorMessage, setErrorMessage] = useState(null);
  // Local max iterations (synced from props)
  const [localMaxIterations, setLocalMaxIterations] = useState(maxIterations);
  // On-completion trigger local state
  const [localTrigger, setLocalTrigger] = useState(trigger || "schedule");
  const [localDelay, setLocalDelay] = useState(delaySeconds || minDelaySeconds);
  const [localMaxDurValue, setLocalMaxDurValue] = useState(
    () => secondsToValueUnit(maxDurationSeconds).value,
  );
  const [localMaxDurUnit, setLocalMaxDurUnit] = useState(
    () => secondsToValueUnit(maxDurationSeconds).unit,
  );
  // Saving enabled state (pause/resume)
  const [isSavingEnabled, setIsSavingEnabled] = useState(false);

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

  // Sync trigger/delay/maxDuration from props (server-authoritative updates)
  useEffect(() => {
    setLocalTrigger(trigger || "schedule");
  }, [trigger]);
  useEffect(() => {
    setLocalDelay(delaySeconds || minDelaySeconds);
  }, [delaySeconds, minDelaySeconds]);
  useEffect(() => {
    const { value, unit } = secondsToValueUnit(maxDurationSeconds);
    setLocalMaxDurValue(value);
    setLocalMaxDurUnit(unit);
  }, [maxDurationSeconds]);

  // Derived: whether this periodic is in on-completion mode
  const isOnCompletion = localTrigger === "onCompletion";

  // Save frequency to backend
  // Note: newAt is in LOCAL time, needs to be converted to UTC before sending
  const saveFrequency = useCallback(
    async (newValue, newUnit, newAtLocal) => {
      if (!sessionId || isSaving) return;

      // Immediately update local next run time estimate
      setLocalNextScheduledAt(calculateNextRun(newValue, newUnit));

      setIsSaving(true);
      try {
        const payload = {
          frequency: {
            value: newValue,
            unit: newUnit,
          },
        };
        // Only include 'at' for daily schedules - convert local time to UTC
        if (newUnit === "days" && newAtLocal) {
          payload.frequency.at = localToUtcTime(newAtLocal);
        }

        const response = await secureFetch(
          apiUrl(`/api/sessions/${sessionId}/periodic`),
          {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          },
        );

        if (response.ok) {
          const data = await response.json();
          // Update with server-authoritative next scheduled time
          setLocalNextScheduledAt(data.next_scheduled_at);
          if (onFrequencyChange) {
            onFrequencyChange(data.frequency, data.next_scheduled_at);
          }
        } else {
          console.error("Failed to update frequency");
        }
      } catch (err) {
        console.error("Failed to update frequency:", err);
      } finally {
        setIsSaving(false);
      }
    },
    [sessionId, isSaving, onFrequencyChange, calculateNextRun],
  );

  // Handle value change
  const handleValueChange = useCallback((e) => {
    const newValue = parseInt(e.target.value, 10) || 1;
    const clampedValue = Math.max(1, Math.min(999, newValue));
    setLocalValue(clampedValue);
  }, []);

  // Handle value blur - save on blur
  const handleValueBlur = useCallback(() => {
    if (localValue !== frequency.value) {
      saveFrequency(localValue, localUnit, localAt);
    }
  }, [localValue, localUnit, localAt, frequency.value, saveFrequency]);

  // Handle unit change - save immediately
  const handleUnitChange = useCallback(
    (e) => {
      const newUnit = e.target.value;
      setLocalUnit(newUnit);
      // Clear 'at' if switching away from days
      const newAt = newUnit === "days" ? localAt : "";
      if (newUnit !== "days") {
        setLocalAt("");
      }
      saveFrequency(localValue, newUnit, newAt);
    },
    [localValue, localAt, saveFrequency],
  );

  // Handle time change
  const handleAtChange = useCallback((e) => {
    setLocalAt(e.target.value);
  }, []);

  // Handle time blur - save on blur
  // Compare local time with the converted UTC time from props
  const handleAtBlur = useCallback(() => {
    const propsAtLocal = utcToLocalTime(frequency.at);
    if (localUnit === "days" && localAt !== propsAtLocal) {
      saveFrequency(localValue, localUnit, localAt);
    }
  }, [localValue, localUnit, localAt, frequency.at, saveFrequency]);

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
      const response = await secureFetch(
        apiUrl(`/api/sessions/${sessionId}/periodic/run-now`),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ reset_timer: resetTimer }),
        },
      );

      if (!response.ok) {
        const errorText = await response.text();
        console.error("Failed to trigger immediate delivery:", errorText);
        // Show error to user
        if (response.status === 409) {
          setErrorMessage(
            "Session is currently processing a prompt. Please wait and try again.",
          );
        } else {
          setErrorMessage(
            "Failed to trigger immediate delivery. Please try again.",
          );
        }
        return; // Don't close dialog on error
      }
      // Success - the WebSocket will notify us of the periodic_started event
      setShowConfirmDialog(false);
    } catch (err) {
      console.error("Failed to trigger immediate delivery:", err);
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

  // Handle fresh context toggle
  const handleFreshContextChange = useCallback(
    async (e) => {
      const newValue = e.target.checked;
      if (!sessionId) return;
      try {
        const response = await secureFetch(
          apiUrl(`/api/sessions/${sessionId}/periodic`),
          {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ fresh_context: newValue }),
          },
        );
        if (response.ok) {
          const data = await response.json();
          if (onFreshContextChange) {
            onFreshContextChange(data.fresh_context ?? newValue);
          }
        } else {
          console.error("Failed to update fresh_context");
        }
      } catch (err) {
        console.error("Failed to update fresh_context:", err);
      }
    },
    [sessionId, onFreshContextChange],
  );

  // Handle max iterations input change
  const handleMaxIterationsChange = useCallback((e) => {
    setLocalMaxIterations(Math.max(0, parseInt(e.target.value, 10) || 0));
  }, []);

  // Save max iterations on blur
  const handleMaxIterationsBlur = useCallback(async () => {
    if (!sessionId || localMaxIterations === maxIterations) return;
    try {
      const response = await secureFetch(
        apiUrl(`/api/sessions/${sessionId}/periodic`),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ max_iterations: localMaxIterations }),
        },
      );
      if (response.ok) {
        if (onMaxIterationsChange) onMaxIterationsChange(localMaxIterations);
      } else {
        console.error("Failed to update max_iterations");
        setLocalMaxIterations(maxIterations);
      }
    } catch (err) {
      console.error("Failed to update max_iterations:", err);
      setLocalMaxIterations(maxIterations);
    }
  }, [sessionId, localMaxIterations, maxIterations, onMaxIterationsChange]);

  // Save trigger type to backend
  const saveTrigger = useCallback(
    async (newTrigger) => {
      if (!sessionId) return;
      try {
        const response = await secureFetch(
          apiUrl(`/api/sessions/${sessionId}/periodic`),
          {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ trigger: newTrigger }),
          },
        );
        if (response.ok) {
          const data = await response.json();
          const t = data.trigger || "schedule";
          setLocalTrigger(t);
          setLocalDelay(data.delay_seconds ?? localDelay);
          setLocalNextScheduledAt(data.next_scheduled_at);
          onTriggerChange?.(t);
          // Keep parent frequency in sync (nextScheduledAt may have changed)
          onFrequencyChange?.(data.frequency, data.next_scheduled_at);
        } else {
          console.error("Failed to update trigger");
        }
      } catch (err) {
        console.error("Failed to update trigger:", err);
      }
    },
    [sessionId, localDelay, onTriggerChange, onFrequencyChange],
  );

  // Save on-completion delay to backend (clamps to minDelaySeconds first)
  const saveDelay = useCallback(async () => {
    if (!sessionId) return;
    const clamped = Math.max(minDelaySeconds, localDelay);
    if (clamped !== localDelay) setLocalDelay(clamped);
    if (clamped === delaySeconds) return; // No change vs server
    try {
      const response = await secureFetch(
        apiUrl(`/api/sessions/${sessionId}/periodic`),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ delay_seconds: clamped }),
        },
      );
      if (response.ok) {
        const data = await response.json();
        const serverDelay = data.delay_seconds ?? clamped;
        setLocalDelay(serverDelay);
        onDelayChange?.(serverDelay);
      } else {
        console.error("Failed to update delay_seconds");
        setLocalDelay(delaySeconds); // Revert on error
      }
    } catch (err) {
      console.error("Failed to update delay_seconds:", err);
      setLocalDelay(delaySeconds);
    }
  }, [sessionId, localDelay, delaySeconds, minDelaySeconds, onDelayChange]);

  // Save max-duration to backend (0 = unlimited).
  // Accepts optional value/unit overrides so the unit select can call immediately
  // on onChange before the state update propagates.
  const saveMaxDuration = useCallback(
    async (valueOverride, unitOverride) => {
      if (!sessionId) return;
      const v = valueOverride !== undefined ? valueOverride : localMaxDurValue;
      const u = unitOverride !== undefined ? unitOverride : localMaxDurUnit;
      const secs = valueUnitToSeconds(v, u);
      if (secs === maxDurationSeconds) return; // No change vs server
      try {
        const response = await secureFetch(
          apiUrl(`/api/sessions/${sessionId}/periodic`),
          {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ max_duration_seconds: secs }),
          },
        );
        if (response.ok) {
          const data = await response.json();
          onMaxDurationChange?.(data.max_duration_seconds ?? secs);
        } else {
          console.error("Failed to update max_duration_seconds");
        }
      } catch (err) {
        console.error("Failed to update max_duration_seconds:", err);
      }
    },
    [
      sessionId,
      localMaxDurValue,
      localMaxDurUnit,
      maxDurationSeconds,
      onMaxDurationChange,
    ],
  );

  // Handle max-duration unit change — update state and persist immediately
  const handleMaxDurUnitChange = useCallback(
    (e) => {
      const newUnit = e.target.value;
      setLocalMaxDurUnit(newUnit);
      saveMaxDuration(localMaxDurValue, newUnit);
    },
    [localMaxDurValue, saveMaxDuration],
  );

  // Handle pause/resume toggle
  const handlePauseResume = useCallback(async () => {
    if (!sessionId || isSavingEnabled) return;
    const newEnabled = !disabled;
    setIsSavingEnabled(true);
    try {
      const response = await secureFetch(
        apiUrl(`/api/sessions/${sessionId}/periodic`),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ enabled: newEnabled }),
        },
      );
      if (response.ok) {
        if (onPeriodicEnabledChange) onPeriodicEnabledChange(newEnabled);
      } else {
        console.error("Failed to update periodic enabled");
      }
    } catch (err) {
      console.error("Failed to update periodic enabled:", err);
    } finally {
      setIsSavingEnabled(false);
    }
  }, [sessionId, disabled, isSavingEnabled, onPeriodicEnabledChange]);

  // Panel classes - part of normal document flow (not absolute positioned).
  // overflow-visible allows the prompt-selector dropdown to escape the card boundary upward.
  const panelClasses = `periodic-frequency-panel w-full bg-mitto-surface-hover dark:bg-mitto-surface-3/95 backdrop-blur-sm border border-mitto-border dark:border-mitto-border-2 rounded-lg overflow-visible transition-all duration-300 ease-out ${
    isOpen
      ? "opacity-100 mb-3"
      : "opacity-0 pointer-events-none h-0 border-0 mb-0"
  }`;

  const panelStyle = isOpen ? "" : "height: 0px;";

  // Format next scheduled time for display (uses local state for immediate feedback)
  const nextTimeDisplay = localNextScheduledAt
    ? new Date(localNextScheduledAt).toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : null;

  // Compact frequency label for the header glance row
  const freqLabel = `every ${localValue}${localUnit === "minutes" ? "min" : localUnit === "hours" ? "h" : "d"}`;

  // Live adaptive countdown to the next run; absolute time surfaced as a tooltip
  const countdownDisplay = localNextScheduledAt
    ? html`<${CountdownDisplay}
        targetIso=${localNextScheduledAt}
        unit=${localUnit}
        active=${isOpen}
        title=${nextTimeDisplay ? `Next: ${nextTimeDisplay}` : ""}
      />`
    : null;

  // Run count for the header glance row
  const runCountLabel =
    maxIterations > 0
      ? html`Run ${iterationCount} of ${maxIterations}`
      : html`${iterationCount} run${iterationCount !== 1 ? "s" : ""} ·${" "}
          <span class="text-lg leading-none align-middle">∞</span>`;

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

      <div
        class="${panelClasses}"
        style="${panelStyle}"
        data-testid="periodic-frequency-panel"
      >
        <!-- HEADER: always visible when isOpen (single ~44px row) -->
        <div class="h-11 px-3 flex items-center gap-2 text-sm">
          <!-- Run-now button -->
          <button
            type="button"
            onClick=${handleIconClick}
            disabled=${isTriggering || isStreaming}
            class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 transition-colors ${isTriggering || isStreaming ? "opacity-50 cursor-not-allowed" : "cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3"}"
            title=${isStreaming ? "Wait for agent to finish responding" : "Run this periodic prompt now"}
            data-testid="periodic-run-now-button"
          >
            ${
              isTriggering
                ? html`<span
                    class="loading loading-spinner w-4 h-4 text-mitto-text-secondary"
                  ></span>`
                : html`<${PlayFilledIcon}
                    className="w-4 h-4 text-mitto-text-secondary"
                  />`
            }
          </button>

          <!-- Pause/Resume button (icon-only, sits next to Run-now) -->
          <button
            type="button"
            onClick=${handlePauseResume}
            disabled=${isSavingEnabled}
            class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 transition-colors ${isSavingEnabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3"}"
            title=${disabled ? "Pause periodic runs" : "Resume periodic runs"}
            data-testid="periodic-pause-resume-button"
          >
            ${
              isSavingEnabled
                ? html`<span
                    class="loading loading-spinner w-4 h-4 text-mitto-text-secondary"
                  ></span>`
                : disabled
                  ? html`<${PauseFilledIcon}
                      className="w-4 h-4 text-mitto-text-secondary"
                    />`
                  : html`<${PlayFilledIcon}
                      className="w-4 h-4 text-mitto-text-secondary"
                    />`
            }
          </button>

          <!-- Inline prompt selector (trigger + dropdown; dropdown opens above) -->
          <${PeriodicPromptSelector}
            prompts=${prompts}
            selectedPromptName=${selectedPromptName}
            disabled=${false}
            onSelect=${onPromptSelect}
            isPromptAreaVisible=${isPromptAreaVisible}
            onTogglePromptArea=${onTogglePromptArea}
          />

          <!-- Flex spacer -->
          <div class="flex-1 min-w-0"></div>

          <!-- Glanceable status: trigger-aware label + live countdown to next run -->
          <span class="text-xs text-mitto-text-muted dark:text-mitto-text-300 shrink-0 flex items-baseline gap-1">
            ${
              isOnCompletion
                ? html`<span
                    >after agent
                    finishes${localDelay > 0 ? ` · +${localDelay}s` : ""}</span
                  >`
                : html`<${Fragment}><span>${freqLabel}</span>${countdownDisplay && html`<span aria-hidden="true">·</span>${countdownDisplay}`}</${Fragment}>`
            }
          </span>

          <!-- Run count -->
          <span class="text-xs text-mitto-text-500 shrink-0 hidden md:block">${runCountLabel}</span>

          <!-- Saving indicator -->
          ${
            isSaving &&
            html`<span
              class="loading loading-spinner w-4 h-4 text-mitto-accent shrink-0"
            ></span>`
          }

          <!-- Expand/collapse chevron button -->
          <button
            type="button"
            onClick=${onToggleExpanded}
            class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3 transition-colors"
            title=${expanded ? "Collapse settings" : "Expand settings"}
            data-testid="periodic-expand-toggle"
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

        <!-- BODY: collapsed by default; expands when user clicks the chevron -->
        <div
          class="transition-all duration-300 ease-out border-t border-mitto-border dark:border-mitto-border-2 ${
            expanded
              ? "max-h-[32rem] opacity-100"
              : "max-h-0 opacity-0 overflow-hidden pointer-events-none"
          }"
        >
          <!-- Trigger tabs: Schedule | On completion -->
          <div class="tabs tabs-border px-4 pt-2">
            <input
              type="radio"
              name="periodic-trigger-${sessionId}"
              role="tab"
              aria-label="Schedule"
              class="tab text-sm"
              checked=${localTrigger === "schedule"}
              onChange=${() => saveTrigger("schedule")}
              data-testid="periodic-trigger-tab-schedule"
            />
            <input
              type="radio"
              name="periodic-trigger-${sessionId}"
              role="tab"
              aria-label="On completion"
              class="tab text-sm"
              checked=${localTrigger === "onCompletion"}
              onChange=${() => saveTrigger("onCompletion")}
              data-testid="periodic-trigger-tab-oncompletion"
            />
          </div>

          <!-- State-driven schedule row: "Run every" (schedule) or "Wait" (onCompletion) -->
          ${
            isOnCompletion
              ? html` <!-- On-completion: delay after agent finishes -->
                  <div class="px-4 pt-2 pb-2 flex items-center gap-3 text-sm">
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
                      onBlur=${saveDelay}
                      class="input input-sm w-20 shrink-0 text-center"
                      data-testid="periodic-delay-input"
                    />
                    <span
                      class="text-xs text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
                    >
                      seconds after the agent finishes (min ${minDelaySeconds}s)
                    </span>
                  </div>`
              : html` <!-- Schedule: Run every N units -->
                  <div class="px-4 pt-2 pb-2 flex items-center gap-3 text-sm">
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
                      onBlur=${handleValueBlur}
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
                        onBlur=${handleAtBlur}
                        disabled=${isSaving}
                        class="h-8 px-2 min-w-16 shrink-0 bg-white dark:bg-mitto-surface-2 border border-mitto-border dark:border-mitto-border-2 rounded text-mitto-text-strong text-sm focus:outline-none focus:ring-1 focus:ring-mitto-accent-500 ${isSaving
                          ? "opacity-50 cursor-not-allowed"
                          : ""}"
                        placeholder="HH:MM"
                      />
                    `}
                  </div>`
          }

          <!-- Fresh-context row (applies to both schedule and onCompletion) -->
          <div class="px-4 pb-2 flex items-center gap-2 text-sm border-t border-mitto-border dark:border-mitto-border-2 pt-2">
            <input
              type="checkbox"
              id="fresh-context-checkbox-${sessionId}"
              checked=${freshContext}
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
              onBlur=${handleMaxIterationsBlur}
              class="input input-sm w-20 text-center shrink-0"
              data-testid="periodic-panel-max-iterations"
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
              onBlur=${() => saveMaxDuration()}
              class="input input-sm w-20 text-center shrink-0"
              data-testid="periodic-max-duration-value"
            />
            <select
              value=${localMaxDurUnit}
              onChange=${handleMaxDurUnitChange}
              class="select select-sm shrink-0 w-24"
              data-testid="periodic-max-duration-unit"
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
