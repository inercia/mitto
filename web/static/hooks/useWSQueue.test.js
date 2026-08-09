/**
 * Tests for useWSQueue.js (mitto-7gta.17 slice S7 Test phase).
 *
 * Covers the 4 authFetch/secureFetch->getSdkClient() call sites migrated in
 * the Implementation phase: fetchQueueMessages (sessions.queue.list),
 * deleteQueueMessage (sessions.queue.remove), addToQueue
 * (sessions.queue.add, including the 409 queue-full branch), and
 * moveQueueMessage (sessions.queue.move). Mirrors the window.preact stub
 * harness established by useFolderPromptsConfig.test.js (slice S2).
 */

import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

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

let currentSetters = [];
let currentEffects = [];
window.preact = {
  useState: (initial) => {
    const setter = jest.fn();
    currentSetters.push(setter);
    return [initial, setter];
  },
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
  useCallback: (fn) => fn,
};

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
  currentSetters = [];
  currentEffects = [];
  const mod = await import("./useWSQueue.js");
  return {
    useWSQueue: mod.useWSQueue,
    setters: currentSetters,
    effects: currentEffects,
  };
}

const IDX = {
  setQueueLength: 0,
  setQueueMessages: 1,
  setQueueConfig: 2,
};

describe("useWSQueue — fetchQueueMessages", () => {
  test("no activeSessionId: clears messages without fetching", async () => {
    global.fetch = jest.fn();
    const { useWSQueue, setters } = await loadHook();
    const { fetchQueueMessages } = useWSQueue(null);
    await fetchQueueMessages();
    expect(global.fetch).not.toHaveBeenCalled();
    expect(setters[IDX.setQueueMessages]).toHaveBeenCalledWith([]);
  });

  test("success: GETs the queue and stores messages/count", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve(jsonResponse({ messages: [{ id: "m1" }], count: 1 })),
    );
    const { useWSQueue, setters } = await loadHook();
    const { fetchQueueMessages } = useWSQueue("sess-1");
    await fetchQueueMessages();
    const [url] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/sessions/sess-1/queue");
    expect(setters[IDX.setQueueMessages]).toHaveBeenCalledWith([{ id: "m1" }]);
    expect(setters[IDX.setQueueLength]).toHaveBeenCalledWith(1);
  });

  test("a network failure is swallowed (logged, no throw, no state change)", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("offline")));
    const { useWSQueue, setters } = await loadHook();
    const { fetchQueueMessages } = useWSQueue("sess-1");
    await expect(fetchQueueMessages()).resolves.toBeUndefined();
    expect(setters[IDX.setQueueMessages]).not.toHaveBeenCalled();
  });

  test("load-on-session-change effect fires fetchQueueMessages", async () => {
    const { useWSQueue, effects } = await loadHook();
    useWSQueue("sess-1");
    expect(effects).toHaveLength(1);
    expect(effects[0].deps).toEqual(["sess-1", expect.any(Function)]);
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
    const { useWSQueue, setters } = await loadHook();
    const { moveQueueMessage } = useWSQueue("sess-1");
    const ok = await moveQueueMessage("m1", "up");
    expect(ok).toBe(true);
    const [url, opts] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/sessions/sess-1/queue/m1/move");
    expect(JSON.parse(opts.body)).toEqual({ direction: "up" });
    expect(setters[IDX.setQueueMessages]).toHaveBeenCalledWith([
      { id: "m1" },
      { id: "m2" },
    ]);
    expect(setters[IDX.setQueueLength]).toHaveBeenCalledWith(2);
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
