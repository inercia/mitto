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
} from "./lib.js";

// Import session tree utilities
import {
  buildSessionTree,
  hasChildren,
  getChildCount,
} from "./utils/sessionTree.js";

// Import utilities
import {
  openExternalURL,
  openFileURL,
  convertFileURLToViewer,
  convertHTTPFileURLToViewer,
  setCurrentWorkspace,
  pickImages,
  hasNativeImagePicker,
  isNativeApp,
  getLastActiveSessionId,
  setLastActiveSessionId,
  secureFetch,
  initCSRF,
  apiUrl,
  authFetch,
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
  FILTER_TAB,
  getFilterTab,
  setFilterTab,
  getFilterTabGrouping,
  cycleFilterTabGrouping,
  fetchConfig,
  invalidateConfigCache,
  getSidebarWidth,
  setSidebarWidth,
} from "./utils/index.js";

// Import hooks
import {
  useWebSocket,
  useSwipeNavigation,
  useSwipeToAction,
  useInfiniteScroll,
  useToast,
  useResizeHandle,
  useTheme,
  useBackgroundNotifications,
  useScrollManagement,
} from "./hooks/index.js";

// Import components
import { SessionItem } from "./components/SessionItem.js";
import { SessionList } from "./components/SessionList.js";
import { MessageList } from "./components/MessageList.js";
import { Message } from "./components/Message.js";
import { ChatInput } from "./components/ChatInput.js";
import { SettingsDialog } from "./components/SettingsDialog.js";
import { WorkspacesDialog } from "./components/WorkspacesDialog.js";
import { AgentDiscoveryDialog } from "./components/AgentDiscoveryDialog.js";
import { QueueDropdown } from "./components/QueueDropdown.js";
import {
  AgentPlanPanel,
  AgentPlanIndicator,
} from "./components/AgentPlanPanel.js";
import { SessionPanel } from "./components/SessionPanel.js";
import { PeriodicFrequencyPanel } from "./components/PeriodicFrequencyPanel.js";
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
  PeriodicIcon,
  PeriodicFilledIcon,
  ChatBubbleIcon,
  LayersIcon,
  TagIcon,
  SidePanelIcon,
  TerminalIcon,
  FolderOpenIcon,
  BeadsIcon,
} from "./components/Icons.js";
import { ContextMenu } from "./components/ContextMenu.js";
import { BeadsView } from "./components/BeadsView.js";

// Import constants
import {
  CYCLING_MODE,
  PERIODIC_PROGRESS_STYLE,
  PERIODIC_PROGRESS_COLORS,
  PERIODIC_PROGRESS_URGENT_THRESHOLD,
} from "./constants.js";

// Import prompt utilities
import {
  promptMenus,
  promptRequires,
  menuSatisfiesRequires,
  MENU_CAPABILITIES,
} from "./utils/prompts.js";

// Import global event handlers (registers side effects on module load) and predicates
import {
  isOverHorizontallyScrollable,
  isModalDialogOpen,
} from "./utils/globalHandlers.js";

// Import extracted components
import { WorkspaceBadge, WorkspacePill } from "./components/WorkspaceBadge.js";
import { DeleteDialog } from "./components/DeleteDialog.js";
import { KeyboardShortcutsDialog } from "./components/KeyboardShortcutsDialog.js";
import { NewSessionWorkspaceDialog } from "./components/NewSessionWorkspaceDialog.js";

// SettingsDialog, WorkspacesDialog, etc. are all imported from ./components/

// SessionItem and SessionList are imported from ./components/

// =============================================================================
// Main App Component
// =============================================================================

