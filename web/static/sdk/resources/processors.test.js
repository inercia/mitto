/**
 * Unit tests for the SDK processors resource module (mitto-7gta.10).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { resolveConfig } from "../core/config.js";
import { MittoApiError } from "../core/errors.js";
import { createProcessorsResource } from "./processors.js";

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

function mk(extra = {}) {
  const calls = [];
  let next = () => fakeResponse({ status: 204 });
  const fetchImpl = async (url, init) => {
    calls.push({ url, init });
    return next();
  };
  const config = resolveConfig({ fetch: fetchImpl, ...extra }, {});
  return {
    processors: createProcessorsResource(config),
    calls,
    respondWith: (fn) => (next = fn),
  };
}

describe("processors resource", () => {
  test("list(uuid) calls GET /api/workspaces/{uuid}/processors, encoding special chars", async () => {
    const { processors, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { processors: [] } }));
    const result = await processors.list("uuid a/b");
    expect(calls[0].url).toBe("/api/workspaces/uuid%20a%2Fb/processors");
    expect(calls[0].init.method).toBe("GET");
    expect(result).toEqual({ processors: [] });
  });

  test("setEnabled(uuid, name, enabled) PATCHes {enabled}", async () => {
    const { processors, calls } = mk();
    await processors.setEnabled("u1", "proc1", true);
    expect(calls[0].url).toBe("/api/workspaces/u1/processors/proc1");
    expect(calls[0].init.method).toBe("PATCH");
    expect(calls[0].init.body).toBe(JSON.stringify({ enabled: true }));
  });

  test("setArguments(uuid, name, argumentsMap) PUTs {arguments}", async () => {
    const { processors, calls } = mk();
    await processors.setArguments("u1", "proc1", { foo: "bar" });
    expect(calls[0].url).toBe("/api/workspaces/u1/processors/proc1/arguments");
    expect(calls[0].init.method).toBe("PUT");
    expect(calls[0].init.body).toBe(JSON.stringify({ arguments: { foo: "bar" } }));
    expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { processors, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 404,
          body: { error: { code: "not_found", message: "Processor not found: x" } },
        }),
      );
      await expect(processors.setEnabled("u1", "x", true)).rejects.toThrow(
        MittoApiError,
      );
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { processors, calls } = mk({ apiPrefix: "/mitto" });
      await processors.list("u1");
      expect(calls[0].url).toBe("/mitto/api/workspaces/u1/processors");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { processors, calls } = mk();
      const controller = new AbortController();
      await processors.list("u1", { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
