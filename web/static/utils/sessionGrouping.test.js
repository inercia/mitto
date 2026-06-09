/**
 * Unit tests for session grouping utilities.
 * Covers: computeGroupedSessions, computeSessionFingerprint
 */

import {
  computeGroupedSessions,
  computeSessionFingerprint,
} from "./sessionGrouping.js";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeSession(overrides = {}) {
  return {
    session_id: "s1",
    working_dir: "/home/user/project",
    acp_server: "auggie",
    parent_session_id: null,
    archived: false,
    periodic_enabled: false,
    pinned: false,
    name: "",
    created_at: "2024-01-01T10:00:00Z",
    ...overrides,
  };
}

const ws1 = {
  working_dir: "/home/user/project",
  acp_server: "auggie",
  name: "MyProject",
  color: null,
  code: null,
};

const ws2 = {
  working_dir: "/home/user/other",
  acp_server: "claude",
  name: "Other",
  color: null,
  code: null,
};

// ---------------------------------------------------------------------------
// computeSessionFingerprint
// ---------------------------------------------------------------------------

describe("computeSessionFingerprint", () => {
  test("returns string", () => {
    const fp = computeSessionFingerprint([], "folder");
    expect(typeof fp).toBe("string");
  });

  test("includes groupingMode in fingerprint", () => {
    const sessions = [makeSession()];
    const fpFolder = computeSessionFingerprint(sessions, "folder");
    const fpServer = computeSessionFingerprint(sessions, "server");
    expect(fpFolder).not.toBe(fpServer);
  });

  test("same sessions same mode → same fingerprint", () => {
    const sessions = [makeSession({ session_id: "a" }), makeSession({ session_id: "b" })];
    const fp1 = computeSessionFingerprint(sessions, "folder");
    const fp2 = computeSessionFingerprint(sessions, "folder");
    expect(fp1).toBe(fp2);
  });

  test("order-independent: different order → same fingerprint (sorted internally)", () => {
    const a = makeSession({ session_id: "a", name: "A" });
    const b = makeSession({ session_id: "b", name: "B" });
    const fp1 = computeSessionFingerprint([a, b], "folder");
    const fp2 = computeSessionFingerprint([b, a], "folder");
    expect(fp1).toBe(fp2);
  });

  test("isStreaming change does NOT change fingerprint", () => {
    const s1 = makeSession({ session_id: "a", isStreaming: false });
    const s2 = makeSession({ session_id: "a", isStreaming: true });
    const fp1 = computeSessionFingerprint([s1], "folder");
    const fp2 = computeSessionFingerprint([s2], "folder");
    expect(fp1).toBe(fp2);
  });

  test("name change changes fingerprint", () => {
    const s1 = makeSession({ session_id: "a", name: "old" });
    const s2 = makeSession({ session_id: "a", name: "new" });
    expect(computeSessionFingerprint([s1], "folder")).not.toBe(
      computeSessionFingerprint([s2], "folder"),
    );
  });

  test("new session added changes fingerprint", () => {
    const a = makeSession({ session_id: "a" });
    const b = makeSession({ session_id: "b" });
    const fp1 = computeSessionFingerprint([a], "folder");
    const fp2 = computeSessionFingerprint([a, b], "folder");
    expect(fp1).not.toBe(fp2);
  });

  test("parent_session_id change changes fingerprint", () => {
    const s1 = makeSession({ session_id: "a", parent_session_id: null });
    const s2 = makeSession({ session_id: "a", parent_session_id: "p1" });
    expect(computeSessionFingerprint([s1], "folder")).not.toBe(
      computeSessionFingerprint([s2], "folder"),
    );
  });
});

// ---------------------------------------------------------------------------
// computeGroupedSessions – mode: none
// ---------------------------------------------------------------------------

