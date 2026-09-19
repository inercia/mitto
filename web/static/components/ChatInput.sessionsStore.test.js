/**
 * Unit tests for ChatInput's read-side migration to stores/sessionsStore.js
 * (mitto-b1k).
 *
 * ChatInput.js cannot be imported under jsdom (window.preact/htm globals at
 * module load -- see ChatInput.test.js's file header). The exact field
 * derivation is mirrored here verbatim:
 *
 *   const sessionInfoSlice = useSessionInfo(sessionId);
 *   const isReadOnly = sessionInfoSlice?.isReadOnly;
 *   const isArchived = sessionInfoSlice?.archived || false;
 *   const loopConfigured = sessionInfoSlice?.loop_configured || false;
 *   const workingDir = sessionInfoSlice?.working_dir || "";
 *
 * Rather than going through the Preact hook wrapper (hooks/useSessionsStore.js,
 * which destructures useState/useEffect from window.preact ONCE at module
 * load -- see MessageList.sessionsStore.test.js's file header for why this
 * suite avoids stubbing window.preact for that shared module), this file
 * drives the SAME underlying store (getInfo/replaceAll, which have no
 * window dependency -- see stores/sessionsStore.test.js) that the hook
 * itself reads from. The hook wrapper's own subscribe/unsubscribe machinery
 * is already covered by useSessionsStore.test.js and the store's per-slice
 * notification isolation by stores/sessionsStore.test.js; this file covers
 * the piece unique to mitto-b1k: ChatInput's own field derivation from that
 * store data.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import {
  getInfo,
  replaceAll,
  _resetSessionsStoreForTests,
} from "../stores/sessionsStore.js";

// Mirrors ChatInput.js's derivation verbatim (see mitto-b1k Implementation
// comment / git show e4359425 -- ChatInput.js). useSessionInfo(id) ===
// getInfo(id) for a truthy id (see hooks/useSessionsStore.js); useSessionInfo
// additionally returns null for a falsy id, matched below.
function deriveChatInputSessionFields(sessionId) {
  const sessionInfoSlice = sessionId ? getInfo(sessionId) : null;
  return {
    isReadOnly: sessionInfoSlice?.isReadOnly,
    isArchived: sessionInfoSlice?.archived || false,
    loopConfigured: sessionInfoSlice?.loop_configured || false,
    workingDir: sessionInfoSlice?.working_dir || "",
  };
}

beforeEach(() => {
  _resetSessionsStoreForTests();
});

describe("ChatInput sessionsStore migration (mitto-b1k) > defaults", () => {
  test("a falsy sessionId (no active session) yields the same defaults as the old prop defaults", () => {
    expect(deriveChatInputSessionFields(null)).toEqual({
      isReadOnly: undefined,
      isArchived: false,
      loopConfigured: false,
      workingDir: "",
    });
  });

  test("an unregistered sessionId (info never set) yields the same defaults", () => {
    expect(deriveChatInputSessionFields("nope")).toEqual({
      isReadOnly: undefined,
      isArchived: false,
      loopConfigured: false,
      workingDir: "",
    });
  });

  test("a session whose info object omits archived/loop_configured/working_dir falls back to defaults", () => {
    replaceAll({ s1: { info: { name: "s1" } } });
    expect(deriveChatInputSessionFields("s1")).toEqual({
      isReadOnly: undefined,
      isArchived: false,
      loopConfigured: false,
      workingDir: "",
    });
  });
});

describe("ChatInput sessionsStore migration (mitto-b1k) > reads real per-session values", () => {
  test("all four fields are read from the session's info slice", () => {
    replaceAll({
      s1: {
        info: {
          isReadOnly: true,
          archived: true,
          loop_configured: true,
          working_dir: "/repo/mitto",
        },
      },
    });

    expect(deriveChatInputSessionFields("s1")).toEqual({
      isReadOnly: true,
      isArchived: true,
      loopConfigured: true,
      workingDir: "/repo/mitto",
    });
  });

  test("isReadOnly is passed through as-is (not coerced) when explicitly false", () => {
    replaceAll({ s1: { info: { isReadOnly: false } } });
    expect(deriveChatInputSessionFields("s1").isReadOnly).toBe(false);
  });
});

describe("ChatInput sessionsStore migration (mitto-b1k) > render isolation", () => {
  test("another session's info update does not change this session's derived fields", () => {
    replaceAll({
      s1: { info: { archived: false, working_dir: "/repo/a" } },
      s2: { info: { archived: false, working_dir: "/repo/b" } },
    });

    const before = deriveChatInputSessionFields("s1");

    // Only s2 (a different, e.g. background, session) changes.
    replaceAll({
      s1: { info: { archived: false, working_dir: "/repo/a" } },
      s2: { info: { archived: true, working_dir: "/repo/b" } },
    });

    expect(deriveChatInputSessionFields("s1")).toEqual(before);
  });

  test("switching sessionId adopts the new session's fields", () => {
    replaceAll({
      s1: { info: { archived: false, working_dir: "/repo/a" } },
      s2: { info: { archived: true, working_dir: "/repo/b" } },
    });

    expect(deriveChatInputSessionFields("s1")).toEqual({
      isReadOnly: undefined,
      isArchived: false,
      loopConfigured: false,
      workingDir: "/repo/a",
    });
    expect(deriveChatInputSessionFields("s2")).toEqual({
      isReadOnly: undefined,
      isArchived: true,
      loopConfigured: false,
      workingDir: "/repo/b",
    });
  });
});
