import { describe, expect, test } from "bun:test";
import {
  MODEL_SHORTCUT_NUMBERS,
  loadModelShortcuts,
  availableModelShortcutNumbers,
  availableModelShortcutTags,
  addModelShortcut,
  updateModelShortcut,
  removeModelShortcut,
  validateModelShortcuts,
  serializeModelShortcuts,
  modelShortcutLabel,
} from "./settingsModelShortcuts.js";

describe("Settings model shortcut drafts (production helpers)", () => {
  test("load absent/null/empty as fresh empty lists, without legacy inference", () => {
    for (const value of [undefined, null, []]) {
      expect(loadModelShortcuts(value)).toEqual([]);
    }
    const saved = [{ number: 5, model_tag: "Custom  Tag", extra: true }];
    const loaded = loadModelShortcuts(saved);
    expect(loaded).toEqual([{ number: 5, model_tag: "Custom  Tag" }]);
    expect(loaded).not.toBe(saved);
    expect(loaded[0]).not.toBe(saved[0]);
    loaded[0].model_tag = "edited";
    expect(saved[0].model_tag).toBe("Custom  Tag");
  });

  test("Add reserves the lowest free number even for blank drafts and guards full", () => {
    let rows = [];
    for (const number of MODEL_SHORTCUT_NUMBERS) {
      const previous = rows;
      rows = addModelShortcut(rows);
      expect(rows.at(-1)).toEqual({ number, model_tag: "" });
      expect(previous).toHaveLength(number - 1);
    }
    expect(addModelShortcut(rows)).toBe(rows);
    expect(availableModelShortcutNumbers(rows)).toEqual([]);
    expect(availableModelShortcutNumbers(rows, 2)).toEqual([3]);
    rows = removeModelShortcut(rows, 1);
    expect(addModelShortcut(rows).at(-1).number).toBe(2);
  });

  test("tag choices exclude selections in other rows and preserve the row's own legacy tag", () => {
    const tags = ["Coding", "Fast", "Smart"];
    const rows = [
      { number: 1, model_tag: " coding " },
      { number: 2, model_tag: "Fast" },
      { number: 3, model_tag: "" },
    ];
    expect(availableModelShortcutTags(tags, rows, 0)).toEqual(["Coding", "Smart"]);
    expect(availableModelShortcutTags(tags, rows, 2)).toEqual(["Smart"]);
    expect(availableModelShortcutTags(tags, rows)).toEqual(["Smart"]);
    expect(availableModelShortcutTags(tags, removeModelShortcut(rows, 1), 1)).toEqual(["Fast", "Smart"]);
    expect(availableModelShortcutTags(["Coding"], [rows[0], { number: 2, model_tag: "CODING" }], 0)).toEqual(["Coding"]);
  });

  test("immutable edits accept own/free numbers, preserve raw tags and never steal", () => {
    const rows = Object.freeze([
      Object.freeze({ number: 1, model_tag: "Coding" }),
      Object.freeze({ number: 3, model_tag: "Coding" }),
    ]);
    expect(availableModelShortcutNumbers(rows, 0)).toEqual([1, 2, 4, 5]);
    expect(updateModelShortcut(rows, 0, { number: "1" })[0].number).toBe(1);
    const moved = updateModelShortcut(rows, 0, { number: "5" });
    expect(moved[0]).toEqual({ number: 5, model_tag: "Coding" });
    expect(moved[1]).toBe(rows[1]);
    expect(rows[0].number).toBe(1);
    const raw = "  CuStOm,  Tag  ";
    expect(updateModelShortcut(rows, 0, { model_tag: raw })[0].model_tag).toBe(
      raw,
    );
    for (const number of [
      3,
      0,
      6,
      -1,
      1.5,
      "1.5",
      "2x",
      NaN,
      Infinity,
      "",
      true,
      null,
    ]) {
      expect(updateModelShortcut(rows, 0, { number })).toBe(rows);
    }
    expect(updateModelShortcut(rows, 0, { model_tag: null })).toBe(rows);
    for (const index of [-1, 2, 0.5, "0", NaN]) {
      expect(updateModelShortcut(rows, index, { number: 2 })).toBe(rows);
      expect(removeModelShortcut(rows, index)).toBe(rows);
    }
    const removed = removeModelShortcut(rows, 0);
    expect(removed).toEqual([rows[1]]);
    expect(rows).toHaveLength(2);
  });

  test("Save preserves order/case/internal whitespace and trims only edges", () => {
    const rows = [
      { number: 5, model_tag: "  CuStOm,  Tag  ", extra: true },
      { number: 1, model_tag: "CuStOm,  Tag" },
    ];
    expect(validateModelShortcuts(rows)).toBeNull();
    expect(serializeModelShortcuts(rows)).toEqual([
      { number: 5, model_tag: "CuStOm,  Tag" },
      { number: 1, model_tag: "CuStOm,  Tag" },
    ]);
    expect(rows[0].model_tag).toBe("  CuStOm,  Tag  ");
    expect(serializeModelShortcuts([])).toEqual([]);
  });

  test("invalid rows block the entire Save with row-specific errors, never dropping rows", () => {
    for (const invalid of [
      { number: 1, model_tag: "duplicate" },
      { number: 2, model_tag: "  " },
      { number: 2, model_tag: null },
      ...[0, 6, -1, 2.5, "2", NaN].map((number) => ({
        number,
        model_tag: "x",
      })),
    ]) {
      const rows = [{ number: 1, model_tag: "ok" }, invalid];
      expect(validateModelShortcuts(rows)).toContain("row 2:");
      expect(() => serializeModelShortcuts(rows)).toThrow("row 2:");
    }
  });

  test("labels use OS platform, including Mac browsers without any native bridge", () => {
    for (const number of MODEL_SHORTCUT_NUMBERS) {
      expect(modelShortcutLabel(number, "MacIntel")).toBe(
        `Command+Shift+${number}`,
      );
      for (const platform of ["Win32", "Linux x86_64", "", undefined, null]) {
        expect(modelShortcutLabel(number, platform)).toBe(
          `Ctrl+Shift+${number}`,
        );
      }
    }
  });
});
