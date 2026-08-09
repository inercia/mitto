/**
 * Unit tests for the SDK config resource module (mitto-7gta.10).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { fakeResponse, mountResource } from "../testing/fake-server.js";
import { createConfigResource } from "./config.js";

function mk(extra = {}) {
  const { resource: serverConfig, calls, respondWith } = mountResource(
    createConfigResource,
    extra,
  );
  return { serverConfig, calls, respondWith };
}

describe("config resource", () => {
  test("get(params) calls GET /api/config with query params", async () => {
    const { serverConfig, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { web: {} } }));
    const result = await serverConfig.get({ acp_server: "auggie", session_id: "s1" });
    expect(calls[0].url).toBe("/api/config?acp_server=auggie&session_id=s1");
    expect(calls[0].init.method).toBe("GET");
    expect(result).toEqual({ web: {} });
  });

  test("get() with no params omits the query string", async () => {
    const { serverConfig, calls } = mk();
    await serverConfig.get();
    expect(calls[0].url).toBe("/api/config");
  });

  test("save(body) POSTs JSON to /api/config", async () => {
    const { serverConfig, calls } = mk();
    const body = { ui: { theme: "dark" } };
    await serverConfig.save(body);
    expect(calls[0].url).toBe("/api/config");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
    expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
  });

  test("advancedFlags() calls GET /api/advanced-flags", async () => {
    const { serverConfig, calls } = mk();
    await serverConfig.advancedFlags();
    expect(calls[0].url).toBe("/api/advanced-flags");
  });

  test("externalStatus() calls GET /api/external-status", async () => {
    const { serverConfig, calls } = mk();
    await serverConfig.externalStatus();
    expect(calls[0].url).toBe("/api/external-status");
  });

  test("supportedRunners() calls GET /api/supported-runners", async () => {
    const { serverConfig, calls } = mk();
    await serverConfig.supportedRunners();
    expect(calls[0].url).toBe("/api/supported-runners");
  });

  test("runnerDefaults() calls GET /api/runner-defaults", async () => {
    const { serverConfig, calls } = mk();
    await serverConfig.runnerDefaults();
    expect(calls[0].url).toBe("/api/runner-defaults");
  });

  describe("cross-cutting concerns", () => {
    test("save() surfaces a MittoApiError on a read-only config (403)", async () => {
      const { serverConfig, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 403,
          body: { error: { code: "forbidden", message: "Configuration is read-only" } },
        }),
      );
      await expect(serverConfig.save({})).rejects.toThrow(MittoApiError);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { serverConfig, calls } = mk({ apiPrefix: "/mitto" });
      await serverConfig.get();
      expect(calls[0].url).toBe("/mitto/api/config");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { serverConfig, calls } = mk();
      const controller = new AbortController();
      await serverConfig.get(undefined, { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
