/**
 * Tests for beadsPreload.js (mitto-h8a.2).
 *
 * preloadBeadsIssues reaches the network via authFetch → global fetch, so
 * stubbing global.fetch is enough to count invocations without mocking ESM
 * modules — matching useBeadsKnownIds.test.js's approach.
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

describe("preloadBeadsIssues — no-op guards", () => {
  test("no-op when workingDir is falsy", () => {
    mod.preloadBeadsIssues(["mitto-aaa"], "");
    mod.preloadBeadsIssues(["mitto-aaa"], null);
    mod.preloadBeadsIssues(["mitto-aaa"], undefined);
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("no-op when ids is empty", () => {
    mod.preloadBeadsIssues([], "/tmp/wsA");
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("no-op when ids is not an array", () => {
    mod.preloadBeadsIssues(null, "/tmp/wsA");
    mod.preloadBeadsIssues(undefined, "/tmp/wsA");
    mod.preloadBeadsIssues("mitto-aaa", "/tmp/wsA");
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("skips empty/falsy IDs within the list but fires for the rest", () => {
    mod.preloadBeadsIssues(["", null, "mitto-aaa", undefined], "/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1);
    const [url] = currentFetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/mitto-aaa");
  });
});

describe("preloadBeadsIssues — fetch surface", () => {
  test("fires GET to /api/issues/{id} with working_dir query for each unique id", () => {
    mod.preloadBeadsIssues(["mitto-aaa", "mitto-bbb"], "/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(2);

    const urls = currentFetch.mock.calls.map((c) => String(c[0]));
    expect(urls.some((u) => u.includes("/api/issues/mitto-aaa"))).toBe(true);
    expect(urls.some((u) => u.includes("/api/issues/mitto-bbb"))).toBe(true);
    // working_dir is URL-encoded on the query string
    expect(
      urls.every((u) => u.includes(encodeURIComponent("/tmp/wsA"))),
    ).toBe(true);
  });

  test("swallows fetch rejection without throwing", async () => {
    currentFetch.mockImplementationOnce(() =>
      Promise.reject(new Error("network down")),
    );
    expect(() =>
      mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA"),
    ).not.toThrow();
    // Give the swallowed rejection a tick to settle so the unhandled-rejection
    // detector inside jest doesn't flag it.
    await Promise.resolve();
  });
});

describe("preloadBeadsIssues — dedup", () => {
  test("dedups within TTL: same id in same workspace only fires once", () => {
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    mod.preloadBeadsIssues(["mitto-aaa", "mitto-aaa"], "/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1);
  });

  test("re-fires after TTL window expires", () => {
    const realNow = Date.now;
    let clock = 1_000_000;
    Date.now = () => clock;
    try {
      mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
      expect(currentFetch).toHaveBeenCalledTimes(1);

      // Just under TTL — still deduped.
      clock += 29_000;
      mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
      expect(currentFetch).toHaveBeenCalledTimes(1);

      // Past TTL — fires again.
      clock += 2_000;
      mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
      expect(currentFetch).toHaveBeenCalledTimes(2);
    } finally {
      Date.now = realNow;
    }
  });

  test("per-workspace isolation: same id in different workspaces both fire", () => {
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsB");
    expect(currentFetch).toHaveBeenCalledTimes(2);

    const urls = currentFetch.mock.calls.map((c) => String(c[0]));
    expect(
      urls.some((u) => u.includes(encodeURIComponent("/tmp/wsA"))),
    ).toBe(true);
    expect(
      urls.some((u) => u.includes(encodeURIComponent("/tmp/wsB"))),
    ).toBe(true);
  });

  test("_resetBeadsPreloadCache clears dedup state", () => {
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1);
    mod._resetBeadsPreloadCache();
    mod.preloadBeadsIssues(["mitto-aaa"], "/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(2);
  });
});
