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

describe("U6 acceptance edge cases (mitto-69t.5)", () => {
  test("serialize preserves whitespace and literal commas/regex bytes", () => {
    const rows = ["  a b ", "a,b", ".*Terra.*", "(?i)x|y", "   ", "", "\t"];
    const out = serializeHiddenModels(rows);
    expect(out.rules).toEqual(["  a b ", "a,b", ".*Terra.*", "(?i)x|y"]);
    expect(out.rowIndexes).toEqual([0, 1, 2, 3]);
  });

  test("serialize skips non-strings, returns fresh array, no mutation", () => {
    const rows = [null, 5, "x", undefined];
    const copy = [...rows];
    const out = serializeHiddenModels(rows);
    expect(out.rules).toEqual(["x"]);
    expect(out.rowIndexes).toEqual([2]);
    expect(rows).toEqual(copy);
    expect(out.rules).not.toBe(rows);
  });

  test("hiddenModelsToRows tolerates non-array input", () => {
    expect(hiddenModelsToRows(null)).toEqual([]);
    expect(hiddenModelsToRows("x")).toEqual([]);
    const src = ["a"];
    const rows = hiddenModelsToRows(src);
    expect(rows).toEqual(["a"]);
    expect(rows).not.toBe(src);
  });

  test("malformed field strings map to null", () => {
    for (const field of [
      "acp_servers[a].hidden_models[0]",
      "acp_servers[0].hidden_models[-1]",
      "acp_servers[0].hidden_models[0]x",
      "acp_servers[0].hidden_models[]",
      "acp_servers[0].hidden_models[0].extra",
    ]) {
      const err = { message: "m", details: { field, message: "bad" } };
      expect(mapHiddenModelError(err, 0, [0], ["x"])).toBeNull();
    }
  });

  test("message falls back and general message is untouched", () => {
    const field = "acp_servers[0].hidden_models[0]";
    const err = { message: "general", details: { field, message: "" } };
    const m = mapHiddenModelError(err, 0, [0], ["x"]);
    expect(m.message).toBe("general");
    expect(err.message).toBe("general");
    const e2 = { details: { field } };
    expect(mapHiddenModelError(e2, 0, [0], ["x"]).message).toBe(
      "Invalid regular expression",
    );
  });

  test("multi-server isolation and stale detection without rules", () => {
    const err = {
      message: "m",
      details: { field: "acp_servers[1].hidden_models[0]", message: "bad" },
    };
    expect(mapHiddenModelError(err, 0, [0], ["x"])).toBeNull();
    const hit = mapHiddenModelError(err, 1, [2], ["(x"]);
    expect(hit.rowIndex).toBe(2);
    expect(hit.rowValue).toBe("(x");
    const noRules = mapHiddenModelError(err, 1, [2]);
    expect(isHiddenModelErrorCurrent(noRules, ["a", "b", "(x"])).toBe(false);
    expect(isHiddenModelErrorCurrent(hit, ["a", "b", "(x"])).toBe(true);
    expect(isHiddenModelErrorCurrent(hit, ["a", "b", "(y"])).toBe(false);
  });
});
