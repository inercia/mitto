/**
 * Unit tests for the explicit browser environment preset.
 *
 * Wired against a fake `globals` object (never the real ambient browser
 * globals) so the preset's wiring is verified in isolation from any actual
 * browser/happy-dom environment.
 */
import { browserEnv } from "./browser.js";

function makeFakeGlobals() {
  const store = new Map();
  const logCalls = [];
  return {
    globals: {
      localStorage: {
        getItem: (key) => (store.has(key) ? store.get(key) : null),
        setItem: (key, value) => store.set(key, String(value)),
        removeItem: (key) => store.delete(key),
      },
      console: {
        debug: (...args) => logCalls.push(["debug", ...args]),
        info: (...args) => logCalls.push(["info", ...args]),
        warn: (...args) => logCalls.push(["warn", ...args]),
        error: (...args) => logCalls.push(["error", ...args]),
      },
    },
    store,
    logCalls,
  };
}

describe("browserEnv", () => {
  test("wires storage to the given globals.localStorage", () => {
    const { globals, store } = makeFakeGlobals();
    const preset = browserEnv(globals);

    preset.storage.setItem("k", "v");
    expect(store.get("k")).toBe("v");
    expect(preset.storage.getItem("k")).toBe("v");

    preset.storage.removeItem("k");
    expect(preset.storage.getItem("k")).toBe(null);
  });

  test("getItem returns null for a missing key", () => {
    const { globals } = makeFakeGlobals();
    const preset = browserEnv(globals);
    expect(preset.storage.getItem("missing")).toBe(null);
  });

  test("wires logger to the given globals.console", () => {
    const { globals, logCalls } = makeFakeGlobals();
    const preset = browserEnv(globals);

    preset.logger.debug("d");
    preset.logger.info("i");
    preset.logger.warn("w");
    preset.logger.error("e");

    expect(logCalls).toEqual([
      ["debug", "d"],
      ["info", "i"],
      ["warn", "w"],
      ["error", "e"],
    ]);
  });

  test("returns a plain options-patch object (storage + logger only)", () => {
    const { globals } = makeFakeGlobals();
    const preset = browserEnv(globals);
    expect(Object.keys(preset).sort()).toEqual(["logger", "storage"]);
  });
});
