/**
 * Unit tests for the hash-based model color palette (mitto-8wj).
 *
 * The palette module is pure — no preact / DOM dependencies — so it can be
 * imported directly instead of duplicating helpers inline.
 */

import { describe, test, expect } from "./testing/testGlobals.js";
import { modelColor, UNKNOWN_MODEL_COLOR, UNKNOWN_MODEL_NAME } from "./palette.js";

describe("modelColor", () => {
  test("is deterministic — same name maps to same string across calls", () => {
    // Repeated invocations must return byte-identical strings so the chart
    // legend keeps the same swatch across renders / reloads.
    expect(modelColor("gpt-4o")).toBe(modelColor("gpt-4o"));
    expect(modelColor("claude-3-5-sonnet")).toBe(modelColor("claude-3-5-sonnet"));
    expect(modelColor("gemini-2.0-flash")).toBe(modelColor("gemini-2.0-flash"));
  });

  test("distinct model names generally map to distinct colors", () => {
    // The palette is a hash — collisions are theoretically possible but must
    // not occur across this small representative sample. This pins the
    // "colors are stable" acceptance criterion at the palette level.
    const names = [
      "gpt-4o",
      "gpt-4-turbo",
      "claude-3-5-sonnet",
      "claude-3-opus",
      "gemini-2.0-flash",
      "gemini-1.5-pro",
      "o1-mini",
      "o3",
    ];
    const colors = new Set(names.map(modelColor));
    expect(colors.size).toBe(names.length);
  });

  test('"unknown" (any case) returns the fixed grey', () => {
    expect(modelColor("unknown")).toBe(UNKNOWN_MODEL_COLOR);
    expect(modelColor("Unknown")).toBe(UNKNOWN_MODEL_COLOR);
    expect(modelColor("UNKNOWN")).toBe(UNKNOWN_MODEL_COLOR);
    expect(UNKNOWN_MODEL_COLOR).toBe("#9CA3AF");
  });

  test("empty string returns the fixed grey", () => {
    expect(modelColor("")).toBe(UNKNOWN_MODEL_COLOR);
  });

  test("null / undefined defensively return the fixed grey", () => {
    expect(modelColor(null)).toBe(UNKNOWN_MODEL_COLOR);
    expect(modelColor(undefined)).toBe(UNKNOWN_MODEL_COLOR);
  });

  test("non-unknown names return an hsl(...) string in the documented shape", () => {
    // Contract: hsl(<hue>, 65%, 55%) where hue is 0..359. Pins the format so
    // downstream CSS parsers cannot regress silently.
    const c = modelColor("gpt-4o");
    expect(c.startsWith("hsl(")).toBe(true);
    expect(c).toMatch(/^hsl\(\d{1,3}, 65%, 55%\)$/);
    const hueMatch = c.match(/^hsl\((\d{1,3}),/);
    expect(hueMatch).not.toBeNull();
    const hue = Number(hueMatch[1]);
    expect(hue).toBeGreaterThanOrEqual(0);
    expect(hue).toBeLessThanOrEqual(359);
  });

  test("UNKNOWN_MODEL_NAME constant matches the case-insensitive check", () => {
    // Consumers importing the constant should be able to feed it back through
    // modelColor() and get the grey — no case mismatch trap.
    expect(modelColor(UNKNOWN_MODEL_NAME)).toBe(UNKNOWN_MODEL_COLOR);
  });
});