function App() {
  const {
    connected,
    messages,
    sendPrompt,
    cancelPrompt,
    newSession,
    switchSession,
    loadMoreMessages,
    updateSessionName,
    renameSession,
    pinSession,
    archiveSession,
    removeSession,
    isStreaming,
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
    periodicStarted,
    clearPeriodicStarted,
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
    ensureResumed,
    isCreatingSession,
  } = useWebSocket();

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
    if (activeSessionId && sessionInfo?.gc_suspended && !sessionInfo?.archived) {
      ensureResumed(activeSessionId);
    }
  }, [activeSessionId, sessionInfo?.gc_suspended, sessionInfo?.archived, ensureResumed]);

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
  // mainView controls what is shown in the right-side area: "conversation" or "beads"
  const [mainView, setMainView] = useState("conversation");
  // Ref mirror of mainView so native swipe-gesture handlers (registered in an effect
  // whose dependency set does not include mainView) always read the current view
  // without a stale closure.
  const mainViewRef = useRef(mainView);
  useEffect(() => {
    mainViewRef.current = mainView;
  }, [mainView]);
  const [beadsWorkingDir, setBeadsWorkingDir] = useState(null);
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
  // When the beads view is opened from a linked conversation (e.g. the
  // properties panel's "Linked beads issue" link), these drive auto-selecting
  // that issue once the list loads. The nonce bumps on every open so clicking
  // the same issue again re-selects it.
  const [beadsInitialIssueId, setBeadsInitialIssueId] = useState(null);
  const [beadsSelectNonce, setBeadsSelectNonce] = useState(0);
  const [sidePanelTab, setSidePanelTab] = useState("properties");
  const [showQueueDropdown, setShowQueueDropdown] = useState(false);
  const [isDeletingQueueMessage, setIsDeletingQueueMessage] = useState(false);
  const [isMovingQueueMessage, setIsMovingQueueMessage] = useState(false);
  const [isAddingToQueue, setIsAddingToQueue] = useState(false);
  const [queueBadgePulse, setQueueBadgePulse] = useState(false);
  // Agent Plan panel state - per-session plan entries stored as { sessionId: entries[] }
  const [planEntriesMap, setPlanEntriesMap] = useState({});
  const [showPlanPanel, setShowPlanPanel] = useState(false);
  const [planUserPinned, setPlanUserPinned] = useState(false);
  // Plan expiration tracking - per-session: { sessionId: { completedAt: timestamp, messagesAfterCompletion: number } }
  const [planExpirationMap, setPlanExpirationMap] = useState({});
  // Plan completion timer - per-session: { sessionId: timeoutId }
  const planCompletionTimersRef = useRef({});

  // Delay in milliseconds before erasing a completed plan
  const PLAN_COMPLETION_ERASE_DELAY = 5000;

  // Number of user messages after plan completion before auto-expiring (configurable between 3-4)
  const PLAN_EXPIRATION_MESSAGE_THRESHOLD = 3;

  // Computed: get plan entries for active session
  const planEntries = useMemo(() => {
    if (!activeSessionId) return [];
    return planEntriesMap[activeSessionId] || [];
  }, [planEntriesMap, activeSessionId]);

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
  const [pendingPeriodicTab, setPendingPeriodicTab] = useState(null); // Track if new session should be periodic
  const [settingsDialog, setSettingsDialog] = useState({
    isOpen: false,
    forceOpen: false,
  }); // Settings dialog
  const [workspacesDialog, setWorkspacesDialog] = useState({ isOpen: false }); // Workspaces management dialog
  const [keyboardShortcutsDialog, setKeyboardShortcutsDialog] = useState({
    isOpen: false,
  }); // Keyboard shortcuts dialog
  const [workspacePrompts, setWorkspacePrompts] = useState([]); // All prompts for current workspace (merged from all sources by backend)
  const [workspacePromptsDir, setWorkspacePromptsDir] = useState(null); // Current workspace dir for prompts cache
  const [workspacePromptsLastModified, setWorkspacePromptsLastModified] =
    useState(null); // Last-Modified header for conditional requests
  const [configReadonly, setConfigReadonly] = useState(
    () => window.mittoIsExternal === true, // Start as true for external connections, or when --config flag was used or using RC file
  );
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

  // Map a beads issue ID → the most recently updated conversation linked to it.
  // The beads view uses this to render issue IDs as links that open the
  // associated conversation (if any).
  const beadsIssueSessionMap = useMemo(() => {
    const map = {};
    const updatedAt = {};
    for (const s of allSessions) {
      const issue = s.beads_issue;
      if (!issue) continue;
      const t = new Date(s.updated_at || 0).getTime();
      if (!(issue in map) || t >= updatedAt[issue]) {
        map[issue] = s.session_id;
        updatedAt[issue] = t;
      }
    }
    return map;
  }, [allSessions]);

  // Predefined prompts: the backend's /api/workspace-prompts endpoint now returns
  // all prompts fully merged (global + server-specific + workspace) and filtered.
  // The frontend just uses them directly — no client-side merge needed.
  // Only prompts whose `menus` list includes "prompts" (or that omit `menus`
  // entirely, defaulting to the dropup) appear in the ChatInput "^" dropup.
  // Prompts that target only other menus (e.g. "conversation") are excluded.
  const predefinedPrompts = workspacePrompts.filter(
    (p) => promptMenus(p).includes("prompts") && menuSatisfiesRequires(p, "prompts"),
  );

  // Fetch the prompts whose `menus` list includes `conversation` for a SPECIFIC
  // conversation, evaluating each prompt's `enabledWhen` against that
  // conversation's own context (child status, children, permissions, tools).
  //
  // The context menu must reflect the conversation being right-clicked, not the
  // active session, so we cannot reuse the active-session `workspacePrompts`
  // list. Instead we query /api/workspace-prompts with the target session_id so
  // the backend evaluates `enabledWhen` for that conversation, then keep only the
  // prompts that opt into the conversation menu via `menus`.
  const fetchConversationPromptsForSession = useCallback(
    async (session, workingDir) => {
      const sessionId = session?.session_id;
      const dir = workingDir || session?.working_dir;
      if (!sessionId || !dir) return [];
      try {
        const res = await authFetch(
          apiUrl(
            `/api/workspace-prompts?dir=${encodeURIComponent(dir)}&session_id=${encodeURIComponent(sessionId)}`,
          ),
        );
        if (!res.ok) return [];
        const data = await res.json();
        const all = data?.prompts || [];
        return all.filter(
          (p) =>
            p &&
            promptMenus(p).includes("conversation") &&
            menuSatisfiesRequires(p, "conversation"),
        );
      } catch (err) {
        console.error(
          "Failed to fetch conversation prompts for session:",
          err,
        );
        return [];
      }
    },
    [],
  );

  // Fetch the prompts whose `menus` list includes `beadsIssues` for a workspace
  // directory. Used by the per-issue context menu in the Beads list view. There
  // is no specific conversation here, so `enabledWhen` is evaluated without a
  // session_id; we only keep the prompts that opt into the beads menu via
  // `menus`.
  const fetchBeadsPromptsForWorkspace = useCallback(async (workingDir) => {
    if (!workingDir) return [];
    try {
      const res = await authFetch(
        apiUrl(`/api/workspace-prompts?dir=${encodeURIComponent(workingDir)}`),
      );
      if (!res.ok) return [];
      const data = await res.json();
      const all = data?.prompts || [];
      return all
        .filter(
          (p) =>
            p &&
            promptMenus(p).includes("beadsIssues") &&
            menuSatisfiesRequires(p, "beadsIssues"),
        )
        .sort((a, b) => (a.name || "").localeCompare(b.name || ""));
    } catch (err) {
      console.error("Failed to fetch beads prompts for workspace:", err);
      return [];
    }
  }, []);

  // Fetch the prompts whose `menus` list includes `beadsList` for a workspace
  // directory. Used by the list-level prompts button in the Beads list view.
  // These prompts operate on the whole issue list (e.g. cleanup, triage) rather
  // than a single issue, so they take no parameters. There is no specific
  // conversation here, so `enabledWhen` is evaluated without a session_id; we
  // only keep the prompts that opt into the beads-list menu via `menus`.
  const fetchBeadsListPromptsForWorkspace = useCallback(async (workingDir) => {
    if (!workingDir) return [];
    try {
      const res = await authFetch(
        apiUrl(`/api/workspace-prompts?dir=${encodeURIComponent(workingDir)}`),
      );
      if (!res.ok) return [];
      const data = await res.json();
      const all = data?.prompts || [];
      return all
        .filter(
          (p) =>
            p &&
            promptMenus(p).includes("beadsList") &&
            menuSatisfiesRequires(p, "beadsList"),
        )
        .sort((a, b) => (a.name || "").localeCompare(b.name || ""));
    } catch (err) {
      console.error("Failed to fetch beads list prompts for workspace:", err);
      return [];
    }
  }, []);

  // Run a beads prompt against a specific issue: create a new conversation in
  // the beads workspace, then seed it with the prompt text plus a single
  // `ISSUE_ID` argument. The backend's ${VAR} substitution engine resolves
  // `${ISSUE_ID}` in the prompt body when the queued message is sent (see the
  // queue `arguments` support from mitto-t93); the prompt itself loads any
  // further detail via `bd show ${ISSUE_ID}`. Mirrors handleSendPromptToConversation's
  // queue delivery (the queue runs the message once the new conversation is idle).
  const handleRunBeadsPrompt = useCallback(
    async (prompt, issue) => {
      const text = prompt?.prompt;
      if (!text || !issue || !beadsWorkingDir) return;

      // When a folder has several workspaces (e.g. Opus and Sonnet variants),
      // prefer the one marked is_default so beads launches use the intended agent.
      const beadsMatches = workspaces.filter((w) => w.working_dir === beadsWorkingDir);
      const ws = beadsMatches.find((w) => w.is_default) || beadsMatches[0];
      // Name the conversation after the issue (e.g. "mitto-kp7 · Fix login") so
      // it doesn't linger as "New conversation". The prompt is delivered via the
      // queue, and auto-title generation on that path only runs once the queued
      // turn completes — which is delayed for beads prompts that immediately wait
      // on user input. Setting an explicit name fixes the title right away and
      // also suppresses auto-title generation (it only runs when the name is empty).
      const convName = issue.title ? `${issue.id} · ${issue.title}` : issue.id;
      const result = await newSession({
        workingDir: beadsWorkingDir,
        acpServer: ws?.acp_server,
        name: convName,
        beadsIssue: issue.id,
      });
      if (!result?.sessionId) {
        showToast({
          style: "error",
          title: result?.error || "Failed to create conversation",
          duration: 4000,
        });
        return;
      }

      // Seed the new conversation with the prompt text and a single `ISSUE_ID`
      // argument. The backend substitutes `${ISSUE_ID}` into the prompt body
      // when the message is sent; the prompt loads any further detail itself
      // via `bd show ${ISSUE_ID}`.
      try {
        await secureFetch(apiUrl(`/api/sessions/${result.sessionId}/queue`), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            message: text,
            arguments: { ISSUE_ID: issue.id },
          }),
        });
      } catch (err) {
        console.error("Failed to seed beads conversation:", err);
      }

      // newSession already activates the new conversation; switch the main view
      // back from the beads panel so the new conversation is shown.
      setMainView("conversation");
      showToast({
        style: "success",
        title: `Started "${prompt.name}" for ${issue.id}`,
        duration: 3000,
      });
    },
    [beadsWorkingDir, workspaces, newSession, showToast],
  );

  // Run a beads-list prompt: create a new conversation in the beads workspace,
  // seed it with the prompt text alone (these prompts operate on the whole issue
  // list and take no parameters), then switch to it. Mirrors handleRunBeadsPrompt
  // minus the per-issue context. The conversation is named after the prompt so it
  // doesn't linger as "New conversation" (this also suppresses auto-title gen).
  const handleRunBeadsListPrompt = useCallback(
    async (prompt) => {
      const text = prompt?.prompt;
      if (!text || !beadsWorkingDir) return;

      // Prefer the folder's default workspace when several share this directory.
      const beadsMatches = workspaces.filter((w) => w.working_dir === beadsWorkingDir);
      const ws = beadsMatches.find((w) => w.is_default) || beadsMatches[0];
      const result = await newSession({
        workingDir: beadsWorkingDir,
        acpServer: ws?.acp_server,
        name: prompt.name,
      });
      if (!result?.sessionId) {
        showToast({
          style: "error",
          title: result?.error || "Failed to create conversation",
          duration: 4000,
        });
        return;
      }

      try {
        await secureFetch(apiUrl(`/api/sessions/${result.sessionId}/queue`), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ message: text }),
        });
      } catch (err) {
        console.error("Failed to seed beads list conversation:", err);
      }

      // newSession already activates the new conversation; switch the main view
      // back from the beads panel so the new conversation is shown.
      setMainView("conversation");
      showToast({
        style: "success",
        title: `Started "${prompt.name}"`,
        duration: 3000,
      });
    },
    [beadsWorkingDir, workspaces, newSession, showToast],
  );

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
  }, [backgroundCompletion, clearBackgroundCompletion, showToast, focusSession]);

  // Show toast and native notification when a periodic prompt starts
  useEffect(() => {
    if (periodicStarted) {
      // Show native macOS notification (not sticky — auto-dismisses)
      if (
        window.mittoNativeNotificationsEnabled &&
        typeof window.mittoShowNativeNotification === "function"
      ) {
        window.mittoShowNativeNotification(
          periodicStarted.sessionName || "Periodic Conversation",
          "Periodic run started",
          periodicStarted.sessionId,
          false,
        );
      }

      // Show in-app toast
      showToast({
        style: "info",
        title: periodicStarted.sessionName || "Periodic Conversation",
        message: "periodic run started",
        duration: 5000,
        onClick: () => focusSession(periodicStarted.sessionId),
      });
      clearPeriodicStarted();
    }
  }, [periodicStarted, clearPeriodicStarted, showToast, focusSession]);

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
      const sessionName = backgroundUIPromptTimeout.sessionName || "Conversation";
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
        message: backgroundUIPromptTimeout.question || "Agent needed your input",
        duration: 10000,
        onClick: () => focusSession(backgroundUIPromptTimeout.sessionId),
      });
      clearBackgroundUIPromptTimeout();
    }
  }, [backgroundUIPromptTimeout, clearBackgroundUIPromptTimeout, showToast, focusSession]);

  // Background notification event listeners (extracted to
  // hooks/useBackgroundNotifications.js): runner fallback, memory recycle,
  // ACP start/permanent errors, hook failures, generic notifications, and
  // active-session native-notification cleanup.
  useBackgroundNotifications({ showToast, focusSession, activeSessionId });

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

  // Conversation cycling mode setting (web UI, default: "all" - cycle through all non-archived)
  const [conversationCyclingMode, setConversationCyclingMode] = useState(
    CYCLING_MODE.ALL,
  );

  // Track expanded groups state for re-computing navigableSessions in "visible_groups" mode
  // We store the actual groups map in state rather than just a version counter, because
  // on mobile/WKWebView, localStorage can become stale and isGroupExpanded() might return
  // incorrect values. By storing the map in React state, we ensure the navigation filtering
  // always uses the correct, current expanded/collapsed state.
  const [expandedGroupsForNav, setExpandedGroupsForNav] = useState(() =>
    getExpandedGroups(),
  );

  // Track filter tab for navigation (needed for filtering navigable sessions)
  const [filterTabForNav, setFilterTabForNav] = useState(() => getFilterTab());

  // Track grouping mode for navigation (needed for "visible_groups" cycling mode)
  // Uses per-tab grouping based on the current filter tab
  const [groupingModeForNav, setGroupingModeForNav] = useState(() =>
    getFilterTabGrouping(getFilterTab()),
  );

  // Helper to get group key for a session (same logic as sidebar grouping)
  const getSessionGroupKey = useCallback(
    (session) => {
      if (groupingModeForNav === "server") {
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        return session.acp_server || storedSession?.acp_server || "Unknown";
      } else if (
        groupingModeForNav === "workspace" ||
        groupingModeForNav === "folder"
      ) {
        // workspace and folder modes - group by working_dir|acp_server
        // In folder mode, this returns the subgroup key (sessions are in subgroups, not folders directly)
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        const workingDir =
          session.working_dir ||
          storedSession?.working_dir ||
          getGlobalWorkingDir(session.session_id) ||
          "";
        const acpServer = session.acp_server || storedSession?.acp_server || "";
        return `${workingDir}|${acpServer}`;
      }
      return null; // no grouping
    },
    [groupingModeForNav, storedSessions],
  );

  // Helper to get group label for sorting (same as sidebar)
  const getSessionGroupLabel = useCallback(
    (session) => {
      if (groupingModeForNav === "server") {
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        return session.acp_server || storedSession?.acp_server || "Unknown";
      } else if (
        groupingModeForNav === "workspace" ||
        groupingModeForNav === "folder"
      ) {
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        const workingDir =
          session.working_dir ||
          storedSession?.working_dir ||
          getGlobalWorkingDir(session.session_id) ||
          "";
        // Label is the workspace display name if available, otherwise basename
        const acpServer = session.acp_server || storedSession?.acp_server || "";
        const ws = workspaces.find(w => w.working_dir === workingDir && (!acpServer || w.acp_server === acpServer));
        return ws?.name || (workingDir ? getBasename(workingDir) : "Unknown");
      }
      return "";
    },
    [groupingModeForNav, storedSessions, workspaces],
  );

  // Sessions available for navigation based on active filter tab
  // Navigation via keyboard shortcuts and swipe gestures should only cycle within the active tab
  // In "visible_groups" cycling mode, also skip sessions in collapsed groups
  // Sessions are ordered to match the visual order in the sidebar:
  // - When grouped: groups sorted alphabetically, sessions within groups by created_at (newest first)
  // - When not grouped: sessions sorted by created_at (newest first)
  const navigableSessions = useMemo(() => {
    // First filter sessions based on the active filter tab
    // Also exclude child sessions (those with parent_session_id) — navigation
    // should only cycle through top-level conversations
    let tabFilteredSessions;
    switch (filterTabForNav) {
      case FILTER_TAB.PERIODIC:
        tabFilteredSessions = allSessions.filter(
          (s) => !s.archived && s.periodic_enabled && !s.parent_session_id,
        );
        break;
      case FILTER_TAB.ARCHIVED:
        tabFilteredSessions = allSessions.filter(
          (s) => s.archived && !s.parent_session_id,
        );
        break;
      case FILTER_TAB.CONVERSATIONS:
      default:
        tabFilteredSessions = allSessions.filter(
          (s) => !s.archived && !s.periodic_enabled && !s.parent_session_id,
        );
        break;
    }

    // If no grouping mode, sessions are already sorted by created_at from allSessions
    if (groupingModeForNav === "none") {
      return tabFilteredSessions;
    }

    // When grouping is enabled, we need to sort sessions to match the sidebar visual order:
    // 1. Groups sorted alphabetically by label
    // 2. Sessions within each group sorted by created_at (newest first)
    //
    // We do this by sorting all sessions with a composite sort key:
    // primary: group label (alphabetical)
    // secondary: created_at (newest first)
    const sortedSessions = [...tabFilteredSessions].sort((a, b) => {
      const labelA = getSessionGroupLabel(a);
      const labelB = getSessionGroupLabel(b);

      // Primary sort: group label (alphabetical)
      const labelCompare = labelA.localeCompare(labelB);
      if (labelCompare !== 0) return labelCompare;

      // Secondary sort: created_at (newest first)
      return new Date(b.created_at) - new Date(a.created_at);
    });

    // In "visible_groups" cycling mode, only include sessions that are in expanded groups
    if (conversationCyclingMode !== CYCLING_MODE.VISIBLE_GROUPS) {
      return sortedSessions;
    }

    // Filter sessions based on their group's expanded state
    // Use expandedGroupsForNav (React state) instead of calling isGroupExpanded()
    // which reads from localStorage. This is critical for mobile/WKWebView where
    // localStorage can become stale or inconsistent.
    return sortedSessions.filter((session) => {
      const groupKey = getSessionGroupKey(session);
      // Check if group is expanded using React state (not localStorage)
      // Default: archived section is collapsed, all others are expanded
      if (groupKey in expandedGroupsForNav) {
        return expandedGroupsForNav[groupKey];
      }
      if (groupKey === "__archived__") {
        return false;
      }
      return true;
    });
  }, [
    allSessions,
    storedSessions,
    conversationCyclingMode,
    groupingModeForNav,
    filterTabForNav,
    expandedGroupsForNav,
    getSessionGroupKey,
    getSessionGroupLabel,
  ]);

  // Navigate to previous/next session with animation direction (wraps around for swipe gestures)
  // Skips archived sessions
  const navigateToPreviousSession = useCallback(() => {
    if (navigableSessions.length === 0) return;
    const currentIndex = navigableSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    // If current session is not in navigableSessions (e.g., in a collapsed group),
    // jump to the last navigable session
    const prevIndex =
      currentIndex === -1
        ? navigableSessions.length - 1
        : currentIndex === 0
          ? navigableSessions.length - 1
          : currentIndex - 1;
    setSwipeDirection("right"); // Content slides in from left
    setSwipeArrow("right"); // Show right arrow (user swiped right)
    switchSession(navigableSessions[prevIndex].session_id);
  }, [navigableSessions, activeSessionId, switchSession]);

  const navigateToNextSession = useCallback(() => {
    if (navigableSessions.length === 0) return;
    const currentIndex = navigableSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    // If current session is not in navigableSessions (e.g., in a collapsed group),
    // jump to the first navigable session
    const nextIndex =
      currentIndex === -1
        ? 0
        : currentIndex === navigableSessions.length - 1
          ? 0
          : currentIndex + 1;
    setSwipeDirection("left"); // Content slides in from right
    setSwipeArrow("left"); // Show left arrow (user swiped left)
    switchSession(navigableSessions[nextIndex].session_id);
  }, [navigableSessions, activeSessionId, switchSession]);

  // Navigate to session above in the list (no wrap-around, for keyboard shortcuts)
  // Note: No swipe animation - only swipe gestures should trigger horizontal scroll effect
  // Skips archived sessions
  const navigateToSessionAbove = useCallback(() => {
    if (navigableSessions.length === 0) return;
    const currentIndex = navigableSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    // If current session is not in navigableSessions (e.g., in a collapsed group),
    // jump to the last navigable session (conceptually "above" since list goes down)
    if (currentIndex === -1) {
      switchSession(navigableSessions[navigableSessions.length - 1].session_id);
      return;
    }
    if (currentIndex === 0) return; // Already at top
    switchSession(navigableSessions[currentIndex - 1].session_id);
  }, [navigableSessions, activeSessionId, switchSession]);

  // Navigate to session below in the list (no wrap-around, for keyboard shortcuts)
  // Note: No swipe animation - only swipe gestures should trigger horizontal scroll effect
  // Skips archived sessions
  const navigateToSessionBelow = useCallback(() => {
    if (navigableSessions.length === 0) return;
    const currentIndex = navigableSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    // If current session is not in navigableSessions (e.g., in a collapsed group),
    // jump to the first navigable session (conceptually "below" since list goes down)
    if (currentIndex === -1) {
      switchSession(navigableSessions[0].session_id);
      return;
    }
    if (currentIndex === navigableSessions.length - 1) return; // Already at bottom
    switchSession(navigableSessions[currentIndex + 1].session_id);
  }, [navigableSessions, activeSessionId, switchSession]);

  // Open sidebar handler for edge swipe
  const openSidebar = useCallback(() => {
    setShowSidebar(true);
  }, []);

  // Enable swipe navigation on mobile
  // - Swipe left/right anywhere: switch sessions
  // - Swipe right from left edge: open sidebar
  useSwipeNavigation(
    mainContentRef,
    navigateToNextSession,
    navigateToPreviousSession,
    {
      threshold: 80, // Require a decent swipe distance
      maxVertical: 80, // Allow some vertical movement
      edgeWidth: 40, // Start from edge zone
      onEdgeSwipeRight: openSidebar, // Swipe right from left edge opens sidebar
    },
  );

  // Navigate to session by index (0-based) for keyboard shortcuts
  // Uses navigableSessions to skip archived conversations
  const navigateToSessionByIndex = useCallback(
    (index) => {
      if (index >= 0 && index < navigableSessions.length) {
        const targetSession = navigableSessions[index];
        if (targetSession.session_id !== activeSessionId) {
          switchSession(targetSession.session_id);
        }
      }
    },
    [navigableSessions, activeSessionId, switchSession],
  );

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
  ]);

  // State for UI theme style (v2 = Clawdbot-inspired)
  const [uiTheme, setUiTheme] = useState("default");

  // UI settings (macOS only)
  const [agentCompletedSoundEnabled, setAgentCompletedSoundEnabled] =
    useState(false);

  // UI confirmation settings (default: true - show confirmations)
  const [confirmDeleteSession, setConfirmDeleteSession] = useState(true);

  // Badge/folder click command (macOS only)
  const [badgeClickCommand, setBadgeClickCommand] = useState("open ${MITTO_WORKING_DIR}");
  // Terminal action command (macOS only)
  const [terminalActionCommand, setTerminalActionCommand] = useState("open -a Terminal ${MITTO_WORKING_DIR}");

  // Derive enabled state from non-empty command
  const badgeClickEnabled = typeof window.mittoPickFolder === "function" && badgeClickCommand.trim() !== "";
  const terminalActionEnabled = typeof window.mittoPickFolder === "function" && terminalActionCommand.trim() !== "";

  // Input font family setting (web UI, default: "system")
  const [inputFontFamily, setInputFontFamily] = useState("system");

  // Input font size setting (web UI, default: "default")
  const [inputFontSize, setInputFontSize] = useState("default");

  // Send key mode setting (web UI, default: "enter")
  // "enter" = Enter to send, Shift+Enter for new line
  // "ctrl-enter" = Ctrl/Cmd+Enter to send, Enter for new line
  const [sendKeyMode, setSendKeyMode] = useState("enter");

  // Agent discovery dialog state (shown on first run when no ACP servers configured)
  const [showAgentDiscovery, setShowAgentDiscovery] = useState(false);

  // Check if running in the native macOS app
  const isMacApp = typeof window.mittoPickFolder === "function";

  // Fetch config on mount to get predefined prompts, UI theme, and check for workspaces
  useEffect(() => {
    fetchConfig()
      .then((config) => {
        // Track if config is read-only (loaded from --config file or RC file)
        if (config?.config_readonly) {
          setConfigReadonly(true);
          // If using an RC file, store the path for tooltip display
          if (config?.rc_file_path) {
            setRcFilePath(config.rc_file_path);
          }
        }
        // Load v2 stylesheet if configured
        if (config?.web?.theme === "v2") {
          setUiTheme("v2");
          // Dynamically load the v2 stylesheet
          const existingLink = document.getElementById("mitto-theme-v2");
          if (!existingLink) {
            const link = document.createElement("link");
            link.id = "mitto-theme-v2";
            link.rel = "stylesheet";
            link.href = "./styles-v2.css";
            document.head.appendChild(link);
          }
          // Add v2-theme class to body for CSS overrides
          document.body.classList.add("v2-theme");
        }
        // Load UI confirmation settings
        if (config?.ui?.confirmations?.delete_session === false) {
          setConfirmDeleteSession(false);
        }
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
        // Load badge/folder click command (macOS only)
        setBadgeClickCommand(
          config?.ui?.mac?.badge_click_action?.command || "open ${MITTO_WORKING_DIR}",
        );
        // Load terminal action command (macOS only)
        setTerminalActionCommand(
          config?.ui?.mac?.terminal_action?.command || "open -a Terminal ${MITTO_WORKING_DIR}",
        );
        // Load input font family setting (web UI)
        if (config?.ui?.web?.input_font_family) {
          setInputFontFamily(config.ui.web.input_font_family);
        }
        // Load input font size setting (web UI)
        if (config?.ui?.web?.input_font_size) {
          setInputFontSize(config.ui.web.input_font_size);
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

  // Listen for grouping mode, expanded groups, and filter tab changes for navigation
  useEffect(() => {
    const handleExpandedGroupsChanged = (e) => {
      // Update React state with the new expanded groups state
      // This uses the event detail (groupKey, expanded) to update state directly,
      // avoiding a read from localStorage which can be stale on mobile/WKWebView
      setExpandedGroupsForNav((prev) => {
        const { groupKey, expanded } = e.detail || {};
        if (groupKey !== undefined) {
          return { ...prev, [groupKey]: expanded };
        }
        // If no detail provided, fall back to reading from localStorage
        // (this handles the case where the event is dispatched without detail)
        return getExpandedGroups();
      });
    };
    const handleGroupingModeChanged = (e) => {
      setGroupingModeForNav(e.detail.mode);
      // Re-read expanded groups when grouping mode changes
      setExpandedGroupsForNav(getExpandedGroups());
    };
    const handleFilterTabChanged = (e) => {
      setFilterTabForNav(e.detail.tab);
      // Also update grouping mode for the new tab
      const tabGroupingMode = getFilterTabGrouping(e.detail.tab);
      setGroupingModeForNav(tabGroupingMode);
    };
    window.addEventListener(
      "mitto-expanded-groups-changed",
      handleExpandedGroupsChanged,
    );
    window.addEventListener(
      "mitto-grouping-mode-changed",
      handleGroupingModeChanged,
    );
    window.addEventListener("mitto-filter-tab-changed", handleFilterTabChanged);
    return () => {
      window.removeEventListener(
        "mitto-expanded-groups-changed",
        handleExpandedGroupsChanged,
      );
      window.removeEventListener(
        "mitto-grouping-mode-changed",
        handleGroupingModeChanged,
      );
      window.removeEventListener(
        "mitto-filter-tab-changed",
        handleFilterTabChanged,
      );
    };
  }, []);

  // Fetch workspace prompts with conditional request support (If-Modified-Since)
  // This enables efficient periodic refresh without transferring data if unchanged
  const fetchWorkspacePrompts = useCallback(
    async (workingDir, forceRefresh = false) => {
      if (!workingDir) return;

      const headers = {};
      // Use If-Modified-Since for conditional requests (unless forcing refresh)
      if (
        !forceRefresh &&
        workspacePromptsLastModified &&
        workingDir === workspacePromptsDir
      ) {
        headers["If-Modified-Since"] = workspacePromptsLastModified;
      }

      try {
        const sessionParam = activeSessionId
          ? `&session_id=${encodeURIComponent(activeSessionId)}`
          : "";
        const res = await authFetch(
          apiUrl(
            `/api/workspace-prompts?dir=${encodeURIComponent(workingDir)}${sessionParam}`,
          ),
          { headers },
        );

        // 304 Not Modified - prompts haven't changed
        if (res.status === 304) {
          return;
        }

        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`);
        }

        const data = await res.json();
        setWorkspacePrompts(data?.prompts || []);
        setWorkspacePromptsDir(workingDir);

        // Store Last-Modified header for future conditional requests
        const lastModified = res.headers.get("Last-Modified");
        setWorkspacePromptsLastModified(lastModified);
      } catch (err) {
        console.error("Failed to fetch workspace prompts:", err);
        // Only clear prompts on error if this is a new workspace
        if (workingDir !== workspacePromptsDir) {
          setWorkspacePrompts([]);
          setWorkspacePromptsDir(workingDir);
          setWorkspacePromptsLastModified(null);
        }
      }
    },
    [workspacePromptsDir, workspacePromptsLastModified, activeSessionId],
  );

  // Fetch workspace prompts when the active session's working_dir changes
  useEffect(() => {
    const workingDir = sessionInfo?.working_dir;
    if (!workingDir) return;

    // Always fetch if workspace changed
    if (workingDir !== workspacePromptsDir) {
      fetchWorkspacePrompts(workingDir, true); // Force refresh for new workspace
    }
  }, [sessionInfo?.working_dir, workspacePromptsDir, fetchWorkspacePrompts]);

  // Re-fetch prompts when active session changes (session switch in same workspace)
  // CEL expressions like session.isChild and parent.exists vary per session,
  // so the filtered prompt list may differ even for the same workspace files.
  useEffect(() => {
    const workingDir = sessionInfo?.working_dir;
    if (!workingDir || !activeSessionId) return;
    // Only re-fetch if we already have prompts for this workspace
    // (initial fetch is handled by the working_dir change effect above)
    if (workingDir === workspacePromptsDir) {
      fetchWorkspacePrompts(workingDir, true); // Force to bypass conditional request (304)
    }
  }, [activeSessionId]);

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

  // Periodic refresh of workspace prompts (every 30 seconds)
  // Uses conditional requests to avoid unnecessary data transfer
  useEffect(() => {
    const workingDir = sessionInfo?.working_dir;
    if (!workingDir) return;

    const intervalId = setInterval(() => {
      fetchWorkspacePrompts(workingDir, false); // Conditional request
    }, 30000); // 30 seconds

    return () => clearInterval(intervalId);
  }, [sessionInfo?.working_dir, fetchWorkspacePrompts]);

  // Refresh workspace prompts when app becomes visible (tab switch, phone wake)
  useEffect(() => {
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible" && sessionInfo?.working_dir) {
        // Small delay to avoid racing with other visibility handlers
        setTimeout(() => {
          fetchWorkspacePrompts(sessionInfo.working_dir, false);
        }, 500);
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () =>
      document.removeEventListener("visibilitychange", handleVisibilityChange);
  }, [sessionInfo?.working_dir, fetchWorkspacePrompts]);

  // Refresh prompts when file watcher detects changes (mitto:prompts_changed event)
  // This event is dispatched by handleGlobalEvent when receiving prompts_changed from WebSocket
  useEffect(() => {
    const handlePromptsChanged = (event) => {
      console.log("[prompts] File watcher detected changes:", event.detail);

      // Refresh workspace prompts (force refresh to skip conditional request)
      // The backend merges all sources (global + server + workspace), so this is all we need.
      if (sessionInfo?.working_dir) {
        fetchWorkspacePrompts(sessionInfo.working_dir, true);
      }
    };

    window.addEventListener("mitto:prompts_changed", handlePromptsChanged);
    return () =>
      window.removeEventListener("mitto:prompts_changed", handlePromptsChanged);
  }, [
    sessionInfo?.working_dir,
    fetchWorkspacePrompts,
  ]);

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

  // Messages-area scroll management (extracted to hooks/useScrollManagement.js):
  // at-bottom tracking, new-message indicator, auto-scroll on new content,
  // instant positioning on session switch, and prepend scroll restoration.
  // messagesContainerRef and scrollPreservationRef are owned by App (shared with
  // the render, useInfiniteScroll, and handleLoadMore) and passed in.
  const { isUserAtBottom, hasNewMessages, scrollToBottom } =
    useScrollManagement({
      messages,
      activeSessionId,
      isStreaming,
      isLoadingMore,
      messagesContainerRef,
      scrollPreservationRef,
    });

  // Ref for the chat input component to allow focusing from native menu
  const chatInputRef = useRef(null);

  // Helper to configure a newly created session as periodic
  const applyPeriodicConfig = async (sessionId) => {
    try {
      await secureFetch(apiUrl(`/api/sessions/${sessionId}/periodic`), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          prompt: "(pending)",
          frequency: { value: 1, unit: "hours" },
          enabled: false,
        }),
      });
    } catch (e) {
      console.error("Failed to create periodic config:", e);
    }
  };

  // Expose global functions for native macOS menu integration
  useEffect(() => {
    // New Conversation - called from native Cmd+N menu
    window.mittoNewConversation = async () => {
      // Use handleNewSession logic to support workspace selection
      const currentTab = getFilterTab();
      const isPeriodic = currentTab === FILTER_TAB.PERIODIC;
      if (workspaces.length === 0) {
        // No workspaces configured - open settings dialog (unless config is read-only)
        if (!configReadonly) {
          setSettingsDialog({ isOpen: true, forceOpen: true });
        }
        setShowSidebar(false);
        return;
      }
      if (workspaces.length > 1) {
        setPendingPeriodicTab(currentTab);
        setWorkspaceDialog({ isOpen: true });
      } else {
        // Single workspace - create session directly with workspace info
        const ws = workspaces[0];
        const result = await newSession({
          workingDir: ws.working_dir,
          acpServer: ws.acp_server,
        });
        if (result?.sessionId && isPeriodic) {
          await applyPeriodicConfig(result.sessionId);
        }
        // Handle creation result
        if (result?.errorCode === "session_creation_timeout") {
          // Agent is busy; auto-retry is in progress — toast already meaningful
          showToast({
            style: "warning",
            title: result.retrying
              ? "Agent is busy \u2014 retrying automatically\u2026"
              : (result.error || "Agent is busy"),
            duration: result.retrying ? 30000 : 5000,
          });
        } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
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

      // If confirmation is enabled, show the delete dialog
      if (confirmDeleteSession) {
        // Find the current session to pass to the dialog
        const currentSession =
          activeSessions.find((s) => s.session_id === activeSessionId) ||
          storedSessions.find((s) => s.session_id === activeSessionId);
        if (currentSession) {
          setDeleteDialog({ isOpen: true, session: currentSession });
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
      const isArchived = currentSession.archived || currentSession.info?.archived;

      // Toggle archive state
      await archiveSession(activeSessionId, !isArchived);

      // When unarchiving, switch to conversations tab and select the session
      if (isArchived) {
        setFilterTab(FILTER_TAB.CONVERSATIONS);
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
      // Don't navigate when the beads view is open — swipes should not switch conversations.
      if (mainViewRef.current === "beads") return;
      navigateToNextSession();
    };

    // Previous Conversation - called from native swipe gesture (swipe right)
    window.mittoPrevConversation = () => {
      // Don't navigate if the cursor is over a horizontally scrollable element.
      if (isOverHorizontallyScrollable()) return;
      // Don't navigate if a modal dialog is open.
      if (isModalDialogOpen()) return;
      // Don't navigate when the beads view is open — swipes should not switch conversations.
      if (mainViewRef.current === "beads") return;
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
    confirmDeleteSession,
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

  const handleNewSession = async (workspace = null, folderFilter = null, currentFilterTab = null) => {
    const isPeriodic = currentFilterTab === FILTER_TAB.PERIODIC;

    // If a specific workspace is provided, create session directly in that workspace
    if (workspace) {
      setShowSidebar(false);
      const result = await newSession({
        workingDir: workspace.working_dir,
        acpServer: workspace.acp_server,
      });
      if (result?.sessionId && isPeriodic) {
        await applyPeriodicConfig(result.sessionId);
      }
      // Handle creation result
      if (result?.errorCode === "session_creation_timeout") {
        showToast({
          style: "warning",
          title: result.retrying
            ? "Agent is busy \u2014 retrying automatically\u2026"
            : (result.error || "Agent is busy"),
          duration: result.retrying ? 30000 : 5000,
        });
      } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
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
        if (result?.sessionId && isPeriodic) {
          await applyPeriodicConfig(result.sessionId);
        }
        if (result?.errorCode === "session_creation_timeout") {
          showToast({
            style: "warning",
            title: result.retrying
              ? "Agent is busy \u2014 retrying automatically\u2026"
              : (result.error || "Agent is busy"),
            duration: result.retrying ? 30000 : 5000,
          });
        } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
          setSettingsDialog({ isOpen: true, forceOpen: true });
        } else if (result?.sessionId) {
          // Switch away from the beads panel so the new conversation is shown.
          setMainView("conversation");
          setTimeout(() => {
            if (chatInputRef.current) chatInputRef.current.focus();
          }, 100);
        }
      } else if (filteredWs.length > 1) {
        setPendingPeriodicTab(currentFilterTab);
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
      setPendingPeriodicTab(currentFilterTab);
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
      if (result?.sessionId && isPeriodic) {
        await applyPeriodicConfig(result.sessionId);
      }
      // Handle creation result
      if (result?.errorCode === "session_creation_timeout") {
        showToast({
          style: "warning",
          title: result.retrying
            ? "Agent is busy \u2014 retrying automatically\u2026"
            : (result.error || "Agent is busy"),
          duration: result.retrying ? 30000 : 5000,
        });
      } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
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
    const isPeriodic = pendingPeriodicTab === FILTER_TAB.PERIODIC;
    setPendingPeriodicTab(null);
    const result = await newSession({
      workingDir: workspace.working_dir,
      acpServer: workspace.acp_server,
    });
    if (result?.sessionId && isPeriodic) {
      await applyPeriodicConfig(result.sessionId);
    }
    // Handle creation result
    if (result?.errorCode === "session_creation_timeout") {
      showToast({
        style: "warning",
        title: result.retrying
          ? "Agent is busy \u2014 retrying automatically\u2026"
          : (result.error || "Agent is busy"),
        duration: result.retrying ? 30000 : 5000,
      });
    } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
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

  const handleShowWorkspacesForFolder = useCallback((workingDir, tab) => {
    if (configReadonly) return;
    setWorkspacesDialog({ isOpen: true, workingDir, tab });
  }, [configReadonly]);

  const handleShowKeyboardShortcuts = () => {
    setKeyboardShortcutsDialog({ isOpen: true });
  };

  // Ref to track queue panel auto-close timer after adding
  const queuePanelAutoCloseTimerRef = useRef(null);

  // Queue dropdown handlers
  const handleToggleQueueDropdown = useCallback(() => {
    // Cancel any auto-close timer when user manually toggles
    if (queuePanelAutoCloseTimerRef.current) {
      clearTimeout(queuePanelAutoCloseTimerRef.current);
      queuePanelAutoCloseTimerRef.current = null;
    }
    if (!showQueueDropdown) {
      // Opening - fetch latest queue messages
      fetchQueueMessages();
    }
    setShowQueueDropdown((prev) => !prev);
  }, [showQueueDropdown, fetchQueueMessages]);

  const handleCloseQueueDropdown = useCallback(() => {
    // Cancel any auto-close timer when closing
    if (queuePanelAutoCloseTimerRef.current) {
      clearTimeout(queuePanelAutoCloseTimerRef.current);
      queuePanelAutoCloseTimerRef.current = null;
    }
    setShowQueueDropdown(false);
  }, []);

  const handleDeleteQueueMessage = useCallback(
    async (messageId) => {
      setIsDeletingQueueMessage(true);
      try {
        await deleteQueueMessage(messageId);
      } finally {
        setIsDeletingQueueMessage(false);
      }
    },
    [deleteQueueMessage],
  );

  const handleMoveQueueMessage = useCallback(
    async (messageId, direction) => {
      setIsMovingQueueMessage(true);
      try {
        await moveQueueMessage(messageId, direction);
      } finally {
        setIsMovingQueueMessage(false);
      }
    },
    [moveQueueMessage],
  );

  // Handle adding message to queue (with optional images and files)
  // Called from ChatInput with message text, images, and files
  const handleAddToQueue = useCallback(
    async (message, images = [], files = []) => {
      // Allow queueing if there's text OR images OR files (or any combination)
      const hasContent =
        message?.trim() || images.length > 0 || files.length > 0;
      if (!hasContent || isAddingToQueue) return { success: false };

      setIsAddingToQueue(true);
      try {
        // Extract image and file IDs from the objects
        const imageIds = images.map((img) => img.id).filter(Boolean);
        const fileIds = files.map((f) => f.id).filter(Boolean);
        const result = await addToQueue(message, imageIds, fileIds);
        if (result.success) {
          // Clear the draft after successful addition
          // Note: Images are cleared by ChatInput on success
          updateDraft(activeSessionId, "");

          // Show queue toast feedback
          showToast({ style: "info", title: "Message queued", duration: 2000, dismissable: false });

          // Trigger badge pulse animation
          setQueueBadgePulse(true);
          setTimeout(() => setQueueBadgePulse(false), 600);

          // Open queue panel briefly to show the new message
          fetchQueueMessages();
          setShowQueueDropdown(true);

          // Clear any existing auto-close timer
          if (queuePanelAutoCloseTimerRef.current) {
            clearTimeout(queuePanelAutoCloseTimerRef.current);
          }

          // Auto-close the queue panel after 1.5 seconds
          queuePanelAutoCloseTimerRef.current = setTimeout(() => {
            setShowQueueDropdown(false);
            queuePanelAutoCloseTimerRef.current = null;
          }, 1500);

          return { success: true };
        }
        return { success: false, error: result.error };
      } finally {
        setIsAddingToQueue(false);
      }
    },
    [
      isAddingToQueue,
      addToQueue,
      updateDraft,
      activeSessionId,
      fetchQueueMessages,
      showToast,
    ],
  );

  // Auto-hide queue dropdown when certain events occur
  useEffect(() => {
    if (!showQueueDropdown) return;

    // Close when settings or workspaces dialog opens
    if (settingsDialog.isOpen || workspacesDialog.isOpen) {
      setShowQueueDropdown(false);
    }
  }, [showQueueDropdown, settingsDialog.isOpen, workspacesDialog.isOpen]);

  // Close queue dropdown when sidebar expands (on mobile)
  useEffect(() => {
    if (showQueueDropdown && showSidebar) {
      setShowQueueDropdown(false);
    }
  }, [showQueueDropdown, showSidebar]);

  // Listen for queue updates from WebSocket to refresh the dropdown
  useEffect(() => {
    const handleQueueUpdate = () => {
      if (showQueueDropdown) {
        fetchQueueMessages();
      }
    };
    window.addEventListener("mitto:queue_updated", handleQueueUpdate);
    return () => {
      window.removeEventListener("mitto:queue_updated", handleQueueUpdate);
    };
  }, [showQueueDropdown, fetchQueueMessages]);

  // Helper function to compare plan entries
  const arePlanEntriesEqual = useCallback((a, b) => {
    if (!a && !b) return true;
    if (!a || !b) return false;
    if (a.length !== b.length) return false;
    // Compare each entry by content, status, and priority
    for (let i = 0; i < a.length; i++) {
      if (
        a[i].content !== b[i].content ||
        a[i].status !== b[i].status ||
        a[i].priority !== b[i].priority
      ) {
        return false;
      }
    }
    return true;
  }, []);

  // Listen for plan updates from WebSocket - store per session in the map
  // When all tasks are completed, erase the plan after a delay
  useEffect(() => {
    const handlePlanUpdate = (event) => {
      const { sessionId, entries } = event.detail;
      if (!sessionId) return;

      // Check if this is a new plan (has entries) or an update to existing
      const hasEntries = entries && entries.length > 0;

      // Get existing entries for comparison
      const existingEntries = planEntriesMap[sessionId] || [];

      // Check if the plan has actually changed
      const hasChanged = !arePlanEntriesEqual(existingEntries, entries || []);

      // If nothing changed, skip all updates
      if (!hasChanged) {
        console.log(
          `[Plan] No changes for session ${sessionId}, skipping update`,
        );
        return;
      }

      // Check if all tasks are completed
      const allCompleted =
        hasEntries && entries.every((e) => e.status === "completed");

      // Cancel any existing completion timer for this session
      if (planCompletionTimersRef.current[sessionId]) {
        clearTimeout(planCompletionTimersRef.current[sessionId]);
        delete planCompletionTimersRef.current[sessionId];
      }

      if (allCompleted) {
        // All tasks completed - update entries to show completion, then schedule erasure
        console.log(
          `[Plan] All tasks completed for session ${sessionId}, scheduling erasure in ${PLAN_COMPLETION_ERASE_DELAY}ms`,
        );

        // Update entries to show completed state
        setPlanEntriesMap((prev) => ({
          ...prev,
          [sessionId]: entries || [],
        }));

        // Remove from expiration tracking if present
        setPlanExpirationMap((prev) => {
          const { [sessionId]: _, ...rest } = prev;
          return rest;
        });

        // Schedule plan erasure after delay
        planCompletionTimersRef.current[sessionId] = setTimeout(() => {
          console.log(`[Plan] Erasing completed plan for session ${sessionId}`);
          delete planCompletionTimersRef.current[sessionId];

          // Close panel first (triggers CSS transition)
          if (sessionId === activeSessionId) {
            setShowPlanPanel(false);
            setPlanUserPinned(false);
          }

          // Wait for panel close animation (300ms transition) before removing entries
          setTimeout(() => {
            setPlanEntriesMap((prevEntries) => {
              const { [sessionId]: _, ...restEntries } = prevEntries;
              return restEntries;
            });
          }, 350); // Slightly longer than 300ms transition to ensure it completes
        }, PLAN_COMPLETION_ERASE_DELAY);

        return;
      }

      // Store plan entries for this session in the map
      setPlanEntriesMap((prev) => ({
        ...prev,
        [sessionId]: entries || [],
      }));

      // Reset expiration tracking when new/updated plan with incomplete tasks is received
      if (hasEntries) {
        setPlanExpirationMap((prev) => {
          const existing = prev[sessionId];
          if (existing) {
            console.log(
              `[Plan] New/updated plan for session ${sessionId}, resetting expiration tracking`,
            );
            const { [sessionId]: _, ...rest } = prev;
            return rest;
          }
          return prev;
        });
      }

      // Auto-expand the panel if this is the active session and not already pinned
      if (sessionId === activeSessionId && !planUserPinned && hasEntries) {
        setShowPlanPanel(true);
      }
    };
    window.addEventListener("mitto:plan_update", handlePlanUpdate);
    return () => {
      window.removeEventListener("mitto:plan_update", handlePlanUpdate);
    };
  }, [activeSessionId, planUserPinned, planEntriesMap, arePlanEntriesEqual]);

  // Reset panel state (but not entries) when switching sessions
  // The entries are preserved in planEntriesMap and will show the badge indicator
  useEffect(() => {
    setShowPlanPanel(false);
    setPlanUserPinned(false);
  }, [activeSessionId]);

  // Plan panel handlers
  const handleTogglePlanPanel = useCallback(() => {
    setShowPlanPanel((prev) => {
      if (!prev) {
        // Opening - mark as user pinned
        setPlanUserPinned(true);
      }
      return !prev;
    });
  }, []);

  const handleClosePlanPanel = useCallback(() => {
    setShowPlanPanel(false);
    setPlanUserPinned(false);
  }, []);

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

  // Track user messages for plan expiration - called when user sends a prompt
  const trackUserMessageForPlanExpiration = useCallback(
    (sessionId) => {
      if (!sessionId) return;

      setPlanExpirationMap((prev) => {
        const existing = prev[sessionId];
        if (!existing?.completedAt) {
          // No completed plan being tracked for this session
          return prev;
        }

        const newCount = (existing.messagesAfterCompletion || 0) + 1;
        console.log(
          `[Plan Expiration] User message sent for session ${sessionId}, count: ${newCount}/${PLAN_EXPIRATION_MESSAGE_THRESHOLD}`,
        );

        if (newCount >= PLAN_EXPIRATION_MESSAGE_THRESHOLD) {
          // Threshold reached - expire the plan
          console.log(
            `[Plan Expiration] Threshold reached for session ${sessionId}, expiring plan`,
          );

          // Remove from expiration tracking
          const { [sessionId]: _, ...rest } = prev;

          // Schedule plan removal with graceful animation:
          // 1. Close panel first (triggers CSS transition)
          // 2. Wait for transition to complete (300ms)
          // 3. Then remove entries from state
          setTimeout(() => {
            // Close panel if it's showing this session's plan
            if (sessionId === activeSessionId) {
              setShowPlanPanel(false);
              setPlanUserPinned(false);
            }

            // Wait for panel close animation (300ms transition) before removing entries
            setTimeout(() => {
              setPlanEntriesMap((prevEntries) => {
                const { [sessionId]: __, ...restEntries } = prevEntries;
                return restEntries;
              });
            }, 350); // Slightly longer than 300ms transition to ensure it completes
          }, 0);

          return rest;
        }

        // Update message count
        return {
          ...prev,
          [sessionId]: {
            ...existing,
            messagesAfterCompletion: newCount,
          },
        };
      });
    },
    [activeSessionId, PLAN_EXPIRATION_MESSAGE_THRESHOLD],
  );

  // Wrapper for sendPrompt that tracks messages for plan expiration
  const handleSendPrompt = useCallback(
    async (message, images = [], files = [], options = {}) => {
      // Track this message for plan expiration before sending
      trackUserMessageForPlanExpiration(activeSessionId);

      // Call the original sendPrompt
      return sendPrompt(message, images, files, options);
    },
    [sendPrompt, trackUserMessageForPlanExpiration, activeSessionId],
  );

  // Handler for prompts dropdown open - refreshes workspace prompts (which now include all sources)
  const handlePromptsOpen = useCallback(() => {
    if (sessionInfo?.working_dir) {
      fetchWorkspacePrompts(sessionInfo.working_dir, false);
    }
  }, [
    sessionInfo?.working_dir,
    fetchWorkspacePrompts,
  ]);

  const handleSelectSession = (sessionId) => {
    switchSession(sessionId);
    setShowSidebar(false);
    setShowSidePanel(false);
    setMainView("conversation");
  };

  // Handle Beads button — switch main view to the beads panel for the given workspace
  const handleBeadsOpen = useCallback((workingDir) => {
    setBeadsWorkingDir(workingDir);
    setMainView("beads");
    // On mobile the beads button lives inside the sidebar overlay; close it so
    // the beads view is not obscured by the still-open left side panel.
    setShowSidebar(false);
  }, []);

  // Open the beads view focused on a specific issue (used by the conversation
  // properties panel's linked-issue link). The nonce bump lets BeadsView
  // re-select even when the same issue is opened again.
  const handleOpenBeadsIssue = useCallback((issueId, workingDir) => {
    if (!issueId || !workingDir) return;
    setBeadsWorkingDir(workingDir);
    setBeadsInitialIssueId(issueId);
    setBeadsSelectNonce((n) => n + 1);
    setMainView("beads");
    setShowSidebar(false);
    setShowSidePanel(false);
  }, []);

  // Handle badge click action - calls API to execute configured command
  const handleBadgeClick = useCallback(
    async (workspacePath) => {
      if (!badgeClickEnabled || !workspacePath) return;

      try {
        const res = await authFetch(apiUrl("/api/badge-click"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ workspace_path: workspacePath }),
        });

        if (!res.ok) {
          const data = await res.json();
          showToast({ style: "error", title: data.error || "Failed to open folder" });
        } else {
          const data = await res.json();
          if (!data.success && data.error) {
            showToast({ style: "error", title: data.error });
          }
        }
      } catch (err) {
        showToast({ style: "error", title: "Failed to open folder: " + err.message });
      }
    },
    [badgeClickEnabled, showToast],
  );

  // Handle folder open action - calls API to open workspace folder
  const handleFolderOpen = useCallback(
    async (workspacePath) => {
      if (!badgeClickEnabled || !workspacePath) return;

      try {
        const res = await authFetch(apiUrl("/api/badge-click"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ workspace_path: workspacePath, action: "folder" }),
        });

        if (!res.ok) {
          const data = await res.json();
          showToast({ style: "error", title: data.error || "Failed to open folder" });
        } else {
          const data = await res.json();
          if (!data.success && data.error) {
            showToast({ style: "error", title: data.error });
          }
        }
      } catch (err) {
        showToast({ style: "error", title: "Failed to open folder: " + err.message });
      }
    },
    [badgeClickEnabled, showToast],
  );

  // Handle terminal action - calls API to open terminal at workspace path
  const handleTerminalClick = useCallback(
    async (workspacePath) => {
      if (!terminalActionEnabled || !workspacePath) return;

      try {
        const res = await authFetch(apiUrl("/api/badge-click"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ workspace_path: workspacePath, action: "terminal" }),
        });

        if (!res.ok) {
          const data = await res.json();
          showToast({ style: "error", title: data.error || "Failed to open terminal" });
        } else {
          const data = await res.json();
          if (!data.success && data.error) {
            showToast({ style: "error", title: data.error });
          }
        }
      } catch (err) {
        showToast({ style: "error", title: "Failed to open terminal: " + err.message });
      }
    },
    [terminalActionEnabled, showToast],
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
    // If confirmation is disabled, delete immediately
    if (!confirmDeleteSession) {
      // Clean up plan entries, expiration tracking, and completion timers for this session
      setPlanEntriesMap((prev) => {
        const { [session.session_id]: _, ...rest } = prev;
        return rest;
      });
      setPlanExpirationMap((prev) => {
        const { [session.session_id]: _, ...rest } = prev;
        return rest;
      });
      if (planCompletionTimersRef.current[session.session_id]) {
        clearTimeout(planCompletionTimersRef.current[session.session_id]);
        delete planCompletionTimersRef.current[session.session_id];
      }
      await removeSession(session.session_id);
      fetchStoredSessions();
      return;
    }
    // Otherwise show the confirmation dialog
    setDeleteDialog({ isOpen: true, session });
  };

  const handleConfirmDelete = async () => {
    const session = deleteDialog.session;
    if (!session) return;

    // Close the dialog first
    setDeleteDialog({ isOpen: false, session: null });

    // Clean up plan entries, expiration tracking, and completion timers for this session
    setPlanEntriesMap((prev) => {
      const { [session.session_id]: _, ...rest } = prev;
      return rest;
    });
    setPlanExpirationMap((prev) => {
      const { [session.session_id]: _, ...rest } = prev;
      return rest;
    });
    if (planCompletionTimersRef.current[session.session_id]) {
      clearTimeout(planCompletionTimersRef.current[session.session_id]);
      delete planCompletionTimersRef.current[session.session_id];
    }

    // removeSession handles: closing WebSocket, updating local state,
    // switching to another session (or creating new if none left), and calling DELETE API
    await removeSession(session.session_id);

    // Refresh the stored sessions list
    fetchStoredSessions();
  };

  const handlePinSession = async (session, pinned) => {
    await pinSession(session.session_id, pinned);
  };

  const handleArchiveSession = async (session, archived) => {
    await archiveSession(session.session_id, archived);

    if (!archived) {
      // When unarchiving, switch to conversations tab and select the session
      setFilterTab(FILTER_TAB.CONVERSATIONS);
      switchSession(session.session_id);
    } else if (session.session_id === activeSessionId) {
      // When archiving the active session, switch to another session in the same tab.
      // The session_archived WebSocket event handler also handles this, but we do it here
      // too (via switchSession for a full load) in case the event arrives late.
      const currentTab = getFilterTab();
      const allSess = computeAllSessions(activeSessions, storedSessions);
      const tabFiltered = allSess.filter((s) => {
        if (s.session_id === session.session_id) return false; // exclude the one being archived
        if (currentTab === FILTER_TAB.ARCHIVED) return s.archived;
        if (currentTab === FILTER_TAB.PERIODIC) return !s.archived && s.periodic_enabled;
        return !s.archived && !s.periodic_enabled; // conversations tab
      });
      if (tabFiltered.length > 0) {
        switchSession(tabFiltered[0].session_id);
      }
      // If no sessions left in this tab, the session_archived WebSocket event handler
      // will call setActiveSessionId(null) to clear the active session.
    }
  };

  // Send a context-menu prompt to a specific conversation by enqueueing its full
  // text. The queue delivers it to the agent when the conversation is idle, so
  // this works for any conversation (not just the active one).
  const handleSendPromptToConversation = useCallback(
    async (session, prompt) => {
      const sessionId = session?.session_id;
      const text = prompt?.prompt;
      if (!sessionId || !text) return;
      try {
        const res = await secureFetch(
          apiUrl(`/api/sessions/${sessionId}/queue`),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ message: text }),
          },
        );
        if (res.ok || res.status === 201) {
          showToast({
            style: "success",
            title: `Sent "${prompt.name}" to conversation`,
            duration: 3000,
          });
        } else {
          const data = await res.json().catch(() => ({}));
          showToast({
            style: "warning",
            title: data.message || "Failed to send prompt",
            duration: 4000,
          });
        }
      } catch (err) {
        console.error("Failed to send prompt to conversation:", err);
        showToast({
          style: "error",
          title: "Failed to send prompt",
          duration: 4000,
        });
      }
    },
    [showToast],
  );


  return html`
    <div class="h-screen-safe flex">
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
            const hasServers = config?.acp_servers && config.acp_servers.length > 0;
            const noWorkspaces = !config?.workspaces || config.workspaces.length === 0;
            if (hasServers && noWorkspaces) {
              setWorkspacesDialog({ isOpen: true });
              return;
            }
          } catch (err) {
            console.error("[AgentDiscovery] Failed to check config on close:", err);
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
              const hasServers = config.acp_servers && config.acp_servers.length > 0;
              const noWorkspaces = !config.workspaces || config.workspaces.length === 0;
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
        onClose=${() => setSettingsDialog({ isOpen: false, forceOpen: false })}
        showToast=${showToast}
        onSave=${async () => {
          // Refresh workspaces after saving
          refreshWorkspaces();
          // Reload config to update prompts and UI settings (invalidate cache first)
          invalidateConfigCache();
          try {
            const config = await fetchConfig();
            if (config) {
              // Reload UI settings
              setConfirmDeleteSession(
                config?.ui?.confirmations?.delete_session !== false,
              );
              // Reload badge/folder click command (macOS only)
              if (typeof window.mittoPickFolder === "function") {
                setBadgeClickCommand(
                  config?.ui?.mac?.badge_click_action?.command || "open ${MITTO_WORKING_DIR}",
                );
                setTerminalActionCommand(
                  config?.ui?.mac?.terminal_action?.command || "open -a Terminal ${MITTO_WORKING_DIR}",
                );
              }
              // Reload input font family setting
              setInputFontFamily(
                config?.ui?.web?.input_font_family || "system",
              );
              // Reload input font size setting
              setInputFontSize(
                config?.ui?.web?.input_font_size || "default",
              );
              // Reload send key mode setting
              setSendKeyMode(config?.ui?.web?.send_key_mode || "enter");
              // Reload conversation cycling mode setting
              setConversationCyclingMode(
                config?.ui?.web?.conversation_cycling_mode || CYCLING_MODE.ALL,
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
      />

      <!-- Keyboard Shortcuts Dialog -->
      <${KeyboardShortcutsDialog}
        isOpen=${keyboardShortcutsDialog.isOpen}
        onClose=${() => setKeyboardShortcutsDialog({ isOpen: false })}
      />

      <!-- Unified toast container -->
      <${ToastContainer} toasts=${toasts} onDismiss=${dismissToast} />

      <!-- Sidebar (hidden on mobile by default) -->
      <div
        class="hidden md:block bg-mitto-sidebar border-r border-slate-700 flex-shrink-0 relative"
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
          workspaces=${workspaces}
          theme=${theme}
          onToggleTheme=${toggleTheme}
          fontSize=${fontSize}
          onToggleFontSize=${toggleFontSize}
          onShowSettings=${handleShowSettings}
          onShowWorkspaces=${handleShowWorkspaces}
          onShowWorkspacesForFolder=${handleShowWorkspacesForFolder}
          onShowKeyboardShortcuts=${handleShowKeyboardShortcuts}
          configReadonly=${configReadonly}
          rcFilePath=${rcFilePath}
          badgeClickEnabled=${badgeClickEnabled}
          onBadgeClick=${handleBadgeClick}
          terminalActionEnabled=${terminalActionEnabled}
          onFolderOpen=${handleFolderOpen}
          onTerminalClick=${handleTerminalClick}
          onBeadsOpen=${handleBeadsOpen}
          queueLength=${queueLength}
          onFetchConversationPrompts=${fetchConversationPromptsForSession}
          onSendPromptToConversation=${handleSendPromptToConversation}
          isCreatingSession=${isCreatingSession}
        />
        <!-- Resize handle on right edge -->
        <div
          class="absolute top-0 right-0 w-1 h-full cursor-col-resize hover:bg-blue-500/30 transition-colors z-10 ${isSidebarDragging ? 'bg-blue-500/40' : ''}"
          style="margin-right: -2px;"
          ...${sidebarHandleProps}
          title="Drag to resize sidebar"
        />
      </div>

      <!-- Mobile sidebar overlay -->
      ${showSidebar &&
      html`
        <div class="md:hidden fixed inset-0 z-40 flex">
          <div class="w-80 bg-mitto-sidebar flex-shrink-0 shadow-2xl">
            <${SessionList}
              activeSessions=${activeSessions}
              storedSessions=${storedSessions}
              activeSessionId=${activeSessionId}
              onSelect=${handleSelectSession}
              onNewSession=${handleNewSession}
              onRename=${handleOpenSessionProperties}
              onDelete=${handleDeleteSession}
              onArchive=${handleArchiveSession}
              onClose=${() => setShowSidebar(false)}
              workspaces=${workspaces}
              theme=${theme}
              onToggleTheme=${toggleTheme}
              fontSize=${fontSize}
              onToggleFontSize=${toggleFontSize}
              onShowSettings=${handleShowSettings}
              onShowWorkspaces=${handleShowWorkspaces}
              onShowWorkspacesForFolder=${handleShowWorkspacesForFolder}
              onShowKeyboardShortcuts=${handleShowKeyboardShortcuts}
              configReadonly=${configReadonly}
              rcFilePath=${rcFilePath}
              badgeClickEnabled=${badgeClickEnabled}
              onBadgeClick=${handleBadgeClick}
              terminalActionEnabled=${terminalActionEnabled}
              onFolderOpen=${handleFolderOpen}
              onTerminalClick=${handleTerminalClick}
              onBeadsOpen=${handleBeadsOpen}
              queueLength=${queueLength}
              onFetchConversationPrompts=${fetchConversationPromptsForSession}
              onSendPromptToConversation=${handleSendPromptToConversation}
              isCreatingSession=${isCreatingSession}
            />
          </div>
          <div
            class="flex-1 bg-black/50"
            onClick=${() => setShowSidebar(false)}
          />
        </div>
      `}

      <!-- Main content area: beads view or conversation -->
      ${mainView === "beads" && beadsWorkingDir
        ? html`
          <div class="flex-1 flex flex-col min-w-0 overflow-hidden bg-mitto-bg">
            <${BeadsView}
              workingDir=${beadsWorkingDir}
              onClose=${() => setMainView("conversation")}
              showToast=${showToast}
              onFetchBeadsPrompts=${fetchBeadsPromptsForWorkspace}
              onRunBeadsPrompt=${handleRunBeadsPrompt}
              onFetchBeadsListPrompts=${fetchBeadsListPromptsForWorkspace}
              onRunBeadsListPrompt=${handleRunBeadsListPrompt}
              onShowSidebar=${() => setShowSidebar(true)}
              onOpenConfig=${window.mittoIsExternal === true ? undefined : () => handleShowWorkspacesForFolder(beadsWorkingDir, "beads")}
              issueSessionMap=${beadsIssueSessionMap}
              onOpenConversation=${handleSelectSession}
              initialSelectedIssueId=${beadsInitialIssueId}
              initialSelectNonce=${beadsSelectNonce}
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
          class="relative p-4 bg-mitto-sidebar border-b border-slate-700 flex items-center gap-3 flex-shrink-0"
        >
          <button
            class="md:hidden p-2 hover:bg-slate-700 rounded-lg transition-colors"
            onClick=${() => setShowSidebar(true)}
          >
            <${MenuIcon} className="w-6 h-6" />
          </button>
          <h1
            class="font-bold text-xl truncate max-w-[300px] sm:max-w-[400px] no-underline ${!activeSessionId
              ? "text-gray-500"
              : "cursor-pointer hover:text-blue-400 transition-colors"}"
            onClick=${activeSessionId ? handleToggleSidePanel : undefined}
            title=${activeSessionId ? "Click to view properties" : ""}
          >
            ${activeSessionId
              ? sessionInfo?.name || "New conversation"
              : "No Active Session"}
          </h1>
          <div class="ml-auto flex items-center gap-2">
            <!-- Status indicator dot (matches session list style) -->
            <span
              class="w-2 h-2 rounded-full flex-shrink-0 ${isStreaming ? "bg-blue-400 streaming-indicator" : connected ? "bg-green-400" : "bg-amber-400"}"
              title=${isStreaming ? "Streaming" : connected ? "Connected" : "Not connected"}
            ></span>
            <!-- Unified side panel toggle -->
            <button
              onClick=${handleToggleSidePanel}
              class="p-1.5 rounded hover:bg-slate-700 transition-colors ${showSidePanel ? "bg-slate-700 text-blue-400" : "text-slate-400 hover:text-slate-200"}"
              title="Session details"
            >
              <${SidePanelIcon} className="w-4 h-4" />
            </button>
          </div>
        </div>

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
          />
        </div>
        <!-- End of messages wrapper -->

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
          <div
            class="flex items-center justify-center gap-2 py-2 text-sm text-yellow-500"
          >
            <span
              class="w-3 h-3 border-2 border-yellow-500 border-t-transparent rounded-full animate-spin"
            ></span>
            Reconnecting to AI agent...
          </div>
        `}

        <!-- Archive reason banner (shown when conversation is archived and has a reason) -->
        <!-- Uses the same balloon style as system messages for visual consistency -->
        ${sessionInfo?.archived &&
        sessionInfo?.archive_reason &&
        html`
          <div class="flex justify-center mb-3">
            <div
              class="text-xs text-gray-500 bg-slate-800/50 px-3 py-1 rounded-full"
            >
              ${getArchiveReasonText(
                sessionInfo.archive_reason,
                sessionInfo.archived_at,
              )}
            </div>
          </div>
        `}

        <!-- Input Area Container (relative for QueueDropdown positioning) -->
        <div class="relative flex-shrink-0">
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
            queueLength=${queueLength}
            queueConfig=${queueConfig}
            onAddToQueue=${handleAddToQueue}
            onToggleQueue=${handleToggleQueueDropdown}
            showQueueDropdown=${showQueueDropdown}
            actionButtons=${actionButtons}
            availableCommands=${availableCommands}
            periodicEnabled=${sessionInfo?.periodic_enabled || false}
            agentSupportsImages=${sessionInfo?.agent_supports_images ?? false}
            acpReady=${connected && sessionInfo ? (sessionInfo.acp_ready ?? true) : true}
            gcSuspended=${sessionInfo?.gc_suspended || false}
            onResume=${() => ensureResumed(activeSessionId)}
            activeUIPrompt=${activeUIPrompt}
            onUIPromptAnswer=${(requestId, optionId, label, freeText) =>
              sendUIPromptAnswer(activeSessionId, requestId, optionId, label, freeText)}
            workingDir=${sessionInfo?.working_dir || ""}
            sendKeyMode=${sendKeyMode}
            configOptions=${configOptions}
            onSetConfigOption=${setConfigOption}
            contextUsage=${sessionInfo?.context_usage ?? null}
            tokenUsage=${sessionInfo?.usage ?? null}
          />
        </div>
      </div>
      `}

      <!-- Unified Session Panel (fixed overlay on right) -->
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
        showToast=${showToast}
      />
    </div>
  `;
}

// =============================================================================
// Mount Application
// =============================================================================

render(html`<${App} />`, document.getElementById("app"));
