/**
 * Tests for ModelTagSelect.js's collectModelTags helper (mitto-b3qe).
 *
 * collectModelTags is imported directly rather than duplicated: unlike most
 * components in this codebase, ModelTagSelect.js only reads `window.preact`
 * for the JSX `html` tag used by the component render function itself —
 * collectModelTags is a plain, side-effect-free function. Stubbing
 * `window.preact` as an empty object (so the top-level `const { html } =
 * window.preact` destructure doesn't throw under happy-dom, which has no
 * Preact) is enough to import the module and exercise the helper for real.
 *
 * This is the same helper AutoChildrenEditor (SettingsDialog.js) uses to
 * build its model-tag dropdown, unioning the user's own model-profile tags
 * with the canonical tags returned by the backend (config.model_tags) via a
 * synthetic `{ tags: [...] }` entry — covered by the last test below.
 */

import { describe, test, expect } from "../utils/testing/testGlobals.js";

global.window = global.window || {};
window.preact = window.preact || {};

async function loadModule() {
  return import("./ModelTagSelect.js");
}

describe("collectModelTags", () => {
  test("dedupes case-insensitively (keeping first-seen casing) and sorts alphabetically", async () => {
    const { collectModelTags } = await loadModule();
    const profiles = [
      { name: "A", tags: ["Coding", "fast"] },
      { name: "B", tags: ["FAST", "Cheap"] },
    ];
    expect(collectModelTags(profiles)).toEqual(["Cheap", "Coding", "fast"]);
  });

  test("ignores non-string/blank tag entries and profiles with no tags array", async () => {
    const { collectModelTags } = await loadModule();
    const profiles = [
      { name: "A", tags: ["Coding", "", "  ", 42, null] },
      { name: "B" },
    ];
    expect(collectModelTags(profiles)).toEqual(["Coding"]);
  });

  test("returns [] for an empty or omitted profile list", async () => {
    const { collectModelTags } = await loadModule();
    expect(collectModelTags([])).toEqual([]);
    expect(collectModelTags()).toEqual([]);
  });

  test("unions a synthetic canonical-tags entry alongside real profiles (AutoChildrenEditor usage)", async () => {
    const { collectModelTags } = await loadModule();
    const modelProfiles = [{ name: "Custom", tags: ["Custom"] }];
    const canonicalModelTags = ["Coding", "Smart"];
    const union = collectModelTags([
      ...modelProfiles,
      { tags: canonicalModelTags },
    ]);
    expect(union).toEqual(["Coding", "Custom", "Smart"]);
  });
});
