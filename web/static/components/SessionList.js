// Mitto Web Interface - Session List Component
const { html, Fragment, useState, useMemo, useCallback, useEffect, useRef } = window.preact;

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
} from "./Icons.js";

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
  onShowDashboard,
  mainView = "conversation", // Current main-content view: "conversation" | "beads" | "dashboard"
  beadsWorkingDir = null, // Working dir whose Tasks (beads) view is open, when mainView === "beads"
  queueLength = 0,
  onFetchConversationPrompts, // Async (session, workingDir) => prompts[] for the context menu
  onSendPromptToConversation,
  onMakePeriodic, // Called with (session) to convert a regular session to periodic
  onMakeNonPeriodic, // Called with (session) to revert a periodic session to regular
  isCreatingSession = false, // True while a new-conversation request is in-flight or retrying
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

  // "New group…" dialog state: { workingDir, label } when open, else null.
  const [newGroupDialog, setNewGroupDialog] = useState(null);
  const [newGroupName, setNewGroupName] = useState("");
  const newGroupInputRef = useRef(null);

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
    // Find the workspace matching both working_dir AND acp_server
    // This is important when multiple workspaces share the same folder but use different ACP servers
    const workspace = workspaces.find(
      (ws) =>
        ws.working_dir === workingDir &&
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
        // Children auto-expand when the parent (or one of its children) is the
        // focused conversation — there is no manual expand/collapse toggle.
        const childrenExpanded = hasChildren
          ? activeSessionId === session.session_id ||
            session.children.some((c) => c.session_id === activeSessionId)
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
          const totalSessions =
            countNodes(folder.conversations) + countNodes(folder.archived);
          const hasFolderStreaming =
            hasStreaming(folder.conversations) ||
            hasStreaming(folder.archived);
          // The Tasks (beads) entry carries the focus highlight while its
          // folder's beads view is the active main-content view.
          const tasksActive =
            mainView === "beads" && beadsWorkingDir === folder.workingDir;
          return html`
            <li key=${folder.key} class="folder-group min-w-0">
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
                  class="flex items-center gap-2 text-sm font-medium text-mitto-text-muted after:hidden"
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
                  <${FolderIcon} className="w-4 h-4 shrink-0" />
                  <span class="truncate min-w-0" title=${folder.workingDir}>
                    ${folder.label}
                  </span>
                  <span class="flex-1"></span>
                  ${!folderExpanded &&
                  hasFolderStreaming &&
                  html`
                    <span
                      class="w-2 h-2 bg-mitto-accent-400 rounded-full shrink-0 streaming-indicator"
                      title="Agent responding in this folder"
                    ></span>
                  `}
                  <span
                    class="badge badge-sm badge-ghost shrink-0 tabular-nums"
                    >${totalSessions}</span
                  >
                  <button
                    type="button"
                    onClick=${(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      if (!isCreatingSession)
                        handleNewSessionInFolder(folder.workingDir, e);
                    }}
                    class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong ${isCreatingSession
                      ? "cursor-wait opacity-60"
                      : ""}"
                    title=${isCreatingSession
                      ? "Creating conversation\u2026"
                      : `New conversation in ${folder.label}`}
                    disabled=${isCreatingSession}
                  >
                    ${isCreatingSession
                      ? html`<${SpinnerIcon} className="w-3.5 h-3.5 animate-spin" />`
                      : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                  </button>
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
                </summary>
                <ul>
                  ${folder.showTasks &&
                  html`
                    <!-- Tasks (static, per-folder) — always the first entry in a
                         project. Opens the Beads view for this folder. Not a
                         conversation; excluded from nav. -->
                    <li>
                      <button
                        type="button"
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
                        class="gap-2 text-sm border-0! ${tasksActive
                          ? "bg-mitto-accent text-mitto-accent-fg"
                          : "text-mitto-text-muted"}"
                        title="Beads issues: ${folder.workingDir}"
                      >
                        <${BeadsIcon} className="w-4 h-4 shrink-0" />
                        <span class="truncate">${folder.tasksNode.label}</span>
                      </button>
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
            ...(!configReadonly && onMoveFolderToGroup && groupContextMenu.workingDir
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
        <div class="flex items-center gap-0.5">
          <button
            data-testid="new-conversation-btn"
            onClick=${() => !isCreatingSession && onNewSession(null, null)}
            aria-disabled=${isCreatingSession ? "true" : "false"}
            class="btn btn-ghost btn-square btn-sm ${isCreatingSession ? "opacity-40 pointer-events-none" : ""}"
            title=${isCreatingSession ? "Creating conversation\u2026" : "New Conversation"}
          >
            ${isCreatingSession
              ? html`<${SpinnerIcon} className="w-4 h-4 animate-spin" />`
              : html`<${PlusIcon} className="w-4 h-4" />`}
          </button>
          <details class="dropdown dropdown-end">
            <summary
              data-testid="category-filter-btn"
              class="btn btn-ghost btn-square btn-sm list-none ${anyCategoryHidden
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
      <!-- Footer with settings, theme and font size toggles -->
      <div class="p-4 border-t border-mitto-border-1">
        <div class="flex items-center justify-center gap-3">
          <!-- Settings | Workspaces segmented button (disabled with tooltip when using RC file, hidden when fully read-only without RC file) -->
          ${!configReadonly
            ? html`
                <div class="flex items-center gap-0.5">
                  <button
                    onClick=${onShowSettings}
                    class="btn btn-ghost btn-square btn-sm text-mitto-text-muted hover:text-mitto-text-strong"
                    title="Settings"
                  >
                    <${SettingsIcon} className="w-4 h-4" />
                  </button>
                  <button
                    onClick=${onShowWorkspaces}
                    class="btn btn-ghost btn-square btn-sm text-mitto-text-muted hover:text-mitto-text-strong"
                    title="Workspaces"
                  >
                    <${FolderIcon} className="w-4 h-4" />
                  </button>
                </div>
              `
            : rcFilePath
              ? html`
                  <button
                    aria-disabled="true"
                    class="btn btn-ghost btn-square btn-sm opacity-40 pointer-events-none"
                    title="Using ${rcFilePath}"
                  >
                    <${SettingsIcon} className="w-5 h-5 text-mitto-text-muted" />
                  </button>
                `
              : null}
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
