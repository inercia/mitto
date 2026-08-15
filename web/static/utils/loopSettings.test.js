/**
 * Unit tests for loopSettings.js helper functions.
 * Tests the real pure exports — no duplicated logic.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
} from "./testing/testGlobals.js";
import {
  KNOWN_LOOP_TRIGGERS,
  KNOWN_CHILD_EVENTS,
  DEFAULT_CHILD_EVENTS,
  canonicalizeLoopTriggers,
  canonicalizeChildEvents,
  valueUnitToSeconds,
  secondsToValueUnit,
  utcToLocalHHMM,
  localToUtcHHMM,
  normalizeLoopConfig,
  validateLoopDraft,
  buildLoopPatch,
  isDangerousUnboundedLoop,
} from "./loopSettings.js";

// =============================================================================
// Canonical ordering tests
// =============================================================================

describe("canonicalizeLoopTriggers", () => {
  test("known trigger order is schedule/onCompletion/onTasks/onChild", () => {
    expect(KNOWN_LOOP_TRIGGERS).toEqual([
      "schedule",
      "onCompletion",
      "onTasks",
      "onChild",
    ]);
  });

  test("orders known triggers canonically regardless of input order", () => {
    expect(
      canonicalizeLoopTriggers([
        "onChild",
        "schedule",
        "onTasks",
        "onCompletion",
      ]),
    ).toEqual(["schedule", "onCompletion", "onTasks", "onChild"]);
    expect(canonicalizeLoopTriggers(["onTasks", "schedule"])).toEqual([
      "schedule",
      "onTasks",
    ]);
  });

  test("preserves unknown future triggers in input order after known triggers", () => {
    expect(
      canonicalizeLoopTriggers([
        "futureB",
        "schedule",
        "futureA",
        "onCompletion",
      ]),
    ).toEqual(["schedule", "onCompletion", "futureB", "futureA"]);
  });

  test("deduplicates triggers", () => {
    expect(
      canonicalizeLoopTriggers(["schedule", "onTasks", "schedule", "onTasks"]),
    ).toEqual(["schedule", "onTasks"]);
  });

  test("handles empty array", () => {
    expect(canonicalizeLoopTriggers([])).toEqual([]);
  });

  test("handles non-array input gracefully", () => {
    expect(canonicalizeLoopTriggers(null)).toEqual([]);
    expect(canonicalizeLoopTriggers(undefined)).toEqual([]);
    expect(canonicalizeLoopTriggers("schedule")).toEqual([]);
  });
});

describe("canonicalizeChildEvents", () => {
  test("known child-event order is anyEndResponse/anyDeleted/anyLoopStopped", () => {
    expect(KNOWN_CHILD_EVENTS).toEqual([
      "anyEndResponse",
      "anyDeleted",
      "anyLoopStopped",
    ]);
  });

  test("default child events are anyEndResponse/anyDeleted", () => {
    expect(DEFAULT_CHILD_EVENTS).toEqual(["anyEndResponse", "anyDeleted"]);
  });

  test("orders known child events canonically", () => {
    expect(
      canonicalizeChildEvents([
        "anyLoopStopped",
        "anyEndResponse",
        "anyDeleted",
      ]),
    ).toEqual(["anyEndResponse", "anyDeleted", "anyLoopStopped"]);
  });

  test("preserves unknown future events in input order after known events", () => {
    expect(
      canonicalizeChildEvents([
        "futureB",
        "anyDeleted",
        "futureA",
        "anyEndResponse",
      ]),
    ).toEqual(["anyEndResponse", "anyDeleted", "futureB", "futureA"]);
  });

  test("deduplicates events", () => {
    expect(
      canonicalizeChildEvents([
        "anyEndResponse",
        "anyDeleted",
        "anyEndResponse",
        "anyDeleted",
      ]),
    ).toEqual(["anyEndResponse", "anyDeleted"]);
  });
});

// =============================================================================
// Duration conversion tests
// =============================================================================

describe("valueUnitToSeconds", () => {
  test("converts minutes to seconds", () => {
    expect(valueUnitToSeconds(5, "minutes")).toBe(300);
  });

  test("converts hours to seconds", () => {
    expect(valueUnitToSeconds(2, "hours")).toBe(7200);
  });

  test("converts days to seconds", () => {
    expect(valueUnitToSeconds(1, "days")).toBe(86400);
  });

  test("returns raw value for unknown unit", () => {
    expect(valueUnitToSeconds(60, "seconds")).toBe(60);
    expect(valueUnitToSeconds(100, "unknown")).toBe(100);
  });

  test("handles non-numeric values", () => {
    expect(valueUnitToSeconds("invalid", "hours")).toBe(0);
    expect(valueUnitToSeconds(null, "hours")).toBe(0);
    expect(valueUnitToSeconds(undefined, "hours")).toBe(0);
  });
});

describe("secondsToValueUnit", () => {
  test("converts exact day multiples", () => {
    expect(secondsToValueUnit(86400)).toEqual({ value: 1, unit: "days" });
    expect(secondsToValueUnit(172800)).toEqual({ value: 2, unit: "days" });
  });

  test("converts exact hour multiples", () => {
    expect(secondsToValueUnit(3600)).toEqual({ value: 1, unit: "hours" });
    expect(secondsToValueUnit(7200)).toEqual({ value: 2, unit: "hours" });
  });

  test("converts exact minute multiples", () => {
    expect(secondsToValueUnit(60)).toEqual({ value: 1, unit: "minutes" });
    expect(secondsToValueUnit(300)).toEqual({ value: 5, unit: "minutes" });
  });

  test("returns raw seconds for non-divisible values", () => {
    expect(secondsToValueUnit(65)).toEqual({ value: 65, unit: "seconds" });
    expect(secondsToValueUnit(3665)).toEqual({ value: 3665, unit: "seconds" });
  });

  test("returns zero with specified unit", () => {
    expect(secondsToValueUnit(0)).toEqual({ value: 0, unit: "hours" });
    expect(secondsToValueUnit(0, "minutes")).toEqual({
      value: 0,
      unit: "minutes",
    });
  });
});

// =============================================================================
// UTC/Local HH:MM conversion tests
// =============================================================================

describe("utcToLocalHHMM / localToUtcHHMM round trips", () => {
  test("valid UTC HH:MM converts to local and back (without assuming timezone)", () => {
    // Test that a round trip produces the original value
    const testValues = ["00:00", "06:30", "12:00", "18:45", "23:59"];
    for (const utc of testValues) {
      const local = utcToLocalHHMM(utc);
      expect(local).toMatch(/^([01][0-9]|2[0-3]):[0-5][0-9]$/);
      const backToUtc = localToUtcHHMM(local);
      expect(backToUtc).toBe(utc);
    }
  });

  test("valid local HH:MM converts to UTC and back", () => {
    const testValues = ["00:00", "06:30", "12:00", "18:45", "23:59"];
    for (const local of testValues) {
      const utc = localToUtcHHMM(local);
      expect(utc).toMatch(/^([01][0-9]|2[0-3]):[0-5][0-9]$/);
      const backToLocal = utcToLocalHHMM(utc);
      expect(backToLocal).toBe(local);
    }
  });

  test("invalid HH:MM returns empty string", () => {
    expect(utcToLocalHHMM("")).toBe("");
    expect(utcToLocalHHMM(null)).toBe("");
    expect(utcToLocalHHMM("25:00")).toBe("");
    expect(utcToLocalHHMM("12:60")).toBe("");
    expect(utcToLocalHHMM("12")).toBe("");
    expect(utcToLocalHHMM("not-a-time")).toBe("");

    expect(localToUtcHHMM("")).toBe("");
    expect(localToUtcHHMM(null)).toBe("");
    expect(localToUtcHHMM("invalid")).toBe("");
  });
});

// =============================================================================
// normalizeLoopConfig tests
// =============================================================================

describe("normalizeLoopConfig", () => {
  test("normalizes complete server config with named prompt", () => {
    const serverConfig = {
      prompt_name: "daily-standup",
      prompt: "",
      arguments: { arg1: "value1" },
      enabled: true,
      fresh_context: true,
      run_on_start: false,
      max_iterations: 10,
      max_duration_seconds: 3600,
      triggers: ["schedule", "onCompletion"],
      frequency: { value: 1, unit: "days", at: "09:00" },
      delay_seconds: 30,
      condition: 'type == "bug"',
      condition_preset: "new-issue-type",
      cooldown_seconds: 60,
      settle_window_seconds: 10,
      coalesce_during_busy: true,
      child_events: ["anyEndResponse", "anyDeleted"],
      iteration_count: 5,
      stopped_reason: "",
    };

    const normalized = normalizeLoopConfig(serverConfig);

    expect(normalized.promptMode).toBe("named");
    expect(normalized.promptName).toBe("daily-standup");
    expect(normalized.promptBody).toBe("");
    expect(normalized.arguments).toEqual({ arg1: "value1" });
    expect(normalized.enabled).toBe(true);
    expect(normalized.freshContext).toBe(true);
    expect(normalized.runOnStart).toBe(false);
    expect(normalized.maxIterations).toBe(10);
    expect(normalized.maxDuration).toEqual({ value: 1, unit: "hours" });
    expect(normalized.triggers).toEqual(["schedule", "onCompletion"]);
    expect(normalized.schedule.value).toBe(1);
    expect(normalized.schedule.unit).toBe("days");
    // at is converted from UTC to local — just check it's a valid HH:MM
    expect(normalized.schedule.at).toMatch(/^([01][0-9]|2[0-3]):[0-5][0-9]$/);
    expect(normalized.onCompletion.delaySeconds).toBe(30);
    expect(normalized.onTasks.condition).toBe('type == "bug"');
    expect(normalized.onTasks.conditionPreset).toBe("new-issue-type");
    expect(normalized.onTasks.cooldownSeconds).toBe(60);
    expect(normalized.onTasks.settleWindowSeconds).toBe(10);
    expect(normalized.onTasks.coalesceDuringBusy).toBe(true);
    expect(normalized.onChild.events).toEqual(["anyEndResponse", "anyDeleted"]);
    expect(normalized.iterationCount).toBe(5);
  });

  test("normalizes server config with free-text prompt", () => {
    const serverConfig = {
      prompt: "Check the build status",
      enabled: false,
    };

    const normalized = normalizeLoopConfig(serverConfig);

    expect(normalized.promptMode).toBe("freeText");
    expect(normalized.promptName).toBe("");
    expect(normalized.promptBody).toBe("Check the build status");
  });

  test("defaults triggers to schedule when absent", () => {
    const normalized = normalizeLoopConfig({});
    expect(normalized.triggers).toEqual(["schedule"]);
  });

  test("defaults child events to anyEndResponse/anyDeleted when empty", () => {
    const normalized = normalizeLoopConfig({ child_events: [] });
    expect(normalized.onChild.events).toEqual(["anyEndResponse", "anyDeleted"]);
  });

  test("defaults booleans correctly", () => {
    const normalized = normalizeLoopConfig({});
    expect(normalized.enabled).toBe(false);
    expect(normalized.freshContext).toBe(false);
    expect(normalized.runOnStart).toBe(false);
    expect(normalized.onTasks.coalesceDuringBusy).toBe(true); // default true
  });

  test("preserves metadata fields", () => {
    const serverConfig = {
      first_run_at: "2024-01-01T00:00:00Z",
      last_sent_at: "2024-01-02T12:00:00Z",
      next_scheduled_at: "2024-01-03T00:00:00Z",
      created_at: "2024-01-01T00:00:00Z",
      updated_at: "2024-01-02T12:00:00Z",
      stopped_at: "2024-01-02T15:00:00Z",
      stopped_reason: "maxIterations",
      acknowledged_stopped_reason: "maxIterations",
    };

    const normalized = normalizeLoopConfig(serverConfig);

    expect(normalized.firstRunAt).toBe("2024-01-01T00:00:00Z");
    expect(normalized.lastSentAt).toBe("2024-01-02T12:00:00Z");
    expect(normalized.nextScheduledAt).toBe("2024-01-03T00:00:00Z");
    expect(normalized.createdAt).toBe("2024-01-01T00:00:00Z");
    expect(normalized.updatedAt).toBe("2024-01-02T12:00:00Z");
    expect(normalized.stoppedAt).toBe("2024-01-02T15:00:00Z");
    expect(normalized.stoppedReason).toBe("maxIterations");
    expect(normalized.acknowledgedStoppedReason).toBe("maxIterations");
  });

  test("re-normalizes an already-normalized draft idempotently", () => {
    const normalized1 = normalizeLoopConfig({
      prompt_name: "test",
      triggers: ["onCompletion", "schedule"],
    });
    const normalized2 = normalizeLoopConfig(normalized1);

    expect(normalized2.promptMode).toBe(normalized1.promptMode);
    expect(normalized2.triggers).toEqual(["schedule", "onCompletion"]);
  });
});

// =============================================================================
// validateLoopDraft tests
// =============================================================================

describe("validateLoopDraft", () => {
  const validDraft = {
    promptMode: "named",
    promptName: "test-prompt",
    enabled: true,
    triggers: ["schedule"],
    schedule: { value: 1, unit: "hours" },
    maxIterations: 5,
    maxDuration: { value: 1, unit: "hours" },
    onCompletion: { delaySeconds: 30 },
    onTasks: { cooldownSeconds: 60, settleWindowSeconds: 10 },
  };

  test("validates a valid draft", () => {
    const result = validateLoopDraft(validDraft);
    expect(result.valid).toBe(true);
    expect(result.errors).toEqual([]);
  });

  test("rejects zero triggers", () => {
    const draft = { ...validDraft, triggers: [] };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(false);
    expect(result.fieldErrors.triggers).toBe("Select at least one trigger.");
  });

  test("rejects onChild-only trigger", () => {
    const draft = { ...validDraft, triggers: ["onChild"] };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(false);
    expect(result.fieldErrors.triggers).toBe(
      "On child must be combined with another trigger.",
    );
  });

  test("accepts onChild with a companion trigger", () => {
    const draft = { ...validDraft, triggers: ["onChild", "onCompletion"] };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(true);
  });

  test("rejects enabled loop with empty prompt", () => {
    const draft = { ...validDraft, promptName: "", promptBody: "" };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(false);
    expect(result.fieldErrors.prompt).toBe(
      "An enabled loop needs a named prompt or free-text prompt.",
    );
  });

  test("allows empty prompt when disabled", () => {
    const draft = { ...validDraft, enabled: false, promptName: "" };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(true);
  });

  test("rejects non-integer maxIterations", () => {
    const draft = { ...validDraft, maxIterations: 5.5 };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(false);
    expect(result.fieldErrors.maxIterations).toMatch(/whole number/);
  });

  test("rejects negative maxIterations", () => {
    const draft = { ...validDraft, maxIterations: -1 };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(false);
    expect(result.fieldErrors.maxIterations).toMatch(/zero or greater/);
  });

  test("rejects invalid schedule frequency", () => {
    const draft = {
      ...validDraft,
      triggers: ["schedule"],
      schedule: { value: 0, unit: "hours" },
    };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(false);
    expect(result.fieldErrors.schedule).toMatch(/positive whole number/);
  });

  test("rejects invalid schedule unit", () => {
    const draft = {
      ...validDraft,
      triggers: ["schedule"],
      schedule: { value: 1, unit: "invalid" },
    };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(false);
    expect(result.fieldErrors.schedule).toMatch(/valid schedule unit/);
  });

  test("rejects invalid schedule.at time", () => {
    const draft = {
      ...validDraft,
      triggers: ["schedule"],
      schedule: { value: 1, unit: "days", at: "invalid" },
    };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(false);
    expect(result.fieldErrors.schedule).toMatch(/valid local time/);
  });

  test("accepts valid schedule.at time", () => {
    const draft = {
      ...validDraft,
      triggers: ["schedule"],
      schedule: { value: 1, unit: "days", at: "09:30" },
    };
    const result = validateLoopDraft(draft);
    expect(result.valid).toBe(true);
  });
});

// =============================================================================
// buildLoopPatch tests
// =============================================================================

describe("buildLoopPatch", () => {
  test("builds complete all-trigger PATCH with canonical triggers", () => {
    const draft = {
      promptMode: "named",
      promptName: "daily-check",
      arguments: { env: "prod" },
      enabled: true,
      freshContext: true,
      runOnStart: false,
      maxIterations: 10,
      maxDuration: { value: 2, unit: "hours" },
      triggers: ["onChild", "onCompletion", "schedule", "onTasks"],
      schedule: { value: 1, unit: "days", at: "09:00" },
      onCompletion: { delaySeconds: 3 },
      onTasks: {
        condition: 'type == "bug"',
        conditionPreset: "new-issue-type",
        cooldownSeconds: 120,
        settleWindowSeconds: 30,
        coalesceDuringBusy: true,
      },
      onChild: { events: ["anyLoopStopped", "anyEndResponse", "anyDeleted"] },
    };

    const patch = buildLoopPatch(draft, { minDelaySeconds: 5 });

    // Canonical trigger order
    expect(patch.triggers).toEqual([
      "schedule",
      "onCompletion",
      "onTasks",
      "onChild",
    ]);

    // Prompt fields
    expect(patch.prompt_name).toBe("daily-check");
    expect(patch.prompt).toBe("");
    expect(patch.arguments).toEqual({ env: "prod" });

    // Common fields
    expect(patch.enabled).toBe(true);
    expect(patch.fresh_context).toBe(true);
    expect(patch.run_on_start).toBe(false);
    expect(patch.max_iterations).toBe(10);
    expect(patch.max_duration_seconds).toBe(7200);

    // Schedule fields with UTC at
    expect(patch.frequency.value).toBe(1);
    expect(patch.frequency.unit).toBe("days");
    expect(patch.frequency.at).toMatch(/^([01][0-9]|2[0-3]):[0-5][0-9]$/);

    // onCompletion delay with min clamp
    expect(patch.delay_seconds).toBe(5);

    // onTasks fields
    expect(patch.condition).toBe('type == "bug"');
    expect(patch.condition_preset).toBe("new-issue-type");
    expect(patch.cooldown_seconds).toBe(120);
    expect(patch.settle_window_seconds).toBe(30);
    expect(patch.coalesce_during_busy).toBe(true);

    // onChild child_events in canonical order
    expect(patch.child_events).toEqual([
      "anyEndResponse",
      "anyDeleted",
      "anyLoopStopped",
    ]);
  });

  test("free-text mode clears prompt_name and arguments", () => {
    const draft = {
      promptMode: "freeText",
      promptName: "should-be-cleared",
      promptBody: "Run the build",
      arguments: { shouldBe: "cleared" },
      triggers: ["schedule"],
      schedule: { value: 1, unit: "hours" },
    };

    const patch = buildLoopPatch(draft);

    expect(patch.prompt_name).toBe("");
    expect(patch.prompt).toBe("Run the build");
    expect(patch.arguments).toEqual({});
  });

  test("dormant trigger-specific fields are omitted", () => {
    const draft = {
      promptMode: "named",
      promptName: "test",
      triggers: ["schedule"], // Only schedule armed
      schedule: { value: 1, unit: "hours" },
      onCompletion: { delaySeconds: 30 },
      onTasks: { condition: "should be omitted" },
      onChild: { events: ["anyEndResponse"] },
    };

    const patch = buildLoopPatch(draft);

    // Schedule is present
    expect(patch.frequency).toBeDefined();

    // Other trigger fields should be absent
    expect(patch.delay_seconds).toBeUndefined();
    expect(patch.condition).toBeUndefined();
    expect(patch.condition_preset).toBeUndefined();
    expect(patch.cooldown_seconds).toBeUndefined();
    expect(patch.settle_window_seconds).toBeUndefined();
    expect(patch.coalesce_during_busy).toBeUndefined();
    expect(patch.child_events).toBeUndefined();
  });

  test("reset_counters is included when option is set", () => {
    const draft = {
      promptMode: "named",
      promptName: "test",
      triggers: ["schedule"],
      schedule: { value: 1, unit: "hours" },
    };

    const patch = buildLoopPatch(draft, { resetCounters: true });
    expect(patch.reset_counters).toBe(true);
  });

  test("accepts legacy minDelaySeconds as number argument", () => {
    const draft = {
      promptMode: "named",
      promptName: "test",
      triggers: ["onCompletion"],
      onCompletion: { delaySeconds: 1 },
    };

    const patch = buildLoopPatch(draft, 10); // Legacy: number arg
    expect(patch.delay_seconds).toBe(10);
  });

  test("defaults child_events when empty", () => {
    const draft = {
      promptMode: "named",
      promptName: "test",
      triggers: ["onChild", "schedule"],
      schedule: { value: 1, unit: "hours" },
      onChild: { events: [] },
    };

    const patch = buildLoopPatch(draft);
    expect(patch.child_events).toEqual(["anyEndResponse", "anyDeleted"]);
  });
});

// =============================================================================
// isDangerousUnboundedLoop tests
// =============================================================================

describe("isDangerousUnboundedLoop", () => {
  // Build a complete normalized draft to pass to isDangerousUnboundedLoop.
  // The function calls normalizeLoopConfig internally, so we need a proper shape.
  function makeDraft(overrides) {
    const base = {
      // Required normalized draft fields
      promptMode: "named",
      promptName: "test",
      promptBody: "",
      arguments: {},
      enabled: true,
      freshContext: false,
      runOnStart: false,
      maxIterations: 0,
      maxDuration: { value: 0, unit: "hours" },
      triggers: ["schedule"],
      schedule: { value: 1, unit: "hours", at: "" },
      onCompletion: { delaySeconds: 5 },
      onTasks: {
        condition: "",
        conditionPreset: "",
        cooldownSeconds: 0,
        settleWindowSeconds: 0,
        coalesceDuringBusy: true,
      },
      onChild: { events: ["anyEndResponse", "anyDeleted"] },
      iterationCount: 0,
      stoppedReason: "",
    };
    return { ...base, ...overrides };
  }

  test("detects dangerous new unlimited event-driven loop", () => {
    expect(
      isDangerousUnboundedLoop(makeDraft({ triggers: ["onCompletion"] })),
    ).toBe(true);
    expect(isDangerousUnboundedLoop(makeDraft({ triggers: ["onTasks"] }))).toBe(
      true,
    );
    expect(
      isDangerousUnboundedLoop(
        makeDraft({ triggers: ["onChild", "schedule"] }),
      ),
    ).toBe(true);
  });

  test("detects dangerous new unlimited fast schedule loop (<5min)", () => {
    expect(
      isDangerousUnboundedLoop(
        makeDraft({
          triggers: ["schedule"],
          schedule: { value: 4, unit: "minutes", at: "" },
        }),
      ),
    ).toBe(true);
  });

  test("slow schedule loops (>=5min) are not dangerous", () => {
    expect(
      isDangerousUnboundedLoop(
        makeDraft({
          triggers: ["schedule"],
          schedule: { value: 5, unit: "minutes", at: "" },
        }),
      ),
    ).toBe(false);
    expect(
      isDangerousUnboundedLoop(
        makeDraft({
          triggers: ["schedule"],
          schedule: { value: 1, unit: "hours", at: "" },
        }),
      ),
    ).toBe(false);
  });

  test("loops with maxIterations cap are not dangerous", () => {
    expect(
      isDangerousUnboundedLoop(
        makeDraft({ triggers: ["onCompletion"], maxIterations: 5 }),
      ),
    ).toBe(false);
  });

  test("loops with maxDuration cap are not dangerous", () => {
    expect(
      isDangerousUnboundedLoop(
        makeDraft({
          triggers: ["onCompletion"],
          maxDuration: { value: 1, unit: "hours" },
        }),
      ),
    ).toBe(false);
  });

  test("existing loops (iterationCount > 0) are not dangerous", () => {
    expect(
      isDangerousUnboundedLoop(
        makeDraft({ triggers: ["onCompletion"], iterationCount: 1 }),
      ),
    ).toBe(false);
  });
});
