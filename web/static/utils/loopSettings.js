// Pure helpers shared by the staged loop-settings editor.

export const KNOWN_LOOP_TRIGGERS = [
  "schedule",
  "onCompletion",
  "onTasks",
  "onChild",
];

export const KNOWN_CHILD_EVENTS = [
  "anyEndResponse",
  "anyDeleted",
  "anyLoopStopped",
];

export const DEFAULT_CHILD_EVENTS = ["anyEndResponse", "anyDeleted"];

const SCHEDULE_UNITS = ["minutes", "hours", "days"];
const HH_MM = /^([01][0-9]|2[0-3]):[0-5][0-9]$/;
const DANGEROUS_SCHEDULE_SECONDS = 5 * 60;

function finiteNumber(value, fallback = 0) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function orderedUnique(values, knownOrder) {
  const source = Array.isArray(values) ? values : [];
  const seen = new Set();
  const cleaned = [];
  for (const value of source) {
    if (typeof value !== "string" || value.length === 0 || seen.has(value)) {
      continue;
    }
    seen.add(value);
    cleaned.push(value);
  }
  return [
    ...knownOrder.filter((value) => seen.has(value)),
    ...cleaned.filter((value) => !knownOrder.includes(value)),
  ];
}

/** Known triggers first in canonical order, then unknown triggers in source order. */
export function canonicalizeLoopTriggers(triggers) {
  return orderedUnique(triggers, KNOWN_LOOP_TRIGGERS);
}

/** Known child events first, while retaining unknown future events. */
export function canonicalizeChildEvents(events) {
  return orderedUnique(events, KNOWN_CHILD_EVENTS);
}

export function valueUnitToSeconds(value, unit) {
  const numeric = finiteNumber(value);
  switch (unit) {
    case "minutes":
      return numeric * 60;
    case "hours":
      return numeric * 3600;
    case "days":
      return numeric * 86400;
    default:
      return numeric;
  }
}

export function secondsToValueUnit(seconds, zeroUnit = "hours") {
  const numeric = finiteNumber(seconds);
  if (numeric === 0) return { value: 0, unit: zeroUnit };
  if (numeric % 86400 === 0) return { value: numeric / 86400, unit: "days" };
  if (numeric % 3600 === 0) return { value: numeric / 3600, unit: "hours" };
  if (numeric % 60 === 0) return { value: numeric / 60, unit: "minutes" };
  return { value: numeric, unit: "seconds" };
}

function timeParts(value) {
  if (!HH_MM.test(value || "")) return null;
  return value.split(":").map(Number);
}

/** Convert a UTC HH:MM value to today's equivalent local HH:MM value. */
export function utcToLocalHHMM(value) {
  const parts = timeParts(value);
  if (!parts) return "";
  const now = new Date();
  const date = new Date(
    Date.UTC(
      now.getUTCFullYear(),
      now.getUTCMonth(),
      now.getUTCDate(),
      parts[0],
      parts[1],
    ),
  );
  return `${String(date.getHours()).padStart(2, "0")}:${String(
    date.getMinutes(),
  ).padStart(2, "0")}`;
}

/** Convert a local HH:MM value to today's equivalent UTC HH:MM value. */
export function localToUtcHHMM(value) {
  const parts = timeParts(value);
  if (!parts) return "";
  const now = new Date();
  const date = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
    parts[0],
    parts[1],
  );
  return `${String(date.getUTCHours()).padStart(2, "0")}:${String(
    date.getUTCMinutes(),
  ).padStart(2, "0")}`;
}

