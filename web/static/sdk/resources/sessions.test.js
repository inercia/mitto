/**
 * Unit tests for the SDK sessions resource module (mitto-7gta.7).
 *
 * Mirrors core/transport.test.js's style: every call is driven by an
 * injected `config.fetch` stub — never global fetch. For representative
 * methods across every group we assert URL (incl. path-segment encoding via
 * encodeURIComponent), HTTP method, body encoding / Content-Type, and the
 * decoded return value — plus the cross-cutting concerns called out in the
 * mitto-7gta.7 plan comment: query-param omission, FormData passthrough,
 * non-2xx -> MittoApiError, 204 -> null, AbortSignal forwarding, and the
 * double-prefixing guard (apiPrefix must appear exactly once in the URL).
 */
import { resolveConfig } from "../core/config.js";
import { MittoApiError } from "../core/errors.js";
import { createSessionsResource } from "./sessions.js";

function fakeResponse({ status = 200, body } = {}) {
  const hasBody = body !== undefined;
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: {
      get: (name) =>
        hasBody && name.toLowerCase() === "content-type" ? "application/json" : null,
    },
    text: async () => (hasBody ? JSON.stringify(body) : ""),
  };
}

/** @returns {{sessions: object, calls: Array, respondWith: Function}} */
function mk(extra = {}) {
  const calls = [];
  let next = () => fakeResponse({ status: 204 });
  const fetchImpl = async (url, init) => {
    calls.push({ url, init });
    return next();
  };
  const config = resolveConfig({ fetch: fetchImpl, ...extra }, {});
  return {
    sessions: createSessionsResource(config),
    calls,
    respondWith: (fn) => (next = fn),
  };
}

