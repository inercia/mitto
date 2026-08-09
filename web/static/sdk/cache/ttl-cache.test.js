/**
 * Unit tests for the generic TTL cache decorator (mitto-7gta.10).
 */
import { resolveConfig } from "../core/config.js";
import { createConfigResource } from "../resources/config.js";
import { createTtlCache, keyForParams } from "./ttl-cache.js";

describe("keyForParams", () => {
  test("sorts keys and omits null/undefined/empty values", () => {
    expect(keyForParams({ b: "2", a: "1", c: null, d: undefined, e: "" })).toBe(
      "a=1&b=2",
    );
  });

  test("undefined-vs-omitted keys are identical", () => {
    expect(keyForParams({ a: "1", item_id: undefined })).toBe(
      keyForParams({ a: "1" }),
    );
  });

  test("distinct item_* values produce distinct keys", () => {
    expect(keyForParams({ item_id: "1" })).not.toBe(keyForParams({ item_id: "2" }));
  });

  test("no params (or all empty) falls back to a stable default key", () => {
    expect(keyForParams()).toBe("__default__");
    expect(keyForParams({})).toBe("__default__");
  });
});

describe("createTtlCache — no revalidation", () => {
  function mkCounter(ttlMs = 1000) {
    let calls = 0;
    const cache = createTtlCache({ ttlMs, keyFor: (id) => String(id) });
    const fetchOne = cache.wrap(async (id) => {
      calls++;
      return { id, calls };
    });
    return { cache, fetchOne, getCalls: () => calls };
  }

  test("a fresh call within the TTL is served from cache without refetching", async () => {
    const { fetchOne, getCalls } = mkCounter();
    const a = await fetchOne("x");
    const b = await fetchOne("x");
    expect(a).toBe(b);
    expect(getCalls()).toBe(1);
  });

  test("expiry triggers a refetch", async () => {
    const { fetchOne, getCalls } = mkCounter(1);
    await fetchOne("x");
    await new Promise((r) => setTimeout(r, 5));
    await fetchOne("x");
    expect(getCalls()).toBe(2);
  });

  test("concurrent callers for the same key collapse into one fetch (thundering herd)", async () => {
    const { fetchOne, getCalls } = mkCounter();
    const [a, b, c] = await Promise.all([fetchOne("x"), fetchOne("x"), fetchOne("x")]);
    expect(getCalls()).toBe(1);
    expect(a).toEqual(b);
    expect(b).toEqual(c);
  });

  test("in-flight entry is cleared on rejection so the next call retries", async () => {
    let attempt = 0;
    const cache = createTtlCache({ ttlMs: 1000, keyFor: () => "k" });
    const fetchOne = cache.wrap(async () => {
      attempt++;
      if (attempt === 1) throw new Error("boom");
      return "ok";
    });
    await expect(fetchOne()).rejects.toThrow("boom");
    await expect(fetchOne()).resolves.toBe("ok");
    expect(attempt).toBe(2);
  });

  test("force bypasses the cache but still repopulates it", async () => {
    const { fetchOne, getCalls } = mkCounter();
    await fetchOne("x");
    await fetchOne("x", { force: true });
    expect(getCalls()).toBe(2);
    // A subsequent non-forced call reuses the repopulated entry.
    await fetchOne("x");
    expect(getCalls()).toBe(2);
  });

  test("invalidate() with no predicate clears everything", async () => {
    const { cache, fetchOne, getCalls } = mkCounter();
    await fetchOne("x");
    cache.invalidate();
    await fetchOne("x");
    expect(getCalls()).toBe(2);
  });

  test("invalidate(predicate) clears only matching keys", async () => {
    const { cache, fetchOne, getCalls } = mkCounter();
    await fetchOne("a");
    await fetchOne("b");
    cache.invalidate((key) => key === "a");
    await fetchOne("a");
    await fetchOne("b");
    expect(getCalls()).toBe(3);
  });
});

