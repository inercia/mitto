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
  useSessionNavigation,
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
import { BeadsView, BeadsDetailPanel } from "./components/BeadsView.js";

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
  // Quick "new task" create panel shown as an overlay over the current content
  // (e.g. a conversation) via the New task shortcut, without switching to the
  // beads list view. { open, workingDir } — workingDir is kept during the
  // close animation so only `open` is flipped on dismiss.
  const [quickCreate, setQuickCreate] = useState({ open: false, workingDir: null });
  // mainView controls what is shown in the right-side area: "conversation" or "beads"
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
  const [pendingPeriodicTab, setPendingPeriodicTab] = useState(null); // Track if new session should be periodic
  const [settingsDialog, setSettingsDialog] = useState({
    isOpen: false,
    forceOpen: false,
  }); // Settings dialog
  const [workspacesDialog, setWorkspacesDialog] = useState({ isOpen: false }); // Workspaces management dialog
  const [keyboardShortcutsDialog, setKeyboardShortcutsDialog] = useState({
    isOpen: false,
  }); // Keyboard shortcuts dialog
  // Workspace prompts: fetch/cache, predefined (dropup) subset, and per-session helpers.
  // (Extracted to hooks/useWorkspacePrompts.js)
  const {
    workspacePrompts,
    predefinedPrompts,
    fetchWorkspacePrompts,
    fetchConversationPromptsForSession,
  } = useWorkspacePrompts({
    workingDir: sessionInfo?.working_dir,
    activeSessionId,
  });

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

  // Beads integration: view state, issue-session map, prompt helpers, handlers.
  // (Extracted to hooks/useBeadsIntegration.js)
  const {
    beadsWorkingDir,
    beadsInitialIssueId,
    beadsSelectNonce,
    beadsCreateNonce,
    beadsIssueSessionMap,
    fetchBeadsPromptsForWorkspace,
    fetchBeadsListPromptsForWorkspace,
    handleRunBeadsPrompt,
    handleRunBeadsListPrompt,
    handleBeadsOpen,
    handleBeadsCreate,
    handleOpenBeadsIssue,
  } = useBeadsIntegration({
    allSessions,
    workspaces,
    newSession,
    showToast,
    setMainView,
    setShowSidebar,
    setShowSidePanel,
  });

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
      clearPlanForSession(session.session_id);
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
        class="hidden md:block bg-mitto-sidebar border-r border-slate-700 shrink-0 relative"
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
          <div class="w-80 bg-mitto-sidebar shrink-0 shadow-2xl">
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
              initialCreateNonce=${beadsCreateNonce}
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
          class="relative p-4 bg-mitto-sidebar border-b border-slate-700 flex items-center gap-3 shrink-0"
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
              class="w-2 h-2 rounded-full shrink-0 ${isStreaming ? "bg-blue-400 streaming-indicator" : connected ? "bg-green-400" : "bg-amber-400"}"
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
  `;
}

// =============================================================================
// Mount Application
// =============================================================================

render(html`<${App} />`, document.getElementById("app"));
