// Mitto Web Interface - Loop Schedule Dialog Component
// A modal dialog for collecting a loop schedule (value, unit, optional at time)
// pre-filled from a prompt's `loop` frontmatter defaults.

const { useState, useEffect, useCallback, html } = window.preact;
import { Modal } from "./Modal.js";
import {
  parseDurationToSeconds,
  readPromptLoopDefaults,
} from "../hooks/useConversationSeeding.js";

/**
 * Convert total seconds into the largest whole value+unit pair.
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
 * Convert value + unit into total seconds.
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
 * Convert UTC time (HH:MM) to local time (HH:MM).
 * @param {string} utcTime
 * @returns {string}
 */
function utcToLocalTime(utcTime) {
  if (!utcTime) return "";
  const [hours, minutes] = utcTime.split(":").map(Number);
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
  const localDate = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
    hours,
    minutes,
    0,
  );
  const utcHours = localDate.getUTCHours().toString().padStart(2, "0");
  const utcMinutes = localDate.getUTCMinutes().toString().padStart(2, "0");
  return `${utcHours}:${utcMinutes}`;
}

/**
 * LoopScheduleDialog — modal to collect a loop schedule for a prompt.
 *
 * Pre-fills from the prompt's `loop:` frontmatter defaults (mitto-r6j nested
 * per-trigger schema — `schedule`, `onCompletion`, `onTasks` blocks).
 *
 * The user picks any non-empty subset of triggers via a checkbox row; each
 * selected trigger's attribute sub-panel is shown, unselected sub-panels are
 * hidden. `onConfirm` receives:
 *   {
 *     triggers: string[],            // armed triggers, at least one entry
 *     value, unit, at?,              // schedule frequency (at UTC HH:MM)
 *     maxIterations,                 // 0 = unlimited
 *     delaySeconds,                  // onCompletion delay in seconds
 *     maxDurationSeconds,            // 0 = unlimited
 *     condition?,                    // onTasks CEL (only when onTasks armed)
 *   }
 *
 * @param {Object} props
 * @param {boolean} props.isOpen
 * @param {Object|null} props.prompt - Prompt object with optional .loop defaults
 * @param {Function} props.onConfirm - Called with the confirm payload above
 * @param {Function} props.onCancel - Called on cancel / close
 */
