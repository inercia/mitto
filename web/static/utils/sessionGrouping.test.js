/**
 * Unit tests for session grouping utilities.
 * Covers: computeGroupedSessions, computeSessionFingerprint
 */

import {
  computeGroupedSessions,
  computeSessionFingerprint,
  computeUnifiedTree,
  filterUnifiedTree,
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

// ---------------------------------------------------------------------------
// computeUnifiedTree
// ---------------------------------------------------------------------------

describe("computeUnifiedTree", () => {
  test("always returns a dashboard node and folders array", () => {
    const result = computeUnifiedTree([]);
    expect(result).toHaveProperty("dashboard");
    expect(result.dashboard).toMatchObject({ type: "dashboard", id: "__dashboard__" });
    expect(result).toHaveProperty("folders");
    expect(Array.isArray(result.folders)).toBe(true);
  });

  test("empty input → folders: []", () => {
    expect(computeUnifiedTree([]).folders).toHaveLength(0);
    expect(computeUnifiedTree(undefined).folders).toHaveLength(0);
    expect(computeUnifiedTree(null).folders).toHaveLength(0);
  });

  test("sessions in two different working_dirs produce two folders sorted alphabetically", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/home/user/zebra" });
    const s2 = makeSession({ session_id: "s2", working_dir: "/home/user/alpha" });
    const result = computeUnifiedTree([s1, s2], []);
    expect(result.folders).toHaveLength(2);
    // Sorted alphabetically by label (basename)
    expect(result.folders[0].label).toBe("alpha");
    expect(result.folders[1].label).toBe("zebra");
  });

  test("each folder has a tasks node with correct id, type, workingDir, and folderKey", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/home/user/project" });
    const result = computeUnifiedTree([s1], [ws1]);
    const folder = result.folders[0];
    expect(folder.tasksNode).toMatchObject({
      type: "tasks",
      id: `tasks:${folder.key}`,
      label: "Tasks",
      workingDir: "/home/user/project",
      folderKey: folder.key,
    });
  });

  test("parent-child nesting: child appears in parent.children, not as root", () => {
    const parent = makeSession({
      session_id: "parent",
      working_dir: "/home/user/project",
      parent_session_id: null,
      created_at: "2024-01-01T10:00:00Z",
    });
    const child = makeSession({
      session_id: "child",
      working_dir: "/home/user/project",
      parent_session_id: "parent",
      created_at: "2024-01-02T10:00:00Z",
    });
    const result = computeUnifiedTree([parent, child], []);
    const folder = result.folders[0];
    const allRoots = [...folder.conversations, ...folder.archived];
    const rootIds = allRoots.map((n) => n.session_id);
    expect(rootIds).toContain("parent");
    expect(rootIds).not.toContain("child");
    const parentNode = allRoots.find((n) => n.session_id === "parent");
    expect(parentNode.children.some((c) => c.session_id === "child")).toBe(true);
  });

  test("category tagging: regular → conversations, periodic → periodic, archived → archived", () => {
    const regular = makeSession({ session_id: "r1", working_dir: "/proj" });
    const periodic = makeSession({
      session_id: "p1",
      working_dir: "/proj",
      periodic_enabled: true,
      created_at: "2024-01-02T00:00:00Z",
    });
    const archived = makeSession({
      session_id: "a1",
      working_dir: "/proj",
      archived: true,
      created_at: "2024-01-03T00:00:00Z",
    });
    const result = computeUnifiedTree([regular, periodic, archived], []);
    const folder = result.folders[0];
    const allNodes = [...folder.conversations, ...folder.archived];
    const r = allNodes.find((n) => n.session_id === "r1");
    const p = allNodes.find((n) => n.session_id === "p1");
    const a = allNodes.find((n) => n.session_id === "a1");
    expect(r.category).toBe("conversations");
    expect(p.category).toBe("periodic");
    expect(a.category).toBe("archived");
  });

  test("category tagging propagates to nested children", () => {
    const parent = makeSession({
      session_id: "parent",
      working_dir: "/proj",
      parent_session_id: null,
      created_at: "2024-01-01T00:00:00Z",
    });
    const child = makeSession({
      session_id: "child",
      working_dir: "/proj",
      parent_session_id: "parent",
      periodic_enabled: true,
      created_at: "2024-01-02T00:00:00Z",
    });
    const result = computeUnifiedTree([parent, child], []);
    const folder = result.folders[0];
    const parentNode = folder.conversations.find((n) => n.session_id === "parent");
    const childNode = parentNode.children.find((c) => c.session_id === "child");
    expect(childNode.category).toBe("periodic");
  });

  test("archived partitioning: archived root → folder.archived, active root → folder.conversations", () => {
    const active = makeSession({
      session_id: "active",
      working_dir: "/proj",
      created_at: "2024-01-02T00:00:00Z",
    });
    const archived = makeSession({
      session_id: "archived",
      working_dir: "/proj",
      archived: true,
      created_at: "2024-01-01T00:00:00Z",
    });
    const result = computeUnifiedTree([active, archived], []);
    const folder = result.folders[0];
    expect(folder.conversations).toHaveLength(1);
    expect(folder.conversations[0].session_id).toBe("active");
    expect(folder.archived).toHaveLength(1);
    expect(folder.archived[0].session_id).toBe("archived");
  });

  test("created_at desc ordering preserved within conversations[]", () => {
    const older = makeSession({
      session_id: "older",
      working_dir: "/proj",
      created_at: "2024-01-01T00:00:00Z",
    });
    const newer = makeSession({
      session_id: "newer",
      working_dir: "/proj",
      created_at: "2024-01-03T00:00:00Z",
    });
    const middle = makeSession({
      session_id: "middle",
      working_dir: "/proj",
      created_at: "2024-01-02T00:00:00Z",
    });
    const result = computeUnifiedTree([older, newer, middle], []);
    const ids = result.folders[0].conversations.map((n) => n.session_id);
    expect(ids).toEqual(["newer", "middle", "older"]);
  });

  test("does NOT mutate input session objects", () => {
    const s1 = makeSession({ session_id: "s1", working_dir: "/proj" });
    const s2 = makeSession({ session_id: "s2", working_dir: "/proj", archived: true });
    const inputCopy1 = { ...s1 };
    const inputCopy2 = { ...s2 };
    computeUnifiedTree([s1, s2], []);
    expect(s1).toEqual(inputCopy1);
    expect(s2).toEqual(inputCopy2);
    expect(Object.prototype.hasOwnProperty.call(s1, "category")).toBe(false);
    expect(Object.prototype.hasOwnProperty.call(s2, "category")).toBe(false);
  });
});

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

