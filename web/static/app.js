// Mitto Web Interface - Preact Application
const {
  render,
  Fragment,
  useState,
  useEffect,
  useLayoutEffect,
  useRef,
  useCallback,
  useMemo,
  html,
} = window.preact;

// Import shared library functions
import {
  computeAllSessions,
  coalesceAgentMessages,
  COALESCE_DEFAULTS,
  limitMessages,
  getWorkspaceVisualInfo,
  getBasename,
  updateGlobalWorkingDir,
  getGlobalWorkingDir,
  validateUsername,
  validatePassword,
  generatePromptId,
  savePendingPrompt,
  removePendingPrompt,
  getPendingPromptsForSession,
  cleanupExpiredPrompts,
  getArchiveReasonText,
  conversationToMarkdown,
  lastAgentMarkdown,
  copyToClipboard,
  LOOP_STOPPED_LABELS,
  formatLoopMaxDuration,
  computeHeaderTriggerLabel,
} from "./lib.js";

// Import session tree utilities
import {
  buildSessionTree,
  hasChildren,
  getChildCount,
} from "./utils/sessionTree.js";

// Import WebSocket utilities for app-activate debounce (mitto-c2p8.3)
import {
  createReconnectDebounceTracker,
  shouldDebounceReconnect,
  APP_ACTIVATE_RESYNC_DEBOUNCE_MS,
} from "./utils/websocket.js";

// Import utilities
import {
  openExternalURL,
  openFileURL,
  convertFileURLToViewer,
  convertHTTPFileURLToViewer,
  setCurrentWorkspace,
  setKnownWorkspaces,
  pickImages,
  hasNativeImagePicker,
  isNativeApp,
  getLastActiveSessionId,
  setLastActiveSessionId,
  initCSRF,
  fixViewerURLIfNeeded,
  getGroupingMode,
  cycleGroupingMode,
  isGroupExpanded,
  setGroupExpanded,
  getExpandedGroups,
  getSingleExpandedGroupMode,
  setSingleExpandedGroupMode,
  initUIPreferences,
  onUIPreferencesLoaded,
  fetchConfig,
  invalidateConfigCache,
  getSidebarWidth,
  setSidebarWidth,
} from "./utils/index.js";
import { getSdkClient } from "./utils/sdkClient.js";
import { errorMessage, isNotFoundError } from "./utils/sdkErrors.js";
import { isGone, markGone } from "./utils/beadsGoneCache.js";

// Import hooks
import {
  useWebSocket,
  useSwipeToAction,
  useInfiniteScroll,
  useToast,
  useResizeHandle,
  useTheme,
  useBackgroundNotifications,
  useScrollManagement,
  useQueueActions,
  useAgentPlan,
  useWorkspacePrompts,
  useBeadsIntegration,
  buildBeadsPromptToast,
  useBeadsKnownIds,
  useSessionNavigation,
  useConversationMenu,
  useConversationSeeding,
  decideLoopAction,
  makeLoopNow,
  useMCPInitState,
} from "./hooks/index.js";

// Import components
import { SessionItem } from "./components/SessionItem.js";
import { SessionList } from "./components/SessionList.js";
import { Toolbar } from "./components/Toolbar.js";
import { HeaderChildRow } from "./components/HeaderChildRow.js";
import { MessageList } from "./components/MessageList.js";
import { Message } from "./components/Message.js";
import { ChatInput } from "./components/ChatInput.js";
import {
  SettingsDialog,
  DEFAULT_MAC_OPEN_TARGETS,
} from "./components/SettingsDialog.js";
import { WorkspacesDialog } from "./components/WorkspacesDialog.js";
import { AddFolderDialog } from "./components/AddFolderDialog.js";
import { AgentDiscoveryDialog } from "./components/AgentDiscoveryDialog.js";
import { QueueDropdown } from "./components/QueueDropdown.js";
import {
  AgentPlanPanel,
  AgentPlanIndicator,
} from "./components/AgentPlanPanel.js";
import { SessionPanel } from "./components/SessionPanel.js";
import { Drawer } from "./components/Drawer.js";
import { CountdownDisplay } from "./components/CountdownDisplay.js";
import { ToastContainer } from "./components/ToastContainer.js";
import {
  SpinnerIcon,
  CloseIcon,
  SettingsIcon,
  PlusIcon,
  ChevronDownIcon,
  MenuIcon,
  TrashIcon,
  EditIcon,
  ArrowDownIcon,
  SaveIcon,
  ServerIcon,
  ServerEmptyIcon,
  FolderIcon,
  KeyboardIcon,
  SunIcon,
  MoonIcon,
  LightningIcon,
  getPromptIconOrDefault,
  RobotIcon,
  PersonIcon,
  HourglassIcon,
  QuestionMarkIcon,
  QueueIcon,
  PinIcon,
  PinFilledIcon,
  ArchiveIcon,
  ArchiveFilledIcon,
  ListIcon,
  LoopIcon,
  LoopOffIcon,
  LoopFilledIcon,
  CheckIcon,
  ClockIcon,
  StopIcon,
  PauseFilledIcon,
  ChatBubbleIcon,
  LayersIcon,
  TagIcon,
  SidePanelIcon,
  TerminalIcon,
  FolderOpenIcon,
  BeadsIcon,
  CopyIcon,
  BroomIcon,
} from "./components/Icons.js";
import { ContextMenu } from "./components/ContextMenu.js";
import {
  BeadsView,
  BeadsIssueView,
  BeadsDetailPanel,
} from "./components/BeadsView.js";
import { Dashboard } from "./components/Dashboard.js";

// Import constants
import {
  CYCLING_MODE,
  LOOP_PROGRESS_STYLE,
  LOOP_PROGRESS_COLORS,
  LOOP_PROGRESS_URGENT_THRESHOLD,
} from "./constants.js";

// Import prompt utilities
import {
  promptMenus,
  promptMenuIncludes,
  shouldOpenPromptDialog,
  promptDialogParameters,
  autofillConversationMenuArgs,
  fetchCachedParamNames,
  promptResolveAsLoop,
} from "./utils/prompts.js";

// Import global event handlers (registers side effects on module load) and predicates
import {
  isOverHorizontallyScrollable,
  isModalDialogOpen,
} from "./utils/globalHandlers.js";
import { OPEN_SETTINGS_EVENT } from "./utils/slackEvents.js";

// Import extracted components
import { WorkspaceBadge, WorkspacePill } from "./components/WorkspaceBadge.js";
import { DeleteDialog } from "./components/DeleteDialog.js";
import { KeyboardShortcutsDialog } from "./components/KeyboardShortcutsDialog.js";
import { NewSessionWorkspaceDialog } from "./components/NewSessionWorkspaceDialog.js";
import { LoopScheduleDialog } from "./components/LoopScheduleDialog.js";
import { PromptParameterDialog } from "./components/PromptParameterDialog.js";
import { Tooltip } from "./components/Tooltip.js";

// SettingsDialog, WorkspacesDialog, etc. are all imported from ./components/

// SessionItem and SessionList are imported from ./components/

// =============================================================================
// Main App Component
// =============================================================================

