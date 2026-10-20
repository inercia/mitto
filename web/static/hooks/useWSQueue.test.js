/**
 * Tests for useWSQueue.js (mitto-7gta.17 slice S7 Test phase; updated
 * mitto-sus.11 for the queueStore migration).
 *
 * Covers the 4 authFetch/secureFetch->getSdkClient() call sites:
 * fetchQueueMessages (sessions.queue.list), deleteQueueMessage
 * (sessions.queue.remove), addToQueue (sessions.queue.add, including the
 * 409 queue-full branch), and moveQueueMessage (sessions.queue.move).
 * Mirrors the window.preact stub harness established by
 * useFolderPromptsConfig.test.js (slice S2).
 *
 * The queue data itself (messages/length) no longer lives in this hook's
 * own useState (mitto-sus.11) -- it's written directly to the module-level
 * stores/queueStore.js, so assertions read it back via that store's getters
 * instead of inspecting useState setter calls.
 */

import {
  describe,
  test,
  expect,
  jest,
  beforeEach,
} from "../utils/testing/testGlobals.js";
import {
  getMessages,
  getLength,
  _resetQueueStoreForTests,
} from "../stores/queueStore.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}
// Every migrated handler here that issues a state-changing request needs a
// CSRF token; pre-seed the cookie so browserCookieAuth's authorize() never
// needs its own network round trip (the test-local fetch mocks below only
// shape the endpoint under test).
global.document.cookie = "mitto_csrf=test-token";

let currentEffects = [];
let hookSlots = [];
let hookCursor = 0;
let effectSlots = [];
let effectCursor = 0;
let pendingEffects = [];
const sameDeps = (a, b) =>
  a &&
  b &&
  a.length === b.length &&
  a.every((value, i) => Object.is(value, b[i]));
window.preact = {
  useEffect: (cb, deps) => {
    const index = effectCursor++;
    const previous = effectSlots[index];
    const effect = { cb, deps, previous, index };
    pendingEffects.push(effect);
    currentEffects.push(effect);
  },
  useCallback: (fn, deps) => {
    const index = hookCursor++;
    const previous = hookSlots[index];
    if (previous && sameDeps(previous.deps, deps)) return previous.value;
    hookSlots[index] = { deps, value: fn };
    return fn;
  },
  useRef: (initial) => {
    const index = hookCursor++;
    if (!hookSlots[index]) hookSlots[index] = { value: { current: initial } };
    return hookSlots[index].value;
  },
};

beforeEach(() => {
  _resetQueueStoreForTests();
  hookSlots = [];
  hookCursor = 0;
  effectSlots = [];
  effectCursor = 0;
  pendingEffects = [];
});

function jsonResponse(data, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: {
      get: (name) =>
        name.toLowerCase() === "content-type" ? "application/json" : null,
    },
    text: () => Promise.resolve(JSON.stringify(data)),
    json: () => Promise.resolve(data),
  };
}