// ---------------------------------------------------------------------------
// filterUnifiedTree Tests
// ---------------------------------------------------------------------------

describe("filterUnifiedTree", () => {
  function makeRegular(overrides = {}) {
    return makeSession({ session_id: `r-${Math.random()}`, ...overrides });
  }
  function makePeriodic(overrides = {}) {
    return makeSession({ session_id: `p-${Math.random()}`, periodic_enabled: true, ...overrides });
  }
  function makeArchived(overrides = {}) {
    return makeSession({ session_id: `a-${Math.random()}`, archived: true, ...overrides });
  }

  const WS = [{ working_dir: "/home/user/project" }];

  test("all-true filter → folders/conversations/archived unchanged; showTasks true", () => {
    const sessions = [makeRegular(), makePeriodic(), makeArchived()];
    const tree = computeUnifiedTree(sessions, WS);
    const result = filterUnifiedTree(tree, { regular: true, periodic: true, archived: true, tasks: true });
    expect(result.folders.length).toBeGreaterThan(0);
    result.folders.forEach((folder) => {
      expect(folder.showTasks).toBe(true);
    });
    // total conversations (non-archived) should include regular + periodic
    const totalConvs = result.folders.reduce((sum, f) => sum + f.conversations.length, 0);
    expect(totalConvs).toBeGreaterThanOrEqual(2);
    const totalArchived = result.folders.reduce((sum, f) => sum + f.archived.length, 0);
    expect(totalArchived).toBeGreaterThanOrEqual(1);
  });

  test("regular:false → regular nodes removed; periodic kept", () => {
    const sessions = [makeRegular(), makePeriodic()];
    const tree = computeUnifiedTree(sessions, WS);
    const result = filterUnifiedTree(tree, { regular: false, periodic: true, archived: true, tasks: true });
    result.folders.forEach((folder) => {
      folder.conversations.forEach((node) => {
        expect(node.category).not.toBe("conversations");
      });
    });
    const totalPeriodic = result.folders.reduce((sum, f) => sum + f.conversations.length, 0);
    expect(totalPeriodic).toBeGreaterThanOrEqual(1);
  });

  test("periodic:false → periodic nodes removed", () => {
    const sessions = [makeRegular(), makePeriodic()];
    const tree = computeUnifiedTree(sessions, WS);
    const result = filterUnifiedTree(tree, { regular: true, periodic: false, archived: true, tasks: true });
    result.folders.forEach((folder) => {
      folder.conversations.forEach((node) => {
        expect(node.category).not.toBe("periodic");
      });
    });
  });

  test("archived:false → every folder's archived is []", () => {
    const sessions = [makeRegular(), makeArchived()];
    const tree = computeUnifiedTree(sessions, WS);
    const result = filterUnifiedTree(tree, { regular: true, periodic: true, archived: false, tasks: true });
    result.folders.forEach((folder) => {
      expect(folder.archived).toEqual([]);
    });
  });

  test("tasks:false → every folder has showTasks === false", () => {
    const sessions = [makeRegular()];
    const tree = computeUnifiedTree(sessions, WS);
    const result = filterUnifiedTree(tree, { regular: true, periodic: true, archived: true, tasks: false });
    result.folders.forEach((folder) => {
      expect(folder.showTasks).toBe(false);
    });
  });

  test("pruning: folder with only regular sessions is removed when regular:false", () => {
    const sessions = [makeRegular()];
    const tree = computeUnifiedTree(sessions, WS);
    const result = filterUnifiedTree(tree, { regular: false, periodic: false, archived: false, tasks: false });
    expect(result.folders).toHaveLength(0);
  });

  test("hiding a periodic parent drops the whole subtree", () => {
    const parent = makePeriodic({ session_id: "parent-1" });
    const child = makeRegular({ session_id: "child-1", parent_session_id: "parent-1" });
    const sessions = [parent, child];
    const tree = computeUnifiedTree(sessions, WS);
    const result = filterUnifiedTree(tree, { regular: true, periodic: false, archived: true, tasks: true });
    // parent (periodic) should not appear
    result.folders.forEach((folder) => {
      folder.conversations.forEach((node) => {
        expect(node.session_id).not.toBe("parent-1");
      });
    });
  });

  test("null/undefined tree → { dashboard: null, folders: [] }", () => {
    expect(filterUnifiedTree(null, {})).toEqual({ dashboard: null, folders: [] });
    expect(filterUnifiedTree(undefined, {})).toEqual({ dashboard: null, folders: [] });
  });

  test("missing filter (undefined) → treated as all-true", () => {
    const sessions = [makeRegular(), makePeriodic(), makeArchived()];
    const tree = computeUnifiedTree(sessions, WS);
    const result = filterUnifiedTree(tree, undefined);
    result.folders.forEach((folder) => {
      expect(folder.showTasks).toBe(true);
    });
    const totalConvs = result.folders.reduce((sum, f) => sum + f.conversations.length, 0);
    expect(totalConvs).toBeGreaterThanOrEqual(2);
  });
});
