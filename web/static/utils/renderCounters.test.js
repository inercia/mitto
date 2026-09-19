/**
 * Unit tests for the mitto-sus.7 dev-only render-count instrumentation
 * (renderCounters.js). Follows perfMarks.test.js's window-global reset
 * pattern for the shared isPerfEnabled() gate.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
} from "../utils/testing/testGlobals.js";

import {
  bumpRender,
  getRenderCounts,
  resetRenderCounts,
  installRenderCountsReset,
} from "./renderCounters.js";
import { _resetPerfEnabledCacheForTests } from "./perfMarks.js";

function resetPerfState() {
  _resetPerfEnabledCacheForTests();
  delete window.__mittoPerf;
  // resetRenderCounts() itself unconditionally sets window.__mittoRenderCounts
  // = {} (it is a scenario-boundary reset, not gated on isPerfEnabled()), so
  // explicitly delete it afterwards to get a clean "instrumentation never
  // touched window" starting point for the disabled-by-default assertions.
  resetRenderCounts();
  delete window.__mittoRenderCounts;
  delete window.__mittoResetRenderCounts;
}

beforeEach(() => {
  resetPerfState();
});

afterEach(() => {
  resetPerfState();
});

describe("renderCounters (mitto-sus.7) > disabled by default", () => {
  test("bumpRender is a no-op when perf instrumentation is disabled", () => {
    bumpRender("App");
    expect(getRenderCounts()).toEqual({});
    expect(window.__mittoRenderCounts).toBeUndefined();
  });
});

describe("renderCounters (mitto-sus.7) > enabled via window.__mittoPerf", () => {
  test("bumpRender increments the named region's count", () => {
    window.__mittoPerf = true;
    bumpRender("SessionList");
    bumpRender("SessionList");
    bumpRender("MessageList");

    expect(getRenderCounts()).toEqual({ SessionList: 2, MessageList: 1 });
  });

  test("mirrors the running snapshot onto window.__mittoRenderCounts after every bump", () => {
    window.__mittoPerf = true;
    bumpRender("ChatInput");
    expect(window.__mittoRenderCounts).toEqual({ ChatInput: 1 });

    bumpRender("ChatInput");
    expect(window.__mittoRenderCounts).toEqual({ ChatInput: 2 });
  });

  test("regions are tracked independently -- bumping one never touches another", () => {
    window.__mittoPerf = true;
    bumpRender("App");
    bumpRender("ToastContainer");
    bumpRender("App");

    expect(getRenderCounts()).toEqual({ App: 2, ToastContainer: 1 });
  });

  test("resetRenderCounts clears both the internal map and the window mirror", () => {
    window.__mittoPerf = true;
    bumpRender("App");
    resetRenderCounts();

    expect(getRenderCounts()).toEqual({});
    expect(window.__mittoRenderCounts).toEqual({});
  });
});

describe("renderCounters (mitto-sus.7) > robustness", () => {
  test("never throws even for an unusual region name", () => {
    window.__mittoPerf = true;
    expect(() => bumpRender(undefined)).not.toThrow();
    expect(() => bumpRender(42)).not.toThrow();
  });

  test("getRenderCounts returns a plain object snapshot, not a live Map reference", () => {
    window.__mittoPerf = true;
    bumpRender("App");
    const snapshot = getRenderCounts();
    bumpRender("App");

    expect(snapshot).toEqual({ App: 1 });
    expect(getRenderCounts()).toEqual({ App: 2 });
  });
});

// =============================================================================
// installRenderCountsReset (mitto-b1k): exposes window.__mittoResetRenderCounts
// so a Playwright render-isolation spec can reset counters mid-run (after
// initial mount settles, before driving the scenario under measurement)
// without a full page reload. Mirrors installPerfBuffer()'s bootstrap gate.
// =============================================================================

describe("installRenderCountsReset (mitto-b1k)", () => {
  test("is a no-op when perf instrumentation is disabled (default)", () => {
    installRenderCountsReset();
    expect(window.__mittoResetRenderCounts).toBeUndefined();
  });

  test("exposes window.__mittoResetRenderCounts when perf is enabled", () => {
    window.__mittoPerf = true;
    installRenderCountsReset();
    expect(typeof window.__mittoResetRenderCounts).toBe("function");
  });

  test("the exposed function actually resets counts (is resetRenderCounts itself)", () => {
    window.__mittoPerf = true;
    bumpRender("SessionList");
    installRenderCountsReset();

    expect(getRenderCounts()).toEqual({ SessionList: 1 });
    window.__mittoResetRenderCounts();
    expect(getRenderCounts()).toEqual({});
    expect(window.__mittoRenderCounts).toEqual({});
  });

  test("is idempotent -- calling it more than once does not throw or double-wrap", () => {
    window.__mittoPerf = true;
    installRenderCountsReset();
    const first = window.__mittoResetRenderCounts;
    expect(() => installRenderCountsReset()).not.toThrow();
    expect(window.__mittoResetRenderCounts).toBe(first);
  });

  test("does not retroactively expose the hook if perf is enabled only after the call", () => {
    // Bootstrap order in app.js calls installRenderCountsReset() once, at
    // module scope, gated on the perf flag at that instant -- it does not
    // poll. Enabling perf afterwards must not magically populate the window
    // global (matches installPerfBuffer()'s one-shot semantics).
    installRenderCountsReset();
    expect(window.__mittoResetRenderCounts).toBeUndefined();
    window.__mittoPerf = true;
    expect(window.__mittoResetRenderCounts).toBeUndefined();
  });
});
