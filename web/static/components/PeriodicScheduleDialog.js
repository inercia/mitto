// Mitto Web Interface - Periodic Schedule Dialog Component
// A modal dialog for collecting a periodic schedule (value, unit, optional at time)
// pre-filled from a prompt's `periodic` frontmatter defaults.

const { useState, useEffect, useCallback, html } = window.preact;
import { Modal } from "./Modal.js";

/**
 * Convert UTC time (HH:MM) to local time (HH:MM).
 * @param {string} utcTime
 * @returns {string}
 */
function utcToLocalTime(utcTime) {
  if (!utcTime) return "";
  const [hours, minutes] = utcTime.split(":").map(Number);
  const now = new Date();
  const utcDate = new Date(
    Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), hours, minutes, 0),
  );
  return utcDate.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

/**
 * Convert local time (HH:MM) to UTC time (HH:MM).
 * @param {string} localTime
 * @returns {string}
 */
function localToUtcTime(localTime) {
  if (!localTime) return "";
  const [hours, minutes] = localTime.split(":").map(Number);
  const now = new Date();
  const localDate = new Date(now.getFullYear(), now.getMonth(), now.getDate(), hours, minutes, 0);
  const utcHours = localDate.getUTCHours().toString().padStart(2, "0");
  const utcMinutes = localDate.getUTCMinutes().toString().padStart(2, "0");
  return `${utcHours}:${utcMinutes}`;
}


/**
 * PeriodicScheduleDialog — modal to collect a periodic schedule for a prompt.
 *
 * Pre-fills from `prompt.periodic` defaults (if present).
 * Calls `onConfirm({ value, unit, at? })` with `at` in UTC HH:MM (days only).
 * Calls `onCancel()` when dismissed.
 *
 * @param {Object} props
 * @param {boolean} props.isOpen
 * @param {Object|null} props.prompt - Prompt object with optional .periodic defaults
 * @param {Function} props.onConfirm - Called with { value, unit, at? } on confirm
 * @param {Function} props.onCancel - Called on cancel / close
 */
export function PeriodicScheduleDialog({ isOpen, prompt, onConfirm, onCancel }) {
  const defaults = prompt?.periodic || {};
  const [value, setValue] = useState(defaults.value || 1);
  const [unit, setUnit] = useState(defaults.unit || "hours");
  // `at` stored in local time for display; defaults.at is in UTC — convert on init.
  const [at, setAt] = useState(() => utcToLocalTime(defaults.at) || "");
  // maxIterations: 0 = unlimited, positive = capped. Pre-filled from prompt defaults.
  const [maxIterations, setMaxIterations] = useState(defaults.maxIterations ?? 0);

  // Reset to prompt defaults whenever the prompt changes (dialog re-opened).
  useEffect(() => {
    const d = prompt?.periodic || {};
    setValue(d.value || 1);
    setUnit(d.unit || "hours");
    setAt(utcToLocalTime(d.at) || "");
    setMaxIterations(d.maxIterations ?? 0);
  }, [prompt]);

  const handleUnitChange = useCallback((e) => {
    const newUnit = e.target.value;
    setUnit(newUnit);
    if (newUnit !== "days") setAt("");
  }, []);

  const handleConfirm = useCallback(() => {
    const schedule = { value: Math.max(1, Math.min(999, value || 1)), unit };
    if (unit === "days" && at) {
      schedule.at = localToUtcTime(at);
    }
    // Include maxIterations: 0 = unlimited, positive = capped run count.
    schedule.maxIterations = Math.max(0, maxIterations || 0);
    onConfirm?.(schedule);
  }, [value, unit, at, maxIterations, onConfirm]);

  const handleCancel = useCallback(() => {
    onCancel?.();
  }, [onCancel]);

  const footer = html`
    <button
      onClick=${handleCancel}
      class="btn btn-ghost btn-sm"
      data-testid="periodic-schedule-cancel"
    >
      Cancel
    </button>
    <button
      onClick=${handleConfirm}
      class="btn btn-primary btn-sm"
      data-testid="periodic-schedule-confirm"
    >
      Start periodic conversation
    </button>
  `;

  return html`
    <${Modal}
      isOpen=${isOpen}
      onClose=${handleCancel}
      title="Set up recurring schedule"
      footer=${footer}
      testid="periodic-schedule-dialog"
    >
      <div class="flex flex-col gap-4 text-sm">
        ${prompt?.description && html`
          <p class="text-mitto-text-muted dark:text-mitto-text-300">${prompt.description}</p>
        `}
        <div class="flex flex-wrap items-center gap-3">
          <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0">Run every</span>
          <input
            type="number"
            min="1"
            max="999"
            value=${value}
            onInput=${(e) => setValue(parseInt(e.target.value, 10) || 1)}
            class="input input-sm w-20 text-center shrink-0"
            data-testid="periodic-schedule-value"
          />
          <select
            value=${unit}
            onChange=${handleUnitChange}
            class="select select-sm w-28 shrink-0"
            data-testid="periodic-schedule-unit"
          >
            <option value="minutes">minutes</option>
            <option value="hours">hours</option>
            <option value="days">days</option>
          </select>
          ${unit === "days" && html`
            <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0">at</span>
            <input
              type="time"
              value=${at}
              onInput=${(e) => setAt(e.target.value)}
              class="h-8 px-2 min-w-16 shrink-0 bg-white dark:bg-mitto-surface-2 border border-mitto-border dark:border-mitto-border-2 rounded text-sm focus:outline-none focus:ring-1 focus:ring-mitto-accent-500"
              placeholder="HH:MM"
              data-testid="periodic-schedule-at"
            />
          `}
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0">Max runs</span>
          <input
            type="number"
            min="0"
            max="9999"
            value=${maxIterations}
            onInput=${(e) => setMaxIterations(Math.max(0, parseInt(e.target.value, 10) || 0))}
            class="input input-sm w-20 text-center shrink-0"
            data-testid="periodic-schedule-max-iterations"
          />
          <span class="text-xs text-mitto-text-muted dark:text-mitto-text-300 shrink-0">(0 = unlimited)</span>
        </div>
        <p class="text-xs text-mitto-text-muted dark:text-mitto-text-300">
          A new recurring conversation will be created using the
          <strong>${prompt?.name || "selected"}</strong> prompt.
        </p>
      </div>
    </${Modal}>
  `;
}
