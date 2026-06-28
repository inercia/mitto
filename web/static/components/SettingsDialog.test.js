/**
 * Unit tests for the Models settings tab pure data-transform helpers.
 *
 * These duplicate the pure transforms from SettingsDialog.js (the component
 * reads window.preact globals at module load and cannot be imported directly
 * under jsdom). Keep these helpers in sync with the implementation.
 *
 * The normalized shape produced by normalizeModelProfile matches the backend
 * preserve-on-omit round-trip contract: criteria is a pointer (object|null)
 * and tags is always a filtered array — never undefined or null.
 */

/**
 * Duplicated from SettingsDialog.js (Tags onInput handler, ~line 4528).
 * Parses a comma-separated tags string into a trimmed, filtered array.
 */
const parseTagsInput = (value) =>
  value
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean);

/**
 * Duplicated from SettingsDialog.js (modelProfilesToSave, ~lines 1869-1876).
 * Normalizes a model profile object for the save payload.
 */
const normalizeModelProfile = (p) => ({
  name: (p.name || "").trim(),
  criteria:
    p.criteria && p.criteria.matchMode
      ? { matchMode: p.criteria.matchMode, pattern: p.criteria.pattern || "" }
      : null,
  tags: Array.isArray(p.tags) ? p.tags.filter((t) => t && t.trim()) : [],
});

describe("parseTagsInput", () => {
  test("splits a comma-separated string into trimmed tags", () => {
    expect(parseTagsInput("Smart, Cheap")).toEqual(["Smart", "Cheap"]);
  });

  test("trims surrounding whitespace on each tag", () => {
    expect(parseTagsInput("  A ,B  ,  C")).toEqual(["A", "B", "C"]);
  });

  test("drops empty entries from trailing/duplicate commas", () => {
    expect(parseTagsInput("A,,B,")).toEqual(["A", "B"]);
  });

  test("empty string returns empty array", () => {
    expect(parseTagsInput("")).toEqual([]);
  });

  test("whitespace-only entries are removed", () => {
    expect(parseTagsInput(" , ")).toEqual([]);
  });
});

describe("normalizeModelProfile", () => {
  test("trims the name", () => {
    expect(normalizeModelProfile({ name: "  Opus  " }).name).toBe("Opus");
  });

  test("missing name becomes empty string", () => {
    expect(normalizeModelProfile({}).name).toBe("");
  });

  test("criteria with matchMode is kept as {matchMode, pattern}", () => {
    const result = normalizeModelProfile({
      criteria: { matchMode: "contains", pattern: "Opus" },
    });
    expect(result.criteria).toEqual({ matchMode: "contains", pattern: "Opus" });
  });

  test("criteria pattern defaults to empty string when absent", () => {
    const result = normalizeModelProfile({
      criteria: { matchMode: "exact" },
    });
    expect(result.criteria).toEqual({ matchMode: "exact", pattern: "" });
  });

  test("criteria without matchMode becomes null", () => {
    const result = normalizeModelProfile({ criteria: { pattern: "x" } });
    expect(result.criteria).toBeNull();
  });

  test("null criteria becomes null", () => {
    expect(normalizeModelProfile({ criteria: null }).criteria).toBeNull();
  });

  test("absent criteria becomes null", () => {
    expect(normalizeModelProfile({}).criteria).toBeNull();
  });

  test("tags array has empty/whitespace entries filtered", () => {
    const result = normalizeModelProfile({
      tags: ["Smart", "", "  ", "Cheap"],
    });
    expect(result.tags).toEqual(["Smart", "Cheap"]);
  });

  test("non-array tags (undefined) become empty array", () => {
    expect(normalizeModelProfile({ tags: undefined }).tags).toEqual([]);
  });

  test("a full realistic profile round-trips to the exact expected object", () => {
    const profile = {
      name: "  Opus  ",
      criteria: { matchMode: "contains", pattern: "Opus" },
      tags: ["Smartest", "", "Expensive"],
    };
    expect(normalizeModelProfile(profile)).toEqual({
      name: "Opus",
      criteria: { matchMode: "contains", pattern: "Opus" },
      tags: ["Smartest", "Expensive"],
    });
  });
});
