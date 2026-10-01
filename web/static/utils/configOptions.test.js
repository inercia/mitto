import { describe, test, expect } from "bun:test";
import {
  placeThoughtLevelNearModel,
  CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED,
  CONFIG_ADJUSTMENT_REASON_VALUE_NOT_ADVERTISED,
  CONFIG_ADJUSTMENT_REASON_SNAPSHOT_UNCONFIRMED,
  describeConfigAdjustment,
  configAdjustmentDedupKey,
} from "./configOptions.js";

describe("placeThoughtLevelNearModel", () => {
  test("moves a single thought_level option to right after the model option", () => {
    const options = [
      { id: "mode", category: "mode" },
      { id: "model", category: "model" },
      { id: "reasoning_effort", category: "thought_level" },
      { id: "other", category: "misc" },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result.map((o) => o.id)).toEqual([
      "mode",
      "model",
      "reasoning_effort",
      "other",
    ]);
  });

  test("preserves relative order of unrelated options", () => {
    const options = [
      { id: "reasoning_effort", category: "thought_level" },
      { id: "a", category: "misc" },
      { id: "model", category: "model" },
      { id: "b", category: "misc" },
      { id: "c", category: "misc" },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result.map((o) => o.id)).toEqual([
      "a",
      "model",
      "reasoning_effort",
      "b",
      "c",
    ]);
  });

  test("handles multiple thought_level options, keeping their relative order", () => {
    const options = [
      { id: "model", category: "model" },
      { id: "other", category: "misc" },
      { id: "t1", category: "thought_level" },
      { id: "t2", category: "thought_level" },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result.map((o) => o.id)).toEqual(["model", "t1", "t2", "other"]);
  });

  test("is a no-op when there is no model option", () => {
    const options = [
      { id: "mode", category: "mode" },
      { id: "reasoning_effort", category: "thought_level" },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result).toBe(options);
  });

  test("is a no-op when there is no thought_level option", () => {
    const options = [
      { id: "model", category: "model" },
      { id: "mode", category: "mode" },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result).toBe(options);
  });

  test("is a no-op on an empty array", () => {
    const options = [];
    const result = placeThoughtLevelNearModel(options);
    expect(result).toBe(options);
  });

  test("handles non-array input gracefully", () => {
    expect(placeThoughtLevelNearModel(null)).toBeNull();
    expect(placeThoughtLevelNearModel(undefined)).toBeUndefined();
  });

  test("places thought_level immediately after model even when model is last", () => {
    const options = [
      { id: "reasoning_effort", category: "thought_level" },
      { id: "mode", category: "mode" },
      { id: "model", category: "model" },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result.map((o) => o.id)).toEqual([
      "mode",
      "model",
      "reasoning_effort",
    ]);
  });

  test("does not hardcode any specific option id or value", () => {
    // Uses opaque, agent-defined ids/values unrelated to any known agent.
    const options = [
      { id: "xyz-model-123", category: "model", options: [{ value: "v1" }] },
      {
        id: "opaque-effort-id",
        category: "thought_level",
        options: [{ value: "lvl-a" }],
      },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result.map((o) => o.id)).toEqual([
      "xyz-model-123",
      "opaque-effort-id",
    ]);
  });
});

