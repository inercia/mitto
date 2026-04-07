/**
 * Unit tests for configCache.js
 *
 * Covers:
 *   - TTL-based response cache (no redundant HTTP requests within TTL window)
 *   - In-flight Promise deduplication (thundering herd prevention)
 *   - force=true bypasses both caches
 *   - Error handling clears inflight so subsequent callers can retry
 *   - invalidateConfigCache resets the TTL cache
 *   - TTL expiry triggers a fresh fetch
 */

// In ESM mode (--experimental-vm-modules), `jest` is not auto-injected as a
// global — it must be imported explicitly from @jest/globals.
import { jest } from "@jest/globals";
import { fetchConfig, invalidateConfigCache } from "./configCache.js";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Build a minimal Headers-like object for use in fetch mocks. */
function makeHeaders(etag = null) {
  return { get: (name) => (name === "ETag" ? etag : null) };
}

/**
 * Creates a controllable fetch mock that doesn't resolve until you call settle().
 * Returns { mockFetch, settle } where settle(data) resolves or settle(null, err) rejects.
 */
function createDeferredFetch(responseData = { ok: true }) {
  let resolver, rejecter;
  const promise = new Promise((resolve, reject) => {
    resolver = resolve;
    rejecter = reject;
  });

  const mockFetch = jest.fn(() => promise);
  const settle = (data = responseData, error = null) => {
    if (error) {
      rejecter(error);
    } else {
      resolver({
        status: 200,
        headers: makeHeaders(),
        json: () => Promise.resolve(data),
      });
    }
  };

  return { mockFetch, settle };
}

