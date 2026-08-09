/**
 * Unit tests for the generic TTL cache decorator (mitto-7gta.10).
 */
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
