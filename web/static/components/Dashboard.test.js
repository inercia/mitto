/**
 * Unit tests for Dashboard pure helpers.
 *
 * The Dashboard component (Dashboard.js) imports window.preact globals at
 * module load time, so it cannot be imported under jsdom. Following the
 * project convention used by BeadsView.test.js / Message.test.js, the pure
 * helpers under test are duplicated verbatim below (with the "Keep in sync"
 * marker) and exercised directly.
 */

// Jest is not injected as a global under --experimental-vm-modules (ESM); we
// must import it explicitly. Matches WorkspacesDialog.test.js.
import { jest } from "@jest/globals";

// =============================================================================
// Duplicated helpers — keep in sync with web/static/components/Dashboard.js
// =============================================================================

// Duplicated from Dashboard.js for testing (component imports window.preact
// globals). Keep in sync. Mirrors the useMemo body at Dashboard.js ~L104-117.
// Filters by `isStreaming` (the client field populated from the WebSocket
// `is_prompting` flag — see computeAllSessions in lib.js), not by a raw
// `is_prompting` property, which does not exist on the client session model.
function deriveCounts(allSessions) {
  let p = 0,
    la = 0,
    ls = 0;
  for (const s of allSessions) {
    if (!s) continue;
    if (s.isStreaming) p += 1;
    if (s.loop_configured || s.loop_enabled) {
      if (s.loop_enabled) la += 1;
      else ls += 1;
    }
  }
  return { prompting: p, loopsActive: la, loopsStopped: ls };
}

// Duplicated from Dashboard.js for testing (component imports window.preact
// globals). Keep in sync. Mirrors the useMemo body at Dashboard.js ~L149-158.
// Same `isStreaming`-vs-`is_prompting` note as deriveCounts above.
function topPrompting(allSessions, max) {
  return (allSessions || [])
    .filter((s) => s && s.isStreaming)
    .slice()
    .sort((a, b) => {
      const au = a.updated_at || "";
      const bu = b.updated_at || "";
      if (au === bu) return 0;
      return au < bu ? 1 : -1;
    })
    .slice(0, max);
}

// Duplicated from Dashboard.js for testing (component imports window.preact
// globals). Keep in sync. Mirrors the defensive server-list slicing at
// Dashboard.js ~L157-159.
function capList(items, max) {
  return (items || []).slice(0, max);
}

// Duplicated from Dashboard.js for testing (component imports window.preact
// globals). Keep in sync. Mirrors Dashboard.js ~L307-314.
function activateOnKey(fn) {
  return (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      fn();
    }
  };
}

// =============================================================================
// Fixtures
// =============================================================================

const S = (over = {}) => ({
  session_id: "s-x",
  isStreaming: false,
  loop_configured: false,
  loop_enabled: false,
  updated_at: "2026-07-12T10:00:00Z",
  ...over,
});

// =============================================================================
// Tests
// =============================================================================

describe("deriveCounts", () => {
  test("empty array returns all zeros", () => {
    expect(deriveCounts([])).toEqual({
      prompting: 0,
      loopsActive: 0,
      loopsStopped: 0,
    });
  });

  test("counts isStreaming sessions", () => {
    const sessions = [
      S({ isStreaming: true }),
      S({ isStreaming: true }),
      S({ isStreaming: false }),
    ];
    expect(deriveCounts(sessions).prompting).toBe(2);
  });

  test("loop_enabled counts as active", () => {
    const sessions = [
      S({ loop_configured: true, loop_enabled: true }),
      S({ loop_configured: true, loop_enabled: true }),
    ];
    const c = deriveCounts(sessions);
    expect(c.loopsActive).toBe(2);
    expect(c.loopsStopped).toBe(0);
  });

  test("loop_configured without loop_enabled counts as stopped", () => {
    const sessions = [
      S({ loop_configured: true, loop_enabled: false }),
      S({ loop_configured: true, loop_enabled: false }),
      S({ loop_configured: true, loop_enabled: false }),
    ];
    const c = deriveCounts(sessions);
    expect(c.loopsActive).toBe(0);
    expect(c.loopsStopped).toBe(3);
  });

  test("mixed fixture tallies correctly", () => {
    const sessions = [
      S({ isStreaming: true, loop_configured: true, loop_enabled: true }),
      S({ isStreaming: true }),
      S({ loop_configured: true, loop_enabled: false }),
      S({ loop_configured: true, loop_enabled: false }),
      S({ loop_enabled: true }),
      S(),
    ];
    expect(deriveCounts(sessions)).toEqual({
      prompting: 2,
      loopsActive: 2,
      loopsStopped: 2,
    });
  });

  test("null / undefined entries are skipped", () => {
    const sessions = [null, undefined, S({ isStreaming: true }), null];
    expect(deriveCounts(sessions).prompting).toBe(1);
  });
});


