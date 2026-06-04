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
  playAgentCompletedSound,
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
} from "./hooks/index.js";

// Import components
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
  ChevronRightIcon,
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
} from "./components/Icons.js";

// Import constants
import {
  KEYBOARD_SHORTCUTS,
  CYCLING_MODE,
  PERIODIC_PROGRESS_STYLE,
  PERIODIC_PROGRESS_COLORS,
  PERIODIC_PROGRESS_URGENT_THRESHOLD,
} from "./constants.js";

// =============================================================================
// Archive Reason Helpers
// =============================================================================

/**
 * Returns a human-readable description of why a session was archived.
 * @param {string} reason - The archive reason code (e.g. "manual", "inactivity", "acp_start_failures")
 * @param {string|null} archivedAt - ISO 8601 timestamp when the session was archived
 * @returns {string}
 */
function getArchiveReasonText(reason, archivedAt) {
  const dateStr = archivedAt ? new Date(archivedAt).toLocaleDateString() : "";
  switch (reason) {
    case "manual":
      return `Archived by user${dateStr ? ` on ${dateStr}` : ""}`;
    case "inactivity":
      return `Auto-archived due to inactivity${dateStr ? ` on ${dateStr}` : ""}`;
    case "acp_start_failures":
      return `Auto-archived: agent failed to start after repeated attempts${dateStr ? ` on ${dateStr}` : ""}`;
    default:
      return `Archived${dateStr ? ` on ${dateStr}` : ""}`;
  }
}

// =============================================================================
// Global Link Click Handler
// =============================================================================

// Intercept clicks on all link types and route them through the viewer.
// All file links in agent responses are now in viewer URL format.
// Backward compatibility is maintained for old file:// and /api/files? links
// from session recordings.
document.addEventListener("click", (e) => {
  // Find the closest anchor element (handles clicks on nested elements inside links)
  const link = e.target.closest("a");
  if (!link) return;

  const href = link.getAttribute("href");
  if (!href) return;

  console.log("[Mitto] Link clicked:", href, "isNativeApp:", isNativeApp());

  // Handle viewer URLs (new format: /viewer.html?ws=...&path=...)
  if (href.includes("/viewer.html?")) {
    console.log("[Mitto] Handling as viewer URL");
    e.preventDefault();
    e.stopPropagation();
    if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
      // macOS native app — open in native viewer window
      const fullUrl = new URL(href, window.location.origin).href;
      window.mittoOpenViewer(fullUrl);
    } else {
      // Web browser — open in new tab
      window.open(href, "_blank", "noopener,noreferrer");
    }
    return;
  }

  // BACKWARD COMPAT: Handle old file:// URLs (from old session recordings)
  if (href.startsWith("file://")) {
    console.log("[Mitto] Handling as file:// URL (backward compat)");
    e.preventDefault();
    e.stopPropagation();
    if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
      const viewerUrl = convertFileURLToViewer(href);
      if (viewerUrl) {
        window.mittoOpenViewer(viewerUrl);
      } else {
        openFileURL(href); // fallback: open with system app
      }
    } else {
      const viewerUrl = convertFileURLToViewer(href);
      if (viewerUrl) {
        window.open(viewerUrl, "_blank", "noopener,noreferrer");
      }
    }
    return;
  }

  // BACKWARD COMPAT: Handle old /api/files? URLs (from old session recordings)
  if (href.includes("/api/files?")) {
    console.log("[Mitto] Handling as /api/files link (backward compat)");
    e.preventDefault();
    e.stopPropagation();
    const viewerUrl = convertHTTPFileURLToViewer(href);
    console.log("[Mitto] Converted to viewer URL:", viewerUrl);
    if (viewerUrl) {
      if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
        window.mittoOpenViewer(new URL(viewerUrl, window.location.origin).href);
      } else {
        window.open(viewerUrl, "_blank", "noopener,noreferrer");
      }
    }
    return;
  }

  // Handle external URLs (http/https) - open in default browser
  if (href.startsWith("http://") || href.startsWith("https://")) {
    // Check if this is an old viewer URL that needs fixing (missing API prefix)
    // Old recordings may have URLs like https://host/viewer.html?... that need
    // to be converted to https://host/mitto/viewer.html?...
    const fixedUrl = fixViewerURLIfNeeded(href);
    if (fixedUrl) {
      console.log("[Mitto] Opening fixed viewer URL:", fixedUrl);
      e.preventDefault();
      e.stopPropagation();
      window.open(fixedUrl, "_blank", "noopener,noreferrer");
      return;
    }

    console.log("[Mitto] Handling as external URL");
    e.preventDefault();
    e.stopPropagation();
    openExternalURL(href);
  }
});

// =============================================================================
// Disable WebView Context Menu (macOS app only)
// =============================================================================

// In the native macOS app, disable the default WebView context menu (which shows "Reload")
// to provide a cleaner, more native app experience. This only applies to areas without
// a custom context menu handler (session items have their own context menu).
if (isNativeApp()) {
  document.addEventListener("contextmenu", (e) => {
    // Allow default behavior for text inputs and textareas (for paste, etc.)
    const tagName = e.target.tagName.toLowerCase();
    if (tagName === "input" || tagName === "textarea") {
      return;
    }
    // Allow custom context menu handlers (session items have data-has-context-menu attribute)
    const hasCustomMenu = e.target.closest("[data-has-context-menu]");
    if (hasCustomMenu) {
      return;
    }
    // Prevent the default WebView context menu
    e.preventDefault();
  });
}
// =============================================================================
// Mouse Position Tracker (for native swipe gesture hit-testing)
// =============================================================================

// Track the last known cursor position so native macOS swipe gestures can
// check whether the cursor is over horizontally scrollable content before
// triggering conversation navigation.
let lastMouseX = 0;
let lastMouseY = 0;
document.addEventListener("mousemove", (e) => {
  lastMouseX = e.clientX;
  lastMouseY = e.clientY;
});

/**
 * Returns true if the element currently under the cursor belongs to a
 * horizontally scrollable container (overflow-x: auto/scroll with actual
 * overflow). Used to suppress swipe-navigation when the user is scrolling
 * a table left/right with a two-finger trackpad gesture.
 */
function isOverHorizontallyScrollable() {
  const el = document.elementFromPoint(lastMouseX, lastMouseY);
  if (!el) return false;
  let node = el;
  while (node) {
    const style = window.getComputedStyle(node);
    const overflowX = style.overflowX;
    if (
      (overflowX === "auto" || overflowX === "scroll") &&
      node.scrollWidth > node.clientWidth
    ) {
      return true;
    }
    node = node.parentElement;
  }
  return false;
}

/**
 * Returns true if a modal dialog overlay is currently open.
 * All modal dialogs use a fixed full-screen z-50 overlay as their backdrop.
 * Used to suppress swipe-navigation when the user is interacting with a dialog.
 */
function isModalDialogOpen() {
  return !!document.querySelector('.fixed.inset-0.z-50');
}

// =============================================================================
// Workspace Badge Component
// =============================================================================

/**
 * A colored badge showing a three-letter abbreviation for a workspace.
 * The color is deterministically generated from the workspace path,
 * or uses custom values if provided.
 *
 * @param {string} path - The workspace directory path
 * @param {string} customColor - Optional custom hex color (e.g., "#ff5500")
 * @param {string} customCode - Optional custom three-letter code
 * @param {string} customName - Optional custom friendly name
 * @param {string} size - Size variant: 'sm', 'md', 'lg' (default: 'md')
 * @param {boolean} showPath - Whether to show the full path below the badge
 */
function WorkspaceBadge({
  path,
  customColor,
  customCode,
  customName,
  size = "md",
  showPath = false,
  className = "",
}) {
  if (!path) return null;

  const { abbreviation, color, displayName } = getWorkspaceVisualInfo(
    path,
    customColor,
    customCode,
    customName,
  );

  const sizeClasses = {
    sm: "w-8 h-8 text-xs",
    md: "w-10 h-10 text-sm",
    lg: "w-12 h-12 text-base",
  };

  return html`
    <div class="flex items-center gap-3 ${className}">
      <div
        class="flex items-center justify-center rounded-lg font-bold ${sizeClasses[
          size
        ] || sizeClasses.md}"
        style=${{
          backgroundColor: color.background,
          color: color.text,
        }}
        title=${path}
      >
        ${abbreviation}
      </div>
      ${showPath &&
      html`
        <div class="min-w-0 flex-1">
          <div class="font-medium text-sm">${displayName}</div>
          <div class="text-xs text-gray-500 truncate" title=${path}>
            ${path}
          </div>
        </div>
      `}
    </div>
  `;
}

/**
 * A pill-shaped workspace badge for compact display.
 * Shows abbreviation and ACP server name (or workspace name if no ACP server).
 * Supports click action to execute a configured command (e.g., open folder in Finder).
 *
 * @param {string} path - The workspace directory path
 * @param {string} customColor - Optional custom hex color (e.g., "#ff5500")
 * @param {string} customCode - Optional custom three-letter code
 * @param {string} customName - Optional custom friendly name
 * @param {string} acpServer - The ACP server name (e.g., "auggie", "claude-code")
 * @param {string} className - Additional CSS classes
 * @param {boolean} clickable - Whether the badge is clickable (default: false)
 * @param {function} onBadgeClick - Optional callback when badge is clicked
 * @param {boolean} hideAbbreviation - When true, hide the 3-letter abbreviation (e.g. in group header when grouping by workspace)
 * @param {boolean} hideAcpServer - When true, show only workspace name, not ACP server (e.g. on items when grouping by ACP server)
 */
function WorkspacePill({
  path,
  customColor,
  customCode,
  customName,
  acpServer,
  className = "",
  clickable = false,
  onBadgeClick,
  hideAbbreviation = false,
  hideAcpServer = false,
}) {
  if (!path) return null;

  const {
    abbreviation,
    color,
    displayName: wsDisplayName,
  } = getWorkspaceVisualInfo(path, customColor, customCode, customName);
  // Display ACP server name if available, otherwise fall back to workspace display name (unless hideAcpServer)
  const displayName = hideAcpServer
    ? wsDisplayName
    : acpServer || wsDisplayName;

  const handleClick = (e) => {
    if (!clickable) return;
    e.stopPropagation(); // Prevent triggering session selection
    if (onBadgeClick) {
      onBadgeClick(path);
    }
  };

  const cursorClass = clickable
    ? "cursor-pointer workspace-pill-clickable"
    : "";

  return html`
    <div
      class="workspace-pill inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${cursorClass} ${className}"
      style=${{
        backgroundColor: color.background,
        color: color.text,
      }}
      title=${clickable ? `Click to open: ${path}` : path}
      onClick=${handleClick}
    >
      ${!hideAbbreviation &&
      html`<span class="font-bold">${abbreviation}</span>`}
      <span class="truncate max-w-[80px]">${displayName}</span>
    </div>
  `;
}

// NOTE: SessionPropertiesDialog has been removed.
// Session properties are now edited via the SessionPanel (right sidebar, Properties tab).

// =============================================================================
// Delete Confirmation Dialog
// =============================================================================