describe("sessions resource", () => {
  describe("CRUD", () => {
    test("list() calls GET /api/sessions and decodes the JSON body", async () => {
      const { sessions, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: [{ session_id: "s1" }] }));
      const result = await sessions.list();
      expect(calls[0].url).toBe("/api/sessions");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toEqual([{ session_id: "s1" }]);
    });

    test("running() calls GET /api/sessions/running", async () => {
      const { sessions, calls } = mk();
      await sessions.running();
      expect(calls[0].url).toBe("/api/sessions/running");
      expect(calls[0].init.method).toBe("GET");
    });

    test("get(id) calls GET /api/sessions/{id}, encoding special chars", async () => {
      const { sessions, calls } = mk();
      await sessions.get("a/b c");
      expect(calls[0].url).toBe("/api/sessions/a%2Fb%20c");
    });

    test("create(body) POSTs JSON and sets Content-Type", async () => {
      const { sessions, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { session_id: "new1" } }));
      const body = { name: "n", working_dir: "/tmp" };
      const result = await sessions.create(body);
      expect(calls[0].url).toBe("/api/sessions");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(JSON.stringify(body));
      expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
      expect(result).toEqual({ session_id: "new1" });
    });

    test("update(id, patch) PATCHes JSON to /api/sessions/{id}", async () => {
      const { sessions, calls } = mk();
      const patch = { name: "renamed", pinned: true };
      await sessions.update("s1", patch);
      expect(calls[0].url).toBe("/api/sessions/s1");
      expect(calls[0].init.method).toBe("PATCH");
      expect(calls[0].init.body).toBe(JSON.stringify(patch));
    });

    test("remove(id) DELETEs and a 204 response decodes to null", async () => {
      const { sessions, calls } = mk();
      const result = await sessions.remove("s1");
      expect(calls[0].url).toBe("/api/sessions/s1");
      expect(calls[0].init.method).toBe("DELETE");
      expect(result).toBeNull();
    });
  });

  describe("events / changes", () => {
    test("events(id) with no params omits the query string", async () => {
      const { sessions, calls } = mk();
      await sessions.events("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/events");
    });

    test("events(id, params) builds the query string, omitting null/undefined/empty", async () => {
      const { sessions, calls } = mk();
      await sessions.events("s1", { limit: 50, before: null, order: "desc" });
      expect(calls[0].url).toBe("/api/sessions/s1/events?limit=50&order=desc");
    });

    test("changes(id) calls GET .../changes", async () => {
      const { sessions, calls } = mk();
      await sessions.changes("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/changes");
    });
  });

  describe("settings", () => {
    test("getSettings(id) calls GET .../settings", async () => {
      const { sessions, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { settings: { a: true } } }));
      const result = await sessions.getSettings("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/settings");
      expect(result).toEqual({ settings: { a: true } });
    });

    test("updateSettings(id, settings) PATCHes a {settings} envelope", async () => {
      const { sessions, calls } = mk();
      await sessions.updateSettings("s1", { autoAcceptEdits: true });
      expect(calls[0].init.method).toBe("PATCH");
      expect(calls[0].init.body).toBe(
        JSON.stringify({ settings: { autoAcceptEdits: true } }),
      );
    });
  });


  describe("flush / prune", () => {
    test("flush(id) POSTs with no body and no Content-Type", async () => {
      const { sessions, calls } = mk();
      await sessions.flush("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/flush");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBeUndefined();
      expect(calls[0].init.headers["Content-Type"]).toBeUndefined();
    });

    test("prune(id, keepLast) POSTs {keep_last}", async () => {
      const { sessions, calls } = mk();
      await sessions.prune("s1", 20);
      expect(calls[0].url).toBe("/api/sessions/s1/prune");
      expect(calls[0].init.body).toBe(JSON.stringify({ keep_last: 20 }));
    });

    test("prune(id) with no keepLast drops the undefined key from the JSON body", async () => {
      const { sessions, calls } = mk();
      await sessions.prune("s1");
      expect(calls[0].init.body).toBe("{}");
    });
  });

  describe("callback", () => {
    test("getCallback(id) calls GET .../callback", async () => {
      const { sessions, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { callback_url: "https://x/cb/tok" } }));
      const result = await sessions.getCallback("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/callback");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toEqual({ callback_url: "https://x/cb/tok" });
    });

    test("createCallback(id) calls POST .../callback", async () => {
      const { sessions, calls } = mk();
      await sessions.createCallback("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/callback");
      expect(calls[0].init.method).toBe("POST");
    });

    test("revokeCallback(id) calls DELETE .../callback", async () => {
      const { sessions, calls } = mk();
      await sessions.revokeCallback("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/callback");
      expect(calls[0].init.method).toBe("DELETE");
    });
  });

  describe("user-data", () => {
    test("getUserData(id) calls GET .../user-data", async () => {
      const { sessions, calls } = mk();
      await sessions.getUserData("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/user-data");
      expect(calls[0].init.method).toBe("GET");
    });

    test("setUserData(id, body) PUTs the {attributes} envelope untouched", async () => {
      const { sessions, calls } = mk();
      const body = { attributes: [{ name: "k", value: "v" }] };
      await sessions.setUserData("s1", body);
      expect(calls[0].init.method).toBe("PUT");
      expect(calls[0].init.body).toBe(JSON.stringify(body));
    });
  });

  describe("promptArgCache", () => {
    test("builds ?prompt= from the prompt name, encoding special chars", async () => {
      const { sessions, calls } = mk();
      await sessions.promptArgCache("s1", "team/my prompt");
      expect(calls[0].url).toBe(
        "/api/sessions/s1/prompt-arg-cache?prompt=team%2Fmy+prompt",
      );
    });
  });

  describe("acknowledgeUIPrompt", () => {
    test("POSTs {request_id}", async () => {
      const { sessions, calls } = mk();
      await sessions.acknowledgeUIPrompt("s1", "req-42");
      expect(calls[0].url).toBe("/api/sessions/s1/ui-prompt/acknowledge");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(JSON.stringify({ request_id: "req-42" }));
    });
  });

  describe("images", () => {
    test("list(id) calls GET .../images", async () => {
      const { sessions, calls } = mk();
      await sessions.images.list("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/images");
    });

    test("upload(id, form) passes a FormData-like body through with no Content-Type", async () => {
      const { sessions, calls } = mk();
      const form = { append: () => {} };
      await sessions.images.upload("s1", form);
      expect(calls[0].url).toBe("/api/sessions/s1/images");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(form);
      expect(calls[0].init.headers["Content-Type"]).toBeUndefined();
    });

    test("uploadFromPath(id, paths) POSTs a JSON {paths} body", async () => {
      const { sessions, calls } = mk();
      await sessions.images.uploadFromPath("s1", ["/tmp/a.png", "/tmp/b.png"]);
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(
        JSON.stringify({ paths: ["/tmp/a.png", "/tmp/b.png"] }),
      );
    });

    test("url(id, imageId) returns a path string without calling fetch", () => {
      const { sessions, calls } = mk();
      expect(sessions.images.url("s1", "img 1")).toBe(
        "/api/sessions/s1/images/img%201",
      );
      expect(calls.length).toBe(0);
    });

    test("url(id, imageId) applies baseUrl and apiPrefix like every other method", () => {
      const { sessions } = mk({ baseUrl: "http://host", apiPrefix: "/mitto" });
      expect(sessions.images.url("s1", "img1")).toBe(
        "http://host/mitto/api/sessions/s1/images/img1",
      );
    });

    test("fetchImage(id, imageId) resolves with the raw Response (raw: true)", async () => {
      const { sessions, calls, respondWith } = mk();
      const raw = fakeResponse({ body: "binarydata-stub" });
      respondWith(() => raw);
      const result = await sessions.images.fetchImage("s1", "img1");
      expect(calls[0].url).toBe("/api/sessions/s1/images/img1");
      expect(result).toBe(raw);
    });

    test("remove(id, imageId) calls DELETE .../images/{imageId}", async () => {
      const { sessions, calls } = mk();
      await sessions.images.remove("s1", "img1");
      expect(calls[0].url).toBe("/api/sessions/s1/images/img1");
      expect(calls[0].init.method).toBe("DELETE");
    });
  });

  describe("queue", () => {
    test("list(id) calls GET .../queue and decodes the JSON body", async () => {
      const { sessions, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { messages: [{ id: "m1" }], count: 1 } }));
      const result = await sessions.queue.list("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/queue");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toEqual({ messages: [{ id: "m1" }], count: 1 });
    });

    test("add(id, body) POSTs the QueueAddRequest body untouched", async () => {
      const { sessions, calls } = mk();
      const body = { message: "hi", image_ids: ["i1"] };
      await sessions.queue.add("s1", body);
      expect(calls[0].url).toBe("/api/sessions/s1/queue");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(JSON.stringify(body));
      expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
    });

    test("addNamed(id, promptName, args, extra) POSTs {prompt_name, arguments, ...extra}", async () => {
      const { sessions, calls } = mk();
      await sessions.queue.addNamed("s1", "team/my prompt", { x: "1" }, {
        image_ids: ["i1"],
      });
      expect(calls[0].url).toBe("/api/sessions/s1/queue");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(
        JSON.stringify({
          prompt_name: "team/my prompt",
          arguments: { x: "1" },
          image_ids: ["i1"],
        }),
      );
    });

    test("get(id, msgId) calls GET .../queue/{msgId}, encoding special chars", async () => {
      const { sessions, calls } = mk();
      await sessions.queue.get("s1", "m 1/x");
      expect(calls[0].url).toBe("/api/sessions/s1/queue/m%201%2Fx");
      expect(calls[0].init.method).toBe("GET");
    });

    test("remove(id, msgId) calls DELETE .../queue/{msgId}", async () => {
      const { sessions, calls } = mk();
      const result = await sessions.queue.remove("s1", "m1");
      expect(calls[0].url).toBe("/api/sessions/s1/queue/m1");
      expect(calls[0].init.method).toBe("DELETE");
      expect(result).toBeNull();
    });

    test("clear(id) calls DELETE .../queue (whole queue, no message id)", async () => {
      const { sessions, calls } = mk();
      await sessions.queue.clear("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/queue");
      expect(calls[0].init.method).toBe("DELETE");
    });

    test("move(id, msgId, direction) POSTs {direction} to .../queue/{msgId}/move", async () => {
      const { sessions, calls } = mk();
      await sessions.queue.move("s1", "m1", "up");
      expect(calls[0].url).toBe("/api/sessions/s1/queue/m1/move");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(JSON.stringify({ direction: "up" }));
    });

    test("config() reads conversations.queue from GET /api/config", async () => {
      const { sessions, calls, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          body: { conversations: { queue: { enabled: true, max_size: 10 } } },
        }),
      );
      const result = await sessions.queue.config("s1");
      expect(calls[0].url).toBe("/api/config");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toEqual({ enabled: true, max_size: 10 });
    });

    test("config() returns undefined when the server config has no queue section", async () => {
      const { sessions, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { conversations: {} } }));
      const result = await sessions.queue.config("s1");
      expect(result).toBeUndefined();
    });
  });

  describe("loop", () => {
    test("get(id) calls GET .../loop", async () => {
      const { sessions, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { prompt: "p", enabled: true } }));
      const result = await sessions.loop.get("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/loop");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toEqual({ prompt: "p", enabled: true });
    });

    test("set(id, body) PUTs the LoopPromptRequest body untouched, including triggers[]", async () => {
      const { sessions, calls } = mk();
      const body = {
        prompt: "p",
        frequency: { value: 1, unit: "hours" },
        enabled: true,
        triggers: ["schedule", "onCompletion"],
        delay_seconds: 30,
      };
      await sessions.loop.set("s1", body);
      expect(calls[0].url).toBe("/api/sessions/s1/loop");
      expect(calls[0].init.method).toBe("PUT");
      expect(calls[0].init.body).toBe(JSON.stringify(body));
    });

    test("update(id, patch) PATCHes the LoopPromptPatchRequest body untouched", async () => {
      const { sessions, calls } = mk();
      const patch = { max_iterations: 5, reset_counters: true };
      await sessions.loop.update("s1", patch);
      expect(calls[0].url).toBe("/api/sessions/s1/loop");
      expect(calls[0].init.method).toBe("PATCH");
      expect(calls[0].init.body).toBe(JSON.stringify(patch));
    });

    test("detach(id) calls DELETE .../loop and a 204 response decodes to null", async () => {
      const { sessions, calls } = mk();
      const result = await sessions.loop.detach("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/loop");
      expect(calls[0].init.method).toBe("DELETE");
      expect(result).toBeNull();
    });

    test("restore(id) calls POST .../loop/restore", async () => {
      const { sessions, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { prompt: "p", enabled: true } }));
      const result = await sessions.loop.restore("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/loop/restore");
      expect(calls[0].init.method).toBe("POST");
      expect(result).toEqual({ prompt: "p", enabled: true });
    });

    test("restore(id) surfaces a 409 conflict as MittoApiError", async () => {
      const { sessions, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 409,
          body: { error: { code: "conflict", message: "loop already configured" } },
        }),
      );
      await expect(sessions.loop.restore("s1")).rejects.toThrow(MittoApiError);
    });

    test("runNow(id, resetTimer) POSTs {reset_timer} when resetTimer is provided", async () => {
      const { sessions, calls } = mk();
      await sessions.loop.runNow("s1", false);
      expect(calls[0].url).toBe("/api/sessions/s1/loop/run-now");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(JSON.stringify({ reset_timer: false }));
    });

    test("runNow(id) with no resetTimer omits the body entirely", async () => {
      const { sessions, calls } = mk();
      await sessions.loop.runNow("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/loop/run-now");
      expect(calls[0].init.body).toBeUndefined();
      expect(calls[0].init.headers["Content-Type"]).toBeUndefined();
    });

    test("suggestFromRecent(id) calls GET .../loop/suggest-from-recent", async () => {
      const { sessions, calls } = mk();
      await sessions.loop.suggestFromRecent("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/loop/suggest-from-recent");
      expect(calls[0].init.method).toBe("GET");
    });

    test("acknowledgeStoppedReason(id) calls POST .../loop/acknowledge-stopped-reason", async () => {
      const { sessions, calls } = mk();
      await sessions.loop.acknowledgeStoppedReason("s1");
      expect(calls[0].url).toBe(
        "/api/sessions/s1/loop/acknowledge-stopped-reason",
      );
      expect(calls[0].init.method).toBe("POST");
    });

    test("enable(id) PATCHes {enabled: true}", async () => {
      const { sessions, calls } = mk();
      await sessions.loop.enable("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/loop");
      expect(calls[0].init.method).toBe("PATCH");
      expect(calls[0].init.body).toBe(JSON.stringify({ enabled: true }));
    });

    test("disable(id) PATCHes {enabled: false}", async () => {
      const { sessions, calls } = mk();
      await sessions.loop.disable("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/loop");
      expect(calls[0].init.method).toBe("PATCH");
      expect(calls[0].init.body).toBe(JSON.stringify({ enabled: false }));
    });
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { sessions, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 404,
          body: { error: { code: "not_found", message: "no such session" } },
        }),
      );
      await expect(sessions.get("missing")).rejects.toThrow(MittoApiError);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { sessions, calls } = mk();
      const controller = new AbortController();
      await sessions.get("s1", { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { sessions, calls } = mk({ apiPrefix: "/mitto" });
      await sessions.get("s1");
      expect(calls[0].url).toBe("/mitto/api/sessions/s1");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("queue.list(id) applies apiPrefix exactly once", async () => {
      const { sessions, calls } = mk({ apiPrefix: "/mitto" });
      await sessions.queue.list("s1");
      expect(calls[0].url).toBe("/mitto/api/sessions/s1/queue");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("loop.get(id) applies apiPrefix exactly once", async () => {
      const { sessions, calls } = mk({ apiPrefix: "/mitto" });
      await sessions.loop.get("s1");
      expect(calls[0].url).toBe("/mitto/api/sessions/s1/loop");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch for a queue method", async () => {
      const { sessions, calls } = mk();
      const controller = new AbortController();
      await sessions.queue.list("s1", { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });

    test("forwards an AbortSignal to fetch for a loop method", async () => {
      const { sessions, calls } = mk();
      const controller = new AbortController();
      await sessions.loop.get("s1", { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
