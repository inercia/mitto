/**
 * Unit tests for promptsCache.js (mitto-8x9).
 *
 * Covers the three-level pattern mirrored from configCache.js, adapted to
 * workspace-prompts' full-param-set cache key:
 *   - TTL-based response cache (no redundant HTTP requests within TTL window)
 *   - Cache key isolation: different item_* / session_id params never share
 *     an entry (mitto-o0u.1 correctness requirement)
 *   - In-flight Promise deduplication (thundering herd prevention)
 *   - force=true bypasses both caches but still populates them
 *   - HTTP 304 (If-Modified-Since) returns the cached data and resets TTL
 *   - Error handling clears inflight so subsequent callers can retry
 *   - invalidateWorkspacePromptsCache: global clear and per-working_dir clear
 *   - Missing working_dir short-circuits without hitting the network
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "./testing/testGlobals.js";
import {
  fetchWorkspacePromptsCached,
  invalidateWorkspacePromptsCache,
} from "./promptsCache.js";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Build a minimal Headers-like object for use in fetch mocks. */
function makeHeaders(lastModified = null) {
  return { get: (name) => (name === "Last-Modified" ? lastModified : null) };
}

/** Build an immediately-resolving 200 fetch mock. */
function immediateOkFetch(body = { prompts: [], migrated: [] }, lm = null) {
  return jest.fn(() =>
    Promise.resolve({
      status: 200,
      ok: true,
      headers: makeHeaders(lm),
      json: () => Promise.resolve(body),
    }),
  );
}

/** Build an immediately-resolving 304 fetch mock (no body). */
function immediateNotModifiedFetch() {
  return jest.fn(() =>
    Promise.resolve({
      status: 304,
      ok: false,
      headers: makeHeaders(),
      json: () => Promise.reject(new Error("must not parse body on 304")),
    }),
  );
}

/**
 * Creates a controllable fetch mock that doesn't resolve until settle() is
 * called. Returns { mockFetch, settle } where settle(body, lm) resolves 200,
 * settle(null, null, err) rejects.
 */
function createDeferredFetch() {
  let resolver, rejecter;
  const promise = new Promise((resolve, reject) => {
    resolver = resolve;
    rejecter = reject;
  });

  const mockFetch = jest.fn(() => promise);
  const settle = (
    body = { prompts: [], migrated: [] },
    lm = null,
    err = null,
  ) => {
    if (err) {
      rejecter(err);
    } else {
      resolver({
        status: 200,
        ok: true,
        headers: makeHeaders(lm),
        json: () => Promise.resolve(body),
      });
    }
  };

  return { mockFetch, settle };
}

/**
 * Resolves after the current microtask queue drains. Needed because the SDK
 * client's `request()` (mitto-7gta.17 S1) awaits `config.auth.authorize()`
 * before invoking `fetch`, unlike the old `authFetch()` which called `fetch`
 * synchronously — so a fetch fired by a still-pending call is not yet
 * reflected in `global.fetch`'s mock call count until this tick passes.
 */
