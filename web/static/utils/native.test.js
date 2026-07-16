/**
 * Unit tests for isNonViewableExtension helper in native.js.
 *
 * Only the pure extension classifier is exercised here — the rest of
 * native.js touches window.* bindings (native app bridges, session
 * storage) and is deliberately out of scope for this file.
 */

import { isNonViewableExtension, NON_VIEWABLE_EXTENSIONS } from "./native.js";

describe("isNonViewableExtension", () => {
  // =============================================================================
  // Non-viewable extensions
  // =============================================================================

  test("recognizes Office document extensions", () => {
    expect(isNonViewableExtension("report.xlsx")).toBe(true);
    expect(isNonViewableExtension("memo.docx")).toBe(true);
    expect(isNonViewableExtension("deck.pptx")).toBe(true);
  });

  test("recognizes archive extensions", () => {
    expect(isNonViewableExtension("bundle.zip")).toBe(true);
  });

  test("recognizes installer extensions", () => {
    expect(isNonViewableExtension("installer.dmg")).toBe(true);
  });

  test("is case-insensitive", () => {
    expect(isNonViewableExtension("REPORT.XLSX")).toBe(true);
    expect(isNonViewableExtension("Mixed.DocX")).toBe(true);
  });

  test("works on full absolute paths", () => {
    expect(isNonViewableExtension("/Users/foo/RSU_Report_Live.xlsx")).toBe(
      true,
    );
  });

  test("strips trailing query/fragment before checking", () => {
    expect(isNonViewableExtension("report.xlsx?foo=bar")).toBe(true);
    expect(isNonViewableExtension("report.xlsx#sheet1")).toBe(true);
  });

  // =============================================================================
  // Viewable / plain-text extensions
  // =============================================================================

  test("rejects viewable text/code extensions", () => {
    expect(isNonViewableExtension("readme.md")).toBe(false);
    expect(isNonViewableExtension("script.js")).toBe(false);
    expect(isNonViewableExtension("notes.txt")).toBe(false);
    expect(isNonViewableExtension("page.html")).toBe(false);
  });

  test("rejects image extensions (viewer renders them)", () => {
    expect(isNonViewableExtension("photo.png")).toBe(false);
  });

  test("rejects viewable files with query/fragment", () => {
    expect(isNonViewableExtension("file.md#anchor")).toBe(false);
    expect(isNonViewableExtension("notes.txt?v=2")).toBe(false);
  });

  // =============================================================================
  // Edge cases
  // =============================================================================

  test("returns false for empty / nullish / non-string input", () => {
    expect(isNonViewableExtension("")).toBe(false);
    expect(isNonViewableExtension(null)).toBe(false);
    expect(isNonViewableExtension(undefined)).toBe(false);
    expect(isNonViewableExtension(42)).toBe(false);
  });

  test("returns false for paths with no extension", () => {
    expect(isNonViewableExtension("noext")).toBe(false);
    expect(isNonViewableExtension("/tmp/README")).toBe(false);
  });

  test("returns false for trailing-dot paths", () => {
    expect(isNonViewableExtension("trailingdot.")).toBe(false);
  });

  // =============================================================================
  // Set contents sanity
  // =============================================================================

  test("NON_VIEWABLE_EXTENSIONS covers expected buckets", () => {
    // Sample from each documented bucket to catch accidental deletions.
    expect(NON_VIEWABLE_EXTENSIONS.has("xlsx")).toBe(true);
    expect(NON_VIEWABLE_EXTENSIONS.has("docx")).toBe(true);
    expect(NON_VIEWABLE_EXTENSIONS.has("zip")).toBe(true);
    expect(NON_VIEWABLE_EXTENSIONS.has("dmg")).toBe(true);
    // Uppercase must NOT be pre-baked into the set — the check lowercases.
    expect(NON_VIEWABLE_EXTENSIONS.has("XLSX")).toBe(false);
  });
});