export function LoopScheduleDialog({ isOpen, prompt, onConfirm, onCancel }) {
  const defaults = readPromptLoopDefaults(prompt?.loop);
  const [value, setValue] = useState(defaults.value);
  const [unit, setUnit] = useState(defaults.unit);
  // `at` stored in local time for display; defaults.at is in UTC — convert on init.
  const [at, setAt] = useState(() => utcToLocalTime(defaults.at) || "");
  // maxIterations: 0 = unlimited, positive = capped. Pre-filled from prompt defaults.
  const [maxIterations, setMaxIterations] = useState(defaults.maxIterations);
  // Armed triggers (mitto-r6j): a Set of {"schedule", "onCompletion",
  // "onTasks"}. The dialog enforces non-empty (Confirm is disabled when 0).
  const [triggers, setTriggers] = useState(() => new Set(defaults.triggers));
  // On-completion delay in seconds (min 5)
  const [delay, setDelay] = useState(defaults.delay || 5);
  // On-tasks CEL condition (optional). Empty string = fire on any task change.
  const [condition, setCondition] = useState(defaults.condition);
  // Max duration: stored as value+unit for display, converted on confirm
  const [maxDurValue, setMaxDurValue] = useState(
    () =>
      secondsToValueUnit(parseDurationToSeconds(prompt?.loop?.maxDuration))
        .value,
  );
  const [maxDurUnit, setMaxDurUnit] = useState(
    () =>
      secondsToValueUnit(parseDurationToSeconds(prompt?.loop?.maxDuration))
        .unit,
  );

  // Reset to prompt defaults whenever the prompt changes (dialog re-opened).
  useEffect(() => {
    const d = readPromptLoopDefaults(prompt?.loop);
    setValue(d.value);
    setUnit(d.unit);
    setAt(utcToLocalTime(d.at) || "");
    setMaxIterations(d.maxIterations);
    setTriggers(new Set(d.triggers));
    setDelay(d.delay || 5);
    setCondition(d.condition);
    const mdSecs = parseDurationToSeconds(prompt?.loop?.maxDuration);
    const { value: mdv, unit: mdu } = secondsToValueUnit(mdSecs);
    setMaxDurValue(mdv);
    setMaxDurUnit(mdu);
  }, [prompt]);

  const handleUnitChange = useCallback((e) => {
    const newUnit = e.target.value;
    setUnit(newUnit);
    if (newUnit !== "days") setAt("");
  }, []);

  // Toggle a trigger in the armed-set. If unchecking would leave the set
  // empty, keep the trigger armed (dialog invariant: at least one).
  const toggleTrigger = useCallback((name) => {
    setTriggers((prev) => {
      const next = new Set(prev);
      if (next.has(name)) {
        if (next.size <= 1) return prev; // reject going to empty
        next.delete(name);
      } else {
        next.add(name);
      }
      return next;
    });
  }, []);

  const armsSchedule = triggers.has("schedule");
  const armsOnCompletion = triggers.has("onCompletion");
  const armsOnTasks = triggers.has("onTasks");

  const handleConfirm = useCallback(() => {
    // Preserve the canonical order (schedule, onCompletion, onTasks) so the
    // wire payload is stable regardless of the click order the user chose.
    const order = ["schedule", "onCompletion", "onTasks"];
    const triggersList = order.filter((t) => triggers.has(t));
    if (triggersList.length === 0) return; // Confirm button disabled anyway
    const result = {
      triggers: triggersList,
      value: Math.max(1, Math.min(999, value || 1)),
      unit,
      maxIterations: Math.max(0, maxIterations || 0),
      delaySeconds: Math.max(0, delay || 0),
      maxDurationSeconds: valueUnitToSeconds(maxDurValue, maxDurUnit),
    };
    if (armsSchedule && unit === "days" && at) {
      result.at = localToUtcTime(at);
    }
    if (armsOnTasks) {
      result.condition = condition;
    }
    onConfirm?.(result);
  }, [
    triggers,
    value,
    unit,
    at,
    maxIterations,
    delay,
    condition,
    maxDurValue,
    maxDurUnit,
    armsSchedule,
    armsOnTasks,
    onConfirm,
  ]);

  const handleCancel = useCallback(() => {
    onCancel?.();
  }, [onCancel]);

  const canConfirm = triggers.size > 0;

  const footer = html`
    <button
      onClick=${handleCancel}
      class="btn btn-ghost btn-sm"
      data-testid="loop-schedule-cancel"
    >
      Cancel
    </button>
    <button
      onClick=${handleConfirm}
      disabled=${!canConfirm}
      class="btn btn-primary btn-sm"
      data-testid="loop-schedule-confirm"
    >
      Start loop conversation
    </button>
  `;

  return html`
    <${Modal}
      isOpen=${isOpen}
      onClose=${handleCancel}
      title="Set up recurring schedule"
      footer=${footer}
      testid="loop-schedule-dialog"
    >
      <div class="flex flex-col gap-4 text-sm">
        ${
          prompt?.description &&
          html`
            <p class="text-mitto-text-muted dark:text-mitto-text-300">
              ${prompt.description}
            </p>
          `
        }

        <!-- Trigger checkboxes (mitto-r6j): armable set of {schedule, onCompletion, onTasks}. -->
        <fieldset
          class="flex flex-col gap-2"
          data-testid="loop-schedule-triggers"
        >
          <legend class="text-mitto-text-muted dark:text-mitto-text-300 mb-1">
            Fire on
          </legend>
          <div class="flex flex-wrap gap-4">
            <label class="label cursor-pointer gap-2 py-0">
              <input
                type="checkbox"
                class="checkbox checkbox-sm"
                checked=${armsSchedule}
                onChange=${() => toggleTrigger("schedule")}
                data-testid="loop-schedule-trigger-check-schedule"
              />
              <span class="label-text">Schedule</span>
            </label>
            <label class="label cursor-pointer gap-2 py-0">
              <input
                type="checkbox"
                class="checkbox checkbox-sm"
                checked=${armsOnCompletion}
                onChange=${() => toggleTrigger("onCompletion")}
                data-testid="loop-schedule-trigger-check-oncompletion"
              />
              <span class="label-text">On completion</span>
            </label>
            <label class="label cursor-pointer gap-2 py-0">
              <input
                type="checkbox"
                class="checkbox checkbox-sm"
                checked=${armsOnTasks}
                onChange=${() => toggleTrigger("onTasks")}
                data-testid="loop-schedule-trigger-check-ontasks"
              />
              <span class="label-text">On tasks</span>
            </label>
          </div>
        </fieldset>

        <!-- Per-trigger sub-panels: show iff the corresponding trigger is armed. -->
        ${
          armsSchedule &&
          html`<div
            class="flex flex-wrap items-center gap-3"
            data-testid="loop-schedule-panel-schedule"
          >
            <span
              class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
              >Run every</span
            >
            <input
              type="number"
              min="1"
              max="999"
              value=${value}
              onInput=${(e) => setValue(parseInt(e.target.value, 10) || 1)}
              class="input input-sm w-20 text-center shrink-0"
              data-testid="loop-schedule-value"
            />
            <select
              value=${unit}
              onChange=${handleUnitChange}
              class="select select-sm w-28 shrink-0"
              data-testid="loop-schedule-unit"
            >
              <option value="minutes">minutes</option>
              <option value="hours">hours</option>
              <option value="days">days</option>
            </select>
            ${unit === "days" &&
            html`
              <span
                class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
                >at</span
              >
              <input
                type="time"
                value=${at}
                onInput=${(e) => setAt(e.target.value)}
                class="h-8 px-2 min-w-16 shrink-0 bg-white dark:bg-mitto-surface-2 border border-mitto-border dark:border-mitto-border-2 rounded text-sm focus:outline-none focus:ring-1 focus:ring-mitto-accent-500"
                placeholder="HH:MM"
                data-testid="loop-schedule-at"
              />
            `}
          </div>`
        }
        ${
          armsOnCompletion &&
          html`<div
            class="flex flex-wrap items-center gap-3"
            data-testid="loop-schedule-panel-oncompletion"
          >
            <span
              class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
              >Wait</span
            >
            <input
              type="number"
              min="5"
              value=${delay}
              onInput=${(e) =>
                setDelay(Math.max(5, parseInt(e.target.value, 10) || 5))}
              class="input input-sm w-20 text-center shrink-0"
              data-testid="loop-schedule-delay"
            />
            <span
              class="text-xs text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
            >
              seconds after the agent finishes (min 5s)
            </span>
          </div>`
        }
        ${
          armsOnTasks &&
          html`<div
            class="flex flex-col gap-2"
            data-testid="loop-schedule-panel-ontasks"
          >
            <p class="text-mitto-text-muted dark:text-mitto-text-300">
              The loop re-fires whenever tasks in .beads/ change matching the
              condition below.
            </p>
            <label
              class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0"
            >
              Condition (optional CEL expression)
            </label>
            <input
              type="text"
              value=${condition}
              onInput=${(e) => setCondition(e.target.value)}
              class="input input-sm w-full"
              data-testid="loop-schedule-condition"
            />
            <span
              class="text-xs text-mitto-text-muted dark:text-mitto-text-300"
            >
              Leave empty to fire on any task change.
            </span>
          </div>`
        }

        <div class="flex flex-wrap items-center gap-3">
          <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0">Max runs</span>
          <input
            type="number"
            min="0"
            max="9999"
            value=${maxIterations}
            onInput=${(e) => setMaxIterations(Math.max(0, parseInt(e.target.value, 10) || 0))}
            class="input input-sm w-20 text-center shrink-0"
            data-testid="loop-schedule-max-iterations"
          />
          <span class="text-xs text-mitto-text-muted dark:text-mitto-text-300 shrink-0">(0 = unlimited)</span>
        </div>

        <div class="flex flex-wrap items-center gap-3">
          <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0">Max time</span>
          <input
            type="number"
            min="0"
            max="9999"
            value=${maxDurValue}
            onInput=${(e) => setMaxDurValue(Math.max(0, parseInt(e.target.value, 10) || 0))}
            class="input input-sm w-20 text-center shrink-0"
            data-testid="loop-schedule-max-duration-value"
          />
          <select
            value=${maxDurUnit}
            onChange=${(e) => setMaxDurUnit(e.target.value)}
            class="select select-sm w-28 shrink-0"
            data-testid="loop-schedule-max-duration-unit"
          >
            <option value="minutes">minutes</option>
            <option value="hours">hours</option>
            <option value="days">days</option>
          </select>
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