function flush() {
  return Promise.resolve();
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

beforeEach(() => {
  invalidateWorkspacePromptsCache();
  window.mittoApiPrefix = "";
});

afterEach(() => {
  jest.restoreAllMocks();
  jest.useRealTimers();
});

// ---------------------------------------------------------------------------
// Missing working_dir
// ---------------------------------------------------------------------------

describe("missing working_dir", () => {
  test("short-circuits to an empty result without calling fetch", async () => {
    global.fetch = jest.fn();

    const result = await fetchWorkspacePromptsCached({ session_id: "s1" });

    expect(result).toEqual({ prompts: [], migrated: [], lastModified: null });
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// TTL cache
// ---------------------------------------------------------------------------

describe("TTL cache", () => {
  test("second call within TTL for the same params returns cached data without a new HTTP request", async () => {
    global.fetch = immediateOkFetch({ prompts: [{ name: "a" }], migrated: [] });

    const r1 = await fetchWorkspacePromptsCached({ working_dir: "/w" });
    const r2 = await fetchWorkspacePromptsCached({ working_dir: "/w" });

    expect(r1.prompts).toEqual([{ name: "a" }]);
    expect(r2.prompts).toEqual([{ name: "a" }]);
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("re-fetches after TTL expires", async () => {
    jest.useFakeTimers();
    global.fetch = immediateOkFetch({ prompts: [{ name: "fresh" }] });

    await fetchWorkspacePromptsCached({ working_dir: "/w" });
    expect(global.fetch).toHaveBeenCalledTimes(1);

    // Advance past the 3000ms TTL.
    jest.advanceTimersByTime(3001);

    await fetchWorkspacePromptsCached({ working_dir: "/w" });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });
});

// ---------------------------------------------------------------------------
// Cache key isolation (mitto-o0u.1 correctness requirement)
// ---------------------------------------------------------------------------

describe("cache key isolation", () => {
  test("different item_id values never share a cache entry", async () => {
    global.fetch = immediateOkFetch({ prompts: [{ name: "row" }] });

    await fetchWorkspacePromptsCached({
      working_dir: "/w",
      item_kind: "beadsIssue",
      item_id: "mitto-1",
    });
    await fetchWorkspacePromptsCached({
      working_dir: "/w",
      item_kind: "beadsIssue",
      item_id: "mitto-2",
    });

    // Two distinct cache keys → two HTTP requests.
    expect(global.fetch).toHaveBeenCalledTimes(2);

    // Repeating the first row's params is now a cache hit.
    await fetchWorkspacePromptsCached({
      working_dir: "/w",
      item_kind: "beadsIssue",
      item_id: "mitto-1",
    });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test("different session_id values never share a cache entry", async () => {
    global.fetch = immediateOkFetch({ prompts: [] });

    await fetchWorkspacePromptsCached({ working_dir: "/w", session_id: "s1" });
    await fetchWorkspacePromptsCached({ working_dir: "/w", session_id: "s2" });

    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test("omitting a param and passing it as undefined/empty produce the same key", async () => {
    global.fetch = immediateOkFetch({ prompts: [{ name: "a" }] });

    await fetchWorkspacePromptsCached({
      working_dir: "/w",
      session_id: undefined,
    });
    // Same effective key (session_id omitted vs empty string) → cache hit.
    await fetchWorkspacePromptsCached({ working_dir: "/w", session_id: "" });

    expect(global.fetch).toHaveBeenCalledTimes(1);
  });
});

// ---------------------------------------------------------------------------
// In-flight deduplication
// ---------------------------------------------------------------------------

describe("in-flight deduplication", () => {
  test("concurrent calls with the same params share one HTTP request", async () => {
    const { mockFetch, settle } = createDeferredFetch();
    global.fetch = mockFetch;

    const p1 = fetchWorkspacePromptsCached({ working_dir: "/w" });
    const p2 = fetchWorkspacePromptsCached({ working_dir: "/w" });
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);

    settle({ prompts: [{ name: "shared" }], migrated: [] });
    const [r1, r2] = await Promise.all([p1, p2]);

    expect(r1.prompts).toEqual([{ name: "shared" }]);
    expect(r2.prompts).toEqual([{ name: "shared" }]);
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("concurrent calls with different params each make their own HTTP request", async () => {
    const { mockFetch: fa, settle: sa } = createDeferredFetch();
    const { mockFetch: fb, settle: sb } = createDeferredFetch();

    let callCount = 0;
    global.fetch = jest.fn(() => (callCount++ === 0 ? fa() : fb()));

    const pa = fetchWorkspacePromptsCached({ working_dir: "/a" });
    const pb = fetchWorkspacePromptsCached({ working_dir: "/b" });
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(2);

    sa({ prompts: [{ name: "a" }] });
    sb({ prompts: [{ name: "b" }] });
    const [ra, rb] = await Promise.all([pa, pb]);

    expect(ra.prompts).toEqual([{ name: "a" }]);
    expect(rb.prompts).toEqual([{ name: "b" }]);
  });
});

// ---------------------------------------------------------------------------
// force=true
// ---------------------------------------------------------------------------

describe("force=true", () => {
  test("bypasses TTL cache and re-fetches, still repopulating the cache", async () => {
    global.fetch = immediateOkFetch({ prompts: [{ name: "v1" }] });
    await fetchWorkspacePromptsCached({ working_dir: "/w" }); // populates TTL cache

    global.fetch = immediateOkFetch({ prompts: [{ name: "v2" }] });
    const forced = await fetchWorkspacePromptsCached(
      { working_dir: "/w" },
      { force: true },
    );
    expect(forced.prompts).toEqual([{ name: "v2" }]);
    expect(global.fetch).toHaveBeenCalledTimes(1);

    // Subsequent non-force call is served from the cache the forced call
    // just populated — no further HTTP request.
    global.fetch = jest.fn();
    const after = await fetchWorkspacePromptsCached({ working_dir: "/w" });
    expect(after.prompts).toEqual([{ name: "v2" }]);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("does not join an existing in-flight request", async () => {
    const { mockFetch, settle } = createDeferredFetch();
    global.fetch = mockFetch;

    const pNormal = fetchWorkspacePromptsCached({ working_dir: "/w" });
    await flush();
    expect(global.fetch).toHaveBeenCalledTimes(1);

    const pForced = fetchWorkspacePromptsCached(
      { working_dir: "/w" },
      { force: true },
    );
    await flush();
    expect(global.fetch).toHaveBeenCalledTimes(2);

    settle({ prompts: [] });
    await Promise.all([pNormal, pForced]);
  });
});

// ---------------------------------------------------------------------------
// 304 Not Modified revalidation
// ---------------------------------------------------------------------------

describe("304 revalidation", () => {
  test("sends If-Modified-Since once a Last-Modified value is cached, and a 304 returns the cached prompts", async () => {
    global.fetch = immediateOkFetch(
      { prompts: [{ name: "cached" }], migrated: [] },
      "Tue, 04 Aug 2026 12:00:00 GMT",
    );
    const first = await fetchWorkspacePromptsCached({ working_dir: "/w" });
    expect(first.prompts).toEqual([{ name: "cached" }]);
    expect(first.lastModified).toBe("Tue, 04 Aug 2026 12:00:00 GMT");

    // Expire the TTL so the next call revalidates instead of hitting the
    // completed-response cache directly.
    jest.useFakeTimers();
    jest.advanceTimersByTime(3001);

    global.fetch = immediateNotModifiedFetch();
    const second = await fetchWorkspacePromptsCached({ working_dir: "/w" });

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [, options] = global.fetch.mock.calls[0];
    expect(options.headers["If-Modified-Since"]).toBe(
      "Tue, 04 Aug 2026 12:00:00 GMT",
    );
    // 304 → same cached prompts returned, not an empty list.
    expect(second.prompts).toEqual([{ name: "cached" }]);
  });

  test("a 304 resets the TTL window so a subsequent immediate call is a cache hit", async () => {
    jest.useFakeTimers();
    global.fetch = immediateOkFetch(
      { prompts: [{ name: "x" }] },
      "Tue, 04 Aug 2026 12:00:00 GMT",
    );
    await fetchWorkspacePromptsCached({ working_dir: "/w" });

    jest.advanceTimersByTime(3001);
    global.fetch = immediateNotModifiedFetch();
    await fetchWorkspacePromptsCached({ working_dir: "/w" }); // revalidates via 304

    // Immediately after the 304, the TTL window was reset — next call is a
    // pure cache hit with no network request.
    global.fetch = jest.fn();
    const third = await fetchWorkspacePromptsCached({ working_dir: "/w" });
    expect(third.prompts).toEqual([{ name: "x" }]);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("force=true never sends If-Modified-Since even when a Last-Modified value is cached", async () => {
    global.fetch = immediateOkFetch(
      { prompts: [{ name: "a" }] },
      "Tue, 04 Aug 2026 12:00:00 GMT",
    );
    await fetchWorkspacePromptsCached({ working_dir: "/w" });

    global.fetch = immediateOkFetch({ prompts: [{ name: "b" }] });
    await fetchWorkspacePromptsCached({ working_dir: "/w" }, { force: true });

    const [, options] = global.fetch.mock.calls[0];
    expect(options.headers["If-Modified-Since"]).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

describe("error handling", () => {
  test("failed request removes the in-flight entry so the next caller retries", async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error("network error")));
    await expect(
      fetchWorkspacePromptsCached({ working_dir: "/w" }),
    ).rejects.toThrow("network error");

    global.fetch = immediateOkFetch({ prompts: [{ name: "retry" }] });
    const result = await fetchWorkspacePromptsCached({ working_dir: "/w" });

    expect(result.prompts).toEqual([{ name: "retry" }]);
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("all concurrent callers reject when the shared in-flight request fails", async () => {
    const { mockFetch, settle } = createDeferredFetch();
    global.fetch = mockFetch;

    const p1 = fetchWorkspacePromptsCached({ working_dir: "/w" });
    const p2 = fetchWorkspacePromptsCached({ working_dir: "/w" });
    p1.catch(() => {});
    p2.catch(() => {});
    await flush();
    expect(global.fetch).toHaveBeenCalledTimes(1);

    settle(null, null, new Error("server down"));

    await expect(p1).rejects.toThrow("server down");
    await expect(p2).rejects.toThrow("server down");
  });

  test("a non-ok, non-304 response rejects and clears the in-flight entry", async () => {
    global.fetch = jest.fn(() =>
      Promise.resolve({
        status: 500,
        ok: false,
        headers: makeHeaders(),
        text: () => Promise.resolve(""),
        json: () => Promise.resolve({}),
      }),
    );

    await expect(
      fetchWorkspacePromptsCached({ working_dir: "/w" }),
    ).rejects.toThrow("Request failed with status 500");

    global.fetch = immediateOkFetch({ prompts: [{ name: "ok" }] });
    const result = await fetchWorkspacePromptsCached({ working_dir: "/w" });
    expect(result.prompts).toEqual([{ name: "ok" }]);
  });
});

// ---------------------------------------------------------------------------
// invalidateWorkspacePromptsCache
// ---------------------------------------------------------------------------

describe("invalidateWorkspacePromptsCache", () => {
  test("with no argument clears every entry regardless of working_dir", async () => {
    global.fetch = immediateOkFetch({ prompts: [{ name: "a" }] });
    await fetchWorkspacePromptsCached({ working_dir: "/a" });
    await fetchWorkspacePromptsCached({ working_dir: "/b" });
    expect(global.fetch).toHaveBeenCalledTimes(2);

    invalidateWorkspacePromptsCache();

    global.fetch = immediateOkFetch({ prompts: [{ name: "fresh" }] });
    await fetchWorkspacePromptsCached({ working_dir: "/a" });
    await fetchWorkspacePromptsCached({ working_dir: "/b" });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test("with a working_dir argument only clears entries for that directory", async () => {
    global.fetch = immediateOkFetch({ prompts: [{ name: "a" }] });
    await fetchWorkspacePromptsCached({ working_dir: "/a" });
    await fetchWorkspacePromptsCached({ working_dir: "/b" });

    invalidateWorkspacePromptsCache("/a");

    // /a was cleared → re-fetches.
    global.fetch = immediateOkFetch({ prompts: [{ name: "a2" }] });
    const ra = await fetchWorkspacePromptsCached({ working_dir: "/a" });
    expect(ra.prompts).toEqual([{ name: "a2" }]);
    expect(global.fetch).toHaveBeenCalledTimes(1);

    // /b is untouched → still a cache hit, no network call.
    global.fetch = jest.fn();
    const rb = await fetchWorkspacePromptsCached({ working_dir: "/b" });
    expect(rb.prompts).toEqual([{ name: "a" }]);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("does not interfere with an in-flight request", async () => {
    const { mockFetch, settle } = createDeferredFetch();
    global.fetch = mockFetch;

    const p = fetchWorkspacePromptsCached({ working_dir: "/w" });
    invalidateWorkspacePromptsCache();

    settle({ prompts: [{ name: "still-resolves" }] });
    await expect(p).resolves.toEqual({
      prompts: [{ name: "still-resolves" }],
      migrated: [],
      lastModified: null,
    });
  });
});
