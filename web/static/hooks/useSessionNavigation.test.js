/**
 * Tests for useSessionNavigation.js — three-state loop filtering sync
 * (mitto-k53.4).
 *
 * Pins the bead's acceptance criteria against the real navigableSessions
 * pipeline (computeUnifiedTree -> filterUnifiedTree -> flattenUnifiedTreeForNav
 * -> scopeNavEntriesToCurrentFolder):
 *   - With loopIdle:false, idle loop conversations are skipped by
 *     keyboard/swipe navigation.
 *   - When a loop transitions Running<->Idle (isStreaming flips on a new
 *     allSessions reference), navigableSessions updates accordingly.
 *   - Existing folder-scoped, non-archived, parent-only cycling behavior is
 *     unaffected by the three loop toggles.
 *
 * The hook (and useSwipeNavigation.js, which it wires) destructure
 * useState/useEffect/useCallback/useMemo/useRef from window.preact at
 * module-load time. Since these tests only assert on the returned
 * navigableSessions value — not on referential memoization or the
 * DOM/window event wiring (already verified by code inspection in the
 * mitto-k53.4 Plan comment) — useMemo/useCallback are pass-throughs that
 * always recompute and useEffect/useRef are no-ops. useState reads from a
 * per-call-index override map so each test can seed categoryFilterForNav
 * (the 3rd useState call), mirroring the override technique in
 * useConversationMenu.test.js.
 */

import { describe, test, expect } from "../utils/testing/testGlobals.js";

global.window = global.window || {};

let stateOverrides;
let callIndex;
window.preact = {
  useState: (initial) => {
    const idx = callIndex++;
    const value =
      stateOverrides && idx in stateOverrides
        ? stateOverrides[idx]
        : typeof initial === "function"
          ? initial()
          : initial;
    return [value, () => {}];
  },
  useMemo: (fn) => fn(),
  useCallback: (fn) => fn,
  useEffect: () => {},
  useRef: (initial) => ({ current: initial }),
};

let useSessionNavigation;

async function navigate(deps, categoryFilter) {
  if (!useSessionNavigation) {
    ({ useSessionNavigation } = await import("./useSessionNavigation.js"));
  }
  callIndex = 0;
  stateOverrides = categoryFilter ? { 2: categoryFilter } : undefined;
  return useSessionNavigation({
    activeSessionId: "s-reg",
    switchSession: () => {},
    setShowSidebar: () => {},
    setSwipeDirection: () => {},
    setSwipeArrow: () => {},
    mainContentRef: { current: null },
    workspaces: [],
    ...deps,
  });
}

const ALL_VISIBLE = {
  regular: true,
  loopRunning: true,
  loopIdle: true,
  loopPaused: true,
  archived: true,
  tasks: true,
};

function session(id, overrides = {}) {
  return {
    session_id: id,
    working_dir: "/proj",
    parent_session_id: null,
    archived: false,
    created_at: "2024-01-01T10:00:00Z",
    ...overrides,
  };
}

const REG = session("s-reg");
const LOOP_RUNNING = session("s-loop-running", {
  loop_configured: true,
  loop_enabled: true,
  isStreaming: true,
});
const LOOP_IDLE = session("s-loop-idle", {
  loop_configured: true,
  loop_enabled: true,
  isStreaming: false,
});
const LOOP_PAUSED = session("s-loop-paused", {
  loop_configured: true,
  loop_enabled: false,
});

function ids(sessions) {
  return sessions.map((s) => s.session_id);
}

describe("useSessionNavigation — three-state loop filtering (mitto-k53.4)", () => {
  test("default filter (all visible): regular + running + idle + paused loops are all navigable", async () => {
    const allSessions = [REG, LOOP_RUNNING, LOOP_IDLE, LOOP_PAUSED];
    const { navigableSessions } = await navigate({ allSessions }, ALL_VISIBLE);
    expect(ids(navigableSessions).sort()).toEqual(
      ["s-reg", "s-loop-running", "s-loop-idle", "s-loop-paused"].sort(),
    );
  });

  test("loopIdle:false hides idle loops; running/paused/regular stay navigable", async () => {
    const allSessions = [REG, LOOP_RUNNING, LOOP_IDLE, LOOP_PAUSED];
    const { navigableSessions } = await navigate(
      { allSessions },
      { ...ALL_VISIBLE, loopIdle: false },
    );
    expect(ids(navigableSessions).sort()).toEqual(
      ["s-reg", "s-loop-running", "s-loop-paused"].sort(),
    );
    expect(ids(navigableSessions)).not.toContain("s-loop-idle");
  });

  test("Running->Idle transition (new allSessions ref, isStreaming flips) updates navigableSessions", async () => {
    const filter = { ...ALL_VISIBLE, loopRunning: true, loopIdle: false };
    const running = session("s-flip", {
      loop_configured: true,
      loop_enabled: true,
      isStreaming: true,
    });
    const { navigableSessions: whileRunning } = await navigate(
      { allSessions: [REG, running] },
      filter,
    );
    expect(ids(whileRunning)).toContain("s-flip");

    // Simulate the loop finishing its turn: a NEW session object (new
    // allSessions reference, as produced by the immutable WS handlers) with
    // isStreaming now false.
    const idled = session("s-flip", {
      loop_configured: true,
      loop_enabled: true,
      isStreaming: false,
    });
    const { navigableSessions: afterIdle } = await navigate(
      { allSessions: [REG, idled] },
      filter,
    );
    expect(ids(afterIdle)).not.toContain("s-flip");
  });

  test("no regression: children, archived, and other folders stay excluded from cycling", async () => {
    const child = session("s-child", { parent_session_id: "s-reg" });
    const archived = session("s-archived", { archived: true });
    const otherFolder = session("s-other-folder", {
      working_dir: "/elsewhere",
    });
    const allSessions = [REG, child, archived, otherFolder];
    const { navigableSessions } = await navigate({ allSessions }, ALL_VISIBLE);
    expect(ids(navigableSessions)).toEqual(["s-reg"]);
  });
});
