// Mitto Web Interface - Session List Component
const { html, Fragment, useState, useMemo, useCallback, useEffect, useRef } = window.preact;

import { computeGroupedSessions, computeSessionFingerprint } from "../utils/sessionGrouping.js";
import { buildSessionTree } from "../utils/sessionTree.js";
import {
  FILTER_TAB,
  getFilterTab,
  setFilterTab,
  getFilterTabGrouping,
  cycleFilterTabGrouping,
  getExpandedGroups,
  isGroupExpanded,
  setGroupExpanded,
  getSingleExpandedGroupMode,
  onUIPreferencesLoaded,
  tabScopedGroupKey,
} from "../utils/index.js";
import { computeAllSessions, getBasename, getGlobalWorkingDir } from "../lib.js";
import { SessionItem } from "./SessionItem.js";
import { WorkspacePill } from "./WorkspaceBadge.js";
import { ContextMenu } from "./ContextMenu.js";
import {
  ChevronDownIcon,
  ServerIcon,
  FolderIcon,
  FolderOpenIcon,
  LayersIcon,
  ListIcon,
  SpinnerIcon,
  PlusIcon,
  CloseIcon,
  ChatBubbleIcon,
  PeriodicIcon,
  PeriodicFilledIcon,
  ArchiveIcon,
  SunIcon,
  MoonIcon,
  KeyboardIcon,
  SettingsIcon,
  RobotIcon,
  BeadsIcon,
  TerminalIcon,
} from "./Icons.js";

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
  onTerminalClick,
  onBeadsOpen,
  queueLength = 0,
  onFetchConversationPrompts, // Async (session, workingDir) => prompts[] for the context menu
  onSendPromptToConversation,
  isCreatingSession = false, // True while a new-conversation request is in-flight or retrying
}) {
  // Combine active and stored sessions using shared helper function
  const allSessions = useMemo(
    () => computeAllSessions(activeSessions, storedSessions),
    [activeSessions, storedSessions],
  );

  const isLight = theme === "light";
  const isLargeFont = fontSize === "large";

  // Filter tab state - initialized from localStorage
  const [filterTab, setFilterTabState] = useState(() => getFilterTab());

  // Grouping state - initialized from the current filter tab's grouping setting
  const [groupingMode, setGroupingModeState] = useState(() =>
    getFilterTabGrouping(getFilterTab()),
  );
  // Track expanded groups in React state to avoid stale localStorage reads in WKWebView.
  // This mirrors the fix applied for navigableSessions (see expandedGroupsForNav in app.js).
  const [sidebarExpandedGroups, setSidebarExpandedGroups] = useState(() =>
    getExpandedGroups(),
  );

  // Group header context menu state: { x, y, workingDir, label }
  const [groupContextMenu, setGroupContextMenu] = useState(null);
  const closeGroupContextMenu = () => setGroupContextMenu(null);

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
    const unsubscribe = onUIPreferencesLoaded((prefs) => {
      // Re-read grouping mode for the current tab from localStorage (which was just synced from server)
      const currentTab = getFilterTab();
      const newMode = getFilterTabGrouping(currentTab);
      setGroupingModeState(newMode);
      // Sync expanded groups from localStorage (just updated by server sync)
      setSidebarExpandedGroups(getExpandedGroups());
      console.debug(
        "[Mitto] SessionList: UI preferences synced from server, tab:",
        currentTab,
        "mode:",
        newMode,
      );
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
        setSidebarExpandedGroups(getExpandedGroups());
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

  // Listen for programmatic filter tab changes (e.g., when unarchiving a session)
  useEffect(() => {
    const handleFilterTabChanged = (e) => {
      const newTab = e.detail.tab;
      setFilterTabState(newTab);
      // Also update grouping mode for the new tab
      const tabGroupingMode = getFilterTabGrouping(newTab);
      setGroupingModeState(tabGroupingMode);
    };
    window.addEventListener("mitto-filter-tab-changed", handleFilterTabChanged);
    return () => {
      window.removeEventListener(
        "mitto-filter-tab-changed",
        handleFilterTabChanged,
      );
    };
  }, []);

  // Auto-scroll sidebar to show the active session when it changes programmatically
  // (e.g., from notification click, swipe navigation, or keyboard shortcut)
  useEffect(() => {
    if (!activeSessionId) return;

    // Find the session to determine which tab it belongs to
    const session = allSessions.find((s) => s.session_id === activeSessionId);
    let tabSwitched = false;
    if (session) {
      // Determine target tab.
      // Child sessions don't have periodic_enabled — they inherit their parent's
      // category. Follow the parent chain (like getSessionCategory does) to find
      // the root ancestor and use its properties to pick the correct tab.
      let targetTab = FILTER_TAB.CONVERSATIONS;
      if (session.archived) {
        targetTab = FILTER_TAB.ARCHIVED;
      } else {
        // Find the root parent to determine the correct tab category
        let categorySession = session;
        if (session.parent_session_id) {
          const sessionMap = new Map(allSessions.map((s) => [s.session_id, s]));
          let current = session;
          let depth = 0;
          while (current.parent_session_id && depth < 10) {
            const parent = sessionMap.get(current.parent_session_id);
            if (!parent) break;
            // If any ancestor is archived, child belongs to the archived tab
            if (parent.archived) {
              categorySession = parent;
              break;
            }
            current = parent;
            depth++;
          }
          if (!categorySession.archived) {
            categorySession = current; // root parent
          }
        }
        if (categorySession.archived) {
          targetTab = FILTER_TAB.ARCHIVED;
        } else if (categorySession.periodic_enabled) {
          targetTab = FILTER_TAB.PERIODIC;
        }
      }

      // Switch tab if needed
      if (filterTab !== targetTab) {
        handleFilterTabChange(targetTab);
        tabSwitched = true;
      }
    }

    // Scroll the active session into view after DOM updates.
    // Use double-rAF when a tab switch occurred to ensure the new tab content
    // has been rendered before attempting to find and scroll the element.
    const scrollToActive = () => {
      const el = document.querySelector(
        `[data-session-id="${activeSessionId}"]`,
      );
      if (el) {
        el.scrollIntoView({ block: "nearest", behavior: "smooth" });
      }
    };

    if (tabSwitched) {
      // Tab switch triggers a state update → re-render → DOM commit.
      // First rAF waits for commit, second rAF ensures paint completed.
      requestAnimationFrame(() => requestAnimationFrame(scrollToActive));
    } else {
      requestAnimationFrame(scrollToActive);
    }
  }, [activeSessionId]); // Intentionally minimal deps - only trigger on session change

  // Handle filter tab change - also update grouping mode to match the new tab's setting
  const handleFilterTabChange = useCallback((tab) => {
    setFilterTab(tab);
    setFilterTabState(tab);
    // Apply the grouping mode for the new tab
    const tabGroupingMode = getFilterTabGrouping(tab);
    setGroupingModeState(tabGroupingMode);
  }, []);

  // Handle grouping mode toggle - cycles the grouping for the current filter tab
  const handleToggleGrouping = useCallback(() => {
    const newMode = cycleFilterTabGrouping(filterTab);
    setGroupingModeState(newMode);
  }, [filterTab]);

  // Helper to check if a group is expanded using React state (not localStorage)
  // to avoid stale reads in WKWebView (macOS native app).
  const isSidebarGroupExpanded = useCallback(
    (groupKey) => {
      // Tab-scope folder/server/workspace keys so each filter tab tracks its own
      // expansion state. Special keys (parent:*, __archived__) pass through unchanged.
      const key = tabScopedGroupKey(filterTab, groupKey);
      if (key in sidebarExpandedGroups) return sidebarExpandedGroups[key];
      if (key === "__archived__") return false;
      return true;
    },
    [sidebarExpandedGroups, filterTab],
  );

  // Handle group expand/collapse toggle
  const handleToggleGroup = useCallback(
    (rawGroupKey, rawAllGroupKeys = []) => {
      // Tab-scope folder/server/workspace keys so each filter tab remembers its
      // own expanded group independently. Special keys (parent:*, __archived__)
      // are returned unchanged by tabScopedGroupKey.
      const groupKey = tabScopedGroupKey(filterTab, rawGroupKey);
      const allGroupKeys = rawAllGroupKeys.map((k) =>
        tabScopedGroupKey(filterTab, k),
      );
      // Parent-child groups always behave as an accordion regardless of the setting.
      const isParentGroup = rawGroupKey.startsWith("parent:");

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
      // Note: setSidebarExpandedGroups already triggers a re-render, no version bump needed
    },
    [sidebarExpandedGroups, filterTab],
  );


  // Get grouping icon based on current mode
  const getGroupingIcon = () => {
    switch (groupingMode) {
      case "server":
        return html`<${ServerIcon} className="w-4 h-4" />`;
      case "folder":
        return html`<${FolderIcon} className="w-4 h-4" />`;
      case "workspace":
        return html`<${LayersIcon} className="w-4 h-4" />`;
      default:
        return html`<${ListIcon} className="w-4 h-4" />`;
    }
  };

  // Get grouping tooltip based on current mode
  const getGroupingTooltip = () => {
    switch (groupingMode) {
      case "server":
        return "Grouped by ACP server (click to group by folder)";
      case "folder":
        return "Grouped by folder (click to group by workspace)";
      case "workspace":
        return "Grouped by workspace (click to disable grouping)";
      default:
        return "No grouping (click to group by server)";
    }
  };

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

  // Helper to get session's ACP server
  const getSessionServer = (session) => {
    const storedSession = storedSessions.find(
      (s) => s.session_id === session.session_id,
    );
    return session.acp_server || storedSession?.acp_server || "Unknown";
  };

  // Separate sessions by category for tab counts
  const { regularSessions, periodicSessions, archivedSessions } =
    useMemo(() => {
      const regular = [];
      const periodic = [];
      const archived = [];

      // Build a map for O(1) parent lookups
      const sessionMap = new Map(allSessions.map((s) => [s.session_id, s]));

      // Walk up the parent chain to find the root ancestor's category.
      // Depth limit guards against circular references.
      // If a child session is itself archived, always categorize as "archived"
      // regardless of the parent's status — this ensures deleted children
      // don't appear in the active conversations list.
      const getSessionCategory = (session, depth = 0) => {
        // A session that is itself archived is always "archived",
        // even if its parent is still active.
        if (session.archived) return "archived";

        if (depth > 10 || !session.parent_session_id) {
          // Base case: categorize by own flags
          if (session.periodic_enabled) return "periodic";
          return "regular";
        }
        const parent = sessionMap.get(session.parent_session_id);
        if (!parent) {
          // Parent not found — fall back to own flags
          if (session.periodic_enabled) return "periodic";
          return "regular";
        }
        return getSessionCategory(parent, depth + 1);
      };

      allSessions.forEach((session) => {
        const category = getSessionCategory(session);
        if (category === "archived") {
          archived.push(session);
        } else if (category === "periodic") {
          periodic.push(session);
        } else {
          regular.push(session);
        }
      });
      return {
        regularSessions: regular,
        periodicSessions: periodic,
        archivedSessions: archived,
      };
    }, [allSessions]);

  // Get sessions to display based on active filter tab
  const filteredSessions = useMemo(() => {
    switch (filterTab) {
      case FILTER_TAB.PERIODIC:
        return periodicSessions;
      case FILTER_TAB.ARCHIVED:
        return archivedSessions;
      case FILTER_TAB.CONVERSATIONS:
      default:
        return regularSessions;
    }
  }, [filterTab, regularSessions, periodicSessions, archivedSessions]);

  // Build a lookup map of session_id → true for sessions currently streaming.
  // This provides fresh streaming state that can be used instead of stale values
  // from cached groupedSessions (whose fingerprint intentionally excludes isStreaming
  // to avoid expensive tree rebuilds during streaming).
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

  // Check which filter tabs have streaming sessions (for pulsing animation)
  const streamingTabs = useMemo(() => {
    return {
      conversations: regularSessions.some((s) => s.isStreaming),
      periodic: periodicSessions.some((s) => s.isStreaming),
      archived: archivedSessions.some((s) => s.isStreaming),
    };
  }, [regularSessions, periodicSessions, archivedSessions]);

  // Structural fingerprint tracking for groupedSessions optimization
  // Prevents expensive buildSessionTree rebuilds when only non-structural properties change
  // (e.g., isStreaming, message content during tool_update events)
  const prevSessionFingerprint = useRef("");
  const prevGroupedSessions = useRef(null);

  // Group sessions based on current mode (uses filtered sessions)
  // Returns:
  // - null for "none" mode (flat list)
  // - Array of { key, label, sessions, workingDir, acpServer } for "server" and "workspace" modes
  // - Array of { key, label, workingDir, subgroups: [{ key, label, acpServer, sessions }] } for "folder" mode (hierarchical)
  const groupedSessions = useMemo(() => {
    if (groupingMode === "none") return null;

    const fingerprint = computeSessionFingerprint(filteredSessions, groupingMode);
    if (fingerprint === prevSessionFingerprint.current && prevGroupedSessions.current) {
      return prevGroupedSessions.current;
    }
    prevSessionFingerprint.current = fingerprint;
    const result = computeGroupedSessions(filteredSessions, groupingMode, allSessions, workspaces);
    prevGroupedSessions.current = result;
    return result;
  }, [filteredSessions, groupingMode, allSessions, workspaces]);

  // Build a map from session ID → its family's parent group key ("parent:<id>").
  // Covers both the parent session itself and all its children.
  // Used by handleSelectWithCollapse to know which family a clicked session belongs to.
  const sessionFamilyMap = useMemo(() => {
    const map = new Map();
    if (!groupedSessions) return map;
    groupedSessions.forEach((folder) => {
      folder.sessions.forEach((session) => {
        if (session.children && session.children.length > 0) {
          const parentKey = `parent:${session.session_id}`;
          map.set(session.session_id, parentKey);
          session.children.forEach((child) => {
            map.set(child.session_id, parentKey);
          });
        }
      });
    });
    return map;
  }, [groupedSessions]);

  // Wrap onSelect to auto-collapse parent-child groups when selecting outside the family.
  // If the selected session belongs to a family (parent + its children), only that family
  // stays expanded. All other expanded parent groups are collapsed.
  const handleSelectWithCollapse = useCallback(
    (sessionId) => {
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

      // Call the original onSelect
      onSelect(sessionId);
    },
    [onSelect, sessionFamilyMap, sidebarExpandedGroups],
  );

  // Enforce accordion mode when groups change (e.g., tab switch, grouping mode change)
  // If multiple groups are expanded and accordion mode is enabled, collapse all but
  // one. The group kept expanded is the one containing the active conversation (so
  // switching filter tabs never collapses the user's focused group); when the active
  // conversation isn't in this tab, the first expanded group is kept.
  useEffect(() => {
    if (!groupedSessions || !getSingleExpandedGroupMode()) {
      return;
    }

    // Raw keys of groups currently shown as expanded in this tab's view.
    // isSidebarGroupExpanded tab-scopes internally for the read.
    const expandedRawKeys = groupedSessions
      .filter((g) => isSidebarGroupExpanded(g.key))
      .map((g) => g.key);

    if (expandedRawKeys.length > 1) {
      // Prefer keeping the group that contains the active conversation.
      let keepRawKey = null;
      if (activeSessionId) {
        const activeGroup = groupedSessions.find((g) =>
          g.sessions.some(
            (s) =>
              s.session_id === activeSessionId ||
              (s.children &&
                s.children.some((c) => c.session_id === activeSessionId)),
          ),
        );
        if (activeGroup && expandedRawKeys.includes(activeGroup.key)) {
          keepRawKey = activeGroup.key;
        }
      }
      if (!keepRawKey) keepRawKey = expandedRawKeys[0];

      const toCollapseRaw = expandedRawKeys.filter((k) => k !== keepRawKey);
      const toCollapseScoped = toCollapseRaw.map((k) =>
        tabScopedGroupKey(filterTab, k),
      );
      console.debug(
        "[Mitto] Accordion mode: collapsing groups on tab/mode change. Keeping:",
        keepRawKey,
        "Collapsing:",
        toCollapseRaw,
      );
      // Update React state and localStorage for collapsed groups (tab-scoped keys)
      setSidebarExpandedGroups((prev) => {
        const next = { ...prev };
        for (const key of toCollapseScoped) {
          next[key] = false;
        }
        return next;
      });
      for (const key of toCollapseScoped) {
        setGroupExpanded(key, false);
      }
    }
  }, [
    groupedSessions,
    filterTab,
    groupingMode,
    sidebarExpandedGroups,
    activeSessionId,
  ]);

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
        isActive=${activeSessionId === session.session_id}
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
        filterTab=${filterTab}
        groupingMode=${groupingMode}
        onFetchConversationPrompts=${onFetchConversationPrompts}
        onSendPromptToConversation=${onSendPromptToConversation}
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

  // Handle creating a new session in a specific workspace group
  const handleNewSessionInGroup = useCallback(
    (groupKey, e) => {
      // Prevent the click from toggling the group
      e.stopPropagation();

      // Find the workspace that matches this group key
      // For workspace and folder modes, groupKey is "working_dir|acp_server" (composite key)
      // For server mode, groupKey is the acp_server
      let workspace = null;
      if (groupingMode === "workspace" || groupingMode === "folder") {
        // Parse composite key: working_dir|acp_server
        const [workingDir, acpServer] = groupKey.split("|");
        workspace = workspaces.find(
          (ws) => ws.working_dir === workingDir && ws.acp_server === acpServer,
        );
      } else if (groupingMode === "server") {
        // For server mode, find first workspace with matching acp_server
        workspace = workspaces.find((ws) => ws.acp_server === groupKey);
      }

      if (workspace) {
        onNewSession(workspace, null, filterTab);
      } else {
        // Fallback to default new session behavior
        onNewSession(null, null, filterTab);
      }
    },
    [groupingMode, workspaces, onNewSession, filterTab],
  );

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
        onNewSession(matchingWorkspaces[0], null, filterTab);
      } else if (matchingWorkspaces.length > 1) {
        // Multiple workspaces - show dialog filtered to this folder
        onNewSession(null, workingDir, filterTab);
      } else {
        // Fallback
        onNewSession(null, null, filterTab);
      }
    },
    [workspaces, onNewSession, filterTab],
  );

  // Render grouped sessions with collapsible headers
  // Handles both flat grouping (server, workspace) and hierarchical grouping (folder)
  const renderGroupedSessions = () => {
    if (!groupedSessions) return null;

    // For hierarchical mode (folder), render two-level tree
    if (groupingMode === "folder") {
      return renderHierarchicalGroups();
    }

    // Get all group keys for accordion mode (flat grouping)
    const allGroupKeys = groupedSessions.map((g) => g.key);

    return html`
      ${groupedSessions.map((group) => {
        const expanded = isSidebarGroupExpanded(group.key);
        // Count total sessions including children
        const sessionCount = group.sessions.reduce(
          (sum, s) => sum + 1 + (s.children ? s.children.length : 0),
          0,
        );
        // Check if any session (or its children) in this group is actively streaming
        // Use streamingMap for fresh state (groupedSessions may cache stale isStreaming)
        const hasStreamingSession = group.sessions.some(
          (s) =>
            streamingMap.has(s.session_id) ||
            (s.children &&
              s.children.some((c) => streamingMap.has(c.session_id))),
        );
        // Get workspace info for badge display (workspace mode only)
        const workspace =
          groupingMode === "workspace" && group.workingDir
            ? workspaces.find(
                (ws) =>
                  ws.working_dir === group.workingDir &&
                  (!group.acpServer || ws.acp_server === group.acpServer),
              )
            : null;

        return html`
          <div key=${group.key} class="group-section">
            <div
              class="w-full px-4 py-2 flex items-center gap-2 text-sm font-medium text-gray-400 hover:text-white hover:bg-slate-700/50 transition-colors sticky top-0 bg-slate-800 z-10 cursor-pointer select-none group/header"
              onClick=${() => handleToggleGroup(group.key, allGroupKeys)}
              onContextMenu=${(e) => {
                if (group.workingDir) {
                  e.preventDefault();
                  e.stopPropagation();
                  setGroupContextMenu({ x: e.clientX, y: e.clientY, workingDir: group.workingDir, label: group.label });
                }
              }}
              data-has-context-menu=${group.workingDir ? "true" : undefined}
            >
              <span
                class="transition-transform ${expanded ? "" : "-rotate-90"}"
              >
                <${ChevronDownIcon} className="w-4 h-4" />
              </span>
              ${groupingMode === "server"
                ? html`<${ServerIcon} className="w-4 h-4 flex-shrink-0" />`
                : html`<${LayersIcon} className="w-4 h-4 flex-shrink-0" />`}
              <span class="text-left truncate">${group.label}</span>
              ${groupingMode === "workspace" &&
              group.workingDir &&
              html`
                <${WorkspacePill}
                  path=${group.workingDir}
                  customColor=${workspace?.color}
                  customCode=${workspace?.code}
                  customName=${workspace?.name}
                  acpServer=${group.acpServer}
                  className="flex-shrink-0"
                  hideAbbreviation=${true}
                />
              `}
              <span class="flex-1"></span>
              ${!expanded &&
              hasStreamingSession &&
              html`
                <span
                  class="w-2 h-2 bg-blue-400 rounded-full flex-shrink-0 streaming-indicator"
                  title="Agent responding in this group"
                ></span>
              `}
              ${groupingMode === "workspace" &&
              group.workingDir &&
              html`
                <button
                  onClick=${(e) => { e.stopPropagation(); onBeadsOpen && onBeadsOpen(group.workingDir); }}
                  class="p-0.5 rounded hover:bg-slate-600 transition-colors text-gray-500 hover:text-white"
                  title="Beads issues: ${group.workingDir}"
                >
                  <${BeadsIcon} className="w-3.5 h-3.5" />
                </button>
              `}
              ${groupingMode === "workspace" &&
              (filterTab === FILTER_TAB.CONVERSATIONS || filterTab === FILTER_TAB.PERIODIC) &&
              html`
                <button
                  onClick=${(e) => !isCreatingSession && handleNewSessionInGroup(group.key, e)}
                  class="p-0.5 rounded transition-colors ${isCreatingSession ? "cursor-wait opacity-60 text-gray-500" : "hover:bg-slate-600 text-gray-500 hover:text-white"}"
                  title=${isCreatingSession ? "Creating conversation\u2026" : `New conversation in ${group.label}`}
                  disabled=${isCreatingSession}
                >
                  ${isCreatingSession
                    ? html`<${SpinnerIcon} className="w-3.5 h-3.5 animate-spin" />`
                    : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                </button>
              `}
              <span class="text-xs text-gray-500">${sessionCount}</span>
            </div>
            ${expanded &&
            (() => {
              // Collect all parent group keys for accordion mode
              const parentGroupKeys = group.sessions
                .filter((s) => s.children && s.children.length > 0)
                .map((s) => `parent:${s.session_id}`);

              return group.sessions.map((session) => {
                const hasChildSessions =
                  session.children && session.children.length > 0;
                const parentKey = `parent:${session.session_id}`;
                const childrenExpanded = hasChildSessions
                  ? isSidebarGroupExpanded(parentKey)
                  : false;
                const hasChildStreaming =
                  hasChildSessions &&
                  session.children.some((c) =>
                    streamingMap.has(c.session_id),
                  );

                return html`
                  <div
                    key=${session.session_id}
                    class="parent-session-group border-b border-slate-700 ${hasChildSessions ? "has-children" : ""}"
                  >
                    ${renderSessionItem(
                      {
                        ...session,
                        isStreaming: streamingMap.has(session.session_id),
                        isWaitingForChildren: waitingMap.has(session.session_id),
                        isWaitingForUserInput: uiPromptMap.has(session.session_id),
                      },
                      {
                        hideBadge: groupingMode === "workspace",
                        badgeHideAcpServer: groupingMode === "server",
                        childCount: hasChildSessions
                          ? session.children.length
                          : 0,
                        hasChildStreaming:
                          hasChildSessions &&
                          !childrenExpanded &&
                          hasChildStreaming,
                        hasChildren: hasChildSessions,
                        isExpanded: childrenExpanded,
                        onToggleExpand: hasChildSessions
                          ? () =>
                              handleToggleGroup(parentKey, parentGroupKeys)
                          : null,
                      },
                    )}
                    ${hasChildSessions &&
                    html`
                      <div
                        class="session-children ${childrenExpanded ? "session-children--expanded" : ""}"
                      >
                        ${session.children.map(
                          (child) =>
                            html`<div class="session-item--child">
                              ${renderSessionItem(
                                {
                                  ...child,
                                  isStreaming: streamingMap.has(
                                    child.session_id,
                                  ),
                                  isWaitingForChildren: waitingMap.has(child.session_id),
                                  isWaitingForUserInput: uiPromptMap.has(child.session_id),
                                },
                                {
                                  hideBadge: groupingMode === "workspace",
                                  badgeHideAcpServer:
                                    groupingMode === "server",
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
            })()}
          </div>
        `;
      })}
    `;
  };

  // Render hierarchical groups for "folder" mode (parent-child tree: folder -> sessions with children)
  const renderHierarchicalGroups = () => {
    if (!groupedSessions) return null;

    // Collect group keys for accordion mode
    // Folder keys and parent session keys are kept separate so that
    // toggling a session's children doesn't collapse the folder.
    const allGroupKeys = []; // folder-level keys only
    const parentGroupKeys = []; // session-level parent keys only
    groupedSessions.forEach((folder) => {
      allGroupKeys.push(folder.key);
      folder.sessions.forEach((session) => {
        if (session.children && session.children.length > 0) {
          parentGroupKeys.push(`parent:${session.session_id}`);
        }
      });
    });

    // Helper to count total sessions including children
    const countTotalSessions = (sessions) => {
      return sessions.reduce((sum, s) => {
        return sum + 1 + (s.children ? s.children.length : 0);
      }, 0);
    };

    // Helper to check if any session (or its children) is streaming
    // Uses streamingMap for fresh state (groupedSessions may cache stale isStreaming)
    const hasStreaming = (sessions) => {
      return sessions.some(
        (s) =>
          streamingMap.has(s.session_id) ||
          (s.children &&
            s.children.some((c) => streamingMap.has(c.session_id))),
      );
    };

    return html`
      ${groupedSessions.map((folder) => {
        const folderExpanded = isSidebarGroupExpanded(folder.key);
        const totalSessions = countTotalSessions(folder.sessions);
        const hasFolderStreaming = hasStreaming(folder.sessions);

        return html`
          <div key=${folder.key} class="folder-group">
            <!-- Level 1: Folder header -->
            <div
              class="w-full px-4 py-2 flex items-center gap-2 text-sm font-medium text-gray-400 hover:text-white hover:bg-slate-700/50 transition-colors sticky top-0 bg-slate-800 z-10 cursor-pointer select-none"
              onClick=${() => handleToggleGroup(folder.key, allGroupKeys)}
              onContextMenu=${(e) => {
                if (folder.workingDir) {
                  e.preventDefault();
                  e.stopPropagation();
                  setGroupContextMenu({ x: e.clientX, y: e.clientY, workingDir: folder.workingDir, label: folder.label });
                }
              }}
              data-has-context-menu=${folder.workingDir ? "true" : undefined}
            >
              <span
                class="transition-transform ${folderExpanded
                  ? ""
                  : "-rotate-90"}"
              >
                <${ChevronDownIcon} className="w-4 h-4" />
              </span>
              <${FolderIcon} className="w-4 h-4 flex-shrink-0" />
              <span class="text-left truncate" title=${folder.workingDir}>
                ${folder.label}
              </span>
              <span class="flex-1"></span>
              ${!folderExpanded &&
              hasFolderStreaming &&
              html`
                <span
                  class="w-2 h-2 bg-blue-400 rounded-full flex-shrink-0 streaming-indicator"
                  title="Agent responding in this folder"
                ></span>
              `}
              ${folder.workingDir &&
              html`
                <button
                  onClick=${(e) => { e.stopPropagation(); onBeadsOpen && onBeadsOpen(folder.workingDir); }}
                  class="p-0.5 rounded hover:bg-slate-600 transition-colors text-gray-500 hover:text-white"
                  title="Beads issues: ${folder.workingDir}"
                >
                  <${BeadsIcon} className="w-3.5 h-3.5" />
                </button>
              `}
              ${(filterTab === FILTER_TAB.CONVERSATIONS || filterTab === FILTER_TAB.PERIODIC) &&
              html`
                <button
                  onClick=${(e) => !isCreatingSession && handleNewSessionInFolder(folder.workingDir, e)}
                  class="p-0.5 rounded transition-colors ${isCreatingSession ? "cursor-wait opacity-60 text-gray-500" : "hover:bg-slate-600 text-gray-500 hover:text-white"}"
                  title=${isCreatingSession ? "Creating conversation\u2026" : `New conversation in ${folder.label}`}
                  disabled=${isCreatingSession}
                >
                  ${isCreatingSession
                    ? html`<${SpinnerIcon} className="w-3.5 h-3.5 animate-spin" />`
                    : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                </button>
              `}
              <span class="text-xs text-gray-500">${totalSessions}</span>
            </div>

            <!-- Level 2: Sessions within folder (only when folder is expanded) -->
            ${folderExpanded &&
            folder.sessions.map((session) => {
              const hasChildren =
                session.children && session.children.length > 0;
              const parentKey = `parent:${session.session_id}`;
              const childrenExpanded = hasChildren
                ? isSidebarGroupExpanded(parentKey)
                : false;
              // Use streamingMap for fresh state (groupedSessions may cache stale isStreaming)
              const hasChildStreaming =
                hasChildren &&
                session.children.some((c) => streamingMap.has(c.session_id));

              return html`
                <div
                  key=${session.session_id}
                  class="parent-session-group border-b border-slate-700 ${hasChildren
                    ? "has-children"
                    : ""}"
                >
                  <!-- Parent/regular session - render with expand/collapse integrated into SessionItem -->
                  ${renderSessionItem(
                    {
                      ...session,
                      isStreaming: streamingMap.has(session.session_id),
                      isWaitingForChildren: waitingMap.has(session.session_id),
                      isWaitingForUserInput: uiPromptMap.has(session.session_id),
                    },
                    {
                      hideBadge: false, // Show badge to display ACP server
                      badgeHideAbbreviation: true, // Hide workspace abbreviation (already in folder header)
                      badgeHideAcpServer: false, // Show ACP server badge
                      isSpawned: !hasChildren && !!session._parentId, // Mark as spawned if it's an orphan (no children)
                      childCount: hasChildren ? session.children.length : 0,
                      hasChildStreaming:
                        hasChildren && !childrenExpanded && hasChildStreaming,
                      // Pass expand/collapse props for parent sessions with children
                      hasChildren: hasChildren,
                      isExpanded: childrenExpanded,
                      onToggleExpand: hasChildren
                        ? () => handleToggleGroup(parentKey, parentGroupKeys)
                        : null,
                    },
                  )}

                  <!-- Level 3: Child sessions (animated container) -->
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
                                isWaitingForChildren: waitingMap.has(child.session_id),
                                isWaitingForUserInput: uiPromptMap.has(child.session_id),
                              },
                              {
                                hideBadge: false, // Show badge to display ACP server
                                badgeHideAbbreviation: true, // Hide workspace abbreviation (already in folder header)
                                badgeHideAcpServer: false, // Show ACP server badge
                                isSpawned: true, // Mark as spawned/child
                                extraLeftPadding: "pl-8", // Indent child sessions
                              },
                            )}
                          </div>`,
                      )}
                    </div>
                  `}
                </div>
              `;
            })}
          </div>
        `;
      })}
    `;
  };

  // Render sessions in "none" grouping mode - tree-aware (parent-child nesting)
  const renderUngroupedSessions = () => {
    // Build parent-child tree
    const allKnownSessionIds = new Set(allSessions.map((s) => s.session_id));
    const { rootSessions, childrenMap, orphans } = buildSessionTree(
      filteredSessions,
      allKnownSessionIds,
    );

    // Attach children to parents
    const parents = rootSessions.map((parent) => ({
      ...parent,
      children: childrenMap.get(parent.session_id) || [],
    }));

    // Sort children within each parent (most recent first)
    parents.forEach((parent) => {
      parent.children.sort(
        (a, b) => new Date(b.created_at || 0) - new Date(a.created_at || 0),
      );
    });

    // Combine parents and orphans
    const sessionsToRender = [...parents, ...orphans];

    // Collect all parent group keys for accordion mode
    const parentGroupKeys = sessionsToRender
      .filter((s) => s.children && s.children.length > 0)
      .map((s) => `parent:${s.session_id}`);

    return sessionsToRender.map((session) => {
      const hasChildSessions = session.children && session.children.length > 0;
      const parentKey = `parent:${session.session_id}`;
      const childrenExpanded = hasChildSessions
        ? isSidebarGroupExpanded(parentKey)
        : false;
      const hasChildStreaming =
        hasChildSessions &&
        session.children.some((c) => streamingMap.has(c.session_id));

      return html`
        <div
          key=${session.session_id}
          class="parent-session-group border-b border-slate-700 ${hasChildSessions ? "has-children" : ""}"
        >
          ${renderSessionItem(
            {
              ...session,
              isStreaming: streamingMap.has(session.session_id),
              isWaitingForChildren: waitingMap.has(session.session_id),
              isWaitingForUserInput: uiPromptMap.has(session.session_id),
            },
            {
              childCount: hasChildSessions ? session.children.length : 0,
              hasChildStreaming:
                hasChildSessions && !childrenExpanded && hasChildStreaming,
              hasChildren: hasChildSessions,
              isExpanded: childrenExpanded,
              onToggleExpand: hasChildSessions
                ? () => handleToggleGroup(parentKey, parentGroupKeys)
                : null,
            },
          )}
          ${hasChildSessions &&
          html`
            <div
              class="session-children ${childrenExpanded ? "session-children--expanded" : ""}"
            >
              ${session.children.map(
                (child) =>
                  html`<div class="session-item--child">
                    ${renderSessionItem(
                      {
                        ...child,
                        isStreaming: streamingMap.has(child.session_id),
                        isWaitingForChildren: waitingMap.has(child.session_id),
                        isWaitingForUserInput: uiPromptMap.has(child.session_id),
                      },
                      {
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
  };

  // Get empty state message based on active filter tab
  const getEmptyMessage = () => {
    switch (filterTab) {
      case FILTER_TAB.PERIODIC:
        return "No periodic conversations";
      case FILTER_TAB.ARCHIVED:
        return "No archived conversations";
      default:
        return "No conversations yet";
    }
  };

  return html`
    <${Fragment}>
      ${groupContextMenu && html`
        <${ContextMenu}
          x=${groupContextMenu.x}
          y=${groupContextMenu.y}
          items=${[
            ...((filterTab === FILTER_TAB.CONVERSATIONS || filterTab === FILTER_TAB.PERIODIC) && groupContextMenu.workingDir
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
                      onClick: () => onNewSession && onNewSession(ws, null, filterTab),
                    })),
                  }];
                })()
              : []),
            ...(groupContextMenu.workingDir ? [{
              label: "Beads",
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
            ...(!configReadonly && groupContextMenu.workingDir ? [{
              label: "Configure Workspace",
              icon: html`<${SettingsIcon} className="w-4 h-4" />`,
              onClick: () => onShowWorkspacesForFolder && onShowWorkspacesForFolder(groupContextMenu.workingDir),
            }] : []),
          ]}
          onClose=${closeGroupContextMenu}
        />
      `}
      <div class="h-full flex flex-col">
      <div
        class="p-4 border-b border-slate-700 flex items-center justify-between"
      >
        <h2 class="font-semibold text-lg">Conversations</h2>
        <div class="flex items-center gap-0.5">
          <button
            onClick=${handleToggleGrouping}
            class="p-1.5 hover:bg-slate-700 rounded transition-colors"
            title=${getGroupingTooltip()}
          >
            ${getGroupingIcon()}
          </button>
          <button
            data-testid="new-conversation-btn"
            onClick=${() => !isCreatingSession && onNewSession(null, null, filterTab)}
            class="p-1.5 rounded transition-colors ${isCreatingSession ? "cursor-wait opacity-60" : "hover:bg-slate-700"}"
            title=${isCreatingSession ? "Creating conversation\u2026" : "New Conversation"}
            disabled=${isCreatingSession}
          >
            ${isCreatingSession
              ? html`<${SpinnerIcon} className="w-4 h-4 animate-spin" />`
              : html`<${PlusIcon} className="w-4 h-4" />`}
          </button>
          ${onClose &&
          html`
            <button
              onClick=${onClose}
              class="p-1.5 hover:bg-slate-700 rounded transition-colors md:hidden"
              title="Close"
            >
              <${CloseIcon} className="w-4 h-4" />
            </button>
          `}
        </div>
      </div>
      <!-- Filter Tab Bar -->
      <div
        class="filter-tab-bar flex border-b border-slate-700"
        role="tablist"
        aria-label="Conversation filters"
      >
        <button
          role="tab"
          aria-selected=${filterTab === FILTER_TAB.CONVERSATIONS}
          class="filter-tab flex-1 py-2 flex items-center justify-center transition-colors ${filterTab ===
          FILTER_TAB.CONVERSATIONS
            ? "filter-tab--active text-blue-400 border-b-2 border-blue-400"
            : "text-gray-400 hover:text-gray-200 hover:bg-slate-700/50"} ${streamingTabs.conversations
            ? "filter-tab-streaming"
            : ""}"
          onClick=${() => handleFilterTabChange(FILTER_TAB.CONVERSATIONS)}
          title="Conversations"
        >
          <${ChatBubbleIcon} className="w-5 h-5" />
          ${regularSessions.filter(s => !s.parent_session_id).length > 0 &&
          html`<span class="ml-1.5 text-xs">${regularSessions.filter(s => !s.parent_session_id).length}</span>`}
        </button>
        <button
          role="tab"
          aria-selected=${filterTab === FILTER_TAB.PERIODIC}
          class="filter-tab flex-1 py-2 flex items-center justify-center transition-colors ${filterTab ===
          FILTER_TAB.PERIODIC
            ? "filter-tab--active text-blue-400 border-b-2 border-blue-400"
            : "text-gray-400 hover:text-gray-200 hover:bg-slate-700/50"} ${streamingTabs.periodic
            ? "filter-tab-streaming"
            : ""}"
          onClick=${() => handleFilterTabChange(FILTER_TAB.PERIODIC)}
          title="Periodic"
        >
          <${PeriodicIcon} className="w-5 h-5" />
          ${periodicSessions.filter(s => !s.parent_session_id).length > 0 &&
          html`<span class="ml-1.5 text-xs">${periodicSessions.filter(s => !s.parent_session_id).length}</span>`}
        </button>
        <button
          role="tab"
          aria-selected=${filterTab === FILTER_TAB.ARCHIVED}
          class="filter-tab flex-1 py-2 flex items-center justify-center transition-colors ${filterTab ===
          FILTER_TAB.ARCHIVED
            ? "filter-tab--active text-blue-400 border-b-2 border-blue-400"
            : "text-gray-400 hover:text-gray-200 hover:bg-slate-700/50"} ${streamingTabs.archived
            ? "filter-tab-streaming"
            : ""}"
          onClick=${() => handleFilterTabChange(FILTER_TAB.ARCHIVED)}
          title="Archived"
        >
          <${ArchiveIcon} className="w-5 h-5" />
          ${archivedSessions.filter(s => !s.parent_session_id).length > 0 &&
          html`<span class="ml-1.5 text-xs">${archivedSessions.filter(s => !s.parent_session_id).length}</span>`}
        </button>
      </div>
      <div class="flex-1 overflow-y-auto scrollbar-hide">
        ${filteredSessions.length === 0 &&
        html`
          <div class="p-4 text-gray-500 text-sm text-center">
            ${getEmptyMessage()}
          </div>
        `}
        ${groupingMode === "none"
          ? renderUngroupedSessions()
          : renderGroupedSessions()}
      </div>
      <!-- Footer with settings, theme and font size toggles -->
      <div class="p-4 border-t border-slate-700">
        <div class="flex items-center justify-center gap-3">
          <!-- Settings | Workspaces segmented button (disabled with tooltip when using RC file, hidden when fully read-only without RC file) -->
          ${!configReadonly
            ? html`
                <div class="flex items-center gap-0.5">
                  <button
                    onClick=${onShowSettings}
                    class="p-1.5 hover:bg-slate-700 rounded transition-colors text-gray-400 hover:text-white"
                    title="Settings"
                  >
                    <${SettingsIcon} className="w-4 h-4" />
                  </button>
                  <button
                    onClick=${onShowWorkspaces}
                    class="p-1.5 hover:bg-slate-700 rounded transition-colors text-gray-400 hover:text-white"
                    title="Workspaces"
                  >
                    <${FolderIcon} className="w-4 h-4" />
                  </button>
                </div>
              `
            : rcFilePath
              ? html`
                  <button
                    disabled
                    class="p-2 rounded-lg opacity-50 cursor-not-allowed"
                    title="Using ${rcFilePath}"
                  >
                    <${SettingsIcon} className="w-5 h-5 text-gray-400" />
                  </button>
                `
              : null}
          <!-- Theme toggle -->
          <div
            class="theme-toggle-v2"
            onClick=${onToggleTheme}
            role="button"
            tabindex="0"
            title="${isLight
              ? "Switch to dark theme"
              : "Switch to light theme"}"
            aria-label="Toggle between light and dark theme"
          >
            <!-- Sun icon -->
            <div class="theme-toggle-v2__option ${isLight ? "active" : ""}">
              <${SunIcon} />
            </div>
            <!-- Moon icon -->
            <div class="theme-toggle-v2__option ${!isLight ? "active" : ""}">
              <${MoonIcon} />
            </div>
          </div>
          <!-- Font size toggle -->
          <div
            class="font-size-toggle"
            onClick=${onToggleFontSize}
            role="button"
            tabindex="0"
            title="${isLargeFont
              ? "Switch to small font"
              : "Switch to large font"}"
            aria-label="Toggle between small and large font size"
          >
            <span
              class="font-size-toggle__option ${!isLargeFont ? "active" : ""}"
              >A</span
            >
            <span
              class="font-size-toggle__option font-size-toggle__option--large ${isLargeFont
                ? "active"
                : ""}"
              >A</span
            >
          </div>
          <!-- Keyboard shortcuts button -->
          <button
            onClick=${onShowKeyboardShortcuts}
            class="p-2 hover:bg-slate-700 rounded-lg transition-colors group"
            title="Keyboard Shortcuts"
          >
            <${KeyboardIcon}
              className="w-4 h-4 text-gray-400 group-hover:text-white"
            />
          </button>
        </div>
      </div>
    </div>
    </${Fragment}>
  `;
}
