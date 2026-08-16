/**
 * Tests for beadsPreload.js (mitto-h8a.2; migrated onto the SDK client in
 * mitto-7gta.17 slice S1).
 *
 * preloadBeadsIssues reaches the network via getSdkClient().issues (wrapped
 * with withIssueCaches) → global fetch, so stubbing global.fetch is enough to
 * count invocations without mocking ESM modules — matching
 * useBeadsKnownIds.test.js's approach. Every assertion on `currentFetch`'s
 * call count is preceded by `await flush()` since the SDK's `request()`
 * awaits `config.auth.authorize()` before invoking `fetch`, unlike the old
 * `authFetch()` which called `fetch` synchronously.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "./testing/testGlobals.js";

// Minimal environment for the module and the transitive utils barrel it
// pulls in (csrf.js touches document.cookie; endpoints.js reads
// window.mittoApiPrefix).
global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let currentFetch;
let originalFetch;
let mod;

beforeEach(async () => {
  originalFetch = global.fetch;
  currentFetch = jest.fn(() =>
    Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}) }),
  );
  global.fetch = currentFetch;
  mod = await import("./beadsPreload.js");
  mod._resetBeadsPreloadCache();
});

afterEach(() => {
  global.fetch = originalFetch;
  jest.restoreAllMocks();
});

/**
 * Resolves after the current microtask queue drains. Needed because the SDK
 * client's `request()` (mitto-7gta.17 S1) awaits `config.auth.authorize()`
 * before invoking `fetch`.
 */
function flush() {
  return Promise.resolve();
}

describe("preloadBeadsIssues — no-op guards", () => {
  test("no-op when workingDir is falsy", async () => {
    mod.preloadBeadsIssues(["mitto-aaa"], "");
    mod.preloadBeadsIssues(["mitto-aaa"], null);
    mod.preloadBeadsIssues(["mitto-aaa"], undefined);
    await flush();
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("no-op when ids is empty", async () => {
    mod.preloadBeadsIssues([], "/tmp/wsA");
    await flush();
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("no-op when ids is not an array", async () => {
    mod.preloadBeadsIssues(null, "/tmp/wsA");
    mod.preloadBeadsIssues(undefined, "/tmp/wsA");
    mod.preloadBeadsIssues("mitto-aaa", "/tmp/wsA");
    await flush();
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("skips empty/falsy IDs within the list but fires for the rest", async () => {
    mod.preloadBeadsIssues(["", null, "mitto-aaa", undefined], "/tmp/wsA");
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(1);
    const [url] = currentFetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/mitto-aaa");
  });
});

describe("preloadBeadsIssues — fetch surface", () => {
  test("fires GET to /api/issues/{id} with working_dir query for each unique id", async () => {
    mod.preloadBeadsIssues(["mitto-aaa", "mitto-bbb"], "/tmp/wsA");
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(2);

    const urls = currentFetch.mock.calls.map((c) => String(c[0]));
    expect(urls.some((u) => u.includes("/api/issues/mitto-aaa"))).toBe(true);
    expect(urls.some((u) => u.includes("/api/issues/mitto-bbb"))).toBe(true);
    // working_dir is URL-encoded on the query string
    expect(urls.every((u) => u.includes(encodeURIComponent("/tmp/wsA")))).toBe(
      true,
    );
  });

  test("bounds concurrent cold detail preloads for a large linked-ID batch", async () => {
    // Regression for mitto-ddrb: keep every request pending so the call count
    // measures simultaneous browser fan-out rather than eventual throughput.
    currentFetch.mockImplementation(() => new Promise(() => {}));
    const ids = Array.from({ length: 40 }, (_, i) => `mitto-cold-${i}`);

    mod.preloadBeadsIssues(ids, "/tmp/wsA");
    await flush();

    expect(currentFetch.mock.calls.length).toBeLessThanOrEqual(2);
  });

  test("continues draining queued preloads when an active request finishes", async () => {
    const resolvers = [];
    currentFetch.mockImplementation(
      () => new Promise((resolve) => resolvers.push(resolve)),
    );
    mod.preloadBeadsIssues(
      ["mitto-first", "mitto-second", "mitto-third"],
      "/tmp/wsA",
    );
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(2);

    resolvers[0]({
      ok: true,
      status: 200,
      json: () => Promise.resolve({}),
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(currentFetch).toHaveBeenCalledTimes(3);
  });

  test("swallows fetch rejection without throwing", async () => {
    currentFetch.mockImplementationOnce(() =>
      Promise.reject(new Error("network down")),
    );
    expect(() =>
      mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA"),
    ).not.toThrow();
    // Give the swallowed rejection a few ticks to settle (SDK request() has
    // more await hops than the old direct fetch) so the unhandled-rejection
    // detector inside jest doesn't flag it.
    await flush();
    await flush();
    await flush();
  });
});

describe("preloadBeadsIssues — dedup", () => {
  test("dedups within TTL: same id in same workspace only fires once", async () => {
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    mod.preloadBeadsIssues(["mitto-aaa", "mitto-aaa"], "/tmp/wsA");
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(1);
  });

  test("re-fires after TTL window expires", async () => {
    const realNow = Date.now;
    let clock = 1_000_000;
    Date.now = () => clock;
    try {
      mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
      await flush();
      expect(currentFetch).toHaveBeenCalledTimes(1);

      // Just under TTL — still deduped.
      clock += 29_000;
      mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
      await flush();
      expect(currentFetch).toHaveBeenCalledTimes(1);

      // Past TTL — fires again.
      clock += 2_000;
      mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
      await flush();
      expect(currentFetch).toHaveBeenCalledTimes(2);
    } finally {
      Date.now = realNow;
    }
  });

  test("per-workspace isolation: same id in different workspaces both fire", async () => {
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsB");
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(2);

    const urls = currentFetch.mock.calls.map((c) => String(c[0]));
    expect(urls.some((u) => u.includes(encodeURIComponent("/tmp/wsA")))).toBe(
      true,
    );
    expect(urls.some((u) => u.includes(encodeURIComponent("/tmp/wsB")))).toBe(
      true,
    );
  });

  test("_resetBeadsPreloadCache clears dedup state", async () => {
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(1);
    mod._resetBeadsPreloadCache();
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(2);
  });
});