describe("describeConfigAdjustment", () => {
  // Opaque fixture ids/values on purpose — the helper must never special-case
  // any specific vendor/model/effort-level identifier.
  const configOptions = [
    {
      id: "qux-setting",
      name: "Qux Setting",
      options: [
        { value: "lvl-a", name: "Level A" },
        { value: "lvl-b", name: "Level B" },
      ],
    },
  ];

  test("option_removed: uses the option display name and warning style", () => {
    const result = describeConfigAdjustment(
      {
        config_id: "qux-setting",
        reason: CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED,
      },
      configOptions,
    );
    expect(result.style).toBe("warning");
    expect(result.message).toContain("Qux Setting");
    expect(result.message.toLowerCase()).toContain("cleared");
  });

  test("value_not_advertised: includes the applied value's display name when known", () => {
    const result = describeConfigAdjustment(
      {
        config_id: "qux-setting",
        reason: CONFIG_ADJUSTMENT_REASON_VALUE_NOT_ADVERTISED,
        requested_value: "lvl-z",
        applied_value: "lvl-b",
      },
      configOptions,
    );
    expect(result.style).toBe("warning");
    expect(result.message).toContain("Qux Setting");
    expect(result.message).toContain("Level B");
  });

  test("value_not_advertised: omits the 'using X instead' clause when applied_value is unknown/absent", () => {
    const result = describeConfigAdjustment(
      {
        config_id: "qux-setting",
        reason: CONFIG_ADJUSTMENT_REASON_VALUE_NOT_ADVERTISED,
        requested_value: "lvl-z",
      },
      configOptions,
    );
    expect(result.message).toContain("Qux Setting");
    expect(result.message).not.toContain("instead");
    expect(result.message).not.toContain("undefined");
  });

  test("snapshot_unconfirmed: uses info style and a non-committal message", () => {
    const result = describeConfigAdjustment(
      {
        config_id: "qux-setting",
        reason: CONFIG_ADJUSTMENT_REASON_SNAPSHOT_UNCONFIRMED,
      },
      configOptions,
    );
    expect(result.style).toBe("info");
    expect(result.message).toContain("Qux Setting");
    expect(result.message.toLowerCase()).toContain("couldn't confirm");
  });

  test("falls back to config_id when no matching configOption is found", () => {
    const result = describeConfigAdjustment(
      {
        config_id: "unknown-id",
        reason: CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED,
      },
      configOptions,
    );
    expect(result.message).toContain("unknown-id");
  });

  test("falls back to a generic label when config_id is also absent", () => {
    const result = describeConfigAdjustment(
      { reason: CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED },
      configOptions,
    );
    expect(result.message).toContain("Setting");
  });

  test("unknown reason falls back to a generic info-style adjustment message", () => {
    const result = describeConfigAdjustment(
      { config_id: "qux-setting", reason: "some_future_reason" },
      configOptions,
    );
    expect(result.style).toBe("info");
    expect(result.message).toContain("Qux Setting");
  });

  test("never hardcodes vendor/model/effort-level names in the output", () => {
    const result = describeConfigAdjustment(
      {
        config_id: "qux-setting",
        reason: CONFIG_ADJUSTMENT_REASON_VALUE_NOT_ADVERTISED,
        applied_value: "lvl-a",
      },
      configOptions,
    );
    // Only the opaque fixture's own display names should appear — assert the
    // message is built from the supplied snapshot, not fixed vendor strings.
    expect(result.message).toContain("Level A");
    expect(result.message).not.toMatch(/claude|gpt|gemini|opus|sonnet|auggie/i);
  });
});

describe("configAdjustmentDedupKey", () => {
  test("produces a stable key for identical inputs", () => {
    const adjustment = {
      config_id: "qux-setting",
      reason: CONFIG_ADJUSTMENT_REASON_VALUE_NOT_ADVERTISED,
      applied_value: "lvl-a",
      requested_value: "lvl-z",
    };
    const key1 = configAdjustmentDedupKey("session-1", adjustment);
    const key2 = configAdjustmentDedupKey("session-1", { ...adjustment });
    expect(key1).toBe(key2);
  });

  test("differs when session id differs", () => {
    const adjustment = {
      config_id: "qux-setting",
      reason: CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED,
    };
    const key1 = configAdjustmentDedupKey("session-1", adjustment);
    const key2 = configAdjustmentDedupKey("session-2", adjustment);
    expect(key1).not.toBe(key2);
  });

  test("differs when config_id differs", () => {
    const base = { reason: CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED };
    const key1 = configAdjustmentDedupKey("session-1", {
      ...base,
      config_id: "a",
    });
    const key2 = configAdjustmentDedupKey("session-1", {
      ...base,
      config_id: "b",
    });
    expect(key1).not.toBe(key2);
  });

  test("differs when reason differs", () => {
    const base = { config_id: "qux-setting" };
    const key1 = configAdjustmentDedupKey("session-1", {
      ...base,
      reason: CONFIG_ADJUSTMENT_REASON_OPTION_REMOVED,
    });
    const key2 = configAdjustmentDedupKey("session-1", {
      ...base,
      reason: CONFIG_ADJUSTMENT_REASON_SNAPSHOT_UNCONFIRMED,
    });
    expect(key1).not.toBe(key2);
  });

  test("differs when applied_value or requested_value differ", () => {
    const base = {
      config_id: "qux-setting",
      reason: CONFIG_ADJUSTMENT_REASON_VALUE_NOT_ADVERTISED,
    };
    const key1 = configAdjustmentDedupKey("session-1", {
      ...base,
      applied_value: "lvl-a",
      requested_value: "lvl-z",
    });
    const key2 = configAdjustmentDedupKey("session-1", {
      ...base,
      applied_value: "lvl-b",
      requested_value: "lvl-z",
    });
    const key3 = configAdjustmentDedupKey("session-1", {
      ...base,
      applied_value: "lvl-a",
      requested_value: "lvl-y",
    });
    expect(key1).not.toBe(key2);
    expect(key1).not.toBe(key3);
  });

  test("handles missing fields without throwing", () => {
    expect(() => configAdjustmentDedupKey("session-1", {})).not.toThrow();
    expect(() => configAdjustmentDedupKey(undefined, undefined)).not.toThrow();
  });
});
