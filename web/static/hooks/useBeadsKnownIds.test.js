/**
 * Tests for useBeadsKnownIds.js (mitto-h8a.1).
 *
 * The hook destructures `useEffect` from `window.preact` at module-load
 * time, so we stub `window.preact.useEffect` to capture the effect's
 * callback + deps, invoke the callback ourselves, and then assert on the
 * side effects (fetch calls, listener registration, cleanup).
 *
 * `fetchAndCacheBeadsIds` reaches the network via `authFetch` → global
 * `fetch`, so stubbing `global.fetch` is enough to count invocations
 * without mocking ESM modules — matching the codebase's other hook tests
 * (see useBeadsFolderConfig.test.js).
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "../utils/testing/testGlobals.js";

// Minimal environment for the module and the transitive utils barrel it
// pulls in (csrf.js touches document.cookie; endpoints.js reads
// window.mittoApiPrefix).
global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

// The hook destructures `useEffect` from `window.preact` at MODULE-LOAD
// time. Install a stub once — before the first import — whose `useEffect`
// pushes into a mutable bucket we reset per test.
let currentEffects = [];
window.preact = {
  ...(window.preact || {}),
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};

// Stub timers so setInterval(..., 60_000) doesn't try to fire in test.
// We only care that fetchAndCacheBeadsIds was invoked once at mount and
// once per matching event — never that the safety-net poll fires.
let currentFetch;
let originalFetch;

// Cleanups from every mount in the current test. Ran in afterEach so
// listeners registered against the module-global `window` don't leak
// across tests (the source module is ESM-cached, so listeners survive
// unless explicitly removed).
let pendingCleanups = [];

async function loadHook() {
  currentEffects = [];
  const mod = await import("./useBeadsKnownIds.js");
  return { useBeadsKnownIds: mod.useBeadsKnownIds };
}

beforeEach(() => {
  originalFetch = global.fetch;
  currentFetch = jest.fn(() =>
    Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve([]),
    }),
  );
  global.fetch = currentFetch;
  pendingCleanups = [];
});

afterEach(() => {
  for (const c of pendingCleanups) {
    try {
      c && c();
    } catch (_) {
      // ignore
    }
  }
  pendingCleanups = [];
  global.fetch = originalFetch;
  jest.restoreAllMocks();
});

// Helper: mount the hook, then run its single effect body synchronously
// and return the cleanup function it returned. The cleanup is also
// registered for afterEach so listeners can't leak between tests.
async function mountEffect(workingDir) {
  const { useBeadsKnownIds } = await loadHook();
  useBeadsKnownIds(workingDir);
  expect(currentEffects.length).toBe(1);
  const { cb } = currentEffects[0];
  const cleanup = cb();
  pendingCleanups.push(cleanup);
  return { cleanup };
}

describe("useBeadsKnownIds — mount", () => {
  test("fetches once for the given workingDir on mount", async () => {
    await mountEffect("/tmp/wsA");

    // fetchAndCacheBeadsIds → authFetch → global.fetch
    expect(currentFetch).toHaveBeenCalledTimes(1);
    const [url] = currentFetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues");
    expect(String(url)).toContain(encodeURIComponent("/tmp/wsA"));
  });

  test("no-op when workingDir is empty (guard short-circuits before fetch)", async () => {
    const { useBeadsKnownIds } = await loadHook();
    useBeadsKnownIds("");
    // Effect body ran but returned early; there is nothing to clean up.
    const { cb } = currentEffects[0];
    const ret = cb();
    expect(ret).toBeUndefined();
    expect(currentFetch).not.toHaveBeenCalled();
  });
});

describe("useBeadsKnownIds — mitto:beads_changed listener", () => {
  test("refetches when event's working_dirs includes the current workingDir", async () => {
    await mountEffect("/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1); // mount

    window.dispatchEvent(
      new CustomEvent("mitto:beads_changed", {
        detail: { working_dirs: ["/tmp/wsA", "/tmp/other"] },
      }),
    );
    // Allow the async fetch chain to settle.
    await Promise.resolve();

    expect(currentFetch).toHaveBeenCalledTimes(2);
  });

  test("does NOT refetch when working_dirs is an array excluding the current workingDir", async () => {
    await mountEffect("/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1);

    window.dispatchEvent(
      new CustomEvent("mitto:beads_changed", {
        detail: { working_dirs: ["/tmp/other"] },
      }),
    );
    await Promise.resolve();

    expect(currentFetch).toHaveBeenCalledTimes(1);
  });

  test("refetches on broadcast event with missing working_dirs (undefined)", async () => {
    await mountEffect("/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1);

    window.dispatchEvent(
      new CustomEvent("mitto:beads_changed", { detail: {} }),
    );
    await Promise.resolve();

    expect(currentFetch).toHaveBeenCalledTimes(2);
  });

  test("refetches on broadcast event with non-array working_dirs", async () => {
    await mountEffect("/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1);

    window.dispatchEvent(
      new CustomEvent("mitto:beads_changed", {
        detail: { working_dirs: "not-an-array" },
      }),
    );
    await Promise.resolve();

    expect(currentFetch).toHaveBeenCalledTimes(2);
  });

  test("refetches on event with no detail at all", async () => {
    await mountEffect("/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1);

    // Plain Event, no detail — hook must tolerate e.detail being undefined
    // (optional chaining in the source).
    window.dispatchEvent(new Event("mitto:beads_changed"));
    await Promise.resolve();

    expect(currentFetch).toHaveBeenCalledTimes(2);
  });
});

describe("useBeadsKnownIds — cleanup", () => {
  test("removes the mitto:beads_changed listener on unmount", async () => {
    const { cleanup } = await mountEffect("/tmp/wsA");
    expect(currentFetch).toHaveBeenCalledTimes(1);

    // Run cleanup (simulating unmount / workingDir change).
    cleanup();

    // A subsequent event must NOT trigger any further fetch, since the
    // listener has been removed.
    window.dispatchEvent(
      new CustomEvent("mitto:beads_changed", {
        detail: { working_dirs: ["/tmp/wsA"] },
      }),
    );
    await Promise.resolve();

    expect(currentFetch).toHaveBeenCalledTimes(1);
  });
});
