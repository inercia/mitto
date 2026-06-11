// Mitto Web Interface - Session List Component
const { html, Fragment, useState, useMemo, useCallback, useEffect, useRef } = window.preact;

import { computeUnifiedTree } from "../utils/sessionGrouping.js";
import {
  getFilterTabForSession,
  getExpandedGroups,
  isGroupExpanded,
  setGroupExpanded,
  getSingleExpandedGroupMode,
  onUIPreferencesLoaded,
} from "../utils/index.js";
import { computeAllSessions, getBasename, getGlobalWorkingDir } from "../lib.js";
import { SessionItem } from "./SessionItem.js";
import { ContextMenu } from "./ContextMenu.js";
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

  // Unified sidebar (mitto-1er.4): folder is the only grouping mode; the filter
  // tabs and per-tab grouping were removed. SessionItem still takes a grouping hint.
  const groupingMode = "folder";
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
    const unsubscribe = onUIPreferencesLoaded(() => {
      setSidebarExpandedGroups(getExpandedGroups());
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
      setGroupExpanded(groupKey, willOpen);
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
        filterTab=${session.category || getFilterTabForSession(session)}
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
    const { dashboard, folders } = unifiedTree;
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
        const childrenExpanded = hasChildren
          ? isSidebarGroupExpanded(parentKey)
          : false;
        const hasChildStreaming =
          hasChildren &&
          session.children.some((c) => streamingMap.has(c.session_id));
        return html`
          <div
            key=${session.session_id}
            class="parent-session-group border-b border-mitto-border-1 ${hasChildren
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
                hideBadge: false,
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
                          hideBadge: false,
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
        <!-- Dashboard (static, top-level) — placeholder; wired in mitto-1er.7 -->
        <li>
          <button type="button" class="gap-2">
            <${FolderIcon} className="w-4 h-4 shrink-0" />
            <span class="truncate">${dashboard.label}</span>
          </button>
        </li>
        ${folders.map((folder) => {
          const folderExpanded = isUnifiedFolderExpanded(folder.key);
          const archivedExpanded = isUnifiedArchivedExpanded(folder.key);
          const totalSessions =
            countNodes(folder.conversations) + countNodes(folder.archived);
          const hasFolderStreaming =
            hasStreaming(folder.conversations) ||
            hasStreaming(folder.archived);
          return html`
            <li key=${folder.key} class="folder-group">
              <details
                open=${folderExpanded}
                onToggle=${(e) => {
                  const open = e.currentTarget.open;
                  if (open !== folderExpanded) {
                    handleUnifiedToggle(folder.key, open, allFolderKeys);
                  }
                }}
              >
                <summary
                  class="gap-2 text-sm font-medium text-mitto-text-muted"
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
                  <span class="truncate" title=${folder.workingDir}>
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
                  ${folder.workingDir &&
                  html`
                    <button
                      type="button"
                      onClick=${(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        onBeadsOpen && onBeadsOpen(folder.workingDir);
                      }}
                      class="p-0.5 rounded hover:bg-mitto-surface-hover transition-colors text-mitto-text-muted hover:text-mitto-text-strong"
                      title="Beads issues: ${folder.workingDir}"
                    >
                      <${BeadsIcon} className="w-3.5 h-3.5" />
                    </button>
                  `}
                  <button
                    type="button"
                    onClick=${(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      if (!isCreatingSession)
                        handleNewSessionInFolder(folder.workingDir, e);
                    }}
                    class="p-0.5 rounded transition-colors ${isCreatingSession
                      ? "cursor-wait opacity-60 text-mitto-text-muted"
                      : "hover:bg-mitto-surface-hover text-mitto-text-muted hover:text-mitto-text-strong"}"
                    title=${isCreatingSession
                      ? "Creating conversation\u2026"
                      : `New conversation in ${folder.label}`}
                    disabled=${isCreatingSession}
                  >
                    ${isCreatingSession
                      ? html`<${SpinnerIcon} className="w-3.5 h-3.5 animate-spin" />`
                      : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                  </button>
                  <span class="text-xs text-mitto-text-muted"
                    >${totalSessions}</span
                  >
                </summary>
                <ul>
                  ${renderSessionNodes(folder.conversations)}
                  <!-- Tasks (static, per-folder) — placeholder; wired in mitto-1er.7 -->
                  <li>
                    <button type="button" class="gap-2">
                      <${BeadsIcon} className="w-4 h-4 shrink-0" />
                      <span class="truncate">${folder.tasksNode.label}</span>
                    </button>
                  </li>
                  ${folder.archived.length > 0 &&
                  html`
                    <li class="archived-subgroup">
                      <details
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
                          class="gap-2 text-sm text-mitto-text-muted"
                        >
                          <${ArchiveIcon} className="w-4 h-4 shrink-0" />
                          <span class="truncate"
                            >Archived (${folder.archived.length})</span
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
        })}
      </ul>
    `;
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
        class="p-4 border-b border-mitto-border-1 flex items-center justify-between"
      >
        <h2 class="font-semibold text-lg">Conversations</h2>
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
            class="swap swap-rotate p-2 rounded-lg hover:bg-mitto-surface-hover transition-colors text-mitto-text-muted"
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
