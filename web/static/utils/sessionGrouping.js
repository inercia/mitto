// Mitto Web Interface - Session Grouping Utilities
// Pure functions for grouping sessions by server / folder / workspace.
// These are extracted from SessionList so they can be unit-tested independently.

import { buildSessionTree } from "./sessionTree.js";
import { getBasename, getGlobalWorkingDir } from "../lib.js";
import { getFilterTabForSession, FILTER_TAB } from "./storage.js";

// ---------------------------------------------------------------------------
// Fingerprint
// ---------------------------------------------------------------------------

/**
 * Compute a structural fingerprint for the current set of filtered sessions
 * and grouping mode. The fingerprint intentionally EXCLUDES isStreaming and
 * other volatile per-session flags so that groupedSessions is NOT recomputed
 * every time the agent sends a message chunk. Only structural changes (new
 * sessions, renames, parent changes, archival) trigger a rebuild.
 *
 * @param {Array} filteredSessions - Sessions currently visible in the active tab
 * @param {string} groupingMode    - Current grouping mode ('none'|'server'|'folder'|'workspace')
 * @returns {string}
 */
export function computeSessionFingerprint(filteredSessions, groupingMode) {
  return (
    groupingMode +
    "\n" +
    filteredSessions
      .map(
        (s) =>
          `${s.session_id}|${s.parent_session_id || ""}|${s.working_dir || ""}|${s.archived || false}|${s.periodic_enabled || false}|${s.pinned || false}|${s.name || ""}`,
      )
      .sort()
      .join("\n")
  );
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

function getSessionInfo(session) {
  return {
    workingDir:
      session.working_dir || getGlobalWorkingDir(session.session_id) || "",
    acpServer: session.acp_server || "",
  };
}

function resolveRootParent(session, sessionById) {
  let current = session;
  let depth = 0;
  while (current.parent_session_id && depth < 10) {
    const parent = sessionById.get(current.parent_session_id);
    if (!parent) break;
    current = parent;
    depth++;
  }
  return current;
}

function sortByCreatedAtDesc(arr) {
  return arr.sort(
    (a, b) => new Date(b.created_at || 0) - new Date(a.created_at || 0),
  );
}

function buildParentChildTree(sessions, allKnownSessionIds) {
  const { rootSessions, childrenMap, orphans } = buildSessionTree(
    sessions,
    allKnownSessionIds,
  );

  const parents = rootSessions.map((parent) => ({
    ...parent,
    children: sortByCreatedAtDesc(childrenMap.get(parent.session_id) || []),
  }));

  sortByCreatedAtDesc(parents);
  sortByCreatedAtDesc(orphans);

  return [...parents, ...orphans];
}

// ---------------------------------------------------------------------------
// Folder mode (hierarchical)
// ---------------------------------------------------------------------------

function computeFolderGroups(filteredSessions, allSessions, workspaces) {
  const sessionById = new Map(filteredSessions.map((s) => [s.session_id, s]));
  const allKnownSessionIds = new Set(allSessions.map((s) => s.session_id));

  const folderGroups = new Map();

  filteredSessions.forEach((session) => {
    const rootParent = resolveRootParent(session, sessionById);
    const { workingDir } = getSessionInfo(rootParent);
    const folderKey = workingDir || "Unknown";

    if (!folderGroups.has(folderKey)) {
      folderGroups.set(folderKey, {
        label: (() => {
          if (!workingDir) return "Unknown";
          const ws = workspaces.find((w) => w.working_dir === workingDir);
          return ws?.name || getBasename(workingDir);
        })(),
        workingDir,
        allSessions: [],
      });
    }

    folderGroups.get(folderKey).allSessions.push(session);
  });

  return Array.from(folderGroups.entries())
    .map(([key, folder]) => ({
      key,
      label: folder.label,
      workingDir: folder.workingDir,
      isHierarchical: true,
      isParentChild: true,
      sessions: buildParentChildTree(folder.allSessions, allKnownSessionIds),
    }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

// ---------------------------------------------------------------------------
// Flat modes: server and workspace
// ---------------------------------------------------------------------------

function computeFlatGroups(filteredSessions, groupingMode, allSessions, workspaces) {
  const sessionById = new Map(filteredSessions.map((s) => [s.session_id, s]));
  const allKnownSessionIds = new Set(allSessions.map((s) => s.session_id));

  const groups = new Map();

  filteredSessions.forEach((session) => {
    const groupSession = resolveRootParent(session, sessionById);

    let groupKey;
    let groupLabel;
    let groupWorkingDir = "";
    let groupAcpServer = "";

    if (groupingMode === "server") {
      const { acpServer } = getSessionInfo(groupSession);
      groupKey = acpServer || "Unknown";
      groupLabel = groupKey;
    } else {
      // workspace mode
      const { workingDir, acpServer } = getSessionInfo(groupSession);
      groupKey = `${workingDir}|${acpServer}`;
      const ws = workspaces.find(
        (w) =>
          w.working_dir === workingDir &&
          (!acpServer || w.acp_server === acpServer),
      );
      groupLabel = ws?.name || (workingDir ? getBasename(workingDir) : "Unknown");
      groupWorkingDir = workingDir;
      groupAcpServer = acpServer;
    }

    if (!groups.has(groupKey)) {
      groups.set(groupKey, {
        label: groupLabel,
        sessions: [],
        workingDir: groupWorkingDir,
        acpServer: groupAcpServer,
      });
    }
    groups.get(groupKey).sessions.push(session);
  });

  // Build parent-child tree within each group
  groups.forEach((group) => {
    group.sessions = buildParentChildTree(group.sessions, allKnownSessionIds);
    group.isParentChild = true;
  });

  return Array.from(groups.entries())
    .map(([key, value]) => ({ key, ...value }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

// ---------------------------------------------------------------------------
// Main exported function
// ---------------------------------------------------------------------------

/**
 * Compute the grouped session structure for the sidebar.
 *
 * @param {Array}  filteredSessions - Sessions to group (already filtered by tab)
 * @param {string} groupingMode     - 'none' | 'server' | 'folder' | 'workspace'
 * @param {Array}  allSessions      - All sessions across all tabs (for allKnownSessionIds)
 * @param {Array}  workspaces       - Workspace metadata list (for labels / names)
 * @returns {Array|null} null when groupingMode is 'none'; array of group objects otherwise
 */
// ---------------------------------------------------------------------------
// Unified tree (new sidebar model)
// ---------------------------------------------------------------------------

/**
 * Recursively annotate a list of conversation nodes with a `category` field.
 * Children arrays are also annotated (new objects; inputs are not mutated).
 *
 * @param {Array} nodes - Array of session nodes (may have .children)
 * @returns {Array} New array of annotated nodes
 */
function annotateWithCategory(nodes) {
  return nodes.map((node) => ({
    ...node,
    category: getFilterTabForSession(node),
    children: annotateWithCategory(node.children || []),
  }));
}

/**
 * Compute the unified sidebar tree over ALL sessions (regular + periodic +
 * archived) without any tab pre-filtering. Returns a stable data model with
 * static injected nodes (dashboard, per-folder tasks) and conversation nodes
 * annotated with their category and partitioned into active vs. archived roots.
 *
 * @param {Array}  allSessions - Full session list (may be undefined/null)
 * @param {Array}  workspaces  - Workspace metadata list (for labels / names)
 * @returns {{ dashboard: Object, folders: Array }}
 */
export function computeUnifiedTree(allSessions, workspaces = []) {
  const sessions = allSessions || [];

  const dashboard = { type: "dashboard", id: "__dashboard__", label: "Dashboard" };

  if (sessions.length === 0) {
    return { dashboard, folders: [] };
  }

  const folderGroups = computeFolderGroups(sessions, sessions, workspaces);

  const folders = folderGroups.map((folder) => {
    const annotated = annotateWithCategory(folder.sessions);

    const conversations = annotated.filter(
      (node) => node.category !== FILTER_TAB.ARCHIVED,
    );
    const archived = annotated.filter(
      (node) => node.category === FILTER_TAB.ARCHIVED,
    );

    const key = folder.key;
    return {
      key,
      label: folder.label,
      workingDir: folder.workingDir,
      tasksNode: {
        type: "tasks",
        id: `tasks:${key}`,
        label: "Tasks",
        workingDir: folder.workingDir,
        folderKey: key,
      },
      conversations,
      archived,
    };
  });

  return { dashboard, folders };
}

/**
 * Filter the unified tree by category visibility (mitto-1er.10).
 *
 * Pure predicate applied to the unified tree (after grouping/nesting), before
 * render. Category derives from each conversation node's `category`
 * (getFilterTabForSession): conversations→regular, periodic→periodic,
 * archived→archived. Per-folder Tasks nodes are the 'tasks' category.
 *
 * Hiding a parent conversation hides its children too (the whole subtree is
 * dropped). A folder with no visible conversations, no visible archived, and
 * Tasks hidden is pruned entirely.
 *
 * @param {{dashboard: Object, folders: Array}} tree - from computeUnifiedTree
 * @param {{regular: boolean, periodic: boolean, archived: boolean, tasks: boolean}} filter
 * @returns {{dashboard: Object, folders: Array}} new tree; each folder gains showTasks
 */
export function filterUnifiedTree(tree, filter) {
  if (!tree) return { dashboard: null, folders: [] };
  const f = filter || {};
  const regular = f.regular !== false;
  const periodic = f.periodic !== false;
  const archived = f.archived !== false;
  const tasks = f.tasks !== false;

  const categoryEnabled = (category) => {
    if (category === FILTER_TAB.PERIODIC) return periodic;
    if (category === FILTER_TAB.ARCHIVED) return archived;
    return regular;
  };

  const filterNodes = (nodes) =>
    (nodes || [])
      .filter((node) => categoryEnabled(node.category))
      .map((node) => ({
        ...node,
        children: filterNodes(node.children || []),
      }));

  const folders = (tree.folders || [])
    .map((folder) => ({
      ...folder,
      conversations: filterNodes(folder.conversations),
      archived: archived ? filterNodes(folder.archived) : [],
      showTasks: tasks,
    }))
    .filter(
      (folder) =>
        folder.conversations.length > 0 ||
        folder.archived.length > 0 ||
        folder.showTasks,
    );

  return { dashboard: tree.dashboard, folders };
}

export function computeGroupedSessions(
  filteredSessions,
  groupingMode,
  allSessions,
  workspaces,
) {
  if (groupingMode === "none") {
    return null;
  }
  if (groupingMode === "folder") {
    return computeFolderGroups(filteredSessions, allSessions, workspaces);
  }
  return computeFlatGroups(filteredSessions, groupingMode, allSessions, workspaces);
}
