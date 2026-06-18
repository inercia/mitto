// Mitto Web Interface - Session List Component
const { html, Fragment, useState, useMemo, useCallback, useEffect, useRef } = window.preact;

import { apiUrl } from "../utils/api.js";
import { authFetch } from "../utils/csrf.js";

import {
  computeUnifiedTree,
  filterUnifiedTree,
  computeFolderGroupSections,
} from "../utils/sessionGrouping.js";
import {
  getFilterTabForSession,
  getCategoryFilter,
  setCategoryFilter,
  getExpandedGroups,
  isGroupExpanded,
  setGroupExpanded,
  getSingleExpandedGroupMode,
  onUIPreferencesLoaded,
  getLastSessionForGroup,
  setLastSessionForGroup,
  getDensity,
  setDensity,
} from "../utils/index.js";
import { computeAllSessions, getBasename, getGlobalWorkingDir } from "../lib.js";
import { SessionItem } from "./SessionItem.js";
import { ContextMenu } from "./ContextMenu.js";
import { Modal } from "./Modal.js";
import {
  FolderIcon,
  FolderOpenIcon,
  SpinnerIcon,
  PlusIcon,
  CloseIcon,
  ArchiveIcon,
  SunIcon,
  MoonIcon,
  KeyboardIcon,
  SettingsIcon,
  RobotIcon,
  BeadsIcon,
  HomeIcon,
  FilterIcon,
  TerminalIcon,
  EllipsisIcon,
  ChatBubbleIcon,
  LayersIcon,
  CheckIcon,
  SlidersIcon,
  SearchIcon,
  RefreshIcon,
  BroomIcon,
  LightningIcon,
  getPromptIconOrDefault,
} from "./Icons.js";

// Module-level cache for git changes: keyed by workingDir.
// Each entry: { data, ts } where ts is Date.now() of the last fetch.
const GIT_CHANGES_CACHE = {};
const GIT_CHANGES_TTL_MS = 30_000;
// In-flight fetch promises keyed by workingDir to avoid duplicate concurrent requests.
const GIT_CHANGES_IN_FLIGHT = {};

// Fetch git changes for a session's workingDir, with caching and in-flight dedup.
// Returns { files, is_git_repo, branch } or null on error.
async function fetchGitChanges(sessionId) {
  try {
    const response = await authFetch(apiUrl(`/api/sessions/${sessionId}/changes`));
    if (!response.ok) return null;
    return await response.json();
  } catch {
    return null;
  }
}

// Get git changes for a workingDir: cache-first, then fetch via representative sessionId.
// Returns a promise resolving to the data or null.
function getGitChanges(workingDir, sessionId) {
  const now = Date.now();
  const cached = GIT_CHANGES_CACHE[workingDir];
  if (cached && now - cached.ts < GIT_CHANGES_TTL_MS) {
    return Promise.resolve(cached.data);
  }
  if (GIT_CHANGES_IN_FLIGHT[workingDir]) {
    return GIT_CHANGES_IN_FLIGHT[workingDir];
  }
  const promise = fetchGitChanges(sessionId).then((data) => {
    GIT_CHANGES_CACHE[workingDir] = { data, ts: Date.now() };
    delete GIT_CHANGES_IN_FLIGHT[workingDir];
    return data;
  });
  GIT_CHANGES_IN_FLIGHT[workingDir] = promise;
  return promise;
}

const BEADS_STATS_CACHE = {};
const BEADS_STATS_TTL_MS = 30_000;
// In-flight fetch promises keyed by workingDir to avoid duplicate concurrent requests.
const BEADS_STATS_IN_FLIGHT = {};

// Fetch beads issue stats (counts by status) for a workingDir.
// Returns the summary object { open_issues, in_progress_issues, ready_issues,
// blocked_issues, total_issues, ... } or null on error / empty database.
async function fetchBeadsStats(workingDir) {
  try {
    const response = await authFetch(
      apiUrl(`/api/beads/stats?working_dir=${encodeURIComponent(workingDir)}`),
    );
    if (!response.ok) return null;
    const data = await response.json();
    if (!data || data.error) return null;
    return data.summary || null;
  } catch {
    return null;
  }
}

// Get beads stats for a workingDir: cache-first, then fetch, with in-flight dedup.
// Returns a promise resolving to the summary object or null.
function getBeadsStats(workingDir) {
  const now = Date.now();
  const cached = BEADS_STATS_CACHE[workingDir];
  if (cached && now - cached.ts < BEADS_STATS_TTL_MS) {
    return Promise.resolve(cached.data);
  }
  if (BEADS_STATS_IN_FLIGHT[workingDir]) {
    return BEADS_STATS_IN_FLIGHT[workingDir];
  }
  const promise = fetchBeadsStats(workingDir).then((data) => {
    BEADS_STATS_CACHE[workingDir] = { data, ts: Date.now() };
    delete BEADS_STATS_IN_FLIGHT[workingDir];
    return data;
  });
  BEADS_STATS_IN_FLIGHT[workingDir] = promise;
  return promise;
}

// Find the first session_id in a folder's conversations tree (recurse into children).
function findRepresentativeSessionId(nodes) {
  for (const node of nodes) {
    if (node.session_id) return node.session_id;
    if (node.children) {
      const found = findRepresentativeSessionId(node.children);
      if (found) return found;
    }
  }
  return null;
}

// The Archived subgroup expansion is intentionally NOT memorized: it always
// resets to collapsed when a group is (re)opened. Strip any persisted
// "archived:<folderKey>" entries when hydrating React state so stale values
// never leak an expanded Archived subgroup across loads.
function withoutArchivedKeys(groups) {
  const out = {};
  for (const key in groups) {
    if (!key.startsWith("archived:")) out[key] = groups[key];
  }
  return out;
}

