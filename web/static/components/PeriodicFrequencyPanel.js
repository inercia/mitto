// Mitto Web Interface - Periodic Frequency Panel Component
// Displays and edits the frequency settings for periodic conversations

const { useState, useEffect, useCallback, useMemo, html } = window.preact;

import { PeriodicFilledIcon } from "./Icons.js";
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
    Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), hours, minutes, 0),
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
  const localDate = new Date(now.getFullYear(), now.getMonth(), now.getDate(), hours, minutes, 0);
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
 */
export function PeriodicFrequencyPanel({
  isOpen,
  disabled = false,
  sessionId,
  frequency = { value: 1, unit: "hours" },
  onFrequencyChange,
  nextScheduledAt,
}) {
  // Local state for editing
  const [localValue, setLocalValue] = useState(frequency.value || 1);
  const [localUnit, setLocalUnit] = useState(frequency.unit || "hours");
  // localAt is stored in LOCAL time for display/editing (converted from UTC when syncing from props)
  const [localAt, setLocalAt] = useState(utcToLocalTime(frequency.at) || "");
  const [isSaving, setIsSaving] = useState(false);
  // Local estimated next run time (updated immediately on frequency change)
  const [localNextScheduledAt, setLocalNextScheduledAt] = useState(nextScheduledAt);

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

  // Save frequency to backend
  // Note: newAt is in LOCAL time, needs to be converted to UTC before sending
  const saveFrequency = useCallback(
    async (newValue, newUnit, newAtLocal) => {
      if (!sessionId || isSaving || disabled) return;

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
    [sessionId, isSaving, disabled, onFrequencyChange, calculateNextRun],
  );

  // Handle value change
  const handleValueChange = useCallback(
    (e) => {
      const newValue = parseInt(e.target.value, 10) || 1;
      const clampedValue = Math.max(1, Math.min(999, newValue));
      setLocalValue(clampedValue);
    },
    [],
  );

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

  // Panel classes - part of normal document flow (not absolute positioned)
  // This ensures it pushes the conversation area up instead of overlaying it
  // Uses lighter background for better readability and contrast
  const panelClasses = `periodic-frequency-panel w-full bg-slate-100 dark:bg-slate-700/95 backdrop-blur-sm border border-slate-300 dark:border-slate-600 rounded-lg overflow-hidden transition-all duration-300 ease-out ${
    isOpen ? "opacity-100 mb-3" : "opacity-0 pointer-events-none h-0 border-0 mb-0"
  }`;

  // Fixed single-row height when open
  const panelStyle = isOpen
    ? "height: 44px;"
    : "height: 0px;";

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
    <div
      class="${panelClasses}"
      style="${panelStyle}"
      data-testid="periodic-frequency-panel"
    >
      <div
        class="h-full px-4 flex items-center gap-3 text-sm"
      >
        <!-- Clock icon -->
        <${PeriodicFilledIcon} className="w-4 h-4 text-blue-600 dark:text-blue-400 flex-shrink-0" />

        <!-- Run every label -->
        <span class="text-slate-600 dark:text-gray-300 flex-shrink-0">Run every</span>

        <!-- Numeric input -->
        <input
          type="number"
          min="1"
          max="999"
          value=${localValue}
          onInput=${handleValueChange}
          onBlur=${handleValueBlur}
          disabled=${isSaving || disabled}
          class="w-16 h-8 px-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-600 rounded text-slate-900 dark:text-white text-center text-sm focus:outline-none focus:ring-1 focus:ring-blue-500 ${isSaving || disabled ? "opacity-50 cursor-not-allowed" : ""}"
        />

        <!-- Unit dropdown -->
        <select
          value=${localUnit}
          onChange=${handleUnitChange}
          disabled=${isSaving || disabled}
          class="h-8 px-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-600 rounded text-slate-900 dark:text-white text-sm focus:outline-none focus:ring-1 focus:ring-blue-500 ${isSaving || disabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer"}"
        >
          <option value="minutes">minutes</option>
          <option value="hours">hours</option>
          <option value="days">days</option>
        </select>

        <!-- Time picker (only shown for daily schedules) -->
        ${localUnit === "days" &&
        html`
          <span class="text-slate-600 dark:text-gray-300 flex-shrink-0">at</span>
          <input
            type="time"
            value=${localAt}
            onInput=${handleAtChange}
            onBlur=${handleAtBlur}
            disabled=${isSaving || disabled}
            class="h-8 px-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-600 rounded text-slate-900 dark:text-white text-sm focus:outline-none focus:ring-1 focus:ring-blue-500 ${isSaving || disabled ? "opacity-50 cursor-not-allowed" : ""}"
            placeholder="HH:MM"
          />
        `}

        <!-- Spacer -->
        <div class="flex-1"></div>

        <!-- Next run time -->
        ${nextTimeDisplay &&
        html`
          <span class="text-slate-600 dark:text-gray-300 text-xs flex-shrink-0">
            Next: ${nextTimeDisplay}
          </span>
        `}

        <!-- Saving indicator -->
        ${isSaving &&
        html`
          <svg
            class="w-4 h-4 animate-spin text-blue-400"
            fill="none"
            viewBox="0 0 24 24"
          >
            <circle
              class="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              stroke-width="4"
            ></circle>
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            ></path>
          </svg>
        `}
      </div>
    </div>
  `;
}

