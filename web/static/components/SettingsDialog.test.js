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
 * Duplicated from SettingsDialog.js (Tags onInput handler, ~lines 4745-4775).
 * Splits the raw draft text on every comma typed so far into already-committed
 * tokens (trimmed, empties dropped) plus the trailing partial token that is
 * still being typed. This is the fix for the historical bug where a
 * controlled `value={tags.join(", ")}` swallowed a just-typed comma.
 */
const splitTagDraftOnInput = (raw) => {
  if (!raw.includes(",")) return { committed: [], trailing: raw };
  const parts = raw.split(",");
  const trailing = parts.pop();
  const committed = parts.map((t) => t.trim()).filter(Boolean);
  return { committed, trailing };
};

/**
 * Duplicated from SettingsDialog.js (commitTagDraft, ~lines 2306-2320).
 * Commits a raw draft string (blur/Enter/comma) into the tags array: split on
 * comma, trim, drop empties, merge+dedupe with the existing tags.
 */
const commitTagTokens = (raw, existingTags = []) => {
  const tokens = raw
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean);
  if (tokens.length === 0) return existingTags;
  return [...new Set([...existingTags, ...tokens])];
};

/**
 * Duplicated from SettingsDialog.js (modelProfilesToSave, ~lines 1919-1933).
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

/**
 * Duplicated from SettingsDialog.js (modelProfilesToSave filter, ~line 1933).
 * A normalized profile is fully empty when it has no name, no criteria, and
 * no tags — these are dropped silently rather than saved.
 */
const isEmptyNormalizedProfile = (normalized) =>
  normalized.name === "" && !normalized.criteria && normalized.tags.length === 0;

/**
 * Duplicated from SettingsDialog.js (handleSave validation, ~lines 1742-1753).
 * A profile blocks Save when its name is blank but it has criteria or tags
 * (i.e. partially filled, as opposed to fully empty).
 */
const hasBlankNamedProfile = (profiles) =>
  profiles.some((p) => {
    const name = (p.name || "").trim();
    const tags = Array.isArray(p.tags) ? p.tags.filter((t) => t && t.trim()) : [];
    return name === "" && (!!p.criteria || tags.length > 0);
  });

describe("splitTagDraftOnInput", () => {
  test("no comma yet: everything is the trailing (in-progress) token", () => {
    expect(splitTagDraftOnInput("Smart")).toEqual({
      committed: [],
      trailing: "Smart",
    });
  });

  test("a trailing comma commits the token and leaves an empty trailing", () => {
    expect(splitTagDraftOnInput("Smart,")).toEqual({
      committed: ["Smart"],
      trailing: "",
    });
  });

  test("typing continues after a comma without losing characters", () => {
    expect(splitTagDraftOnInput("Smart,Che")).toEqual({
      committed: ["Smart"],
      trailing: "Che",
    });
  });

  test("multiple commas (e.g. pasted text) commit multiple tokens", () => {
    expect(splitTagDraftOnInput("A,B,C")).toEqual({
      committed: ["A", "B"],
      trailing: "C",
    });
  });

  test("empty string stays as an empty trailing token", () => {
    expect(splitTagDraftOnInput("")).toEqual({ committed: [], trailing: "" });
  });
});

describe("commitTagTokens", () => {
  test("splits, trims and drops empties", () => {
    expect(commitTagTokens("  A ,B  ,  C")).toEqual(["A", "B", "C"]);
  });

  test("drops empty entries from trailing/duplicate commas", () => {
    expect(commitTagTokens("A,,B,")).toEqual(["A", "B"]);
  });

  test("empty/whitespace-only raw text leaves existing tags unchanged", () => {
    expect(commitTagTokens(" , ", ["Smart"])).toEqual(["Smart"]);
  });

  test("merges with and dedupes against existing tags", () => {
    expect(commitTagTokens("Smart, New", ["Smart"])).toEqual(["Smart", "New"]);
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

describe("isEmptyNormalizedProfile", () => {
  test("a profile with no name, criteria or tags is empty", () => {
    expect(isEmptyNormalizedProfile(normalizeModelProfile({}))).toBe(true);
  });

  test("a profile with only a name is not empty", () => {
    expect(
      isEmptyNormalizedProfile(normalizeModelProfile({ name: "Opus" })),
    ).toBe(false);
  });

  test("a blank-name profile with criteria is not empty (blocked, not dropped)", () => {
    const normalized = normalizeModelProfile({
      criteria: { matchMode: "contains", pattern: "Opus" },
    });
    expect(isEmptyNormalizedProfile(normalized)).toBe(false);
  });

  test("a blank-name profile with tags is not empty (blocked, not dropped)", () => {
    const normalized = normalizeModelProfile({ tags: ["Smart"] });
    expect(isEmptyNormalizedProfile(normalized)).toBe(false);
  });
});

describe("hasBlankNamedProfile", () => {
  test("no profiles: false", () => {
    expect(hasBlankNamedProfile([])).toBe(false);
  });

  test("fully-empty profile does not block save", () => {
    expect(hasBlankNamedProfile([{ name: "", criteria: null, tags: [] }])).toBe(
      false,
    );
  });

  test("named profile does not block save", () => {
    expect(
      hasBlankNamedProfile([{ name: "Opus", criteria: null, tags: [] }]),
    ).toBe(false);
  });

  test("blank name with criteria blocks save", () => {
    expect(
      hasBlankNamedProfile([
        { name: "", criteria: { matchMode: "contains" }, tags: [] },
      ]),
    ).toBe(true);
  });

  test("blank name with tags blocks save", () => {
    expect(
      hasBlankNamedProfile([{ name: "  ", criteria: null, tags: ["Smart"] }]),
    ).toBe(true);
  });
});
