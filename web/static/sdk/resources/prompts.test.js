/**
 * Unit tests for the SDK prompts resource module (mitto-7gta.10).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { fakeResponse, mountResource } from "../testing/fake-server.js";
import { createPromptsResource } from "./prompts.js";

function mk(extra = {}) {
  const { resource: prompts, calls, respondWith } = mountResource(createPromptsResource, extra);
  return { prompts, calls, respondWith };
}

describe("prompts resource", () => {
  test("list(params) calls GET /api/workspace-prompts with query params", async () => {
    const { prompts, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { prompts: [{ name: "p1" }] } }));
    const result = await prompts.list({ working_dir: "/tmp", include_global: true });
    expect(calls[0].url).toBe(
      "/api/workspace-prompts?working_dir=%2Ftmp&include_global=true",
    );
    expect(calls[0].init.method).toBe("GET");
    expect(result).toEqual({ prompts: [{ name: "p1" }] });
  });

  test("create(body) POSTs JSON to /api/workspace-prompts", async () => {
    const { prompts, calls } = mk();
    const body = { working_dir: "/tmp", name: "p1", prompt: "text" };
    await prompts.create(body);
    expect(calls[0].url).toBe("/api/workspace-prompts");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
    expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
  });

  test("remove(params) DELETEs with query params, not a path segment or body", async () => {
    const { prompts, calls } = mk();
    await prompts.remove({ name: "p1", working_dir: "/tmp" });
    expect(calls[0].url).toBe("/api/workspace-prompts?name=p1&working_dir=%2Ftmp");
    expect(calls[0].init.method).toBe("DELETE");
    expect(calls[0].init.body).toBeUndefined();
  });

  test("setEnabled(name, workingDir, enabled) PATCHes {enabled} with working_dir as a query param", async () => {
    const { prompts, calls } = mk();
    await prompts.setEnabled("My Prompt", "/tmp", false);
    expect(calls[0].url).toBe("/api/workspace-prompts/My%20Prompt?working_dir=%2Ftmp");
    expect(calls[0].init.method).toBe("PATCH");
    expect(calls[0].init.body).toBe(JSON.stringify({ enabled: false }));
  });

  test("rememberedArgs(params) calls GET /api/workspace-prompts/remembered-args", async () => {
    const { prompts, calls } = mk();
    await prompts.rememberedArgs({
      working_dir: "/tmp",
      prompt: "p1",
      session_id: "s1",
    });
    expect(calls[0].url).toBe(
      "/api/workspace-prompts/remembered-args?working_dir=%2Ftmp&prompt=p1&session_id=s1",
    );
    expect(calls[0].init.method).toBe("GET");
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { prompts, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 404,
          body: { error: { code: "not_found", message: "prompt not found" } },
        }),
      );
      await expect(
        prompts.remove({ name: "missing", working_dir: "/tmp" }),
      ).rejects.toThrow(MittoApiError);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { prompts, calls } = mk({ apiPrefix: "/mitto" });
      await prompts.list({ working_dir: "/tmp" });
      expect(calls[0].url).toBe("/mitto/api/workspace-prompts?working_dir=%2Ftmp");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { prompts, calls } = mk();
      const controller = new AbortController();
      await prompts.list({ working_dir: "/tmp" }, { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
