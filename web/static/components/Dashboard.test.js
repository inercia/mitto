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
// must import it explicitly. testGlobals.js re-exports the lifecycle globals
// and `jest` from whichever runner is active (Jest or bun:test).
import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";

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
// globals). Keep in sync. Mirrors selectRecentConversations in Dashboard.js.
// Builds the "Recent conversations" panel list: currently-prompting sessions
// first (isStreaming), then most-recently-active non-prompting sessions
// sorted by updated_at desc, with X + Y capped at `max`. Auto-created
// children (child_origin === "auto") are excluded from both groups.
function selectRecentConversations(allSessions, max) {
  const list = Array.isArray(allSessions) ? allSessions : [];
  const isEligible = (s) => s && s.child_origin !== "auto";
  const byUpdatedDesc = (a, b) => {
    const au = a.updated_at || "";
    const bu = b.updated_at || "";
    if (au === bu) return 0;
    return au < bu ? 1 : -1;
  };
  const prompting = list
    .filter((s) => isEligible(s) && s.isStreaming)
    .slice()
    .sort(byUpdatedDesc);
  if (prompting.length >= max) return prompting.slice(0, max);
  const remaining = max - prompting.length;
  const recent = list
    .filter((s) => isEligible(s) && !s.isStreaming)
    .slice()
    .sort(byUpdatedDesc)
    .slice(0, remaining);
  return prompting.concat(recent);
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

describe("selectRecentConversations", () => {
  test("empty in → empty out", () => {
    expect(selectRecentConversations([], 5)).toEqual([]);
  });

  test("null / undefined input treated as empty", () => {
    expect(selectRecentConversations(null, 5)).toEqual([]);
    expect(selectRecentConversations(undefined, 5)).toEqual([]);
  });

  test("null/undefined session entries are skipped", () => {
    const sessions = [
      null,
      S({ session_id: "a", isStreaming: true }),
      undefined,
    ];
    expect(
      selectRecentConversations(sessions, 5).map((s) => s.session_id),
    ).toEqual(["a"]);
  });

  // ---- prompting-only cases (X only) --------------------------------------

  test("all prompting: caps at max and sorts by updated_at desc", () => {
    const sessions = Array.from({ length: 8 }, (_, i) =>
      S({
        session_id: `s${i}`,
        isStreaming: true,
        updated_at: `2026-07-12T10:0${i}:00Z`,
      }),
    );
    const out = selectRecentConversations(sessions, 5);
    expect(out).toHaveLength(5);
    // Most recent first: s7, s6, s5, s4, s3.
    expect(out.map((s) => s.session_id)).toEqual([
      "s7",
      "s6",
      "s5",
      "s4",
      "s3",
    ]);
  });

  test("prompting count == max: only prompting shown (Y = 0)", () => {
    const sessions = [
      S({
        session_id: "p1",
        isStreaming: true,
        updated_at: "2026-07-12T10:05:00Z",
      }),
      S({
        session_id: "p2",
        isStreaming: true,
        updated_at: "2026-07-12T10:04:00Z",
      }),
      S({
        session_id: "p3",
        isStreaming: true,
        updated_at: "2026-07-12T10:03:00Z",
      }),
      S({
        session_id: "p4",
        isStreaming: true,
        updated_at: "2026-07-12T10:02:00Z",
      }),
      S({
        session_id: "p5",
        isStreaming: true,
        updated_at: "2026-07-12T10:01:00Z",
      }),
      // Very-recent non-prompting: must NOT appear because X already fills the panel.
      S({
        session_id: "r1",
        isStreaming: false,
        updated_at: "2026-07-12T99:59:59Z",
      }),
    ];
    const out = selectRecentConversations(sessions, 5);
    expect(out.map((s) => s.session_id)).toEqual([
      "p1",
      "p2",
      "p3",
      "p4",
      "p5",
    ]);
  });

  test("prompting count > max: caps at max, non-prompting ignored", () => {
    const sessions = Array.from({ length: 7 }, (_, i) =>
      S({
        session_id: `p${i}`,
        isStreaming: true,
        updated_at: `2026-07-12T10:0${i}:00Z`,
      }),
    ).concat([
      S({
        session_id: "r1",
        isStreaming: false,
        updated_at: "2026-07-12T99:00:00Z",
      }),
    ]);
    const out = selectRecentConversations(sessions, 5);
    expect(out).toHaveLength(5);
    expect(out.every((s) => s.isStreaming)).toBe(true);
  });

  // ---- recent-only cases (Y only) -----------------------------------------

  test("no prompting: fills entirely with most-recent non-prompting", () => {
    const sessions = [
      S({ session_id: "old", updated_at: "2026-07-10T00:00:00Z" }),
      S({ session_id: "new", updated_at: "2026-07-12T00:00:00Z" }),
      S({ session_id: "mid", updated_at: "2026-07-11T00:00:00Z" }),
    ];
    expect(
      selectRecentConversations(sessions, 5).map((s) => s.session_id),
    ).toEqual(["new", "mid", "old"]);
  });

  // ---- mixed cases (X + Y) ------------------------------------------------

  test("mixed: prompting appear first, then recent, X + Y = max", () => {
    const sessions = [
      // 2 prompting (X = 2)
      S({
        session_id: "p1",
        isStreaming: true,
        updated_at: "2026-07-12T10:05:00Z",
      }),
      S({
        session_id: "p2",
        isStreaming: true,
        updated_at: "2026-07-12T10:06:00Z",
      }),
      // 5 non-prompting; only the 3 most recent (Y = 3) should appear.
      S({ session_id: "r1", updated_at: "2026-07-12T09:05:00Z" }),
      S({ session_id: "r2", updated_at: "2026-07-12T09:06:00Z" }),
      S({ session_id: "r3", updated_at: "2026-07-12T09:07:00Z" }),
      S({ session_id: "r-old-1", updated_at: "2026-07-10T00:00:00Z" }),
      S({ session_id: "r-old-2", updated_at: "2026-07-09T00:00:00Z" }),
    ];
    const out = selectRecentConversations(sessions, 5);
    expect(out).toHaveLength(5);
    // Prompting first (sorted desc within group), then recent (sorted desc).
    expect(out.map((s) => s.session_id)).toEqual([
      "p2",
      "p1",
      "r3",
      "r2",
      "r1",
    ]);
  });

  test("mixed: even a very-recent non-prompting stays below a stale prompting one", () => {
    const sessions = [
      S({
        session_id: "stale-prompt",
        isStreaming: true,
        updated_at: "2020-01-01T00:00:00Z",
      }),
      S({ session_id: "fresh-idle", updated_at: "2026-12-31T23:59:59Z" }),
    ];
    const out = selectRecentConversations(sessions, 5);
    expect(out.map((s) => s.session_id)).toEqual([
      "stale-prompt",
      "fresh-idle",
    ]);
  });

  test("mixed: fewer sessions than max returns everything", () => {
    const sessions = [
      S({ session_id: "p1", isStreaming: true }),
      S({ session_id: "r1" }),
    ];
    const out = selectRecentConversations(sessions, 5);
    expect(out).toHaveLength(2);
    expect(out.map((s) => s.session_id)).toEqual(["p1", "r1"]);
  });

  test("missing updated_at sorts last within its group", () => {
    const sessions = [
      S({ session_id: "no-ts", isStreaming: true, updated_at: undefined }),
      S({
        session_id: "with-ts",
        isStreaming: true,
        updated_at: "2026-07-12T10:00:00Z",
      }),
    ];
    expect(
      selectRecentConversations(sessions, 5).map((s) => s.session_id),
    ).toEqual(["with-ts", "no-ts"]);
  });

  // ---- auto-child filtering (child_origin === "auto") ---------------------

  test("auto-child sessions are excluded from both groups", () => {
    const sessions = [
      // Auto-child that is prompting — must be dropped.
      S({
        session_id: "coder-1",
        isStreaming: true,
        child_origin: "auto",
        updated_at: "2026-07-12T10:10:00Z",
      }),
      // Auto-child that is idle-recent — must be dropped.
      S({
        session_id: "coder-2",
        child_origin: "auto",
        updated_at: "2026-07-12T10:09:00Z",
      }),
      // Regular top-level (no child_origin) — must appear.
      S({
        session_id: "top",
        isStreaming: true,
        updated_at: "2026-07-12T10:05:00Z",
      }),
      // Recent idle top-level — must appear.
      S({ session_id: "idle", updated_at: "2026-07-12T10:04:00Z" }),
    ];
    const out = selectRecentConversations(sessions, 5);
    expect(out.map((s) => s.session_id)).toEqual(["top", "idle"]);
  });

  test("mcp- and human-spawned children are kept (not filtered)", () => {
    const sessions = [
      S({
        session_id: "mcp-child",
        child_origin: "mcp",
        updated_at: "2026-07-12T10:03:00Z",
      }),
      S({
        session_id: "human-child",
        child_origin: "human",
        updated_at: "2026-07-12T10:02:00Z",
      }),
      S({
        session_id: "auto-child",
        child_origin: "auto",
        updated_at: "2026-07-12T10:01:00Z",
      }),
    ];
    expect(
      selectRecentConversations(sessions, 5).map((s) => s.session_id),
    ).toEqual(["mcp-child", "human-child"]);
  });

  test("auto-child does not consume a slot when prompting exceeds max", () => {
    // 5 real prompting + 1 auto-child prompting → panel shows only the 5 real ones.
    const sessions = [
      S({
        session_id: "auto",
        isStreaming: true,
        child_origin: "auto",
        updated_at: "2026-07-12T10:99:00Z",
      }),
      S({
        session_id: "p1",
        isStreaming: true,
        updated_at: "2026-07-12T10:05:00Z",
      }),
      S({
        session_id: "p2",
        isStreaming: true,
        updated_at: "2026-07-12T10:04:00Z",
      }),
      S({
        session_id: "p3",
        isStreaming: true,
        updated_at: "2026-07-12T10:03:00Z",
      }),
      S({
        session_id: "p4",
        isStreaming: true,
        updated_at: "2026-07-12T10:02:00Z",
      }),
      S({
        session_id: "p5",
        isStreaming: true,
        updated_at: "2026-07-12T10:01:00Z",
      }),
    ];
    const out = selectRecentConversations(sessions, 5);
    expect(out.map((s) => s.session_id)).toEqual([
      "p1",
      "p2",
      "p3",
      "p4",
      "p5",
    ]);
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

// =============================================================================
// Loading-state row rendering (mitto-eml)
// =============================================================================
//
// Regression tests for mitto-eml: when the Dashboard mounts and the first
// /api/dashboard fetch is in flight (`data === null`), the three bd-driven
// list panels (In-progress tasks, Ready tasks, Recently modified) must render
// a loading indicator, NOT the "No items" empty-state row. The Recent-
// conversations panel is client-derived from `allSessions`, so its caller
// gates loading on `data === null && allSessions.length === 0` — mirroring
// the existing pattern used for the loops-stats block (Dashboard.js ~L280).
//
// Following the file's own "duplicate helpers verbatim" convention, the row
// helpers (`emptyRow`, `renderConversationRows`, `renderTaskRows`) are
// duplicated verbatim from Dashboard.js below. The mock `html` template tag
// below serializes the tag call into a raw string so a test can inspect the
// row's `key=` attribute (which is where `emptyRow` and a future `loadingRow`
// are distinguishable). Populated-row branches call project helpers
// (`workspaceBadge`, `priorityPill`, etc.) not in scope for this file; those
// branches are elided with an explanatory comment because the mitto-eml bug
// lives entirely in the empty-vs-loading branch at the top of each helper.

// Mock htm's tagged-template `html`: joins strings and interpolated values
// into a single raw string. Interpolations are wrapped in guillemets so a
// static template literal like `key="__empty"` remains distinguishable from
// a dynamic key such as `key=${id}` (which serializes as `key=‹abc-1›`).
const html = (strings, ...values) => {
  let raw = "";
  for (let i = 0; i < strings.length; i++) {
    raw += strings[i];
    if (i < values.length) raw += `\u2039${String(values[i])}\u203A`;
  }
  return { __html: raw };
};

// Duplicated from Dashboard.js for testing (component imports window.preact
// globals). Keep in sync. Mirrors Dashboard.js ~L376.
const COMPACT_ROW_STYLE = "gap: 0.5rem; padding: 0.5rem 0.75rem;";

// Duplicated from Dashboard.js for testing. Keep in sync. Mirrors emptyRow.
function emptyRow() {
  return html`
    <li class="list-row" style="${COMPACT_ROW_STYLE}" key="__empty">
      <div class="list-col-grow min-w-0 flex flex-col gap-1">
        <div class="text-xs">${"\u00A0"}</div>
        <div class="text-center text-sm text-mitto-text-muted">No items</div>
      </div>
    </li>
  `;
}

// Duplicated from Dashboard.js for testing. Keep in sync. Mirrors loadingRow.
function loadingRow() {
  return html`
    <li class="list-row" style="${COMPACT_ROW_STYLE}" key="__loading">
      <div class="list-col-grow min-w-0 flex flex-col gap-1">
        <div class="text-xs">${"\u00A0"}</div>
        <div
          class="flex items-center justify-center gap-2 text-sm text-mitto-text-muted"
        >
          <span
            class="loading loading-spinner loading-xs text-mitto-text-muted"
            aria-hidden="true"
          ></span>
          <span>Loading…</span>
        </div>
      </div>
    </li>
  `;
}

// Duplicated from Dashboard.js for testing (component imports window.preact
// globals). Keep in sync with renderConversationRows. Populated-row branch
// elided — the mitto-eml bug lives entirely in the empty/loading branch at
// the top.
function renderConversationRows(sessions, onClick, isLoading) {
  // Loading takes precedence over empty so the panel does not lie about the
  // server state while the first fetch is still in flight.
  if ((!sessions || sessions.length === 0) && isLoading) return [loadingRow()];
  // Empty list → one visible "No items" row. Bottom-alignment padding across
  // sibling panels is now handled by renderListPanel(padTo) so this helper
  // returns only the real content and never over-pads a lone panel.
  if (!sessions || sessions.length === 0) return [emptyRow()];
  // Populated branch elided — mitto-eml regression tests below exercise the
  // empty/loading branch only. See Dashboard.js for the full row template.
  return sessions.map(() => null);
}

// Duplicated from Dashboard.js for testing (component imports window.preact
// globals). Keep in sync with renderTaskRows. Populated-row branch elided —
// the mitto-eml bug lives entirely in the empty/loading branch at the top.
function renderTaskRows(items, onClick, isLoading) {
  // Loading takes precedence over empty so the panel does not lie about the
  // server state while the first fetch is still in flight.
  if ((!items || items.length === 0) && isLoading) return [loadingRow()];
  // See renderConversationRows: real rows only, bottom-alignment is handled
  // page-scoped by renderListPanel(padTo).
  if (!items || items.length === 0) return [emptyRow()];
  // Populated branch elided — see Dashboard.js for the full row template.
  return items.map(() => null);
}

describe("renderTaskRows loading state (mitto-eml)", () => {
  test("data===null on first load: renders a loading row, not an empty row", () => {
    // isLoading=true is the caller's signal that /api/dashboard has not yet
    // resolved (data===null). Under the mitto-eml fix, the panel MUST show a
    // loading indicator (key="__loading") instead of the "No items" empty
    // row so the user does not perceive the panel as "server confirmed
    // nothing" mid-fetch.
    const rows = renderTaskRows([], null, /* isLoading */ true);
    expect(rows).toHaveLength(1);
    expect(rows[0].__html).toMatch(/key="__loading"/);
    expect(rows[0].__html).not.toMatch(/key="__empty"/);
  });

  test("data resolved, list genuinely empty: still renders 'No items'", () => {
    // After the first fetch resolves, isLoading is false; a genuinely empty
    // list must show "No items" (key="__empty") — unchanged from today.
    const rows = renderTaskRows([], null, /* isLoading */ false);
    expect(rows).toHaveLength(1);
    expect(rows[0].__html).toMatch(/key="__empty"/);
  });
});

describe("renderConversationRows loading state (mitto-eml)", () => {
  test("data===null AND allSessions empty: renders a loading row", () => {
    // Recent conversations is derived client-side from allSessions and can
    // already be populated before /api/dashboard resolves. Callers therefore
    // pass isLoading := (data === null && allSessions.length === 0). When
    // that combined gate is true, the panel MUST show a loading indicator.
    const rows = renderConversationRows([], null, /* isLoading */ true);
    expect(rows).toHaveLength(1);
    expect(rows[0].__html).toMatch(/key="__loading"/);
    expect(rows[0].__html).not.toMatch(/key="__empty"/);
  });

  test("data resolved, no sessions: renders 'No items'", () => {
    // After the first fetch resolves, isLoading is false; the panel returns
    // to the standard empty-state copy (key="__empty").
    const rows = renderConversationRows([], null, /* isLoading */ false);
    expect(rows).toHaveLength(1);
    expect(rows[0].__html).toMatch(/key="__empty"/);
  });
});