describe("createTtlCache — conditional revalidation", () => {
  function mkRevalidating(ttlMs = 1) {
    let calls = 0;
    const cache = createTtlCache({
      ttlMs,
      keyFor: () => "k",
      revalidate: {
        header: (record) => (record.etag ? { name: "If-None-Match", value: record.etag } : null),
        isUnchanged: (response) => response.status === 304,
        extract: (response, data) => ({ payload: data, etag: response.etag }),
        value: (record) => record.payload,
      },
    });
    const fetchOne = cache.wrap(async (header) => {
      calls++;
      if (header && header.value === "etag-1") {
        return { response: { status: 304 }, data: null };
      }
      return { response: { status: 200, etag: "etag-1" }, data: { v: calls } };
    });
    return { fetchOne, getCalls: () => calls };
  }

  test("a 304 after TTL expiry keeps serving the cached payload and resets the TTL", async () => {
    const { fetchOne, getCalls } = mkRevalidating();
    const first = await fetchOne();
    await new Promise((r) => setTimeout(r, 5));
    const second = await fetchOne();
    expect(second).toEqual(first);
    expect(getCalls()).toBe(2); // second call hit the network (304), but reused the payload
  });

  test("a fresh 200 replaces the cached payload", async () => {
    let calls = 0;
    const cache = createTtlCache({
      ttlMs: 1,
      keyFor: () => "k",
      revalidate: {
        header: () => null,
        isUnchanged: (response) => response.status === 304,
        extract: (response, data) => ({ payload: data, etag: response.etag }),
        value: (record) => record.payload,
      },
    });
    const fetchOne = cache.wrap(async () => {
      calls++;
      return { response: { status: 200, etag: `e${calls}` }, data: { v: calls } };
    });
    const first = await fetchOne();
    await new Promise((r) => setTimeout(r, 5));
    const second = await fetchOne();
    expect(second).not.toEqual(first);
    expect(second).toEqual({ v: 2 });
  });
});

describe("createTtlCache — real transport integration (mitto-7gta.10)", () => {
  function fakeConfigResponse({ status = 200, body, etag } = {}) {
    const hasBody = body !== undefined;
    return {
      ok: status >= 200 && status < 300,
      status,
      headers: {
        get: (name) => {
          const n = name.toLowerCase();
          if (n === "etag") return etag ?? null;
          if (n === "content-type") return hasBody ? "application/json" : null;
          return null;
        },
      },
      text: async () => (hasBody ? JSON.stringify(body) : ""),
    };
  }

  // Proves the decorator composes with the REAL `request()` primitive (via
  // `resources/config.js`'s `raw`/`allowStatus` passthrough), reproducing
  // utils/configCache.js's ETag / If-None-Match / 304 semantics end-to-end —
  // not just against a fully-faked wrapped function like the suite above.
  test("wrapping client.serverConfig.get with createTtlCache reproduces configCache.js's ETag/If-None-Match revalidation over the real transport", async () => {
    let calls = 0;
    const seenIfNoneMatch = [];
    const fetchImpl = async (_url, init) => {
      calls++;
      seenIfNoneMatch.push(init.headers["If-None-Match"]);
      if (init.headers["If-None-Match"] === "etag-1") {
        return fakeConfigResponse({ status: 304 });
      }
      return fakeConfigResponse({
        status: 200,
        body: { web: { theme: "dark" } },
        etag: "etag-1",
      });
    };
    const config = resolveConfig({ fetch: fetchImpl }, {});
    const serverConfig = createConfigResource(config);

    const cache = createTtlCache({
      ttlMs: 1,
      keyFor: () => "k",
      revalidate: {
        header: (record) =>
          record.etag ? { name: "If-None-Match", value: record.etag } : null,
        isUnchanged: (response) => response.status === 304,
        extract: (response, data) => ({
          payload: data,
          etag: response.headers.get("ETag"),
        }),
        value: (record) => record.payload,
      },
    });

    // getConfig() is always called with zero real arguments here, so the
    // decorator's trailing revalidation header lands as this function's
    // *only* parameter (see createTtlCache's `fn(...fnArgs, revalidationHeader)`
    // call convention — fnArgs is empty in this test).
    const getConfig = cache.wrap(async (revalidationHeader) => {
      const headers = revalidationHeader
        ? { [revalidationHeader.name]: revalidationHeader.value }
        : undefined;
      const response = await serverConfig.get(undefined, {
        raw: true,
        allowStatus: [304],
        headers,
      });
      const data = response.status === 304 ? null : JSON.parse(await response.text());
      return { response, data };
    });

    const first = await getConfig();
    expect(first).toEqual({ web: { theme: "dark" } });
    expect(calls).toBe(1);
    expect(seenIfNoneMatch[0]).toBeUndefined();

    await new Promise((r) => setTimeout(r, 5)); // let the 1ms TTL expire

    const second = await getConfig();
    expect(second).toEqual(first); // still served from cache after a 304
    expect(calls).toBe(2); // the revalidation request DID hit the network
    expect(seenIfNoneMatch[1]).toBe("etag-1"); // real If-None-Match header sent
  });
});
