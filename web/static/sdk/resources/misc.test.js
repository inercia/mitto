/**
 * Unit tests for the SDK misc resource module (mitto-7gta.12).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { fakeResponse, resourceMounter } from "../testing/fake-server.js";
import { createConfigResource } from "./config.js";
import { createMiscResource } from "./misc.js";

const mk = resourceMounter((config) => {
  const serverConfig = createConfigResource(config);
  return { misc: createMiscResource(config, serverConfig), serverConfig };
});

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

  // mitto-7gta.19.1: pre-auth endpoints used by auth.js.
  describe("authInfo", () => {
    test("calls GET /api/auth-info", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() =>
        fakeResponse({ body: { simple: true, cloudflare: false } }),
      );
      const result = await misc.authInfo();
      expect(calls[0].url).toBe("/api/auth-info");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toEqual({ simple: true, cloudflare: false });
    });
  });

  describe("login", () => {
    test("POSTs the credentials body untouched", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { success: true } }));
      const result = await misc.login({
        username: "alice",
        password: "hunter2",
      });
      expect(calls[0].url).toBe("/api/login");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(
        JSON.stringify({ username: "alice", password: "hunter2" }),
      );
      expect(result).toEqual({ success: true });
    });

    test("a 401 (bad credentials) surfaces as MittoApiError, not a thrown network error", async () => {
      const { misc, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 401,
          body: { error: "Invalid username or password" },
        }),
      );
      await expect(
        misc.login({ username: "alice", password: "wrong" }),
      ).rejects.toBeInstanceOf(MittoApiError);
    });
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
      await expect(misc.checkFileExists("/tmp/a.txt")).rejects.toThrow(
        MittoApiError,
      );
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

    // Regression for mitto-hr5m: a transient warm-up response must not require
    // the user to click the magic wand a second time.
    test("retries one unavailable 503 and returns the successful improvement", async () => {
      const { misc, calls, respondWith, respondOnce } = mk();
      respondWith(() =>
        fakeResponse({ body: { improved_prompt: "better after warm-up" } }),
      );
      respondOnce(() =>
        fakeResponse({
          status: 503,
          headers: { "Retry-After": "0" },
          body: { error: { code: "unavailable", message: "starting up" } },
        }),
      );

      await expect(misc.improvePrompt("do it", "ws-1")).resolves.toEqual({
        improved_prompt: "better after warm-up",
      });
      expect(calls).toHaveLength(2);
    });

    test("a 503 while the auxiliary session warms up surfaces as MittoApiError", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 503,
          body: { error: { code: "unavailable", message: "starting up" } },
        }),
      );
      await expect(misc.improvePrompt("do it", "ws-1")).rejects.toThrow(
        MittoApiError,
      );
      expect(calls).toHaveLength(2);
    });

    test("does not retry a non-unavailable server error", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 503,
          body: { error: { code: "server_error", message: "broken" } },
        }),
      );
      await expect(misc.improvePrompt("do it", "ws-1")).rejects.toThrow(
        MittoApiError,
      );
      expect(calls).toHaveLength(1);
    });

    test("aborts while waiting for Retry-After without making a second call", async () => {
      const { misc, calls, respondWith } = mk();
      const controller = new AbortController();
      respondWith(() =>
        fakeResponse({
          status: 503,
          headers: { "Retry-After": "30" },
          body: { error: { code: "unavailable", message: "starting up" } },
        }),
      );

      const result = misc.improvePrompt("do it", "ws-1", {
        signal: controller.signal,
      });
      await Promise.resolve();
      controller.abort();

      await expect(result).rejects.toMatchObject({
        code: "network_error",
        cause: { name: "AbortError" },
      });
      expect(calls).toHaveLength(1);
    });
  });

  describe("badgeClick", () => {
    test("POSTs the body untouched", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { success: true } }));
      const body = {
        workspace_path: "/tmp/ws",
        action: "open",
        target_id: "finder",
      };
      const result = await misc.badgeClick(body);
      expect(calls[0].url).toBe("/api/badge-click");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(JSON.stringify(body));
      expect(result).toEqual({ success: true });
    });

    test("a 403 from a non-loopback client surfaces as MittoApiError", async () => {
      const { misc, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 403,
          body: { error: { code: "forbidden", message: "localhost only" } },
        }),
      );
      await expect(
        misc.badgeClick({
          workspace_path: "/tmp/ws",
          action: "open",
          target_id: "finder",
        }),
      ).rejects.toThrow(MittoApiError);
    });
  });

  describe("folderPin", () => {
    test("get() builds ?working_dir=", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { pinned: true } }));
      const result = await misc.folderPin.get({ working_dir: "/tmp/ws" });
      expect(calls[0].url).toBe("/api/folders/pin?working_dir=%2Ftmp%2Fws");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toEqual({ pinned: true });
    });

    test("set() PUTs {pinned} with ?working_dir=", async () => {
      const { misc, calls, respondWith } = mk();
      respondWith(() => fakeResponse({ body: { pinned: false } }));
      const result = await misc.folderPin.set(
        { working_dir: "/tmp/ws" },
        { pinned: false },
      );
      expect(calls[0].url).toBe("/api/folders/pin?working_dir=%2Ftmp%2Fws");
      expect(calls[0].init.method).toBe("PUT");
      expect(calls[0].init.body).toBe(JSON.stringify({ pinned: false }));
      expect(result).toEqual({ pinned: false });
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
