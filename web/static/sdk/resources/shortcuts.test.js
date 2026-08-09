/**
 * Unit tests for the SDK shortcuts resource module (mitto-7gta.10).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { fakeResponse, mountResource } from "../testing/fake-server.js";
import { createShortcutsResource } from "./shortcuts.js";

function mk(extra = {}) {
  const { resource: shortcuts, calls, respondWith } = mountResource(
    createShortcutsResource,
    extra,
  );
  return { shortcuts, calls, respondWith };
}

describe("shortcuts resource", () => {
  test("getGlobal() with no params calls GET /api/global/shortcuts (no include_prompts)", async () => {
    const { shortcuts, calls } = mk();
    await shortcuts.getGlobal();
    expect(calls[0].url).toBe("/api/global/shortcuts");
    expect(calls[0].init.method).toBe("GET");
  });

  test("getGlobal({include_prompts: true}) appends the opt-in query param", async () => {
    const { shortcuts, calls } = mk();
    await shortcuts.getGlobal({ include_prompts: true });
    expect(calls[0].url).toBe("/api/global/shortcuts?include_prompts=true");
  });

  test("setGlobal(body) PUTs {sections}", async () => {
    const { shortcuts, calls } = mk();
    const body = { sections: { toolbar: [{ prompt: "p1" }] } };
    await shortcuts.setGlobal(body);
    expect(calls[0].url).toBe("/api/global/shortcuts");
    expect(calls[0].init.method).toBe("PUT");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
  });

  test("getFolder(params) calls GET /api/folders/shortcuts?working_dir=...", async () => {
    const { shortcuts, calls } = mk();
    await shortcuts.getFolder({ working_dir: "/tmp/ws" });
    expect(calls[0].url).toBe("/api/folders/shortcuts?working_dir=%2Ftmp%2Fws");
    expect(calls[0].init.method).toBe("GET");
  });

  test("setFolder(workingDir, body) PUTs {sections} with working_dir as a query param", async () => {
    const { shortcuts, calls } = mk();
    const body = { sections: { toolbar: [] } };
    await shortcuts.setFolder("/tmp/ws", body);
    expect(calls[0].url).toBe("/api/folders/shortcuts?working_dir=%2Ftmp%2Fws");
    expect(calls[0].init.method).toBe("PUT");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { shortcuts, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 400,
          body: { error: { code: "invalid_argument", message: "working_dir is required" } },
        }),
      );
      await expect(shortcuts.getFolder({})).rejects.toThrow(MittoApiError);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { shortcuts, calls } = mk({ apiPrefix: "/mitto" });
      await shortcuts.getGlobal();
      expect(calls[0].url).toBe("/mitto/api/global/shortcuts");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { shortcuts, calls } = mk();
      const controller = new AbortController();
      await shortcuts.getGlobal(undefined, { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
