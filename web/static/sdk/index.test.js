/**
 * Unit tests for the SDK's public entry point wiring (mitto-7gta.10).
 *
 * Scope: only the client surface area this bead added (`client.prompts`,
 * `client.processors`, `client.shortcuts`, `client.serverConfig`, and the
 * `createTtlCache`/`keyForParams` re-exports) plus a regression guard for
 * the `client.config` vs `client.serverConfig` naming decision recorded in
 * the Implementation comment on mitto-7gta.10. Pre-existing wiring
 * (`client.sessions`, streams, etc.) predates this bead and is out of scope
 * here.
 */
import { createClient, createTtlCache, keyForParams, withIssueCaches } from "./index.js";

function noopFetch() {
  return Promise.resolve({
    ok: true,
    status: 204,
    headers: { get: () => null },
    text: async () => "",
  });
}

describe("createClient() wiring (mitto-7gta.10)", () => {
  test("exposes client.prompts with the full resource surface", () => {
    const client = createClient({ fetch: noopFetch });
    expect(typeof client.prompts.list).toBe("function");
    expect(typeof client.prompts.create).toBe("function");
    expect(typeof client.prompts.remove).toBe("function");
    expect(typeof client.prompts.setEnabled).toBe("function");
    expect(typeof client.prompts.rememberedArgs).toBe("function");
  });

  test("exposes client.processors with the full resource surface", () => {
    const client = createClient({ fetch: noopFetch });
    expect(typeof client.processors.list).toBe("function");
    expect(typeof client.processors.setEnabled).toBe("function");
    expect(typeof client.processors.setArguments).toBe("function");
  });

  test("exposes client.shortcuts with the full resource surface", () => {
    const client = createClient({ fetch: noopFetch });
    expect(typeof client.shortcuts.getGlobal).toBe("function");
    expect(typeof client.shortcuts.setGlobal).toBe("function");
    expect(typeof client.shortcuts.getFolder).toBe("function");
    expect(typeof client.shortcuts.setFolder).toBe("function");
  });

  test("exposes client.serverConfig with the full resource surface", () => {
    const client = createClient({ fetch: noopFetch });
    expect(typeof client.serverConfig.get).toBe("function");
    expect(typeof client.serverConfig.save).toBe("function");
    expect(typeof client.serverConfig.advancedFlags).toBe("function");
    expect(typeof client.serverConfig.externalStatus).toBe("function");
    expect(typeof client.serverConfig.supportedRunners).toBe("function");
    expect(typeof client.serverConfig.runnerDefaults).toBe("function");
  });

  test("exposes client.issues with the full resource surface (mitto-7gta.11)", () => {
    const client = createClient({ fetch: noopFetch });
    for (const method of [
      "list",
      "stats",
      "show",
      "create",
      "update",
      "remove",
      "status",
      "comments",
      "dependencies",
      "labels",
      "labelsAll",
      "cleanup",
      "config",
      "upstream",
      "sync",
      "migrate",
    ]) {
      expect(typeof client.issues[method]).toBe("function");
    }
  });

  test("client.config remains the resolved SDK config, distinct from client.serverConfig (naming-collision regression guard)", () => {
    const client = createClient({ fetch: noopFetch, baseUrl: "http://x" });
    expect(client.config.baseUrl).toBe("http://x");
    expect(client.config.get).toBeUndefined();
    expect(client.config.save).toBeUndefined();
    expect(client.serverConfig).not.toBe(client.config);
    expect(typeof client.serverConfig.get).toBe("function");
  });

  test("each call to createClient() returns independently-scoped resources (no shared module-level state)", () => {
    const a = createClient({ fetch: noopFetch });
    const b = createClient({ fetch: noopFetch });
    expect(a.prompts).not.toBe(b.prompts);
    expect(a.serverConfig).not.toBe(b.serverConfig);
  });
});

describe("createTtlCache / keyForParams public export (mitto-7gta.10)", () => {
  test("createTtlCache is re-exported and usable from the public entry point", async () => {
    const cache = createTtlCache({ ttlMs: 1000, keyFor: () => "k" });
    const wrapped = cache.wrap(async () => "v");
    expect(await wrapped()).toBe("v");
  });

  test("keyForParams is re-exported and usable from the public entry point", () => {
    expect(keyForParams({ b: "2", a: "1" })).toBe("a=1&b=2");
  });
});

describe("withIssueCaches public export (mitto-7gta.11)", () => {
  test("withIssueCaches is re-exported and decorates client.issues without mutating it", () => {
    const client = createClient({ fetch: noopFetch });
    const wrapped = withIssueCaches(client.issues, {});
    expect(typeof wrapped.show).toBe("function");
    expect(typeof wrapped.preload).toBe("function");
    expect(wrapped).not.toBe(client.issues);
    expect(client.issues.preload).toBeUndefined();
  });
});
