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
