/**
 * Unit tests for useRenderCounter.js (mitto-sus.7).
 *
 * The hook has no useState/useEffect dependency (it is a plain function call
 * safe to invoke unconditionally, including after an early return), so it is
 * imported and called directly -- no window.preact stubbing needed.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
} from "../utils/testing/testGlobals.js";

import { useRenderCounter } from "./useRenderCounter.js";
import { getRenderCounts, resetRenderCounts } from "../utils/renderCounters.js";
import { _resetPerfEnabledCacheForTests } from "../utils/perfMarks.js";

function resetPerfState() {
  _resetPerfEnabledCacheForTests();
  delete window.__mittoPerf;
  resetRenderCounts();
}

beforeEach(() => {
  resetPerfState();
});

afterEach(() => {
  resetPerfState();
});

describe("useRenderCounter (mitto-sus.7)", () => {
  test("is a no-op when perf instrumentation is disabled", () => {
    useRenderCounter("App");
    expect(getRenderCounts()).toEqual({});
  });

  test("bumps the named domain's counter once per call, simulating one render each", () => {
    window.__mittoPerf = true;

    useRenderCounter("MessageList");
    useRenderCounter("MessageList");
    useRenderCounter("ChatInput");

    expect(getRenderCounts()).toEqual({ MessageList: 2, ChatInput: 1 });
  });

  test("each named render domain accumulates independently across many calls", () => {
    window.__mittoPerf = true;
    const domains = ["App", "SessionList", "MessageList", "ChatInput", "ToastContainer"];

    for (const domain of domains) useRenderCounter(domain);
    for (const domain of domains) useRenderCounter(domain);

    const counts = getRenderCounts();
    for (const domain of domains) expect(counts[domain]).toBe(2);
  });
});
