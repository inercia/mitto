/**
 * Unit tests for the SDK files resource module (mitto-7gta.12).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { resolveConfig } from "../core/config.js";
import { MittoApiError } from "../core/errors.js";
import { createFilesResource } from "./files.js";

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
    files: createFilesResource(config),
    calls,
    respondWith: (fn) => (next = fn),
  };
}

describe("files resource", () => {
  describe("session-scoped files", () => {
    test("list(id) calls GET .../files", async () => {
      const { files, calls } = mk();
      await files.list("s1");
      expect(calls[0].url).toBe("/api/sessions/s1/files");
      expect(calls[0].init.method).toBe("GET");
    });

    test("upload(id, form) passes a FormData-like body through with no Content-Type", async () => {
      const { files, calls } = mk();
      const form = { append: () => {} };
      await files.upload("s1", form);
      expect(calls[0].url).toBe("/api/sessions/s1/files");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(form);
      expect(calls[0].init.headers["Content-Type"]).toBeUndefined();
    });

    test("uploadFromPath(id, paths) POSTs a JSON {paths} body", async () => {
      const { files, calls } = mk();
      await files.uploadFromPath("s1", ["/tmp/a.txt", "/tmp/b.txt"]);
      expect(calls[0].url).toBe("/api/sessions/s1/files/from-path");
      expect(calls[0].init.method).toBe("POST");
      expect(calls[0].init.body).toBe(
        JSON.stringify({ paths: ["/tmp/a.txt", "/tmp/b.txt"] }),
      );
    });

    test("url(id, fileId) returns a path string without calling fetch", () => {
      const { files, calls } = mk();
      expect(files.url("s1", "file 1")).toBe("/api/sessions/s1/files/file%201");
      expect(calls.length).toBe(0);
    });

    test("url(id, fileId) applies baseUrl and apiPrefix like every other method", () => {
      const { files } = mk({ baseUrl: "http://host", apiPrefix: "/mitto" });
      expect(files.url("s1", "f1")).toBe("http://host/mitto/api/sessions/s1/files/f1");
    });

    test("fetchFile(id, fileId) resolves with the raw Response (raw: true)", async () => {
      const { files, calls, respondWith } = mk();
      const raw = fakeResponse({ body: "binarydata-stub" });
      respondWith(() => raw);
      const result = await files.fetchFile("s1", "f1");
      expect(calls[0].url).toBe("/api/sessions/s1/files/f1");
      expect(result).toBe(raw);
    });

    test("remove(id, fileId) calls DELETE .../files/{fileId}", async () => {
      const { files, calls } = mk();
      await files.remove("s1", "f1");
      expect(calls[0].url).toBe("/api/sessions/s1/files/f1");
      expect(calls[0].init.method).toBe("DELETE");
    });

    test("list(id) encodes special characters in the session id, not just the fileId", async () => {
      const { files, calls } = mk();
      await files.list("s 1/2");
      expect(calls[0].url).toBe("/api/sessions/s%201%2F2/files");
    });
  });

  describe("workspace file server", () => {
    test("contentUrl(params) builds a query string without calling fetch", () => {
      const { files, calls } = mk();
      expect(files.contentUrl({ ws: "w1", path: "a/b.txt" })).toBe(
        "/api/files?ws=w1&path=a%2Fb.txt",
      );
      expect(calls.length).toBe(0);
    });

    test("contentUrl(params) applies baseUrl and apiPrefix", () => {
      const { files } = mk({ baseUrl: "http://host", apiPrefix: "/mitto" });
      expect(files.contentUrl({ ws: "w1", path: "a.txt" })).toBe(
        "http://host/mitto/api/files?ws=w1&path=a.txt",
      );
    });

    test("fetchContent(params) calls GET /api/files with raw: true", async () => {
      const { files, calls, respondWith } = mk();
      const raw = fakeResponse({ body: "content-stub" });
      respondWith(() => raw);
      const result = await files.fetchContent({ ws: "w1", path: "a.txt", render: "html" });
      expect(calls[0].url).toBe("/api/files?ws=w1&path=a.txt&render=html");
      expect(calls[0].init.method).toBe("GET");
      expect(result).toBe(raw);
    });
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { files, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 404,
          body: { error: { code: "not_found", message: "no such file" } },
        }),
      );
      await expect(files.list("s1")).rejects.toThrow(MittoApiError);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { files, calls } = mk({ apiPrefix: "/mitto" });
      await files.list("s1");
      expect(calls[0].url).toBe("/mitto/api/sessions/s1/files");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { files, calls } = mk();
      const controller = new AbortController();
      await files.list("s1", { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
