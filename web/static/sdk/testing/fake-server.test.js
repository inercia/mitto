/**
 * Unit tests for the shared fake-server fixture (mitto-7gta.23).
 *
 * This fixture is infrastructure every resource test depends on, so it
 * gets its own direct coverage rather than being trusted implicitly.
 */
import {
  apiFailure,
  authFailure,
  createFakeServer,
  fakeResponse,
  mountResource,
  networkFailure,
} from "./fake-server.js";

describe("fakeResponse", () => {
  test("a JSON body sets content-type and JSON-encodes text()", async () => {
    const res = fakeResponse({ body: { a: 1 } });
    expect(res.ok).toBe(true);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("application/json");
    expect(await res.text()).toBe('{"a":1}');
  });

  test("no body and no text yields an empty text() with a null content-type", async () => {
    const res = fakeResponse({ status: 204 });
    expect(res.headers.get("content-type")).toBeNull();
    expect(await res.text()).toBe("");
  });

  test("raw text is returned as-is when body is omitted", async () => {
    const res = fakeResponse({ text: "plain" });
    expect(await res.text()).toBe("plain");
  });

  test("explicit headers[content-type] wins over the JSON-body default", async () => {
    const res = fakeResponse({ body: { a: 1 }, headers: { "Content-Type": "text/plain" } });
    expect(res.headers.get("content-type")).toBe("text/plain");
  });

  test("headers lookup is case-insensitive for both set and get", async () => {
    const res = fakeResponse({ headers: { "X-Foo": "bar" } });
    expect(res.headers.get("x-foo")).toBe("bar");
  });

  test("ok reflects the 2xx range boundaries", () => {
    expect(fakeResponse({ status: 199 }).ok).toBe(false);
    expect(fakeResponse({ status: 200 }).ok).toBe(true);
    expect(fakeResponse({ status: 299 }).ok).toBe(true);
    expect(fakeResponse({ status: 300 }).ok).toBe(false);
  });
});

describe("createFakeServer", () => {
  test("defaults every call to a bare 204", async () => {
    const { config, calls } = createFakeServer();
    const res = await config.fetch("/x", { method: "GET" });
    expect(res.status).toBe(204);
    expect(calls).toEqual([{ url: "/x", init: { method: "GET" } }]);
  });

  test("respondWith affects every subsequent call", async () => {
    const server = createFakeServer();
    let n = 0;
    server.respondWith(() => fakeResponse({ body: { n: ++n } }));
    const r1 = await server.config.fetch("/x", {});
    const r2 = await server.config.fetch("/x", {});
    expect(await r1.text()).toBe('{"n":1}');
    expect(await r2.text()).toBe('{"n":2}');
  });

  test("respondOnce answers exactly one call then reverts", async () => {
    const { config } = createFakeServer();
    const server = createFakeServer();
    server.respondWith(() => fakeResponse({ body: "default" }));
    server.respondOnce(() => fakeResponse({ body: "once" }));
    const r1 = await server.config.fetch("/x", {});
    const r2 = await server.config.fetch("/x", {});
    expect(await r1.text()).toBe('"once"');
    expect(await r2.text()).toBe('"default"');
  });

  test("respondTo dispatches by METHOD + url", async () => {
    const server = createFakeServer();
    server.respondTo({ "GET /api/a": { ok: 1 }, "POST /api/a": { ok: 2 } });
    const rGet = await server.config.fetch("/api/a", { method: "GET" });
    const rPost = await server.config.fetch("/api/a", { method: "POST" });
    expect(await rGet.text()).toBe('{"ok":1}');
    expect(await rPost.text()).toBe('{"ok":2}');
  });

  test("respondTo throws for an unregistered route", async () => {
    const server = createFakeServer();
    server.respondTo({});
    await expect(server.config.fetch("/nope", { method: "GET" })).rejects.toThrow(
      /no route registered/,
    );
  });

  test("lastCall() returns the most recent recorded call", async () => {
    const server = createFakeServer();
    await server.config.fetch("/a", {});
    await server.config.fetch("/b", {});
    expect(server.lastCall().url).toBe("/b");
  });

  test("reset() clears calls and reverts to the default 204 responder", async () => {
    const server = createFakeServer();
    server.respondWith(() => fakeResponse({ body: "x" }));
    await server.config.fetch("/a", {});
    server.reset();
    expect(server.calls).toEqual([]);
    const res = await server.config.fetch("/a", {});
    expect(res.status).toBe(204);
  });

  test("extra options (e.g. apiPrefix) are forwarded to resolveConfig", () => {
    const { config } = createFakeServer({ apiPrefix: "/mitto" });
    expect(config.apiPrefix).toBe("/mitto");
  });
});

describe("mountResource", () => {
  test("mounts a factory onto a fresh fake server", async () => {
    const { resource, calls } = mountResource((config) => ({
      ping: () => config.fetch("/ping", { method: "GET" }),
    }));
    await resource.ping();
    expect(calls[0].url).toBe("/ping");
  });
});

describe("failure injectors", () => {
  test("networkFailure() throws synchronously from the responder", () => {
    const responder = networkFailure("offline");
    expect(responder).toThrow("offline");
  });

  test("authFailure() answers a 401 with a nested error envelope", async () => {
    const res = authFailure()();
    expect(res.status).toBe(401);
    expect(JSON.parse(await res.text())).toEqual({
      error: { code: "unauthenticated", message: "Authentication required" },
    });
  });

  test("apiFailure() answers the given status with code/message/extra", async () => {
    const res = apiFailure(409, "conflict", "already exists", { field: "name" })();
    expect(res.status).toBe(409);
    expect(JSON.parse(await res.text())).toEqual({
      error: { code: "conflict", message: "already exists", field: "name" },
    });
  });
});
