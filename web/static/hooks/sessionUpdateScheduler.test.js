import {
  createSessionUpdateScheduler,
  sessionHasLoadedMessages,
  sessionWasStreaming,
} from "./sessionUpdateScheduler.js";

// Timers and animation frames are tracked as independent id -> fn maps (not
// a single shared slot) because the frame-paced active queue (mitto-sus.3)
// can have a background setTimeout, a frame-fallback setTimeout, and a
// requestAnimationFrame callback all pending at once. runTimer()/runFrame()
// each fire the oldest pending entry of their kind, mirroring "whichever
// fires first" real-browser semantics; cancel callbacks remove by id so a
// flush that clears one via clearFrameTimers() is reflected here too.
function harness(activeSessionId = "active") {
  let state = {};
  let renders = 0;
  let nextTimerId = 1;
  let nextFrameId = 1;
  const timers = new Map();
  const frames = new Map();
  const runOldest = (map) => {
    const firstKey = map.keys().next().value;
    if (firstKey === undefined) return false;
    const fn = map.get(firstKey);
    map.delete(firstKey);
    fn();
    return true;
  };
  const scheduler = createSessionUpdateScheduler({
    getActiveSessionId: () => activeSessionId,
    setSessions: (update) => {
      state = update(state);
      renders += 1;
    },
    setTimeoutFn: (fn) => {
      const id = nextTimerId++;
      timers.set(id, fn);
      return id;
    },
    clearTimeoutFn: (id) => {
      timers.delete(id);
    },
    requestFrameFn: (fn) => {
      const id = nextFrameId++;
      frames.set(id, fn);
      return id;
    },
    cancelFrameFn: (id) => {
      frames.delete(id);
    },
  });
  return {
    scheduler,
    state: () => state,
    renders: () => renders,
    pendingTimers: () => timers.size,
    pendingFrames: () => frames.size,
    runTimer: () => runOldest(timers),
    runFrame: () => runOldest(frames),
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

describe("createSessionUpdateScheduler: frame-paced active queue (mitto-sus.3)", () => {
  test("the first chunk of a burst still commits synchronously (no wait for a frame)", () => {
    const h = harness();
    h.scheduler.schedule("active", append("active", 1));
    // First-content promptness: no frame/timer needed for the first chunk.
    expect(h.renders()).toBe(1);
    expect(h.state().active).toEqual([1]);
    // A coalescing window is armed for whatever arrives next.
    expect(h.pendingFrames()).toBe(1);
  });

  test("coalesces multiple active-session updates within one animation frame into a single render", () => {
    const h = harness();
    h.scheduler.schedule("active", append("active", 1)); // sync commit, arms the frame
    h.scheduler.schedule("active", append("active", 2)); // queued
    h.scheduler.schedule("active", append("active", 3)); // queued
    expect(h.renders()).toBe(1);
    h.runFrame();
    expect(h.renders()).toBe(2);
    expect(h.state().active).toEqual([1, 2, 3]);
    // The window closes after a flush; nothing left pending.
    expect(h.pendingFrames()).toBe(0);
    expect(h.pendingTimers()).toBe(0);
  });

  test("preserves cross-type ordering (message/thought/tool_call/tool_update) within one frame flush", () => {
    const h = harness();
    h.scheduler.schedule("active", append("active", "agent_message"));
    h.scheduler.schedule("active", append("active", "agent_thought"));
    h.scheduler.schedule("active", append("active", "tool_call"));
    h.scheduler.schedule("active", append("active", "tool_update"));
    h.runFrame();
    expect(h.state().active).toEqual([
      "agent_message",
      "agent_thought",
      "tool_call",
      "tool_update",
    ]);
  });

  test("applyImmediate drains a queued active frame before applying its own terminal update", () => {
    const h = harness();
    h.scheduler.schedule("active", append("active", 1)); // sync commit, arms the frame
    h.scheduler.schedule("active", append("active", 2)); // queued
    h.scheduler.applyImmediate("active", append("active", "complete"));
    expect(h.state().active).toEqual([1, 2, "complete"]);
    expect(h.renders()).toBe(2);
    // The frame + fallback timer were cancelled as part of the drain.
    expect(h.pendingFrames()).toBe(0);
    expect(h.pendingTimers()).toBe(0);
    // A stray late frame must not double-apply already-drained content.
    h.runFrame();
    expect(h.renders()).toBe(2);
  });

  test("flushSession drains a queued active frame for that session", () => {
    const h = harness();
    h.scheduler.schedule("active", append("active", 1));
    h.scheduler.schedule("active", append("active", 2));
    expect(h.scheduler.flushSession("active")).toBe(true);
    expect(h.state().active).toEqual([1, 2]);
    expect(h.renders()).toBe(2);
    expect(h.pendingFrames()).toBe(0);
    expect(h.pendingTimers()).toBe(0);
  });

  test("a newly active session drains its background queue immediately, then frame-paces further chunks", () => {
    const h = harness();
    h.scheduler.schedule("next", append("next", "queued")); // background (not yet active)
    h.setActive("next");
    h.scheduler.schedule("next", append("next", "active1")); // idle -> busy: sync commit
    expect(h.renders()).toBe(1);
    expect(h.state().next).toEqual(["queued", "active1"]);
    h.scheduler.schedule("next", append("next", "active2")); // window open: queued
    expect(h.renders()).toBe(1);
    h.runFrame();
    expect(h.renders()).toBe(2);
    expect(h.state().next).toEqual(["queued", "active1", "active2"]);
  });

  test("falls back to the timeout when no animation frame fires (hidden/throttled tab)", () => {
    const h = harness();
    h.scheduler.schedule("active", append("active", 1)); // sync commit, arms frame + fallback
    h.scheduler.schedule("active", append("active", 2)); // queued
    expect(h.renders()).toBe(1);
    // Fallback timer fires instead of rAF (e.g. hidden tab throttling rAF).
    h.runTimer();
    expect(h.renders()).toBe(2);
    expect(h.state().active).toEqual([1, 2]);
    // The now-redundant frame request was cancelled by the fallback firing.
    expect(h.pendingFrames()).toBe(0);
  });

  test("dispose mid-window cancels the frame/fallback timer and drops queued active updates", () => {
    const h = harness();
    h.scheduler.schedule("active", append("active", 1)); // sync commit, arms frame + fallback
    h.scheduler.schedule("active", append("active", 2)); // queued
    expect(h.renders()).toBe(1);
    h.scheduler.dispose();
    expect(h.pendingFrames()).toBe(0);
    expect(h.pendingTimers()).toBe(0);
    // Neither a stray frame nor a stray timer may commit after dispose.
    h.runFrame();
    h.runTimer();
    expect(h.renders()).toBe(1);
    expect(h.state().active).toEqual([1]);
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
