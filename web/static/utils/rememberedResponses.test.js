import { describe, expect, test } from "bun:test";
import {
  applyRememberedValues,
  contextualKeys,
  resolveRememberResponses,
} from "./rememberedResponses.js";

describe("contextualKeys", () => {
  test("counts only non-empty initial values as context", () => {
    const keys = contextualKeys({ a: "x", b: "", c: null, d: "0" });
    expect([...keys].sort()).toEqual(["a", "d"]);
  });
});

describe("applyRememberedValues", () => {
  test("fills keys that are neither contextual nor touched", () => {
    const prev = { a: "", b: "" };
    const next = applyRememberedValues(prev, { a: "1", b: "2" }, {
      contextKeys: new Set(),
      touchedKeys: new Set(),
    });
    expect(next).toEqual({ a: "1", b: "2" });
    expect(prev).toEqual({ a: "", b: "" });
  });

  test("never overrides contextual values", () => {
    const prev = { a: "ctx" };
    const next = applyRememberedValues(prev, { a: "old" }, {
      contextKeys: new Set(["a"]),
      touchedKeys: new Set(),
    });
    expect(next).toBe(prev);
  });

  test("never overrides fields the user touched", () => {
    const prev = { a: "typed" };
    const next = applyRememberedValues(prev, { a: "old" }, {
      contextKeys: new Set(),
      touchedKeys: new Set(["a"]),
    });
    expect(next.a).toBe("typed");
  });

  test("preserves empty strings and literal false/0 remembered values", () => {
    const next = applyRememberedValues({}, { a: "", b: "false", c: "0" }, {
      contextKeys: new Set(),
      touchedKeys: new Set(),
    });
    expect(next).toEqual({ a: "", b: "false", c: "0" });
  });
});

describe("resolveRememberResponses", () => {
  test("undefined when the control is not shown", () => {
    expect(
      resolveRememberResponses({ showControl: false, touched: true, checked: true }),
    ).toBeUndefined();
  });

  test("user toggle wins over saved preference", () => {
    expect(
      resolveRememberResponses({
        showControl: true,
        touched: true,
        checked: false,
        savedPreference: true,
      }),
    ).toBe(false);
  });

  test("untouched uses saved preference", () => {
    expect(
      resolveRememberResponses({ showControl: true, touched: false, checked: false, savedPreference: true }),
    ).toBe(true);
    expect(
      resolveRememberResponses({ showControl: true, touched: false, checked: false, savedPreference: false }),
    ).toBe(false);
  });

  test("untouched without saved preference is undefined (not an opt-out)", () => {
    expect(
      resolveRememberResponses({ showControl: true, touched: false, checked: false }),
    ).toBeUndefined();
  });
});