/** Build an immediately-resolving fetch mock. */
function immediateOkFetch(data = {}) {
  return jest.fn(() =>
    Promise.resolve({
      status: 200,
      headers: makeHeaders(),
      json: () => Promise.resolve(data),
    }),
  );
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

beforeEach(() => {
  // Clear the TTL cache between tests so each test starts from a cold cache.
  // The inflight map self-clears as Promises settle (each test awaits its promises).
  invalidateConfigCache();
  // Ensure no API prefix noise
  window.mittoApiPrefix = "";
});

afterEach(() => {
  jest.restoreAllMocks();
  jest.useRealTimers();
});

// ---------------------------------------------------------------------------
// TTL cache
// ---------------------------------------------------------------------------

describe("TTL cache", () => {
  test("second call within TTL returns cached data without a new HTTP request", async () => {
    global.fetch = immediateOkFetch({ cached: true });

    const r1 = await fetchConfig();
    const r2 = await fetchConfig();

    expect(r1).toEqual({ cached: true });
    expect(r2).toEqual({ cached: true });
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("uses separate TTL cache entries for different ACP servers", async () => {
    global.fetch = immediateOkFetch({ data: "ok" });

    await fetchConfig("server-a");
    await fetchConfig("server-b");

    // Two distinct cache keys → two HTTP requests
    expect(global.fetch).toHaveBeenCalledTimes(2);

    // Third call for server-a should be a cache hit (no new request)
    await fetchConfig("server-a");
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test("re-fetches after TTL expires", async () => {
    jest.useFakeTimers();
    global.fetch = immediateOkFetch({ fresh: true });

    await fetchConfig();
    expect(global.fetch).toHaveBeenCalledTimes(1);

    // Advance past the 30s TTL
    jest.advanceTimersByTime(31_000);

    await fetchConfig();
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });
});

// ---------------------------------------------------------------------------
// In-flight deduplication
// ---------------------------------------------------------------------------

describe("in-flight deduplication", () => {
  test("concurrent calls with the same key share one HTTP request", async () => {
    const { mockFetch, settle } = createDeferredFetch({ shared: true });
    global.fetch = mockFetch;

    // Both calls issued before any response arrives
    const p1 = fetchConfig();
    const p2 = fetchConfig();

    // Only one outbound request should have been started
    expect(global.fetch).toHaveBeenCalledTimes(1);

    // Resolve the single in-flight request
    settle({ shared: true });
    const [r1, r2] = await Promise.all([p1, p2]);

    expect(r1).toEqual({ shared: true });
    expect(r2).toEqual({ shared: true });
    // Still exactly one HTTP request total
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("after in-flight resolves, next call is served from TTL cache", async () => {
    const { mockFetch, settle } = createDeferredFetch({ v: 1 });
    global.fetch = mockFetch;

    const p1 = fetchConfig();
    settle({ v: 1 });
    await p1;

    // inflight is now empty; TTL cache is populated
    await fetchConfig(); // should hit TTL cache
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("concurrent calls with different keys each make their own HTTP request", async () => {
    const { mockFetch: fa, settle: sa } = createDeferredFetch({ server: "a" });
    const { mockFetch: fb, settle: sb } = createDeferredFetch({ server: "b" });

    // Alternate responses by call order
    let callCount = 0;
    global.fetch = jest.fn(() => {
      return callCount++ === 0 ? fa() : fb();
    });

    const pa = fetchConfig("server-a");
    const pb = fetchConfig("server-b");

    // Different keys → different in-flight entries → 2 requests
    expect(global.fetch).toHaveBeenCalledTimes(2);

    sa({ server: "a" });
    sb({ server: "b" });
    const [ra, rb] = await Promise.all([pa, pb]);

    expect(ra).toEqual({ server: "a" });
    expect(rb).toEqual({ server: "b" });
  });
});


// ---------------------------------------------------------------------------
// force=true
// ---------------------------------------------------------------------------

describe("force=true", () => {
  test("bypasses TTL cache and re-fetches", async () => {
    global.fetch = immediateOkFetch({ v: 1 });
    await fetchConfig(); // populates TTL cache

    global.fetch = immediateOkFetch({ v: 2 });
    const result = await fetchConfig(null, true);

    expect(result).toEqual({ v: 2 });
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("does not join an existing in-flight request", async () => {
    const { mockFetch, settle } = createDeferredFetch({ v: "normal" });
    global.fetch = mockFetch;

    const pNormal = fetchConfig(); // starts in-flight
    expect(global.fetch).toHaveBeenCalledTimes(1);

    // force=true bypasses inflight deduplication → second HTTP request
    const pForced = fetchConfig(null, true);
    expect(global.fetch).toHaveBeenCalledTimes(2);

    settle({ v: "normal" });
    await Promise.all([pNormal, pForced]);
  });

  test("concurrent force=true calls each make their own HTTP request", async () => {
    global.fetch = immediateOkFetch({});

    const p1 = fetchConfig(null, true);
    const p2 = fetchConfig(null, true);

    expect(global.fetch).toHaveBeenCalledTimes(2);
    await Promise.all([p1, p2]);
  });
});

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

describe("error handling", () => {
  test("failed request removes inflight entry so next caller retries", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("network error")));
    await expect(fetchConfig()).rejects.toThrow("network error");

    // inflight was cleared on error — next call makes a fresh request
    global.fetch = immediateOkFetch({ retry: true });
    const result = await fetchConfig();

    expect(result).toEqual({ retry: true });
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("all concurrent callers reject when the shared in-flight request fails", async () => {
    const { mockFetch, settle } = createDeferredFetch();
    global.fetch = mockFetch;

    const p1 = fetchConfig();
    const p2 = fetchConfig();
    expect(global.fetch).toHaveBeenCalledTimes(1); // still deduplicated

    settle(null, new Error("server down"));

    await expect(p1).rejects.toThrow("server down");
    await expect(p2).rejects.toThrow("server down");
  });
});

// ---------------------------------------------------------------------------
// invalidateConfigCache
// ---------------------------------------------------------------------------

describe("invalidateConfigCache", () => {
  test("clears TTL cache so next call re-fetches from server", async () => {
    global.fetch = immediateOkFetch({ v: 1 });
    await fetchConfig(); // populate TTL cache

    invalidateConfigCache();

    global.fetch = immediateOkFetch({ v: 2 });
    const result = await fetchConfig();

    expect(result).toEqual({ v: 2 });
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("does not interfere with in-flight requests", async () => {
    const { mockFetch, settle } = createDeferredFetch({ v: 1 });
    global.fetch = mockFetch;

    const p = fetchConfig(); // in-flight
    invalidateConfigCache(); // clear cache while request is in-flight

    settle({ v: 1 });
    // The in-flight request still resolves normally
    await expect(p).resolves.toEqual({ v: 1 });
  });
});
