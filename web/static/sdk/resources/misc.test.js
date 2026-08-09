/**
 * Unit tests for the SDK misc resource module (mitto-7gta.12).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { createFakeServer, fakeResponse } from "../testing/fake-server.js";
import { createConfigResource } from "./config.js";
import { createMiscResource } from "./misc.js";

function mk(extra = {}) {
  const { config, calls, respondWith } = createFakeServer(extra);
  const serverConfig = createConfigResource(config);
  return {
    misc: createMiscResource(config, serverConfig),
    serverConfig,
    calls,
    respondWith,
  };
}

describe("misc resource", () => {
  describe("uiPreferences", () => {
    test("get() calls GET /api/ui-preferences", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { font_size: "large" } }));
      const result = await misc.uiPreferences.get();
      expect(calls[0].url).toBe("/api/ui-preferences");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toEqual({ font_size: "large" });
    });

    test("save(prefs) PUTs the prefs body untouched", async () => {
      const { misc, calls } = mk();
      const prefs = { font_size: "large", theme: "dark" };
      await misc.uiPreferences.save(prefs);
      expect(calls[0].url).toBe("/api/ui-preferences");
      expect(calls[0].init.method).toBe("PUT");
      expect(calls[0].init.body).toBe(JSON.stringify(prefs));
      expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
    });
  });

  test("csrfToken() calls GET /api/csrf-token", async () => {
    const { misc, calls } = mk();
    await misc.csrfToken();
    expect(calls[0].url).toBe("/api/csrf-token");
    expect(calls[0].init.method).toBe("GET");
  });

  describe("checkFileExists", () => {
    test("builds ?path= from the given path", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { exists: true } }));
      const result = await misc.checkFileExists("/tmp/a b.txt");
      expect(calls[0].url).toBe("/api/check-file-exists?path=%2Ftmp%2Fa+b.txt");
      expect(result).toEqual({ exists: true });
    });

    test("a 403 from the external listener surfaces as MittoApiError", async () => {
      const { misc, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 403,
          body: { error: { code: "forbidden", message: "Forbidden" } },
        }),
      );
      await expect(misc.checkFileExists("/tmp/a.txt")).rejects.toThrow(MittoApiError);
    });
  });

  test("saveFileToPath(path, content) POSTs {path, content}", async () => {
    const { misc, calls } = mk();
    await misc.saveFileToPath("/tmp/a.txt", "hello");
    expect(calls[0].url).toBe("/api/save-file-to-path");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(
      JSON.stringify({ path: "/tmp/a.txt", content: "hello" }),
    );
  });

  describe("improvePrompt", () => {
    test("POSTs {prompt, workspace_uuid}", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { improved_prompt: "better" } }));
      const result = await misc.improvePrompt("do it", "ws-1");
      expect(calls[0].url).toBe("/api/aux/improve-prompt");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(
        JSON.stringify({ prompt: "do it", workspace_uuid: "ws-1" }),
      );
      expect(result).toEqual({ improved_prompt: "better" });
    });

    test("a 503 while the auxiliary session warms up surfaces as MittoApiError", async () => {
      const { misc, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 503,
          body: { error: { code: "unavailable", message: "starting up" } },
        }),
      );
      await expect(misc.improvePrompt("do it", "ws-1")).rejects.toThrow(MittoApiError);
    });
  });

  describe("delegated discovery endpoints (mitto-7gta.10)", () => {
    test("advancedFlags/externalStatus/supportedRunners/runnerDefaults are the same function objects as serverConfig's", () => {
      const { misc, serverConfig } = mk();
      expect(misc.advancedFlags).toBe(serverConfig.advancedFlags);
      expect(misc.externalStatus).toBe(serverConfig.externalStatus);
      expect(misc.supportedRunners).toBe(serverConfig.supportedRunners);
      expect(misc.runnerDefaults).toBe(serverConfig.runnerDefaults);
    });

    test("misc.advancedFlags() calls GET /api/advanced-flags", async () => {
      const { misc, calls } = mk();
      await misc.advancedFlags();
      expect(calls[0].url).toBe("/api/advanced-flags");
    });
  });

  describe("cross-cutting concerns", () => {
    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { misc, calls } = mk({ apiPrefix: "/mitto" });
      await misc.csrfToken();
      expect(calls[0].url).toBe("/mitto/api/csrf-token");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { misc, calls } = mk();
      const controller = new AbortController();
      await misc.csrfToken({ signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