/** Normalize the loop GET response into a complete, editable staged draft. */
export function normalizeLoopConfig(config = {}) {
  // Also accept an already-normalized draft. This makes the helper safe at the
  // component boundary when onConfigChange feeds its value back as loopConfig.
  if (
    config.promptMode &&
    config.schedule &&
    config.onTasks &&
    config.onChild
  ) {
    const childEvents = canonicalizeChildEvents(config.onChild.events);
    return {
      ...config,
      arguments: { ...(config.arguments || {}) },
      maxDuration: { ...(config.maxDuration || { value: 0, unit: "hours" }) },
      triggers: canonicalizeLoopTriggers(config.triggers),
      schedule: { ...config.schedule },
      onCompletion: { ...config.onCompletion },
      onTasks: { ...config.onTasks },
      onChild: {
        events:
          childEvents.length > 0 ? childEvents : [...DEFAULT_CHILD_EVENTS],
      },
    };
  }
  const named = typeof config.prompt_name === "string" && config.prompt_name;
  const sourceTriggers =
    Array.isArray(config.triggers) && config.triggers.length > 0
      ? config.triggers
      : config.trigger
        ? [config.trigger]
        : ["schedule"];
  const childEvents = canonicalizeChildEvents(config.child_events);
  const maxDuration = secondsToValueUnit(config.max_duration_seconds || 0);
  const frequency = config.frequency || {};

  return {
    promptMode: named ? "named" : "freeText",
    promptName: named || "",
    promptBody: typeof config.prompt === "string" ? config.prompt : "",
    arguments:
      config.arguments && typeof config.arguments === "object"
        ? { ...config.arguments }
        : {},
    enabled: config.enabled === true,
    freshContext: config.fresh_context === true,
    runOnStart: config.run_on_start === true,
    maxIterations: finiteNumber(config.max_iterations),
    maxDuration,
    triggers: canonicalizeLoopTriggers(sourceTriggers),
    schedule: {
      value: finiteNumber(frequency.value, 1),
      unit: SCHEDULE_UNITS.includes(frequency.unit) ? frequency.unit : "hours",
      at: utcToLocalHHMM(frequency.at || ""),
    },
    onCompletion: {
      delaySeconds: finiteNumber(config.delay_seconds, 5),
    },
    onTasks: {
      condition: typeof config.condition === "string" ? config.condition : "",
      conditionPreset:
        typeof config.condition_preset === "string"
          ? config.condition_preset
          : "",
      cooldownSeconds: finiteNumber(config.cooldown_seconds),
      settleWindowSeconds: finiteNumber(config.settle_window_seconds),
      coalesceDuringBusy: config.coalesce_during_busy !== false,
    },
    onChild: {
      events: childEvents.length > 0 ? childEvents : [...DEFAULT_CHILD_EVENTS],
    },
    iterationCount: finiteNumber(config.iteration_count),
    stoppedReason: config.stopped_reason || "",
    stoppedAt: config.stopped_at || null,
    acknowledgedStoppedReason: config.acknowledged_stopped_reason || "",
    firstRunAt: config.first_run_at || null,
    lastSentAt: config.last_sent_at || null,
    nextScheduledAt: config.next_scheduled_at || null,
    createdAt: config.created_at || null,
    updatedAt: config.updated_at || null,
  };
}

function addError(errors, fieldErrors, field, message) {
  errors.push({ field, message });
  if (!fieldErrors[field]) fieldErrors[field] = message;
}

/** Validate a staged draft and return errors suitable for inline rendering. */
export function validateLoopDraft(draft) {
  const errors = [];
  const fieldErrors = {};
  const triggers = canonicalizeLoopTriggers(draft?.triggers);
  if (triggers.length === 0) {
    addError(errors, fieldErrors, "triggers", "Select at least one trigger.");
  } else if (triggers.length === 1 && triggers[0] === "onChild") {
    addError(
      errors,
      fieldErrors,
      "triggers",
      "On child must be combined with another trigger.",
    );
  }

  const hasActivePrompt =
    draft?.promptMode === "named"
      ? (draft?.promptName || "").trim() !== ""
      : (draft?.promptBody || "").trim() !== "";
  if (draft?.enabled && !hasActivePrompt) {
    addError(
      errors,
      fieldErrors,
      "prompt",
      "An enabled loop needs a named prompt or free-text prompt.",
    );
  }

  const nonnegative = [
    ["maxIterations", draft?.maxIterations, "Max runs"],
    ["maxDuration", draft?.maxDuration?.value, "Max duration"],
  ];
  if (triggers.includes("onCompletion")) {
    nonnegative.push([
      "delaySeconds",
      draft?.onCompletion?.delaySeconds,
      "Completion delay",
    ]);
  }
  if (triggers.includes("onTasks")) {
    nonnegative.push(
      ["cooldownSeconds", draft?.onTasks?.cooldownSeconds, "Cooldown"],
      [
        "settleWindowSeconds",
        draft?.onTasks?.settleWindowSeconds,
        "Settle time",
      ],
    );
  }
  for (const [field, value, label] of nonnegative) {
    if (!Number.isFinite(Number(value)) || Number(value) < 0) {
      addError(errors, fieldErrors, field, `${label} must be zero or greater.`);
    } else if (!Number.isInteger(Number(value))) {
      addError(errors, fieldErrors, field, `${label} must be a whole number.`);
    }
  }

  if (triggers.includes("schedule")) {
    const value = Number(draft?.schedule?.value);
    const unit = draft?.schedule?.unit;
    if (!Number.isFinite(value) || value <= 0 || !Number.isInteger(value)) {
      addError(
        errors,
        fieldErrors,
        "schedule",
        "Schedule frequency must be a positive whole number.",
      );
    } else if (!SCHEDULE_UNITS.includes(unit)) {
      addError(
        errors,
        fieldErrors,
        "schedule",
        "Select a valid schedule unit.",
      );
    } else if (draft.schedule.at && !HH_MM.test(draft.schedule.at)) {
      addError(errors, fieldErrors, "schedule", "Enter a valid local time.");
    }
  }

  return {
    valid: errors.length === 0,
    errors,
    fieldErrors,
    firstError: errors[0]?.message || "",
  };
}

