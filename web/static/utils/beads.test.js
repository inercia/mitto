import {
  isDarkColor,
  taskTitleTextClass,
  TITLE_DARK_LUMINANCE_THRESHOLD,
} from "./beads.js";

// mitto-m5f.4: contrast-aware task-title text color (WCAG relative luminance).
// Boundary colors and their measured luminance (see the Plan/Implementation
// bead comments for the full grounded computation):
//   #000000 black   L=0.0000 -> dark  -> white text
//   #ffffff white   L=1.0000 -> light -> dark text
//   #8d8600 olive   L=0.2271 -> dark  -> white text
//   #3b82f6 blue    L=0.2355 -> dark  -> white text (naive avg 0.568 would say "light")
//   #eab308 yellow  L=0.4975 -> light -> dark text (right on the old 0.5 boundary)
//   #f59e0b amber   L=0.4389 -> light -> dark text
//   #22c55e green   L=0.4108 -> light -> dark text
describe("isDarkColor (mitto-m5f.4): WCAG relative luminance classification", () => {
  test("black is dark", () => {
    expect(isDarkColor("#000000")).toBe(true);
  });

  test("white is light", () => {
    expect(isDarkColor("#ffffff")).toBe(false);
  });

  test("saturated olive is dark despite a mid-range naive average", () => {
    expect(isDarkColor("#8d8600")).toBe(true);
  });

  test("saturated blue is dark (relative luminance, not naive channel average)", () => {
    // Naive average of (59,130,246)/255 ~= 0.568 would misclassify this as
    // "light"; true relative luminance (0.2355) is below the threshold.
    expect(isDarkColor("#3b82f6")).toBe(true);
  });

  test("light yellow is light even though it sits just under the old 0.5 threshold", () => {
    // L=0.4975 for #eab308 would misclassify as "dark" under a naive 0.5
    // cutoff; the calibrated 0.4 threshold correctly classifies it as light.
    expect(isDarkColor("#eab308")).toBe(false);
  });

  test("amber and green are light", () => {
    expect(isDarkColor("#f59e0b")).toBe(false);
    expect(isDarkColor("#22c55e")).toBe(false);
  });

  test("supports 3-digit shorthand hex", () => {
    expect(isDarkColor("#000")).toBe(true);
    expect(isDarkColor("#fff")).toBe(false);
  });

  test("is case-insensitive and tolerates surrounding whitespace", () => {
    expect(isDarkColor("#000000".toUpperCase())).toBe(true);
    expect(isDarkColor("  #000000  ")).toBe(true);
  });

  test("treats invalid or empty input as light (preserves default dark-theme text)", () => {
    expect(isDarkColor("")).toBe(false);
    expect(isDarkColor(undefined)).toBe(false);
    expect(isDarkColor(null)).toBe(false);
    expect(isDarkColor("not-a-color")).toBe(false);
    expect(isDarkColor("#12345")).toBe(false);
    expect(isDarkColor("#gggggg")).toBe(false);
  });

  test("threshold constant is calibrated to 0.4 (documented deviation from a naive 0.5)", () => {
    expect(TITLE_DARK_LUMINANCE_THRESHOLD).toBe(0.4);
  });
});

describe("taskTitleTextClass (mitto-m5f.4): text color mapping", () => {
  test("dark backgrounds get white title text", () => {
    expect(taskTitleTextClass("#000000")).toBe("text-white");
    expect(taskTitleTextClass("#3b82f6")).toBe("text-white");
    expect(taskTitleTextClass("#8d8600")).toBe("text-white");
  });

  test("light backgrounds get near-black title text", () => {
    expect(taskTitleTextClass("#ffffff")).toBe("text-neutral-900");
    expect(taskTitleTextClass("#eab308")).toBe("text-neutral-900");
    expect(taskTitleTextClass("#f59e0b")).toBe("text-neutral-900");
    expect(taskTitleTextClass("#22c55e")).toBe("text-neutral-900");
  });

  test("invalid/empty input falls back to the light-background (dark text) branch", () => {
    expect(taskTitleTextClass("")).toBe("text-neutral-900");
    expect(taskTitleTextClass(undefined)).toBe("text-neutral-900");
  });
});
