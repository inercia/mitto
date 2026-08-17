import {
  createSessionUpdateScheduler,
  sessionHasLoadedMessages,
  sessionWasStreaming,
} from "./sessionUpdateScheduler.js";

function harness(activeSessionId = "active") {
  let state = {};
  let scheduled = null;
  let renders = 0;
  const scheduler = createSessionUpdateScheduler({
    getActiveSessionId: () => activeSessionId,
    setSessions: (update) => {
      state = update(state);
      renders += 1;
    },
    setTimeoutFn: (fn) => {
      scheduled = fn;
      return 1;
    },
    clearTimeoutFn: () => {
      scheduled = null;
    },
  });
  return {
    scheduler,
    state: () => state,
    renders: () => renders,
    runTimer: () => {
      const fn = scheduled;
      scheduled = null;
      fn?.();
    },
    setActive: (id) => {
      activeSessionId = id;
    },
  };
}

const append = (sessionId, value) => (state) => ({
  ...state,
  [sessionId]: [...(state[sessionId] || []), value],
});

describe("createSessionUpdateScheduler", () => {
  test("coalesces background updates into one state render", () => {
    const h = harness();
    h.scheduler.schedule("background", append("background", 1));
    h.scheduler.schedule("background", append("background", 2));
    expect(h.renders()).toBe(0);
    h.runTimer();
    expect(h.renders()).toBe(1);
    expect(h.state().background).toEqual([1, 2]);
  });

  test("keeps active-session updates immediate", () => {
    const h = harness();
    h.scheduler.schedule("active", append("active", 1));
    expect(h.renders()).toBe(1);
    expect(h.state().active).toEqual([1]);
  });

  test("applies queued chunks before an immediate terminal update", () => {
    const h = harness();
    h.scheduler.schedule("background", append("background", "chunk"));
    h.scheduler.applyImmediate("background", append("background", "complete"));
    expect(h.renders()).toBe(1);
    expect(h.state().background).toEqual(["chunk", "complete"]);
    h.runTimer();
    expect(h.renders()).toBe(1);
  });

  test("flushes a newly selected session without flushing other sessions", () => {
    const h = harness();
    h.scheduler.schedule("first", append("first", 1));
    h.scheduler.schedule("second", append("second", 2));
    h.scheduler.flushSession("first");
    expect(h.state()).toEqual({ first: [1] });
    h.runTimer();
    expect(h.state()).toEqual({ first: [1], second: [2] });
  });

  test("reports whether a session flush consumed pending content", () => {
    const h = harness();
    expect(h.scheduler.flushSession("background")).toBe(false);
    h.scheduler.schedule("background", append("background", 1));
    expect(h.scheduler.flushSession("background")).toBe(true);
    expect(h.scheduler.flushSession("background")).toBe(false);
  });

  test("makes a newly active session immediate and preserves its queued order", () => {
    const h = harness();
    h.scheduler.schedule("next", append("next", "queued"));
    h.setActive("next");
    h.scheduler.schedule("next", append("next", "active"));
    expect(h.renders()).toBe(1);
    expect(h.state().next).toEqual(["queued", "active"]);
  });

  test("dispose drops pending updates and cancels their timer", () => {
    const h = harness();
    h.scheduler.schedule("background", append("background", 1));
    h.scheduler.dispose();
    h.runTimer();
    expect(h.renders()).toBe(0);
    expect(h.state()).toEqual({});
  });
});

describe("scheduler/composer stale-state seams", () => {
  test("pending content proves a stale session ref was streaming", () => {
    expect(sessionWasStreaming({ isStreaming: false }, true)).toBe(true);
    expect(sessionWasStreaming({ isStreaming: true }, false)).toBe(true);
    expect(sessionWasStreaming({ isStreaming: false }, false)).toBe(false);
  });

  test("pending content proves messages are loading before state commits", () => {
    expect(sessionHasLoadedMessages({ messages: [] }, true)).toBe(true);
    expect(sessionHasLoadedMessages({ messages: [{}] }, false)).toBe(true);
    expect(sessionHasLoadedMessages({ messages: [] }, false)).toBe(false);
  });
});
