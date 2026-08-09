/**
 * Unit tests for the SDK issues (beads) resource module (mitto-7gta.11).
 *
 * Mirrors sessions.test.js / shortcuts.test.js's style: every call is driven
 * by an injected `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { fakeResponse, resourceMounter } from "../testing/fake-server.js";
import { createIssuesResource, withIssueCaches } from "./issues.js";

const mk = resourceMounter((config) => ({ issues: createIssuesResource(config) }));

const WD = "/Users/x/proj";

describe("issues resource", () => {
  test("list(params) calls GET /api/issues with working_dir", async () => {
    const { issues, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: [{ id: "a-1" }] }));
    const result = await issues.list({ working_dir: WD });
    expect(calls[0].url).toBe(`/api/issues?working_dir=${encodeURIComponent(WD)}`);
    expect(calls[0].init.method).toBe("GET");
    expect(result).toEqual([{ id: "a-1" }]);
  });

  test("stats(params) calls GET /api/issues/stats", async () => {
    const { issues, calls } = mk();
    await issues.stats({ working_dir: WD });
    expect(calls[0].url).toBe(`/api/issues/stats?working_dir=${encodeURIComponent(WD)}`);
  });

  test("labelsAll(params) calls GET /api/issues/labels (not /api/issues/{id})", async () => {
    const { issues, calls } = mk();
    await issues.labelsAll({ working_dir: WD });
    expect(calls[0].url).toBe(`/api/issues/labels?working_dir=${encodeURIComponent(WD)}`);
    expect(calls[0].init.method).toBe("GET");
  });

  describe("config", () => {
    test("config(params) calls GET /api/issues/config", async () => {
      const { issues, calls } = mk();
      await issues.config({ working_dir: WD });
      expect(calls[0].url).toBe(`/api/issues/config?working_dir=${encodeURIComponent(WD)}`);
    });
    test("setConfig(params, body) PUTs {key, value}", async () => {
      const { issues, calls } = mk();
      await issues.setConfig({ working_dir: WD }, { key: "k", value: "v" });
      expect(calls[0].init.method).toBe("PUT");
      expect(calls[0].init.body).toBe(JSON.stringify({ key: "k", value: "v" }));
    });
    test("deleteConfig(params) DELETEs with key in query", async () => {
      const { issues, calls } = mk();
      await issues.deleteConfig({ working_dir: WD, key: "k" });
      expect(calls[0].url).toContain("key=k");
      expect(calls[0].init.method).toBe("DELETE");
    });
  });

  describe("upstream", () => {
    test("upstream(params) calls GET /api/issues/upstream", async () => {
      const { issues, calls } = mk();
      await issues.upstream({ working_dir: WD });
      expect(calls[0].url).toBe(`/api/issues/upstream?working_dir=${encodeURIComponent(WD)}`);
    });
    test("setUpstream(params, body) PUTs the integration config", async () => {
      const { issues, calls } = mk();
      const body = { upstream: "jira" };
      await issues.setUpstream({ working_dir: WD }, body);
      expect(calls[0].init.method).toBe("PUT");
      expect(calls[0].init.body).toBe(JSON.stringify(body));
    });
  });

  test("show(id, params) encodes the id and calls GET /api/issues/{id}", async () => {
    const { issues, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { id: "a/b.1" } }));
    await issues.show("a/b.1", { working_dir: WD });
    expect(calls[0].url).toBe(
      `/api/issues/${encodeURIComponent("a/b.1")}?working_dir=${encodeURIComponent(WD)}`,
    );
  });

  test("create(params, body) POSTs to /api/issues", async () => {
    const { issues, calls } = mk();
    const body = { title: "t" };
    await issues.create({ working_dir: WD }, body);
    expect(calls[0].url).toBe(`/api/issues?working_dir=${encodeURIComponent(WD)}`);
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
  });

  test("update(id, params, patch) PATCHes /api/issues/{id}", async () => {
    const { issues, calls } = mk();
    await issues.update("a-1", { working_dir: WD }, { title: "new" });
    expect(calls[0].url).toBe(`/api/issues/a-1?working_dir=${encodeURIComponent(WD)}`);
    expect(calls[0].init.method).toBe("PATCH");
  });

  test("remove(id, params) DELETEs /api/issues/{id}", async () => {
    const { issues, calls } = mk();
    await issues.remove("a-1", { working_dir: WD });
    expect(calls[0].init.method).toBe("DELETE");
  });

  test("status(id, params, body) POSTs /api/issues/{id}/status", async () => {
    const { issues, calls } = mk();
    await issues.status("a-1", { working_dir: WD }, { action: "close" });
    expect(calls[0].url).toBe(`/api/issues/a-1/status?working_dir=${encodeURIComponent(WD)}`);
    expect(calls[0].init.body).toBe(JSON.stringify({ action: "close" }));
  });

  test("comment()/comments() alias share the same POST /api/issues/{id}/comments", async () => {
    const { issues, calls } = mk();
    expect(issues.comments).toBe(issues.comment);
    await issues.comments("a-1", { working_dir: WD }, { text: "hi" });
    expect(calls[0].url).toBe(`/api/issues/a-1/comments?working_dir=${encodeURIComponent(WD)}`);
  });

  test("dependency()/dependencies() alias share the same POST /api/issues/{id}/dependencies", async () => {
    const { issues, calls } = mk();
    expect(issues.dependencies).toBe(issues.dependency);
    await issues.dependencies("a-1", { working_dir: WD }, { depends_on: "a-2", action: "add" });
    expect(calls[0].url).toBe(`/api/issues/a-1/dependencies?working_dir=${encodeURIComponent(WD)}`);
  });

  test("label()/labels() alias share the same POST /api/issues/{id}/labels", async () => {
    const { issues, calls } = mk();
    expect(issues.labels).toBe(issues.label);
    await issues.labels("a-1", { working_dir: WD }, { label: "x", action: "add" });
    expect(calls[0].url).toBe(`/api/issues/a-1/labels?working_dir=${encodeURIComponent(WD)}`);
  });

  test("cleanup(params) POSTs /api/issues/cleanup and decodes the started/total shape", async () => {
    const { issues, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { started: true, total: 5 } }));
    const result = await issues.cleanup({ working_dir: WD });
    expect(calls[0].url).toBe(`/api/issues/cleanup?working_dir=${encodeURIComponent(WD)}`);
    expect(calls[0].init.method).toBe("POST");
    expect(result).toEqual({ started: true, total: 5 });
  });

  test("sync(params, body) POSTs the action to /api/issues/sync", async () => {
    const { issues, calls } = mk();
    await issues.sync({ working_dir: WD }, { action: "pull" });
    expect(calls[0].url).toBe(`/api/issues/sync?working_dir=${encodeURIComponent(WD)}`);
    expect(calls[0].init.body).toBe(JSON.stringify({ action: "pull" }));
  });

  test("migrate(body) POSTs to /api/beads/migrate with working_dir in the BODY, not the query", async () => {
    const { issues, calls } = mk();
    await issues.migrate({ working_dir: WD, mode: "migrate" });
    expect(calls[0].url).toBe("/api/beads/migrate");
    expect(calls[0].init.body).toBe(JSON.stringify({ working_dir: WD, mode: "migrate" }));
  });

  test("non-2xx response rejects with MittoApiError", async () => {
    const { issues, respondWith } = mk();
    respondWith(() =>
      fakeResponse({ status: 404, body: { error: { code: "not_found", message: "gone" } } }),
    );
    await expect(issues.show("missing", { working_dir: WD })).rejects.toBeInstanceOf(
      MittoApiError,
    );
  });

  describe("cross-cutting concerns (mitto-7gta.7 parity)", () => {
    test("remove(id, params) with a 204 response decodes to null", async () => {
      const { issues } = mk();
      const result = await issues.remove("a-1", { working_dir: WD });
      expect(result).toBeNull();
    });

    test("list(params) omits null/undefined/empty query values", async () => {
      const { issues, calls } = mk();
      await issues.list({ working_dir: WD, priority: null, assignee: undefined, notes: "" });
      expect(calls[0].url).toBe(`/api/issues?working_dir=${encodeURIComponent(WD)}`);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { issues, calls } = mk();
      const controller = new AbortController();
      await issues.show("a-1", { working_dir: WD }, { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { issues, calls } = mk({ apiPrefix: "/mitto" });
      await issues.show("a-1", { working_dir: WD });
      expect(calls[0].url).toBe(
        `/mitto/api/issues/a-1?working_dir=${encodeURIComponent(WD)}`,
      );
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("migrate(body) applies apiPrefix exactly once", async () => {
      const { issues, calls } = mk({ apiPrefix: "/mitto" });
      await issues.migrate({ working_dir: WD, mode: "migrate" });
      expect(calls[0].url).toBe("/mitto/api/beads/migrate");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });
  });
});

describe("withIssueCaches decorator", () => {
  test("pass-through with no hooks: behaves identically and calls nothing extra", async () => {
    const { issues, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { id: "a-1" } }));
    const wrapped = withIssueCaches(issues, {});
    const result = await wrapped.show("a-1", { working_dir: WD });
    expect(result).toEqual({ id: "a-1" });
    expect(calls).toHaveLength(1);
  });

  test("show() short-circuits without a network call when isGone() is true", async () => {
    const { issues, calls } = mk();
    const wrapped = withIssueCaches(issues, { isGone: () => true });
    await expect(wrapped.show("gone-id", { working_dir: WD })).rejects.toMatchObject({
      status: 404,
    });
    expect(calls).toHaveLength(0);
  });

  test("show() calls markGone() on a real 404 and rethrows", async () => {
    const { issues, respondWith } = mk();
    respondWith(() => fakeResponse({ status: 404, body: { error: "not found" } }));
    const markGone = jest.fn();
    const wrapped = withIssueCaches(issues, { markGone });
    await expect(wrapped.show("a-1", { working_dir: WD })).rejects.toBeInstanceOf(MittoApiError);
    expect(markGone).toHaveBeenCalledWith(WD, "a-1");
  });

  test("list() calls onListed() with the working dir and the decoded array", async () => {
    const { issues, respondWith } = mk();
    const rows = [{ id: "a-1" }, { id: "a-2" }];
    respondWith(() => fakeResponse({ body: rows }));
    const onListed = jest.fn();
    const wrapped = withIssueCaches(issues, { onListed });
    await wrapped.list({ working_dir: WD });
    expect(onListed).toHaveBeenCalledWith(WD, rows);
  });

  test("preload() fetches each non-deduped, non-gone id and swallows errors", async () => {
    const { issues, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ status: 500, body: { error: "boom" } }));
    const isGone = jest.fn((wd, id) => id === "skip-me");
    const wrapped = withIssueCaches(issues, { isGone });
    wrapped.preload(["a-1", "skip-me", null, "a-2"], { working_dir: WD });
    await new Promise((r) => setTimeout(r, 0));
    expect(calls.map((c) => c.url)).toEqual([
      `/api/issues/a-1?working_dir=${encodeURIComponent(WD)}`,
      `/api/issues/a-2?working_dir=${encodeURIComponent(WD)}`,
    ]);
  });

  test("preload() skips ids rejected by shouldPreload()", async () => {
    const { issues, calls } = mk();
    const shouldPreload = jest.fn((wd, id) => id !== "throttled");
    const wrapped = withIssueCaches(issues, { shouldPreload });
    wrapped.preload(["throttled"], { working_dir: WD });
    await new Promise((r) => setTimeout(r, 0));
    expect(calls).toHaveLength(0);
  });

  test("show() does NOT call markGone() on a non-404 error", async () => {
    const { issues, respondWith } = mk();
    respondWith(() => fakeResponse({ status: 500, body: { error: "boom" } }));
    const markGone = jest.fn();
    const wrapped = withIssueCaches(issues, { markGone });
    await expect(wrapped.show("a-1", { working_dir: WD })).rejects.toBeInstanceOf(MittoApiError);
    expect(markGone).not.toHaveBeenCalled();
  });

  test("list() does NOT call onListed() when the decoded body is not an array", async () => {
    const { issues, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { not: "an array" } }));
    const onListed = jest.fn();
    const wrapped = withIssueCaches(issues, { onListed });
    await wrapped.list({ working_dir: WD });
    expect(onListed).not.toHaveBeenCalled();
  });

  test("preload() with no hooks fetches every non-null id unconditionally", async () => {
    const { issues, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { id: "x" } }));
    const wrapped = withIssueCaches(issues, {});
    wrapped.preload(["a-1", "a-2"], { working_dir: WD });
    await new Promise((r) => setTimeout(r, 0));
    expect(calls.map((c) => c.url)).toEqual([
      `/api/issues/a-1?working_dir=${encodeURIComponent(WD)}`,
      `/api/issues/a-2?working_dir=${encodeURIComponent(WD)}`,
    ]);
  });
});