function DeleteDialog({
  isOpen,
  sessionName,
  isActive,
  isStreaming,
  onConfirm,
  onCancel,
}) {
  if (!isOpen) return null;

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${onCancel}
    >
      <div
        class="bg-mitto-sidebar rounded-xl p-6 w-80 shadow-2xl"
        onClick=${(e) => e.stopPropagation()}
      >
        <h3 class="text-lg font-semibold mb-2">Delete Session</h3>
        <p class="text-gray-400 text-sm mb-4">
          Are you sure you want to delete "${sessionName}"?
          ${isStreaming &&
          html`<br /><span class="text-orange-400"
              >⚠️ This session is still receiving a response.</span
            >`}
          ${isActive &&
          !isStreaming &&
          html`<br /><span class="text-yellow-400"
              >This is the active session.</span
            >`}
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            onClick=${onCancel}
            class="px-4 py-2 rounded-lg hover:bg-slate-700 transition-colors"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick=${onConfirm}
            class="px-4 py-2 bg-red-600 hover:bg-red-700 text-white rounded-lg transition-colors"
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  `;
}

// =============================================================================
// Keyboard Shortcuts Dialog
// =============================================================================

function KeyboardShortcutsDialog({ isOpen, onClose }) {
  if (!isOpen) return null;

  // Check if running in the native macOS app
  const isMacApp = typeof window.mittoPickFolder === "function";

  // Handle Escape key to close dialog
  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  // Filter shortcuts based on environment and group by section
  // In browser (not macOS app), hide macOnly shortcuts since they're handled by native menu
  const sections = {};
  KEYBOARD_SHORTCUTS.forEach((shortcut) => {
    // Skip macOnly shortcuts when not in the macOS app
    if (shortcut.macOnly && !isMacApp) {
      return;
    }
    const section = shortcut.section || "General";
    if (!sections[section]) {
      sections[section] = [];
    }
    sections[section].push(shortcut);
  });

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${onClose}
    >
      <div
        class="bg-mitto-sidebar rounded-xl p-6 w-[420px] md:w-[700px] shadow-2xl max-h-[80vh] overflow-y-auto"
        onClick=${(e) => e.stopPropagation()}
      >
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-lg font-semibold">Keyboard Shortcuts</h3>
          <button
            onClick=${onClose}
            class="p-1 hover:bg-slate-700 rounded-lg transition-colors"
            title="Close"
          >
            <${CloseIcon} className="w-5 h-5 text-gray-400 hover:text-white" />
          </button>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          ${Object.entries(sections)
            .sort((a, b) => b[1].length - a[1].length)
            .map(
              ([sectionName, shortcuts]) => html`
                <div key=${sectionName}>
                  <h4
                    class="text-xs font-medium text-gray-400 uppercase tracking-wide mb-2"
                  >
                    ${sectionName}
                  </h4>
                  <div class="space-y-1">
                    ${shortcuts.map(
                      (shortcut) => html`
                        <div
                          key=${shortcut.keys}
                          class="flex items-center justify-between py-2 px-3 rounded-lg bg-slate-700/30"
                        >
                          <div class="flex flex-col gap-0.5">
                            <div class="flex items-center gap-2">
                              <span class="text-gray-300"
                                >${shortcut.description}</span
                              >
                              ${shortcut.macOnly &&
                              html`
                                <span
                                  class="text-[10px] px-1.5 py-0.5 rounded bg-slate-600 text-gray-400"
                                  >macOS app</span
                                >
                              `}
                            </div>
                            ${shortcut.hint &&
                            html`
                              <span class="text-[11px] text-gray-500"
                                >${shortcut.hint}</span
                              >
                            `}
                          </div>
                          <kbd
                            class="px-2 py-1 text-sm font-mono bg-slate-700 rounded border border-slate-600 text-gray-200"
                          >
                            ${shortcut.keys}
                          </kbd>
                        </div>
                      `,
                    )}
                  </div>
                </div>
              `,
            )}
        </div>
        <div class="mt-4 pt-3 border-t border-slate-700 space-y-2">
          <p class="text-xs text-gray-500 text-center">
            On touch devices, swipe left/right to switch conversations
          </p>
          <p class="text-xs text-gray-500 text-center">Press Escape to close</p>
        </div>
      </div>
    </div>
  `;
}

// =============================================================================
// Workspace Selection Dialog
// =============================================================================

// Threshold for showing filter UI and max items with keyboard shortcuts
// When workspace count exceeds this, filter input is shown and only first N items get number keys
const WORKSPACE_FILTER_THRESHOLD = 5;

// Helper to get parent directory from a path
function getParentDir(path) {
  if (!path) return "";
  const normalized = path.replace(/\\/g, "/").replace(/\/$/, "");
  const lastSlash = normalized.lastIndexOf("/");
  return lastSlash > 0 ? normalized.substring(0, lastSlash) : "/";
}

// Helper to get/set folder expansion state from localStorage
function getFolderExpansionState(folderId) {
  try {
    const state = localStorage.getItem(`workspace-folder-${folderId}`);
    return state === null ? true : state === "true"; // Default to expanded
  } catch (e) {
    return true;
  }
}

function setFolderExpansionState(folderId, expanded) {
  try {
    localStorage.setItem(`workspace-folder-${folderId}`, String(expanded));
  } catch (e) {
    // Ignore localStorage errors
  }
}