describe("topPrompting", () => {
  test("empty in → empty out", () => {
    expect(topPrompting([], 5)).toEqual([]);
  });

  test("filters out non-prompting sessions", () => {
    const sessions = [
      S({ session_id: "a", isStreaming: true }),
      S({ session_id: "b", isStreaming: false }),
      S({ session_id: "c", isStreaming: true }),
    ];
    const out = topPrompting(sessions, 5);
    expect(out.map((s) => s.session_id).sort()).toEqual(["a", "c"]);
  });

  test("caps at max", () => {
    const sessions = Array.from({ length: 8 }, (_, i) =>
      S({
        session_id: `s${i}`,
        isStreaming: true,
        updated_at: `2026-07-12T10:0${i}:00Z`,
      }),
    );
    expect(topPrompting(sessions, 5)).toHaveLength(5);
  });

  test("sorts by updated_at descending", () => {
    const sessions = [
      S({ session_id: "old", isStreaming: true, updated_at: "2026-07-10T00:00:00Z" }),
      S({ session_id: "new", isStreaming: true, updated_at: "2026-07-12T00:00:00Z" }),
      S({ session_id: "mid", isStreaming: true, updated_at: "2026-07-11T00:00:00Z" }),
    ];
    expect(topPrompting(sessions, 5).map((s) => s.session_id)).toEqual([
      "new",
      "mid",
      "old",
    ]);
  });

  test("equal timestamps: both present in output", () => {
    const sessions = [
      S({ session_id: "a", isStreaming: true, updated_at: "2026-07-12T10:00:00Z" }),
      S({ session_id: "b", isStreaming: true, updated_at: "2026-07-12T10:00:00Z" }),
    ];
    const ids = topPrompting(sessions, 5).map((s) => s.session_id).sort();
    expect(ids).toEqual(["a", "b"]);
  });

  test("missing updated_at sorts last", () => {
    const sessions = [
      S({ session_id: "no-ts", isStreaming: true, updated_at: undefined }),
      S({ session_id: "with-ts", isStreaming: true, updated_at: "2026-07-12T10:00:00Z" }),
    ];
    expect(topPrompting(sessions, 5).map((s) => s.session_id)).toEqual([
      "with-ts",
      "no-ts",
    ]);
  });

  test("null/undefined session entries are skipped", () => {
    const sessions = [null, S({ session_id: "a", isStreaming: true }), undefined];
    expect(topPrompting(sessions, 5).map((s) => s.session_id)).toEqual(["a"]);
  });

  test("null input treated as empty", () => {
    expect(topPrompting(null, 5)).toEqual([]);
    expect(topPrompting(undefined, 5)).toEqual([]);
  });
});

describe("capList", () => {
  test("null / undefined → empty array", () => {
    expect(capList(null, 5)).toEqual([]);
    expect(capList(undefined, 5)).toEqual([]);
  });

  test("long list is capped", () => {
    const items = Array.from({ length: 10 }, (_, i) => ({ id: i }));
    expect(capList(items, 5)).toHaveLength(5);
    expect(capList(items, 5)[0]).toEqual({ id: 0 });
  });

  test("short list is returned unchanged", () => {
    const items = [{ id: 1 }, { id: 2 }];
    expect(capList(items, 5)).toEqual(items);
  });

  test("empty array stays empty", () => {
    expect(capList([], 5)).toEqual([]);
  });
});

describe("activateOnKey", () => {
  test("Enter invokes callback and prevents default", () => {
    const cb = jest.fn();
    const preventDefault = jest.fn();
    activateOnKey(cb)({ key: "Enter", preventDefault });
    expect(preventDefault).toHaveBeenCalledTimes(1);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  test("Space invokes callback and prevents default", () => {
    const cb = jest.fn();
    const preventDefault = jest.fn();
    activateOnKey(cb)({ key: " ", preventDefault });
    expect(preventDefault).toHaveBeenCalledTimes(1);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  test("unrelated key does nothing", () => {
    const cb = jest.fn();
    const preventDefault = jest.fn();
    activateOnKey(cb)({ key: "a", preventDefault });
    expect(preventDefault).not.toHaveBeenCalled();
    expect(cb).not.toHaveBeenCalled();
  });

  test("Tab / Escape do nothing", () => {
    const cb = jest.fn();
    const pd = jest.fn();
    activateOnKey(cb)({ key: "Tab", preventDefault: pd });
    activateOnKey(cb)({ key: "Escape", preventDefault: pd });
    expect(cb).not.toHaveBeenCalled();
    expect(pd).not.toHaveBeenCalled();
  });
});