async function loadHook() {
  currentEffects = [];
  const mod = await import("./useWSQueue.js");
  const render = (...args) => {
    hookCursor = 0;
    effectCursor = 0;
    currentEffects = [];
    pendingEffects = [];
    const result = mod.useWSQueue(...args);
    return result;
  };
  const flushEffects = () => {
    for (const effect of pendingEffects) {
      const prior = effectSlots[effect.index];
      if (prior && !sameDeps(prior.deps, effect.deps)) prior.cleanup?.();
      if (!prior || !sameDeps(prior.deps, effect.deps)) {
        effectSlots[effect.index] = {
          deps: effect.deps,
          cleanup: effect.cb(),
        };
      }
    }
    pendingEffects = [];
  };
  const unmount = () => effectSlots.forEach((effect) => effect?.cleanup?.());
  return {
    useWSQueue: (...args) => render(...args),
    render,
    flushEffects,
    unmount,
    get effects() {
      return currentEffects;
    },
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("useWSQueue — fetchQueueMessages", () => {
  test("no activeSessionId: no-ops without fetching", async () => {
    global.fetch = jest.fn();
    const { useWSQueue } = await loadHook();
    const { fetchQueueMessages } = useWSQueue(null);
    await fetchQueueMessages();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("success: GETs the queue and stores messages/count", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ messages: [{ id: "m1" }], count: 1 })),
    );
    const { useWSQueue } = await loadHook();
    const { fetchQueueMessages } = useWSQueue("sess-1");
    await fetchQueueMessages();
    const [url] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/sessions/sess-1/queue");
    expect(getMessages("sess-1")).toEqual([{ id: "m1" }]);
    expect(getLength("sess-1")).toBe(1);
  });

  test("a network failure is swallowed (logged, no throw, no state change)", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("offline")));
    const { useWSQueue } = await loadHook();
    const { fetchQueueMessages } = useWSQueue("sess-1");
    await expect(fetchQueueMessages()).resolves.toBeUndefined();
    expect(getMessages("sess-1")).toEqual([]);
  });

  test("load-on-session-change effect fires fetchQueueMessages", async () => {
    const harness = await loadHook();
    const { useWSQueue } = harness;
    useWSQueue("sess-1");
    expect(harness.effects).toHaveLength(2);
    expect(harness.effects[0].deps).toEqual(["sess-1", expect.any(Function)]);
  });

  test("current 404 clears the queue, reports missing once, and makes later refreshes terminal", async () => {
    global.fetch = jest.fn(() => Promise.resolve(jsonResponse({}, 404)));
    const onSessionMissing = jest.fn();
    const { useWSQueue } = await loadHook();
    const queue = useWSQueue("sess-1", { onSessionMissing });
    await queue.fetchQueueMessages();
    await queue.fetchQueueMessages();
    expect(global.fetch).toHaveBeenCalledTimes(1);
    expect(onSessionMissing).toHaveBeenCalledTimes(1);
    expect(onSessionMissing).toHaveBeenCalledWith("sess-1");
    expect(getMessages("sess-1")).toEqual([]);
    expect(getLength("sess-1")).toBe(0);
  });

  test("404 does not log, while a current server error keeps the existing log path", async () => {
    const error = jest.spyOn(console, "error").mockImplementation(() => {});
    global.fetch = jest.fn(() => Promise.resolve(jsonResponse({}, 404)));
    const { useWSQueue } = await loadHook();
    await useWSQueue("missing").fetchQueueMessages();
    expect(error).not.toHaveBeenCalled();

    global.fetch = jest.fn(() => Promise.resolve(jsonResponse({}, 500)));
    await useWSQueue("healthy").fetchQueueMessages();
    expect(error).toHaveBeenCalledTimes(1);
    error.mockRestore();
  });

  test("superseding a queue read aborts it and ignores its late response", async () => {
    const oldRead = deferred();
    const newRead = deferred();
    const signals = [];
    global.fetch = jest.fn((_url, options) => {
      signals.push(options.signal);
      return signals.length === 1 ? oldRead.promise : newRead.promise;
    });
    const { render } = await loadHook();
    const first = render("sess-1");
    const pending = first.fetchQueueMessages();
    await Promise.resolve();
    await Promise.resolve();
    const second = render("sess-1");
    const latest = second.fetchQueueMessages();
    expect(signals[0].aborted).toBe(true);
    newRead.resolve(jsonResponse({ messages: [{ id: "new" }], count: 1 }));
    await latest;
    oldRead.resolve(jsonResponse({ messages: [{ id: "old" }], count: 1 }));
    await pending;
    expect(getMessages("sess-1")).toEqual([{ id: "new" }]);
  });

  test("selection generations fence A→B→A stale results and deletion aborts its read", async () => {
    const firstA = deferred();
    const secondA = deferred();
    global.fetch = jest
      .fn()
      .mockImplementationOnce(() => firstA.promise)
      .mockImplementationOnce(() => secondA.promise);
    const { render } = await loadHook();
    const a1 = render("sess-A");
    const oldRequest = a1.fetchQueueMessages();
    render("sess-B");
    const a2 = render("sess-A");
    const currentRequest = a2.fetchQueueMessages();
    firstA.resolve(jsonResponse({ messages: [{ id: "stale" }], count: 1 }));
    await oldRequest;
    expect(getMessages("sess-A")).toEqual([]);
    secondA.resolve(jsonResponse({ messages: [{ id: "current" }], count: 1 }));
    await currentRequest;
    expect(getMessages("sess-A")).toEqual([{ id: "current" }]);

    const deleting = deferred();
    global.fetch = jest.fn((_url, options) => {
      deleting.signal = options.signal;
      return deleting.promise;
    });
    const queue = render("sess-C");
    const pending = queue.fetchQueueMessages();
    await Promise.resolve();
    await Promise.resolve();
    queue.invalidateQueueSession("sess-C");
    expect(deleting.signal.aborted).toBe(true);
    deleting.resolve(jsonResponse({ messages: [{ id: "late" }], count: 1 }));
    await pending;
    expect(getMessages("sess-C")).toEqual([]);
  });

  test("unmount cleanup aborts the active read", async () => {
    const pendingResponse = deferred();
    let signal;
    global.fetch = jest.fn((_url, options) => {
      signal = options.signal;
      return pendingResponse.promise;
    });
    const { render, flushEffects, unmount } = await loadHook();
    render("sess-1");
    flushEffects();
    await Promise.resolve();
    await Promise.resolve();
    expect(signal.aborted).toBe(false);
    unmount();
    expect(signal.aborted).toBe(true);
  });
});

