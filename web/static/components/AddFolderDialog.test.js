/**
 * Unit tests for AddFolderDialog's pickAddFolderLabel helper.
 *
 * The helper picks the display label for a hidden-workspace entry: prefer the
 * friendly `name` (merged onto workspace records from folders.json via the
 * backend's ApplyFolderDefaults) and fall back to `working_dir`. Both list
 * sites in the dialog (top-3 and "more folders…" popover) use it so a user
 * with a folders.json `name: "blog"` sees "blog" in both places instead of
 * only the raw path in the overflow popover.
 */

import { describe, test, expect } from "../utils/testing/testGlobals.js";
// tildifyPath is a pure module with no window/document access, so it can be
// imported directly here. Only pickAddFolderLabel is duplicated below because
// AddFolderDialog.js touches window.preact at module load.
import { tildifyPath } from "../utils/paths.js";

/**
 * Duplicated from AddFolderDialog.js for testing (the component imports
 * window.preact globals, so it cannot be imported directly under jsdom).
 * Keep this in sync with the implementation.
 */
function pickAddFolderLabel(ws) {
  if (!ws) return "";
  return (
    (ws.name && String(ws.name).trim()) || tildifyPath(ws.working_dir) || ""
  );
}

describe("pickAddFolderLabel", () => {
  test("returns ws.name when it is a non-empty string", () => {
    expect(
      pickAddFolderLabel({
        name: "blog",
        working_dir: "/Users/alvaro/Development/inercia/blog-inerciatech",
      }),
    ).toBe("blog");
  });

  test("falls back to working_dir when name is undefined", () => {
    expect(pickAddFolderLabel({ working_dir: "/tmp/proj" })).toBe("/tmp/proj");
  });

  test("falls back to working_dir when name is null", () => {
    expect(
      pickAddFolderLabel({ name: null, working_dir: "/tmp/proj" }),
    ).toBe("/tmp/proj");
  });

  test("falls back to working_dir when name is an empty string", () => {
    expect(
      pickAddFolderLabel({ name: "", working_dir: "/tmp/proj" }),
    ).toBe("/tmp/proj");
  });

  test("falls back to working_dir when name is whitespace-only", () => {
    expect(
      pickAddFolderLabel({ name: "   ", working_dir: "/tmp/proj" }),
    ).toBe("/tmp/proj");
    expect(
      pickAddFolderLabel({ name: "\t\n", working_dir: "/tmp/proj" }),
    ).toBe("/tmp/proj");
  });

  test("returns empty string on nil-ish input", () => {
    expect(pickAddFolderLabel(null)).toBe("");
    expect(pickAddFolderLabel(undefined)).toBe("");
  });

  test("returns empty string on an empty object", () => {
    expect(pickAddFolderLabel({})).toBe("");
  });

  test("prefers name over working_dir when both are set", () => {
    expect(
      pickAddFolderLabel({
        name: "personal",
        working_dir: "/Users/alvaro/personal-notes",
      }),
    ).toBe("personal");
  });

  test("preserves internal whitespace and casing in name", () => {
    expect(
      pickAddFolderLabel({
        name: "My Cool Project",
        working_dir: "/tmp/proj",
      }),
    ).toBe("My Cool Project");
  });

  test("tildifies the working_dir fallback when name is missing", () => {
    expect(pickAddFolderLabel({ working_dir: "/Users/alvaro/proj" })).toBe(
      "~/proj",
    );
  });
});
