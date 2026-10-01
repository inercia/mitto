import { describe, test, expect } from "bun:test";
import { placeThoughtLevelNearModel } from "./configOptions.js";

describe("placeThoughtLevelNearModel", () => {
  test("moves a single thought_level option to right after the model option", () => {
    const options = [
      { id: "mode", category: "mode" },
      { id: "model", category: "model" },
      { id: "reasoning_effort", category: "thought_level" },
      { id: "other", category: "misc" },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result.map((o) => o.id)).toEqual(["mode", "model", "reasoning_effort", "other"]);
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
    expect(result.map((o) => o.id)).toEqual(["a", "model", "reasoning_effort", "b", "c"]);
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
    expect(result.map((o) => o.id)).toEqual(["mode", "model", "reasoning_effort"]);
  });

  test("does not hardcode any specific option id or value", () => {
    // Uses opaque, agent-defined ids/values unrelated to any known agent.
    const options = [
      { id: "xyz-model-123", category: "model", options: [{ value: "v1" }] },
      { id: "opaque-effort-id", category: "thought_level", options: [{ value: "lvl-a" }] },
    ];
    const result = placeThoughtLevelNearModel(options);
    expect(result.map((o) => o.id)).toEqual(["xyz-model-123", "opaque-effort-id"]);
  });
});