/** Build the single full-common-fields PATCH used by the staged editor. */
export function buildLoopPatch(draft, options = {}) {
  const opts =
    typeof options === "number" ? { minDelaySeconds: options } : options || {};
  const minDelaySeconds = finiteNumber(opts.minDelaySeconds, 5);
  const triggers = canonicalizeLoopTriggers(draft?.triggers);
  const namedMode = draft?.promptMode === "named";
  const patch = {
    triggers,
    prompt_name: namedMode ? (draft.promptName || "").trim() : "",
    prompt: namedMode ? "" : draft?.promptBody || "",
    arguments: namedMode ? { ...(draft?.arguments || {}) } : {},
    enabled: draft?.enabled === true,
    fresh_context: draft?.freshContext === true,
    max_iterations: finiteNumber(draft?.maxIterations),
    max_duration_seconds: valueUnitToSeconds(
      draft?.maxDuration?.value,
      draft?.maxDuration?.unit,
    ),
    run_on_start: draft?.runOnStart === true,
  };

  if (triggers.includes("schedule")) {
    patch.frequency = {
      value: finiteNumber(draft?.schedule?.value, 1),
      unit: draft?.schedule?.unit || "hours",
    };
    if (patch.frequency.unit === "days" && draft?.schedule?.at) {
      patch.frequency.at = localToUtcHHMM(draft.schedule.at);
    }
  }
  if (triggers.includes("onCompletion")) {
    patch.delay_seconds = Math.max(
      minDelaySeconds,
      finiteNumber(draft?.onCompletion?.delaySeconds),
    );
  }
  if (triggers.includes("onTasks")) {
    patch.condition = draft?.onTasks?.condition || "";
    patch.condition_preset = draft?.onTasks?.conditionPreset || "";
    patch.cooldown_seconds = finiteNumber(draft?.onTasks?.cooldownSeconds);
    patch.settle_window_seconds = finiteNumber(
      draft?.onTasks?.settleWindowSeconds,
    );
    patch.coalesce_during_busy = draft?.onTasks?.coalesceDuringBusy !== false;
  }
  if (triggers.includes("onChild")) {
    const events = canonicalizeChildEvents(draft?.onChild?.events);
    patch.child_events = events.length > 0 ? events : [...DEFAULT_CHILD_EVENTS];
  }
  if (opts.resetCounters === true) patch.reset_counters = true;
  return patch;
}

/** True for a brand-new, unlimited loop with an aggressive/event cadence. */
export function isDangerousUnboundedLoop(draft) {
  const staged = normalizeLoopConfig(draft);
  if (finiteNumber(staged.iterationCount) !== 0) return false;
  if (finiteNumber(staged.maxIterations) > 0) return false;
  if (
    valueUnitToSeconds(staged.maxDuration.value, staged.maxDuration.unit) > 0
  ) {
    return false;
  }
  const triggers = canonicalizeLoopTriggers(staged.triggers);
  if (
    triggers.some((trigger) =>
      ["onCompletion", "onTasks", "onChild"].includes(trigger),
    )
  ) {
    return true;
  }
  return (
    triggers.includes("schedule") &&
    valueUnitToSeconds(staged.schedule.value, staged.schedule.unit) <
      DANGEROUS_SCHEDULE_SECONDS
  );
}

// Explicit settings-oriented aliases for callers that use the module name.
export const normalizeLoopSettings = normalizeLoopConfig;
export const validateLoopSettings = validateLoopDraft;
export const buildLoopSettingsPatch = buildLoopPatch;
export const isDangerousLoopSettings = isDangerousUnboundedLoop;
export const utcToLocalTime = utcToLocalHHMM;
export const localToUtcTime = localToUtcHHMM;