function App() {
  // Holds a callback (wired below, once useBeadsIntegration is set up) that
  // useWebSocket invokes when the ACTIVE conversation is removed from view
  // (deleted or archived), so the UI can navigate to the global Dashboard
  // instead of bouncing to another conversation or an empty state (mitto-ce3,
  // superseding mitto-17d's folder-Tasks route). A ref avoids the hook-ordering
  // problem: useWebSocket runs before handleShowDashboard exists.
  const onActiveSessionRemovedRef = useRef(null);
  // Holds a callback that useWebSocket invokes on initial connection when there
  // is no valid last-active conversation to restore (either no persisted id, or
  // the persisted id no longer maps to any existing session). When set, the
  // hook does NOT auto-switch to the most-recent session, giving the UI a
  // chance to land on the Dashboard on cold start (mitto-ce3).
  const onNoInitialSessionRef = useRef(null);
  // Debounce tracker for macOS app-activate resync (mitto-c2p8.3)
  const appActivateDebounceRef = useRef(createReconnectDebounceTracker());
  const {
    connected,
    messages,
    sendPrompt,
    cancelPrompt,
    newSession,
    switchSession,
    setActiveSessionId,
    loadMoreMessages,
    updateSessionName,
    renameSession,
    pinSession,
    setSessionColor,
    archiveSession,
    removeSession,
    isStreaming,
    agentWorking,
    isRunning,
    hasMoreMessages,
    hasReachedLimit,
    isLoadingMore,
    actionButtons,
    sessionInfo,
    activeSessionId,
    activeSessions,
    storedSessions,
    fetchStoredSessions,
    backgroundCompletion,
    clearBackgroundCompletion,
    loopStarted,
    clearLoopStarted,
    backgroundUIPrompt,
    clearBackgroundUIPrompt,
    backgroundUIPromptTimeout,
    clearBackgroundUIPromptTimeout,
    queueLength,
    queueMessages,
    queueConfig,
    fetchQueueMessages,
    deleteQueueMessage,
    addToQueue,
    moveQueueMessage,
    workspaces,
    acpServers,
    addWorkspace,
    removeWorkspace,
    refreshWorkspaces,
    forceReconnectActiveSession,
    reconnectAllSessionsStaggered,
    availableCommands,
    configOptions,
    setConfigOption,
    activeUIPrompt,
    sendUIPromptAnswer,
    mcpTools,
    mcpStatus,
    ensureResumed,
    isCreatingSession,
    creatingWorkingDirs,
  } = useWebSocket({ onActiveSessionRemovedRef, onNoInitialSessionRef });

  const { showToast, dismissToast, toasts } = useToast();

  // Auto-resume GC-suspended sessions when they become the active (focused) session.
  // Covers two cases:
  // 1. User switches to a gc-suspended session → resume starts immediately
  // 2. Session gets gc-suspended while user is already viewing it → resume triggers
  // After resume, gc_suspended becomes false so this effect won't re-trigger until
  // the next suspension. The GC won't immediately re-suspend because the session
  // has active WebSocket clients.
  // NOTE: This effect must stay after the useWebSocket() destructuring above so that
  // sessionInfo and ensureResumed are in scope when the dependency array is evaluated.
  useEffect(() => {
    if (
      activeSessionId &&
      sessionInfo?.gc_suspended &&
      !sessionInfo?.archived
    ) {
      ensureResumed(activeSessionId);
    }
  }, [
    activeSessionId,
    sessionInfo?.gc_suspended,
    sessionInfo?.archived,
    ensureResumed,
  ]);

  // Sidebar resize handle (horizontal direction)
  const {
    height: sidebarWidth,
    isDragging: isSidebarDragging,
    handleProps: sidebarHandleProps,
  } = useResizeHandle({
    initialHeight: getSidebarWidth(),
    minHeight: 320,
    maxHeight: 640,
    direction: "horizontal",
    onDragEnd: (finalWidth) => {
      setSidebarWidth(finalWidth);
    },
  });

  const [showSidebar, setShowSidebar] = useState(false);
  const [showSidePanel, setShowSidePanel] = useState(false);
  // Controlled-open state for the header "children dropdown" (mitto-7vpp). Kept
  // controlled so the Toolbar's closeOnOutsideClick machinery can dismiss it
  // when the user clicks outside or presses Escape, and so clicking a child
  // entry closes the menu before switching sessions.
  const [childrenMenuOpen, setChildrenMenuOpen] = useState(false);

  // Open/close state for the conversation-header "Copy" dropdown (mitto-a6v1).
  // Mirrors childrenMenuOpen above: driven by the Toolbar dropdown's
  // open/onToggle + closeOnOutsideClick props.
  const [copyMenuOpen, setCopyMenuOpen] = useState(false);

  // Close the mobile left sidebar when the user clicks outside of it (e.g. on the
  // conversation peek to its right). Below the md breakpoint the sidebar's
  // dimming .drawer-overlay backdrop is display:none (styles.css, mitto-cdf) — a
  // full-area overlay over the conversation dropped its GPU backing store on
  // pointer-move — so outside clicks are detected with a document listener (no
  // DOM overlay) instead, mirroring the right-side SessionPanel. Clicks inside
  // the sidebar panel (.drawer-side), or inside any modal dialog (.modal),
  // are ignored so those surfaces keep working. Guarded to the mobile breakpoint
  // (and showSidebar) so the always-open desktop sidebar (md:drawer-open) is
  // never dismissed.
  useEffect(() => {
    if (!showSidebar) return undefined;
    const onDocMouseDown = (e) => {
      if (!window.matchMedia("(max-width: 767.98px)").matches) return;
      const t = e.target;
      if (!t || !t.closest) return;
      if (t.closest(".drawer-side") || t.closest(".modal")) return;
      setShowSidebar(false);
    };
    document.addEventListener("mousedown", onDocMouseDown);
    return () => document.removeEventListener("mousedown", onDocMouseDown);
  }, [showSidebar]);
  // Quick "new task" create panel shown as an overlay over the current content
  // (e.g. a conversation) via the New task shortcut, without switching to the
  // beads list view. { open, workingDir } — workingDir is kept during the
  // close animation so only `open` is flipped on dismiss.
  const [quickCreate, setQuickCreate] = useState({
    open: false,
    workingDir: null,
  });
  // mainView controls what is shown in the right-side area:
  // "conversation" | "beads" | "dashboard" (mitto-aqo).
  const [mainView, setMainView] = useState("conversation");
  // Ref mirror of mainView so native swipe-gesture handlers (registered in an effect
  // whose dependency set does not include mainView) always read the current view
  // without a stale closure.
  const mainViewRef = useRef(mainView);
  useEffect(() => {
    mainViewRef.current = mainView;
  }, [mainView]);
  // Switch to a conversation AND bring it into focus. Unlike a bare
  // switchSession (which only changes the active session), this also leaves the
  // beads view if it is open and closes the mobile side panels, so the target
  // conversation is actually shown. Use this for notification/toast clicks where
  // the user expects the conversation to come to the foreground.
  const focusSession = useCallback(
    (sessionId) => {
      if (!sessionId) return;
      switchSession(sessionId);
      setMainView("conversation");
      setShowSidebar(false);
      setShowSidePanel(false);
    },
    [switchSession],
  );
  // Switch mainView to the global Dashboard (mitto-aqo). Leaves activeSessionId
  // untouched so returning to a conversation resumes the last selected one.
  // Mirrors handleSelectSession's mobile-drawer close so tapping the (future)
  // sidebar Dashboard button on a phone hands focus to the dashboard.
  const handleShowDashboard = useCallback(() => {
    setMainView("dashboard");
    setShowSidebar(false);
    setShowSidePanel(false);
  }, []);
  // When the beads view is opened from a linked conversation (e.g. the
  // properties panel's "Linked beads issue" link), these drive auto-selecting
  // that issue once the list loads. The nonce bumps on every open so clicking
  // the same issue again re-selects it.
  const [sidePanelTab, setSidePanelTab] = useState("properties");
  // Agent Plan panel (extracted to hooks/useAgentPlan.js): per-session plan
  // entries, mitto:plan_update handling, auto-expand/erase/expire, panel
  // toggle/close, user-message tracking, and per-session cleanup on delete.
  const {
    planEntries,
    showPlanPanel,
    planUserPinned,
    handleTogglePlanPanel,
    handleClosePlanPanel,
    trackUserMessageForPlanExpiration,
    clearPlanForSession,
  } = useAgentPlan({ activeSessionId });

  // Coalesce consecutive agent messages for display.
  // The backend's MarkdownBuffer flushes content at semantic boundaries (paragraphs,
  // headers, horizontal rules, etc.), creating separate events. This is correct for
  // tracking and sync, but creates a poor visual experience where each flush appears
  // as a separate message bubble. This combines them for rendering.
  //
  // EXPERIMENT: hrBreaksCoalescing - when enabled, <hr/> elements break coalescing,
  // creating visual separation between sections. See COALESCE_DEFAULTS in lib.js.
  const displayMessages = useMemo(() => {
    return coalesceAgentMessages(messages, {
      hrBreaksCoalescing: COALESCE_DEFAULTS.hrBreaksCoalescing,
    });
  }, [messages]);

  const [deleteDialog, setDeleteDialog] = useState({
    isOpen: false,
    session: null,
  });
  const [workspaceDialog, setWorkspaceDialog] = useState({ isOpen: false }); // Workspace selector for new session
  const [settingsDialog, setSettingsDialog] = useState({
    isOpen: false,
    forceOpen: false,
    initialTab: null,
  }); // Settings dialog
  const [workspacesDialog, setWorkspacesDialog] = useState({ isOpen: false }); // Workspaces management dialog
  const [addFolderDialogOpen, setAddFolderDialogOpen] = useState(false); // "Add folder to sidebar" dialog
  const [keyboardShortcutsDialog, setKeyboardShortcutsDialog] = useState({
    isOpen: false,
  }); // Keyboard shortcuts dialog
  // Loop schedule dialog: opened when a loop prompt is selected from any menu.
  // Shape: null | { prompt, onSchedule: async ({ value, unit, at? }) => void }
  const [loopScheduleDialog, setLoopScheduleDialog] = useState(null);
  // Prompt parameter dialog: opened when a beadsIssues prompt has parameters that
  // the menu cannot auto-fill. Shape: null | { prompt, parameters, onSubmit }
  const [promptParamDialog, setPromptParamDialog] = useState(null);
  // Workspace prompts: fetch/cache, predefined (dropup) subset, and per-session helpers.
  // (Extracted to hooks/useWorkspacePrompts.js)
  const {
    workspacePrompts,
    predefinedPrompts,
    loopPrompts,
    fetchWorkspacePrompts,
    fetchConversationPromptsForSession,
  } = useWorkspacePrompts({
    workingDir: sessionInfo?.working_dir,
    activeSessionId,
    showToast,
  });

  // Whether the active workspace has beads (`.beads` + `bd` on PATH): reuses the
  // SAME gate already evaluated server-side for beads prompts (enabledWhen:
  // CommandExists("bd") && DirExists(".beads")) — no new fetch. If ANY workspace
  // prompt opts into the beadsIssues/beadsList menus, the backend has already
  // proven this workspace is beads-enabled for the active session's folder.
  // Drives the "On tasks" loop trigger tab's visibility (mitto-oja.4).
  const hasBeadsWorkspace = useMemo(
    () =>
      (workspacePrompts || []).some(
        (p) =>
          promptMenuIncludes(p, "beadsIssues") ||
          promptMenuIncludes(p, "beadsList"),
      ),
    [workspacePrompts],
  );

  const [configReadonly, setConfigReadonly] = useState(
    () => window.mittoIsExternal === true, // Start as true for external connections, or when --config flag was used or using RC file
  );

  useEffect(() => {
    const handleOpenSettings = (event) => {
      if (configReadonly) return;
      setSettingsDialog({
        isOpen: true,
        forceOpen: false,
        initialTab: event?.detail?.tab || null,
      });
    };
    window.addEventListener(OPEN_SETTINGS_EVENT, handleOpenSettings);
    return () =>
      window.removeEventListener(OPEN_SETTINGS_EVENT, handleOpenSettings);
  }, [configReadonly]);
  const [rcFilePath, setRcFilePath] = useState(null); // Path to RC file when config is read-only due to RC file
  const [swipeDirection, setSwipeDirection] = useState(null); // 'left' or 'right' for animation
  const [swipeArrow, setSwipeArrow] = useState(null); // 'left' or 'right' for arrow indicator
  // Per-session draft text: { sessionId: draftText } - null key for "no session" state
  const [sessionDrafts, setSessionDrafts] = useState({});
  const sessionDraftsRef = useRef(sessionDrafts);
  useEffect(() => {
    sessionDraftsRef.current = sessionDrafts;
  }, [sessionDrafts]);
  const messagesEndRef = useRef(null);
  const mainContentRef = useRef(null);
  const messagesContainerRef = useRef(null);
  // Scroll position preservation for "load more" (prepend) - stores scroll metrics before loading
  const scrollPreservationRef = useRef(null);

  // Compute all sessions for navigation using shared helper function
  const allSessions = useMemo(
    () => computeAllSessions(activeSessions, storedSessions),
    [activeSessions, storedSessions],
  );

  // Beads integration: view state, issue-session map, prompt helpers, handlers.
  // (Extracted to hooks/useBeadsIntegration.js)
  const {
    beadsWorkingDir,
    beadsInitialIssueId,
    beadsSelectNonce,
    beadsCreateNonce,
    beadsRefreshNonce,
    beadsCleanupNonce,
    beadsIssueOpen,
    beadsIssueSessionMap,
    beadsIssueStreamingSet,
    fetchBeadsPromptsForWorkspace,
    fetchBeadsListPromptsForWorkspace,
    handleRunBeadsPrompt,
    handleRunBeadsListPrompt,
    handleBeadsOpen,
    handleBeadsCreate,
    handleBeadsRefresh,
    handleBeadsCleanup,
    handleOpenBeadsIssue,
    handleReturnFromBeadsIssue,
  } = useBeadsIntegration({
    allSessions,
    workspaces,
    newSession,
    showToast,
    switchSession,
    setMainView,
    setShowSidebar,
    setShowSidePanel,
    setSidePanelTab,
    activeSessionId,
    onOpenLoopDialog: (prompt, onSchedule) =>
      setLoopScheduleDialog({ prompt, onSchedule }),
    onOpenPromptParamDialog: (prompt, parameters, onSubmit, opts = {}) =>
      setPromptParamDialog({
        prompt,
        parameters,
        onSubmit,
        workingDir: opts.workingDir,
        initialValues: opts.initialValues,
        hostSessionId: opts.hostSessionId,
      }),
  });

  // Ref mirror of beadsIssueOpen: the native swipe-gesture handlers are
  // registered in an effect that does not depend on it, so they read the current
  // value through a ref to avoid a stale closure (matches mainViewRef).
  const beadsIssueOpenRef = useRef(beadsIssueOpen);
  useEffect(() => {
    beadsIssueOpenRef.current = beadsIssueOpen;
  }, [beadsIssueOpen]);

  // Conversation seeding: send a named prompt to an existing conversation via queue,
  // or create a new (optionally loop) conversation seeded with a named prompt.
  const { seedConversationWithPrompt, startConversationWithPrompt } =
    useConversationSeeding({ newSession });

  // Launch a named prompt in a new conversation for the "prompts" upstream type in BeadsView.
  // action is "pull"|"push"|"sync"; conversationName is set to "Pull tasks" etc.
  // args is an optional map of prompt argument name→value forwarded to the queue seed.
  const handleBeadsLaunchPrompt = useCallback(
    async (action, promptName, args) => {
      const names = {
        pull: "Pull tasks",
        push: "Push tasks",
        sync: "Sync tasks",
      };
      const conversationName = names[action] || "Tasks";
      const result = await startConversationWithPrompt({
        workingDir: beadsWorkingDir,
        // omit acpServer — use the folder default
        name: conversationName,
        prompt: { name: promptName },
        arguments: args,
      });
      if (!result?.sessionId) {
        showToast({
          style: "error",
          title: result?.error || `Failed to launch ${action} prompt`,
          duration: 4000,
        });
        return;
      }
      setMainView("conversation");
      showToast(
        buildBeadsPromptToast({
          result,
          promptName,
          activeSessionId,
        }),
      );
    },
    [
      startConversationWithPrompt,
      beadsWorkingDir,
      showToast,
      setMainView,
      activeSessionId,
    ],
  );

  // Fetch and cache known beads issue IDs for the active session's workspace.
  // Dispatches "beads-ids-updated" to re-linkify already-rendered messages.
  useBeadsKnownIds(sessionInfo?.working_dir);

  // Expose a global so globalHandlers.js can open the beads issue viewer when
  // a linkified beads ID is clicked in a conversation message.
  useEffect(() => {
    window.mittoOpenBeadsIssue = (id) =>
      handleOpenBeadsIssue(
        id,
        sessionInfo?.working_dir || window.mittoCurrentWorkspace || "",
        activeSessionId,
      );
    return () => {
      delete window.mittoOpenBeadsIssue;
    };
  }, [handleOpenBeadsIssue, activeSessionId, sessionInfo?.working_dir]);

  // Linked beads issue status for the conversation-toolbar "linked issue" button
  // badge dot. Fetched asynchronously (bd show can be slow) and keyed on the
  // active conversation's linked issue; cleared when there is no linked issue.
  // Mirrors the fetch in SessionPanel.js so both surfaces stay in sync.
  //
  // Re-fetches on:
  //   - link change (activeSessionId / sessionInfo.beads_issue / working_dir)
  //     — including MCP-driven changes via session_beads_issue_updated, which
  //     updates sessionInfo.beads_issue and re-triggers this effect.
  //   - filesystem change to the .beads/ dir for our working_dir
  //     (mitto:beads_changed) — captures status transitions made via `bd` CLI
  //     or another agent, refreshed via a monotonic beadsRefreshTick counter.
  const [headerBeadsStatus, setHeaderBeadsStatus] = useState(null);
  const [beadsRefreshTick, setBeadsRefreshTick] = useState(0);
  useEffect(() => {
    const workingDir = sessionInfo?.working_dir;
    if (!workingDir) return undefined;
    const onBeadsChanged = (e) => {
      const dirs = e?.detail?.working_dirs;
      if (!Array.isArray(dirs) || dirs.length === 0) {
        // Missing/malformed payload — assume relevant and refresh.
        setBeadsRefreshTick((t) => t + 1);
        return;
      }
      if (dirs.includes(workingDir)) {
        setBeadsRefreshTick((t) => t + 1);
      }
    };
    window.addEventListener("mitto:beads_changed", onBeadsChanged);
    return () =>
      window.removeEventListener("mitto:beads_changed", onBeadsChanged);
  }, [sessionInfo?.working_dir]);
  useEffect(() => {
    const issueId = sessionInfo?.beads_issue;
    const workingDir = sessionInfo?.working_dir;
    if (!issueId || !workingDir) {
      setHeaderBeadsStatus(null);
      return;
    }
    // mitto-msv: short-circuit ids known to 404. Keeps beadsRefreshTick bumps
    // (fired for any mitto:beads_changed broadcast) from re-issuing the same
    // 404 for a stale linked bead.
    if (isGone(workingDir, issueId)) {
      setHeaderBeadsStatus(null);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const data = await getSdkClient().issues.show(issueId, {
          working_dir: workingDir,
        });
        if (cancelled) return;
        const issueObj = Array.isArray(data) ? data[0] : data;
        if (issueObj && !issueObj.error && issueObj.status) {
          setHeaderBeadsStatus(issueObj.status);
        } else {
          setHeaderBeadsStatus(null);
        }
      } catch (err) {
        if (isNotFoundError(err)) markGone(workingDir, issueId);
        if (!cancelled) setHeaderBeadsStatus(null);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [
    activeSessionId,
    sessionInfo?.beads_issue,
    sessionInfo?.working_dir,
    beadsRefreshTick,
  ]);

  // Wire the active-conversation-removed callback consumed by useWebSocket. When
  // the active conversation is deleted or archived (in this window or via a
  // cross-window session_deleted / session_archived broadcast), navigate to the
  // global Dashboard so the user lands on a workspace-agnostic overview instead
  // of being bounced to another conversation or an empty state (mitto-ce3,
  // superseding mitto-17d's folder-Tasks route). The folderWorkingDir argument
  // from the hook is unused now but the signature is preserved to keep the
  // call-sites in useWebSocket.js unchanged.
  useEffect(() => {
    onActiveSessionRemovedRef.current = (_folderWorkingDir) => {
      handleShowDashboard();
    };
  }, [handleShowDashboard]);

  // Wire the no-initial-session callback: on cold start, when there is no valid
  // last-active conversation to restore, land on the Dashboard instead of
  // aggressively opening the most-recent conversation (mitto-ce3).
  useEffect(() => {
    onNoInitialSessionRef.current = () => {
      handleShowDashboard();
    };
  }, [handleShowDashboard]);

  // Initialize CSRF protection and UI preferences on mount
  // This pre-fetches a CSRF token so subsequent state-changing requests are protected
  // Also loads UI preferences from server (for macOS app where localStorage doesn't persist)
  useEffect(() => {
    initCSRF();
    initUIPreferences();
  }, []);

  // Clear swipe direction after animation completes
  useEffect(() => {
    if (swipeDirection) {
      const timer = setTimeout(() => setSwipeDirection(null), 250);
      return () => clearTimeout(timer);
    }
  }, [swipeDirection, activeSessionId]);

  // Clear swipe arrow indicator after animation completes (1 second)
  useEffect(() => {
    if (swipeArrow) {
      const timer = setTimeout(() => setSwipeArrow(null), 1000);
      return () => clearTimeout(timer);
    }
  }, [swipeArrow]);

  // Show toast and native notification when a background session completes
  useEffect(() => {
    if (backgroundCompletion) {
      // Show native macOS notification (not sticky — auto-dismisses)
      if (
        window.mittoNativeNotificationsEnabled &&
        typeof window.mittoShowNativeNotification === "function"
      ) {
        window.mittoShowNativeNotification(
          backgroundCompletion.sessionName || "Conversation",
          "Agent completed",
          backgroundCompletion.sessionId,
          false,
        );
      }

      // Show in-app toast
      showToast({
        style: "success",
        title: backgroundCompletion.sessionName || "Conversation",
        message: "finished",
        duration: 5000,
        onClick: () => focusSession(backgroundCompletion.sessionId),
      });
      clearBackgroundCompletion();
    }
  }, [
    backgroundCompletion,
    clearBackgroundCompletion,
    showToast,
    focusSession,
  ]);

  // Show toast and native notification when a loop prompt starts
  useEffect(() => {
    if (loopStarted) {
      // Show native macOS notification (not sticky — auto-dismisses)
      if (
        window.mittoNativeNotificationsEnabled &&
        typeof window.mittoShowNativeNotification === "function"
      ) {
        window.mittoShowNativeNotification(
          loopStarted.sessionName || "Loop Conversation",
          "Loop run started",
          loopStarted.sessionId,
          false,
        );
      }

      // Show in-app toast
      showToast({
        style: "info",
        title: loopStarted.sessionName || "Loop Conversation",
        message: "loop run started",
        duration: 5000,
        onClick: () => focusSession(loopStarted.sessionId),
      });
      clearLoopStarted();
    }
  }, [loopStarted, clearLoopStarted, showToast, focusSession]);

  // Show toast when a UI prompt arrives in a background session
  useEffect(() => {
    if (backgroundUIPrompt) {
      // In-app toast (native notification is handled in useWebSocket)
      showToast({
        style: "warning",
        title: `Question in ${backgroundUIPrompt.sessionName || "conversation"}`,
        duration: 8000,
        onClick: () => focusSession(backgroundUIPrompt.sessionId),
      });
      clearBackgroundUIPrompt();
    }
  }, [backgroundUIPrompt, clearBackgroundUIPrompt, showToast, focusSession]);

  // Show toast and native notification when a background UI prompt times out
  // This fires when a blocking prompt expired while the user was not viewing the session.
  useEffect(() => {
    if (backgroundUIPromptTimeout) {
      const sessionName =
        backgroundUIPromptTimeout.sessionName || "Conversation";
      // Show native macOS notification (sticky — user needs to go check the session)
      if (
        window.mittoNativeNotificationsEnabled &&
        typeof window.mittoShowNativeNotification === "function"
      ) {
        window.mittoShowNativeNotification(
          sessionName,
          backgroundUIPromptTimeout.question || "Agent needed your input",
          backgroundUIPromptTimeout.sessionId,
          true, // sticky — keep until dismissed
        );
      }
      // Show in-app toast
      showToast({
        style: "warning",
        title: `Missed prompt in ${sessionName}`,
        message:
          backgroundUIPromptTimeout.question || "Agent needed your input",
        duration: 10000,
        onClick: () => focusSession(backgroundUIPromptTimeout.sessionId),
      });
      clearBackgroundUIPromptTimeout();
    }
  }, [
    backgroundUIPromptTimeout,
    clearBackgroundUIPromptTimeout,
    showToast,
    focusSession,
  ]);

  // Background notification event listeners (extracted to
  // hooks/useBackgroundNotifications.js): runner fallback, memory recycle,
  // ACP start/permanent errors, hook failures, generic notifications, and
  // active-session native-notification cleanup. activeWorkspaceUUID drives
  // workspace-scoped notification filtering for mitto_workspace_ui_notify
  // (mitto-6bn) so only clients viewing the target workspace see the toast.
  useBackgroundNotifications({
    showToast,
    focusSession,
    activeSessionId,
    activeWorkspaceUUID: sessionInfo?.workspace_uuid ?? null,
  });

  // Per-workspace MCP-init lifecycle state (mitto-8fm): drives a persistent
  // inline "Waiting for MCP servers…" indicator in MessageList, distinct from
  // the transient toast fired by useBackgroundNotifications above. Derived
  // for the active session's workspace only; re-evaluated whenever the
  // underlying state map changes (getMCPInitState's identity bumps) or the
  // active workspace changes.
  const { getMCPInitState, clearMCPInit } = useMCPInitState();
  const mcpInitState = useMemo(
    () =>
      getMCPInitState(sessionInfo?.workspace_uuid, sessionInfo?.working_dir),
    [getMCPInitState, sessionInfo?.workspace_uuid, sessionInfo?.working_dir],
  );

  // Get the current draft for the active session (null key = no session)
  const currentDraft = sessionDrafts[activeSessionId ?? "__no_session__"] || "";

  // Update draft for a specific session (or null = no session)
  const updateDraft = useCallback((sessionId, text) => {
    const key = sessionId ?? "__no_session__";
    setSessionDrafts((prev) => ({ ...prev, [key]: text }));
  }, []);

  // Ref-based version for async callbacks (avoid stale closure)
  const updateDraftForSession = useCallback((sessionId, text) => {
    const key = sessionId ?? "__no_session__";
    setSessionDrafts((prev) => ({ ...prev, [key]: text }));
  }, []);

  // Handle loading more messages
  // Note: isLoadingMore state is managed by useWebSocket hook, not locally.
  // The hook sets isLoadingMore=true when sending load_events request,
  // and clears it when events_loaded response is received.
  const handleLoadMore = useCallback(() => {
    if (isLoadingMore || !activeSessionId || !hasMoreMessages) return;

    // Save scroll metrics BEFORE loading for scroll position preservation
    // When new messages are prepended, we'll restore the position relative to existing content
    const container = messagesContainerRef.current;
    if (container) {
      scrollPreservationRef.current = {
        scrollHeight: container.scrollHeight,
        scrollTop: container.scrollTop,
      };
      console.log(
        "[Scroll] Saved scroll metrics before load more:",
        scrollPreservationRef.current,
      );
    }

    loadMoreMessages(activeSessionId);
  }, [isLoadingMore, activeSessionId, hasMoreMessages, loadMoreMessages]);

  // Infinite scroll for loading earlier messages
  // Uses IntersectionObserver to detect when user scrolls near the top
  // Scroll position restoration is handled by the useInfiniteScroll hook
  const { sentinelRef } = useInfiniteScroll({
    hasMoreMessages,
    isLoading: isLoadingMore,
    onLoadMore: handleLoadMore,
    containerRef: messagesContainerRef,
    rootMargin: "300px", // Trigger 300px before reaching top for smooth experience
    debounceMs: 500, // Prevent rapid-fire loading
  });

  // Conversation navigation: cycling mode, navigable-sessions memo, keyboard/swipe
  // navigate handlers, and sidebar-sync event listeners.
  // (Extracted to hooks/useSessionNavigation.js)
  const {
    conversationCyclingMode,
    setConversationCyclingMode,
    navigableSessions,
    navigateToPreviousSession,
    navigateToNextSession,
    navigateToSessionAbove,
    navigateToSessionBelow,
    navigateToSessionByIndex,
    openSidebar,
  } = useSessionNavigation({
    allSessions,
    storedSessions,
    workspaces,
    activeSessionId,
    switchSession,
    setShowSidebar,
    setSwipeDirection,
    setSwipeArrow,
    mainContentRef,
  });

  // Global keyboard shortcuts for Command+1-9 to switch sessions and Command+, for settings
  useEffect(() => {
    const handleGlobalKeyDown = (e) => {
      // Command+Control+Up/Down to navigate between conversations (macOS)
      if (e.metaKey && e.ctrlKey && !e.shiftKey && !e.altKey) {
        if (e.key === "ArrowUp") {
          e.preventDefault();
          navigateToSessionAbove();
          setTimeout(() => {
            if (chatInputRef.current) {
              chatInputRef.current.focus();
            }
          }, 100);
          return;
        }
        if (e.key === "ArrowDown") {
          e.preventDefault();
          navigateToSessionBelow();
          setTimeout(() => {
            if (chatInputRef.current) {
              chatInputRef.current.focus();
            }
          }, 100);
          return;
        }
      }

      // Command+Shift+A to archive/unarchive current conversation
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey) {
        if (e.key === "A" || e.key === "a") {
          e.preventDefault();
          if (window.mittoArchiveConversation) {
            window.mittoArchiveConversation();
          }
          return;
        }
        // Command+Shift+N to create a new task in the current workspace.
        if (e.key === "N" || e.key === "n") {
          e.preventDefault();
          if (mainViewRef.current === "beads" && beadsWorkingDir) {
            // Already in the beads view: use its in-panel create so the issue
            // list refreshes after saving (same as the list's "+" button).
            handleBeadsCreate(beadsWorkingDir);
          } else {
            // Anywhere else (e.g. a conversation): open the create panel as an
            // overlay on top of the current content, without switching views.
            const wd = sessionInfo?.working_dir;
            if (wd) {
              setQuickCreate({ open: true, workingDir: wd });
            }
          }
          return;
        }
      }

      // Check for Command (macOS) or Ctrl (other platforms)
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey) {
        const key = e.key;
        // Check if key is 1-9
        if (key >= "1" && key <= "9") {
          e.preventDefault();
          const index = parseInt(key, 10) - 1; // Convert to 0-based index
          navigateToSessionByIndex(index);
          // Focus the input after switching sessions
          setTimeout(() => {
            if (chatInputRef.current) {
              chatInputRef.current.focus();
            }
          }, 100);
        }
        // Command+, to open settings (standard macOS convention)
        if (key === ",") {
          e.preventDefault();
          if (!configReadonly) {
            setSettingsDialog({ isOpen: true, forceOpen: false });
          }
        }
        // Ctrl+/ or Cmd+/ to toggle prompts menu (global shortcut)
        if (key === "/") {
          e.preventDefault();
          if (chatInputRef.current?.togglePrompts) {
            chatInputRef.current.togglePrompts();
          }
        }
      }
    };

    window.addEventListener("keydown", handleGlobalKeyDown);
    return () => window.removeEventListener("keydown", handleGlobalKeyDown);
  }, [
    navigateToSessionByIndex,
    navigateToSessionAbove,
    navigateToSessionBelow,
    configReadonly,
    handleBeadsCreate,
    beadsWorkingDir,
    sessionInfo?.working_dir,
  ]);

  // State for UI theme style (v2 = Clawdbot-inspired)

  // UI settings (macOS only)
  const [agentCompletedSoundEnabled, setAgentCompletedSoundEnabled] =
    useState(false);

  // Confirmation mode for destroying a conversation (close via Cmd+W, sidebar
  // delete). One of "always" (default), "responding", or "never".
  const [deleteConfirmMode, setDeleteConfirmMode] = useState("always");

  // "Open In" targets (macOS only, mitto-bbi). Populated from config.ui.mac.open_in.targets;
  // falls back to DEFAULT_MAC_OPEN_TARGETS when the block is absent — matches the fallback
  // the backend applies at exec time via config.DefaultOpenTargets(). Passed to
  // <SessionList /> for the folder context-menu "Open ▸" submenu; the row workspace
  // badge also invokes the "finder" entry via handleBadgeClick.
  const [openInTargets, setOpenInTargets] = useState(() =>
    DEFAULT_MAC_OPEN_TARGETS.map((t) => ({ ...t })),
  );

  // The row workspace badge is enabled iff we are in the native app AND the
  // "finder" OpenTarget is present and enabled — clicking it routes through
  // action=open,target_id=finder (mitto-b7d).
  const finderTarget = openInTargets.find((t) => t && t.id === "finder");
  const badgeClickEnabled = isNativeApp() && finderTarget?.enabled === true;

  // Input font family setting (web UI, default: "system")
  const [inputFontFamily, setInputFontFamily] = useState("system");

  // Input font size setting (web UI, default: "default")
  const [inputFontSize, setInputFontSize] = useState("default");

  // Conversation font family setting (web UI, default: "system") — applied to
  // .markdown-content only, independent of the compose/input font.
  const [conversationFontFamily, setConversationFontFamily] =
    useState("system");

  // Conversation base font size (web UI, default: "sm") — the sidebar
  // small-A / large-A toggle re-anchors on this value.
  const [conversationFontSize, setConversationFontSize] = useState("sm");

  // Send key mode setting (web UI, default: "enter")
  // "enter" = Enter to send, Shift+Enter for new line
  // "ctrl-enter" = Ctrl/Cmd+Enter to send, Enter for new line
  const [sendKeyMode, setSendKeyMode] = useState("enter");

  // Agent discovery dialog state (shown on first run when no ACP servers configured)
  const [showAgentDiscovery, setShowAgentDiscovery] = useState(false);

  // Global model profiles (config.models). Threaded into ChatInput → PromptsMenu
  // so prompts with structured preferredModels ({modelName}/{modelTag}) can
  // resolve to an "overrides model" chip. Refreshed alongside other UI settings
  // on mount and after SettingsDialog saves.
  const [modelProfiles, setModelProfiles] = useState([]);

  // Check if running in the native macOS app
  const isMacApp = typeof window.mittoPickFolder === "function";

  // Fetch config on mount to get predefined prompts, UI theme, and check for workspaces
  useEffect(() => {
    fetchConfig()
      .then((config) => {
        // Load global model profiles (config.models) for PromptsMenu chips.
        setModelProfiles(Array.isArray(config?.models) ? config.models : []);
        // Track if config is read-only (loaded from --config file or RC file)
        if (config?.config_readonly) {
          setConfigReadonly(true);
          // If using an RC file, store the path for tooltip display
          if (config?.rc_file_path) {
            setRcFilePath(config.rc_file_path);
          }
        }
        // Load UI confirmation mode (default "always")
        setDeleteConfirmMode(
          config?.ui?.confirmations?.delete_conversation || "always",
        );
        // Load UI settings (macOS only)
        console.log(
          "[config] ui.mac.notifications:",
          config?.ui?.mac?.notifications,
        );
        if (config?.ui?.mac?.notifications?.sounds?.agent_completed) {
          console.log("[config] Setting agent_completed sound ENABLED");
          setAgentCompletedSoundEnabled(true);
          window.mittoAgentCompletedSoundEnabled = true;
        }
        // Load native notifications setting (macOS only)
        if (config?.ui?.mac?.notifications?.native_enabled) {
          console.log("[config] Setting native notifications ENABLED");
          window.mittoNativeNotificationsEnabled = true;
        }
        // Load Open In targets (macOS only). Same shape and fallback as
        // SettingsDialog.js — when ui.mac.open_in.targets is missing/empty we
        // synthesize the shared DEFAULT_MAC_OPEN_TARGETS so the folder
        // context-menu submenu still shows Finder + Terminal on fresh installs.
        const macOpenInTargets = config?.ui?.mac?.open_in?.targets;
        if (Array.isArray(macOpenInTargets) && macOpenInTargets.length > 0) {
          setOpenInTargets(
            macOpenInTargets.map((t) => ({
              id: t.id || "",
              label: t.label || "",
              icon: t.icon || "",
              command: t.command || "",
              enabled: t.enabled !== false,
              builtin: t.builtin === true,
            })),
          );
        } else {
          setOpenInTargets(DEFAULT_MAC_OPEN_TARGETS.map((t) => ({ ...t })));
        }
        // Load input font family setting (web UI)
        if (config?.ui?.web?.input_font_family) {
          setInputFontFamily(config.ui.web.input_font_family);
        }
        // Load input font size setting (web UI)
        if (config?.ui?.web?.input_font_size) {
          setInputFontSize(config.ui.web.input_font_size);
        }
        // Load conversation font family setting (web UI)
        if (config?.ui?.web?.conversation_font_family) {
          setConversationFontFamily(config.ui.web.conversation_font_family);
        }
        // Load conversation base font size setting (web UI)
        if (config?.ui?.web?.conversation_font_size) {
          setConversationFontSize(config.ui.web.conversation_font_size);
        }
        // Load send key mode setting (web UI, default: "enter")
        if (config?.ui?.web?.send_key_mode) {
          setSendKeyMode(config.ui.web.send_key_mode);
        }
        // Load conversation cycling mode setting (web UI, default: "all")
        if (config?.ui?.web?.conversation_cycling_mode) {
          setConversationCyclingMode(config.ui.web.conversation_cycling_mode);
        }
        // Load accordion mode setting for groups (web UI, default: false)
        setSingleExpandedGroupMode(
          config?.ui?.web?.single_expanded_group === true,
        );
        // Check if ACP servers or workspaces are configured - if not, prompt user to set up
        // Skip this if config is read-only (user manages config via file) or if external connection
        const noAcpServers =
          !config?.acp_servers || config.acp_servers.length === 0;
        const noWorkspaces =
          !config?.workspaces || config.workspaces.length === 0;
        const isExternalConnection = window.mittoIsExternal === true;
        if (
          (noAcpServers || noWorkspaces) &&
          !config?.config_readonly &&
          !isExternalConnection
        ) {
          if (noAcpServers) {
            // Show agent discovery dialog first so the user can auto-detect installed agents
            setShowAgentDiscovery(true);
          } else {
            // Only workspaces missing - go straight to settings
            setSettingsDialog({ isOpen: true, forceOpen: true });
          }
        }
      })
      .catch((err) => console.error("Failed to fetch config:", err));
  }, []);

  // Set current workspace for file URL conversion (used in web browser mode)
  // Use workspace_uuid directly from sessionInfo (sent by backend in 'connected' message)
  // instead of looking it up by working_dir, which fails when multiple workspaces
  // exist for the same directory (different ACP servers).
  useEffect(() => {
    const workingDir = sessionInfo?.working_dir;
    const workspaceUUID = sessionInfo?.workspace_uuid;
    if (workingDir) {
      setCurrentWorkspace(workingDir, workspaceUUID);
    }
  }, [sessionInfo?.working_dir, sessionInfo?.workspace_uuid]);

  // Publish the known-workspaces registry so buildWorkspaceViewerURL can
  // resolve the correct UUID for File: links in views that target a workspace
  // other than the currently-active conversation (e.g. the beads view opened
  // for workspace B while the active conversation is in workspace A).
  useEffect(() => {
    setKnownWorkspaces(workspaces || []);
  }, [workspaces]);

  // Theme, font-size, and reduced-motion preferences (extracted to hooks/useTheme.js)
  const { theme, toggleTheme, fontSize, toggleFontSize } = useTheme();

  // Apply input font family class to document
  useEffect(() => {
    const root = document.documentElement;
    // Remove all input font classes first
    const fontClasses = [
      "input-font-system",
      "input-font-sans-serif",
      "input-font-serif",
      "input-font-monospace",
      "input-font-menlo",
      "input-font-monaco",
      "input-font-consolas",
      "input-font-courier-new",
      "input-font-jetbrains-mono",
      "input-font-sf-mono",
      "input-font-cascadia-code",
    ];
    fontClasses.forEach((cls) => root.classList.remove(cls));
    // Add the current font class
    root.classList.add(`input-font-${inputFontFamily}`);
  }, [inputFontFamily]);

  // Apply input font size class to document
  useEffect(() => {
    const root = document.documentElement;
    const sizeClasses = [
      "input-fontsize-small",
      "input-fontsize-default",
      "input-fontsize-medium",
      "input-fontsize-large",
      "input-fontsize-xl",
    ];
    sizeClasses.forEach((cls) => root.classList.remove(cls));
    root.classList.add(`input-fontsize-${inputFontSize}`);
  }, [inputFontSize]);

  // Apply conversation font family class to <html>. Parallel to input-font-*
  // but targets .markdown-content only via .conv-font-* rules in styles.css.
  useEffect(() => {
    const root = document.documentElement;
    const convFontClasses = [
      "conv-font-system",
      "conv-font-sans-serif",
      "conv-font-serif",
      "conv-font-inter",
      "conv-font-sf-pro",
      "conv-font-helvetica-neue",
      "conv-font-roboto",
      "conv-font-georgia",
      "conv-font-charter",
      "conv-font-ibm-plex-sans",
    ];
    convFontClasses.forEach((cls) => root.classList.remove(cls));
    root.classList.add(`conv-font-${conversationFontFamily}`);
  }, [conversationFontFamily]);

  // Apply conversation base font size as a CSS variable so the existing
  // .font-small / .font-large rules re-anchor on it (small-A = base,
  // large-A = base + 2px). Kept in sync with the WebUIConfig options.
  useEffect(() => {
    const CONV_BASE_PX = {
      xs: "13px",
      sm: "14px",
      md: "15px",
      lg: "16px",
      xl: "18px",
    };
    const px = CONV_BASE_PX[conversationFontSize] || CONV_BASE_PX.sm;
    document.documentElement.style.setProperty("--mitto-conv-base-size", px);
  }, [conversationFontSize]);

  // Messages-area scroll management (extracted to hooks/useScrollManagement.js):
  // at-bottom tracking, new-message indicator, auto-scroll on new content,
  // instant positioning on session switch, and prepend scroll restoration.
  // messagesContainerRef and scrollPreservationRef are owned by App (shared with
  // the render, useInfiniteScroll, and handleLoadMore) and passed in.
  const { isUserAtBottom, hasNewMessages, scrollToBottom } =
    useScrollManagement({
      messages,
      activeSessionId,
      mainView,
      isStreaming,
      isLoadingMore,
      messagesContainerRef,
      scrollPreservationRef,
    });

  // Ref for the chat input component to allow focusing from native menu
  const chatInputRef = useRef(null);

  // Expose global functions for native macOS menu integration
  useEffect(() => {
    // New Conversation - called from native Cmd+N menu
    window.mittoNewConversation = async () => {
      // Use handleNewSession logic to support workspace selection
      if (workspaces.length === 0) {
        // No workspaces configured - open settings dialog (unless config is read-only)
        if (!configReadonly) {
          setSettingsDialog({ isOpen: true, forceOpen: true });
        }
        setShowSidebar(false);
        return;
      }
      if (workspaces.length > 1) {
        setWorkspaceDialog({ isOpen: true });
      } else {
        // Single workspace - create session directly with workspace info
        const ws = workspaces[0];
        const result = await newSession({
          workingDir: ws.working_dir,
          acpServer: ws.acp_server,
        });
        // Handle creation result
        if (result?.errorCode === "session_creation_timeout") {
          // Agent is busy; auto-retry is in progress — toast already meaningful
          showToast({
            style: "warning",
            title: result.retrying
              ? "Agent is busy \u2014 retrying automatically\u2026"
              : result.error || "Agent is busy",
            duration: result.retrying ? 30000 : 5000,
          });
        } else if (
          result?.errorCode === "no_workspace_configured" &&
          !configReadonly
        ) {
          setSettingsDialog({ isOpen: true, forceOpen: true });
        } else if (result?.sessionId) {
          // Switch away from the beads panel so the new conversation is shown.
          setMainView("conversation");
        }
      }
      setShowSidebar(false);
      // Focus the input after creating new session
      setTimeout(() => {
        if (chatInputRef.current) {
          chatInputRef.current.focus();
        }
      }, 100);
    };

    // Focus Input - called from native Cmd+L menu
    window.mittoFocusInput = () => {
      if (chatInputRef.current) {
        chatInputRef.current.focus();
      }
    };

    // Toggle Sidebar - called from native Cmd+Shift+S menu
    window.mittoToggleSidebar = () => {
      setShowSidebar((prev) => !prev);
    };

    // Show Settings - called from native Cmd+, menu
    window.mittoShowSettings = () => {
      if (!configReadonly) {
        setSettingsDialog({ isOpen: true, forceOpen: false });
      }
    };

    // Close Conversation - called from native Cmd+W menu
    window.mittoCloseConversation = async () => {
      if (!activeSessionId) return;

      // Find the current session to pass to the dialog
      const currentSession =
        activeSessions.find((s) => s.session_id === activeSessionId) ||
        storedSessions.find((s) => s.session_id === activeSessionId);

      // The active conversation's live streaming state is authoritative for
      // whether the agent is currently responding.
      const isActivePrompting =
        isStreaming || currentSession?.isStreaming || false;

      // Confirm based on the delete-confirmation mode: "always" confirms every
      // close; "responding" confirms only while the agent is responding (so an
      // accidental Cmd+W cannot discard an in-progress conversation); "never"
      // closes without a dialog.
      if (
        deleteConfirmMode === "always" ||
        (deleteConfirmMode === "responding" && isActivePrompting)
      ) {
        if (currentSession) {
          setDeleteDialog({
            isOpen: true,
            session: { ...currentSession, isStreaming: isActivePrompting },
          });
        }
        return;
      }

      // Otherwise delete immediately
      await removeSession(activeSessionId);
      fetchStoredSessions();
    };

    // Archive Conversation - called from native Cmd+Shift+A menu or web shortcut
    window.mittoArchiveConversation = async () => {
      if (!activeSessionId) return;

      // Find the current session
      const currentSession =
        activeSessions.find((s) => s.session_id === activeSessionId) ||
        storedSessions.find((s) => s.session_id === activeSessionId);
      if (!currentSession) return;

      // Don't archive spawned (child) sessions
      if (currentSession.parent_id) return;

      // Check if already archived
      const isArchived =
        currentSession.archived || currentSession.info?.archived;

      // Protected conversations (mitto-yvel.4) cannot be archived — this native
      // shortcut bypasses every button, so it needs its own guard. Unarchive
      // stays allowed.
      const isProtected =
        currentSession.no_archive || currentSession.info?.no_archive;
      if (!isArchived && isProtected) {
        showToast({
          style: "warning",
          title: "This conversation is protected from archiving",
          duration: 4000,
        });
        return;
      }

      // Toggle archive state
      await archiveSession(activeSessionId, !isArchived);

      // When unarchiving, select the session
      if (isArchived) {
        switchSession(activeSessionId);
      }
    };

    // Next Conversation - called from native swipe gesture (swipe left)
    window.mittoNextConversation = () => {
      // Don't navigate if the cursor is over a horizontally scrollable element
      // (e.g. a wide table) — the user is scrolling the table, not navigating.
      if (isOverHorizontallyScrollable()) return;
      // Don't navigate if a modal dialog is open.
      if (isModalDialogOpen()) return;
      // Don't navigate when the beads list view or the docked single-issue
      // overlay is open — swipes should not switch conversations underneath them.
      if (mainViewRef.current === "beads" || beadsIssueOpenRef.current) return;
      navigateToNextSession();
    };

    // Previous Conversation - called from native swipe gesture (swipe right)
    window.mittoPrevConversation = () => {
      // Don't navigate if the cursor is over a horizontally scrollable element.
      if (isOverHorizontallyScrollable()) return;
      // Don't navigate if a modal dialog is open.
      if (isModalDialogOpen()) return;
      // Don't navigate when the beads list view or the docked single-issue
      // overlay is open — swipes should not switch conversations underneath them.
      if (mainViewRef.current === "beads" || beadsIssueOpenRef.current) return;
      navigateToPreviousSession();
    };

    // Switch to Session - called from native notification tap. Bring the
    // conversation into focus (leaving the beads view if it is open) so the
    // tapped conversation is actually shown, not just activated underneath.
    window.mittoSwitchToSession = (sessionId) => {
      if (sessionId) {
        focusSession(sessionId);
      }
    };

    // App Did Become Active - called from native macOS when app becomes visible
    // WKWebView doesn't fire visibilitychange events, so the native app calls this
    // to trigger WebSocket reconnection and sync any missed messages.
    // Uses staggered reconnect so multiple sessions don't all send load_events simultaneously.
    window.mittoAppDidBecomeActive = () => {
      const { debounced, elapsed } = shouldDebounceReconnect(
        appActivateDebounceRef.current,
        "__app_activate__",
        { windowMs: APP_ACTIVATE_RESYNC_DEBOUNCE_MS },
      );
      if (debounced) {
        console.debug(
          `[macOS] App became active — skipping redundant resync (${elapsed}ms since last, debounce=${APP_ACTIVATE_RESYNC_DEBOUNCE_MS}ms)`,
        );
        return;
      }
      console.log(
        "[macOS] App became active, triggering staggered reconnect and sync",
      );
      reconnectAllSessionsStaggered();
      // Also refresh session list in case there were changes
      fetchStoredSessions();
    };

    // Cleanup on unmount
    return () => {
      delete window.mittoNewConversation;
      delete window.mittoFocusInput;
      delete window.mittoToggleSidebar;
      delete window.mittoShowSettings;
      delete window.mittoCloseConversation;
      delete window.mittoArchiveConversation;
      delete window.mittoNextConversation;
      delete window.mittoPrevConversation;
      delete window.mittoSwitchToSession;
      delete window.mittoAppDidBecomeActive;
    };
  }, [
    newSession,
    workspaces,
    removeSession,
    fetchStoredSessions,
    activeSessionId,
    deleteConfirmMode,
    isStreaming,
    activeSessions,
    storedSessions,
    configReadonly,
    navigateToNextSession,
    navigateToPreviousSession,
    switchSession,
    focusSession,
    forceReconnectActiveSession,
    reconnectAllSessionsStaggered,
    archiveSession,
  ]);

  const handleNewSession = async (workspace = null, folderFilter = null) => {
    // If a specific workspace is provided, create session directly in that workspace
    if (workspace) {
      setShowSidebar(false);
      const result = await newSession({
        workingDir: workspace.working_dir,
        acpServer: workspace.acp_server,
      });
      // Handle creation result
      if (result?.errorCode === "session_creation_timeout") {
        showToast({
          style: "warning",
          title: result.retrying
            ? "Agent is busy \u2014 retrying automatically\u2026"
            : result.error || "Agent is busy",
          duration: result.retrying ? 30000 : 5000,
        });
      } else if (
        result?.errorCode === "no_workspace_configured" &&
        !configReadonly
      ) {
        setSettingsDialog({ isOpen: true, forceOpen: true });
      } else if (result?.sessionId) {
        // newSession activates the new conversation; switch away from the beads
        // panel so the new conversation is shown instead of the beads view.
        setMainView("conversation");
        // Focus the input after creating new session
        setTimeout(() => {
          if (chatInputRef.current) {
            chatInputRef.current.focus();
          }
        }, 100);
      }
      return;
    }

    // If folder filter provided, show workspace dialog filtered to that folder
    if (folderFilter) {
      const filteredWs = workspaces.filter(
        (ws) => ws.working_dir === folderFilter,
      );
      if (filteredWs.length === 1) {
        // Single workspace in folder - create directly
        setShowSidebar(false);
        const result = await newSession({
          workingDir: filteredWs[0].working_dir,
          acpServer: filteredWs[0].acp_server,
        });
        if (result?.errorCode === "session_creation_timeout") {
          showToast({
            style: "warning",
            title: result.retrying
              ? "Agent is busy \u2014 retrying automatically\u2026"
              : result.error || "Agent is busy",
            duration: result.retrying ? 30000 : 5000,
          });
        } else if (
          result?.errorCode === "no_workspace_configured" &&
          !configReadonly
        ) {
          setSettingsDialog({ isOpen: true, forceOpen: true });
        } else if (result?.sessionId) {
          // Switch away from the beads panel so the new conversation is shown.
          setMainView("conversation");
          setTimeout(() => {
            if (chatInputRef.current) chatInputRef.current.focus();
          }, 100);
        }
      } else if (filteredWs.length > 1) {
        setWorkspaceDialog({ isOpen: true, filteredWorkspaces: filteredWs });
        setShowSidebar(false);
      }
      return;
    }

    // If no workspaces configured, open settings dialog (unless config is read-only)
    if (workspaces.length === 0) {
      if (!configReadonly) {
        setSettingsDialog({ isOpen: true, forceOpen: true });
      }
      setShowSidebar(false);
      return;
    }
    // If multiple workspaces, show workspace selector
    if (workspaces.length > 1) {
      setWorkspaceDialog({ isOpen: true });
      setShowSidebar(false);
    } else {
      // Single workspace - create session directly with workspace info
      setShowSidebar(false);
      const ws = workspaces[0];
      const result = await newSession({
        workingDir: ws.working_dir,
        acpServer: ws.acp_server,
      });
      // Handle creation result
      if (result?.errorCode === "session_creation_timeout") {
        showToast({
          style: "warning",
          title: result.retrying
            ? "Agent is busy \u2014 retrying automatically\u2026"
            : result.error || "Agent is busy",
          duration: result.retrying ? 30000 : 5000,
        });
      } else if (
        result?.errorCode === "no_workspace_configured" &&
        !configReadonly
      ) {
        setSettingsDialog({ isOpen: true, forceOpen: true });
      } else if (result?.sessionId) {
        // Switch away from the beads panel so the new conversation is shown.
        setMainView("conversation");
        // Focus the input after creating new session
        setTimeout(() => {
          if (chatInputRef.current) {
            chatInputRef.current.focus();
          }
        }, 100);
      }
    }
  };

  const handleWorkspaceSelect = async (workspace) => {
    setWorkspaceDialog({ isOpen: false });
    const result = await newSession({
      workingDir: workspace.working_dir,
      acpServer: workspace.acp_server,
    });
    // Handle creation result
    if (result?.errorCode === "session_creation_timeout") {
      showToast({
        style: "warning",
        title: result.retrying
          ? "Agent is busy \u2014 retrying automatically\u2026"
          : result.error || "Agent is busy",
        duration: result.retrying ? 30000 : 5000,
      });
    } else if (
      result?.errorCode === "no_workspace_configured" &&
      !configReadonly
    ) {
      setSettingsDialog({ isOpen: true, forceOpen: true });
    } else if (result?.sessionId) {
      // Switch away from the beads panel so the new conversation is shown.
      setMainView("conversation");
      // Focus the input after creating new session
      setTimeout(() => {
        if (chatInputRef.current) {
          chatInputRef.current.focus();
        }
      }, 100);
    }
  };

  const handleShowSettings = () => {
    // Don't open settings dialog if config is read-only
    if (configReadonly) {
      return;
    }
    setSettingsDialog({ isOpen: true, forceOpen: false });
  };

  const handleShowWorkspaces = () => {
    if (configReadonly) return;
    setWorkspacesDialog({ isOpen: true });
  };

  const handleShowWorkspacesForFolder = useCallback(
    (workingDir, tab) => {
      if (configReadonly) return;
      setWorkspacesDialog({ isOpen: true, workingDir, tab });
    },
    [configReadonly],
  );

  const handleShowKeyboardShortcuts = () => {
    setKeyboardShortcutsDialog({ isOpen: true });
  };

  // Message-queue dropdown actions/state (extracted to hooks/useQueueActions.js):
  // open/close/toggle, add/delete/move queued messages, badge pulse, auto-close
  // timer, and auto-hide effects (dialog open, sidebar expand, queue_updated).
  const {
    showQueueDropdown,
    isDeletingQueueMessage,
    isMovingQueueMessage,
    isAddingToQueue,
    handleToggleQueueDropdown,
    handleCloseQueueDropdown,
    handleDeleteQueueMessage,
    handleMoveQueueMessage,
    handleAddToQueue,
  } = useQueueActions({
    activeSessionId,
    showToast,
    updateDraft,
    fetchQueueMessages,
    addToQueue,
    deleteQueueMessage,
    moveQueueMessage,
    settingsDialogOpen: settingsDialog.isOpen,
    workspacesDialogOpen: workspacesDialog.isOpen,
    showSidebar,
  });

  // Unified side panel handlers
  const handleToggleSidePanel = useCallback(() => {
    setShowSidePanel((prev) => {
      if (!prev) setSidePanelTab("properties");
      return !prev;
    });
  }, []);

  const handleCloseSidePanel = useCallback(() => {
    setShowSidePanel(false);
  }, []);

  const handleOpenSidePanelTab = useCallback((tab) => {
    setSidePanelTab(tab);
    setShowSidePanel(true);
  }, []);

  // Wrapper for sendPrompt that tracks messages for plan expiration.
  // When a named prompt is dispatched with user-supplied arguments (from the
  // PromptParameterDialog), route through the queue API so the backend can
  // apply ${VAR} substitution — the WebSocket prompt path does not forward
  // arguments. All other sends go through the normal WebSocket path.
  const handleSendPrompt = useCallback(
    async (message, images = [], files = [], options = {}) => {
      // Track this message for plan expiration before sending
      trackUserMessageForPlanExpiration(activeSessionId);

      // Named prompt with user arguments → queue API (supports ${VAR} substitution)
      if (
        options.promptName &&
        options.arguments &&
        Object.keys(options.arguments).length > 0 &&
        activeSessionId
      ) {
        return seedConversationWithPrompt(
          activeSessionId,
          { name: options.promptName },
          { arguments: options.arguments },
        );
      }

      // Call the original sendPrompt
      return sendPrompt(message, images, files, options);
    },
    [
      sendPrompt,
      seedConversationWithPrompt,
      trackUserMessageForPlanExpiration,
      activeSessionId,
    ],
  );

  // Handler for prompts dropdown open - refreshes workspace prompts (which now include all sources)
  const handlePromptsOpen = useCallback(() => {
    if (sessionInfo?.working_dir) {
      fetchWorkspacePrompts(sessionInfo.working_dir, false);
    }
  }, [sessionInfo?.working_dir, fetchWorkspacePrompts]);

  const handleSelectSession = (sessionId, opts) => {
    switchSession(sessionId);
    // keepSidebarOpen is set when the selection is an auto-focus triggered by
    // expanding a folder (see SessionList.handleFolderOpened). In that case the
    // mobile sidebar drawer must stay open — only direct conversation clicks
    // close it.
    if (!opts?.keepSidebarOpen) {
      setShowSidebar(false);
      setShowSidePanel(false);
    }
    setMainView("conversation");
  };

  // Handle badge click action — routes through the "finder" OpenTarget
  // (mitto-b7d). Sends {action:"open", target_id:"finder"} to /api/badge-click;
  // backend resolves against EffectiveOpenTargets() and executes the target's
  // Command via sh -c. Errors surface as toasts.
  const handleBadgeClick = useCallback(
    async (workspacePath) => {
      if (!badgeClickEnabled || !workspacePath) return;

      try {
        const data = await getSdkClient().misc.badgeClick({
          workspace_path: workspacePath,
          action: "open",
          target_id: "finder",
        });
        if (!data.success && data.error) {
          showToast({ style: "error", title: data.error });
        }
      } catch (err) {
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to open folder"),
        });
      }
    },
    [badgeClickEnabled, showToast],
  );

  // Fire a configured "Open In" target (mitto-bbi.4). Sends
  // {action:"open", target_id} to /api/badge-click; backend resolves against
  // EffectiveOpenTargets() and executes target.Command via sh -c. Errors surface
  // as toasts using the same envelope as handleBadgeClick.
  const handleOpenTarget = useCallback(
    async (workspacePath, targetId) => {
      if (!workspacePath || !targetId) return;

      try {
        const data = await getSdkClient().misc.badgeClick({
          workspace_path: workspacePath,
          action: "open",
          target_id: targetId,
        });
        if (!data.success && data.error) {
          showToast({ style: "error", title: data.error });
        }
      } catch (err) {
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to open target"),
        });
      }
    },
    [showToast],
  );

  // Move a folder to an organizational group (folders.json group label). An
  // empty group clears the assignment. Persists via PUT /api/workspaces/{uuid}/folder-group,
  // then refreshes workspaces so the sidebar regroups immediately.
  const handleMoveFolderToGroup = useCallback(
    async (workingDir, group) => {
      if (!workingDir) return;
      const ws = (workspaces || []).find((w) => w.working_dir === workingDir);
      const uuid = ws?.uuid;
      if (!uuid) {
        showToast({ style: "error", title: "Unknown workspace folder" });
        return;
      }
      try {
        await getSdkClient().workspaces.setFolderGroup(uuid, group || "");
        invalidateConfigCache();
        refreshWorkspaces();
        const trimmed = (group || "").trim();
        showToast({
          style: "success",
          title: trimmed ? `Moved to group "${trimmed}"` : "Removed from group",
        });
      } catch (err) {
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to move folder to group"),
        });
      }
    },
    [showToast, refreshWorkspaces, workspaces],
  );

  // Unpin a folder from the sidebar (removes the folder-native `pinned` flag in
  // folders.json). Used by the folder context-menu "Remove from sidebar" action
  // for pinned empty folders. Persists via PUT /api/folders/pin, then refreshes
  // workspaces so the sidebar drops the now-unpinned empty folder.
  const handleUnpinFolder = useCallback(
    async (workingDir) => {
      if (!workingDir) return;
      try {
        await getSdkClient().misc.folderPin.set(
          { working_dir: workingDir },
          { pinned: false },
        );
        invalidateConfigCache();
        refreshWorkspaces();
        showToast({
          style: "success",
          title: "Removed folder from sidebar",
        });
      } catch (err) {
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to remove folder from sidebar"),
        });
      }
    },
    [showToast, refreshWorkspaces],
  );

  // Pin an existing (currently hidden) workspace to the sidebar. Mirrors
  // handleUnpinFolder but sets pinned=true. Used by AddFolderDialog when the
  // user picks a hidden workspace from the list.
  const handlePinExistingFolder = useCallback(
    async (workingDir) => {
      if (!workingDir) return;
      try {
        await getSdkClient().misc.folderPin.set(
          { working_dir: workingDir },
          { pinned: true },
        );
        invalidateConfigCache();
        refreshWorkspaces();
        showToast({
          style: "success",
          title: "Folder added to sidebar",
        });
      } catch (err) {
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to add folder to sidebar"),
        });
      }
    },
    [showToast, refreshWorkspaces],
  );

  // Hidden workspaces = configured workspaces that are (a) not currently pinned
  // and (b) have no active/stored session pointing at their working_dir. These
  // are the candidates surfaced in AddFolderDialog's pick-existing list.
  const hiddenWorkspaces = useMemo(() => {
    const visibleWorkingDirs = new Set();
    (activeSessions || []).forEach((s) => {
      if (s && s.working_dir) visibleWorkingDirs.add(s.working_dir);
    });
    (storedSessions || []).forEach((s) => {
      if (s && s.working_dir) visibleWorkingDirs.add(s.working_dir);
    });
    const filtered = (workspaces || []).filter(
      (ws) =>
        ws &&
        ws.working_dir &&
        ws.pinned !== true &&
        !visibleWorkingDirs.has(ws.working_dir),
    );
    // Sort MRU-first by last_opened_at (ISO-8601 timestamp from folders.json,
    // projected onto workspace records). Zero/absent timestamps sort last;
    // ties fall back to alphabetical by display name, then working_dir.
    const sorted = filtered.slice().sort((a, b) => {
      const ta = a.last_opened_at ? Date.parse(a.last_opened_at) : 0;
      const tb = b.last_opened_at ? Date.parse(b.last_opened_at) : 0;
      if (ta !== tb) return tb - ta;
      const na = (a.name || a.working_dir || "").toLowerCase();
      const nb = (b.name || b.working_dir || "").toLowerCase();
      return na.localeCompare(nb);
    });
    // Collapse duplicate working_dir entries (e.g. two workspace UUIDs sharing
    // one folder — an interactive workspace + a loop-babysit workspace). The
    // picker pins by working_dir, so extra rows are indistinguishable to the
    // user. Post-sort dedup keeps the MRU-highest occurrence of each path.
    const seen = new Set();
    const deduped = [];
    for (const ws of sorted) {
      if (seen.has(ws.working_dir)) continue;
      seen.add(ws.working_dir);
      deduped.push(ws);
    }
    return deduped;
  }, [workspaces, activeSessions, storedSessions]);

  const handleAddFolderOpen = useCallback(
    () => setAddFolderDialogOpen(true),
    [],
  );
  const handleAddFolderClose = useCallback(
    () => setAddFolderDialogOpen(false),
    [],
  );

  // Open the properties panel for a session (used by pencil button in session list)
  const handleOpenSessionProperties = useCallback(
    (session) => {
      // Switch to the session if not already active
      if (session.session_id !== activeSessionId) {
        switchSession(session.session_id);
        setShowSidebar(false);
      }
      // Open the side panel on the properties tab
      setSidePanelTab("properties");
      setShowSidePanel(true);
    },
    [activeSessionId, switchSession],
  );

  const handleDeleteSession = async (session) => {
    // A conversation that is still receiving a response must always be
    // confirmed before deletion. For the active conversation the live
    // top-level streaming state is authoritative; otherwise fall back to the
    // per-session flag.
    const isPrompting =
      session?.isStreaming ||
      (session?.session_id === activeSessionId && isStreaming) ||
      false;

    // Delete immediately only when no confirmation is required: mode is "never",
    // or mode is "responding" while the agent is not currently responding.
    if (
      deleteConfirmMode === "never" ||
      (deleteConfirmMode === "responding" && !isPrompting)
    ) {
      // Clean up plan entries, expiration tracking, and completion timers for this session
      clearPlanForSession(session.session_id);
      await removeSession(session.session_id);
      fetchStoredSessions();
      return;
    }
    // Otherwise show the confirmation dialog
    setDeleteDialog({
      isOpen: true,
      session: { ...session, isStreaming: isPrompting },
    });
  };

  const handleConfirmDelete = async () => {
    const session = deleteDialog.session;
    if (!session) return;

    // Close the dialog first
    setDeleteDialog({ isOpen: false, session: null });

    // Clean up plan entries, expiration tracking, and completion timers for this session
    clearPlanForSession(session.session_id);

    // removeSession handles: closing WebSocket, updating local state,
    // switching to another session (or creating new if none left), and calling DELETE API
    await removeSession(session.session_id);

    // Refresh the stored sessions list
    fetchStoredSessions();
  };

  const handlePinSession = async (session, pinned) => {
    await pinSession(session.session_id, pinned);
  };

  const handleSetSessionColor = useCallback(
    (session, color) => {
      const id = session?.session_id;
      if (id) setSessionColor(id, color);
    },
    [setSessionColor],
  );

  const handleArchiveSession = async (session, archived) => {
    await archiveSession(session.session_id, archived);

    if (!archived) {
      // When unarchiving, select the session
      switchSession(session.session_id);
    }
    // When archiving the active conversation, navigation to that conversation's
    // folder Tasks (beads) view is handled inside useWebSocket's archiveSession
    // (synchronously, same window) and the session_archived broadcast handler
    // (cross-window), mirroring how deletion defers to removeSession (mitto-17d).
  };

  // Convert an existing regular conversation to a loop one. First try to restore
  // settings that were preserved by a previous "un-loop" (POST /loop/restore);
  // this brings back the saved prompt/frequency/trigger and its enabled state,
  // making loop ⇄ un-loop a symmetric toggle. When there is nothing saved (404),
  // fall back to creating a blank draft config (enabled:false). Either way the
  // loop_updated WebSocket event sets loop_configured=true and exposes the Loop
  // tab in the conversation panel.
  const handleMakeLoop = useCallback(
    async (session) => {
      const sessionId = session?.session_id;
      if (!sessionId) return;
      const openLoopEditor = () => {
        focusSession(sessionId);
        // Let a context-menu conversion finish switching conversations before
        // selecting a tab that only exists on the target loop conversation.
        setTimeout(() => handleOpenSidePanelTab("loop"), 0);
      };
      try {
        // Attempt to restore previously-saved loop settings.
        try {
          await getSdkClient().sessions.loop.restore(sessionId);
          openLoopEditor();
          showToast({
            style: "success",
            title: "Loop settings restored",
            message: "Your previous loop settings were restored.",
            duration: 6000,
          });
          return;
        } catch (err) {
          if (!isNotFoundError(err)) throw err;
        }

        // Nothing saved — try to pre-fill the draft from the most recent
        // named prompt's loop: frontmatter block (mitto-qff). On any
        // suggest/set failure fall through to today's blank-draft PUT below.
        try {
          const suggestion =
            await getSdkClient().sessions.loop.suggestFromRecent(sessionId);
          await getSdkClient().sessions.loop.set(sessionId, {
            ...suggestion,
            enabled: false,
          });
          openLoopEditor();
          showToast({
            style: "success",
            title: "Loop pre-filled from your last prompt",
            message: "Review and enable scheduling.",
            duration: 6000,
          });
          return;
        } catch (_) {
          // Fall through to blank-draft PUT on any suggest/PUT failure.
        }

        // Nothing saved and no suggestion — create a fresh blank draft.
        // Draft body: an empty prompt is accepted while enabled:false keeps
        // it as DRAFT so nothing is scheduled yet. Never write a placeholder
        // body — the runner would deliver it literally once enabled.
        await getSdkClient().sessions.loop.set(sessionId, {
          prompt: "",
          frequency: { value: 1, unit: "hours" },
          enabled: false,
        });
        openLoopEditor();
        showToast({
          style: "success",
          title: "Conversation is now loop",
          message: "Choose a prompt and enable scheduling.",
          duration: 6000,
        });
      } catch (e) {
        showToast({
          style: "error",
          title: "Failed to make conversation loop",
          duration: 5000,
        });
      }
    },
    [focusSession, handleOpenSidePanelTab, showToast],
  );

  // Remove the loop config from a conversation, reverting it to a regular one.
  // DELETE /api/sessions/{id}/loop broadcasts loop_updated (nil), which
  // sets both loop_configured=false (hides the compact controls and Loop tab) and
  // loop_enabled=false (moves conversation back to the Conversations group).
  const handleMakeNonLoop = useCallback(
    async (session) => {
      const sessionId = session?.session_id;
      if (!sessionId) return;
      try {
        await getSdkClient().sessions.loop.detach(sessionId);
        showToast({
          style: "success",
          title: "Loop scheduling removed",
          message:
            "Now a regular conversation. Your loop settings are saved and will be restored if you loop it again.",
          duration: 6000,
        });
      } catch (e) {
        showToast({
          style: "error",
          title: "Failed to remove loop scheduling",
          duration: 5000,
        });
      }
    },
    [showToast],
  );

  // Send a context-menu prompt to a specific conversation by enqueueing its full
  // text. The queue delivers it to the agent when the conversation is idle, so
  // this works for any conversation (not just the active one).
  //
  // When the chosen prompt declares `loop`, the handler branches on the target
  // conversation's state (decideLoopAction):
  //   "new-loop"  — no session: open schedule dialog → create NEW loop conversation.
  //   "make-loop" — regular conversation: configure as loop + fire first run.
  //   "one-shot"  — already a loop / child conversation: send prompt once, no config change.
  const handleSendPromptToConversation = useCallback(
    async (session, prompt, opts) => {
      if (!prompt?.name) return;

      // The menu can target any conversation, not just the active one, so the
      // parameter dialog is scoped to that conversation's folder.
      const targetWorkingDir = session?.working_dir || undefined;
      const asLoop = promptResolveAsLoop(prompt, opts?.asLoop);
      if (asLoop) {
        const action = decideLoopAction(session);

        if (action === "make-loop") {
          // Regular conversation: configure it as loop now and fire the first run.
          const sessionId = session.session_id;
          const cached = sessionId
            ? await fetchCachedParamNames(sessionId, prompt.name)
            : undefined;
          const shouldOpen = shouldOpenPromptDialog(
            prompt,
            "conversation",
            cached,
          );
          if (shouldOpen) {
            setPromptParamDialog({
              prompt,
              parameters: promptDialogParameters(prompt, "conversation"),
              hostSessionId: sessionId,
              workingDir: targetWorkingDir,
              onSubmit: async (userArgs) => {
                const result = await makeLoopNow(sessionId, prompt, {
                  arguments: userArgs,
                });
                if (result.success) {
                  showToast({
                    style: "success",
                    title: `Made conversation loop with "${prompt.name}"`,
                    duration: 3000,
                  });
                } else {
                  showToast({
                    style: "warning",
                    title: "Failed to configure loop schedule",
                    duration: 4000,
                  });
                }
              },
            });
            return;
          }
          const result = await makeLoopNow(sessionId, prompt);
          if (result.success) {
            showToast({
              style: "success",
              title: `Made conversation loop with "${prompt.name}"`,
              duration: 3000,
            });
          } else {
            showToast({
              style: "warning",
              title: "Failed to configure loop schedule",
              duration: 4000,
            });
          }
          return;
        }

        if (action === "one-shot") {
          // Already-loop or child conversation: enqueue a single run without touching config.
          const sessionId = session?.session_id;
          if (!sessionId) return;
          const cached = await fetchCachedParamNames(sessionId, prompt.name);
          const shouldOpen = shouldOpenPromptDialog(
            prompt,
            "conversation",
            cached,
          );
          if (shouldOpen) {
            setPromptParamDialog({
              prompt,
              parameters: promptDialogParameters(prompt, "conversation"),
              hostSessionId: sessionId,
              workingDir: targetWorkingDir,
              onSubmit: async (userArgs) => {
                const result = await seedConversationWithPrompt(
                  sessionId,
                  prompt,
                  { arguments: userArgs },
                );
                if (result.success) {
                  showToast({
                    style: "success",
                    title: `Sent "${prompt.name}" to conversation`,
                    duration: 3000,
                  });
                } else {
                  showToast({
                    style: "warning",
                    title: "Failed to send prompt",
                    duration: 4000,
                  });
                }
              },
            });
            return;
          }
          const result = await seedConversationWithPrompt(sessionId, prompt);
          if (result.success) {
            showToast({
              style: "success",
              title: `Sent "${prompt.name}" to conversation`,
              duration: 3000,
            });
          } else {
            showToast({
              style: "warning",
              title: "Failed to send prompt",
              duration: 4000,
            });
          }
          return;
        }

        // action === "new-loop": no session — open schedule dialog → create NEW loop conversation.
        // When the prompt has parameters, collect them first, then open the schedule dialog.
        const openScheduleDialog = (collectedArgs) => {
          setLoopScheduleDialog({
            prompt,
            onSchedule: async (schedule) => {
              setLoopScheduleDialog(null);
              const workingDir = session?.working_dir;
              const acpServer = session?.acp_server;
              const result = await startConversationWithPrompt({
                workingDir,
                acpServer,
                prompt,
                ...(collectedArgs && Object.keys(collectedArgs).length > 0
                  ? { arguments: collectedArgs }
                  : {}),
                loop: schedule,
              });
              if (result?.sessionId) {
                focusSession(result.sessionId);
                showToast({
                  style: "success",
                  title: `Started loop "${prompt.name}"`,
                  duration: 3000,
                });
              } else {
                showToast({
                  style: "warning",
                  title: "Failed to start loop conversation",
                  duration: 4000,
                });
              }
            },
          });
        };
        if (shouldOpenPromptDialog(prompt, "conversation")) {
          setPromptParamDialog({
            prompt,
            parameters: promptDialogParameters(prompt, "conversation"),
            workingDir: targetWorkingDir,
            onSubmit: (userArgs) => openScheduleDialog(userArgs),
          });
          return;
        }
        openScheduleDialog(undefined);
        return;
      }

      // Non-loop prompt: enqueue the named prompt to the existing conversation.
      const sessionId = session?.session_id;
      if (!sessionId) return;
      // Auto-fill what the host conversation can supply (e.g. a lone child for a
      // childSessionId param), then prompt the user only for what remains.
      const autoArgs = autofillConversationMenuArgs(
        prompt,
        sessionId,
        allSessions,
      );
      const knownNames = new Set(
        Object.keys(autoArgs).filter((k) => autoArgs[k] !== undefined),
      );
      const cached = await fetchCachedParamNames(sessionId, prompt.name);
      const shouldOpen = shouldOpenPromptDialog(
        prompt,
        "conversation",
        cached,
        knownNames,
      );
      if (shouldOpen) {
        setPromptParamDialog({
          prompt,
          parameters: promptDialogParameters(
            prompt,
            "conversation",
            knownNames,
          ),
          hostSessionId: sessionId,
          workingDir: targetWorkingDir,
          initialValues: autoArgs,
          onSubmit: async (userArgs) => {
            const result = await seedConversationWithPrompt(sessionId, prompt, {
              arguments: { ...autoArgs, ...userArgs },
            });
            if (result.success) {
              showToast({
                style: "success",
                title: `Sent "${prompt.name}" to conversation`,
                duration: 3000,
              });
            } else {
              showToast({
                style: "warning",
                title: "Failed to send prompt",
                duration: 4000,
              });
            }
          },
        });
        return;
      }
      const result = await seedConversationWithPrompt(
        sessionId,
        prompt,
        Object.keys(autoArgs).length > 0 ? { arguments: autoArgs } : undefined,
      );
      if (result.success) {
        showToast({
          style: "success",
          title: `Sent "${prompt.name}" to conversation`,
          duration: 3000,
        });
      } else {
        showToast({
          style: "warning",
          title: "Failed to send prompt",
          duration: 4000,
        });
      }
    },
    [
      seedConversationWithPrompt,
      startConversationWithPrompt,
      showToast,
      focusSession,
      setPromptParamDialog,
      allSessions,
    ],
  );

  // ----- Chat header conversation menu -----
  // Resolve the active conversation object (the same enriched object the sidebar
  // list uses) so the header three-dot menu mirrors the sidebar row menu exactly.
  const activeSession = useMemo(
    () =>
      activeSessionId
        ? allSessions.find((s) => s.session_id === activeSessionId) || null
        : null,
    [allSessions, activeSessionId],
  );

  // ----- Header title inline editing (click title to rename, mitto-dpd) -----
  // Mirrors the inline-rename pattern in ConversationPropertiesPanel.js /
  // SessionPanel.js, but click-only (no pencil) per the header-specific UX
  // decision, and available whenever a conversation is active (including the
  // "New conversation" placeholder) — not just when a name is already set.
  const [isEditingHeaderTitle, setIsEditingHeaderTitle] = useState(false);
  const [editedHeaderTitle, setEditedHeaderTitle] = useState("");
  const [isSavingHeaderTitle, setIsSavingHeaderTitle] = useState(false);
  const headerTitleInputRef = useRef(null);

  const handleStartEditHeaderTitle = useCallback(() => {
    if (!activeSessionId) return;
    setEditedHeaderTitle(sessionInfo?.name || "");
    setIsEditingHeaderTitle(true);
  }, [activeSessionId, sessionInfo?.name]);

  const handleSaveHeaderTitle = useCallback(async () => {
    if (!activeSessionId || isSavingHeaderTitle) return;
    const newTitle = editedHeaderTitle.trim();
    // Unlike the side-panel handlers, an empty title is NOT blocked here: the
    // backend already treats an empty name as "clear NameExplicit" (re-enable
    // auto-title), and the bead's acceptance criteria require that path to
    // work from the header too. Only skip the network round-trip when nothing
    // actually changed.
    if (newTitle === (sessionInfo?.name || "")) {
      setIsEditingHeaderTitle(false);
      return;
    }
    setIsSavingHeaderTitle(true);
    try {
      await renameSession(activeSessionId, newTitle);
      setIsEditingHeaderTitle(false);
    } catch (err) {
      console.error("Failed to save header title:", err);
    } finally {
      setIsSavingHeaderTitle(false);
    }
  }, [
    activeSessionId,
    editedHeaderTitle,
    sessionInfo?.name,
    renameSession,
    isSavingHeaderTitle,
  ]);

  const handleHeaderTitleKeyDown = useCallback(
    (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        handleSaveHeaderTitle();
      } else if (e.key === "Escape") {
        e.preventDefault();
        setIsEditingHeaderTitle(false);
      }
    },
    [handleSaveHeaderTitle],
  );

  // Focus + select the input when entering edit mode (matches the side panel).
  useEffect(() => {
    if (isEditingHeaderTitle && headerTitleInputRef.current) {
      headerTitleInputRef.current.focus();
      headerTitleInputRef.current.select();
    }
  }, [isEditingHeaderTitle]);

  // Cancel any in-progress header title edit when switching conversations so
  // a stale edit buffer can't leak across sessions.
  useEffect(() => {
    setIsEditingHeaderTitle(false);
  }, [activeSessionId]);

  // A conversation is "spawned" (a child) when it has a parent and is not itself
  // a parent of other conversations — mirrors SessionList's row classification.
  const activeHasChildren = useMemo(
    () =>
      !!activeSessionId &&
      allSessions.some((s) => s.parent_session_id === activeSessionId),
    [allSessions, activeSessionId],
  );
  // Children of the active conversation, in the same order they appear in
  // allSessions (which the sidebar already renders sorted). Feeds the header
  // "children dropdown" (mitto-7vpp) so users can jump between generations of
  // a spawned tree without navigating the sidebar. Archived children are
  // omitted — they are not part of the active tree the user cares about.
  const activeChildren = useMemo(
    () =>
      activeSessionId
        ? allSessions.filter(
            (s) => s.parent_session_id === activeSessionId && !s.archived,
          )
        : [],
    [allSessions, activeSessionId],
  );
  const headerIsArchived = activeSession?.archived || false;
  const headerIsLoop = activeSession?.loop_configured || false;
  const headerIsSpawned =
    !!(activeSession && activeSession.parent_session_id) && !activeHasChildren;

  // Cmd/Ctrl+Shift+L: toggle loop/unloop for the active conversation. Mirrors
  // the header toolbar buttons in conversationToolbarItems (Loop / Unloop) —
  // same guards, same handlers — so keyboard and mouse stay in lockstep.
  // (Plain ⌘L is already claimed by the native macOS menu for "Focus Input".)
  // Placed here (not in the main shortcut effect above) because it needs
  // activeSession and the headerIs* flags, which are declared just above.
  useEffect(() => {
    const handleLoopShortcut = (e) => {
      if (!(e.metaKey || e.ctrlKey) || !e.shiftKey || e.altKey) return;
      if (e.key !== "l" && e.key !== "L") return;
      if (!activeSession) return;
      if (headerIsSpawned) return;
      if (!headerIsLoop && headerIsArchived) return;
      e.preventDefault();
      if (headerIsLoop) {
        handleMakeNonLoop(activeSession);
      } else {
        handleMakeLoop(activeSession);
      }
    };
    window.addEventListener("keydown", handleLoopShortcut);
    return () => window.removeEventListener("keydown", handleLoopShortcut);
  }, [
    activeSession,
    headerIsLoop,
    headerIsSpawned,
    headerIsArchived,
    handleMakeLoop,
    handleMakeNonLoop,
  ]);

  // Only the active conversation can have queued messages; streaming state comes
  // from the live socket. Both block archiving (matches SessionItem logic).
  // Protected-conversation flag (mitto-yvel.4): suppresses the archive
  // direction only — unarchive stays available (mitto-a5p direction-aware shape).
  const headerHasQueued = queueLength > 0;
  const headerIsProtected = !!(
    activeSession?.no_archive || sessionInfo?.no_archive
  );
  const headerCanArchive =
    headerIsArchived ||
    (!headerIsProtected && !headerHasQueued && !isStreaming);
  const headerArchiveBlockedReason = !headerIsArchived
    ? headerIsProtected
      ? "This conversation is protected from archiving"
      : headerHasQueued
        ? "Clear queue before archiving"
        : isStreaming
          ? "Wait for response to complete"
          : null
    : null;
  const headerWorkingDir =
    activeSession?.working_dir || sessionInfo?.working_dir || "";

  // Header subtitle: ACP server name (always) plus, for loop conversations, a
  // live countdown + next scheduled run time. The loop fields live on the
  // stored session object (GET /api/sessions + loop_updated broadcasts carry
  // next_scheduled_at + frequency; the per-session "connected" message does not).
  const headerAcpServer =
    sessionInfo?.acp_server || activeSession?.acp_server || "";
  const headerNextScheduledAt =
    (activeSession?.loop_configured && activeSession?.next_scheduled_at) ||
    null;
  const headerLoopUnit = activeSession?.loop_frequency?.unit || "hours";
  // Derive a single 3-state pill for the loop status: running | paused | stopped | null.
  // null means not loop (no pill rendered).
  const headerLoopState = (() => {
    if (!activeSession?.loop_configured) return null;
    if (activeSession?.loop_enabled) {
      return {
        state: "running",
        label: "Auto",
        badgeClass: "badge-success badge-soft",
      };
    }
    // Loop is disabled — check the reason for stopped vs paused distinction
    const entry = LOOP_STOPPED_LABELS[activeSession?.loop_stopped_reason];
    if (entry && entry.kind === "stopped") {
      return {
        state: "stopped",
        label: entry.label,
        badgeClass: "badge-error badge-soft",
      };
    }
    if (entry && entry.kind === "paused") {
      return {
        state: "paused",
        label: entry.label,
        badgeClass: "badge-warning badge-soft",
      };
    }
    // No reason set — manual pause / unknown
    return {
      state: "paused",
      label: "Paused",
      badgeClass: "badge-warning badge-soft",
    };
  })();
  // Keep backwards-compat references used by cap-highlight logic below
  const headerStoppedReason =
    (activeSession?.loop_configured && activeSession?.loop_stopped_reason) ||
    null;

  // Loop "glance" badges shown in the subtitle for ALL loop sessions
  // (running or stopped, schedule or onCompletion).
  const headerLoopTrigger = activeSession?.loop_trigger || null;
  // mitto-r6j: canonical armed-triggers list. Falls back to the scalar
  // wrapped in a single-entry array for backward-compat with older sessions.
  const headerLoopTriggers = Array.isArray(activeSession?.loop_triggers)
    ? activeSession.loop_triggers
    : headerLoopTrigger
      ? [headerLoopTrigger]
      : null;
  const headerIterationCount = activeSession?.loop_iteration_count ?? 0;
  const headerMaxIterations = activeSession?.loop_max_iterations ?? 0;
  const headerDelaySeconds = activeSession?.loop_delay_seconds ?? 0;
  const headerMaxDurationSecs = activeSession?.loop_max_duration_seconds ?? 0;

  // Trigger badge: "every 2h" for schedule, "after agent finishes [· +Ns]" for
  // onCompletion, "on task changes" for onTasks (mitto-oja.4). Multi-trigger
  // loops render "<primary label> +N" where N is the extra-trigger count
  // (mitto-r6j).
  const headerTriggerLabel = activeSession?.loop_configured
    ? computeHeaderTriggerLabel(
        headerLoopTriggers ?? headerLoopTrigger,
        headerDelaySeconds,
        activeSession?.loop_frequency,
      )
    : null;
  // Run-count badge: "Run N of M" or "N run(s) · ∞". A compact variant ("N/M" or
  // "N·∞") is rendered alongside and CSS-swapped in on narrow screens (styles.css).
  const headerRunCountLabel = activeSession?.loop_configured
    ? headerMaxIterations > 0
      ? `Run ${headerIterationCount} of ${headerMaxIterations}`
      : `${headerIterationCount} run${headerIterationCount !== 1 ? "s" : ""} · ∞`
    : null;
  const headerRunCountLabelShort = activeSession?.loop_configured
    ? headerMaxIterations > 0
      ? `${headerIterationCount}/${headerMaxIterations}`
      : `${headerIterationCount}·∞`
    : null;
  // Max-time badge: "max 2h" etc; omitted when not set (0 means unlimited)
  const headerMaxTimeLabel =
    activeSession?.loop_configured && headerMaxDurationSecs > 0
      ? `max ${formatLoopMaxDuration(headerMaxDurationSecs)}`
      : null;
  // When a loop loop is auto-stopped by a cap, soft-red highlight the
  // specific cap badge that was exceeded (and the Stopped badge) so the user
  // can see at a glance which limit was hit.
  const headerIterCapHit =
    headerStoppedReason === "maxIterations" ||
    headerStoppedReason === "iterationSafeguard";
  const headerTimeCapHit = headerStoppedReason === "maxDuration";
  const headerRunCountBadgeClass = headerIterCapHit
    ? "badge-error badge-soft"
    : "badge-ghost";
  const headerMaxTimeBadgeClass = headerTimeCapHit
    ? "badge-error badge-soft"
    : "badge-ghost";

  // Shared "copy text, then toast the outcome" helper backing all four Copy
  // dropdown entries (mitto-a6v1). Kept as a single useCallback so each entry
  // handler below is a thin wrapper with its own text source + toast titles.
  const copyWithToast = useCallback(
    async (text, okTitle, failTitle) => {
      const ok = await copyToClipboard(text);
      showToast({
        style: ok ? "success" : "error",
        title: ok ? okTitle : failTitle,
        duration: ok ? 3000 : 4000,
      });
    },
    [showToast],
  );

  // Full-conversation Markdown, memoized so both the copy handler and the
  // dropdown's disabled state (no copyable messages yet) share one computation.
  const headerConversationMarkdown = useMemo(
    () => conversationToMarkdown(messages),
    [messages],
  );

  const handleCopyConversation = useCallback(async () => {
    await copyWithToast(
      headerConversationMarkdown,
      "Conversation copied as Markdown",
      "Failed to copy conversation",
    );
  }, [headerConversationMarkdown, copyWithToast]);

  const handleCopyConversationName = useCallback(async () => {
    await copyWithToast(
      sessionInfo?.name || "",
      "Conversation name copied",
      "Failed to copy conversation name",
    );
  }, [sessionInfo?.name, copyWithToast]);

  const handleCopyConversationId = useCallback(async () => {
    await copyWithToast(
      activeSessionId || "",
      "Conversation ID copied",
      "Failed to copy conversation ID",
    );
  }, [activeSessionId, copyWithToast]);

  // Last agent (assistant) message in the conversation, rendered to Markdown.
  // Empty string when there is no copyable agent message yet — used to
  // disable the "Copy last response" entry rather than hide it.
  const headerLastAgentMarkdown = useMemo(
    () => lastAgentMarkdown(messages),
    [messages],
  );

  const handleCopyLastResponse = useCallback(async () => {
    await copyWithToast(
      headerLastAgentMarkdown,
      "Last response copied as Markdown",
      "Failed to copy last response",
    );
  }, [headerLastAgentMarkdown, copyWithToast]);

  // Flush the agent's conversation context by sending the configured
  // context-flush command (e.g. "/clear") to the active conversation. The
  // backend resolves the command per ACP server; the menu item is only shown
  // when one is configured (see flushCommand below).
  const handleFlushContext = useCallback(
    async (session) => {
      const sessionId = session?.session_id || activeSessionId;
      if (!sessionId) return;
      try {
        await getSdkClient().sessions.flush(sessionId);
        showToast({
          style: "success",
          title: "Flushing conversation context\u2026",
          duration: 3000,
        });
      } catch (err) {
        console.error("Failed to flush context:", err);
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to flush context"),
          duration: 4000,
        });
      }
    },
    [activeSessionId, showToast],
  );

  const {
    contextMenu: headerMenu,
    promptGroupItems: headerPromptGroupItems,
    closeContextMenu: closeHeaderMenu,
    handleMenuButtonClick: handleHeaderMenuButtonClick,
  } = useConversationMenu({
    session: activeSession,
    workingDir: headerWorkingDir,
    isArchived: headerIsArchived,
    isLoopConfigured: headerIsLoop,
    isSpawned: headerIsSpawned,
    canArchive: headerCanArchive,
    archiveBlockedReason: headerArchiveBlockedReason,
    onRename: handleOpenSessionProperties,
    onDelete: handleDeleteSession,
    onArchive: handleArchiveSession,
    onMakeLoop: handleMakeLoop,
    onMakeNonLoop: handleMakeNonLoop,
    onFetchConversationPrompts: fetchConversationPromptsForSession,
    onSendPromptToConversation: handleSendPromptToConversation,
    onSetColor: handleSetSessionColor,
    onCopyConversation: activeSessionId ? handleCopyConversation : undefined,
    onCopyConversationName: activeSessionId
      ? handleCopyConversationName
      : undefined,
    onCopyConversationId: activeSessionId
      ? handleCopyConversationId
      : undefined,
    onCopyLastResponse: activeSessionId ? handleCopyLastResponse : undefined,
    hasConversationMarkdown: !!headerConversationMarkdown,
    hasLastResponseMarkdown: !!headerLastAgentMarkdown,
    flushCommand: sessionInfo?.context_flush_command || "",
    onFlushContext: activeSessionId ? handleFlushContext : undefined,
  });

  // Conversation toolbar items (rendered as a portable Toolbar pill below the
  // title header). The actions that used to live inside the "…" conversation
  // menu are now promoted to individual buttons (Copy, Flush, loop toggle,
  // Archive, Delete), each gated exactly like its former menu entry. The
  // hierarchical prompt groups (menus:conversation prompts) stay behind a
  // single dropdown button that opens the shared ContextMenu (lazy-loaded).
  // "Properties" is intentionally omitted — the Session-details side-panel
  // toggle already covers it. The toggle carries an active state while the
  // panel is open.
  const conversationHasFlush = !!(
    sessionInfo && sessionInfo.context_flush_command
  );
  // Linked beads issue for the right-aligned toolbar button. The button is
  // disabled when the conversation has no linked issue; the badge dot color
  // reflects the fetched status (see headerBeadsStatus above). Dot colors use
  // vivid 500-level tokens confirmed present in the precompiled tailwind.css.
  const headerBeadsIssue = sessionInfo?.beads_issue || "";
  const headerBeadsDotColor =
    {
      open: "bg-green-500",
      in_progress: "bg-blue-500",
      blocked: "bg-red-500",
      deferred: "bg-cyan-800",
      closed: "bg-slate-400",
    }[headerBeadsStatus] || "bg-slate-400";

  // Per-folder shortcut buttons configured for this folder's `conversations`
  // section (mirrors the `tasksList` shortcuts in BeadsView.js). Each button
  // runs a `prompts`/`conversation`-menu prompt in the active conversation. A
  // missing/renamed linked prompt renders disabled rather than erroring.
  const [convShortcuts, setConvShortcuts] = useState([]);
  const [convShortcutPromptMap, setConvShortcutPromptMap] = useState(new Map());

  const loadConvShortcuts = useCallback(
    async (isStale) => {
      const wd = sessionInfo?.working_dir;
      const sess = activeSession;
      if (!wd || !activeSessionId) {
        setConvShortcuts([]);
        setConvShortcutPromptMap(new Map());
        return;
      }
      try {
        // Merge global + folder shortcuts for the conversations section. Global
        // buttons come first; folder buttons duplicating a global prompt drop out.
        const [data, globalData] = await Promise.all([
          getSdkClient().shortcuts.getFolder({ working_dir: wd }),
          getSdkClient()
            .shortcuts.getGlobal()
            .catch(() => ({})),
        ]);
        const globalList = globalData?.sections?.conversations || [];
        const folderList = data?.sections?.conversations || [];
        const globalNames = new Set(globalList.map((s) => s.prompt));
        const list = [
          ...globalList,
          ...folderList.filter((s) => !globalNames.has(s.prompt)),
        ];
        if (isStale && isStale()) return;
        setConvShortcuts(list);
        if (list.length > 0) {
          // Resolve against the union of the `prompts` (ChatInput dropup) and
          // `conversation` menus so buttons configured from either list run.
          const prompts = await fetchConversationPromptsForSession(sess, wd, [
            "prompts",
            "conversation",
          ]);
          if (isStale && isStale()) return;
          const map = new Map((prompts || []).map((p) => [p.name, p]));
          setConvShortcutPromptMap(map);
        } else {
          setConvShortcutPromptMap(new Map());
        }
      } catch (_err) {
        if (isStale && isStale()) return;
        setConvShortcuts([]);
        setConvShortcutPromptMap(new Map());
      }
    },
    [
      sessionInfo?.working_dir,
      activeSessionId,
      activeSession,
      fetchConversationPromptsForSession,
    ],
  );

  // Initial load (and reload on folder/conversation switch), with
  // stale-fetch cancellation.
  useEffect(() => {
    let cancelled = false;
    loadConvShortcuts(() => cancelled);
    return () => {
      cancelled = true;
    };
  }, [loadConvShortcuts]);

  // Refresh shortcut buttons immediately when the Workspaces dialog saves new
  // shortcuts for this folder, so no page reload is needed.
  useEffect(() => {
    const handler = (e) => {
      const dir = e?.detail?.working_dir;
      if (!dir || dir === sessionInfo?.working_dir) loadConvShortcuts();
    };
    // Global shortcuts changes affect every folder, so always refresh.
    const globalHandler = () => loadConvShortcuts();
    window.addEventListener("mitto:folder_shortcuts_updated", handler);
    window.addEventListener("mitto:global_shortcuts_updated", globalHandler);
    return () => {
      window.removeEventListener("mitto:folder_shortcuts_updated", handler);
      window.removeEventListener(
        "mitto:global_shortcuts_updated",
        globalHandler,
      );
    };
  }, [loadConvShortcuts, sessionInfo?.working_dir]);

  // Shortcut items for the conversation toolbar (mirrors shortcutItems in
  // BeadsView.js). Each button runs its linked prompt in the active
  // conversation via handleSendPromptToConversation.
  const convShortcutItems = convShortcuts.map((sc, i) => {
    const prompt = convShortcutPromptMap.get(sc.prompt);
    const found = !!prompt;
    const Icon = getPromptIconOrDefault(sc.icon || (prompt && prompt.icon));
    return {
      kind: "button",
      testId: `conversation-shortcut-btn-${i}`,
      // On phone-width screens only the first shortcut is shown; the rest are
      // hidden (see .mitto-shortcut-extra in styles-v2.css) to avoid overflow.
      className: i > 0 ? "mitto-shortcut-extra" : undefined,
      icon: html`<${Icon} className="w-4 h-4" />`,
      tip: found ? sc.prompt : `Prompt "${sc.prompt}" not found`,
      ariaLabel: found
        ? `Run "${sc.prompt}"`
        : `Prompt "${sc.prompt}" not found`,
      disabled: !found,
      onClick: () =>
        found && handleSendPromptToConversation(activeSession, prompt),
    };
  });

  const conversationToolbarItems = [
    ...(activeSessionId
      ? [
          {
            kind: "button",
            testId: "header-conversation-prompts",
            icon: html`<${LightningIcon} className="w-4 h-4" />`,
            tip: "Conversation prompts",
            ariaLabel: "Conversation prompts",
            active: !!headerMenu,
            onClick: handleHeaderMenuButtonClick,
          },
          {
            kind: "dropdown",
            testId: "header-copy-markdown",
            icon: html`<${CopyIcon} className="w-4 h-4" />`,
            tip: "Copy",
            ariaLabel: "Copy",
            caret: true,
            // mitto-nmiv: NOT "end" — this trigger sits close to the sidebar's
            // right edge, so a dropdown-end (right-aligned) menu extends left
            // past the sidebar and is clipped by the main-content column's
            // overflow:hidden (clipping, not a z-index/paint-order issue — see
            // the bead's Investigation comment). Default alignment extends the
            // w-64 menu to the right instead, staying inside the column.
            open: copyMenuOpen,
            onToggle: setCopyMenuOpen,
            closeOnOutsideClick: true,
            menu: html`
              <ul
                class="dropdown-content menu menu-sm bg-mitto-surface-2 rounded-box z-10 mt-1 w-64 p-2 shadow border border-mitto-border-1"
                data-testid="header-copy-markdown-menu"
              >
                <li class=${!sessionInfo?.name ? "menu-disabled" : ""}>
                  <button
                    type="button"
                    data-testid="header-copy-name"
                    disabled=${!sessionInfo?.name}
                    onClick=${() => {
                      setCopyMenuOpen(false);
                      handleCopyConversationName();
                    }}
                  >
                    <span class="flex-1">Copy conversation name</span>
                  </button>
                </li>
                <li class=${!activeSessionId ? "menu-disabled" : ""}>
                  <button
                    type="button"
                    data-testid="header-copy-id"
                    disabled=${!activeSessionId}
                    onClick=${() => {
                      setCopyMenuOpen(false);
                      handleCopyConversationId();
                    }}
                  >
                    <span class="flex-1">Copy conversation ID</span>
                  </button>
                </li>
                <li class=${!headerConversationMarkdown ? "menu-disabled" : ""}>
                  <button
                    type="button"
                    data-testid="header-copy-conversation-md"
                    disabled=${!headerConversationMarkdown}
                    onClick=${() => {
                      setCopyMenuOpen(false);
                      handleCopyConversation();
                    }}
                  >
                    <span class="flex-1">Copy full contents as Markdown</span>
                  </button>
                </li>
                <li class=${!headerLastAgentMarkdown ? "menu-disabled" : ""}>
                  <button
                    type="button"
                    data-testid="header-copy-last-response-md"
                    disabled=${!headerLastAgentMarkdown}
                    onClick=${() => {
                      setCopyMenuOpen(false);
                      handleCopyLastResponse();
                    }}
                  >
                    <span class="flex-1">Copy last response as Markdown</span>
                  </button>
                </li>
              </ul>
            `,
          },
          ...(conversationHasFlush
            ? [
                {
                  kind: "button",
                  testId: "header-flush-context",
                  icon: html`<${BroomIcon} className="w-4 h-4" />`,
                  tip: `Flush context (${sessionInfo.context_flush_command})`,
                  ariaLabel: "Flush context",
                  onClick: () => handleFlushContext(activeSession),
                },
              ]
            : []),
          ...(!headerIsLoop && !headerIsSpawned && !headerIsArchived
            ? [
                {
                  kind: "button",
                  testId: "header-make-loop",
                  icon: html`<${LoopIcon} className="w-4 h-4" />`,
                  tip: "Loop (⌘⇧L)",
                  ariaLabel: "Loop",
                  onClick: () => handleMakeLoop(activeSession),
                },
              ]
            : []),
          ...(headerIsLoop && !headerIsSpawned
            ? [
                {
                  kind: "button",
                  testId: "header-make-non-loop",
                  icon: html`<${LoopOffIcon} className="w-4 h-4" />`,
                  tip: "Unloop (⌘⇧L)",
                  ariaLabel: "Unloop",
                  onClick: () => handleMakeNonLoop(activeSession),
                },
              ]
            : []),
          // Per-folder configurable prompt shortcuts (conversations section).
          ...(convShortcuts.length > 0
            ? [{ kind: "separator" }, ...convShortcutItems]
            : []),
          // Separator before the destructive group (archive + delete), keeping
          // those two together but set apart from the actions above.
          { kind: "separator" },
          ...(headerIsSpawned
            ? []
            : [
                {
                  kind: "button",
                  testId: "header-archive",
                  icon: headerIsArchived
                    ? html`<${ArchiveFilledIcon} className="w-4 h-4" />`
                    : html`<${ArchiveIcon} className="w-4 h-4" />`,
                  tip: !headerCanArchive
                    ? headerArchiveBlockedReason
                    : headerIsArchived
                      ? "Unarchive"
                      : "Archive",
                  ariaLabel: headerIsArchived ? "Unarchive" : "Archive",
                  disabled: !headerCanArchive,
                  onClick: () =>
                    handleArchiveSession(activeSession, !headerIsArchived),
                },
              ]),
          {
            kind: "button",
            testId: "header-delete",
            icon: html`<${TrashIcon} className="w-4 h-4" />`,
            tip: "Delete",
            ariaLabel: "Delete",
            danger: true,
            onClick: () => handleDeleteSession(activeSession),
          },
        ]
      : []),
    // Spacer pushes the right-aligned controls to the far right of the pill.
    { kind: "spacer" },
    // Linked beads issue: opens the docked issue viewer for the conversation's
    // associated issue. Disabled when there is no linked issue; a status badge
    // dot appears once the (possibly slow) status fetch resolves. Sits just to
    // the left of the Session-details toggle.
    // Children dropdown (mitto-7vpp): quick jump to any child conversation of
    // the active session. Hidden entirely on mobile (< md) and when the active
    // conversation has no children — keeps the header uncluttered. Live-updates
    // via allSessions (WebSocket push) without any extra subscription.
    ...(activeSessionId && activeChildren.length > 0
      ? [
          {
            kind: "dropdown",
            testId: "header-children-dropdown",
            // Wrap the icon in a daisyUI `indicator` so the child count sits as
            // a small badge on the top-right corner of the LayersIcon — same
            // visual language as unread/count badges elsewhere in the app.
            icon: html`<span class="indicator">
              <span
                class="indicator-item badge badge-xs badge-primary tabular-nums"
                data-testid="header-children-count"
                >${activeChildren.length}</span
              >
              <${LayersIcon} className="w-4 h-4" />
            </span>`,
            tip: `Children (${activeChildren.length})`,
            ariaLabel: `Children of this conversation (${activeChildren.length})`,
            // Portal placement escapes the clipped header/sidebar ancestors;
            // end is only a preference because Toolbar clamps every viewport
            // edge after measuring the rendered menu.
            align: "end",
            portal: true,
            className: "hidden md:block",
            open: childrenMenuOpen,
            onToggle: setChildrenMenuOpen,
            closeOnOutsideClick: true,
            menu: html`
              <ul
                class="dropdown-content menu bg-mitto-surface-2 rounded-box shadow border border-mitto-border-1 z-10 mt-1 w-64 max-h-96 overflow-y-auto p-1"
                data-testid="header-children-dropdown-menu"
              >
                ${activeChildren.map(
                  (child) =>
                    html`<${HeaderChildRow}
                      key=${child.session_id}
                      child=${child}
                      onSelect=${(sid) => {
                        setChildrenMenuOpen(false);
                        focusSession(sid);
                      }}
                    />`,
                )}
              </ul>
            `,
          },
        ]
      : []),
    ...(activeSessionId
      ? [
          {
            kind: "button",
            testId: "header-linked-issue",
            icon: html`<span class="relative inline-flex">
              <${BeadsIcon} className="w-4 h-4" />
              ${headerBeadsIssue && headerBeadsStatus
                ? html`<span
                    class="absolute -top-1 -right-1 w-2 h-2 rounded-full ${headerBeadsDotColor} border border-mitto-border-1"
                  ></span>`
                : null}
            </span>`,
            tip: headerBeadsIssue
              ? `Linked issue ${headerBeadsIssue}${
                  headerBeadsStatus
                    ? ` (${headerBeadsStatus.replace(/_/g, " ")})`
                    : ""
                }`
              : "No linked issue",
            ariaLabel: headerBeadsIssue
              ? `Open linked issue ${headerBeadsIssue}`
              : "No linked issue",
            disabled: !headerBeadsIssue,
            onClick: () =>
              handleOpenBeadsIssue(
                headerBeadsIssue,
                sessionInfo?.working_dir || "",
                activeSessionId,
                { reopenProperties: false },
              ),
          },
        ]
      : []),
    {
      kind: "button",
      testId: "header-session-details",
      icon: html`<${SidePanelIcon} className="w-4 h-4" />`,
      tip: "Session details",
      ariaLabel: "Session details",
      active: showSidePanel,
      onClick: handleToggleSidePanel,
    },
  ];

  return html`
    <div class="drawer md:drawer-open h-screen-safe sidebar-shell">
      <!-- Drawer toggle: Preact-controlled via showSidebar (mobile) + md:drawer-open (desktop) -->
      <input
        type="checkbox"
        id="sidebar-drawer"
        class="drawer-toggle"
        checked=${showSidebar}
        onChange=${(e) => setShowSidebar(e.target.checked)}
        tabindex=${-1}
        aria-hidden="true"
      />
      <!-- drawer-content: ALL page content (header, messages, input, dialogs).
           position:relative so the dock-mode SessionPanel (an absolutely
           positioned right-edge overlay) is confined to this content area
           (right of the sidebar) rather than the whole viewport. -->
      <div class="drawer-content flex flex-col h-full relative">
        <!-- Delete Dialog -->
        <${DeleteDialog}
          isOpen=${deleteDialog.isOpen}
          sessionName=${deleteDialog.session?.name ||
          deleteDialog.session?.description ||
          "Untitled"}
          isActive=${deleteDialog.session?.session_id === activeSessionId}
          isStreaming=${deleteDialog.session?.isStreaming || false}
          onConfirm=${handleConfirmDelete}
          onCancel=${() => setDeleteDialog({ isOpen: false, session: null })}
        />

        <!-- Workspace Selection Dialog (for new conversations) -->
        <${NewSessionWorkspaceDialog}
          isOpen=${workspaceDialog.isOpen}
          workspaces=${workspaceDialog.filteredWorkspaces || workspaces}
          onSelect=${handleWorkspaceSelect}
          onCancel=${() => setWorkspaceDialog({ isOpen: false })}
          onCreateWorkspace=${configReadonly
            ? null
            : () => {
                setWorkspaceDialog({ isOpen: false });
                handleShowWorkspaces();
              }}
        />

        <!-- Agent Discovery Dialog (first-run when no ACP servers configured) -->
        <${AgentDiscoveryDialog}
          isOpen=${showAgentDiscovery}
          onClose=${async () => {
            setShowAgentDiscovery(false);
            // Check if ACP servers exist but no workspaces → open workspaces dialog
            try {
              invalidateConfigCache();
              const config = await fetchConfig();
              const hasServers =
                config?.acp_servers && config.acp_servers.length > 0;
              const noWorkspaces =
                !config?.workspaces || config.workspaces.length === 0;
              if (hasServers && noWorkspaces) {
                setWorkspacesDialog({ isOpen: true });
                return;
              }
            } catch (err) {
              console.error(
                "[AgentDiscovery] Failed to check config on close:",
                err,
              );
            }
            // Fall through to settings dialog so user can configure manually
            setSettingsDialog({ isOpen: true, forceOpen: true });
          }}
          onAgentsConfirmed=${async () => {
            setShowAgentDiscovery(false);
            // Refresh config to pick up newly added servers
            invalidateConfigCache();
            try {
              const config = await fetchConfig();
              if (config) {
                refreshWorkspaces();
                // If ACP servers exist but no workspaces, open workspaces dialog
                const hasServers =
                  config.acp_servers && config.acp_servers.length > 0;
                const noWorkspaces =
                  !config.workspaces || config.workspaces.length === 0;
                if (hasServers && noWorkspaces) {
                  setWorkspacesDialog({ isOpen: true });
                }
              }
            } catch (err) {
              console.error("[AgentDiscovery] Failed to refresh config:", err);
            }
          }}
        />

        <!-- Settings Dialog -->
        <${SettingsDialog}
          isOpen=${settingsDialog.isOpen}
          forceOpen=${settingsDialog.forceOpen}
          initialTab=${settingsDialog.initialTab}
          onClose=${() =>
            setSettingsDialog({
              isOpen: false,
              forceOpen: false,
              initialTab: null,
            })}
          showToast=${showToast}
          onSave=${async () => {
            // Refresh workspaces after saving
            refreshWorkspaces();
            // Reload config to update prompts and UI settings (invalidate cache first)
            invalidateConfigCache();
            try {
              const config = await fetchConfig();
              if (config) {
                // Reload global model profiles for PromptsMenu chips.
                setModelProfiles(
                  Array.isArray(config?.models) ? config.models : [],
                );
                // Reload UI settings
                setDeleteConfirmMode(
                  config?.ui?.confirmations?.delete_conversation || "always",
                );
                // Reload input font family setting
                setInputFontFamily(
                  config?.ui?.web?.input_font_family || "system",
                );
                // Reload input font size setting
                setInputFontSize(config?.ui?.web?.input_font_size || "default");
                // Reload conversation font family setting
                setConversationFontFamily(
                  config?.ui?.web?.conversation_font_family || "system",
                );
                // Reload conversation base font size setting
                setConversationFontSize(
                  config?.ui?.web?.conversation_font_size || "sm",
                );
                // Reload send key mode setting
                setSendKeyMode(config?.ui?.web?.send_key_mode || "enter");
                // Reload conversation cycling mode setting
                setConversationCyclingMode(
                  config?.ui?.web?.conversation_cycling_mode ||
                    CYCLING_MODE.ALL,
                );
                // Reload accordion mode setting for groups
                setSingleExpandedGroupMode(
                  config?.ui?.web?.single_expanded_group === true,
                );
              }
            } catch (err) {
              console.error("Failed to reload config after save:", err);
            }
          }}
        />

        <!-- Workspaces Dialog -->
        <${WorkspacesDialog}
          isOpen=${workspacesDialog.isOpen}
          initialWorkingDir=${workspacesDialog.workingDir || null}
          initialTab=${workspacesDialog.tab || null}
          onClose=${() => setWorkspacesDialog({ isOpen: false })}
          showToast=${showToast}
          onSave=${async () => {
            refreshWorkspaces();
            invalidateConfigCache();
          }}
          onOpenPromptParamDialog=${(prompt, parameters, onSubmit, opts = {}) =>
            setPromptParamDialog({
              prompt,
              parameters,
              onSubmit,
              workingDir: opts.workingDir,
              initialValues: opts.initialValues,
              hostSessionId: opts.hostSessionId,
            })}
        />

        <!-- Add Folder Dialog -->
        <${AddFolderDialog}
          isOpen=${addFolderDialogOpen}
          onClose=${handleAddFolderClose}
          hiddenWorkspaces=${hiddenWorkspaces}
          onPinExisting=${handlePinExistingFolder}
          onCreateNew=${() => {
            setAddFolderDialogOpen(false);
            handleShowWorkspaces();
          }}
        />

        <!-- Keyboard Shortcuts Dialog -->
        <${KeyboardShortcutsDialog}
          isOpen=${keyboardShortcutsDialog.isOpen}
          onClose=${() => setKeyboardShortcutsDialog({ isOpen: false })}
        />

        <!-- Loop Schedule Dialog: opened when a loop-declaring prompt is selected -->
        <${LoopScheduleDialog}
          isOpen=${loopScheduleDialog !== null}
          prompt=${loopScheduleDialog?.prompt}
          onConfirm=${(schedule) => {
            const { onSchedule } = loopScheduleDialog || {};
            setLoopScheduleDialog(null);
            onSchedule?.(schedule);
          }}
          onCancel=${() => setLoopScheduleDialog(null)}
        />

        <!-- Prompt Parameter Dialog: opened when a menu (beads, conversation, or
           the ChatInput dropup) has prompt params it cannot auto-fill. The
           conversation menu sets hostSessionId to the right-clicked conversation
           so a childSessionId picker is scoped to its children; other surfaces
           fall back to the active session. workingDir prefers the dir the opener
           passed explicitly, then the beads view's dir but only while a beads
           surface is actually on screen (beadsWorkingDir is sticky and would
           otherwise leak a previously-visited folder into a conversation-opened
           dialog), and finally the active conversation's working dir. -->
        <${PromptParameterDialog}
          isOpen=${promptParamDialog !== null}
          parameters=${promptParamDialog?.parameters || []}
          workingDir=${promptParamDialog?.workingDir ||
          ((mainView === "beads" || beadsIssueOpen) && beadsWorkingDir) ||
          headerWorkingDir}
          hostSessionId=${promptParamDialog?.hostSessionId ?? activeSessionId}
          title=${promptParamDialog?.prompt?.name || "Prompt parameters"}
          initialValues=${promptParamDialog?.initialValues || {}}
          onClose=${() => setPromptParamDialog(null)}
          onSubmit=${(args) => {
            promptParamDialog?.onSubmit?.(args);
            setPromptParamDialog(null);
          }}
        />

        <!-- Unified toast container -->
        <${ToastContainer} toasts=${toasts} onDismiss=${dismissToast} />

        <!-- Main content area: dashboard, beads view, or conversation -->
        ${mainView === "dashboard"
          ? html`<${Dashboard}
              allSessions=${allSessions}
              showToast=${showToast}
              onFocusConversation=${focusSession}
              onOpenTask=${(issueId, workingDir) =>
                handleOpenBeadsIssue(issueId, workingDir, activeSessionId)}
              onShowSidebar=${() => setShowSidebar(true)}
            />`
          : mainView === "beads" && beadsWorkingDir
            ? html`
                <div
                  class="flex-1 flex flex-col min-w-0 overflow-hidden bg-mitto-bg"
                >
                  <${BeadsView}
                    workingDir=${beadsWorkingDir}
                    onClose=${() => setMainView("conversation")}
                    showToast=${showToast}
                    dismissToast=${dismissToast}
                    onFetchBeadsPrompts=${fetchBeadsPromptsForWorkspace}
                    onRunBeadsPrompt=${handleRunBeadsPrompt}
                    onFetchBeadsListPrompts=${fetchBeadsListPromptsForWorkspace}
                    onRunBeadsListPrompt=${handleRunBeadsListPrompt}
                    onShowSidebar=${() => setShowSidebar(true)}
                    onOpenConfig=${window.mittoIsExternal === true
                      ? undefined
                      : () =>
                          handleShowWorkspacesForFolder(
                            beadsWorkingDir,
                            "beads",
                          )}
                    issueSessionMap=${beadsIssueSessionMap}
                    issueStreamingSet=${beadsIssueStreamingSet}
                    onOpenConversation=${handleSelectSession}
                    onLaunchPrompt=${handleBeadsLaunchPrompt}
                    initialCreateNonce=${beadsCreateNonce}
                    initialRefreshNonce=${beadsRefreshNonce}
                    initialCleanupNonce=${beadsCleanupNonce}
                  />
                </div>
              `
            : html`
                <div
                  ref=${mainContentRef}
                  class="flex-1 flex flex-col min-w-0 overflow-hidden"
                >
                  <!-- Header -->
                  <div
                    class="relative pt-4 px-4 pb-2 bg-mitto-sidebar flex items-center gap-3 shrink-0"
                  >
                    <${Tooltip}
                      tip="Show conversations"
                      placement="bottom"
                      className="md:hidden"
                    >
                      <button
                        class="p-2 hover:bg-mitto-surface-hover rounded-lg transition-colors"
                        onClick=${() => setShowSidebar(true)}
                        aria-label="Show conversations"
                      >
                        <${MenuIcon} className="w-6 h-6" />
                      </button>
                    <//>
                    <div class="flex-1 min-w-0 flex flex-col justify-center">
                      ${isEditingHeaderTitle
                        ? html`<input
                            ref=${headerTitleInputRef}
                            type="text"
                            class="input input-sm font-bold text-xl w-full min-w-0"
                            value=${editedHeaderTitle}
                            onInput=${(e) =>
                              setEditedHeaderTitle(e.target.value)}
                            onKeyDown=${handleHeaderTitleKeyDown}
                            onBlur=${() => {
                              // Delay so a Save-button click (if ever added)
                              // wouldn't be swallowed by the blur; matches the
                              // side panel's guard even though the header has
                              // no separate Save control today.
                              setTimeout(() => {
                                if (
                                  isEditingHeaderTitle &&
                                  !isSavingHeaderTitle
                                ) {
                                  setIsEditingHeaderTitle(false);
                                }
                              }, 150);
                            }}
                            disabled=${isSavingHeaderTitle}
                            aria-label="Conversation title"
                          />`
                        : html`<h1
                            class="font-bold text-xl truncate no-underline tooltip tooltip-bottom ${!activeSessionId
                              ? "text-mitto-text-muted"
                              : connected
                                ? ""
                                : "text-mitto-text-muted"} ${activeSessionId
                              ? "cursor-pointer hover:text-mitto-accent transition-colors"
                              : ""}"
                            data-tip=${activeSessionId
                              ? sessionInfo?.name || "New conversation"
                              : ""}
                            aria-label=${activeSessionId
                              ? sessionInfo?.name || "New conversation"
                              : ""}
                            onClick=${activeSessionId
                              ? handleStartEditHeaderTitle
                              : undefined}
                          >
                            ${activeSessionId
                              ? sessionInfo?.name || "New conversation"
                              : "No Active Session"}
                          </h1>`}
                      ${activeSessionId &&
                      (headerAcpServer ||
                        headerNextScheduledAt ||
                        headerLoopState ||
                        activeSession?.loop_configured) &&
                      html`<div
                        class="text-xs text-mitto-text-muted truncate flex items-center gap-2 min-w-0"
                        data-testid="conversation-header-subtitle"
                      >
                        ${headerLoopState &&
                        html`<span
                          class="badge badge-sm ${headerLoopState.badgeClass} whitespace-nowrap inline-flex items-center gap-1"
                          data-testid="loop-status-pill"
                          title=${headerLoopState.state === "running"
                            ? "Loop loop is iterating"
                            : (activeSession?.loop_stopped_reason || "") +
                              (activeSession?.stopped_at
                                ? " · " +
                                  new Date(
                                    activeSession.stopped_at,
                                  ).toLocaleString()
                                : "")}
                          >${headerLoopState.state === "running"
                            ? html`<${LoopIcon} className="w-3 h-3" />`
                            : headerLoopState.state === "stopped"
                              ? html`<${StopIcon} className="w-3 h-3" />`
                              : html`<${PauseFilledIcon}
                                  className="w-3 h-3"
                                />`}<span class="badge-collapse-label"
                            >${headerLoopState.label}</span
                          ></span
                        >`}
                        ${headerAcpServer &&
                        html`<span class="truncate min-w-0"
                          >${headerAcpServer}</span
                        >`}
                        ${headerTriggerLabel &&
                        (() => {
                          // Icon reflects the PRIMARY (first) armed trigger;
                          // the badge label already carries "+N" when extra
                          // triggers are armed (mitto-r6j).
                          const primary =
                            (headerLoopTriggers && headerLoopTriggers[0]) ||
                            headerLoopTrigger;
                          const iconMarkup =
                            primary === "onCompletion"
                              ? html`<${CheckIcon} className="w-3 h-3" />`
                              : primary === "onTasks"
                                ? html`<${BeadsIcon} className="w-3 h-3" />`
                                : html`<${ClockIcon} className="w-3 h-3" />`;
                          const fullList =
                            (headerLoopTriggers &&
                              headerLoopTriggers.join(", ")) ||
                            primary ||
                            "";
                          return html`<${Fragment}>
                <span class="opacity-60">·</span>
                <span
                  class="badge badge-sm badge-ghost whitespace-nowrap inline-flex items-center gap-1"
                  data-testid="loop-trigger-badge"
                  title=${fullList}
                >${iconMarkup}<span
                    class="badge-collapse-label"
                    >${headerTriggerLabel}</span
                  ></span>
              </${Fragment}>`;
                        })()}
                        ${headerRunCountLabel !== null &&
                        html`<${Fragment}>
                <span class="opacity-60">·</span>
                <span
                  class="badge badge-sm ${headerRunCountBadgeClass} whitespace-nowrap"
                  data-testid="loop-run-count-badge"
                  title=${
                    headerIterCapHit
                      ? "Reached the maximum number of iterations"
                      : null
                  }
                ><span class="runcount-full">${headerRunCountLabel}</span
                  ><span class="runcount-short">${headerRunCountLabelShort}</span
                ></span>
              </${Fragment}>`}
                        ${headerMaxTimeLabel &&
                        html`<${Fragment}>
                <span class="opacity-60">·</span>
                <span
                  class="badge badge-sm ${headerMaxTimeBadgeClass} whitespace-nowrap"
                  data-testid="loop-max-time-badge"
                  title=${
                    headerTimeCapHit ? "Reached the maximum run time" : null
                  }
                >${headerMaxTimeLabel}</span>
              </${Fragment}>`}
                        ${headerLoopState?.state === "running" &&
                        headerNextScheduledAt &&
                        html`<${Fragment}>
                ${
                  headerAcpServer ||
                  headerTriggerLabel ||
                  headerRunCountLabel !== null ||
                  headerMaxTimeLabel
                    ? html`<span class="opacity-60">·</span>`
                    : null
                }
                <${CountdownDisplay}
                  targetIso=${headerNextScheduledAt}
                  unit=${headerLoopUnit}
                  active=${true}
                  className="whitespace-nowrap"
                />
              </${Fragment}>`}
                      </div>`}
                    </div>
                  </div>

                  <!-- Conversation toolbar: the portable Toolbar pill, sitting
                       right below the title header and vertically aligned with
                       the sidebar toolbar (both live in a px-3 wrapper directly
                       under their p-4 header). Holds the actions that used to
                       sit top-right in the header: the "…" conversation-actions
                       menu and the Session-details panel toggle. -->
                  <div
                    class="px-3 pb-2 shrink-0"
                    data-testid="conversation-toolbar"
                  >
                    <${Toolbar}
                      variant="block"
                      surface="bg-mitto-surface-3"
                      ariaLabel="Conversation actions"
                      items=${conversationToolbarItems}
                    />
                  </div>
                  ${headerMenu &&
                  html`
                    <${ContextMenu}
                      x=${headerMenu.x}
                      y=${headerMenu.y}
                      items=${headerPromptGroupItems}
                      onClose=${closeHeaderMenu}
                    />
                  `}

                  <!-- Messages wrapper (for positioning scroll-to-bottom button and plan panel) -->
                  <div class="flex-1 relative min-h-0 overflow-hidden">
                    <!-- Agent Plan Panel (floating overlay at top) -->
                    <${AgentPlanPanel}
                      isOpen=${showPlanPanel}
                      onClose=${handleClosePlanPanel}
                      onToggle=${handleTogglePlanPanel}
                      entries=${planEntries}
                      userPinned=${planUserPinned}
                    />
                    <!-- Agent Plan Indicator (shown when panel is collapsed but has entries) -->
                    ${!showPlanPanel &&
                    planEntries.length > 0 &&
                    html`
                      <div
                        class="absolute top-2 left-1/2 transform -translate-x-1/2 z-10"
                      >
                        <${AgentPlanIndicator}
                          onClick=${handleTogglePlanPanel}
                          entries=${planEntries}
                        />
                      </div>
                    `}
                    <!-- Messages list (scrollable container + scroll-to-bottom button) -->
                    <${MessageList}
                      displayMessages=${displayMessages}
                      messages=${messages}
                      hasMoreMessages=${hasMoreMessages}
                      hasReachedLimit=${hasReachedLimit}
                      isLoadingMore=${isLoadingMore}
                      isStreaming=${isStreaming}
                      agentWorking=${agentWorking}
                      onLoadMore=${handleLoadMore}
                      onScrollToBottom=${scrollToBottom}
                      isUserAtBottom=${isUserAtBottom}
                      hasNewMessages=${hasNewMessages}
                      sentinelRef=${sentinelRef}
                      onRetry=${handleSendPrompt}
                      activeSessionId=${activeSessionId}
                      swipeDirection=${swipeDirection}
                      swipeArrow=${swipeArrow}
                      connected=${connected}
                      sessionInfo=${sessionInfo}
                      workspaces=${workspaces}
                      messagesContainerRef=${messagesContainerRef}
                      mcpInitState=${mcpInitState}
                      clearMCPInit=${clearMCPInit}
                    />
                  </div>
                  <!-- End of messages wrapper -->

                  <!-- Persistent MCP-unavailable banner (global; survives reconnects). -->
                  ${mcpStatus &&
                  mcpStatus.available === false &&
                  html`
                    <div class="flex justify-center my-2">
                      <div
                        role="alert"
                        class="alert alert-warning max-w-2xl text-sm py-2"
                      >
                        <span>
                          MCP server
                          unavailable${mcpStatus.reason === "port_in_use"
                            ? ` — port ${mcpStatus.port} is already in use (another Mitto instance may be running)`
                            : mcpStatus.port
                              ? ` (port ${mcpStatus.port})`
                              : ""}.
                          Mitto continues without MCP tools.
                        </span>
                      </div>
                    </div>
                  `}

                  <!-- ACP reconnecting banner (shown when ACP not ready and there are messages) -->
                  <!-- Only show when global WS is connected — during shutdown, WS disconnects and we don't want to show this -->
                  <!-- Skip for GC-suspended sessions — they are intentionally paused, not reconnecting -->
                  ${connected &&
                  activeSessionId &&
                  sessionInfo &&
                  !sessionInfo.acp_ready &&
                  !sessionInfo.archived &&
                  !sessionInfo.gc_suspended &&
                  messages.length > 0 &&
                  html`
                    <div class="flex items-center justify-center py-2 text-sm">
                      <span
                        class="skeleton skeleton-text skeleton-text-readable"
                        >Establishing ACP session...</span
                      >
                    </div>
                  `}

                  <!-- Archive reason banner (shown when conversation is archived and has a reason) -->
                  <!-- Uses the same balloon style as system messages for visual consistency -->
                  ${sessionInfo?.archived &&
                  sessionInfo?.archive_reason &&
                  html`
                    <div class="flex justify-center mb-3">
                      <div
                        class="text-xs text-mitto-text-muted bg-mitto-surface-2/50 px-3 py-1 rounded-full"
                      >
                        ${getArchiveReasonText(
                          sessionInfo.archive_reason,
                          sessionInfo.archived_at,
                        )}
                      </div>
                    </div>
                  `}

                  <!-- Input Area Container (relative for QueueDropdown positioning) -->
                  <div class="relative shrink-0">
                    <!-- Queue Dropdown (floating overlay above input) -->
                    <${QueueDropdown}
                      isOpen=${showQueueDropdown}
                      onClose=${handleCloseQueueDropdown}
                      messages=${queueMessages}
                      onDelete=${handleDeleteQueueMessage}
                      onMove=${handleMoveQueueMessage}
                      isDeleting=${isDeletingQueueMessage}
                      isMoving=${isMovingQueueMessage}
                      queueLength=${queueLength}
                      maxSize=${queueConfig.max_size}
                    />

                    <!-- Input -->
                    <${ChatInput}
                      onSend=${handleSendPrompt}
                      onCancel=${cancelPrompt}
                      disabled=${!connected || !activeSessionId}
                      isStreaming=${isStreaming}
                      isRunning=${isRunning}
                      isReadOnly=${sessionInfo?.isReadOnly}
                      isArchived=${sessionInfo?.archived || false}
                      predefinedPrompts=${predefinedPrompts}
                      inputRef=${chatInputRef}
                      noSession=${!activeSessionId}
                      sessionId=${activeSessionId}
                      draft=${currentDraft}
                      onDraftChange=${updateDraft}
                      sessionDraftsRef=${sessionDraftsRef}
                      onPromptsOpen=${handlePromptsOpen}
                      onConfigurePrompts=${!configReadonly &&
                      sessionInfo?.working_dir
                        ? () =>
                            handleShowWorkspacesForFolder(
                              sessionInfo.working_dir,
                              "prompts",
                            )
                        : undefined}
                      queueLength=${queueLength}
                      queueConfig=${queueConfig}
                      onAddToQueue=${handleAddToQueue}
                      onToggleQueue=${handleToggleQueueDropdown}
                      showQueueDropdown=${showQueueDropdown}
                      actionButtons=${actionButtons}
                      availableCommands=${availableCommands}
                      loopConfigured=${sessionInfo?.loop_configured || false}
                      onOpenLoopSettings=${() => handleOpenSidePanelTab("loop")}
                      onLoopPrompt=${(prompt, opts) =>
                        handleSendPromptToConversation(
                          activeSession,
                          prompt,
                          opts,
                        )}
                      onOpenPromptParamDialog=${(
                        prompt,
                        parameters,
                        onSubmit,
                        opts = {},
                      ) =>
                        setPromptParamDialog({
                          prompt,
                          parameters,
                          onSubmit,
                          workingDir: opts.workingDir,
                          initialValues: opts.initialValues,
                          hostSessionId: opts.hostSessionId,
                        })}
                      agentSupportsImages=${sessionInfo?.agent_supports_images ??
                      false}
                      acpReady=${connected && sessionInfo
                        ? (sessionInfo.acp_ready ?? true)
                        : true}
                      gcSuspended=${sessionInfo?.gc_suspended || false}
                      onResume=${() => ensureResumed(activeSessionId)}
                      activeUIPrompt=${activeUIPrompt}
                      onUIPromptAnswer=${(
                        requestId,
                        optionId,
                        label,
                        freeText,
                      ) =>
                        sendUIPromptAnswer(
                          activeSessionId,
                          requestId,
                          optionId,
                          label,
                          freeText,
                        )}
                      workingDir=${sessionInfo?.working_dir || ""}
                      sendKeyMode=${sendKeyMode}
                      configOptions=${configOptions}
                      onSetConfigOption=${setConfigOption}
                      modelProfiles=${modelProfiles}
                      contextUsage=${sessionInfo?.context_usage ?? null}
                      tokenUsage=${sessionInfo?.usage ?? null}
                      flushCommand=${sessionInfo?.context_flush_command || ""}
                      onFlushContext=${activeSessionId
                        ? () => handleFlushContext(activeSession)
                        : undefined}
                    />
                  </div>
                </div>
              `}

        <!-- Unified Session Panel: docks to the right edge of drawer-content as a
           confined overlay (Drawer dock mode + styles.css), so it does NOT
           reflow the conversation (messages keep full width); on phones it
           covers the whole view. Self-gates on showSidePanel; only relevant in
           conversation view. -->
        <${SessionPanel}
          isOpen=${showSidePanel}
          onClose=${handleCloseSidePanel}
          activeTab=${sidePanelTab}
          onTabChange=${setSidePanelTab}
          sessionId=${activeSessionId}
          sessionInfo=${sessionInfo}
          onRename=${renameSession}
          onOpenBeadsIssue=${handleOpenBeadsIssue}
          isStreaming=${isStreaming}
          configOptions=${configOptions}
          onSetConfigOption=${setConfigOption}
          mcpTools=${mcpTools}
          loopPrompts=${loopPrompts}
          allPrompts=${workspacePrompts}
          hasBeadsWorkspace=${hasBeadsWorkspace}
          messages=${messages}
          onOpenPromptParamDialog=${(prompt, parameters, onSubmit, opts = {}) =>
            setPromptParamDialog({
              prompt,
              parameters,
              onSubmit,
              workingDir: opts.workingDir,
              initialValues: opts.initialValues,
              hostSessionId: opts.hostSessionId,
            })}
          showToast=${showToast}
        />

        <!-- Single-issue viewer: docks to the right edge of drawer-content as a
           confined overlay (Drawer dock mode, like SessionPanel) over the
           conversation, which stays mounted and visible behind it. Opened from a
           conversation's "Linked beads issue" link or an inline beads link.
           Gated on beadsIssueOpen so it unmounts after its close animation. -->
        ${beadsIssueOpen && beadsWorkingDir && beadsInitialIssueId
          ? html`
              <${BeadsIssueView}
                workingDir=${beadsWorkingDir}
                issueId=${beadsInitialIssueId}
                selectNonce=${beadsSelectNonce}
                showToast=${showToast}
                onFetchBeadsPrompts=${fetchBeadsPromptsForWorkspace}
                onRunBeadsPrompt=${handleRunBeadsPrompt}
                onReturnToConversation=${handleReturnFromBeadsIssue}
              />
            `
          : ""}

        <!-- Quick "new task" create panel (⌘⇧N) shown as an overlay over the
           current content without switching to the beads list view. Its own
           fixed/absolute layers float over the viewport. -->
        <${BeadsDetailPanel}
          isCreating=${quickCreate.open}
          workingDir=${quickCreate.workingDir}
          onClose=${() => setQuickCreate((qc) => ({ ...qc, open: false }))}
          onCreated=${() => {}}
          showToast=${showToast}
        />
      </div>
      <!-- END drawer-content -->

      <!-- drawer-side: single unified SessionList (desktop always-open + mobile toggled) -->
      <div class="drawer-side z-40">
        <!-- Backdrop: shown on mobile; click to close.
             We deliberately do NOT use for="sidebar-drawer" here. Pairing the
             native label->checkbox toggle with the onClick handler produced a
             double-toggle: onClick set showSidebar=false (re-rendering the
             controlled checkbox to unchecked), then the label's native default
             action synthesised a click on the now-unchecked checkbox, toggling
             it back to checked and reopening the drawer. Driving the close
             purely through onClick (the controlled-state path) avoids that.
             cursor-pointer is required for iOS Safari: it does not dispatch a
             click on the backdrop on tap unless the element carries
             cursor:pointer, so without it outside-taps would never close the
             sidebar drawer on iPhone (matches Drawer.js). -->
        <label
          aria-label="Close sidebar"
          class="drawer-overlay cursor-pointer"
          onClick=${() => setShowSidebar(false)}
        ></label>
        <!-- Panel: resizable on desktop (sidebarWidth), fixed w-80 class provides
             fallback but inline style takes precedence when set via resize handle.
             Uses a soft grey surface (surface-2) rather than the white sidebar
             tone so the conversation rail reads as a distinct panel; the sidebar
             toolbar pill is bumped to the elevated surface-3 so it "floats"
             above this panel (see SessionList.js Toolbar surface prop). -->
        <div
          class="bg-mitto-surface-2 border-r border-mitto-border-1 h-full relative"
          style="width: ${sidebarWidth}px;"
        >
          <${SessionList}
            activeSessions=${activeSessions}
            storedSessions=${storedSessions}
            activeSessionId=${activeSessionId}
            onSelect=${handleSelectSession}
            onNewSession=${handleNewSession}
            onRename=${handleOpenSessionProperties}
            onDelete=${handleDeleteSession}
            onArchive=${handleArchiveSession}
            onSetColor=${handleSetSessionColor}
            onClose=${() => setShowSidebar(false)}
            workspaces=${workspaces}
            theme=${theme}
            onToggleTheme=${toggleTheme}
            fontSize=${fontSize}
            onToggleFontSize=${toggleFontSize}
            onShowSettings=${handleShowSettings}
            onShowWorkspaces=${handleShowWorkspaces}
            onAddFolder=${handleAddFolderOpen}
            onShowWorkspacesForFolder=${handleShowWorkspacesForFolder}
            onShowKeyboardShortcuts=${handleShowKeyboardShortcuts}
            configReadonly=${configReadonly}
            rcFilePath=${rcFilePath}
            badgeClickEnabled=${badgeClickEnabled}
            onBadgeClick=${handleBadgeClick}
            onMoveFolderToGroup=${handleMoveFolderToGroup}
            onUnpinFolder=${handleUnpinFolder}
            openInTargets=${openInTargets}
            onOpenTarget=${handleOpenTarget}
            onBeadsOpen=${handleBeadsOpen}
            onBeadsCreate=${(wd) =>
              setQuickCreate({ open: true, workingDir: wd })}
            onFetchBeadsListPrompts=${fetchBeadsListPromptsForWorkspace}
            onRunBeadsListPrompt=${handleRunBeadsListPrompt}
            onBeadsRefresh=${handleBeadsRefresh}
            onBeadsCleanup=${handleBeadsCleanup}
            mainView=${mainView}
            beadsWorkingDir=${beadsWorkingDir}
            onShowDashboard=${handleShowDashboard}
            queueLength=${queueLength}
            onFetchConversationPrompts=${fetchConversationPromptsForSession}
            onSendPromptToConversation=${handleSendPromptToConversation}
            onMakeLoop=${handleMakeLoop}
            onMakeNonLoop=${handleMakeNonLoop}
            isCreatingSession=${isCreatingSession}
            creatingWorkingDirs=${creatingWorkingDirs}
          />
          <!-- Resize handle on right edge (desktop: drag to resize sidebarWidth) -->
          <div
            class="absolute top-0 right-0 w-1 h-full cursor-col-resize hover:bg-mitto-accent-500/30 transition-colors z-10 ${isSidebarDragging
              ? "bg-mitto-accent-500/40"
              : ""}"
            style="margin-right: -2px;"
            ...${sidebarHandleProps}
            title="Drag to resize sidebar"
          />
        </div>
      </div>
      <!-- END drawer-side -->
    </div>
  `;
}

// =============================================================================
// Mount Application
// =============================================================================

render(html`<${App} />`, document.getElementById("app"));