describe("computeGroupedSessions – none", () => {
  test("returns null for groupingMode='none'", () => {
    const sessions = [makeSession()];
    expect(computeGroupedSessions(sessions, "none", sessions, [ws1])).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// computeGroupedSessions – server mode
// ---------------------------------------------------------------------------

describe("computeGroupedSessions – server", () => {
  test("groups sessions by acp_server", () => {
    const s1 = makeSession({ session_id: "s1", acp_server: "auggie" });
    const s2 = makeSession({ session_id: "s2", acp_server: "claude" });
    const result = computeGroupedSessions([s1, s2], "server", [s1, s2], []);
    expect(result).toHaveLength(2);
    const keys = result.map((g) => g.key).sort();
    expect(keys).toEqual(["auggie", "claude"]);
  });

  test("sessions with same server go in one group", () => {
    const s1 = makeSession({ session_id: "s1", acp_server: "auggie" });
    const s2 = makeSession({ session_id: "s2", acp_server: "auggie" });
    const result = computeGroupedSessions([s1, s2], "server", [s1, s2], []);
    expect(result).toHaveLength(1);
    expect(result[0].key).toBe("auggie");
    expect(result[0].sessions).toHaveLength(2);
  });

  test("groups are sorted alphabetically", () => {
    const s1 = makeSession({ session_id: "s1", acp_server: "zeta" });
    const s2 = makeSession({ session_id: "s2", acp_server: "alpha" });
    const result = computeGroupedSessions([s1, s2], "server", [s1, s2], []);
    expect(result[0].key).toBe("alpha");
    expect(result[1].key).toBe("zeta");
  });

  test("empty input returns empty array", () => {
    const result = computeGroupedSessions([], "server", [], []);
    expect(result).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// computeGroupedSessions – folder mode
// ---------------------------------------------------------------------------

describe("computeGroupedSessions – folder", () => {
  test("groups sessions by working_dir", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/home/user/a" });
    const s2 = makeSession({ session_id: "s2", working_dir: "/home/user/b" });
    const result = computeGroupedSessions([s1, s2], "folder", [s1, s2], []);
    expect(result).toHaveLength(2);
  });

  test("sessions with same working_dir go in one group", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/home/user/proj" });
    const s2 = makeSession({ session_id: "s2", working_dir: "/home/user/proj" });
    const result = computeGroupedSessions([s1, s2], "folder", [s1, s2], []);
    expect(result).toHaveLength(1);
    expect(result[0].sessions).toHaveLength(2);
  });

  test("uses workspace name as label when available", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/home/user/project" });
    const result = computeGroupedSessions([s1], "folder", [s1], [ws1]);
    expect(result[0].label).toBe("MyProject");
  });

  test("falls back to basename when no matching workspace", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/home/user/myrepo" });
    const result = computeGroupedSessions([s1], "folder", [s1], []);
    expect(result[0].label).toBe("myrepo");
  });

  test("child session goes in same folder group as parent", () => {
    const parent = makeSession({
      session_id: "parent",
      working_dir: "/home/user/a",
      parent_session_id: null,
    });
    const child = makeSession({
      session_id: "child",
      working_dir: "/home/user/a",
      parent_session_id: "parent",
    });
    const result = computeGroupedSessions(
      [parent, child],
      "folder",
      [parent, child],
      [],
    );
    expect(result).toHaveLength(1);
    // parent is a root session; child appears as its child
    const rootSessions = result[0].sessions;
    expect(rootSessions.some((s) => s.session_id === "parent")).toBe(true);
  });

  test("isHierarchical and isParentChild flags are set", () => {
    const s1 = makeSession({ session_id: "s1" });
    const result = computeGroupedSessions([s1], "folder", [s1], []);
    expect(result[0].isHierarchical).toBe(true);
    expect(result[0].isParentChild).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// computeGroupedSessions – workspace mode
// ---------------------------------------------------------------------------

describe("computeGroupedSessions – workspace", () => {
  test("groups by composite working_dir|acp_server key", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/a", acp_server: "aug" });
    const s2 = makeSession({ session_id: "s2", working_dir: "/a", acp_server: "claude" });
    const result = computeGroupedSessions([s1, s2], "workspace", [s1, s2], []);
    expect(result).toHaveLength(2);
  });

  test("same working_dir and same acp_server → one group", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/a", acp_server: "aug" });
    const s2 = makeSession({ session_id: "s2", working_dir: "/a", acp_server: "aug" });
    const result = computeGroupedSessions([s1, s2], "workspace", [s1, s2], []);
    expect(result).toHaveLength(1);
  });
});
