import { describe, expect, test } from "bun:test";
import {
  hiddenModelsToRows,
  isHiddenModelErrorCurrent,
  mapHiddenModelError,
  serializeHiddenModels,
} from "./hiddenModels.js";

describe("hiddenModelsToRows", () => {
  test("absent → no rows", () => {
    expect(hiddenModelsToRows(undefined)).toEqual([]);
    expect(hiddenModelsToRows(null)).toEqual([]);
  });
  test("copies verbatim", () => {
    const src = [" a, b ", ".*Terra.*"];
    const rows = hiddenModelsToRows(src);
    expect(rows).toEqual(src);
    expect(rows).not.toBe(src);
  });
});

describe("serializeHiddenModels", () => {
  test("empty/all-blank yields explicit []", () => {
    expect(serializeHiddenModels([])).toEqual({ rules: [], rowIndexes: [] });
    expect(serializeHiddenModels(["", "  ", "\t"])).toEqual({
      rules: [],
      rowIndexes: [],
    });
    expect(serializeHiddenModels(undefined)).toEqual({
      rules: [],
      rowIndexes: [],
    });
  });
  test("preserves nonblank text exactly and maps indices", () => {
    const { rules, rowIndexes } = serializeHiddenModels([
      "",
      " a,b ",
      "  ",
      "(?i).*Terra.*",
    ]);
    expect(rules).toEqual([" a,b ", "(?i).*Terra.*"]);
    expect(rowIndexes).toEqual([1, 3]);
  });
});

describe("mapHiddenModelError", () => {
  const err = (field, message) => ({
    message: "bad",
    details: { field, message },
  });
  const rowIndexes = [1, 3];
  const rules = ["x(", "ok"];

  test("maps through serialized index to draft row", () => {
    expect(
      mapHiddenModelError(
        err("acp_servers[2].hidden_models[0]", "boom"),
        2,
        rowIndexes,
        rules,
      ),
    ).toEqual({ rowIndex: 1, message: "boom", rowValue: "x(" });
  });
  test("falls back to err.message", () => {
    const r = mapHiddenModelError(
      err("acp_servers[0].hidden_models[1]"),
      0,
      rowIndexes,
      rules,
    );
    expect(r.rowIndex).toBe(3);
    expect(r.message).toBe("bad");
  });
  test("ignores other servers, bad fields, out-of-range, no details", () => {
    expect(
      mapHiddenModelError(
        err("acp_servers[1].hidden_models[0]"),
        2,
        rowIndexes,
      ),
    ).toBeNull();
    expect(mapHiddenModelError(err("name"), 0, rowIndexes)).toBeNull();
    expect(
      mapHiddenModelError(
        err("acp_servers[0].hidden_models[5]"),
        0,
        rowIndexes,
      ),
    ).toBeNull();
    expect(mapHiddenModelError({ message: "x" }, 0, rowIndexes)).toBeNull();
    expect(mapHiddenModelError(null, 0, rowIndexes)).toBeNull();
  });
});

describe("isHiddenModelErrorCurrent", () => {
  const e = { rowIndex: 1, message: "m", rowValue: "x(" };
  test("current only while row text is unchanged", () => {
    expect(isHiddenModelErrorCurrent(e, ["", "x("])).toBe(true);
    expect(isHiddenModelErrorCurrent(e, ["", "x()"])).toBe(false);
    expect(isHiddenModelErrorCurrent(e, [""])).toBe(false);
    expect(isHiddenModelErrorCurrent(null, ["x("])).toBe(false);
  });
});
