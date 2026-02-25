// Mitto Web Interface - Preact Application
const {
  h,
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
  ROLE_USER,
  ROLE_AGENT,
  ROLE_THOUGHT,
  ROLE_TOOL,
  ROLE_ERROR,
  ROLE_SYSTEM,
  INITIAL_EVENTS_LIMIT,
  computeAllSessions,
  convertEventsToMessages,
  coalesceAgentMessages,
  COALESCE_DEFAULTS,
  safeJsonParse,
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

// Import utilities
import {
  openExternalURL,
  openFileURL,
  convertHTTPFileURLToFile,
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
  getAPIPrefix,
  initUIPreferences,
  onUIPreferencesLoaded,
  FILTER_TAB,
  getFilterTab,
  setFilterTab,
  getFilterTabGrouping,
  cycleFilterTabGrouping,
} from "./utils/index.js";

// Import hooks
import {
  useWebSocket,
  useSwipeNavigation,
  useSwipeToAction,
  useInfiniteScroll,
} from "./hooks/index.js";

// Import components
import { Message } from "./components/Message.js";
import { ChatInput } from "./components/ChatInput.js";
import { SettingsDialog } from "./components/SettingsDialog.js";
import { QueueDropdown } from "./components/QueueDropdown.js";
import {
  AgentPlanPanel,
  AgentPlanIndicator,
} from "./components/AgentPlanPanel.js";
import { ConversationPropertiesPanel } from "./components/ConversationPropertiesPanel.js";
import { PeriodicFrequencyPanel } from "./components/PeriodicFrequencyPanel.js";
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
  QueueIcon,
  PinIcon,
  PinFilledIcon,
  ArchiveIcon,
  ArchiveFilledIcon,
  ListIcon,
  PeriodicIcon,
  PeriodicFilledIcon,
  ChatBubbleIcon,
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
// File Link Helpers
// =============================================================================

/**
 * Determines if a file link should open directly (browser renders it) or in the viewer.
 * HTML files and Markdown files should open directly since the browser/server renders them.
 * Other files should open in the syntax-highlighted viewer.
 * @param {string} href - The file API URL (e.g., /mitto/api/files?ws=...&path=...)
 * @returns {boolean} True if the file should open directly, false if it should open in viewer
 */
function shouldOpenFileDirectly(href) {
  try {
    // Parse the URL to extract the path parameter
    const url = new URL(href, window.location.origin);
    const path = url.searchParams.get("path") || "";

    // Get file extension
    const ext = path.split(".").pop()?.toLowerCase() || "";

    // HTML files should always open directly (browser renders them)
    if (ext === "html" || ext === "htm") {
      return true;
    }

    // Markdown files should always open directly (server renders them as HTML)
    // The render=html parameter will be added if not present
    if (ext === "md" || ext === "markdown") {
      return true;
    }

    // All other files should open in the viewer (syntax highlighting)
    return false;
  } catch (e) {
    console.error("[Mitto] Error checking file type:", e);
    return false;
  }
}

// =============================================================================
// Global Link Click Handler
// =============================================================================

// Intercept clicks on external links (http/https), file links (file://),
// and internal file API links to prevent WebView navigation.
// In the native macOS app, this ensures:
// - External links open in the default browser
// - File links open in the default application for that file type
// - Internal file API links (used in web mode) are converted to file:// URLs
document.addEventListener("click", (e) => {
  // Find the closest anchor element (handles clicks on nested elements inside links)
  const link = e.target.closest("a");
  if (!link) return;

  const href = link.getAttribute("href");
  if (!href) return;

  console.log("[Mitto] Link clicked:", href, "isNativeApp:", isNativeApp());

  // Handle file:// URLs (open in default application)
  if (href.startsWith("file://")) {
    console.log("[Mitto] Handling as file:// URL");
    e.preventDefault();
    e.stopPropagation();
    openFileURL(href);
    return;
  }

  // Handle internal file API links (e.g., /mitto/api/files?workspace=...&path=...)
  if (href.includes("/api/files?")) {
    console.log("[Mitto] Handling as /api/files link");
    e.preventDefault();
    e.stopPropagation();
    if (isNativeApp()) {
      // Native macOS app - convert to file:// URL and open with system app
      const fileUrl = convertHTTPFileURLToFile(href);
      console.log("[Mitto] Converted to file URL:", fileUrl);
      if (fileUrl) {
        openFileURL(fileUrl);
      }
    } else {
      // Web browser - determine how to open the file
      // HTML files and Markdown files should open directly (browser/server renders them)
      // Other files should open in the syntax-highlighted viewer
      const shouldOpenDirectly = shouldOpenFileDirectly(href);
      if (shouldOpenDirectly) {
        // Open directly via API - browser will render HTML/Markdown
        // Fix old links missing API prefix
        const apiPrefix = getAPIPrefix();
        let finalUrl = href;
        if (apiPrefix && !href.includes(apiPrefix)) {
          // Old link without prefix - add the current prefix
          finalUrl = finalUrl.replace("/api/files?", apiPrefix + "/api/files?");
          console.log("[Mitto] Fixed old API link:", href, "->", finalUrl);
        }
        // Ensure markdown files have render=html parameter for proper rendering
        // (old recordings may have links without this parameter)
        const url = new URL(finalUrl, window.location.origin);
        const path = url.searchParams.get("path") || "";
        const ext = path.split(".").pop()?.toLowerCase() || "";
        if (
          (ext === "md" || ext === "markdown") &&
          url.searchParams.get("render") !== "html"
        ) {
          url.searchParams.set("render", "html");
          finalUrl = url.toString();
          console.log("[Mitto] Added render=html for markdown file:", finalUrl);
        }
        window.open(finalUrl, "_blank", "noopener,noreferrer");
      } else {
        // Open in viewer page with syntax highlighting
        const viewerUrl = convertHTTPFileURLToViewer(href);
        console.log("[Mitto] Converted to viewer URL:", viewerUrl);
        if (viewerUrl) {
          window.open(viewerUrl, "_blank", "noopener,noreferrer");
        }
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
  const displayName = hideAcpServer ? wsDisplayName : (acpServer || wsDisplayName);

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
      ${!hideAbbreviation && html`<span class="font-bold">${abbreviation}</span>`}
      <span class="truncate max-w-[80px]">${displayName}</span>
    </div>
  `;
}

// NOTE: SessionPropertiesDialog has been removed.
// Session properties are now edited via the ConversationPropertiesPanel (right sidebar).

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

function WorkspaceDialog({ isOpen, workspaces, onSelect, onCancel }) {
  const [filterText, setFilterText] = useState("");
  const filterInputRef = useRef(null);

  // Show filter only when there are more than WORKSPACE_FILTER_THRESHOLD workspaces
  const showFilter = workspaces.length > WORKSPACE_FILTER_THRESHOLD;

  // Sort workspaces alphabetically by working_dir for deterministic ordering
  const sortedWorkspaces = useMemo(() => {
    return [...workspaces].sort((a, b) =>
      a.working_dir.localeCompare(b.working_dir),
    );
  }, [workspaces]);

  // Filter workspaces based on filter text (case-insensitive substring match on name or friendly name)
  const filteredWorkspaces = useMemo(() => {
    if (!filterText.trim()) return sortedWorkspaces;
    const lowerFilter = filterText.toLowerCase();
    return sortedWorkspaces.filter((ws) => {
      // Match against friendly name if set, otherwise basename
      const displayName = ws.name || getBasename(ws.working_dir);
      return displayName.toLowerCase().includes(lowerFilter);
    });
  }, [sortedWorkspaces, filterText]);

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
        if (index < filteredWorkspaces.length) {
          e.preventDefault();
          onSelect(filteredWorkspaces[index]);
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, filteredWorkspaces, filterText, onSelect, onCancel]);

  if (!isOpen) return null;

  // Help text varies based on whether filter is shown
  const helpText = showFilter
    ? `Type to filter, or press 1-${WORKSPACE_FILTER_THRESHOLD} to select.`
    : "Click on a workspace or press its number to select it.";

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
                      filteredWorkspaces.length,
                    )
                ) {
                  e.preventDefault();
                  const workspace = filteredWorkspaces[num - 1];
                  if (workspace) {
                    onSelect(workspace);
                  }
                }
              }}
              placeholder="Filter workspaces..."
              autofocus
              class="w-full px-3 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-sm focus:outline-none focus:border-blue-500 placeholder-gray-500"
            />
          </div>
        `}

        <div class="space-y-2">
          ${filteredWorkspaces.length === 0
            ? html`
                <div class="text-center py-4 text-gray-500">
                  No workspaces match your filter.
                </div>
              `
            : filteredWorkspaces.map(
                (ws, index) => html`
                  <button
                    key=${ws.working_dir}
                    onClick=${() => onSelect(ws)}
                    class="w-full p-4 text-left rounded-lg bg-slate-700/50 hover:bg-slate-700 transition-colors flex items-center gap-4"
                  >
                    ${index < WORKSPACE_FILTER_THRESHOLD
                      ? html`
                          <div
                            class="w-8 h-8 flex items-center justify-center rounded-lg bg-slate-600 text-gray-300 font-mono text-sm flex-shrink-0"
                          >
                            ${index + 1}
                          </div>
                        `
                      : html` <div class="w-8 h-8 flex-shrink-0"></div> `}
                    <${WorkspaceBadge}
                      path=${ws.working_dir}
                      customColor=${ws.color}
                      customCode=${ws.code}
                      size="lg"
                    />
                    <div class="flex-1 min-w-0">
                      <div class="font-medium">
                        ${ws.name || getBasename(ws.working_dir)}
                      </div>
                      <div
                        class="text-xs text-gray-500 truncate"
                        title=${ws.working_dir}
                      >
                        ${ws.working_dir}
                      </div>
                      <div class="text-xs text-blue-400 mt-1">
                        ${ws.acp_server}
                      </div>
                    </div>
                  </button>
                `,
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
          <button
            key=${item.label}
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
  const elapsedColor = isUrgent ? themeColors.urgentElapsed : themeColors.elapsed;
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
}) {
  const [showActions, setShowActions] = useState(false);
  const [contextMenu, setContextMenu] = useState(null);

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

  // Toggle periodic prompt enabled/disabled
  const handleTogglePeriodic = useCallback(
    async (e) => {
      if (e) e.stopPropagation();
      try {
        // Get current periodic config first
        const getResponse = await secureFetch(
          apiUrl(`/api/sessions/${session.session_id}/periodic`),
        );
        if (getResponse.status === 404) {
          // No periodic configured - create one with enabled=false (unlocked/paused)
          // User will configure prompt in the textarea and lock it to enable periodic runs
          // Default to 1 hour - frequent enough to be useful, but not overwhelming
          const createResponse = await secureFetch(
            apiUrl(`/api/sessions/${session.session_id}/periodic`),
            {
              method: "PUT",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                prompt: "(pending)", // Placeholder - user must set via lock button
                frequency: { value: 1, unit: "hours" },
                enabled: false, // Start unlocked - user must lock to enable periodic runs
              }),
            },
          );
          if (!createResponse.ok) {
            console.error("Failed to create periodic config");
          }
          // Server broadcasts the update
          return;
        }
        if (!getResponse.ok) {
          console.error("Failed to get periodic config");
          return;
        }
        // Periodic config exists - DELETE to convert back to regular conversation
        // This removes periodic entirely (regardless of locked/unlocked state)
        const response = await secureFetch(
          apiUrl(`/api/sessions/${session.session_id}/periodic`),
          { method: "DELETE" },
        );
        if (!response.ok) {
          console.error("Failed to delete periodic config");
        }
        // Server broadcasts the update, no need to update local state
      } catch (err) {
        console.error("Failed to toggle periodic:", err);
      }
    },
    [session.session_id],
  );

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
        parts.push(`Next run: ${nextDate.toLocaleString()} (in ${relativeTime})`);
      }
    }

    return parts.join("\n");
  };

  // Determine swipe action based on filter tab:
  // - Archived tab: swipe to delete
  // - Regular/Periodic tabs: swipe to archive
  const isSwipeToDelete = filterTab === FILTER_TAB.ARCHIVED;

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

  const contextMenuItems = [
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
    {
      label: "Properties",
      icon: html`<${EditIcon} />`,
      onClick: () => handleRename(),
    },
    // Hide periodic option for archived sessions
    ...(isArchived
      ? []
      : [
          {
            label: isPeriodicEnabled ? "Disable Periodic" : "Enable Periodic",
            icon: isPeriodicEnabled
              ? html`<${PeriodicFilledIcon} />`
              : html`<${PeriodicIcon} />`,
            onClick: () => handleTogglePeriodic(),
          },
        ]),
    {
      label: "Delete",
      icon: html`<${TrashIcon} />`,
      onClick: () => handleDelete(),
      danger: true,
    },
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
        class="session-item-container relative overflow-hidden border-b border-slate-700"
        ...${containerProps}
      >
        <!-- Swipe action background (revealed when swiping left) -->
        <!-- Shows Archive (amber) for regular/periodic tabs, Delete (red) for archived tab -->
        <div
          class="absolute inset-0 ${isSwipeToDelete ? "bg-red-600" : "bg-amber-600"} flex items-center justify-end pr-6 transition-opacity"
          style="opacity: ${isRevealed || absOffset > 20 ? 1 : 0}"
        >
          <button
            onClick=${(e) => {
              e.preventDefault();
              e.stopPropagation();
              triggerAction();
            }}
            class="p-3 rounded-full ${isSwipeToDelete ? "bg-red-700 hover:bg-red-800" : "bg-amber-700 hover:bg-amber-800"} transition-colors"
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
            : ""} ${isSwiping ? "" : "transition-transform duration-200"}"
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
        <div class="flex items-start gap-2">
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2">
              ${isStreaming
                ? html`
                    <span
                      class="w-2 h-2 bg-blue-400 rounded-full flex-shrink-0 streaming-indicator"
                      title="Receiving response..."
                    ></span>
                  `
                : isActiveSession
                  ? html`
                      <span
                        class="w-2 h-2 bg-green-400 rounded-full flex-shrink-0"
                      ></span>
                    `
                  : null}
              <span class="text-sm font-medium truncate">${displayName}</span>
            </div>
          </div>
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
        <!-- Bottom row: saved/stored badge and action buttons -->
        <div class="flex items-center justify-between mt-1">
          <div class="flex items-center gap-2">
            ${session.isActive
              ? html`
                  <span class="text-gray-500" title="Session is auto-saved">
                    <${SaveIcon} className="w-3 h-3" />
                  </span>
                `
              : html`
                  <span
                    class="text-xs px-1.5 py-0.5 rounded bg-slate-700 text-gray-400"
                    >stored</span
                  >
                `}
          </div>
          <div
            class="flex items-center gap-1 ${showActions
              ? "opacity-100"
              : "opacity-0"} transition-opacity flex-shrink-0"
          >
            <button
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
                  ? "Unarchive"
                  : "Archive"}"
            >
              ${isArchived
                ? html`<${ArchiveFilledIcon} className="w-4 h-4" />`
                : html`<${ArchiveIcon} className="w-4 h-4" />`}
            </button>
            <button
              onClick=${handleRename}
              class="p-1.5 bg-slate-700 hover:bg-slate-600 rounded transition-colors text-gray-300 hover:text-white"
              title="Properties"
            >
              <${EditIcon} className="w-4 h-4" />
            </button>
            ${!isArchived &&
            html`<button
              onClick=${handleTogglePeriodic}
              class="p-1.5 ${isPeriodicEnabled
                ? "bg-white hover:bg-gray-100 dark:bg-slate-600 dark:hover:bg-slate-500"
                : "bg-slate-700 hover:bg-slate-600"} rounded transition-colors ${isPeriodicEnabled
                ? "text-blue-600 dark:text-blue-400"
                : "text-gray-300 hover:text-white"}"
              title="${isPeriodicEnabled
                ? "Periodic enabled - click to disable"
                : "Periodic disabled - click to enable"}"
            >
              ${isPeriodicEnabled
                ? html`<${PeriodicFilledIcon} className="w-4 h-4" />`
                : html`<${PeriodicIcon} className="w-4 h-4" />`}
            </button>`}
            <button
              onClick=${handleDelete}
              class="p-1.5 bg-slate-700 hover:bg-red-600 rounded transition-colors text-gray-300 hover:text-white"
              title="Delete"
            >
              <${TrashIcon} className="w-4 h-4" />
            </button>
          </div>
        </div>
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
  onShowKeyboardShortcuts,
  configReadonly = false,
  rcFilePath = null,
  badgeClickEnabled = false,
  onBadgeClick,
  queueLength = 0,
}) {
  // Combine active and stored sessions using shared helper function
  // Note: Not using useMemo to ensure working_dir is always up-to-date
  const allSessions = computeAllSessions(activeSessions, storedSessions);

  const isLight = theme === "light";
  const isLargeFont = fontSize === "large";

  // Filter tab state - initialized from localStorage
  const [filterTab, setFilterTabState] = useState(() => getFilterTab());

  // Grouping state - initialized from the current filter tab's grouping setting
  const [groupingMode, setGroupingModeState] = useState(() =>
    getFilterTabGrouping(getFilterTab()),
  );
  // Track expanded groups - use a counter to force re-render when localStorage changes
  const [expandedGroupsVersion, setExpandedGroupsVersion] = useState(0);

  // Subscribe to UI preferences loaded from server (for macOS app where localStorage doesn't persist)
  useEffect(() => {
    const unsubscribe = onUIPreferencesLoaded((prefs) => {
      // Re-read grouping mode for the current tab from localStorage (which was just synced from server)
      const currentTab = getFilterTab();
      const newMode = getFilterTabGrouping(currentTab);
      setGroupingModeState(newMode);
      // Force re-render for expanded groups
      setExpandedGroupsVersion((v) => v + 1);
      console.debug(
        "[Mitto] SessionList: UI preferences synced from server, tab:",
        currentTab,
        "mode:",
        newMode,
      );
    });
    return unsubscribe;
  }, []);

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

  // Handle group expand/collapse toggle
  const handleToggleGroup = useCallback(
    (groupKey, allGroupKeys = []) => {
      const currentlyExpanded = isGroupExpanded(groupKey);
      const willExpand = !currentlyExpanded;

      // In accordion mode, collapse all other groups when expanding
      if (willExpand && getSingleExpandedGroupMode()) {
        // Use provided allGroupKeys to collapse all other groups
        // (needed because getExpandedGroups only tracks explicitly set groups)
        for (const key of allGroupKeys) {
          if (key !== groupKey && isGroupExpanded(key)) {
            setGroupExpanded(key, false);
          }
        }
      }

      setGroupExpanded(groupKey, willExpand);
      setExpandedGroupsVersion((v) => v + 1); // Force re-render
    },
    [],
  );

  // Get grouping icon based on current mode
  const getGroupingIcon = () => {
    switch (groupingMode) {
      case "server":
        return html`<${ServerIcon} className="w-5 h-5" />`;
      case "workspace":
        return html`<${FolderIcon} className="w-5 h-5" />`;
      default:
        return html`<${ListIcon} className="w-5 h-5" />`;
    }
  };

  // Get grouping tooltip based on current mode
  const getGroupingTooltip = () => {
    switch (groupingMode) {
      case "server":
        return "Grouped by ACP server (click to group by workspace)";
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
  const { regularSessions, periodicSessions, archivedSessions } = useMemo(() => {
    const regular = [];
    const periodic = [];
    const archived = [];
    allSessions.forEach((session) => {
      if (session.archived) {
        archived.push(session);
      } else if (session.periodic_enabled) {
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

  // Check which filter tabs have streaming sessions (for pulsing animation)
  const streamingTabs = useMemo(() => {
    return {
      conversations: regularSessions.some((s) => s.isStreaming),
      periodic: periodicSessions.some((s) => s.isStreaming),
      archived: archivedSessions.some((s) => s.isStreaming),
    };
  }, [regularSessions, periodicSessions, archivedSessions]);

  // Group sessions based on current mode (uses filtered sessions)
  const groupedSessions = useMemo(() => {
    if (groupingMode === "none") {
      return null; // No grouping, render flat list
    }

    const groups = new Map();

    // Group filtered sessions
    filteredSessions.forEach((session) => {
      let groupKey;
      let groupLabel;
      let groupWorkingDir = "";
      let groupAcpServer = "";

      if (groupingMode === "server") {
        // Inline getSessionServer logic to avoid stale closure
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        groupKey = session.acp_server || storedSession?.acp_server || "Unknown";
        groupLabel = groupKey;
      } else {
        // workspace mode - group by workspace (working_dir + acp_server combination)
        // This ensures workspaces with the same folder but different ACP servers are separate groups
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        const workingDir =
          session.working_dir ||
          storedSession?.working_dir ||
          getGlobalWorkingDir(session.session_id) ||
          "";
        const acpServer =
          session.acp_server || storedSession?.acp_server || "";
        // Use composite key: working_dir|acp_server (to separate same-folder workspaces)
        groupKey = `${workingDir}|${acpServer}`;
        // Label is just the basename - acpServer is shown as a badge
        const basename = workingDir ? getBasename(workingDir) : "Unknown";
        groupLabel = basename;
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

    // Convert to array and sort by label
    return Array.from(groups.entries())
      .map(([key, value]) => ({ key, ...value }))
      .sort((a, b) => a.label.localeCompare(b.label));
  }, [filteredSessions, groupingMode, storedSessions]);

  // Enforce accordion mode when groups change (e.g., tab switch, grouping mode change)
  // If multiple groups are expanded and accordion mode is enabled, collapse all but the first.
  useEffect(() => {
    if (!groupedSessions || !getSingleExpandedGroupMode()) {
      return;
    }

    // Find all currently expanded groups in the current view
    const expandedKeys = groupedSessions
      .filter((g) => isGroupExpanded(g.key))
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
      for (const key of toCollapse) {
        setGroupExpanded(key, false);
      }
      // Force re-render to reflect the collapsed state
      setExpandedGroupsVersion((v) => v + 1);
    }
  }, [groupedSessions, filterTab, groupingMode]);

  // Render a single session item
  // hideBadge: if true, hides the entire badge
  // badgeHideAbbreviation: if true, badge hides 3-letter workspace code (used in workspace grouping mode)
  // badgeHideAcpServer: if true, badge hides ACP server name (used in ACP server grouping mode)
  const renderSessionItem = (session, { hideBadge = false, badgeHideAbbreviation = false, badgeHideAcpServer = false } = {}) => {
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

    return html`
      <${SessionItem}
        key=${session.session_id}
        session=${finalSession}
        isActive=${activeSessionId === session.session_id}
        onSelect=${onSelect}
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
      />
    `;
  };

  // Handle creating a new session in a specific workspace group
  const handleNewSessionInGroup = useCallback(
    (groupKey, e) => {
      // Prevent the click from toggling the group
      e.stopPropagation();

      // Find the workspace that matches this group key
      // For workspace mode, groupKey is "working_dir|acp_server" (composite key)
      // For server mode, groupKey is the acp_server
      let workspace = null;
      if (groupingMode === "workspace") {
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
        onNewSession(workspace);
      } else {
        // Fallback to default new session behavior
        onNewSession();
      }
    },
    [groupingMode, workspaces, onNewSession],
  );

  // Render grouped sessions with collapsible headers
  const renderGroupedSessions = () => {
    if (!groupedSessions) return null;

    // Get all group keys for accordion mode
    const allGroupKeys = groupedSessions.map((g) => g.key);

    return html`
      ${groupedSessions.map((group) => {
        const expanded = isGroupExpanded(group.key);
        const sessionCount = group.sessions.length;
        // Check if any session in this group is actively streaming
        const hasStreamingSession = group.sessions.some((s) => s.isStreaming);
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
              class="w-full px-4 py-2 flex items-center gap-2 text-sm font-medium text-gray-400 hover:text-white hover:bg-slate-700/50 transition-colors sticky top-0 bg-slate-800 z-10 cursor-pointer group/header"
              onClick=${() => handleToggleGroup(group.key, allGroupKeys)}
            >
              <span
                class="transition-transform ${expanded ? "" : "-rotate-90"}"
              >
                <${ChevronDownIcon} className="w-4 h-4" />
              </span>
              ${groupingMode === "server"
                ? html`<${ServerIcon} className="w-4 h-4" />`
                : html`<${FolderIcon} className="w-4 h-4" />`}
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
              filterTab === FILTER_TAB.CONVERSATIONS &&
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
            group.sessions.map((session) =>
              renderSessionItem(session, {
                // In workspace grouping, hide entire badge (both abbreviation and ACP server are in group header)
                hideBadge: groupingMode === "workspace",
                // In server grouping, hide the ACP server name (redundant with group header)
                badgeHideAcpServer: groupingMode === "server",
              }),
            )}
          </div>
        `;
      })}
    `;
  };

  // Render sessions in "none" grouping mode - flat list
  const renderUngroupedSessions = () => {
    return filteredSessions.map((session) => renderSessionItem(session));
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
    <div class="h-full flex flex-col">
      <div
        class="p-4 border-b border-slate-700 flex items-center justify-between"
      >
        <h2 class="font-semibold text-lg">Conversations</h2>
        <div class="flex items-center gap-2">
          <button
            onClick=${handleToggleGrouping}
            class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
            title=${getGroupingTooltip()}
          >
            ${getGroupingIcon()}
          </button>
          <button
            onClick=${() => onNewSession()}
            class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
            title="New Conversation"
          >
            <${PlusIcon} className="w-5 h-5" />
          </button>
          ${onClose &&
          html`
            <button
              onClick=${onClose}
              class="p-2 hover:bg-slate-700 rounded-lg transition-colors md:hidden"
              title="Close"
            >
              <${CloseIcon} className="w-5 h-5" />
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
          ${regularSessions.length > 0 &&
          html`<span class="ml-1.5 text-xs">${regularSessions.length}</span>`}
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
          ${periodicSessions.length > 0 &&
          html`<span class="ml-1.5 text-xs">${periodicSessions.length}</span>`}
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
          ${archivedSessions.length > 0 &&
          html`<span class="ml-1.5 text-xs">${archivedSessions.length}</span>`}
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
          <!-- Settings button (disabled with tooltip when using RC file, hidden when fully read-only without RC file) -->
          ${!configReadonly
            ? html`
                <button
                  onClick=${onShowSettings}
                  class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                  title="Settings"
                >
                  <${SettingsIcon}
                    className="w-5 h-5 text-gray-400 hover:text-white"
                  />
                </button>
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
    availableCommands,
    configOptions,
    setConfigOption,
    activeUIPrompt,
    sendUIPromptAnswer,
  } = useWebSocket();

  const [showSidebar, setShowSidebar] = useState(false);
  const [showPropertiesPanel, setShowPropertiesPanel] = useState(false);
  const [showQueueDropdown, setShowQueueDropdown] = useState(false);
  const [isDeletingQueueMessage, setIsDeletingQueueMessage] = useState(false);
  const [isMovingQueueMessage, setIsMovingQueueMessage] = useState(false);
  const [isAddingToQueue, setIsAddingToQueue] = useState(false);
  const [queueToastVisible, setQueueToastVisible] = useState(false);
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
  const [settingsDialog, setSettingsDialog] = useState({
    isOpen: false,
    forceOpen: false,
  }); // Settings dialog
  const [keyboardShortcutsDialog, setKeyboardShortcutsDialog] = useState({
    isOpen: false,
  }); // Keyboard shortcuts dialog
  const [globalPrompts, setGlobalPrompts] = useState([]); // Global prompts from web.prompts
  const [globalPromptsACPServer, setGlobalPromptsACPServer] = useState(null); // ACP server used when fetching global prompts
  const [acpServersWithPrompts, setAcpServersWithPrompts] = useState([]); // ACP servers with their per-server prompts
  const [workspacePrompts, setWorkspacePrompts] = useState([]); // Workspace-specific prompts from .mittorc
  const [workspacePromptsDir, setWorkspacePromptsDir] = useState(null); // Current workspace dir for prompts cache
  const [workspacePromptsLastModified, setWorkspacePromptsLastModified] =
    useState(null); // Last-Modified header for conditional requests
  const [configReadonly, setConfigReadonly] = useState(
    () => window.mittoIsExternal === true, // Start as true for external connections, or when --config flag was used or using RC file
  );
  const [rcFilePath, setRcFilePath] = useState(null); // Path to RC file when config is read-only due to RC file
  const [swipeDirection, setSwipeDirection] = useState(null); // 'left' or 'right' for animation
  const [swipeArrow, setSwipeArrow] = useState(null); // 'left' or 'right' for arrow indicator
  const [toastVisible, setToastVisible] = useState(false);
  const [toastData, setToastData] = useState(null); // { sessionId, sessionName }
  const [periodicToastVisible, setPeriodicToastVisible] = useState(false);
  const [periodicToastData, setPeriodicToastData] = useState(null); // { sessionId, sessionName }
  const [uiPromptToastVisible, setUIPromptToastVisible] = useState(false);
  const [uiPromptToastData, setUIPromptToastData] = useState(null); // { sessionId, sessionName, question }
  const [runnerFallbackWarning, setRunnerFallbackWarning] = useState(null); // { requestedType, fallbackType, reason }
  const [acpStartFailedError, setAcpStartFailedError] = useState(null); // { session_id, error }
  const [hookFailedError, setHookFailedError] = useState(null); // { name, exit_code, error }
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

  // Compute merged prompts: workspace prompts (highest priority) + global prompts + server-specific prompts
  // Workspace prompts override global/server prompts with the same name
  // Prompts are filtered by the current ACP server TYPE using the "acps" field
  const predefinedPrompts = useMemo(() => {
    const currentAcpServerName = sessionInfo?.acp_server || "";
    // Look up the server's type from acpServersWithPrompts.
    // Servers with the same type share prompts (e.g., auggie-fast and auggie-smart
    // can both have type "auggie" to share prompts with acps: auggie).
    const currentServerConfig = acpServersWithPrompts.find(
      (s) => s.name === currentAcpServerName,
    );
    // Use type if specified, otherwise fall back to name (consistent with backend behavior)
    const currentAcpServerType = (currentServerConfig?.type || currentAcpServerName).toLowerCase();

    // Helper to check if a prompt is allowed for the current ACP server type
    // If acps is empty, the prompt is allowed for all servers
    // Otherwise, check if the current server type is in the comma-separated list
    const isAllowedForACP = (prompt) => {
      if (!prompt.acps || prompt.acps.trim() === "") {
        return true; // No restriction, allowed for all
      }
      if (!currentAcpServerType) {
        return true; // No ACP server selected, show all prompts
      }
      // Parse comma-separated list and check for match (case-insensitive)
      const allowedServers = prompt.acps
        .split(",")
        .map((s) => s.trim().toLowerCase());
      return allowedServers.includes(currentAcpServerType);
    };

    // Build a map of prompt names to prompts, with workspace prompts having highest priority
    const promptMap = new Map();

    // First add global prompts (lowest priority), filtered by ACP server
    for (const p of globalPrompts) {
      if (isAllowedForACP(p)) {
        promptMap.set(p.name, { ...p, source: "global" });
      }
    }

    // Then add server-specific prompts (medium priority)
    if (sessionInfo?.acp_server && acpServersWithPrompts.length > 0) {
      const server = acpServersWithPrompts.find(
        (s) => s.name === sessionInfo.acp_server,
      );
      if (server?.prompts?.length > 0) {
        for (const p of server.prompts) {
          if (isAllowedForACP(p)) {
            promptMap.set(p.name, { ...p, source: "server" });
          }
        }
      }
    }

    // Finally add workspace prompts (highest priority - override others with same name)
    // Workspace prompts are also filtered by ACP server
    for (const p of workspacePrompts) {
      if (isAllowedForACP(p)) {
        promptMap.set(p.name, { ...p, source: "workspace" });
      }
    }

    // Convert map back to array, maintaining order: workspace first, then server, then global
    const result = [];
    // Add workspace prompts first (visually distinct section)
    for (const p of workspacePrompts) {
      const entry = promptMap.get(p.name);
      if (entry && entry.source === "workspace") {
        result.push(entry);
      }
    }
    // Add remaining prompts (server + global that weren't overridden)
    for (const [name, entry] of promptMap) {
      if (entry.source !== "workspace") {
        result.push(entry);
      }
    }

    return result;
  }, [
    globalPrompts,
    sessionInfo?.acp_server,
    acpServersWithPrompts,
    workspacePrompts,
  ]);

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

  // Ref to track toast hide timer
  const toastTimerRef = useRef(null);

  // Show toast and native notification when a background session completes
  useEffect(() => {
    if (backgroundCompletion) {
      // Clear any existing timer
      if (toastTimerRef.current) {
        clearTimeout(toastTimerRef.current);
      }

      // Check if native notifications are enabled (macOS app only)
      const useNativeNotification =
        window.mittoNativeNotificationsEnabled &&
        typeof window.mittoShowNativeNotification === "function";

      if (useNativeNotification) {
        // Show native macOS notification
        window.mittoShowNativeNotification(
          backgroundCompletion.sessionName || "Conversation",
          "Agent completed",
          backgroundCompletion.sessionId,
        );
      }

      // Always show in-app toast (in addition to native notification if enabled)
      setToastData(backgroundCompletion);
      setToastVisible(true);
      clearBackgroundCompletion();

      // Set timer to hide toast after 5 seconds
      toastTimerRef.current = setTimeout(() => {
        setToastVisible(false);
        toastTimerRef.current = null;
      }, 5000);
    }
  }, [backgroundCompletion, clearBackgroundCompletion]);

  // Clear toast data after exit animation completes
  useEffect(() => {
    if (!toastVisible && toastData) {
      const clearTimer = setTimeout(() => {
        setToastData(null);
      }, 200);
      return () => clearTimeout(clearTimer);
    }
  }, [toastVisible, toastData]);

  // Ref to track periodic toast hide timer
  const periodicToastTimerRef = useRef(null);

  // Show toast and native notification when a periodic prompt starts
  useEffect(() => {
    if (periodicStarted) {
      // Clear any existing timer
      if (periodicToastTimerRef.current) {
        clearTimeout(periodicToastTimerRef.current);
      }

      // Check if native notifications are enabled (macOS app only)
      const useNativeNotification =
        window.mittoNativeNotificationsEnabled &&
        typeof window.mittoShowNativeNotification === "function";

      if (useNativeNotification) {
        // Show native macOS notification
        window.mittoShowNativeNotification(
          periodicStarted.sessionName || "Periodic Conversation",
          "Periodic run started",
          periodicStarted.sessionId,
        );
      }

      // Always show in-app toast (in addition to native notification if enabled)
      setPeriodicToastData(periodicStarted);
      setPeriodicToastVisible(true);
      clearPeriodicStarted();

      // Set timer to hide toast after 5 seconds
      periodicToastTimerRef.current = setTimeout(() => {
        setPeriodicToastVisible(false);
        periodicToastTimerRef.current = null;
      }, 5000);
    }
  }, [periodicStarted, clearPeriodicStarted]);

  // Clear periodic toast data after exit animation completes
  useEffect(() => {
    if (!periodicToastVisible && periodicToastData) {
      const clearTimer = setTimeout(() => {
        setPeriodicToastData(null);
      }, 200);
      return () => clearTimeout(clearTimer);
    }
  }, [periodicToastVisible, periodicToastData]);

  // Ref to track UI prompt toast hide timer
  const uiPromptToastTimerRef = useRef(null);

  // Show toast when a UI prompt arrives in a background session
  useEffect(() => {
    if (backgroundUIPrompt) {
      // Clear any existing timer
      if (uiPromptToastTimerRef.current) {
        clearTimeout(uiPromptToastTimerRef.current);
      }

      // Show in-app toast (native notification is handled in useWebSocket)
      setUIPromptToastData(backgroundUIPrompt);
      setUIPromptToastVisible(true);
      clearBackgroundUIPrompt();

      // Set timer to hide toast after 8 seconds (longer for questions)
      uiPromptToastTimerRef.current = setTimeout(() => {
        setUIPromptToastVisible(false);
        uiPromptToastTimerRef.current = null;
      }, 8000);
    }
  }, [backgroundUIPrompt, clearBackgroundUIPrompt]);

  // Clear UI prompt toast data after exit animation completes
  useEffect(() => {
    if (!uiPromptToastVisible && uiPromptToastData) {
      const clearTimer = setTimeout(() => {
        setUIPromptToastData(null);
      }, 200);
      return () => clearTimeout(clearTimer);
    }
  }, [uiPromptToastVisible, uiPromptToastData]);

  // Listen for runner fallback events
  useEffect(() => {
    const handleRunnerFallback = (event) => {
      const data = event.detail;
      if (data) {
        setRunnerFallbackWarning(data);
        // Auto-hide after 10 seconds
        setTimeout(() => {
          setRunnerFallbackWarning(null);
        }, 10000);
      }
    };
    window.addEventListener("mitto:runner_fallback", handleRunnerFallback);
    return () => {
      window.removeEventListener("mitto:runner_fallback", handleRunnerFallback);
    };
  }, []);

  // Listen for ACP start failed events
  useEffect(() => {
    const handleAcpStartFailed = (event) => {
      const data = event.detail;
      if (data) {
        setAcpStartFailedError(data);
        // Auto-hide after 10 seconds
        setTimeout(() => {
          setAcpStartFailedError(null);
        }, 10000);
      }
    };
    window.addEventListener("mitto:acp_start_failed", handleAcpStartFailed);
    return () => {
      window.removeEventListener(
        "mitto:acp_start_failed",
        handleAcpStartFailed,
      );
    };
  }, []);

  // Ref to track hook-failed toast hide timer
  const hookFailedTimerRef = useRef(null);

  // Listen for hook failed events
  useEffect(() => {
    const handleHookFailed = (event) => {
      const data = event.detail;
      if (data) {
        setHookFailedError(data);
        // Clear any existing timer
        if (hookFailedTimerRef.current) {
          clearTimeout(hookFailedTimerRef.current);
        }
        // Auto-hide after 10 seconds
        hookFailedTimerRef.current = setTimeout(() => {
          setHookFailedError(null);
          hookFailedTimerRef.current = null;
        }, 10000);
      }
    };
    window.addEventListener("mitto:hook_failed", handleHookFailed);
    return () => {
      window.removeEventListener("mitto:hook_failed", handleHookFailed);
    };
  }, []);

  // Cleanup timers on unmount
  useEffect(() => {
    return () => {
      if (toastTimerRef.current) {
        clearTimeout(toastTimerRef.current);
      }
      if (periodicToastTimerRef.current) {
        clearTimeout(periodicToastTimerRef.current);
      }
      if (uiPromptToastTimerRef.current) {
        clearTimeout(uiPromptToastTimerRef.current);
      }
      if (hookFailedTimerRef.current) {
        clearTimeout(hookFailedTimerRef.current);
      }
    };
  }, []);

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
      } else if (groupingModeForNav === "workspace") {
        // workspace mode - group by working_dir|acp_server
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
      } else if (groupingModeForNav === "workspace") {
        const storedSession = storedSessions.find(
          (s) => s.session_id === session.session_id,
        );
        const workingDir =
          session.working_dir ||
          storedSession?.working_dir ||
          getGlobalWorkingDir(session.session_id) ||
          "";
        // Label is the basename (same as sidebar)
        return workingDir ? getBasename(workingDir) : "Unknown";
      }
      return "";
    },
    [groupingModeForNav, storedSessions],
  );

  // Sessions available for navigation based on active filter tab
  // Navigation via keyboard shortcuts and swipe gestures should only cycle within the active tab
  // In "visible_groups" cycling mode, also skip sessions in collapsed groups
  // Sessions are ordered to match the visual order in the sidebar:
  // - When grouped: groups sorted alphabetically, sessions within groups by created_at (newest first)
  // - When not grouped: sessions sorted by created_at (newest first)
  const navigableSessions = useMemo(() => {
    // First filter sessions based on the active filter tab
    let tabFilteredSessions;
    switch (filterTabForNav) {
      case FILTER_TAB.PERIODIC:
        tabFilteredSessions = allSessions.filter(
          (s) => !s.archived && s.periodic_enabled,
        );
        break;
      case FILTER_TAB.ARCHIVED:
        tabFilteredSessions = allSessions.filter((s) => s.archived);
        break;
      case FILTER_TAB.CONVERSATIONS:
      default:
        tabFilteredSessions = allSessions.filter(
          (s) => !s.archived && !s.periodic_enabled,
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

  // Badge click action settings (macOS only, default: enabled)
  const [badgeClickEnabled, setBadgeClickEnabled] = useState(true);

  // Input font family setting (web UI, default: "system")
  const [inputFontFamily, setInputFontFamily] = useState("system");

  // Check if running in the native macOS app
  const isMacApp = typeof window.mittoPickFolder === "function";

  // Fetch config on mount to get predefined prompts, UI theme, and check for workspaces
  useEffect(() => {
    authFetch(apiUrl("/api/config"))
      .then((res) => res.json())
      .then((config) => {
        // Load global prompts from top-level prompts
        if (config?.prompts) {
          setGlobalPrompts(config.prompts);
        }
        // Store ACP servers with their per-server prompts
        if (config?.acp_servers) {
          console.log("[config] ACP servers with prompts:", config.acp_servers);
          setAcpServersWithPrompts(config.acp_servers);
        }
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
        // Load badge click action setting (macOS only, default: enabled)
        // Only enable if running in native macOS app
        if (typeof window.mittoPickFolder === "function") {
          const badgeClickSetting =
            config?.ui?.mac?.badge_click_action?.enabled;
          // Default to enabled if not explicitly disabled
          setBadgeClickEnabled(badgeClickSetting !== false);
        } else {
          // Disable for non-macOS environments
          setBadgeClickEnabled(false);
        }
        // Load input font family setting (web UI)
        if (config?.ui?.web?.input_font_family) {
          setInputFontFamily(config.ui.web.input_font_family);
        }
        // Load conversation cycling mode setting (web UI, default: "all")
        if (config?.ui?.web?.conversation_cycling_mode) {
          setConversationCyclingMode(config.ui.web.conversation_cycling_mode);
        }
        // Load accordion mode setting for groups (web UI, default: false)
        setSingleExpandedGroupMode(
          config?.ui?.web?.single_expanded_group === true,
        );
        // Check if ACP servers or workspaces are configured - if not, force open settings
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
          setSettingsDialog({ isOpen: true, forceOpen: true });
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
        const res = await authFetch(
          apiUrl(
            `/api/workspace-prompts?dir=${encodeURIComponent(workingDir)}`,
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
    [workspacePromptsDir, workspacePromptsLastModified],
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

  // Set current workspace for file URL conversion (used in web browser mode)
  useEffect(() => {
    const workingDir = sessionInfo?.working_dir;
    if (workingDir) {
      // Find the workspace UUID from the workspaces list
      const workspace = workspaces.find((ws) => ws.working_dir === workingDir);
      const workspaceUUID = workspace?.uuid;
      setCurrentWorkspace(workingDir, workspaceUUID);
    }
  }, [sessionInfo?.working_dir, workspaces]);

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
      if (sessionInfo?.working_dir) {
        fetchWorkspacePrompts(sessionInfo.working_dir, true);
      }

      // Refresh global prompts
      const acpServer = sessionInfo?.acp_server;
      const url = acpServer
        ? apiUrl(`/api/config?acp_server=${encodeURIComponent(acpServer)}`)
        : apiUrl("/api/config");

      authFetch(url)
        .then((res) => res.json())
        .then((config) => {
          if (config?.prompts) {
            setGlobalPrompts(config.prompts);
          }
          if (config?.acp_servers) {
            setAcpServersWithPrompts(config.acp_servers);
          }
        })
        .catch((err) => {
          console.error("Failed to refresh prompts after file change:", err);
        });
    };

    window.addEventListener("mitto:prompts_changed", handlePromptsChanged);
    return () =>
      window.removeEventListener("mitto:prompts_changed", handlePromptsChanged);
  }, [
    sessionInfo?.working_dir,
    sessionInfo?.acp_server,
    fetchWorkspacePrompts,
  ]);

  // Refetch global prompts when ACP server changes
  // This ensures prompts with "acps" restrictions are filtered correctly per workspace
  useEffect(() => {
    const acpServer = sessionInfo?.acp_server;
    // Skip if ACP server hasn't changed or isn't set yet
    if (!acpServer || acpServer === globalPromptsACPServer) return;

    // Fetch global prompts filtered by ACP server
    authFetch(apiUrl(`/api/config?acp_server=${encodeURIComponent(acpServer)}`))
      .then((res) => res.json())
      .then((config) => {
        if (config?.prompts) {
          setGlobalPrompts(config.prompts);
          setGlobalPromptsACPServer(acpServer);
        }
      })
      .catch((err) => {
        console.error("Failed to fetch global prompts for ACP server:", err);
      });
  }, [sessionInfo?.acp_server, globalPromptsACPServer]);

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
      console.log("[Scroll] Skipping auto-scroll - just loaded older messages");
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
        // If session creation failed due to no workspace configured, open settings
        if (
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

    // Next Conversation - called from native swipe gesture (swipe left)
    window.mittoNextConversation = () => {
      navigateToNextSession();
    };

    // Previous Conversation - called from native swipe gesture (swipe right)
    window.mittoPrevConversation = () => {
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
    // to trigger WebSocket reconnection and sync any missed messages
    window.mittoAppDidBecomeActive = () => {
      console.log("[macOS] App became active, triggering reconnect and sync");
      forceReconnectActiveSession();
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
  ]);

  const handleNewSession = async (workspace = null) => {
    // If a specific workspace is provided, create session directly in that workspace
    if (workspace) {
      setShowSidebar(false);
      const result = await newSession({
        workingDir: workspace.working_dir,
        acpServer: workspace.acp_server,
      });
      // If session creation failed due to no workspace configured, open settings
      if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
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
      // If session creation failed due to no workspace configured, open settings
      if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
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
    const result = await newSession({
      workingDir: workspace.working_dir,
      acpServer: workspace.acp_server,
    });
    // If session creation failed due to no workspace configured, open settings (unless config is read-only)
    if (result?.errorCode === "no_workspace_configured" && !configReadonly) {
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

  const handleShowKeyboardShortcuts = () => {
    setKeyboardShortcutsDialog({ isOpen: true });
  };

  // Ref to track queue panel auto-close timer after adding
  const queuePanelAutoCloseTimerRef = useRef(null);

  // Queue dropdown handlers
  const handleToggleQueueDropdown = useCallback(() => {
    console.log(
      "[DEBUG] handleToggleQueueDropdown called, current showQueueDropdown:",
      showQueueDropdown,
    );
    // Cancel any auto-close timer when user manually toggles
    if (queuePanelAutoCloseTimerRef.current) {
      clearTimeout(queuePanelAutoCloseTimerRef.current);
      queuePanelAutoCloseTimerRef.current = null;
    }
    if (!showQueueDropdown) {
      // Opening - fetch latest queue messages
      fetchQueueMessages();
    }
    setShowQueueDropdown((prev) => {
      console.log("[DEBUG] setShowQueueDropdown: prev=", prev, "new=", !prev);
      return !prev;
    });
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

  // Ref to track queue toast hide timer
  const queueToastTimerRef = useRef(null);

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
          if (queueToastTimerRef.current) {
            clearTimeout(queueToastTimerRef.current);
          }
          setQueueToastVisible(true);
          queueToastTimerRef.current = setTimeout(() => {
            setQueueToastVisible(false);
            queueToastTimerRef.current = null;
          }, 2000);

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
    ],
  );

  // Auto-hide queue dropdown when certain events occur
  useEffect(() => {
    if (!showQueueDropdown) return;

    // Close when settings dialog opens
    if (settingsDialog.isOpen) {
      setShowQueueDropdown(false);
    }
  }, [showQueueDropdown, settingsDialog.isOpen]);

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

  // Properties panel handlers
  const handleTogglePropertiesPanel = useCallback(() => {
    setShowPropertiesPanel((prev) => !prev);
  }, []);

  const handleClosePropertiesPanel = useCallback(() => {
    setShowPropertiesPanel(false);
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

  // Handler for prompts dropdown open - refreshes both global and workspace prompts
  const handlePromptsOpen = useCallback(() => {
    // Refresh workspace prompts
    if (sessionInfo?.working_dir) {
      fetchWorkspacePrompts(sessionInfo.working_dir, false);
    }

    // Refresh global prompts (checks for new/modified prompt files)
    const acpServer = sessionInfo?.acp_server;
    const url = acpServer
      ? apiUrl(`/api/config?acp_server=${encodeURIComponent(acpServer)}`)
      : apiUrl("/api/config");

    authFetch(url)
      .then((res) => res.json())
      .then((config) => {
        if (config?.prompts) {
          setGlobalPrompts(config.prompts);
        }
        if (config?.acp_servers) {
          setAcpServersWithPrompts(config.acp_servers);
        }
      })
      .catch((err) => {
        console.error("Failed to refresh global prompts:", err);
      });
  }, [
    sessionInfo?.working_dir,
    sessionInfo?.acp_server,
    fetchWorkspacePrompts,
  ]);

  const handleSelectSession = (sessionId) => {
    switchSession(sessionId);
    setShowSidebar(false);
    setShowPropertiesPanel(false);
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
          console.error("Badge click failed:", data.error || "Unknown error");
        }
      } catch (err) {
        console.error("Badge click error:", err);
      }
    },
    [badgeClickEnabled],
  );

  // Open the properties panel for a session (used by pencil button in session list)
  const handleOpenSessionProperties = useCallback(
    (session) => {
      // Switch to the session if not already active
      if (session.session_id !== activeSessionId) {
        switchSession(session.session_id);
        setShowSidebar(false);
      }
      // Open the properties panel
      setShowPropertiesPanel(true);
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
  };

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
        workspaces=${workspaces}
        onSelect=${handleWorkspaceSelect}
        onCancel=${() => setWorkspaceDialog({ isOpen: false })}
      />

      <!-- Settings Dialog -->
      <${SettingsDialog}
        isOpen=${settingsDialog.isOpen}
        forceOpen=${settingsDialog.forceOpen}
        onClose=${() => setSettingsDialog({ isOpen: false, forceOpen: false })}
        WorkspaceBadge=${WorkspaceBadge}
        onSave=${async () => {
          // Refresh workspaces after saving
          refreshWorkspaces();
          // Reload config to update prompts and UI settings
          try {
            const res = await authFetch(apiUrl("/api/config"));
            if (res.ok) {
              const config = await res.json();
              // Reload global prompts (use empty array if not present)
              setGlobalPrompts(config?.prompts || []);
              // Reload ACP servers with their per-server prompts
              setAcpServersWithPrompts(config?.acp_servers || []);
              // Reload UI settings
              setConfirmDeleteSession(
                config?.ui?.confirmations?.delete_session !== false,
              );
              // Reload badge click action settings (macOS only)
              if (typeof window.mittoPickFolder === "function") {
                setBadgeClickEnabled(
                  config?.ui?.mac?.badge_click_action?.enabled !== false,
                );
              }
              // Reload input font family setting
              setInputFontFamily(
                config?.ui?.web?.input_font_family || "system",
              );
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

      <!-- Keyboard Shortcuts Dialog -->
      <${KeyboardShortcutsDialog}
        isOpen=${keyboardShortcutsDialog.isOpen}
        onClose=${() => setKeyboardShortcutsDialog({ isOpen: false })}
      />

      <!-- Background completion toast -->
      ${toastData &&
      html`
        <div
          class="fixed top-4 left-1/2 -translate-x-1/2 z-50 ${toastVisible
            ? "toast-enter"
            : "toast-exit"}"
          onClick=${() => {
            switchSession(toastData.sessionId);
            setToastVisible(false);
            setTimeout(() => setToastData(null), 200);
          }}
        >
          <div
            class="flex items-center gap-2 px-4 py-2 bg-green-600 text-white rounded-full shadow-lg cursor-pointer hover:bg-green-500 transition-colors"
          >
            <span class="text-lg">✓</span>
            <span class="text-sm font-medium truncate max-w-[200px]"
              >${toastData.sessionName}</span
            >
            <span class="text-xs opacity-75">finished</span>
          </div>
        </div>
      `}

      <!-- Periodic started toast -->
      ${periodicToastData &&
      html`
        <div
          class="fixed top-4 left-1/2 -translate-x-1/2 z-50 ${periodicToastVisible
            ? "toast-enter"
            : "toast-exit"}"
          onClick=${() => {
            switchSession(periodicToastData.sessionId);
            setPeriodicToastVisible(false);
            setTimeout(() => setPeriodicToastData(null), 200);
          }}
        >
          <div
            class="flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-full shadow-lg cursor-pointer hover:bg-indigo-500 transition-colors"
          >
            <span class="text-lg">🔄</span>
            <span class="text-sm font-medium truncate max-w-[200px]"
              >${periodicToastData.sessionName}</span
            >
            <span class="text-xs opacity-75">periodic run started</span>
          </div>
        </div>
      `}

      <!-- UI prompt in background session toast -->
      ${uiPromptToastData &&
      html`
        <div
          class="fixed top-4 left-1/2 -translate-x-1/2 z-50 ${uiPromptToastVisible
            ? "toast-enter"
            : "toast-exit"}"
          onClick=${() => {
            switchSession(uiPromptToastData.sessionId);
            setUIPromptToastVisible(false);
            setTimeout(() => setUIPromptToastData(null), 200);
          }}
        >
          <div
            class="flex items-center gap-2 px-4 py-2 bg-amber-600 text-white rounded-full shadow-lg cursor-pointer hover:bg-amber-500 transition-colors"
          >
            <span class="text-lg">❓</span>
            <span class="text-sm font-medium">Question in</span>
            <span class="text-sm font-medium truncate max-w-[200px]"
              >${uiPromptToastData.sessionName}</span
            >
          </div>
        </div>
      `}

      <!-- Queue added toast -->
      ${queueToastVisible &&
      html`
        <div class="fixed top-4 left-1/2 -translate-x-1/2 z-50 toast-enter">
          <div
            class="queue-toast flex items-center gap-2 px-4 py-2 bg-blue-600 rounded-full shadow-lg"
          >
            <span class="text-lg">📋</span>
            <span class="text-sm font-medium">Message queued</span>
          </div>
        </div>
      `}

      <!-- Runner fallback warning toast -->
      ${runnerFallbackWarning &&
      html`
        <div class="fixed top-4 left-1/2 -translate-x-1/2 z-50 toast-enter">
          <div
            class="flex flex-col gap-1 px-4 py-3 bg-yellow-600 text-white rounded-lg shadow-lg max-w-md"
          >
            <div class="flex items-center gap-2">
              <span class="text-lg">⚠️</span>
              <span class="text-sm font-medium">Runner Not Supported</span>
              <button
                onClick=${() => setRunnerFallbackWarning(null)}
                class="ml-auto text-white/80 hover:text-white"
                title="Dismiss"
              >
                ✕
              </button>
            </div>
            <div class="text-xs opacity-90 ml-7">
              <div>
                Requested:
                <strong>${runnerFallbackWarning.requested_type}</strong>
              </div>
              <div>
                Using:
                <strong>${runnerFallbackWarning.fallback_type}</strong> (no
                restrictions)
              </div>
              <div class="mt-1 text-white/70">
                ${runnerFallbackWarning.reason}
              </div>
            </div>
          </div>
        </div>
      `}

      <!-- ACP start failed toast -->
      ${acpStartFailedError &&
      html`
        <div class="fixed top-4 left-1/2 -translate-x-1/2 z-50 toast-enter">
          <div
            class="flex flex-col gap-1 px-4 py-3 bg-red-600 text-white rounded-lg shadow-lg max-w-md"
          >
            <div class="flex items-center gap-2">
              <span class="text-lg">❌</span>
              <span class="text-sm font-medium"
                >ACP Server Failed to Start</span
              >
              <button
                onClick=${() => setAcpStartFailedError(null)}
                class="ml-auto text-white/80 hover:text-white"
                title="Dismiss"
              >
                ✕
              </button>
            </div>
            <div class="text-xs opacity-90 ml-7">
              <div class="text-white/70 break-words">
                ${acpStartFailedError.error}
              </div>
            </div>
          </div>
        </div>
      `}

      <!-- Hook failed toast -->
      ${hookFailedError &&
      html`
        <div class="fixed top-4 left-1/2 -translate-x-1/2 z-50 toast-enter">
          <div
            class="flex flex-col gap-1 px-4 py-3 bg-orange-600 text-white rounded-lg shadow-lg max-w-md"
          >
            <div class="flex items-center gap-2">
              <span class="text-lg">⚠️</span>
              <span class="text-sm font-medium">Hook Failed</span>
              <button
                onClick=${() => setHookFailedError(null)}
                class="ml-auto text-white/80 hover:text-white"
                title="Dismiss"
              >
                ✕
              </button>
            </div>
            <div class="text-xs opacity-90 ml-7">
              <div>
                Hook: <strong>${hookFailedError.name || "up"}</strong>
                ${hookFailedError.exit_code !== undefined &&
                ` (exit code ${hookFailedError.exit_code})`}
              </div>
              <div class="mt-1 text-white/70 break-words">
                ${hookFailedError.error}
              </div>
            </div>
          </div>
        </div>
      `}

      <!-- Sidebar (hidden on mobile by default) -->
      <div
        class="hidden md:block w-80 bg-mitto-sidebar border-r border-slate-700 flex-shrink-0"
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
          onShowKeyboardShortcuts=${handleShowKeyboardShortcuts}
          configReadonly=${configReadonly}
          rcFilePath=${rcFilePath}
          badgeClickEnabled=${badgeClickEnabled}
          onBadgeClick=${handleBadgeClick}
          queueLength=${queueLength}
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
              onShowKeyboardShortcuts=${handleShowKeyboardShortcuts}
              configReadonly=${configReadonly}
              rcFilePath=${rcFilePath}
              badgeClickEnabled=${badgeClickEnabled}
              onBadgeClick=${handleBadgeClick}
              queueLength=${queueLength}
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
            onClick=${activeSessionId ? handleTogglePropertiesPanel : undefined}
            title=${activeSessionId ? "Click to view properties" : ""}
          >
            ${activeSessionId
              ? sessionInfo?.name || "New conversation"
              : "No Active Session"}
          </h1>
          <div class="ml-auto flex items-center gap-2">
            ${isStreaming &&
            html`
              <span
                class="w-2 h-2 bg-blue-400 rounded-full animate-pulse"
                title="Streaming"
              ></span>
            `}
            <span
              class="w-2 h-2 rounded-full ${connected
                ? "bg-green-400"
                : "bg-red-400"}"
              title="${connected ? "Connected" : "Disconnected"}"
            ></span>
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
                    <div class="text-6xl mb-6">💬</div>
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
                .map(
                  (msg, i, arr) => html`
                    <${Message}
                      key=${msg.timestamp + "-" + (arr.length - 1 - i)}
                      message=${msg}
                      isLast=${i === 0}
                      isStreaming=${isStreaming}
                    />
                  `,
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
            activeUIPrompt=${activeUIPrompt}
            onUIPromptAnswer=${(requestId, optionId, label) =>
              sendUIPromptAnswer(activeSessionId, requestId, optionId, label)}
            workingDir=${sessionInfo?.working_dir || ""}
          />
        </div>
      </div>

      <!-- Conversation Properties Panel (fixed overlay on left) -->
      <${ConversationPropertiesPanel}
        isOpen=${showPropertiesPanel}
        onClose=${handleClosePropertiesPanel}
        sessionId=${activeSessionId}
        sessionInfo=${sessionInfo}
        onRename=${renameSession}
        isStreaming=${isStreaming}
        configOptions=${configOptions}
        onSetConfigOption=${setConfigOption}
      />
    </div>
  `;
}

// =============================================================================
// Mount Application
// =============================================================================

render(html`<${App} />`, document.getElementById("app"));