export function SessionList({
  activeSessions,
  storedSessions,
  activeSessionId,
  onSelect,
  onNewSession,
  onRename,
  onDelete,
  onArchive,
  onClose,
  workspaces,
  theme,
  onToggleTheme,
  fontSize,
  onToggleFontSize,
  onShowSettings,
  onShowWorkspaces,
  onShowWorkspacesForFolder,
  onShowKeyboardShortcuts,
  configReadonly = false,
  rcFilePath = null,
  badgeClickEnabled = false,
  onBadgeClick,
  terminalActionEnabled = false,
  onFolderOpen,
  onMoveFolderToGroup, // Called with (workingDir, group) to reassign a folder's group
  onTerminalClick,
  onBeadsOpen,
  onBeadsCreate, // (workingDir) => open the new-issue side panel for a folder
  onFetchBeadsListPrompts, // async (workingDir) => menus:beadsList prompts[]
  onRunBeadsListPrompt, // (prompt, workingDir) => run a beadsList prompt
  onBeadsRefresh, // (workingDir) => open the beads view and refresh its list
  onBeadsCleanup, // (workingDir) => open the beads view and clean up closed issues
  onShowDashboard,
  mainView = "conversation", // Current main-content view: "conversation" | "beads" | "dashboard"
  beadsWorkingDir = null, // Working dir whose Tasks (beads) view is open, when mainView === "beads"
  queueLength = 0,
  onFetchConversationPrompts, // Async (session, workingDir) => prompts[] for the context menu
  onSendPromptToConversation,
  onMakePeriodic, // Called with (session) to convert a regular session to periodic
  onMakeNonPeriodic, // Called with (session) to revert a periodic session to regular
  isCreatingSession = false, // True while ANY new-conversation request is in-flight or retrying
  creatingWorkingDirs = new Set(), // Set of workingDirs with an in-flight create request
}) {
  // Combine active and stored sessions using shared helper function
  const allSessions = useMemo(
    () => computeAllSessions(activeSessions, storedSessions),
    [activeSessions, storedSessions],
  );

  const isLight = theme === "light";
  const isLargeFont = fontSize === "large";

  // Unified sidebar (mitto-1er.4): folder is the only grouping mode; the filter
  // tabs and per-tab grouping were removed. SessionItem still takes a grouping hint.
  const groupingMode = "folder";
  // Track expanded groups in React state to avoid stale localStorage reads in WKWebView.
  // This mirrors the fix applied for navigableSessions (see expandedGroupsForNav in app.js).
  const [sidebarExpandedGroups, setSidebarExpandedGroups] = useState(() =>
    withoutArchivedKeys(getExpandedGroups()),
  );

  // Group header context menu state: { x, y, workingDir, label }
  const [groupContextMenu, setGroupContextMenu] = useState(null);
  const closeGroupContextMenu = () => setGroupContextMenu(null);

  // Per-folder "Tasks" entry context menu state: { x, y, workingDir, label }.
  // Mirrors groupContextMenu but for the static Tasks node. The beadsList
  // prompts shown in its "Tasks" submenu are loaded lazily when the menu opens.
  const [tasksContextMenu, setTasksContextMenu] = useState(null);
  const [tasksMenuPrompts, setTasksMenuPrompts] = useState([]);
  const [tasksMenuPromptsLoading, setTasksMenuPromptsLoading] = useState(false);
  const closeTasksContextMenu = () => setTasksContextMenu(null);
  const openTasksContextMenu = useCallback(
    (x, y, workingDir, label) => {
      setTasksContextMenu({ x, y, workingDir, label });
      setTasksMenuPrompts([]);
      if (onFetchBeadsListPrompts && workingDir) {
        setTasksMenuPromptsLoading(true);
        onFetchBeadsListPrompts(workingDir)
          .then((p) => setTasksMenuPrompts(p || []))
          .finally(() => setTasksMenuPromptsLoading(false));
      }
    },
    [onFetchBeadsListPrompts],
  );

  // "New group…" dialog state: { workingDir, label } when open, else null.
  const [newGroupDialog, setNewGroupDialog] = useState(null);
  const [newGroupName, setNewGroupName] = useState("");
  const newGroupInputRef = useRef(null);

  const [density, setDensityState] = useState(() => getDensity());
  const handleDensityChange = useCallback((mode) => {
    setDensityState(mode);
    setDensity(mode);
    setOpenToolbarMenu(null);
  }, []);

  // Git changes data keyed by workingDir, populated on demand in comfortable density.
  const [gitChangesMap, setGitChangesMap] = useState({});

  // Beads issue stats (counts by status) keyed by workingDir, populated on
  // demand for the open folder's Tasks line in comfortable density.
  const [beadsStatsMap, setBeadsStatsMap] = useState({});

  // Which side-panel toolbar dropdown is open ("filter" | "density" | null).
  // Controlled so the menus are mutually exclusive — opening one closes the other.
  const [openToolbarMenu, setOpenToolbarMenu] = useState(null);
  const toolbarRef = useRef(null);
  const handleToolbarMenuToggle = useCallback((key, willOpen) => {
    setOpenToolbarMenu((prev) => (willOpen ? key : prev === key ? null : prev));
  }, []);

  // Close the open toolbar dropdown when clicking outside the toolbar.
  useEffect(() => {
    if (!openToolbarMenu) return;
    const handleClickOutside = (e) => {
      if (toolbarRef.current && !toolbarRef.current.contains(e.target)) {
        setOpenToolbarMenu(null);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [openToolbarMenu]);

  // All organizational groups currently in use across folders (folders.json
  // group label, shared by all workspaces in a folder). Sorted case-insensitively.
  const allGroups = useMemo(() => {
    const set = new Set();
    (workspaces || []).forEach((ws) => {
      const g = (ws.group || "").trim();
      if (g) set.add(g);
    });
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  }, [workspaces]);

  // Current group for a given folder working dir (empty string when unassigned).
  const getFolderGroup = useCallback(
    (workingDir) => {
      const ws = (workspaces || []).find((w) => w.working_dir === workingDir);
      return (ws && ws.group ? ws.group.trim() : "") || "";
    },
    [workspaces],
  );

  // Focus the input when the new-group dialog opens.
  useEffect(() => {
    if (newGroupDialog) {
      setNewGroupName("");
      const t = setTimeout(() => newGroupInputRef.current?.focus(), 50);
      return () => clearTimeout(t);
    }
  }, [newGroupDialog]);

  const submitNewGroup = useCallback(() => {
    const name = newGroupName.trim();
    if (!name || !newGroupDialog) return;
    if (onMoveFolderToGroup) onMoveFolderToGroup(newGroupDialog.workingDir, name);
    setNewGroupDialog(null);
    setNewGroupName("");
  }, [newGroupName, newGroupDialog, onMoveFolderToGroup]);

  // Track new sessions for blink animation
  const [newSessionIds, setNewSessionIds] = useState(new Set());
  const previousSessionIdsRef = useRef(new Set());

  // Detect new sessions and trigger blink animation
  useEffect(() => {
    const currentSessionIds = new Set(allSessions.map((s) => s.session_id));
    const previousSessionIds = previousSessionIdsRef.current;

    // Find sessions that are new (in current but not in previous)
    const newIds = new Set();
    currentSessionIds.forEach((id) => {
      if (!previousSessionIds.has(id)) {
        newIds.add(id);
      }
    });

    if (newIds.size > 0) {
      setNewSessionIds(newIds);
      // Remove the new session IDs after animation completes (1.5s * 2 blinks = 3s)
      setTimeout(() => {
        setNewSessionIds(new Set());
      }, 3000);
    }

    // Update the ref for next comparison
    previousSessionIdsRef.current = currentSessionIds;
  }, [allSessions]);

  // Subscribe to UI preferences loaded from server (for macOS app where localStorage doesn't persist)
  useEffect(() => {
    const unsubscribe = onUIPreferencesLoaded(() => {
      setSidebarExpandedGroups(withoutArchivedKeys(getExpandedGroups()));
      console.debug("[Mitto] SessionList: UI preferences synced from server");
    });
    return unsubscribe;
  }, []);


  // Listen for programmatic group expansion changes (e.g., from swipe/keyboard navigation)
  // When expandGroupForSession in useWebSocket.js expands a group during session switching,
  // it dispatches mitto-expanded-groups-changed. We sync React state to avoid stale
  // localStorage reads in WKWebView.
  useEffect(() => {
    const handleExpandedGroupsChanged = (e) => {
      const { groupKey, expanded } = e.detail || {};
      if (groupKey !== undefined) {
        setSidebarExpandedGroups((prev) => ({ ...prev, [groupKey]: expanded }));
      } else {
        // Fallback: re-read from localStorage if no detail provided
        setSidebarExpandedGroups(withoutArchivedKeys(getExpandedGroups()));
      }
    };
    window.addEventListener(
      "mitto-expanded-groups-changed",
      handleExpandedGroupsChanged,
    );
    return () => {
      window.removeEventListener(
        "mitto-expanded-groups-changed",
        handleExpandedGroupsChanged,
      );
    };
  }, []);

  // Auto-scroll sidebar to show the active session when it changes programmatically
  // (e.g., from notification click, swipe navigation, or keyboard shortcut)
  useEffect(() => {
    if (!activeSessionId) return;

    // Scroll the active session into view after DOM updates.
    const scrollToActive = () => {
      const el = document.querySelector(
        `[data-session-id="${activeSessionId}"]`,
      );
      if (el) {
        el.scrollIntoView({ block: "nearest", behavior: "smooth" });
      }
    };

    requestAnimationFrame(scrollToActive);
  }, [activeSessionId]); // Intentionally minimal deps - only trigger on session change

  // Helper to check if a group is expanded using React state (not localStorage)
  // to avoid stale reads in WKWebView (macOS native app).
  const isSidebarGroupExpanded = useCallback((groupKey) => {
    if (groupKey in sidebarExpandedGroups) return sidebarExpandedGroups[groupKey];
    if (groupKey === "__archived__") return false;
    return true;
  }, [sidebarExpandedGroups]);

  // Handle group expand/collapse toggle
  const handleToggleGroup = useCallback(
    (groupKey, allGroupKeys = []) => {
      // Parent-child groups always behave as an accordion regardless of the setting.
      const isParentGroup = groupKey.startsWith("parent:");

      // Update React state (source of truth for sidebar rendering)
      setSidebarExpandedGroups((prev) => {
        const currentlyExpanded =
          groupKey in prev
            ? prev[groupKey]
            : groupKey === "__archived__"
              ? false
              : true;
        const willExpand = !currentlyExpanded;
        const next = { ...prev, [groupKey]: willExpand };
        if (willExpand && (getSingleExpandedGroupMode() || isParentGroup)) {
          for (const key of allGroupKeys) {
            if (key !== groupKey) next[key] = false;
          }
        }
        return next;
      });

      // Persist to localStorage (for cross-session persistence)
      const currentlyExpanded = isGroupExpanded(groupKey);
      const willExpand = !currentlyExpanded;
      if (willExpand && (getSingleExpandedGroupMode() || isParentGroup)) {
        for (const key of allGroupKeys) {
          if (key !== groupKey && isGroupExpanded(key)) {
            setGroupExpanded(key, false);
          }
        }
      }
      setGroupExpanded(groupKey, willExpand);
    },
    [sidebarExpandedGroups],
  );

  // --- Unified tree (mitto-1er.3) expansion helpers ---------------------------
  // The unified sidebar tree uses a SINGLE, untabbed expansion keyspace (per the
  // mitto-1er.1 spike): folder keys and the per-folder "archived:<key>" subgroup
  // key are stored unscoped. Parent-child keys ("parent:<id>") keep using
  // handleToggleGroup/isSidebarGroupExpanded (those already pass through unscoped).
  const isUnifiedFolderExpanded = (folderKey) =>
    folderKey in sidebarExpandedGroups ? sidebarExpandedGroups[folderKey] : true;
  const isUnifiedArchivedExpanded = (folderKey) => {
    const key = `archived:${folderKey}`;
    return key in sidebarExpandedGroups ? sidebarExpandedGroups[key] : false;
  };
  // Top-level folder-group sections ("group:<name>" / "group:__other__"). They
  // default to expanded and persist like folder keys, but do NOT participate in
  // folder accordion (single-expanded) mode — a group only wraps its folders.
  const isUnifiedGroupExpanded = (groupKey) =>
    groupKey in sidebarExpandedGroups ? sidebarExpandedGroups[groupKey] : true;
  const handleGroupSectionToggle = useCallback((groupKey, willOpen) => {
    setSidebarExpandedGroups((prev) => ({ ...prev, [groupKey]: willOpen }));
    setGroupExpanded(groupKey, willOpen);
  }, []);

  // Controlled <details> toggle for unified-tree folders and the archived subgroup.
  // willOpen is the <details> element's resulting open state. Folder-level toggles
  // honor accordion (single-expanded) mode across allFolderKeys; the archived
  // subgroup is exempt (it nests inside a folder).
  const handleUnifiedToggle = useCallback(
    (groupKey, willOpen, allFolderKeys = []) => {
      const isFolderKey = !groupKey.startsWith("archived:");
      const accordion = willOpen && isFolderKey && getSingleExpandedGroupMode();
      setSidebarExpandedGroups((prev) => {
        const next = { ...prev, [groupKey]: willOpen };
        if (accordion) {
          for (const k of allFolderKeys) {
            if (k !== groupKey) next[k] = false;
          }
        }
        return next;
      });
      if (accordion) {
        for (const k of allFolderKeys) {
          if (k !== groupKey && isGroupExpanded(k)) setGroupExpanded(k, false);
        }
      }
      // The Archived subgroup is never persisted: its expansion always resets to
      // collapsed when the parent group is reopened (handled in handleFolderOpened).
      if (isFolderKey) setGroupExpanded(groupKey, willOpen);
    },
    [],
  );


  // Helper to get session's working directory
  const getSessionWorkingDir = (session) => {
    const storedSession = storedSessions.find(
      (s) => s.session_id === session.session_id,
    );
    return (
      session.working_dir ||
      storedSession?.working_dir ||
      getGlobalWorkingDir(session.session_id) ||
      ""
    );
  };

  // Build a lookup map of session_id → true for sessions currently streaming.
  // This provides fresh streaming state for the unified tree render.
  const streamingMap = useMemo(() => {
    const map = new Map();
    allSessions.forEach((s) => {
      if (s.isStreaming) map.set(s.session_id, true);
    });
    return map;
  }, [allSessions]);

  // Build a lookup map of session_id → true for sessions currently waiting for children.
  const waitingMap = useMemo(() => {
    const map = new Map();
    allSessions.forEach((s) => {
      if (s.isWaitingForChildren) map.set(s.session_id, true);
    });
    return map;
  }, [allSessions]);

  // Build a lookup map of session_id → true for sessions currently waiting for user input.
  const uiPromptMap = useMemo(() => {
    const map = new Map();
    allSessions.forEach((s) => {
      if (s.isWaitingForUserInput) map.set(s.session_id, true);
    });
    return map;
  }, [allSessions]);


  // Unified sidebar tree (mitto-1er.3): a single folder-grouped tree over ALL
  // sessions (regular + periodic + archived), independent of the filter tab.
  const unifiedTree = useMemo(
    () => computeUnifiedTree(allSessions, workspaces),
    [allSessions, workspaces],
  );

  // Category visibility filter (mitto-1er.10): show/hide Regular/Periodic/
  // Archived/Tasks. Browser-session scoped (sessionStorage); all visible by
  // default. Applied as a pure predicate over the unified tree before render.
  const [categoryFilter, setCategoryFilterState] = useState(() =>
    getCategoryFilter(),
  );
  const handleCategoryToggle = useCallback((key) => {
    setCategoryFilterState((prev) => {
      const next = { ...prev, [key]: !prev[key] };
      setCategoryFilter(next);
      window.dispatchEvent(
        new CustomEvent("mitto-category-filter-changed", {
          detail: { filter: next },
        }),
      );
      return next;
    });
  }, []);
  const anyCategoryHidden =
    !categoryFilter.regular ||
    !categoryFilter.periodic ||
    !categoryFilter.archived ||
    !categoryFilter.tasks;
  const filteredTree = useMemo(
    () => filterUnifiedTree(unifiedTree, categoryFilter),
    [unifiedTree, categoryFilter],
  );

  // Fetch git changes and beads stats for expanded folders in comfortable
  // density. Both summary lines only render for the currently open folder, so
  // only fetch for those. Re-runs when density, the folder list, or the
  // expansion state changes.
  useEffect(() => {
    if (density !== "comfortable") return;
    const folders = filteredTree.folders || [];
    folders.forEach((folder) => {
      if (!folder.workingDir) return;
      if (!isUnifiedFolderExpanded(folder.key)) return;
      // Beads issue stats for the folder's Tasks line (no session needed).
      if (folder.showTasks) {
        getBeadsStats(folder.workingDir).then((data) => {
          setBeadsStatsMap((prev) => {
            if (prev[folder.workingDir] === data) return prev;
            return { ...prev, [folder.workingDir]: data };
          });
        });
      }
      // Git changes for the folder header line (needs a representative session).
      const sessionId = findRepresentativeSessionId(folder.conversations);
      if (!sessionId) return;
      getGitChanges(folder.workingDir, sessionId).then((data) => {
        setGitChangesMap((prev) => {
          if (prev[folder.workingDir] === data) return prev;
          return { ...prev, [folder.workingDir]: data };
        });
      });
    });
  }, [density, filteredTree.folders, sidebarExpandedGroups]);

  // Build a map from session ID → its family's parent group key ("parent:<id>").
  // Covers both the parent session itself and all its children.
  // Used by handleSelectWithCollapse to know which family a clicked session belongs to.
  const sessionFamilyMap = useMemo(() => {
    const map = new Map();
    unifiedTree.folders.forEach((folder) => {
      [...folder.conversations, ...folder.archived].forEach((session) => {
        if (session.children && session.children.length > 0) {
          const parentKey = `parent:${session.session_id}`;
          map.set(session.session_id, parentKey);
          session.children.forEach((child) => map.set(child.session_id, parentKey));
        }
      });
    });
    return map;
  }, [unifiedTree]);

  // Build a map from session ID → its folder key, covering root conversations,
  // their nested children, and archived sessions. Used to remember the
  // last-focused conversation per group.
  const sessionFolderMap = useMemo(() => {
    const map = new Map();
    const scan = (nodes, folderKey) => {
      (nodes || []).forEach((node) => {
        map.set(node.session_id, folderKey);
        scan(node.children, folderKey);
      });
    };
    unifiedTree.folders.forEach((folder) => {
      scan(folder.conversations, folder.key);
      scan(folder.archived, folder.key);
    });
    return map;
  }, [unifiedTree]);

  // Build a set of session IDs that live under an "Archived" subgroup (root
  // archived conversations and their nested children). Selecting one of these
  // must NOT collapse its folder's Archived subgroup.
  const archivedSessionSet = useMemo(() => {
    const set = new Set();
    const scan = (nodes) =>
      (nodes || []).forEach((node) => {
        set.add(node.session_id);
        scan(node.children);
      });
    unifiedTree.folders.forEach((folder) => scan(folder.archived));
    return set;
  }, [unifiedTree]);

  // Remember the last-focused conversation for its group whenever the active
  // session changes (clicks, keyboard/swipe nav, programmatic focus). Reopening
  // that group later restores this conversation (see handleFolderOpened).
  useEffect(() => {
    if (!activeSessionId) return;
    const folderKey = sessionFolderMap.get(activeSessionId);
    if (folderKey) setLastSessionForGroup(folderKey, activeSessionId);
  }, [activeSessionId, sessionFolderMap]);

  // Wrap onSelect to auto-collapse parent-child groups when selecting outside the family.
  // If the selected session belongs to a family (parent + its children), only that family
  // stays expanded. All other expanded parent groups are collapsed.
  const handleSelectWithCollapse = useCallback(
    (sessionId, opts) => {
      // Find which family (if any) this session belongs to
      const familyKey = sessionFamilyMap.get(sessionId);

      // Find all currently expanded parent groups
      const expandedParentKeys = Object.entries(sidebarExpandedGroups)
        .filter(([key, expanded]) => key.startsWith("parent:") && expanded)
        .map(([key]) => key);

      // If there are expanded parent groups and the selected session doesn't belong
      // to any of them, collapse all other parent groups
      if (expandedParentKeys.length > 0) {
        const shouldCollapse = expandedParentKeys.some((key) => key !== familyKey);
        if (shouldCollapse) {
          setSidebarExpandedGroups((prev) => {
            const next = { ...prev };
            for (const key of expandedParentKeys) {
              if (key !== familyKey) {
                next[key] = false;
              }
            }
            return next;
          });
          // Persist to localStorage
          for (const key of expandedParentKeys) {
            if (key !== familyKey) {
              setGroupExpanded(key, false);
            }
          }
        }
      }

      // Auto-collapse the folder's Archived subgroup when selecting a
      // non-archived item in that folder. Selecting an archived conversation
      // keeps its Archived subgroup open. (Archived expansion is never
      // persisted, so only React state is updated — see handleUnifiedToggle.)
      if (!archivedSessionSet.has(sessionId)) {
        const folderKey = sessionFolderMap.get(sessionId);
        if (folderKey) {
          const archivedKey = `archived:${folderKey}`;
          setSidebarExpandedGroups((prev) =>
            prev[archivedKey] === false
              ? prev
              : { ...prev, [archivedKey]: false },
          );
        }
      }

      // Call the original onSelect (opts threads through e.g. keepSidebarOpen
      // so an auto-select triggered by expanding a folder does not close the
      // mobile sidebar drawer — see handleFolderOpened).
      onSelect(sessionId, opts);
    },
    [
      onSelect,
      sessionFamilyMap,
      sessionFolderMap,
      archivedSessionSet,
      sidebarExpandedGroups,
    ],
  );

  // Whether a folder contains a given session (root conversation, nested child,
  // or archived). Used to validate a remembered session before refocusing it.
  const folderHasSession = (folder, sessionId) => {
    const scan = (nodes) =>
      (nodes || []).some(
        (node) => node.session_id === sessionId || scan(node.children),
      );
    return scan(folder.conversations) || scan(folder.archived);
  };

  // Default behaviors when a group is (re)opened:
  //  1. The Archived subgroup always collapses — never memorized.
  //  2. Focus the conversation last focused in this group; if none is
  //     remembered (or it no longer exists), open the group's Tasks view.
  // The auto-select/Tasks-open passes keepSidebarOpen so that, on mobile,
  // expanding a folder leaves the sidebar drawer open (the conversation or
  // Tasks view loads underneath). Direct conversation/Tasks clicks still close
  // the drawer.
  const handleFolderOpened = useCallback(
    (folder) => {
      const archivedKey = `archived:${folder.key}`;
      setSidebarExpandedGroups((prev) => {
        if (prev[archivedKey] === false) return prev;
        return { ...prev, [archivedKey]: false };
      });

      const remembered = getLastSessionForGroup(folder.key);
      if (remembered && folderHasSession(folder, remembered)) {
        handleSelectWithCollapse(remembered, { keepSidebarOpen: true });
      } else if (folder.workingDir) {
        onBeadsOpen && onBeadsOpen(folder.workingDir, { keepSidebarOpen: true });
      }
    },
    [handleSelectWithCollapse, onBeadsOpen],
  );


  // Render a single session item
  // hideBadge: if true, hides the entire badge
  // badgeHideAbbreviation: if true, badge hides 3-letter workspace code (used in workspace grouping mode)
  // badgeHideAcpServer: if true, badge hides ACP server name (used in ACP server grouping mode)
  // isSpawned: if true, shows a visual indicator that this session was spawned from another
  // extraLeftPadding: additional CSS class for left padding (e.g., "pl-6" for parent-child indentation)
  // childCount: number of child sessions (shows count indicator for collapsed parents)
  // hasChildStreaming: if true, shows streaming indicator for collapsed parent with streaming child
  const renderSessionItem = (
    session,
    {
      hideBadge = false,
      badgeHideAbbreviation = false,
      badgeHideAcpServer = false,
      isSpawned = false,
      extraLeftPadding = "",
      childCount = 0,
      hasChildStreaming = false,
      hasChildren = false,
      isExpanded = false,
      onToggleExpand = null,
    } = {},
  ) => {
    const workingDir = getSessionWorkingDir(session);
    const finalSession = workingDir
      ? { ...session, working_dir: workingDir }
      : session;
    // Get the session's ACP server (stored when session was created)
    const sessionAcpServer =
      session.acp_server || session.info?.acp_server || "";
    const workspaceDir = workingDir;
    // Find the workspace matching both working_dir AND acp_server
    // This is important when multiple workspaces share the same folder but use different ACP servers
    const workspace = workspaces.find(
      (ws) =>
        ws.working_dir === workspaceDir &&
        (!sessionAcpServer || ws.acp_server === sessionAcpServer),
    );
    // Only the active session can have queued messages
    const hasQueuedMessages =
      session.session_id === activeSessionId && queueLength > 0;
    // Check if the session is currently streaming (agent is responding)
    const isSessionStreaming = session.isStreaming || false;
    // Check if this is a new session (for blink animation)
    const isNew = newSessionIds.has(session.session_id);

    return html`
      <${SessionItem}
        key=${session.session_id}
        session=${finalSession}
        isActive=${activeSessionId === session.session_id &&
        mainView === "conversation"}
        onSelect=${handleSelectWithCollapse}
        onRename=${onRename}
        onDelete=${onDelete}
        onArchive=${onArchive}
        workspaceColor=${workspace?.color || null}
        workspaceCode=${workspace?.code || null}
        workspaceName=${workspace?.name || null}
        badgeClickEnabled=${badgeClickEnabled}
        onBadgeClick=${onBadgeClick}
        hasQueuedMessages=${hasQueuedMessages}
        isSessionStreaming=${isSessionStreaming}
        hideBadge=${hideBadge}
        badgeHideAbbreviation=${badgeHideAbbreviation}
        badgeHideAcpServer=${badgeHideAcpServer}
        isLightTheme=${isLight}
        filterTab=${session.category || getFilterTabForSession(session)}
        groupingMode=${groupingMode}
        onFetchConversationPrompts=${onFetchConversationPrompts}
        onSendPromptToConversation=${onSendPromptToConversation}
        onMakePeriodic=${onMakePeriodic}
        onMakeNonPeriodic=${onMakeNonPeriodic}
        isSpawned=${isSpawned}
        extraLeftPadding=${extraLeftPadding}
        childCount=${childCount}
        hasChildStreaming=${hasChildStreaming}
        hasChildren=${hasChildren}
        isExpanded=${isExpanded}
        onToggleExpand=${onToggleExpand}
        isNew=${isNew}
        density=${density}
      />
    `;
  };

  // Handle creating a new session in a specific folder group
  const handleNewSessionInFolder = useCallback(
    (workingDir, e) => {
      e.stopPropagation();

      // Find all workspaces matching this folder's working_dir
      const matchingWorkspaces = workspaces.filter(
        (ws) => ws.working_dir === workingDir,
      );

      if (matchingWorkspaces.length === 1) {
        // Single workspace - create session directly
        onNewSession(matchingWorkspaces[0], null);
      } else if (matchingWorkspaces.length > 1) {
        // Multiple workspaces - show dialog filtered to this folder
        onNewSession(null, workingDir);
      } else {
        // Fallback
        onNewSession(null, null);
      }
    },
    [workspaces, onNewSession],
  );

  // Get empty state message
  const getEmptyMessage = () => "No conversations yet";

  // Render the unified sidebar tree (mitto-1er.3): daisyUI `menu` with CONTROLLED
  // <details> expansion. Consumes computeUnifiedTree (Dashboard + folders, each with
  // conversations[]/archived[] and a Tasks node). Folders and the per-folder Archived
  // subgroup are controlled <details>; parent-child nesting reuses the existing
  // SessionItem expand/collapse mechanism. Static Dashboard/Tasks rows are placeholders
  // here — their behavior is wired in mitto-1er.7; per-category icons in mitto-1er.5.
  const renderUnifiedTree = () => {
    const { dashboard, folders } = filteredTree;
    const allFolderKeys = folders.map((f) => f.key);

    // All parent keys across the whole tree, so opening one parent collapses the
    // others (parent groups always behave as an accordion).
    const allParentKeys = [];
    folders.forEach((f) => {
      [...f.conversations, ...f.archived].forEach((s) => {
        if (s.children && s.children.length > 0) {
          allParentKeys.push(`parent:${s.session_id}`);
        }
      });
    });

    const countNodes = (nodes) =>
      nodes.reduce(
        (sum, s) => sum + 1 + (s.children ? s.children.length : 0),
        0,
      );
    const hasStreaming = (nodes) =>
      nodes.some(
        (s) =>
          streamingMap.has(s.session_id) ||
          (s.children &&
            s.children.some((c) => streamingMap.has(c.session_id))),
      );

    // Render a list of root session nodes (with nested children) — shared by the
    // folder conversations[] list and the archived[] subgroup.
    const renderSessionNodes = (nodes) =>
      nodes.map((session) => {
        const hasChildren =
          session.children && session.children.length > 0;
        const parentKey = `parent:${session.session_id}`;
        // Children are collapsed by default and expand only when the user clicks
        // the child-count badge. The manual choice is tracked (and persisted) via
        // sidebarExpandedGroups; we never auto-expand based on the active session.
        const childrenExpanded = hasChildren
          ? parentKey in sidebarExpandedGroups
            ? sidebarExpandedGroups[parentKey]
            : false
          : false;
        const hasChildStreaming =
          hasChildren &&
          session.children.some((c) => streamingMap.has(c.session_id));
        return html`
          <div
            key=${session.session_id}
            class="parent-session-group ${hasChildren
              ? "has-children"
              : ""}"
          >
            ${renderSessionItem(
              {
                ...session,
                isStreaming: streamingMap.has(session.session_id),
                isWaitingForChildren: waitingMap.has(session.session_id),
                isWaitingForUserInput: uiPromptMap.has(session.session_id),
              },
              {
                // The folder tree already groups by workspace, so the only
                // thing the pill would show here is the agent/ACP name — hide
                // the badge entirely to keep conversation rows compact.
                hideBadge: true,
                badgeHideAbbreviation: true,
                badgeHideAcpServer: false,
                isSpawned: !hasChildren && !!session._parentId,
                childCount: hasChildren ? session.children.length : 0,
                hasChildStreaming:
                  hasChildren && !childrenExpanded && hasChildStreaming,
                hasChildren: hasChildren,
                isExpanded: childrenExpanded,
                onToggleExpand: hasChildren
                  ? () => handleToggleGroup(parentKey, allParentKeys)
                  : null,
              },
            )}
            ${hasChildren &&
            html`
              <div
                class="session-children ${childrenExpanded
                  ? "session-children--expanded"
                  : ""}"
              >
                ${session.children.map(
                  (child) =>
                    html`<div class="session-item--child">
                      ${renderSessionItem(
                        {
                          ...child,
                          isStreaming: streamingMap.has(child.session_id),
                          isWaitingForChildren: waitingMap.has(
                            child.session_id,
                          ),
                          isWaitingForUserInput: uiPromptMap.has(
                            child.session_id,
                          ),
                        },
                        {
                          // The folder tree already groups by workspace, so the
                          // child pill would only show the agent/ACP name — hide
                          // the badge entirely to match the parent rows.
                          hideBadge: true,
                          badgeHideAbbreviation: true,
                          badgeHideAcpServer: false,
                          isSpawned: true,
                          extraLeftPadding: "pl-8",
                        },
                      )}
                    </div>`,
                )}
              </div>
            `}
          </div>
        `;
      });

    return html`
      <ul class="menu menu-sm w-full p-0 flex-nowrap">
        <!-- Dashboard (static, top-level) — clears the active session to show
             the no-session view. Not a conversation; excluded from nav. -->
        <li>
          <button
            type="button"
            onClick=${() => onShowDashboard && onShowDashboard()}
            aria-current=${!activeSessionId ? "page" : undefined}
            class="gap-2 text-sm ${!activeSessionId
              ? "text-mitto-text-strong bg-mitto-surface-3"
              : "text-mitto-text-muted"}"
          >
            <${HomeIcon} className="w-4 h-4 shrink-0" />
            <span class="truncate">${dashboard.label}</span>
          </button>
        </li>
        ${(() => {
          // When any folder has a group assigned, render collapsible group
          // sections (named groups + a trailing "Other" for ungrouped folders).
          // Otherwise render folders as a flat list (unchanged behavior).
          const { grouped, sections } = computeFolderGroupSections(folders);
          return grouped
            ? sections.map(renderGroupSectionLi)
            : folders.map(renderFolderLi);
        })()}
      </ul>
    `;

    // Render a single folder <li> (shared by the flat and grouped layouts).
    // Declared as a hoisted function so it can be referenced by the IIFE above
    // and by renderGroupSectionLi regardless of source order.
    function renderFolderLi(folder) {
          const folderExpanded = isUnifiedFolderExpanded(folder.key);
          const archivedExpanded = isUnifiedArchivedExpanded(folder.key);
          // Count badge excludes archived conversations (active conversations only).
          const totalSessions = countNodes(folder.conversations);
          const hasFolderStreaming =
            hasStreaming(folder.conversations) ||
            hasStreaming(folder.archived);
          // The Tasks (beads) entry carries the focus highlight while its
          // folder's beads view is the active main-content view.
          const tasksActive =
            mainView === "beads" && beadsWorkingDir === folder.workingDir;
          return html`
            <li
              key=${folder.key}
              class="folder-group min-w-0 ${density === "comfortable"
                ? "mt-2"
                : ""}"
            >
              <details
                class="min-w-0 w-full"
                open=${folderExpanded}
                onToggle=${(e) => {
                  const open = e.currentTarget.open;
                  if (open !== folderExpanded) {
                    handleUnifiedToggle(folder.key, open, allFolderKeys);
                    if (open) handleFolderOpened(folder);
                  }
                }}
              >
                <summary
                  class="block text-sm font-medium text-mitto-text-muted after:hidden"
                  onContextMenu=${(e) => {
                    if (folder.workingDir) {
                      e.preventDefault();
                      e.stopPropagation();
                      setGroupContextMenu({
                        x: e.clientX,
                        y: e.clientY,
                        workingDir: folder.workingDir,
                        label: folder.label,
                      });
                    }
                  }}
                  data-has-context-menu=${folder.workingDir
                    ? "true"
                    : undefined}
                >
                  <div class="flex items-center gap-2">
                    ${hasFolderStreaming
                      ? html`
                          <span
                            class="loading loading-ring loading-xs shrink-0 text-mitto-accent"
                            title="Agent responding in this folder"
                          ></span>
                        `
                      : html`<${FolderIcon} className="w-4 h-4 shrink-0" />`}
                    <span class="truncate min-w-0" title=${folder.workingDir}>
                      ${folder.label}
                    </span>
                    <span class="flex-1"></span>
                    <span
                      class="badge badge-sm badge-ghost shrink-0 tabular-nums"
                      >${totalSessions}</span
                    >
                    ${(() => {
                      const folderCreating = creatingWorkingDirs.has(folder.workingDir);
                      return html`<button
                        type="button"
                        onClick=${(e) => {
                          e.preventDefault();
                          e.stopPropagation();
                          if (!folderCreating)
                            handleNewSessionInFolder(folder.workingDir, e);
                        }}
                        class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong ${folderCreating
                          ? "cursor-wait opacity-60"
                          : ""}"
                        title=${folderCreating
                          ? "Creating conversation\u2026"
                          : `New conversation in ${folder.label}`}
                        disabled=${folderCreating}
                      >
                        ${folderCreating
                          ? html`<${SpinnerIcon} className="w-3.5 h-3.5 animate-spin" />`
                          : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                      </button>`;
                    })()}
                    ${folder.workingDir &&
                    html`
                      <button
                        type="button"
                        onClick=${(e) => {
                          e.preventDefault();
                          e.stopPropagation();
                          const rect = e.currentTarget.getBoundingClientRect();
                          setGroupContextMenu({
                            x: rect.left,
                            y: rect.bottom,
                            workingDir: folder.workingDir,
                            label: folder.label,
                          });
                        }}
                        class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong"
                        title="More actions"
                        aria-label="More actions"
                      >
                        <${EllipsisIcon} className="w-3.5 h-3.5" />
                      </button>
                    `}
                  </div>
                  ${density === "comfortable" && folderExpanded && folder.workingDir && (() => {
                    const gitData = gitChangesMap[folder.workingDir];
                    if (!gitData || !gitData.is_git_repo) return null;
                    const files = gitData.files || [];
                    const modified = files.filter((f) => f.status === "M" || f.status === "R" || f.status === "C").length;
                    const added = files.filter((f) => f.status === "A").length;
                    const deleted = files.filter((f) => f.status === "D").length;
                    const untracked = files.filter((f) => f.status === "?").length;
                    if (!modified && !added && !deleted && !untracked) return null;
                    const MAX_BRANCH_LEN = 18;
                    const branchDisplay =
                      gitData.branch && gitData.branch.length > MAX_BRANCH_LEN
                        ? "…" + gitData.branch.slice(-MAX_BRANCH_LEN)
                        : gitData.branch;
                    const parts = [];
                    if (modified) parts.push(html`<span class="text-amber-400">✎${modified}</span>`);
                    if (added) parts.push(html`<span class="text-green-400">+${added}</span>`);
                    if (deleted) parts.push(html`<span class="text-red-400">−${deleted}</span>`);
                    if (untracked) parts.push(html`<span class="text-mitto-text-muted">?${untracked}</span>`);
                    return html`
                      <div class="text-[0.5625rem] font-normal italic text-mitto-text-muted truncate mt-0.5 pl-6 flex items-center gap-1.5">
                        ${gitData.branch ? html`<${Fragment}><span title=${gitData.branch}>⎇ ${branchDisplay}</span><span>·</span></${Fragment}>` : null}
                        ${parts}
                      </div>
                    `;
                  })()}
                </summary>
                <ul>
                  ${folder.showTasks &&
                  html`
                    <!-- Tasks (static, per-folder) — always the first entry in a
                         project. Opens the Beads view for this folder. Not a
                         conversation; excluded from nav. -->
                    <li>
                      <div
                        role="button"
                        tabindex="0"
                        onClick=${(e) => {
                          e.preventDefault();
                          e.stopPropagation();
                          // Selecting Tasks collapses this folder's Archived
                          // subgroup (matches conversation-selection behavior).
                          const archivedKey = `archived:${folder.key}`;
                          setSidebarExpandedGroups((prev) =>
                            prev[archivedKey] === false
                              ? prev
                              : { ...prev, [archivedKey]: false },
                          );
                          onBeadsOpen && onBeadsOpen(folder.workingDir);
                        }}
                        aria-current=${tasksActive ? "page" : undefined}
                        class="flex flex-col gap-0.5 items-stretch text-sm border-0! ${tasksActive
                          ? "bg-mitto-accent text-mitto-accent-fg"
                          : "text-mitto-text-muted"}"
                        title="Beads issues: ${folder.workingDir}"
                      >
                        <!-- Top row: icon, label, and trailing action buttons.
                             The stats row below lives inside the same clickable
                             container so the whole entry behaves as one unit
                             (matches regular conversation items). -->
                        <div class="flex items-center gap-2 min-w-0 w-full">
                          <${BeadsIcon} className="w-4 h-4 shrink-0" />
                          <span class="truncate min-w-0"
                            >${folder.tasksNode.label}</span
                          >
                          <span class="flex-1"></span>
                          ${folder.workingDir &&
                          html`
                            <button
                              type="button"
                              onClick=${(e) => {
                                e.preventDefault();
                                e.stopPropagation();
                                onBeadsCreate &&
                                  onBeadsCreate(folder.workingDir);
                              }}
                              class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong"
                              title="New issue"
                              aria-label="New issue"
                            >
                              <${PlusIcon} className="w-3.5 h-3.5" />
                            </button>
                            <button
                              type="button"
                              onClick=${(e) => {
                                e.preventDefault();
                                e.stopPropagation();
                                const rect =
                                  e.currentTarget.getBoundingClientRect();
                                openTasksContextMenu(
                                  rect.left,
                                  rect.bottom,
                                  folder.workingDir,
                                  folder.tasksNode.label,
                                );
                              }}
                              class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong"
                              title="More actions"
                              aria-label="More actions"
                            >
                              <${EllipsisIcon} className="w-3.5 h-3.5" />
                            </button>
                          `}
                        </div>
                        ${density === "comfortable" && folderExpanded && (() => {
                          const stats = beadsStatsMap[folder.workingDir];
                          if (!stats) return null;
                          const open = stats.open_issues || 0;
                          const inProgress = stats.in_progress_issues || 0;
                          const ready = stats.ready_issues || 0;
                          const blocked = stats.blocked_issues || 0;
                          const total = stats.total_issues || 0;
                          if (!total) return null;
                          return html`
                            <!-- w-full + min-w-0: the parent button is a flex
                                 column where daisyUI's menu rule forces
                                 align-items:center, which would shrink this row
                                 to its content and center it (pushing the stats
                                 ~22px right of the label). Stretching it full
                                 width keeps its box at the content edge so the
                                 pl-6 indent lands the text under the label —
                                 matching the folder git line and conversation
                                 subtitle second-line style. -->
                            <div class="text-[0.5625rem] font-normal italic truncate pl-6 w-full min-w-0 flex items-center gap-1.5 ${tasksActive ? "text-mitto-accent-fg/80" : "text-mitto-text-muted"}">
                              <span title="${open} open">○ ${open}</span>
                              <span class="${tasksActive ? "" : "text-amber-400"}" title="${inProgress} in progress">◐ ${inProgress}</span>
                              <span class="${tasksActive ? "" : "text-green-400"}" title="${ready} ready">● ${ready}</span>
                              ${blocked ? html`<span class="${tasksActive ? "" : "text-red-400"}" title="${blocked} blocked">⊘ ${blocked}</span>` : null}
                            </div>
                          `;
                        })()}
                      </div>
                    </li>
                  `}
                  ${renderSessionNodes(folder.conversations)}
                  ${folder.archived.length > 0 &&
                  html`
                    <li class="archived-subgroup min-w-0">
                      <details
                        class="min-w-0 w-full"
                        open=${archivedExpanded}
                        onToggle=${(e) => {
                          const open = e.currentTarget.open;
                          if (open !== archivedExpanded) {
                            handleUnifiedToggle(
                              `archived:${folder.key}`,
                              open,
                              allFolderKeys,
                            );
                          }
                        }}
                      >
                        <summary
                          class="flex items-center gap-2 text-sm text-mitto-text-muted after:hidden"
                        >
                          <${ArchiveIcon} className="w-4 h-4 shrink-0" />
                          <span class="truncate">Archived</span>
                          <span class="flex-1"></span>
                          <span
                            class="badge badge-sm badge-ghost shrink-0 tabular-nums"
                            >${folder.archived.length}</span
                          >
                        </summary>
                        <ul>${renderSessionNodes(folder.archived)}</ul>
                      </details>
                    </li>
                  `}
                </ul>
              </details>
            </li>
          `;
    }

    // Render a top-level group section (collapsible) that wraps its folders.
    // Only used when at least one folder has a group assigned. The synthetic
    // "Other" section (section.isOther) collects ungrouped folders and is
    // styled distinctly (muted + italic) so it reads apart from named groups.
    function renderGroupSectionLi(section) {
      const expanded = isUnifiedGroupExpanded(section.key);
      return html`
        <li key=${section.key} class="folder-group-section min-w-0 mt-4">
          <details
            class="min-w-0 w-full"
            open=${expanded}
            onToggle=${(e) => {
              const open = e.currentTarget.open;
              if (open !== expanded)
                handleGroupSectionToggle(section.key, open);
            }}
          >
            <summary
              class="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide after:hidden ${section.isOther
                ? "text-mitto-text-muted/60 italic"
                : "text-mitto-text-muted"}"
            >
              <span class="truncate min-w-0">${section.name}</span>
            </summary>
            <ul>
              ${section.folders.map(renderFolderLi)}
            </ul>
          </details>
        </li>
      `;
    }
  };

  return html`
    <${Fragment}>
      ${groupContextMenu && html`
        <${ContextMenu}
          x=${groupContextMenu.x}
          y=${groupContextMenu.y}
          items=${[
            ...(groupContextMenu.workingDir
              ? (() => {
                  // List workspaces/agents matching this folder, mirroring the "+" button.
                  const matching = workspaces.filter(
                    (ws) => ws.working_dir === groupContextMenu.workingDir,
                  );
                  if (matching.length === 0) return [];
                  return [{
                    label: "New",
                    icon: html`<${PlusIcon} className="w-4 h-4" />`,
                    submenu: matching.map((ws) => ({
                      label: ws.acp_server || ws.name || getBasename(ws.working_dir),
                      icon: html`<${RobotIcon} className="w-4 h-4" />`,
                      onClick: () => onNewSession && onNewSession(ws, null),
                    })),
                  }];
                })()
              : []),
            ...(groupContextMenu.workingDir ? [{
              label: "Tasks",
              icon: html`<${BeadsIcon} className="w-4 h-4" />`,
              onClick: () => onBeadsOpen && onBeadsOpen(groupContextMenu.workingDir),
            }] : []),
            ...(badgeClickEnabled && groupContextMenu.workingDir ? [{
              label: "Open Folder",
              icon: html`<${FolderOpenIcon} className="w-4 h-4" />`,
              onClick: () => onFolderOpen && onFolderOpen(groupContextMenu.workingDir),
            }] : []),
            ...(terminalActionEnabled && groupContextMenu.workingDir ? [{
              label: "Open Terminal",
              icon: html`<${TerminalIcon} className="w-4 h-4" />`,
              onClick: () => onTerminalClick && onTerminalClick(groupContextMenu.workingDir),
            }] : []),
            ...(onMoveFolderToGroup && groupContextMenu.workingDir
              // Not gated by configReadonly: a folder's group is local
              // organizational metadata in folders.json, not host config like
              // adding servers. The backend permits it for authenticated
              // external clients, so it stays available on external connections.
              ? [(() => {
                  const wd = groupContextMenu.workingDir;
                  const lbl = groupContextMenu.label;
                  const current = getFolderGroup(wd);
                  const submenu = [];
                  allGroups.forEach((g) => {
                    const isCurrent =
                      g.toLowerCase() === current.toLowerCase();
                    submenu.push({
                      label: g,
                      icon: isCurrent
                        ? html`<${CheckIcon} className="w-4 h-4" />`
                        : html`<span class="inline-block w-4 h-4"></span>`,
                      disabled: isCurrent,
                      onClick: () => onMoveFolderToGroup(wd, g),
                    });
                  });
                  if (current) {
                    submenu.push({
                      label: "No group",
                      icon: html`<${CloseIcon} className="w-4 h-4" />`,
                      onClick: () => onMoveFolderToGroup(wd, ""),
                    });
                  }
                  submenu.push({
                    label: "New group\u2026",
                    icon: html`<${PlusIcon} className="w-4 h-4" />`,
                    onClick: () => setNewGroupDialog({ workingDir: wd, label: lbl }),
                  });
                  return {
                    label: "Move to group",
                    icon: html`<${LayersIcon} className="w-4 h-4" />`,
                    submenu,
                  };
                })()]
              : []),
            ...(!configReadonly && groupContextMenu.workingDir ? [{
              label: "Configure Workspace",
              icon: html`<${SettingsIcon} className="w-4 h-4" />`,
              onClick: () => onShowWorkspacesForFolder && onShowWorkspacesForFolder(groupContextMenu.workingDir),
            }] : []),
          ]}
          onClose=${closeGroupContextMenu}
        />
      `}
      ${tasksContextMenu &&
      html`
        <${ContextMenu}
          x=${tasksContextMenu.x}
          y=${tasksContextMenu.y}
          items=${[
            {
              label: "New",
              icon: html`<${PlusIcon} className="w-4 h-4" />`,
              onClick: () =>
                onBeadsCreate && onBeadsCreate(tasksContextMenu.workingDir),
            },
            {
              label: "Tasks",
              icon: html`<${LightningIcon} className="w-4 h-4" />`,
              submenu: tasksMenuPromptsLoading
                ? [
                    {
                      label: "Loading\u2026",
                      disabled: true,
                      onClick: () => {},
                    },
                  ]
                : tasksMenuPrompts.length === 0
                  ? [
                      {
                        label: "No task prompts",
                        disabled: true,
                        onClick: () => {},
                      },
                    ]
                  : tasksMenuPrompts.map((p) => {
                      const PromptIcon = getPromptIconOrDefault(p.icon);
                      return {
                        label: p.name,
                        icon: html`<${PromptIcon} className="w-4 h-4" />`,
                        onClick: () =>
                          onRunBeadsListPrompt &&
                          onRunBeadsListPrompt(p, tasksContextMenu.workingDir),
                      };
                    }),
            },
            {
              label: "Refresh",
              icon: html`<${RefreshIcon} className="w-4 h-4" />`,
              onClick: () =>
                onBeadsRefresh && onBeadsRefresh(tasksContextMenu.workingDir),
            },
            {
              label: "Cleanup closed",
              icon: html`<${BroomIcon} className="w-4 h-4" />`,
              onClick: () =>
                onBeadsCleanup && onBeadsCleanup(tasksContextMenu.workingDir),
            },
          ]}
          onClose=${closeTasksContextMenu}
        />
      `}
      ${newGroupDialog &&
      html`
        <${Modal}
          isOpen=${true}
          onClose=${() => setNewGroupDialog(null)}
          title="New group"
          testid="new-group-dialog"
          backdropTestid="new-group-dialog-backdrop"
          closeTestid="new-group-dialog-close"
          footer=${html`
            <button
              class="btn btn-sm btn-ghost"
              onClick=${() => setNewGroupDialog(null)}
              data-testid="new-group-cancel-btn"
            >
              Cancel
            </button>
            <button
              class="btn btn-sm btn-primary"
              disabled=${!newGroupName.trim()}
              onClick=${submitNewGroup}
              data-testid="new-group-create-btn"
            >
              Create
            </button>
          `}
        >
          <div class="space-y-2">
            <label
              class="block text-sm font-medium text-mitto-text-secondary"
              for="new-group-name-input"
            >
              Group name
            </label>
            <input
              id="new-group-name-input"
              ref=${newGroupInputRef}
              type="text"
              value=${newGroupName}
              onInput=${(e) => setNewGroupName(e.target.value)}
              onKeyDown=${(e) => {
                if (e.key === "Enter" && newGroupName.trim()) {
                  e.preventDefault();
                  submitNewGroup();
                }
              }}
              placeholder="e.g., Personal, Development, Operations"
              class="input input-sm w-full"
              data-testid="new-group-name-input"
            />
            ${newGroupDialog.label &&
            html`<p class="text-xs text-mitto-text-muted">
              "${newGroupDialog.label}" will be moved to this group.
            </p>`}
          </div>
        </${Modal}>
      `}
      <div class="h-full flex flex-col">
      <div
        class="p-4 flex items-center justify-between"
      >
        <h2 class="font-semibold text-lg flex items-center gap-2">
          <${ChatBubbleIcon} className="w-5 h-5 shrink-0" />
          <span>Mitto</span>
        </h2>
        ${onClose &&
        html`
          <button
            onClick=${onClose}
            class="btn btn-ghost btn-square btn-sm md:hidden"
            title="Close"
          >
            <${CloseIcon} className="w-4 h-4" />
          </button>
        `}
      </div>
      <!-- Side panel toolbar: panel-wide actions, sitting right above the
           Dashboard entry. Holds, in order: new-conversation, workspaces,
           category-filter, density, search, and settings. Workspaces and
           settings were moved up from the footer; they are disabled (greyed)
           rather than hidden when the configuration is read-only. -->
      <div
        ref=${toolbarRef}
        class="px-3 pb-8"
        data-testid="sidebar-toolbar"
      >
        <!-- daisyUI join: welds the actions into one group spanning the full
             panel width. Each direct child grows equally (flex-1); dropdown
             triggers carry join-item on the <summary> (join styles apply even
             when join-item is nested). -->
        <div class="join w-full">
          <button
            data-testid="new-conversation-btn"
            onClick=${() => !isCreatingSession && onNewSession(null, null)}
            aria-disabled=${isCreatingSession ? "true" : "false"}
            class="btn btn-ghost btn-sm join-item flex-auto ${isCreatingSession ? "opacity-40 pointer-events-none" : ""}"
            title=${isCreatingSession ? "Creating conversation\u2026" : "New Conversation"}
          >
            ${isCreatingSession
              ? html`<${SpinnerIcon} className="w-4 h-4 animate-spin" />`
              : html`<${PlusIcon} className="w-4 h-4" />`}
          </button>
          <!-- Workspaces: moved up from the footer. Disabled (greyed) instead
               of hidden when the configuration is read-only. -->
          <button
            data-testid="workspaces-btn"
            type="button"
            onClick=${() => !configReadonly && onShowWorkspaces && onShowWorkspaces()}
            aria-disabled=${configReadonly ? "true" : "false"}
            class="btn btn-ghost btn-sm join-item flex-auto ${configReadonly
              ? "opacity-40 pointer-events-none text-mitto-text-muted"
              : "text-mitto-text-muted hover:text-mitto-text-strong"}"
            title=${configReadonly ? "Workspaces (read-only configuration)" : "Workspaces"}
            aria-label="Workspaces"
          >
            <${FolderIcon} className="w-4 h-4" />
          </button>
          <!-- The dropdown trigger is the nested <summary>, so the join's
               weld margin (applied to direct join-item children) never reaches
               it. -ms-px reproduces that weld so the trigger sits flush with
               the adjacent buttons, exactly like the plain <button> items. -->
          <details
            class="dropdown flex-auto -ms-px"
            open=${openToolbarMenu === "filter"}
            onToggle=${(e) => {
              const open = e.currentTarget.open;
              if (open !== (openToolbarMenu === "filter"))
                handleToolbarMenuToggle("filter", open);
            }}
          >
            <summary
              data-testid="category-filter-btn"
              class="btn btn-ghost btn-sm join-item w-full list-none ${anyCategoryHidden
                ? "text-mitto-accent-400"
                : "text-mitto-text-muted"}"
              title="Filter categories"
              aria-label="Filter categories"
            >
              <${FilterIcon} className="w-4 h-4" />
            </summary>
            <ul
              class="dropdown-content menu menu-sm bg-mitto-surface-2 rounded-box z-10 mt-1 w-44 p-2 shadow border border-mitto-border-1"
            >
              <li class="menu-title text-xs">Show categories</li>
              ${[
                { key: "regular", label: "Regular" },
                { key: "periodic", label: "Periodic" },
                { key: "archived", label: "Archived" },
                { key: "tasks", label: "Tasks" },
              ].map(
                (opt) => html`
                  <li key=${opt.key}>
                    <label class="flex items-center gap-2 cursor-pointer">
                      <input
                        type="checkbox"
                        class="checkbox checkbox-sm"
                        checked=${categoryFilter[opt.key]}
                        onInput=${() => handleCategoryToggle(opt.key)}
                        data-testid=${`category-filter-${opt.key}`}
                      />
                      <span class="text-sm">${opt.label}</span>
                    </label>
                  </li>
                `,
              )}
            </ul>
          </details>
          <!-- Density control: opens a menu with "Comfortable" / "Condensed".
               The active mode is checked; the choice persists in localStorage. -->
          <details
            class="dropdown flex-auto -ms-px"
            open=${openToolbarMenu === "density"}
            onToggle=${(e) => {
              const open = e.currentTarget.open;
              if (open !== (openToolbarMenu === "density"))
                handleToolbarMenuToggle("density", open);
            }}
          >
            <summary
              data-testid="density-btn"
              class="btn btn-ghost btn-sm join-item w-full list-none text-mitto-text-muted"
              title="Density"
              aria-label="Density"
            >
              <${SlidersIcon} className="w-4 h-4" />
            </summary>
            <ul
              class="dropdown-content menu menu-sm bg-mitto-surface-2 rounded-box z-10 mt-1 w-44 p-2 shadow border border-mitto-border-1"
            >
              <li class="menu-title text-xs">Density</li>
              <li>
                <button type="button" data-testid="density-comfortable" onClick=${() => handleDensityChange("comfortable")}>
                  ${density === "comfortable" ? html`<${CheckIcon} className="w-4 h-4" />` : html`<span class="inline-block w-4 h-4"></span>`}
                  <span class="text-sm">Comfortable</span>
                </button>
              </li>
              <li>
                <button type="button" data-testid="density-condensed" onClick=${() => handleDensityChange("condensed")}>
                  ${density === "condensed" ? html`<${CheckIcon} className="w-4 h-4" />` : html`<span class="inline-block w-4 h-4"></span>`}
                  <span class="text-sm">Condensed</span>
                </button>
              </li>
            </ul>
          </details>
          <!-- Search (placeholder — search is not yet implemented). -->
          <button
            type="button"
            data-testid="search-btn"
            class="btn btn-ghost btn-sm join-item flex-auto text-mitto-text-muted"
            aria-label="Search"
            title="Search"
          >
            <${SearchIcon} className="w-4 h-4" />
          </button>
          <!-- Settings: moved up from the footer. Disabled (greyed) instead of
               hidden when the configuration is read-only. -->
          <button
            data-testid="settings-btn"
            type="button"
            onClick=${() => !configReadonly && onShowSettings && onShowSettings()}
            aria-disabled=${configReadonly ? "true" : "false"}
            class="btn btn-ghost btn-sm join-item flex-auto ${configReadonly
              ? "opacity-40 pointer-events-none text-mitto-text-muted"
              : "text-mitto-text-muted hover:text-mitto-text-strong"}"
            title=${configReadonly
              ? (rcFilePath ? `Using ${rcFilePath}` : "Settings (read-only configuration)")
              : "Settings"}
            aria-label="Settings"
          >
            <${SettingsIcon} className="w-4 h-4" />
          </button>
        </div>
      </div>
      <div class="flex-1 overflow-y-auto scrollbar-hide">
        ${allSessions.length === 0 &&
        html`
          <div class="p-4 text-mitto-text-muted text-sm text-center">
            ${getEmptyMessage()}
          </div>
        `}
        ${renderUnifiedTree()}
      </div>
      <!-- Footer with theme and font size toggles -->
      <div class="p-4 border-t border-mitto-border-1">
        <div class="flex items-center justify-center gap-3">
          <!-- Theme toggle (daisyUI swap; checked = light = sun shown).
               Controlled Preact checkbox — useTheme owns persistence / follow-system /
               Mermaid sync; we do NOT use daisyUI's data-theme theme-controller. -->
          <label
            class="btn btn-ghost btn-square btn-sm swap swap-rotate text-mitto-text-muted hover:text-mitto-text-strong"
            title="${isLight ? "Switch to dark theme" : "Switch to light theme"}"
            aria-label="Toggle between light and dark theme"
            data-testid="theme-toggle"
          >
            <input
              type="checkbox"
              checked=${isLight}
              onChange=${onToggleTheme}
              aria-label="Toggle between light and dark theme"
            />
            <${SunIcon} className="swap-on w-4 h-4" />
            <${MoonIcon} className="swap-off w-4 h-4" />
          </label>
          <!-- Font size toggle (daisyUI join segmented control) -->
          <div
            class="join"
            role="group"
            aria-label="Toggle between small and large font size"
          >
            <button
              type="button"
              onClick=${() => isLargeFont && onToggleFontSize()}
              class="btn btn-sm join-item ${!isLargeFont
                ? "btn-active"
                : "btn-ghost"}"
              title="Switch to small font"
              aria-pressed=${!isLargeFont}
            >
              <span class="text-xs font-semibold">A</span>
            </button>
            <button
              type="button"
              onClick=${() => !isLargeFont && onToggleFontSize()}
              class="btn btn-sm join-item ${isLargeFont
                ? "btn-active"
                : "btn-ghost"}"
              title="Switch to large font"
              aria-pressed=${isLargeFont}
            >
              <span class="text-base font-semibold">A</span>
            </button>
          </div>
          <!-- Keyboard shortcuts button -->
          <button
            onClick=${onShowKeyboardShortcuts}
            class="btn btn-ghost btn-square btn-sm group"
            title="Keyboard Shortcuts"
          >
            <${KeyboardIcon}
              className="w-4 h-4 text-mitto-text-muted group-hover:text-mitto-text-strong"
            />
          </button>
        </div>
      </div>
    </div>
    </${Fragment}>
  `;
}
