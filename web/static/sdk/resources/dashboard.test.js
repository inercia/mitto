/**
 * Unit tests for the SDK dashboard resource module (mitto-7gta.12).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { resolveConfig } from "../core/config.js";
import { MittoApiError } from "../core/errors.js";
import { createDashboardResource } from "./dashboard.js";

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
    dashboard: createDashboardResource(config),
    calls,
    respondWith: (fn) => (next = fn),
  };
}

describe("dashboard resource", () => {
  describe("summary", () => {
    test("summary() with no params omits the query string", async () => {
      const { dashboard, calls } = mk();
      await dashboard.summary();
      expect(calls[0].url).toBe("/api/dashboard");
      expect(calls[0].init.method).toBe("GET");
    });

    test("summary(params) builds ?limit=", async () => {
      const { dashboard, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { stats: {}, lists: {} } }));
      const result = await dashboard.summary({ limit: 10 });
      expect(calls[0].url).toBe("/api/dashboard?limit=10");
      expect(result).toEqual({ stats: {}, lists: {} });
    });
  });

  describe("timeseries", () => {
    test("timeseries() with no params omits the query string", async () => {
      const { dashboard, calls } = mk();
      await dashboard.timeseries();
      expect(calls[0].url).toBe("/api/dashboard/timeseries");
      expect(calls[0].init.method).toBe("GET");
    });

    test("timeseries({metrics: [...]}) comma-joins an array of metrics into a single param", async () => {
      const { dashboard, calls } = mk();
      await dashboard.timeseries({ range: "7d", metrics: ["a", "b", "c"] });
      expect(calls[0].url).toBe("/api/dashboard/timeseries?range=7d&metrics=a%2Cb%2Cc");
    });

    test("timeseries({metrics: 'a,b'}) passes a string value through untouched", async () => {
      const { dashboard, calls } = mk();
      await dashboard.timeseries({ metrics: "a,b" });
      expect(calls[0].url).toBe("/api/dashboard/timeseries?metrics=a%2Cb");
    });

    test("timeseries(params) forwards bucket/workspace/groupBy", async () => {
      const { dashboard, calls } = mk();
      await dashboard.timeseries({ bucket: "day", workspace: "w1", groupBy: "model" });
      expect(calls[0].url).toBe(
        "/api/dashboard/timeseries?bucket=day&workspace=w1&groupBy=model",
      );
    });

    test("timeseries({metrics: []}) omits the metrics param entirely (comma-join of an empty array is '')", async () => {
      const { dashboard, calls } = mk();
      await dashboard.timeseries({ range: "7d", metrics: [] });
      expect(calls[0].url).toBe("/api/dashboard/timeseries?range=7d");
    });
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { dashboard, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 400,
          body: { error: { code: "bad_request", message: "invalid metric: bogus" } },
        }),
      );
      await expect(dashboard.timeseries({ metrics: ["bogus"] })).rejects.toThrow(
        MittoApiError,
      );
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { dashboard, calls } = mk({ apiPrefix: "/mitto" });
      await dashboard.summary();
      expect(calls[0].url).toBe("/mitto/api/dashboard");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { dashboard, calls } = mk();
      const controller = new AbortController();
      await dashboard.summary(undefined, { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