describe("useWSQueue — deleteQueueMessage", () => {
  test("success: DELETEs the message, refetches, and returns true", async () => {
    const calls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      calls.push(method);
      if (method === "DELETE") return Promise.resolve(jsonResponse(null, 204));
      return Promise.resolve(jsonResponse({ messages: [], count: 0 }));
    });
    const { useWSQueue } = await loadHook();
    const { deleteQueueMessage } = useWSQueue("sess-1");
    const ok = await deleteQueueMessage("m1");
    expect(ok).toBe(true);
    expect(calls).toEqual(["DELETE", "GET"]);
  });

  test("no messageId: returns false without fetching", async () => {
    global.fetch = jest.fn();
    const { useWSQueue } = await loadHook();
    const { deleteQueueMessage } = useWSQueue("sess-1");
    expect(await deleteQueueMessage(null)).toBe(false);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("failure: returns false, does not throw", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: "boom" }, 500)),
    );
    const { useWSQueue } = await loadHook();
    const { deleteQueueMessage } = useWSQueue("sess-1");
    expect(await deleteQueueMessage("m1")).toBe(false);
  });
});

describe("useWSQueue — addToQueue", () => {
  test("success: POSTs the body, refetches, and returns the new message id", async () => {
    const calls = [];
    global.fetch = jest.fn((url, opts) => {
      const method = (opts && opts.method) || "GET";
      calls.push({ url: String(url), method, body: opts && opts.body });
      if (method === "POST")
        return Promise.resolve(jsonResponse({ id: "new-1" }, 201));
      return Promise.resolve(jsonResponse({ messages: [], count: 1 }));
    });
    const { useWSQueue } = await loadHook();
    const { addToQueue } = useWSQueue("sess-1");
    const result = await addToQueue("hello", ["img-1"], [], {
      promptName: "P1",
      arguments: { A: "1" },
    });
    expect(result).toEqual({ success: true, messageId: "new-1" });
    const postCall = calls.find((c) => c.method === "POST");
    expect(postCall.url).toContain("/api/sessions/sess-1/queue");
    const body = JSON.parse(postCall.body);
    expect(body).toEqual({
      message: "hello",
      image_ids: ["img-1"],
      file_ids: [],
      prompt_name: "P1",
      arguments: { A: "1" },
    });
  });

  test("no message and no promptName: returns {success:false} without fetching", async () => {
    global.fetch = jest.fn();
    const { useWSQueue } = await loadHook();
    const { addToQueue } = useWSQueue("sess-1");
    const result = await addToQueue("  ");
    expect(result).toEqual({ success: false });
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("409 queue-full: returns the structured error without a console.error path re-fetching", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse(
          { error: { code: "queue_full", message: "Queue is full" } },
          409,
        ),
      ),
    );
    const { useWSQueue } = await loadHook();
    const { addToQueue } = useWSQueue("sess-1");
    const result = await addToQueue("hello");
    expect(result.success).toBe(false);
    expect(result.error).toBe("queue_full");
    expect(result.message).toBe("Queue is full");
  });

  test("other failure: returns a generic request_failed error", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ error: { message: "boom" } }, 500)),
    );
    const { useWSQueue } = await loadHook();
    const { addToQueue } = useWSQueue("sess-1");
    const result = await addToQueue("hello");
    expect(result).toEqual({ success: false, error: "request_failed" });
  });
});

describe("useWSQueue — moveQueueMessage", () => {
  test("success: POSTs {direction} and updates messages/count from the response", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(
        jsonResponse({ messages: [{ id: "m1" }, { id: "m2" }], count: 2 }),
      ),
    );
    const { useWSQueue } = await loadHook();
    const { moveQueueMessage } = useWSQueue("sess-1");
    const ok = await moveQueueMessage("m1", "up");
    expect(ok).toBe(true);
    const [url, opts] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/sessions/sess-1/queue/m1/move");
    expect(JSON.parse(opts.body)).toEqual({ direction: "up" });
    expect(getMessages("sess-1")).toEqual([{ id: "m1" }, { id: "m2" }]);
    expect(getLength("sess-1")).toBe(2);
  });

  test("invalid direction: returns false without fetching", async () => {
    global.fetch = jest.fn();
    const { useWSQueue } = await loadHook();
    const { moveQueueMessage } = useWSQueue("sess-1");
    expect(await moveQueueMessage("m1", "sideways")).toBe(false);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("failure: returns false, does not throw", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("offline")));
    const { useWSQueue } = await loadHook();
    const { moveQueueMessage } = useWSQueue("sess-1");
    expect(await moveQueueMessage("m1", "up")).toBe(false);
  });
});