function WorkspaceDialog({ isOpen, workspaces, onSelect, onCancel }) {
  const [filterText, setFilterText] = useState("");
  const [expandedFolders, setExpandedFolders] = useState({});
  const filterInputRef = useRef(null);

  // Show filter only when there are more than WORKSPACE_FILTER_THRESHOLD workspaces
  const showFilter = workspaces.length > WORKSPACE_FILTER_THRESHOLD;

  // Group workspaces by working_dir (matching conversations list behavior)
  // Each workspace (working_dir + acp_server) is its own group
  // This matches the "workspace" grouping mode in the conversations list
  const groupedWorkspaces = useMemo(() => {
    const groups = new Map();

    workspaces.forEach((ws) => {
      // Use working_dir as the group key (not parent folder)
      // This matches the conversations list grouping logic
      const workingDir = ws.working_dir || "Unknown";

      if (!groups.has(workingDir)) {
        groups.set(workingDir, []);
      }
      groups.get(workingDir).push(ws);
    });

    // Sort workspaces within each group by ACP server name
    groups.forEach((wsArray) => {
      wsArray.sort((a, b) => {
        return (a.acp_server || "").localeCompare(b.acp_server || "");
      });
    });

    // Convert to array and sort by workspace folder name (basename)
    return Array.from(groups.entries())
      .sort(([dirA], [dirB]) => {
        const nameA = dirA ? getBasename(dirA) : "Unknown";
        const nameB = dirB ? getBasename(dirB) : "Unknown";
        return nameA.localeCompare(nameB);
      })
      .map(([workingDir, wsArray]) => ({
        workingDir,
        label: workingDir ? getBasename(workingDir) : "Unknown",
        workspaces: wsArray,
      }));
  }, [workspaces]);

  // Initialize expanded state from localStorage when dialog opens
  useEffect(() => {
    if (isOpen) {
      const initialExpanded = {};
      groupedWorkspaces.forEach(({ workingDir }) => {
        initialExpanded[workingDir] = getFolderExpansionState(workingDir);
      });
      setExpandedFolders(initialExpanded);
    }
  }, [isOpen, groupedWorkspaces]);

  // Filter workspaces based on filter text (match against name, path, and ACP server)
  const filteredGroups = useMemo(() => {
    if (!filterText.trim()) return groupedWorkspaces;

    const lowerFilter = filterText.toLowerCase();

    return groupedWorkspaces
      .map(({ workingDir, label, workspaces: wsArray }) => {
        const filtered = wsArray.filter((ws) => {
          const displayName = ws.name || getBasename(ws.working_dir);
          const matchName = displayName.toLowerCase().includes(lowerFilter);
          const matchPath = ws.working_dir.toLowerCase().includes(lowerFilter);
          const matchServer = (ws.acp_server || "")
            .toLowerCase()
            .includes(lowerFilter);
          return matchName || matchPath || matchServer;
        });

        return { workingDir, label, workspaces: filtered };
      })
      .filter(({ workspaces: wsArray }) => wsArray.length > 0);
  }, [groupedWorkspaces, filterText]);

  // Flatten filtered groups for keyboard shortcuts
  const flatFilteredWorkspaces = useMemo(() => {
    return filteredGroups.flatMap(({ workspaces: wsArray }) => wsArray);
  }, [filteredGroups]);

  // Reset filter when dialog opens
  useEffect(() => {
    if (isOpen) {
      setFilterText("");
    }
  }, [isOpen]);

  // Auto-focus filter input when dialog opens (only if filter is shown)
  useEffect(() => {
    if (isOpen && showFilter && filterInputRef.current) {
      // Focus immediately and also after a delay to win against competing focus events
      filterInputRef.current?.focus();
      // Additional delayed focus to handle cases where other handlers steal focus
      const timerId = setTimeout(() => {
        filterInputRef.current?.focus();
      }, 100);
      return () => clearTimeout(timerId);
    }
  }, [isOpen, showFilter]);

  // Handle keyboard shortcuts (1-5) to select workspaces
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e) => {
      const key = e.key;

      // Escape to cancel
      if (key === "Escape") {
        e.preventDefault();
        onCancel();
        return;
      }

      // Number keys 1-N for quick selection (N = WORKSPACE_FILTER_THRESHOLD)
      // Only trigger if filter is empty (so typing numbers goes to filter when there's text)
      // Check both React state and DOM value to handle race conditions with state updates
      const maxShortcut = String(WORKSPACE_FILTER_THRESHOLD);
      const filterInputHasValue = filterInputRef.current?.value?.length > 0;
      const filterIsEmpty = !filterText && !filterInputHasValue;

      if (key >= "1" && key <= maxShortcut && filterIsEmpty) {
        const index = parseInt(key, 10) - 1;
        if (index < flatFilteredWorkspaces.length) {
          e.preventDefault();
          onSelect(flatFilteredWorkspaces[index]);
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, flatFilteredWorkspaces, filterText, onSelect, onCancel]);

  // Toggle folder expansion
  const toggleFolder = useCallback((workingDir) => {
    setExpandedFolders((prev) => {
      const newExpanded = !prev[workingDir];
      setFolderExpansionState(workingDir, newExpanded);
      return { ...prev, [workingDir]: newExpanded };
    });
  }, []);

  if (!isOpen) return null;

  // Help text varies based on whether filter is shown
  const helpText = showFilter
    ? `Type to filter, or press 1-${WORKSPACE_FILTER_THRESHOLD} to select.`
    : "Click on a workspace or press its number to select it.";

  // Track global index for keyboard shortcuts
  let globalIndex = 0;

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${onCancel}
    >
      <div
        class="bg-mitto-sidebar rounded-xl p-6 w-[420px] max-h-[80vh] overflow-y-auto shadow-2xl"
        onClick=${(e) => e.stopPropagation()}
      >
        <h3 class="text-lg font-semibold mb-2">Select Workspace</h3>
        <p class="text-gray-400 text-sm mb-4">${helpText}</p>

        ${showFilter &&
        html`
          <div class="mb-4">
            <input
              ref=${filterInputRef}
              type="text"
              value=${filterText}
              onInput=${(e) => setFilterText(e.target.value)}
              onKeyDown=${(e) => {
                // Intercept number keys 1-9 to select workspaces quickly
                const num = parseInt(e.key, 10);
                if (
                  num >= 1 &&
                  num <=
                    Math.min(
                      WORKSPACE_FILTER_THRESHOLD,
                      flatFilteredWorkspaces.length,
                    )
                ) {
                  e.preventDefault();
                  const workspace = flatFilteredWorkspaces[num - 1];
                  if (workspace) {
                    onSelect(workspace);
                  }
                }
              }}
              placeholder="Filter workspaces..."
              autofocus
              autocomplete="off"
              class="w-full px-3 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-sm focus:outline-none focus:border-blue-500 placeholder-gray-500"
            />
          </div>
        `}

        <div class="space-y-2">
          ${filteredGroups.length === 0
            ? html`
                <div class="text-center py-4 text-gray-500">
                  No workspaces match your filter.
                </div>
              `
            : filteredGroups.map(
                ({ workingDir, label, workspaces: wsArray }) => {
                  // Auto-expand folders when filtering is active
                  const isExpanded = filterText.trim()
                    ? true
                    : expandedFolders[workingDir] !== false;
                  const showGroupHeader = filteredGroups.length > 1;

                  return html`
                    <div key=${workingDir} class="space-y-1">
                      ${showGroupHeader &&
                      html`
                        <button
                          onClick=${() => toggleFolder(workingDir)}
                          class="w-full px-2 py-1 text-left text-xs text-gray-400 hover:text-gray-300 hover:bg-slate-700/30 rounded transition-colors flex items-center gap-2"
                        >
                          <span class="font-mono"
                            >${isExpanded ? "▼" : "▶"}</span
                          >
                          <span class="truncate" title=${workingDir}>
                            ${label}
                          </span>
                          <span class="text-gray-500">(${wsArray.length})</span>
                        </button>
                      `}
                      ${isExpanded &&
                      wsArray.map((ws) => {
                        const currentIndex = globalIndex++;
                        return html`
                          <button
                            key=${ws.working_dir + "|" + ws.acp_server}
                            onClick=${() => onSelect(ws)}
                            class="w-full p-3 text-left rounded-lg bg-slate-700/50 hover:bg-slate-700 transition-colors flex items-center gap-3 ${showGroupHeader
                              ? "ml-4"
                              : ""}"
                          >
                            <div
                              class="w-8 h-8 flex-shrink-0 ${currentIndex <
                              WORKSPACE_FILTER_THRESHOLD
                                ? "flex items-center justify-center rounded-lg bg-slate-600 text-gray-300 font-mono text-sm"
                                : ""}"
                            >
                              ${currentIndex < WORKSPACE_FILTER_THRESHOLD
                                ? currentIndex + 1
                                : ""}
                            </div>
                            <${WorkspaceBadge}
                              path=${ws.working_dir}
                              customColor=${ws.color}
                              customCode=${ws.code}
                              size="lg"
                            />
                            <div class="flex-1 min-w-0">
                              ${(!showGroupHeader ||
                                (ws.name && ws.name !== label)) &&
                              html`
                                <div class="text-sm font-medium">
                                  ${ws.name || getBasename(ws.working_dir)}
                                </div>
                              `}
                              ${ws.acp_server &&
                              html`
                                <div
                                  class="${showGroupHeader &&
                                  (!ws.name || ws.name === label)
                                    ? "text-sm font-medium"
                                    : "text-xs text-blue-400"}"
                                >
                                  ${ws.acp_server}
                                </div>
                              `}
                              ${!showGroupHeader &&
                              html`
                                <div class="text-xs text-gray-500 truncate">
                                  ${ws.working_dir}
                                </div>
                              `}
                            </div>
                          </button>
                        `;
                      })}
                    </div>
                  `;
                },
              )}
        </div>
        <div class="flex justify-end mt-4">
          <button
            type="button"
            onClick=${onCancel}
            class="px-4 py-2 rounded-lg hover:bg-slate-700 transition-colors"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  `;
}

// SettingsDialog is now imported from ./components/SettingsDialog.js

// =============================================================================
// Context Menu Component
// =============================================================================

// Renders a single context menu entry. Entries with a non-empty `submenu`
// array expand a flyout submenu on hover (positioned to the right, flipping
// left or shifting up when it would overflow the viewport).
function ContextMenuItem({ item, onClose }) {
  const hasSubmenu = !!(item.submenu && item.submenu.length > 0);
  const [submenuOpen, setSubmenuOpen] = useState(false);
  const [submenuPos, setSubmenuPos] = useState({ left: 0, top: 0 });
  const itemRef = useRef(null);
  const closeTimerRef = useRef(null);

  const openSubmenu = () => {
    if (!hasSubmenu) return;
    clearTimeout(closeTimerRef.current);
    if (itemRef.current) {
      const rect = itemRef.current.getBoundingClientRect();
      const submenuWidth = 180;
      const submenuHeight = item.submenu.length * 38 + 8;
      // Prefer opening to the right; flip to the left if it would overflow
      let left = rect.right - 4;
      if (left + submenuWidth > window.innerWidth) {
        left = rect.left - submenuWidth + 4;
      }
      // Shift up if it would overflow the bottom of the viewport
      let top = rect.top;
      if (top + submenuHeight > window.innerHeight) {
        top = Math.max(8, window.innerHeight - submenuHeight - 8);
      }
      setSubmenuPos({ left, top });
    }
    setSubmenuOpen(true);
  };

  const scheduleClose = () => {
    clearTimeout(closeTimerRef.current);
    closeTimerRef.current = setTimeout(() => setSubmenuOpen(false), 150);
  };

  useEffect(() => () => clearTimeout(closeTimerRef.current), []);

  if (hasSubmenu) {
    return html`
      <div
        ref=${itemRef}
        class="relative"
        onMouseEnter=${openSubmenu}
        onMouseLeave=${scheduleClose}
      >
        <button
          onClick=${(e) => {
            e.stopPropagation();
            openSubmenu();
          }}
          class="w-full px-3 py-2 text-left text-sm transition-colors flex items-center gap-2 text-gray-200 hover:bg-slate-700"
        >
          ${item.icon && html`<span class="w-4 h-4">${item.icon}</span>`}
          <span class="flex-1">${item.label}</span>
          <${ChevronRightIcon} className="w-4 h-4 text-gray-400" />
        </button>
        ${submenuOpen &&
        html`
          <div
            class="fixed z-50 bg-slate-800 border border-slate-600 rounded-lg shadow-xl py-1 min-w-[140px]"
            style="left: ${submenuPos.left}px; top: ${submenuPos.top}px;"
            onMouseEnter=${() => clearTimeout(closeTimerRef.current)}
            onMouseLeave=${scheduleClose}
          >
            ${item.submenu.map(
              (sub) => html`
                <button
                  key=${sub.label}
                  onClick=${(e) => {
                    e.stopPropagation();
                    if (!sub.disabled) {
                      sub.onClick();
                      onClose();
                    }
                  }}
                  disabled=${sub.disabled}
                  class="w-full px-3 py-2 text-left text-sm transition-colors flex items-center gap-2 ${sub.disabled
                    ? "text-gray-500 cursor-not-allowed"
                    : sub.danger
                      ? "text-red-400 hover:text-red-300 hover:bg-slate-700"
                      : "text-gray-200 hover:bg-slate-700"}"
                >
                  ${sub.icon && html`<span class="w-4 h-4">${sub.icon}</span>`}
                  ${sub.label}
                </button>
              `,
            )}
          </div>
        `}
      </div>
    `;
  }

  return html`
    <button
      onClick=${(e) => {
        e.stopPropagation();
        if (!item.disabled) {
          item.onClick();
          onClose();
        }
      }}
      disabled=${item.disabled}
      class="w-full px-3 py-2 text-left text-sm transition-colors flex items-center gap-2 ${item.disabled
        ? "text-gray-500 cursor-not-allowed"
        : item.danger
          ? "text-red-400 hover:text-red-300 hover:bg-slate-700"
          : "text-gray-200 hover:bg-slate-700"}"
    >
      ${item.icon && html`<span class="w-4 h-4">${item.icon}</span>`}
      ${item.label}
    </button>
  `;
}

function ContextMenu({ x, y, items, onClose }) {
  const menuRef = useRef(null);

  // Close menu when clicking outside - delay to avoid catching the click that opened the menu
  useEffect(() => {
    const handleClickOutside = (e) => {
      if (menuRef.current && !menuRef.current.contains(e.target)) {
        onClose();
      }
    };
    const handleEscape = (e) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    // Delay to avoid catching the opening right-click
    const timeoutId = setTimeout(() => {
      document.addEventListener("mousedown", handleClickOutside);
    }, 10);
    document.addEventListener("keydown", handleEscape);
    return () => {
      clearTimeout(timeoutId);
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [onClose]);

  // Calculate adjusted position synchronously using useMemo
  // This avoids the useState + useEffect anti-pattern that causes the menu
  // to not appear on first render (see 28-anti-patterns-ui.md)
  const position = useMemo(() => {
    // On first render, menuRef.current is null - use raw position
    if (!menuRef.current) {
      return { x, y };
    }
    // Menu exists - calculate adjusted position to stay within viewport
    const rect = menuRef.current.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    let newX = x;
    let newY = y;
    if (x + rect.width > viewportWidth) {
      newX = viewportWidth - rect.width - 8;
    }
    if (y + rect.height > viewportHeight) {
      newY = viewportHeight - rect.height - 8;
    }
    return { x: newX, y: newY };
  }, [x, y, menuRef.current]);

  return html`
    <div
      ref=${menuRef}
      class="fixed z-50 bg-slate-800 border border-slate-600 rounded-lg shadow-xl py-1 min-w-[140px]"
      style="left: ${position.x}px; top: ${position.y}px;"
    >
      ${items.map(
        (item) => html`
          <${ContextMenuItem}
            key=${item.label}
            item=${item}
            onClose=${onClose}
          />
        `,
      )}
    </div>
  `;
}

// =============================================================================
// Session Item Component
// =============================================================================

/**
 * Calculate periodic progress background style.
 * Returns a CSS background style showing elapsed time as a progress indicator.
 *
 * @param {Object} params - Parameters
 * @param {string|null} params.nextScheduledAt - ISO timestamp of next scheduled run
 * @param {Object|null} params.frequency - Frequency config { value, unit, at? }
 * @param {boolean} params.isLight - Whether light theme is active
 * @returns {string|null} CSS background style or null if not applicable
 */
function getPeriodicProgressStyle({ nextScheduledAt, frequency, isLight }) {
  // Skip if progress indicator is disabled
  if (PERIODIC_PROGRESS_STYLE === "none" || !nextScheduledAt || !frequency) {
    return null;
  }

  const colors = PERIODIC_PROGRESS_COLORS[PERIODIC_PROGRESS_STYLE];
  if (!colors) return null;

  const themeColors = isLight ? colors.light : colors.dark;
  const now = Date.now();
  const nextTime = new Date(nextScheduledAt).getTime();

  // Calculate the interval duration in milliseconds
  let intervalMs;
  switch (frequency.unit) {
    case "minutes":
      intervalMs = frequency.value * 60 * 1000;
      break;
    case "hours":
      intervalMs = frequency.value * 60 * 60 * 1000;
      break;
    case "days":
      intervalMs = frequency.value * 24 * 60 * 60 * 1000;
      break;
    default:
      return null;
  }

  // Calculate elapsed time since last run (interval start)
  const intervalStart = nextTime - intervalMs;
  const elapsed = now - intervalStart;
  const progress = Math.max(0, Math.min(1, elapsed / intervalMs));

  // Determine if we're in "urgent" state (close to next run)
  const remaining = 1 - progress;
  const isUrgent = remaining < PERIODIC_PROGRESS_URGENT_THRESHOLD;

  // Get the appropriate color
  const elapsedColor = isUrgent
    ? themeColors.urgentElapsed
    : themeColors.elapsed;
  const remainingColor = themeColors.remaining;

  // Create the gradient - progress goes left to right
  const progressPercent = (progress * 100).toFixed(1);

  return `linear-gradient(to right, ${elapsedColor} 0%, ${elapsedColor} ${progressPercent}%, ${remainingColor} ${progressPercent}%, ${remainingColor} 100%)`;
}

function SessionItem({
  session,
  isActive,
  onSelect,
  onRename,
  onDelete,
  onArchive,
  workspaceColor = null,
  workspaceCode = null,
  workspaceName = null,
  badgeClickEnabled = false,
  onBadgeClick,
  hasQueuedMessages = false,
  isSessionStreaming = false,
  hideBadge = false,
  badgeHideAbbreviation = false,
  badgeHideAcpServer = false,
  isLightTheme = false,
  filterTab = FILTER_TAB.CONVERSATIONS,
  groupingMode = "none", // Current grouping mode (to hide spawned indicator in hierarchical mode)
  onFetchConversationPrompts, // Async (session, workingDir) => menus:conversation prompts evaluated for THIS conversation
  onSendPromptToConversation, // Called with (session, prompt) when a context-menu prompt is clicked
  // New props for parent-child hierarchy display
  isSpawned = false, // If true, shows "spawned" indicator (child session)
  extraLeftPadding = "", // Additional CSS class for left padding (e.g., "pl-6")
  childCount = 0, // Number of child sessions (for collapsed parents)
  hasChildStreaming = false, // If true and collapsed, shows streaming indicator for child
  isNew = false, // If true, applies blink animation for new conversations
  // Props for expand/collapse functionality (when session has children)
  hasChildren = false, // If true, shows expand/collapse chevron
  isExpanded = false, // If true, chevron points down (expanded state)
  onToggleExpand = null, // Callback when expand/collapse is clicked
}) {
  const [showActions, setShowActions] = useState(false);
  const [contextMenu, setContextMenu] = useState(null);
  // menus:conversation prompts evaluated for THIS conversation. Loaded lazily
  // when the context menu opens (enabledWhen depends on this conversation's own
  // context, not the active session). Cached between opens; refreshed each open.
  const [menuPrompts, setMenuPrompts] = useState([]);

  // Check if session is archived
  const isArchived = session.archived || false;

  // Check if periodic is enabled for this session
  const isPeriodicEnabled = session.periodic_enabled || false;

  // Calculate periodic progress background style
  const periodicProgressBg = useMemo(() => {
    if (!isPeriodicEnabled || isArchived) return null;
    return getPeriodicProgressStyle({
      nextScheduledAt: session.next_scheduled_at,
      frequency: session.periodic_frequency,
      isLight: isLightTheme,
    });
  }, [
    isPeriodicEnabled,
    isArchived,
    session.next_scheduled_at,
    session.periodic_frequency,
    isLightTheme,
  ]);

  // Archive button should be disabled if:
  // 1. There are queued messages (can't archive with pending messages)
  // 2. The session is streaming (agent is responding - archiving would block for up to 5 minutes)
  const canArchive = !hasQueuedMessages && !isSessionStreaming;

  // Get the reason why archiving is blocked (for tooltip)
  const archiveBlockedReason = hasQueuedMessages
    ? "Clear queue before archiving"
    : isSessionStreaming
      ? "Wait for response to complete"
      : null;

  // Get working_dir from session, or fall back to global map
  const workingDir =
    session.working_dir || getGlobalWorkingDir(session.session_id) || "";
  // Get acp_server from session
  const acpServer = session.acp_server || "";

  // Build tooltip with session metadata
  const buildTooltip = () => {
    const parts = [];

    // Workspace folder
    if (workingDir) {
      parts.push(`Folder: ${workingDir}`);
    }

    // ACP server
    if (acpServer) {
      parts.push(`Server: ${acpServer}`);
    }

    // Runner type
    if (session.runner_type) {
      const runnerInfo = session.runner_restricted
        ? `${session.runner_type} (restricted)`
        : `${session.runner_type} (unrestricted)`;
      parts.push(`Runner: ${runnerInfo}`);
    }

    // Message/event count
    if (session.messageCount !== undefined) {
      parts.push(`Messages: ${session.messageCount}`);
    } else if (session.event_count !== undefined) {
      parts.push(`Events: ${session.event_count}`);
    }

    // Creation time
    if (session.created_at) {
      const createdDate = new Date(session.created_at);
      parts.push(`Created: ${createdDate.toLocaleString()}`);
    }

    // Last activity time
    if (session.updated_at) {
      const updatedDate = new Date(session.updated_at);
      parts.push(`Last activity: ${updatedDate.toLocaleString()}`);
    } else if (session.last_user_message_at) {
      const lastMsgDate = new Date(session.last_user_message_at);
      parts.push(`Last message: ${lastMsgDate.toLocaleString()}`);
    }

    // Archived time (for archived sessions)
    if (isArchived && session.archived_at) {
      const archivedDate = new Date(session.archived_at);
      parts.push(`Archived: ${archivedDate.toLocaleString()}`);
    }

    // GC-suspended status (for periodic sessions paused to save resources)
    if (session.gc_suspended) {
      parts.push("Status: Suspended (saving resources)");
    }

    // Next scheduled run (for periodic sessions)
    if (isPeriodicEnabled && session.next_scheduled_at) {
      const nextDate = new Date(session.next_scheduled_at);
      const now = Date.now();
      const diff = nextDate.getTime() - now;
      if (diff > 0) {
        // Format relative time
        const hours = Math.floor(diff / (1000 * 60 * 60));
        const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));
        let relativeTime;
        if (hours > 24) {
          const days = Math.floor(hours / 24);
          relativeTime = `${days}d ${hours % 24}h`;
        } else if (hours > 0) {
          relativeTime = `${hours}h ${minutes}m`;
        } else {
          relativeTime = `${minutes}m`;
        }
        parts.push(
          `Next run: ${nextDate.toLocaleString()} (in ${relativeTime})`,
        );
      }
    }

    return parts.join("\n");
  };

  // Determine swipe action based on filter tab and session type:
  // - Archived tab: swipe to delete
  // - Child (spawned) sessions: swipe to delete (archive not applicable)
  // - Regular/Periodic tabs: swipe to archive
  const isSwipeToDelete = filterTab === FILTER_TAB.ARCHIVED || isSpawned;

  // Swipe action handler - archive or delete based on current tab
  const handleSwipeAction = useCallback(() => {
    if (isSwipeToDelete) {
      onDelete(session);
    } else {
      // Archive the session (pass true to archive)
      onArchive(session, true);
    }
  }, [isSwipeToDelete, session, onDelete, onArchive]);

  // Swipe-to-action hook (archive or delete based on tab)
  const {
    swipeOffset,
    isSwiping,
    isSwipingRef,
    isRevealed,
    containerProps,
    reset,
    triggerAction,
  } = useSwipeToAction({
    onAction: handleSwipeAction,
    threshold: 0.5,
    revealWidth: 80,
    disabled: false,
  });

  const handleRename = (e) => {
    if (e) e.stopPropagation();
    onRename(session);
  };

  const handleDelete = (e) => {
    if (e) e.stopPropagation();
    onDelete(session);
  };

  const handleArchive = (e) => {
    if (e) e.stopPropagation();
    onArchive(session, !isArchived);
  };

  const handleContextMenu = (e) => {
    e.preventDefault();
    e.stopPropagation();
    setContextMenu({ x: e.clientX, y: e.clientY });
    // Evaluate menus:conversation prompts against THIS conversation's context so
    // the submenu reflects the right-clicked conversation (e.g. "Report to
    // parent" only for children), not the active session.
    if (onFetchConversationPrompts) {
      onFetchConversationPrompts(session, workingDir).then((prompts) => {
        setMenuPrompts(prompts || []);
      });
    }
  };

  const closeContextMenu = () => {
    setContextMenu(null);
  };

  // Handle click - only select if not swiping/revealed
  // Use ref for isSwiping to avoid stale closure issues
  const handleClick = useCallback(() => {
    if (isSwipingRef.current) return;
    if (isRevealed) {
      reset();
      return;
    }
    onSelect(session.session_id);
  }, [isSwipingRef, isRevealed, reset, onSelect, session.session_id]);

  const displayName = session.name || session.description || "Untitled";
  // Archived sessions should never show as active (they have no ACP connection)
  const isActiveSession =
    !isArchived && (session.isActive || session.status === "active");
  const isStreaming = !isArchived && (session.isStreaming || false);

  // Build group submenus from prompts flagged with menus:conversation.
  // Prompts are grouped by their `group` attribute; ungrouped prompts fall
  // under "Other". Each group becomes a submenu listing its prompts.
  const promptGroupItems = [];
  if (
    onSendPromptToConversation &&
    menuPrompts &&
    menuPrompts.length > 0
  ) {
    const groups = new Map();
    for (const p of menuPrompts) {
      if (!p || !p.name) continue;
      const groupName = (p.group && p.group.trim()) || "Other";
      if (!groups.has(groupName)) groups.set(groupName, []);
      groups.get(groupName).push(p);
    }
    for (const [groupName, prompts] of groups) {
      promptGroupItems.push({
        label: groupName,
        submenu: prompts.map((p) => ({
          label: p.name,
          onClick: () => onSendPromptToConversation(session, p),
        })),
      });
    }
  }

  const contextMenuItems = [
    // Hide archive option for child (spawned) sessions
    ...(isSpawned
      ? []
      : [
          {
            label: !canArchive
              ? archiveBlockedReason
              : isArchived
                ? "Unarchive"
                : "Archive",
            icon: isArchived
              ? html`<${ArchiveFilledIcon} />`
              : html`<${ArchiveIcon} />`,
            onClick: canArchive ? () => handleArchive() : undefined,
            disabled: !canArchive,
          },
        ]),
    {
      label: "Properties",
      icon: html`<${EditIcon} />`,
      onClick: () => handleRename(),
    },
    {
      label: "Delete",
      icon: html`<${TrashIcon} />`,
      onClick: () => handleDelete(),
      danger: true,
    },
    // Prompt group submenus (menus:conversation prompts)
    ...promptGroupItems,
  ];

  // Calculate visual feedback intensity based on swipe progress
  const absOffset = Math.abs(swipeOffset);
  const deleteProgress = Math.min(absOffset / 160, 1); // Max at 160px

  // Context menu must be rendered outside the overflow-hidden containers
  // to prevent clipping. Use a Fragment to render it as a sibling.
  return html`
    <${Fragment}>
      ${contextMenu &&
      html`
        <${ContextMenu}
          x=${contextMenu.x}
          y=${contextMenu.y}
          items=${contextMenuItems}
          onClose=${closeContextMenu}
        />
      `}
      <div
        class="session-item-container relative overflow-hidden"
        ...${containerProps}
      >
        <!-- Swipe action background (revealed when swiping left) -->
        <!-- Shows Archive (amber) for regular/periodic tabs, Delete (red) for archived tab -->
        <div
          class="absolute inset-0 ${isSwipeToDelete
            ? "bg-red-600"
            : "bg-amber-600"} flex items-center justify-end pr-6 transition-opacity"
          style="opacity: ${isRevealed || absOffset > 20 ? 1 : 0}"
        >
          <button
            onClick=${(e) => {
              e.preventDefault();
              e.stopPropagation();
              triggerAction();
            }}
            class="p-3 rounded-full ${isSwipeToDelete
              ? "bg-red-700 hover:bg-red-800"
              : "bg-amber-700 hover:bg-amber-800"} transition-colors"
            title=${isSwipeToDelete ? "Delete" : "Archive"}
          >
            ${isSwipeToDelete
              ? html`<${TrashIcon} className="w-5 h-5 text-white" />`
              : html`<${ArchiveIcon} className="w-5 h-5 text-white" />`}
          </button>
        </div>
        <!-- Swipeable content -->
        <div
          onClick=${handleClick}
          onContextMenu=${handleContextMenu}
          onMouseEnter=${() => setShowActions(true)}
          onMouseLeave=${() => setShowActions(false)}
          class="p-3 cursor-pointer hover:bg-slate-700/50 relative bg-mitto-sidebar overflow-hidden ${isActive
            ? "bg-blue-900/30 border-l-2 border-l-blue-500"
            : ""} ${isSwiping
            ? ""
            : "transition-transform duration-200"} ${extraLeftPadding} ${isNew
            ? "session-item-new"
            : ""}"
          style="transform: translateX(${swipeOffset}px);"
          title=${buildTooltip()}
          data-session-id=${session.session_id}
          data-has-context-menu="true"
        >
          ${periodicProgressBg
            ? html`<div
                class="absolute inset-0 z-0 pointer-events-none"
                style="background: ${periodicProgressBg};"
                aria-hidden="true"
              ></div>`
            : ""}
          <div class="relative z-10">
            <!-- Top row: status indicator, title, and workspace pill -->
            <div class="flex items-center gap-2">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2">
                  ${isSpawned
                    ? html`
                          <span
                            class="spawned-indicator flex-shrink-0"
                            title="Spawned from another conversation"
                            >↳</span
                          >
                        `
                      : null
                  }
                  <span class="text-sm font-medium truncate"
                    >${displayName}</span
                  >
                  ${session.child_origin === "auto"
                    ? html`
                        <span class="flex-shrink-0 text-amber-400" title="Auto-created child">
                          <${LightningIcon} className="w-4 h-4" />
                        </span>
                      `
                    : session.child_origin === "mcp"
                      ? html`
                          <span class="flex-shrink-0 text-blue-400" title="Created by agent">
                            <${RobotIcon} className="w-4 h-4" />
                          </span>
                        `
                      : session.child_origin === "human"
                        ? html`
                            <span class="flex-shrink-0 text-green-400" title="Manually created child">
                              <${PersonIcon} className="w-4 h-4" />
                            </span>
                          `
                        : null}
                  ${session.isWaitingForChildren
                    ? html`
                        <span class="flex-shrink-0 text-yellow-400 animate-pulse" title="Waiting for child conversations">
                          <${HourglassIcon} className="w-4 h-4" />
                        </span>
                      `
                    : null}
                  ${session.isWaitingForUserInput
                    ? html`
                        <span class="flex-shrink-0 text-purple-400 animate-pulse" title="Waiting for user input">
                          <${QuestionMarkIcon} className="w-4 h-4" />
                        </span>
                      `
                    : null}
                </div>
              </div>
              ${isStreaming || hasChildStreaming
                ? html`
                    <span
                      class="w-2 h-2 bg-blue-400 rounded-full flex-shrink-0 ${hasChildStreaming
                        ? "child-streaming-indicator"
                        : "streaming-indicator"}"
                      title=${hasChildStreaming
                        ? "Child conversation responding..."
                        : "Receiving response..."}
                    ></span>
                  `
                : isActiveSession
                  ? html`
                      <span
                        class="w-2 h-2 bg-green-400 rounded-full flex-shrink-0"
                        title="Active"
                      ></span>
                    `
                  : !isArchived
                    ? html`
                        <span
                          class="w-2 h-2 bg-amber-400 rounded-full flex-shrink-0"
                          title="Not connected"
                        ></span>
                      `
                    : null}
              ${workingDir &&
              !hideBadge &&
              html`
                <${WorkspacePill}
                  path=${workingDir}
                  customColor=${workspaceColor}
                  customCode=${workspaceCode}
                  customName=${workspaceName}
                  acpServer=${acpServer}
                  clickable=${badgeClickEnabled}
                  onBadgeClick=${onBadgeClick}
                  hideAbbreviation=${badgeHideAbbreviation}
                  hideAcpServer=${badgeHideAcpServer}
                />
              `}
            </div>
            ${!isSpawned && html`
            <!-- Bottom row: children count and action buttons -->
            <div class="flex items-center justify-between mt-1">
              <div class="flex items-center gap-2">
                ${hasChildren && childCount > 0
                  ? html`
                      <button
                        class="flex items-center gap-1 text-xs px-1.5 py-0.5 rounded ${hasChildStreaming ? 'child-expand-streaming' : 'bg-slate-700'} text-gray-400 hover:text-white hover:bg-slate-600 transition-colors cursor-pointer"
                        onClick=${(e) => {
                          e.stopPropagation();
                          if (onToggleExpand) onToggleExpand();
                        }}
                        title="${isExpanded ? 'Collapse children' : 'Expand children'}"
                      >
                        <span class="inline-block transition-transform ${isExpanded ? '' : '-rotate-90'}">
                          <${ChevronDownIcon} className="w-3 h-3" />
                        </span>
                        <span>+${childCount}</span>
                      </button>
                    `
                  : null}
              </div>
              <div
                class="flex items-center gap-1 ${showActions
                  ? "opacity-100"
                  : "opacity-0"} transition-opacity flex-shrink-0"
              >
                ${isSpawned
                  ? html`<button
                      onClick=${handleDelete}
                      class="p-1.5 bg-slate-700 hover:bg-red-600 rounded transition-colors text-gray-300 hover:text-white"
                      title="Delete"
                    >
                      <${TrashIcon} className="w-4 h-4" />
                    </button>`
                  : html`<button
                      onClick=${canArchive ? handleArchive : undefined}
                      disabled=${!canArchive}
                      class="p-1.5 bg-slate-700 rounded transition-colors ${!canArchive
                        ? "opacity-50 cursor-not-allowed text-gray-500"
                        : isArchived
                          ? "hover:bg-slate-600 text-gray-500"
                          : "hover:bg-slate-600 text-gray-300 hover:text-white"}"
                      title="${!canArchive
                        ? archiveBlockedReason
                        : isArchived
                          ? `Unarchive${session.archive_reason ? " — " + getArchiveReasonText(session.archive_reason, session.archived_at) : ""}`
                          : "Archive"}"
                    >
                      ${isArchived
                        ? html`<${ArchiveFilledIcon} className="w-4 h-4" />`
                        : html`<${ArchiveIcon} className="w-4 h-4" />`}
                    </button>`
                }
                <button
                  onClick=${handleRename}
                  class="p-1.5 bg-slate-700 hover:bg-slate-600 rounded transition-colors text-gray-300 hover:text-white"
                  title="Properties"
                >
                  <${EditIcon} className="w-4 h-4" />
                </button>
                <button
                  onClick=${handleDelete}
                  class="p-1.5 bg-slate-700 hover:bg-red-600 rounded transition-colors text-gray-300 hover:text-white"
                  title="Delete"
                >
                  <${TrashIcon} className="w-4 h-4" />
                </button>
              </div>
            </div>
            `}
          </div>
        </div>
      </div>
    <//>
  `;
}

// =============================================================================
// Session List Component (Sidebar)
// =============================================================================

function SessionList({
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
  queueLength = 0,
  onFetchConversationPrompts, // Async (session, workingDir) => prompts[] for the context menu
  onSendPromptToConversation,
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
      if (groupKey in sidebarExpandedGroups) return sidebarExpandedGroups[groupKey];
      if (groupKey === "__archived__") return false;
      return true;
    },
    [sidebarExpandedGroups],
  );

  // Handle group expand/collapse toggle
  const handleToggleGroup = useCallback(
    (groupKey, allGroupKeys = []) => {
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
        // Always enforce accordion mode for parent-child groups (only one expanded at a time)
        const isParentGroup = groupKey.startsWith("parent:");
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
      // Always enforce accordion mode for parent-child groups (only one expanded at a time)
      const isParentGroup = groupKey.startsWith("parent:");
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
    [sidebarExpandedGroups],
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
    if (groupingMode === "none") {
      return null; // No grouping, render flat list
    }

    // Compute structural fingerprint: session IDs, parent IDs, working dirs, AND grouping mode
    // This avoids expensive buildSessionTree rebuilds when only non-structural properties change
    // (e.g., isStreaming, message content during tool_update events).
    // groupingMode MUST be included because the same sessions produce different group structures
    // depending on the mode (server vs folder vs workspace).
    const fingerprint =
      groupingMode +
      "\n" +
      filteredSessions
        .map(
          (s) =>
            `${s.session_id}|${s.parent_session_id || ""}|${s.working_dir || ""}|${s.archived || false}|${s.periodic_enabled || false}|${s.pinned || false}|${s.name || ""}`,
        )
        .sort()
        .join("\n");

    if (
      fingerprint === prevSessionFingerprint.current &&
      prevGroupedSessions.current
    ) {
      return prevGroupedSessions.current;
    }
    prevSessionFingerprint.current = fingerprint;

    // Helper to get session working dir and acp server
    // working_dir and acp_server are already merged by computeAllSessions() in lib.js
    const getSessionInfo = (session) => {
      return {
        workingDir:
          session.working_dir || getGlobalWorkingDir(session.session_id) || "",
        acpServer: session.acp_server || "",
      };
    };

    // Build a lookup map and root-parent resolver used by all grouping modes
    // to ensure children are placed in the same group as their parent.
    const sessionById = new Map(filteredSessions.map((s) => [s.session_id, s]));
    const resolveRootParent = (session) => {
      let current = session;
      let depth = 0;
      while (current.parent_session_id && depth < 10) {
        const parent = sessionById.get(current.parent_session_id);
        if (!parent) break;
        current = parent;
        depth++;
      }
      return current;
    };

    if (groupingMode === "folder") {
      // Hierarchical mode: folder -> sessions (with parent-child relationships)
      // Structure: Folder → Parent sessions (with nested child sessions)
      const folderGroups = new Map();

      // All known session IDs across all tabs (conversations + periodic + archived)
      // Used by buildSessionTree to distinguish "parent in another tab" from "parent truly missing"
      const allKnownSessionIds = new Set(allSessions.map((s) => s.session_id));

      // Helper to get parent_session_id from session
      // parent_session_id is already merged by computeAllSessions() in lib.js
      const getParentSessionId = (session) => session.parent_session_id || "";

      // First pass: group all sessions by folder
      // Use root parent's working_dir so children with a different working_dir
      // end up in the same folder group as their parent.
      filteredSessions.forEach((session) => {
        const rootParent = resolveRootParent(session);
        const { workingDir } = getSessionInfo(rootParent);
        const folderKey = workingDir || "Unknown";

        if (!folderGroups.has(folderKey)) {
          folderGroups.set(folderKey, {
            label: (() => {
              if (!workingDir) return "Unknown";
              const ws = workspaces.find(w => w.working_dir === workingDir);
              return ws?.name || getBasename(workingDir);
            })(),
            workingDir,
            allSessions: [], // All sessions in this folder (before parent-child grouping)
          });
        }

        folderGroups.get(folderKey).allSessions.push(session);
      });

      // Second pass: build parent-child hierarchy within each folder
      const result = Array.from(folderGroups.entries())
        .map(([key, folder]) => {
          const { allSessions: folderSessions } = folder;

          // Build parent-child tree using utility function
          const { rootSessions, childrenMap, orphans } = buildSessionTree(
            folderSessions,
            allKnownSessionIds,
          );

          // Attach children array to each parent session
          const parents = rootSessions.map((parent) => ({
            ...parent,
            children: childrenMap.get(parent.session_id) || [],
          }));

          // Sort children within each parent by created_at (most recent first)
          // Use created_at instead of updated_at to prevent re-ordering when agent responds
          parents.forEach((parent) => {
            parent.children.sort((a, b) => {
              const aDate = new Date(a.created_at || 0);
              const bDate = new Date(b.created_at || 0);
              return bDate - aDate;
            });
          });

          // Sort parents by created_at (most recent first)
          // Use created_at instead of updated_at to prevent re-ordering when agent responds
          parents.sort((a, b) => {
            const aDate = new Date(a.created_at || 0);
            const bDate = new Date(b.created_at || 0);
            return bDate - aDate;
          });

          // Sort orphans by created_at (most recent first)
          // Use created_at instead of updated_at to prevent re-ordering when agent responds
          orphans.sort((a, b) => {
            const aDate = new Date(a.created_at || 0);
            const bDate = new Date(b.created_at || 0);
            return bDate - aDate;
          });

          // Combine: parents first, then orphans (orphans are children whose parents aren't visible)
          const sessions = [...parents, ...orphans];

          return {
            key,
            label: folder.label,
            workingDir: folder.workingDir,
            isHierarchical: true, // Flag to identify hierarchical groups
            isParentChild: true, // Flag to identify parent-child mode (vs old ACP subgroups)
            sessions, // Sessions with optional .children array
          };
        })
        .sort((a, b) => a.label.localeCompare(b.label));

      prevGroupedSessions.current = result;
      return result;
    }

    // Flat grouping modes: server and workspace
    const groups = new Map();

    filteredSessions.forEach((session) => {
      // Use root parent's properties for grouping so children with a different
      // acp_server or working_dir stay in the same group as their parent.
      const groupSession = resolveRootParent(session);

      let groupKey;
      let groupLabel;
      let groupWorkingDir = "";
      let groupAcpServer = "";

      if (groupingMode === "server") {
        const { acpServer } = getSessionInfo(groupSession);
        groupKey = acpServer || "Unknown";
        groupLabel = groupKey;
      } else {
        // workspace mode - group by workspace (working_dir + acp_server combination)
        // This ensures workspaces with the same folder but different ACP servers are separate groups
        const { workingDir, acpServer } = getSessionInfo(groupSession);
        // Use composite key: working_dir|acp_server (to separate same-folder workspaces)
        groupKey = `${workingDir}|${acpServer}`;
        // Label is the workspace display name if available, otherwise basename - acpServer is shown as a badge
        const ws = workspaces.find(w => w.working_dir === workingDir && (!acpServer || w.acp_server === acpServer));
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

    // Build parent-child tree within each group (same as folder mode)
    const allKnownSessionIds = new Set(allSessions.map((s) => s.session_id));
    groups.forEach((group) => {
      const { rootSessions, childrenMap, orphans } = buildSessionTree(
        group.sessions,
        allKnownSessionIds,
      );

      // Attach children to parents
      const parents = rootSessions.map((parent) => ({
        ...parent,
        children: childrenMap.get(parent.session_id) || [],
      }));

      // Sort children within each parent by created_at (most recent first)
      parents.forEach((parent) => {
        parent.children.sort(
          (a, b) => new Date(b.created_at || 0) - new Date(a.created_at || 0),
        );
      });

      // Sort parents by created_at (most recent first)
      parents.sort(
        (a, b) => new Date(b.created_at || 0) - new Date(a.created_at || 0),
      );

      // Sort orphans by created_at (most recent first)
      orphans.sort(
        (a, b) => new Date(b.created_at || 0) - new Date(a.created_at || 0),
      );

      // Replace flat list with tree structure
      group.sessions = [...parents, ...orphans];
      group.isParentChild = true;
    });

    // Convert to array and sort by label
    const result = Array.from(groups.entries())
      .map(([key, value]) => ({ key, ...value }))
      .sort((a, b) => a.label.localeCompare(b.label));

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
  // If multiple groups are expanded and accordion mode is enabled, collapse all but the first.
  useEffect(() => {
    if (!groupedSessions || !getSingleExpandedGroupMode()) {
      return;
    }

    // Find all currently expanded groups in the current view (use React state, not localStorage)
    const expandedKeys = groupedSessions
      .filter((g) => isSidebarGroupExpanded(g.key))
      .map((g) => g.key);

    // If more than one group is expanded, collapse all but the first
    if (expandedKeys.length > 1) {
      const [keepExpanded, ...toCollapse] = expandedKeys;
      console.debug(
        "[Mitto] Accordion mode: collapsing groups on tab/mode change. Keeping:",
        keepExpanded,
        "Collapsing:",
        toCollapse,
      );
      // Update React state and localStorage for collapsed groups
      setSidebarExpandedGroups((prev) => {
        const next = { ...prev };
        for (const key of toCollapse) {
          next[key] = false;
        }
        return next;
      });
      for (const key of toCollapse) {
        setGroupExpanded(key, false);
      }
    }
  }, [groupedSessions, filterTab, groupingMode, sidebarExpandedGroups]);

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
              typeof window.mittoPickFolder === "function" &&
              group.workingDir &&
              html`
                ${badgeClickEnabled && html`
                  <button
                    onClick=${(e) => { e.stopPropagation(); onFolderOpen && onFolderOpen(group.workingDir); }}
                    class="p-0.5 rounded hover:bg-slate-600 transition-colors text-gray-500 hover:text-white"
                    title="Open folder: ${group.workingDir}"
                  >
                    <${FolderOpenIcon} className="w-3.5 h-3.5" />
                  </button>
                `}
                ${terminalActionEnabled && html`
                  <button
                    onClick=${(e) => { e.stopPropagation(); onTerminalClick && onTerminalClick(group.workingDir); }}
                    class="p-0.5 rounded hover:bg-slate-600 transition-colors text-gray-500 hover:text-white"
                    title="Open terminal: ${group.workingDir}"
                  >
                    <${TerminalIcon} className="w-3.5 h-3.5" />
                  </button>
                `}
              `}
              ${groupingMode === "workspace" &&
              (filterTab === FILTER_TAB.CONVERSATIONS || filterTab === FILTER_TAB.PERIODIC) &&
              html`
                <button
                  onClick=${(e) => handleNewSessionInGroup(group.key, e)}
                  class="p-0.5 rounded hover:bg-slate-600 transition-colors text-gray-500 hover:text-white"
                  title="New conversation in ${group.label}"
                >
                  <${PlusIcon} className="w-3.5 h-3.5" />
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
              ${typeof window.mittoPickFolder === "function" &&
              folder.workingDir &&
              html`
                ${badgeClickEnabled && html`
                  <button
                    onClick=${(e) => { e.stopPropagation(); onFolderOpen && onFolderOpen(folder.workingDir); }}
                    class="p-0.5 rounded hover:bg-slate-600 transition-colors text-gray-500 hover:text-white"
                    title="Open folder: ${folder.workingDir}"
                  >
                    <${FolderOpenIcon} className="w-3.5 h-3.5" />
                  </button>
                `}
                ${terminalActionEnabled && html`
                  <button
                    onClick=${(e) => { e.stopPropagation(); onTerminalClick && onTerminalClick(folder.workingDir); }}
                    class="p-0.5 rounded hover:bg-slate-600 transition-colors text-gray-500 hover:text-white"
                    title="Open terminal: ${folder.workingDir}"
                  >
                    <${TerminalIcon} className="w-3.5 h-3.5" />
                  </button>
                `}
              `}
              ${(filterTab === FILTER_TAB.CONVERSATIONS || filterTab === FILTER_TAB.PERIODIC) &&
              html`
                <button
                  onClick=${(e) => handleNewSessionInFolder(folder.workingDir, e)}
                  class="p-0.5 rounded hover:bg-slate-600 transition-colors text-gray-500 hover:text-white"
                  title="New conversation in ${folder.label}"
                >
                  <${PlusIcon} className="w-3.5 h-3.5" />
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
            onClick=${() => onNewSession(null, null, filterTab)}
            class="p-1.5 hover:bg-slate-700 rounded transition-colors"
            title="New Conversation"
          >
            <${PlusIcon} className="w-4 h-4" />
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
  const [isUserAtBottom, setIsUserAtBottom] = useState(true);
  const [hasNewMessages, setHasNewMessages] = useState(false);
  // Per-session draft text: { sessionId: draftText } - null key for "no session" state
  const [sessionDrafts, setSessionDrafts] = useState({});
  const sessionDraftsRef = useRef(sessionDrafts);
  useEffect(() => {
    sessionDraftsRef.current = sessionDrafts;
  }, [sessionDrafts]);
  const messagesEndRef = useRef(null);
  const mainContentRef = useRef(null);
  const messagesContainerRef = useRef(null);
  const prevMessagesLengthRef = useRef(0);
  // Scroll position preservation for "load more" (prepend) - stores scroll metrics before loading
  const scrollPreservationRef = useRef(null);

  // Compute all sessions for navigation using shared helper function
  const allSessions = useMemo(
    () => computeAllSessions(activeSessions, storedSessions),
    [activeSessions, storedSessions],
  );

  // Predefined prompts: the backend's /api/workspace-prompts endpoint now returns
  // all prompts fully merged (global + server-specific + workspace) and filtered.
  // The frontend just uses them directly — no client-side merge needed.
  const predefinedPrompts = workspacePrompts;

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
            typeof p.menus === "string" &&
            p.menus
              .split(",")
              .map((m) => m.trim())
              .includes("conversation"),
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
        onClick: () => switchSession(backgroundCompletion.sessionId),
      });
      clearBackgroundCompletion();
    }
  }, [backgroundCompletion, clearBackgroundCompletion, showToast, switchSession]);

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
        onClick: () => switchSession(periodicStarted.sessionId),
      });
      clearPeriodicStarted();
    }
  }, [periodicStarted, clearPeriodicStarted, showToast, switchSession]);

  // Show toast when a UI prompt arrives in a background session
  useEffect(() => {
    if (backgroundUIPrompt) {
      // In-app toast (native notification is handled in useWebSocket)
      showToast({
        style: "warning",
        title: `Question in ${backgroundUIPrompt.sessionName || "conversation"}`,
        duration: 8000,
        onClick: () => switchSession(backgroundUIPrompt.sessionId),
      });
      clearBackgroundUIPrompt();
    }
  }, [backgroundUIPrompt, clearBackgroundUIPrompt, showToast, switchSession]);

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
        onClick: () => switchSession(backgroundUIPromptTimeout.sessionId),
      });
      clearBackgroundUIPromptTimeout();
    }
  }, [backgroundUIPromptTimeout, clearBackgroundUIPromptTimeout, showToast, switchSession]);

  // Listen for runner fallback events
  useEffect(() => {
    const handleRunnerFallback = (event) => {
      const data = event.detail;
      if (data) {
        showToast({
          style: "warning",
          title: "Runner Not Supported",
          message: `Requested: ${data.requested_type} — Using: ${data.fallback_type} (no restrictions). ${data.reason || ""}`,
          duration: 10000,
        });
      }
    };
    window.addEventListener("mitto:runner_fallback", handleRunnerFallback);
    return () => {
      window.removeEventListener("mitto:runner_fallback", handleRunnerFallback);
    };
  }, [showToast]);

  // Listen for memory-recycle events (GC Tier 4 restarted a bloated idle agent)
  useEffect(() => {
    const handleMemoryRecycled = (event) => {
      const data = event.detail;
      if (!data) return;
      const toMB = (b) => Math.round((Number(b) || 0) / (1024 * 1024));
      const name =
        data.workspace_name ||
        (data.working_dir ? data.working_dir.split("/").pop() : "") ||
        "a workspace";
      const used = toMB(data.rss_bytes);
      const limit = toMB(data.threshold_bytes);
      const count = data.session_count || 0;
      const convs = count === 1 ? "conversation" : "conversations";
      showToast({
        style: "info",
        title: `Memory reclaimed: ${name}`,
        message: `Idle agent using ${used} MB (limit ${limit} MB) was restarted to free memory. ${count} ${convs} will resume automatically when reopened.`,
        duration: 10000,
      });
    };
    window.addEventListener("mitto:memory_recycled", handleMemoryRecycled);
    return () => {
      window.removeEventListener("mitto:memory_recycled", handleMemoryRecycled);
    };
  }, [showToast]);

  // Listen for ACP start failed events
  useEffect(() => {
    const handleAcpStartFailed = (event) => {
      const data = event.detail;
      if (data) {
        showToast({
          style: "error",
          title: "AI Agent Failed to Start",
          message: "Try switching to the session and sending a message to retry.",
          duration: 10000,
          onClick: data.session_id ? () => switchSession(data.session_id) : null,
        });
      }
    };
    window.addEventListener("mitto:acp_start_failed", handleAcpStartFailed);
    return () => {
      window.removeEventListener("mitto:acp_start_failed", handleAcpStartFailed);
    };
  }, [showToast, switchSession]);

  // Listen for ACP permanent error events (non-retryable errors with guidance)
  useEffect(() => {
    const handleAcpPermanentError = (event) => {
      const data = event.detail;
      if (data) {
        const detail = [data.user_guidance, data.command ? `Command: ${data.command}` : ""]
          .filter(Boolean)
          .join(" — ");
        showToast({
          style: "error",
          title: data.user_message || "ACP Server Error",
          message: detail,
          duration: 30000,
        });
      }
    };
    window.addEventListener("mitto:acp_error_permanent", handleAcpPermanentError);
    return () => {
      window.removeEventListener("mitto:acp_error_permanent", handleAcpPermanentError);
    };
  }, [showToast]);

  // Listen for hook failed events
  useEffect(() => {
    const handleHookFailed = (event) => {
      const data = event.detail;
      if (data) {
        const exitPart = data.exit_code !== undefined ? ` (exit code ${data.exit_code})` : "";
        showToast({
          style: "warning",
          title: `Hook Failed: ${data.name || "up"}${exitPart}`,
          message: data.error || "",
          duration: 10000,
        });
      }
    };
    window.addEventListener("mitto:hook_failed", handleHookFailed);
    return () => {
      window.removeEventListener("mitto:hook_failed", handleHookFailed);
    };
  }, [showToast]);

  // Listen for mitto:notification events dispatched by useWebSocket
  useEffect(() => {
    const handleNotification = (event) => {
      const data = event.detail;
      if (!data) return;

      // Play sound if requested (reuse the agent-completed sound)
      if (data.sound && window.mittoAgentCompletedSoundEnabled) {
        playAgentCompletedSound();
      }

      // Show native notification if requested and available (macOS app only)
      if (
        data.native &&
        window.mittoNativeNotificationsEnabled &&
        typeof window.mittoShowNativeNotification === "function"
      ) {
        window.mittoShowNativeNotification(
          data.title || "Notification",
          data.message || "",
          data.session_id || "",
          data.sticky || false,
        );
      }

      // Show in-app toast
      showToast({
        style: data.style || "info",
        title: data.title || "Notification",
        message: data.message || "",
        duration: data.style === "error" ? 8000 : 5000,
      });
    };
    window.addEventListener("mitto:notification", handleNotification);
    return () => {
      window.removeEventListener("mitto:notification", handleNotification);
    };
  }, [showToast]);

  // Remove native notifications for the active session when switching to it
  // This prevents stale notifications from lingering in Notification Center
  useEffect(() => {
    if (
      activeSessionId &&
      typeof window.mittoRemoveNotificationsForSession === "function"
    ) {
      window.mittoRemoveNotificationsForSession(activeSessionId);
    }
  }, [activeSessionId]);

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

  // Follow system theme state - persisted to localStorage
  const [followSystemTheme, setFollowSystemTheme] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto-follow-system-theme");
      // Default to true for new users (follow system theme by default)
      return saved === null ? true : saved === "true";
    }
    return true;
  });

  // Theme state - respects OS preference when followSystemTheme is enabled
  const [theme, setTheme] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const followSystem = localStorage.getItem("mitto-follow-system-theme");
      // If following system theme (default for new users)
      if (followSystem === null || followSystem === "true") {
        if (typeof window !== "undefined" && window.matchMedia) {
          const prefersDark = window.matchMedia(
            "(prefers-color-scheme: dark)",
          ).matches;
          return prefersDark ? "dark" : "light";
        }
      }
      // Otherwise use saved theme preference
      const saved = localStorage.getItem("mitto-theme");
      if (saved) return saved;
    }
    // Check OS preference for dark/light mode
    if (typeof window !== "undefined" && window.matchMedia) {
      const prefersDark = window.matchMedia(
        "(prefers-color-scheme: dark)",
      ).matches;
      return prefersDark ? "dark" : "light";
    }
    // Fallback: If v2 theme is active (set by index.html script), default to light
    if (
      window.mittoTheme === "v2" ||
      document.documentElement.classList.contains("v2-theme")
    ) {
      return "light";
    }
    return "dark";
  });

  // Listen for OS theme changes when followSystemTheme is enabled
  useEffect(() => {
    if (
      !followSystemTheme ||
      typeof window === "undefined" ||
      !window.matchMedia
    ) {
      return;
    }

    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
    const handleChange = (e) => {
      setTheme(e.matches ? "dark" : "light");
    };

    // Add listener for theme changes
    mediaQuery.addEventListener("change", handleChange);
    return () => mediaQuery.removeEventListener("change", handleChange);
  }, [followSystemTheme]);

  // Persist followSystemTheme to localStorage
  useEffect(() => {
    localStorage.setItem(
      "mitto-follow-system-theme",
      String(followSystemTheme),
    );
  }, [followSystemTheme]);

  // Apply theme class to document
  useEffect(() => {
    const root = document.documentElement;
    if (theme === "light") {
      root.classList.add("light");
      root.classList.remove("dark");
      // Also apply to body for v2-theme CSS selectors (which use .v2-theme.dark)
      document.body.classList.add("light");
      document.body.classList.remove("dark");
    } else {
      root.classList.add("dark");
      root.classList.remove("light");
      // Also apply to body for v2-theme CSS selectors (which use .v2-theme.dark)
      document.body.classList.add("dark");
      document.body.classList.remove("light");
    }
    localStorage.setItem("mitto-theme", theme);
    // Update Mermaid.js theme for new diagrams
    if (typeof window.updateMermaidTheme === "function") {
      window.updateMermaidTheme(theme);
    }
  }, [theme]);

  const toggleTheme = useCallback(() => {
    // When user manually toggles theme, disable follow system theme
    setFollowSystemTheme(false);
    setTheme((prev) => (prev === "dark" ? "light" : "dark"));
  }, []);

  const handleSetFollowSystemTheme = useCallback((value) => {
    setFollowSystemTheme(value);
    // When enabling follow system theme, immediately sync with OS preference
    if (value && typeof window !== "undefined" && window.matchMedia) {
      const prefersDark = window.matchMedia(
        "(prefers-color-scheme: dark)",
      ).matches;
      setTheme(prefersDark ? "dark" : "light");
    }
  }, []);

  // Listen for follow system theme changes from SettingsDialog
  useEffect(() => {
    const handleFollowSystemThemeChanged = (e) => {
      handleSetFollowSystemTheme(e.detail.enabled);
    };
    window.addEventListener(
      "mitto-follow-system-theme-changed",
      handleFollowSystemThemeChanged,
    );
    return () =>
      window.removeEventListener(
        "mitto-follow-system-theme-changed",
        handleFollowSystemThemeChanged,
      );
  }, [handleSetFollowSystemTheme]);

  // Follow system reduced motion state - persisted to localStorage
  const [followSystemReducedMotion, setFollowSystemReducedMotion] = useState(
    () => {
      if (typeof localStorage !== "undefined") {
        const saved = localStorage.getItem(
          "mitto-follow-system-reduced-motion",
        );
        // Default to true for new users (respect OS preference by default)
        return saved === null ? true : saved === "true";
      }
      return true;
    },
  );

  // Reduce animations state - respects OS preference when followSystemReducedMotion is enabled
  const [reduceAnimations, setReduceAnimations] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const followSystem = localStorage.getItem(
        "mitto-follow-system-reduced-motion",
      );
      // If following system preference (default for new users)
      if (followSystem === null || followSystem === "true") {
        if (typeof window !== "undefined" && window.matchMedia) {
          if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
            return true;
          }
        }
        // Auto-enable on mobile/tablet (iPad reports as Macintosh with touch support)
        if (typeof navigator !== "undefined") {
          const ua = navigator.userAgent || "";
          if (/iPad|iPhone|iPod|Android/i.test(ua) ||
              (navigator.maxTouchPoints > 1 && /Macintosh/i.test(ua))) {
            return true;
          }
        }
      }
      // Otherwise use saved explicit preference
      const saved = localStorage.getItem("mitto-reduce-animations");
      if (saved !== null) return saved === "true";
    }
    // Fallback: check OS preference
    if (typeof window !== "undefined" && window.matchMedia) {
      return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    }
    // Auto-enable on mobile/tablet devices to save battery —
    // backdrop-filter blur causes sustained GPU compositing work
    // even when idle, draining battery on iPad and similar devices.
    if (typeof navigator !== "undefined") {
      const ua = navigator.userAgent || "";
      if (/iPad|iPhone|iPod|Android/i.test(ua) ||
          (navigator.maxTouchPoints > 1 && /Macintosh/i.test(ua))) {
        return true;
      }
    }
    return false;
  });

  // Listen for OS reduced motion changes when followSystemReducedMotion is enabled
  useEffect(() => {
    if (
      !followSystemReducedMotion ||
      typeof window === "undefined" ||
      !window.matchMedia
    ) {
      return;
    }

    const mediaQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    const handleChange = (e) => {
      setReduceAnimations(e.matches);
    };

    mediaQuery.addEventListener("change", handleChange);
    return () => mediaQuery.removeEventListener("change", handleChange);
  }, [followSystemReducedMotion]);

  // Persist followSystemReducedMotion to localStorage
  useEffect(() => {
    localStorage.setItem(
      "mitto-follow-system-reduced-motion",
      String(followSystemReducedMotion),
    );
  }, [followSystemReducedMotion]);

  // Apply reduce-animations class to document
  useEffect(() => {
    const root = document.documentElement;
    if (reduceAnimations) {
      root.classList.add("reduce-animations");
    } else {
      root.classList.remove("reduce-animations");
    }
    localStorage.setItem("mitto-reduce-animations", String(reduceAnimations));
  }, [reduceAnimations]);

  const handleSetFollowSystemReducedMotion = useCallback((value) => {
    setFollowSystemReducedMotion(value);
    // When enabling follow system, immediately sync with OS preference
    if (value && typeof window !== "undefined" && window.matchMedia) {
      const prefersReduced = window.matchMedia(
        "(prefers-reduced-motion: reduce)",
      ).matches;
      setReduceAnimations(prefersReduced);
    }
  }, []);

  // Listen for reduce animations changes from SettingsDialog
  useEffect(() => {
    const handleReduceAnimationsChanged = (e) => {
      if (e.detail.followSystem !== undefined) {
        handleSetFollowSystemReducedMotion(e.detail.followSystem);
      }
      if (e.detail.reduceAnimations !== undefined) {
        setReduceAnimations(e.detail.reduceAnimations);
      }
    };
    window.addEventListener(
      "mitto-reduce-animations-changed",
      handleReduceAnimationsChanged,
    );
    return () =>
      window.removeEventListener(
        "mitto-reduce-animations-changed",
        handleReduceAnimationsChanged,
      );
  }, [handleSetFollowSystemReducedMotion]);

  // Font size state - persisted to localStorage
  const [fontSize, setFontSize] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto-font-size");
      if (saved === "small" || saved === "large") return saved;
    }
    return "small"; // Default to small
  });

  // Apply font size class to document
  useEffect(() => {
    const root = document.documentElement;
    if (fontSize === "large") {
      root.classList.add("font-large");
      root.classList.remove("font-small");
    } else {
      root.classList.add("font-small");
      root.classList.remove("font-large");
    }
    localStorage.setItem("mitto-font-size", fontSize);
  }, [fontSize]);

  const toggleFontSize = useCallback(() => {
    setFontSize((prev) => (prev === "small" ? "large" : "small"));
  }, []);

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

  // Threshold for considering user "at bottom"
  // For large scroll ranges (>200px), use a fixed 50px threshold
  // For smaller ranges, use 25% of maxScroll to ensure the button can appear
  const SCROLL_THRESHOLD_PX = 50;
  const SCROLL_THRESHOLD_PERCENT = 0.25;

  // Check if the user is at the bottom of the messages container
  // With flex-col-reverse on the INNER wrapper (not the scrollable container):
  // - scrollTop=0 means we're at the visual TOP (oldest messages)
  // - scrollTop=scrollHeight-clientHeight means we're at the visual BOTTOM (newest messages)
  const checkIfAtBottom = useCallback(() => {
    const container = messagesContainerRef.current;
    if (!container) return true;
    const maxScroll = container.scrollHeight - container.clientHeight;
    // If there's no scrollable content, consider us at bottom
    if (maxScroll <= 0) return true;
    // Use percentage-based threshold for small scroll ranges,
    // fixed threshold for larger ones
    const threshold = Math.min(
      SCROLL_THRESHOLD_PX,
      maxScroll * SCROLL_THRESHOLD_PERCENT,
    );
    const atBottom = container.scrollTop >= maxScroll - threshold;
    if (window.__debug?.scroll)
      console.log("[scroll] checkIfAtBottom:", {
        scrollTop: container.scrollTop,
        scrollHeight: container.scrollHeight,
        clientHeight: container.clientHeight,
        maxScroll,
        threshold,
        atBottom,
      });
    return atBottom;
  }, []);

  // Scroll to bottom handler
  // With flex-col-reverse on inner wrapper, scrollHeight is the visual bottom
  const scrollToBottom = useCallback((smooth = true) => {
    const container = messagesContainerRef.current;
    if (container) {
      container.scrollTo({
        top: container.scrollHeight,
        behavior: smooth ? "smooth" : "auto",
      });
      setIsUserAtBottom(true);
      setHasNewMessages(false);
    }
  }, []);

  // Handle scroll events to track user's scroll position
  useEffect(() => {
    const container = messagesContainerRef.current;
    if (!container) return;

    const handleScroll = (source = "scroll") => {
      const atBottom = checkIfAtBottom();
      if (window.__debug?.scroll)
        console.log(`[scroll] handleScroll(${source}):`, { atBottom });
      setIsUserAtBottom(atBottom);
      // Clear new messages indicator when user scrolls to bottom
      if (atBottom) {
        setHasNewMessages(false);
      }
    };

    // Check initial scroll position on mount
    // This handles cases where content fits in viewport (no scroll event fires)
    // Use requestAnimationFrame to ensure layout is complete before checking
    requestAnimationFrame(() => {
      handleScroll("initial-raf");
    });

    const onScroll = () => handleScroll("event");
    container.addEventListener("scroll", onScroll, { passive: true });
    return () => container.removeEventListener("scroll", onScroll);
  }, [checkIfAtBottom]);

  // Track the active session to detect when we switch sessions
  const prevActiveSessionIdRef = useRef(activeSessionId);
  // Track if we're still in the initial load phase after a session switch
  const sessionJustSwitchedRef = useRef(false);
  // Track previous isLoadingMore state to detect when a "load more" completes
  const prevIsLoadingMoreRef = useRef(false);
  // Track if we just finished loading more (prepend) - skip auto-scroll in this case
  const justLoadedMoreRef = useRef(false);

  // Position at bottom synchronously BEFORE paint when switching sessions
  // This prevents any visible "jump" - the content appears already at the bottom
  useLayoutEffect(() => {
    const currentLength = messages.length;
    const container = messagesContainerRef.current;

    // Helper to scroll to bottom instantly (bypassing CSS scroll-behavior: smooth)
    // With flex-col-reverse on inner wrapper, scrollHeight is the visual bottom
    const scrollToBottomInstant = () => {
      if (!container) return;
      // Temporarily disable smooth scrolling to make scroll instant
      const originalBehavior = container.style.scrollBehavior;
      container.style.scrollBehavior = "auto";
      const beforeScrollTop = container.scrollTop;
      container.scrollTop = container.scrollHeight; // scrollHeight = visual bottom
      if (window.__debug?.scroll)
        console.log("[scroll] scrollToBottomInstant:", {
          beforeScrollTop,
          afterScrollTop: container.scrollTop,
          scrollHeight: container.scrollHeight,
          clientHeight: container.clientHeight,
        });
      // Restore original behavior after the scroll completes
      container.style.scrollBehavior = originalBehavior;
      // Explicitly set state since scroll event may not fire if position doesn't change
      setIsUserAtBottom(true);
      setHasNewMessages(false);
    };

    // Detect session switch (activeSessionId changed)
    const sessionSwitched = prevActiveSessionIdRef.current !== activeSessionId;
    if (sessionSwitched) {
      prevActiveSessionIdRef.current = activeSessionId;
      prevMessagesLengthRef.current = currentLength;

      // Position at bottom instantly - useLayoutEffect ensures this happens BEFORE paint
      if (currentLength > 0) {
        scrollToBottomInstant();
      } else {
        // No messages yet - set flag so we scroll when messages arrive
        sessionJustSwitchedRef.current = true;
      }
      return;
    }

    // If we just switched sessions and now messages appeared, this is the initial load
    // Position at bottom instantly BEFORE paint
    if (sessionJustSwitchedRef.current && currentLength > 0) {
      sessionJustSwitchedRef.current = false;
      prevMessagesLengthRef.current = currentLength;
      scrollToBottomInstant();
      return;
    }
  }, [messages, activeSessionId]);

  // Detect when "load more" (prepend) completes - restore scroll position and skip auto-scroll
  // Uses useLayoutEffect to run BEFORE browser paint, preventing visual jump
  useLayoutEffect(() => {
    // Detect transition from isLoadingMore=true to isLoadingMore=false
    if (prevIsLoadingMoreRef.current && !isLoadingMore) {
      // Load more just completed - set flag to skip auto-scroll for prepended content
      justLoadedMoreRef.current = true;
      if (window.__debug?.scroll)
        console.log("[Scroll] Load more completed, will skip auto-scroll");

      // Restore scroll position to maintain visual position after prepend
      // The new content was added above, so we need to offset scrollTop by the height difference
      const container = messagesContainerRef.current;
      const savedMetrics = scrollPreservationRef.current;
      if (container && savedMetrics) {
        // Temporarily disable smooth scrolling to make scroll position restoration instant
        // Without this, the browser will animate the scroll which causes visual jumping
        const originalBehavior = container.style.scrollBehavior;
        container.style.scrollBehavior = "auto";

        const newScrollHeight = container.scrollHeight;
        const heightDiff = newScrollHeight - savedMetrics.scrollHeight;
        const newScrollTop = savedMetrics.scrollTop + heightDiff;
        container.scrollTop = newScrollTop;
        if (window.__debug?.scroll)
          console.log("[Scroll] Restored scroll position after prepend:", {
            oldScrollHeight: savedMetrics.scrollHeight,
            newScrollHeight,
            heightDiff,
            oldScrollTop: savedMetrics.scrollTop,
            newScrollTop,
          });

        // Restore original scroll behavior after the instant scroll
        container.style.scrollBehavior = originalBehavior;
        scrollPreservationRef.current = null;
      }
    }
    prevIsLoadingMoreRef.current = isLoadingMore;
  }, [isLoadingMore, messages]);

  // Smart auto-scroll for new content during active conversation
  useEffect(() => {
    const currentLength = messages.length;
    const prevLength = prevMessagesLengthRef.current;

    // Skip if this is a session switch (handled by useLayoutEffect above)
    if (prevActiveSessionIdRef.current !== activeSessionId) {
      return;
    }

    // Skip if this is initial load after session switch (handled by useLayoutEffect above)
    if (sessionJustSwitchedRef.current) {
      return;
    }

    // Skip auto-scroll if we just loaded older messages (prepend)
    // The useInfiniteScroll hook handles scroll position restoration for this case
    if (justLoadedMoreRef.current) {
      if (window.__debug?.scroll)
        console.log(
          "[Scroll] Skipping auto-scroll - just loaded older messages",
        );
      justLoadedMoreRef.current = false;
      prevMessagesLengthRef.current = currentLength;
      return;
    }

    const hasNewContent =
      currentLength > prevLength || (isStreaming && currentLength > 0);

    if (hasNewContent) {
      if (isUserAtBottom) {
        // User is at bottom, auto-scroll
        scrollToBottom(true);
      } else {
        // User has scrolled up, show new messages indicator
        setHasNewMessages(true);
      }
    }

    prevMessagesLengthRef.current = currentLength;
  }, [messages, isStreaming, isUserAtBottom, scrollToBottom, activeSessionId]);

  // Reset scroll state when switching sessions
  // The auto-scroll effect above handles the initial positioning after messages load
  useEffect(() => {
    setIsUserAtBottom(true);
    setHasNewMessages(false);
  }, [activeSessionId]);

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
        // If session creation failed due to no workspace configured, open settings
        if (result?.errorCode === "session_creation_backoff") {
          showToast({ style: "warning", title: result.error, duration: 5000 });
        } else if (
          result?.errorCode === "no_workspace_configured" &&
          !configReadonly
        ) {
          setSettingsDialog({ isOpen: true, forceOpen: true });
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
      navigateToNextSession();
    };

    // Previous Conversation - called from native swipe gesture (swipe right)
    window.mittoPrevConversation = () => {
      // Don't navigate if the cursor is over a horizontally scrollable element.
      if (isOverHorizontallyScrollable()) return;
      // Don't navigate if a modal dialog is open.
      if (isModalDialogOpen()) return;
      navigateToPreviousSession();
    };

    // Switch to Session - called from native notification tap
    window.mittoSwitchToSession = (sessionId) => {
      if (sessionId) {
        switchSession(sessionId);
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
      // If session creation failed due to no workspace configured, open settings
      if (result?.errorCode === "session_creation_backoff") {
        showToast({ style: "warning", title: result.error, duration: 5000 });
      } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
        setSettingsDialog({ isOpen: true, forceOpen: true });
      } else {
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
        if (result?.errorCode === "session_creation_backoff") {
          showToast({ style: "warning", title: result.error, duration: 5000 });
        } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
          setSettingsDialog({ isOpen: true, forceOpen: true });
        } else {
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
      // If session creation failed due to no workspace configured, open settings
      if (result?.errorCode === "session_creation_backoff") {
        showToast({ style: "warning", title: result.error, duration: 5000 });
      } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
        setSettingsDialog({ isOpen: true, forceOpen: true });
      } else {
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
    // If session creation failed due to no workspace configured, open settings (unless config is read-only)
    if (result?.errorCode === "session_creation_backoff") {
      showToast({ style: "warning", title: result.error, duration: 5000 });
    } else if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
      setSettingsDialog({ isOpen: true, forceOpen: true });
    } else {
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

  const handleShowWorkspacesForFolder = useCallback((workingDir) => {
    if (configReadonly) return;
    setWorkspacesDialog({ isOpen: true, workingDir });
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
      <${WorkspaceDialog}
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
        WorkspaceBadge=${WorkspaceBadge}
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
        onClose=${() => setWorkspacesDialog({ isOpen: false })}
        WorkspaceBadge=${WorkspaceBadge}
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
          queueLength=${queueLength}
          onFetchConversationPrompts=${fetchConversationPromptsForSession}
          onSendPromptToConversation=${handleSendPromptToConversation}
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
              queueLength=${queueLength}
              onFetchConversationPrompts=${fetchConversationPromptsForSession}
              onSendPromptToConversation=${handleSendPromptToConversation}
            />
          </div>
          <div
            class="flex-1 bg-black/50"
            onClick=${() => setShowSidebar(false)}
          />
        </div>
      `}

      <!-- Main chat area (swipe left/right to switch sessions on mobile) -->
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
          <!-- Messages (scrollable container with normal scroll) -->
          <!-- The inner wrapper uses flex-col-reverse for message ordering -->
          <!-- Note: scrollbar-hide removed for Edge compatibility - scrollbar styled in CSS -->
          <div
            ref=${messagesContainerRef}
            class="absolute inset-0 overflow-y-auto scroll-smooth p-4 messages-container-reverse"
          >
            ${swipeDirection &&
            html`
              <div
                key=${`flash-${activeSessionId}`}
                class="swipe-flash swipe-flash-${swipeDirection}"
              />
            `}
            ${swipeArrow &&
            html`
              <div
                key=${`arrow-${activeSessionId}-${swipeArrow}`}
                class="swipe-arrow-indicator"
              >
                <div class="swipe-arrow-indicator__content">
                  <span class="swipe-arrow-indicator__arrow"
                    >${swipeArrow === "left" ? "→" : "←"}</span
                  >
                </div>
              </div>
            `}
            <div
              key=${activeSessionId}
              class="max-w-2xl mx-auto flex flex-col-reverse ${swipeDirection
                ? `swipe-slide-${swipeDirection}`
                : ""}"
            >
              ${
                /* With column-reverse: first in DOM = visual bottom, last in DOM = visual top
                So we render messages in reverse order (newest first in DOM = visual bottom)
                and put the infinite scroll sentinel at the DOM end (= visual top) */ ""
              }
              ${messages.length === 0 &&
              !hasMoreMessages &&
              html`
                <div class="flex items-center justify-center h-full">
                  <div class="text-center text-gray-400">
                    <img src="./favicon.png" alt="Mitto" class="w-24 h-24 mb-6 opacity-30 mx-auto" />
                    <p class="text-2xl font-medium text-gray-300 mb-4">
                      Welcome to Mitto
                    </p>
                    ${workspaces.length === 0
                      ? html`
                          <p class="text-base text-gray-500 max-w-md">
                            Get started by creating a workspace in Settings
                            (<span class="inline-block align-middle">
                              <${SettingsIcon} className="w-5 h-5 inline" />
                            </span>
                            icon in the sidebar)
                          </p>
                        `
                      : activeSessionId
                        ? html`
                            <p class="text-base text-gray-500">
                              Type a message to start chatting with the AI agent
                            </p>
                          `
                        : html`
                            <div class="text-base text-gray-500 max-w-md">
                              <p>
                                Create a new conversation using the
                                <span
                                  class="inline-flex items-center justify-center w-6 h-6 rounded text-white text-sm font-bold mx-1"
                                  >+</span
                                >
                                button in the sidebar
                              </p>
                              ${workspaces.length > 1
                                ? html`
                                    <p class="text-sm text-gray-600 mt-3">
                                      You'll be able to choose which workspace
                                      to use
                                    </p>
                                  `
                                : ""}
                            </div>
                          `}
                    ${!connected &&
                    html`
                      <p class="text-sm mt-6 text-yellow-500">
                        Connecting to server...
                      </p>
                    `}
                    ${connected &&
                    activeSessionId &&
                    sessionInfo &&
                    !sessionInfo.acp_ready &&
                    !sessionInfo.archived &&
                    html`
                      <p
                        class="text-sm mt-6 text-yellow-500 flex items-center gap-2"
                      >
                        <span
                          class="w-3 h-3 border-2 border-yellow-500 border-t-transparent rounded-full animate-spin"
                        ></span>
                        Connecting to AI agent...
                      </p>
                    `}
                  </div>
                </div>
              `}
              ${
                /* Render coalesced messages in reverse order for column-reverse:
                newest (last in array) becomes first in DOM = visual bottom.
                displayMessages combines consecutive agent messages for cleaner display. */ ""
              }
              ${[...displayMessages]
                .reverse()
                .flatMap(
                  (msg, i, arr) => {
                    // For error messages, find the last user prompt before this error
                    // and offer a retry button to resend it (including any attached images).
                    let retryHandler = undefined;
                    if (msg.role === "error") {
                      const origIdx = arr.length - 1 - i;
                      for (let j = origIdx - 1; j >= 0; j--) {
                        const prev = displayMessages[j];
                        if (prev.role === "user" && prev.text) {
                          const retryText = prev.text;
                          const retryImages = prev.images || [];
                          retryHandler = () => handleSendPrompt(retryText, retryImages);
                          break;
                        }
                      }
                    }

                    // Date separator logic.
                    // The array is rendered in reverse (column-reverse), so index i=0 is the
                    // newest message (visual bottom). "Next" in iteration = older message above.
                    // We show a separator ABOVE a message when its date differs from the
                    // message that immediately follows it chronologically (i.e. arr[i+1] in
                    // the reversed array, which is one step older).
                    const origIdx = arr.length - 1 - i;
                    let dateSeparator = null;
                    if (msg.timestamp) {
                      const msgDate = new Date(msg.timestamp).toDateString();
                      // i+1 in reversed array = the next-older message
                      const olderMsg = arr[i + 1];
                      const olderDate = olderMsg?.timestamp
                        ? new Date(olderMsg.timestamp).toDateString()
                        : null;
                      // Show separator when the date changes (or at the top of the list)
                      if (!olderMsg || msgDate !== olderDate) {
                        const now = new Date();
                        const yesterday = new Date(now);
                        yesterday.setDate(yesterday.getDate() - 1);
                        let label;
                        const d = new Date(msg.timestamp);
                        if (d.toDateString() === now.toDateString()) {
                          label = "Today";
                        } else if (d.toDateString() === yesterday.toDateString()) {
                          label = "Yesterday";
                        } else {
                          label = d.toLocaleDateString([], {
                            month: "short",
                            day: "numeric",
                            year: d.getFullYear() !== now.getFullYear() ? "numeric" : undefined,
                          });
                        }
                        dateSeparator = html`
                          <div
                            key=${"sep-" + origIdx}
                            class="date-separator"
                          >
                            ${label}
                          </div>
                        `;
                      }
                    }

                    const msgEl = html`
                      <${Message}
                        key=${msg.timestamp + "-" + origIdx}
                        message=${msg}
                        isLast=${i === 0}
                        isStreaming=${isStreaming}
                        onRetry=${retryHandler}
                      />
                    `;
                    // In column-reverse, elements added after the message appear visually above it.
                    return dateSeparator ? [dateSeparator, msgEl] : [msgEl];
                  },
                )}
              ${
                /* Load more button / loading indicator / limit reached - at DOM end = visual top */ ""
              }
              ${(hasMoreMessages || hasReachedLimit) &&
              html`
                <div class="flex justify-center my-4">
                  ${isLoadingMore
                    ? html`
                        <div
                          class="px-4 py-2 text-sm text-gray-400 flex items-center gap-2"
                        >
                          <${SpinnerIcon} className="w-4 h-4" />
                          <span>Loading earlier messages...</span>
                        </div>
                      `
                    : hasReachedLimit
                      ? html`
                          <div
                            class="px-4 py-2 text-sm text-gray-500 flex items-center gap-2"
                            data-testid="limit-reached-indicator"
                          >
                            <span>📚</span>
                            <span
                              >Message limit reached (${messages.length}
                              messages loaded)</span
                            >
                          </div>
                        `
                      : html`
                          <button
                            onClick=${handleLoadMore}
                            class="load-more-btn px-4 py-2 text-sm text-gray-400 hover:text-gray-200 hover:bg-gray-700/50 rounded-lg transition-colors flex items-center gap-2"
                            data-testid="load-more-button"
                          >
                            <span>↑</span>
                            <span>Load earlier messages...</span>
                          </button>
                        `}
                </div>
              `}
              ${html`
                <!-- Infinite scroll sentinel: at DOM end = visual top (triggers when scrolling up) -->
                <div ref=${sentinelRef} class="h-1 w-full" aria-hidden="true" />
              `}
            </div>
          </div>
          <!-- End of scrollable messages container -->

          <!-- Scroll to bottom button (positioned relative to wrapper, not scrollable container) -->
          ${(!isUserAtBottom || hasNewMessages) &&
          messages.length > 0 &&
          html`
            <div class="scroll-to-bottom-wrapper">
              <button
                onClick=${() => scrollToBottom(true)}
                class="scroll-to-bottom-btn ${hasNewMessages ? "has-new" : ""}"
                title="Scroll to bottom"
              >
                <${ArrowDownIcon} className="w-5 h-5" />
                ${hasNewMessages &&
                html` <span class="new-messages-indicator"></span> `}
              </button>
            </div>
          `}
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

      <!-- Unified Session Panel (fixed overlay on right) -->
      <${SessionPanel}
        isOpen=${showSidePanel}
        onClose=${handleCloseSidePanel}
        activeTab=${sidePanelTab}
        onTabChange=${setSidePanelTab}
        sessionId=${activeSessionId}
        sessionInfo=${sessionInfo}
        onRename=${renameSession}
        isStreaming=${isStreaming}
        configOptions=${configOptions}
        onSetConfigOption=${setConfigOption}
        mcpTools=${mcpTools}
      />
    </div>
  `;
}

// =============================================================================
// Mount Application
// =============================================================================

render(html`<${App} />`, document.getElementById("app"));
