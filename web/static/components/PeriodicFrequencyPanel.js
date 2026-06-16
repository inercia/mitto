// Mitto Web Interface - Periodic Frequency Panel Component
// Displays and edits the frequency settings for periodic conversations

const { useState, useEffect, useCallback, useMemo, html } = window.preact;

import { PeriodicFilledIcon, PlayFilledIcon } from "./Icons.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { secureFetch, authFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";

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
 * PeriodicFrequencyPanel component - displays and edits periodic frequency settings
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the panel is visible (shown when periodic is enabled)
 * @param {boolean} props.disabled - Whether the panel is read-only (true when periodic is locked)
 * @param {string} props.sessionId - Current session ID
 * @param {Object} props.frequency - Current frequency config { value, unit, at } (at is in UTC)
 * @param {Function} props.onFrequencyChange - Callback when frequency is updated
 * @param {string} props.nextScheduledAt - ISO timestamp of next scheduled run
 * @param {boolean} props.isStreaming - Whether the agent is currently responding (disables immediate delivery)
 * @param {boolean} props.freshContext - Whether each run starts with a fresh agent context
 * @param {Function} props.onFreshContextChange - Callback when freshContext is toggled
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

  // Handle click on the periodic icon when locked - show confirmation dialog
  const handleIconClick = useCallback(() => {
    // Only allow clicking when locked (disabled=true), not already triggering,
    // and the agent is not currently responding
    if (!disabled || isTriggering || !sessionId || isStreaming) return;
    // Show the confirmation dialog
    setShowConfirmDialog(true);
  }, [disabled, isTriggering, sessionId, isStreaming]);

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

  // Panel classes - part of normal document flow (not absolute positioned)
  // This ensures it pushes the conversation area up instead of overlaying it
  // Uses lighter background for better readability and contrast
  const panelClasses = `periodic-frequency-panel w-full bg-mitto-surface-hover dark:bg-mitto-surface-3/95 backdrop-blur-sm border border-mitto-border dark:border-mitto-border-2 rounded-lg overflow-hidden transition-all duration-300 ease-out ${
    isOpen
      ? "opacity-100 mb-3"
      : "opacity-0 pointer-events-none h-0 border-0 mb-0"
  }`;

  // Open height: when locked (single row) match the prompt selector's fixed 44px
  // so both side-by-side boxes have identical height. When editing (fresh-context
  // row is shown) the panel grows to fit both rows.
  const panelStyle = isOpen ? (disabled ? "height: 44px;" : "") : "height: 0px;";

  // Format next scheduled time for display (uses local state for immediate feedback)
  const nextTimeDisplay = localNextScheduledAt
    ? new Date(localNextScheduledAt).toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : null;

  return html`
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
      <div
        class="${disabled ? "h-full" : "h-11"} px-4 flex items-center gap-3 text-sm"
      >
        <!-- Periodic icon - clickable when locked to trigger immediate delivery -->
        <!-- Disabled when agent is streaming (isStreaming) or already triggering -->
        ${disabled
          ? html`
              <button
                type="button"
                onClick=${handleIconClick}
                disabled=${isTriggering || isStreaming}
                class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 transition-colors ${isTriggering ||
                isStreaming
                  ? "opacity-50 cursor-not-allowed"
                  : "cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3"}"
                title=${isStreaming
                  ? "Wait for agent to finish responding"
                  : "Click to run this periodic prompt now"}
                data-testid="periodic-run-now-button"
              >
                ${isTriggering
                  ? html`<span class="loading loading-spinner w-4 h-4 text-mitto-accent dark:text-mitto-accent-400"></span>`
                  : html`<${PlayFilledIcon}
                      className="w-4 h-4 text-mitto-accent dark:text-mitto-accent-400"
                    />`}
              </button>
            `
          : html`<${PeriodicFilledIcon}
              className="w-4 h-4 text-mitto-accent dark:text-mitto-accent-400 shrink-0"
            />`}

        <!-- Run every label -->
        <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
          >Run every</span
        >

        <!-- Numeric input -->
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

        <!-- Unit dropdown -->
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
          <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
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

        <!-- Spacer -->
        <div class="flex-1"></div>

        <!-- Next run time -->
        ${nextTimeDisplay &&
        html`
          <span class="text-mitto-text-muted dark:text-mitto-text-300 text-xs shrink-0">
            Next: ${nextTimeDisplay}
          </span>
        `}

        <!-- Saving indicator -->
        ${isSaving &&
        html`<span class="loading loading-spinner w-4 h-4 text-mitto-accent"></span>`}
      </div>

      <!-- Fresh context option (only shown when not locked / editing is allowed) -->
      ${!disabled &&
      html`
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
            class="text-mitto-text-muted dark:text-mitto-text-300 cursor-pointer select-none"
          >
            Start each run with a fresh context
          </label>
        </div>
      `}
    </div>
  `;
}
