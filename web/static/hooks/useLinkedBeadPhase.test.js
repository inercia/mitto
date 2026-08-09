/**
 * Tests for useLinkedBeadPhase.js (mitto-msv).
 *
 * The hook is the dominant driver of the 404 poll-storm this bead addresses:
 * SessionItem renders it per visible session row, and on every
 * `mitto:beads_changed` broadcast the hook invalidates its cache and re-fetches
 * for every mounted id. Two guards land the fix:
 *   1) an `archived` parameter that short-circuits the effect entirely for
 *      archived sessions, and
 *   2) a shared negative cache (beadsGoneCache) consulted BEFORE the fetch and
 *      populated on 404, so a beads_changed re-fire for a known-gone id costs
 *      nothing.
 *
 * The hook destructures `useEffect` from `window.preact` at MODULE-LOAD time,
 * matching useBeadsKnownIds.test.js: we install a preact stub whose useEffect
 * captures (cb, deps), invoke cb ourselves, and assert on side effects.
 * `useState` also comes from that stub — we return a benign [state, setState]
 * shape so the hook can call `setState(null)` without throwing.
 *
 * mitto-7gta.17 slice S3: the hook now reaches the network via
 * `getSdkClient().issues` (wrapped with `withIssueCaches`) instead of
 * `authFetch`, but the SDK client's `fetch` option is late-bound (see
 * utils/sdkClient.js), so stubbing `global.fetch` still works. Responses are
 * built with the shared `fakeResponse` fixture (not hand-rolled
 * `{ok,status,json}` objects) because `sdk/core/transport.js`'s `decodeBody`
 * unconditionally calls `response.text()` — a mock without it throws inside
 * `request()`, which the hook's `fetchIssue` catch-all then silently
 * swallows into `null`/no-markGone, masking the intended 404 behavior.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "../utils/testing/testGlobals.js";
import { fakeResponse } from "../sdk/testing/fake-server.js";

// Minimal environment for the module and the transitive utils barrel it pulls
// in (csrf.js touches document.cookie; endpoints.js reads
// window.mittoApiPrefix).
global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let currentEffects = [];
window.preact = {
  ...(window.preact || {}),
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
  // useState is called by the hook body itself; return a stable tuple so the
  // hook can setState(null) freely without triggering a re-render loop.
  useState: (initial) => [initial, () => {}],
};

let currentFetch;
let originalFetch;
let pendingCleanups = [];
let hookMod;
let goneMod;

async function loadModules() {
  currentEffects = [];
  // Import fresh handles each test — module state itself is cached by the ESM
  // loader, so we reset the caches via _resetBeadsGoneCache and by clearing
  // the module's own dedup Map through the effect lifecycle (unmount).
  hookMod = await import("./useLinkedBeadPhase.js");
  goneMod = await import("../utils/beadsGoneCache.js");
  goneMod._resetBeadsGoneCache();
}

beforeEach(async () => {
  originalFetch = global.fetch;
  currentFetch = jest.fn(() =>
    Promise.resolve(
      fakeResponse({
        status: 200,
        body: { issue_type: "feature", labels: ["planned"], status: "open" },
      }),
    ),
  );
  global.fetch = currentFetch;
  pendingCleanups = [];
  await loadModules();
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

// Mount the hook and run its single effect body synchronously. Returns the
// cleanup for the caller (also queued into pendingCleanups so listeners on
// window can't leak across tests).
function mount(issueId, workingDir, archived) {
  hookMod.useLinkedBeadPhase(issueId, workingDir, archived);
  expect(currentEffects.length).toBeGreaterThanOrEqual(1);
  const { cb } = currentEffects[currentEffects.length - 1];
  const cleanup = cb();
  if (typeof cleanup === "function") pendingCleanups.push(cleanup);
  return cleanup;
}

// Await enough microtask ticks that the getOrFetch → fetchIssue → .then chain
// (SDK request(): auth.authorize(), fetch, decodeBody, cache-store) fully
// settles before the assertion runs.
async function flush() {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

// The hook's own module-level dedup Map (keyed by `${workingDir}|${issueId}`)
// is NOT exported for reset, so each test uses a unique issueId to avoid
// cross-test cache hits masking the fetch behavior under assertion.
let idCounter = 0;
function freshId(prefix) {
  idCounter += 1;
  return `${prefix || "mitto-t"}-${idCounter}`;
}

describe("useLinkedBeadPhase — archived short-circuit (mitto-msv)", () => {
  test("archived=true skips the fetch entirely", async () => {
    mount(freshId(), "/tmp/wsA", true);
    await flush();
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("archived=true does NOT register a beads_changed listener", async () => {
    mount(freshId(), "/tmp/wsA", true);
    await flush();
    // A subsequent broadcast must not fire any fetch.
    window.dispatchEvent(
      new CustomEvent("mitto:beads_changed", { detail: {} }),
    );
    await flush();
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("archived=false (default) issues the fetch as normal", async () => {
    const id = freshId("mitto-arch-off");
    mount(id, "/tmp/wsA", false);
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(1);
    const [url] = currentFetch.mock.calls[0];
    expect(String(url)).toContain(`/api/issues/${id}`);
    expect(String(url)).toContain(encodeURIComponent("/tmp/wsA"));
  });

  test("missing issueId or workingDir short-circuits before the fetch", async () => {
    mount("", "/tmp/wsA", false);
    mount(freshId(), "", false);
    mount(undefined, undefined, false);
    await flush();
    expect(currentFetch).not.toHaveBeenCalled();
  });
});

describe("useLinkedBeadPhase — 404 negative cache (mitto-msv)", () => {
  test("a 404 response marks the id gone and subsequent mounts skip fetch", async () => {
    const id = freshId("mitto-404");
    currentFetch.mockImplementationOnce(() =>
      Promise.resolve(fakeResponse({ status: 404, body: {} })),
    );
    mount(id, "/tmp/wsA", false);
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(1);
    expect(goneMod.isGone("/tmp/wsA", id)).toBe(true);

    // Second mount of the same id must NOT hit the network — the negative
    // cache short-circuits BEFORE the hook's per-key dedup Map is consulted.
    mount(id, "/tmp/wsA", false);
    await flush();
    expect(currentFetch).toHaveBeenCalledTimes(1);
  });

  test("beads_changed re-fire on a known-gone id does NOT trigger a fetch", async () => {
    const id = freshId("mitto-broadcast");
    // Pre-seed the negative cache so the first mount short-circuits and we
    // isolate the beads_changed path from the initial-fetch path.
    goneMod.markGone("/tmp/wsA", id);
    mount(id, "/tmp/wsA", false);
    await flush();
    expect(currentFetch).not.toHaveBeenCalled();

    // The mitto:beads_changed listener IS wired (archived=false) but must
    // consult the negative cache on the follow-up fetch → still zero calls.
    window.dispatchEvent(
      new CustomEvent("mitto:beads_changed", {
        detail: { working_dirs: ["/tmp/wsA"] },
      }),
    );
    await flush();
    expect(currentFetch).not.toHaveBeenCalled();
  });

  test("non-404 errors do NOT poison the negative cache", async () => {
    const id = freshId("mitto-500");
    currentFetch.mockImplementationOnce(() =>
      Promise.resolve(fakeResponse({ status: 500, body: {} })),
    );
    mount(id, "/tmp/wsA", false);
    await flush();
    expect(goneMod.isGone("/tmp/wsA", id)).toBe(false);
  });

  test("a 404 in wsA does NOT shadow the same id in wsB", async () => {
    const id = freshId("mitto-iso");
    currentFetch.mockImplementationOnce(() =>
      Promise.resolve(fakeResponse({ status: 404, body: {} })),
    );
    mount(id, "/tmp/wsA", false);
    await flush();
    expect(goneMod.isGone("/tmp/wsA", id)).toBe(true);
    expect(goneMod.isGone("/tmp/wsB", id)).toBe(false);

    // Fresh mount in wsB must still fetch — cache is per-workspace.
    mount(id, "/tmp/wsB", false);
    await flush();
    // First call was the 404 in wsA; the wsB mount is the second.
    expect(currentFetch).toHaveBeenCalledTimes(2);
  });
});
