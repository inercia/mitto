// Mitto Web Interface - Preact Application
const {
  h,
  render,
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
  getLastSeenSeq,
  setLastSeenSeq,
  getLastActiveSessionId,
  setLastActiveSessionId,
  playAgentCompletedSound,
  secureFetch,
  initCSRF,
  apiUrl,
  authFetch,
  fixViewerURLIfNeeded,
} from "./utils/index.js";

// Import hooks
import { useWebSocket, useSwipeNavigation, useSwipeToDelete } from "./hooks/index.js";

// Import components
import { Message } from "./components/Message.js";
import { ChatInput } from "./components/ChatInput.js";
import { SettingsDialog } from "./components/SettingsDialog.js";
import { QueueDropdown } from "./components/QueueDropdown.js";
import {
  SpinnerIcon,
  CloseIcon,
  SettingsIcon,
  PlusIcon,
  ChevronUpIcon,
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
} from "./components/Icons.js";

// Import constants
import { KEYBOARD_SHORTCUTS } from "./constants.js";

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
      // Web browser - open in viewer page with syntax highlighting
      const viewerUrl = convertHTTPFileURLToViewer(href);
      console.log("[Mitto] Converted to viewer URL:", viewerUrl);
      if (viewerUrl) {
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
}) {
  if (!path) return null;

  const { abbreviation, color, displayName: wsDisplayName } = getWorkspaceVisualInfo(
    path,
    customColor,
    customCode,
    customName,
  );
  // Display ACP server name if available, otherwise fall back to workspace display name
  const displayName = acpServer || wsDisplayName;

  const handleClick = (e) => {
    if (!clickable) return;
    e.stopPropagation(); // Prevent triggering session selection
    if (onBadgeClick) {
      onBadgeClick(path);
    }
  };

  const cursorClass = clickable ? "cursor-pointer workspace-pill-clickable" : "";

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
      <span class="font-bold">${abbreviation}</span>
      <span class="truncate max-w-[80px]">${displayName}</span>
    </div>
  `;
}

// =============================================================================
// Session Properties Dialog Component
// =============================================================================

function SessionPropertiesDialog({
  isOpen,
  session,
  onSave,
  onCancel,
  workspaces = [],
}) {
  const [name, setName] = useState("");
  const inputRef = useRef(null);

  const sessionName = session?.name || session?.description || "Untitled";
  const workingDir = session?.working_dir || session?.info?.working_dir || "";
  const acpServer = session?.acp_server || session?.info?.acp_server || "";
  // Look up workspace for customization
  const workspace = workspaces.find((ws) => ws.working_dir === workingDir);

  useEffect(() => {
    if (isOpen) {
      setName(sessionName);
      setTimeout(() => inputRef.current?.focus(), 100);
    }
  }, [isOpen, sessionName]);

  if (!isOpen) return null;

  const handleSubmit = (e) => {
    e.preventDefault();
    onSave(name.trim() || "Untitled");
  };

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${onCancel}
    >
      <div
        class="bg-mitto-sidebar rounded-xl p-6 w-96 shadow-2xl"
        onClick=${(e) => e.stopPropagation()}
      >
        <h3 class="text-lg font-semibold mb-4">Session Properties</h3>
        <form onSubmit=${handleSubmit}>
          <!-- Session Name (editable) -->
          <div class="mb-4">
            <label class="block text-sm text-gray-400 mb-1">Name</label>
            <input
              ref=${inputRef}
              type="text"
              value=${name}
              onInput=${(e) => setName(e.target.value)}
              class="w-full bg-slate-800 text-white rounded-lg px-4 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500"
              placeholder="Session name"
            />
          </div>

          <!-- Workspace Info (read-only) -->
          ${(workingDir || acpServer) &&
          html`
            <div
              class="mb-4 p-3 bg-slate-800/50 rounded-lg border border-slate-700"
            >
              <div class="text-xs text-gray-500 uppercase tracking-wide mb-3">
                Workspace
              </div>
              ${workingDir &&
              html`
                <${WorkspaceBadge}
                  path=${workingDir}
                  customColor=${workspace?.color}
                  customCode=${workspace?.code}
                  customName=${workspace?.name}
                  size="md"
                  showPath=${true}
                  className="mb-3"
                />
              `}
              ${acpServer &&
              html`
                <div
                  class="flex items-center gap-2 ${workingDir
                    ? "ml-13 pl-0.5"
                    : ""}"
                >
                  <${ServerIcon}
                    className="w-4 h-4 text-gray-400 flex-shrink-0"
                  />
                  <span
                    class="px-2 py-1 bg-blue-500/20 text-blue-400 rounded text-xs"
                  >
                    ${acpServer}
                  </span>
                </div>
              `}
            </div>
          `}

          <div class="flex justify-end gap-2">
            <button
              type="button"
              onClick=${onCancel}
              class="px-4 py-2 rounded-lg hover:bg-slate-700 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              class="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg transition-colors"
            >
              Save
            </button>
          </div>
        </form>
      </div>
    </div>
  `;
}

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
                if (num >= 1 && num <= Math.min(WORKSPACE_FILTER_THRESHOLD, filteredWorkspaces.length)) {
                  e.preventDefault();
                  const workspace = filteredWorkspaces[num - 1];
                  if (workspace) {
                    onSelect(workspace);
                  }
                }
              }}
              placeholder="Filter workspaces..."
              autoFocus
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
                      : html`
                          <div class="w-8 h-8 flex-shrink-0"></div>
                        `}
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

  // Close menu when clicking outside
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
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [onClose]);

  // Adjust position to keep menu within viewport
  const [adjustedPos, setAdjustedPos] = useState({ x, y });
  useEffect(() => {
    if (menuRef.current) {
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
      setAdjustedPos({ x: newX, y: newY });
    }
  }, [x, y]);

  return html`
    <div
      ref=${menuRef}
      class="fixed z-50 bg-slate-800 border border-slate-600 rounded-lg shadow-xl py-1 min-w-[140px]"
      style="left: ${adjustedPos.x}px; top: ${adjustedPos.y}px;"
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

function SessionItem({
  session,
  isActive,
  onSelect,
  onRename,
  onDelete,
  onPin,
  workspaceColor = null,
  workspaceCode = null,
  workspaceName = null,
  badgeClickEnabled = false,
  onBadgeClick,
}) {
  const [showActions, setShowActions] = useState(false);
  const [contextMenu, setContextMenu] = useState(null);

  // Check if session is pinned
  const isPinned = session.pinned || false;

  // Build tooltip with session metadata
  const buildTooltip = () => {
    const parts = [];

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

    // Runner type
    if (session.runner_type) {
      const runnerInfo = session.runner_restricted
        ? `${session.runner_type} (restricted)`
        : `${session.runner_type} (unrestricted)`;
      parts.push(`Runner: ${runnerInfo}`);
    }

    return parts.join('\n');
  };

  // Swipe-to-delete hook - disabled when pinned
  const { swipeOffset, isSwiping, isSwipingRef, isRevealed, containerProps, reset, triggerDelete } =
    useSwipeToDelete({
      onDelete: () => onDelete(session),
      threshold: 0.5,
      revealWidth: 80,
      disabled: isPinned,
    });

  const handleRename = (e) => {
    if (e) e.stopPropagation();
    onRename(session);
  };

  const handleDelete = (e) => {
    if (e) e.stopPropagation();
    if (isPinned) return; // Prevent delete if pinned
    onDelete(session);
  };

  const handlePin = (e) => {
    if (e) e.stopPropagation();
    onPin(session, !isPinned);
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
  const isActiveSession = session.isActive || session.status === "active";
  const isStreaming = session.isStreaming || false;
  // Get working_dir from session, or fall back to global map
  const workingDir =
    session.working_dir || getGlobalWorkingDir(session.session_id) || "";
  // Get acp_server from session
  const acpServer = session.acp_server || "";

  const contextMenuItems = [
    {
      label: isPinned ? "Unpin" : "Pin",
      icon: isPinned ? html`<${PinFilledIcon} />` : html`<${PinIcon} />`,
      onClick: () => handlePin(),
    },
    {
      label: "Rename",
      icon: html`<${EditIcon} />`,
      onClick: () => handleRename(),
    },
    {
      label: "Delete",
      icon: html`<${TrashIcon} />`,
      onClick: () => handleDelete(),
      danger: true,
      disabled: isPinned,
    },
  ];

  // Calculate visual feedback intensity based on swipe progress
  const absOffset = Math.abs(swipeOffset);
  const deleteProgress = Math.min(absOffset / 160, 1); // Max at 160px

  return html`
    <div
      class="session-item-container relative overflow-hidden border-b border-slate-700"
      ...${containerProps}
    >
      <!-- Delete background (revealed when swiping left) -->
      <div
        class="absolute inset-0 bg-red-600 flex items-center justify-end pr-6 transition-opacity"
        style="opacity: ${isRevealed || absOffset > 20 ? 1 : 0}"
      >
        <button
          onClick=${(e) => {
            e.preventDefault();
            e.stopPropagation();
            triggerDelete();
          }}
          class="p-3 rounded-full bg-red-700 hover:bg-red-800 transition-colors"
          title="Delete"
        >
          <${TrashIcon} className="w-5 h-5 text-white" />
        </button>
      </div>
      <!-- Swipeable content -->
      <div
        onClick=${handleClick}
        onContextMenu=${handleContextMenu}
        onMouseEnter=${() => setShowActions(true)}
        onMouseLeave=${() => setShowActions(false)}
        class="p-3 cursor-pointer hover:bg-slate-700/50 relative bg-mitto-sidebar ${isActive
          ? "bg-blue-900/30 border-l-2 border-l-blue-500"
          : ""} ${isSwiping ? "" : "transition-transform duration-200"}"
        style="transform: translateX(${swipeOffset}px)"
        title=${buildTooltip()}
      >
      ${contextMenu &&
      html`
        <${ContextMenu}
          x=${contextMenu.x}
          y=${contextMenu.y}
          items=${contextMenuItems}
          onClose=${closeContextMenu}
        />
      `}
      <!-- Top row: status indicator, title, and action buttons -->
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
          <div class="text-xs text-gray-500 mt-1">
            ${new Date(session.created_at).toLocaleDateString()}
            ${new Date(session.created_at).toLocaleTimeString([], {
              hour: "2-digit",
              minute: "2-digit",
            })}
          </div>
        </div>
        <div
          class="flex items-center gap-1 ${showActions || isPinned
            ? "opacity-100"
            : "opacity-0"} transition-opacity flex-shrink-0"
        >
          ${isPinned && !showActions
            ? html`
                <span class="text-amber-400" title="Pinned">
                  <${PinFilledIcon} className="w-4 h-4" />
                </span>
              `
            : html`
                <button
                  onClick=${handlePin}
                  class="p-1.5 bg-slate-700 hover:bg-slate-600 rounded transition-colors ${isPinned
                    ? "text-amber-400"
                    : "text-gray-300 hover:text-white"}"
                  title="${isPinned ? "Unpin" : "Pin"}"
                >
                  ${isPinned
                    ? html`<${PinFilledIcon} className="w-4 h-4" />`
                    : html`<${PinIcon} className="w-4 h-4" />`}
                </button>
                <button
                  onClick=${handleRename}
                  class="p-1.5 bg-slate-700 hover:bg-slate-600 rounded transition-colors text-gray-300 hover:text-white"
                  title="Rename"
                >
                  <${EditIcon} className="w-4 h-4" />
                </button>
                <button
                  onClick=${handleDelete}
                  class="p-1.5 bg-slate-700 rounded transition-colors ${isPinned
                    ? "text-gray-500 cursor-not-allowed"
                    : "hover:bg-red-600 text-gray-300 hover:text-white"}"
                  title="${isPinned ? "Unpin to delete" : "Delete"}"
                  disabled=${isPinned}
                >
                  <${TrashIcon} className="w-4 h-4" />
                </button>
              `}
        </div>
      </div>
      <!-- Bottom row: message count, saved/stored badge, and workspace pill -->
      <div class="flex items-center justify-between mt-2">
        <div class="flex items-center gap-2">
          ${session.messageCount !== undefined
            ? html`
                <span class="text-xs text-gray-500"
                  >${session.messageCount} msgs</span
                >
              `
            : session.event_count !== undefined
              ? html`
                  <span class="text-xs text-gray-500"
                    >${session.event_count} events</span
                  >
                `
              : null}
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
        ${workingDir &&
        html`
          <${WorkspacePill}
            path=${workingDir}
            customColor=${workspaceColor}
            customCode=${workspaceCode}
            customName=${workspaceName}
            acpServer=${acpServer}
            clickable=${badgeClickEnabled}
            onBadgeClick=${onBadgeClick}
          />
        `}
      </div>
      </div>
    </div>
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
  onPin,
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
}) {
  // Combine active and stored sessions using shared helper function
  // Note: Not using useMemo to ensure working_dir is always up-to-date
  const allSessions = computeAllSessions(activeSessions, storedSessions);

  const isLight = theme === "light";
  const isLargeFont = fontSize === "large";

  return html`
    <div class="h-full flex flex-col">
      <div
        class="p-4 border-b border-slate-700 flex items-center justify-between"
      >
        <h2 class="font-semibold text-lg">Conversations</h2>
        <div class="flex items-center gap-2">
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
      <div class="flex-1 overflow-y-auto scrollbar-hide">
        ${allSessions.length === 0 &&
        html`
          <div class="p-4 text-gray-500 text-sm text-center">
            No conversations yet
          </div>
        `}
        ${allSessions.map((session) => {
          // Ensure working_dir is available by looking up in storedSessions or global map
          const storedSession = storedSessions.find(
            (s) => s.session_id === session.session_id,
          );
          const workingDir =
            session.working_dir ||
            storedSession?.working_dir ||
            getGlobalWorkingDir(session.session_id) ||
            "";
          const finalSession = workingDir
            ? { ...session, working_dir: workingDir }
            : session;
          // Look up workspace for customization
          const workspace = workspaces.find(
            (ws) => ws.working_dir === workingDir,
          );
          return html`
            <${SessionItem}
              key=${session.session_id}
              session=${finalSession}
              isActive=${activeSessionId === session.session_id}
              onSelect=${onSelect}
              onRename=${onRename}
              onDelete=${onDelete}
              onPin=${onPin}
              workspaceColor=${workspace?.color || null}
              workspaceCode=${workspace?.code || null}
              workspaceName=${workspace?.name || null}
              badgeClickEnabled=${badgeClickEnabled}
              onBadgeClick=${onBadgeClick}
            />
          `;
        })}
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
    removeSession,
    isStreaming,
    hasMoreMessages,
    actionButtons,
    sessionInfo,
    activeSessionId,
    activeSessions,
    storedSessions,
    fetchStoredSessions,
    backgroundCompletion,
    clearBackgroundCompletion,
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
  } = useWebSocket();

  const [showSidebar, setShowSidebar] = useState(false);
  const [showQueueDropdown, setShowQueueDropdown] = useState(false);
  const [isDeletingQueueMessage, setIsDeletingQueueMessage] = useState(false);
  const [isMovingQueueMessage, setIsMovingQueueMessage] = useState(false);
  const [isAddingToQueue, setIsAddingToQueue] = useState(false);
  const [queueToastVisible, setQueueToastVisible] = useState(false);
  const [queueBadgePulse, setQueueBadgePulse] = useState(false);
  const [renameDialog, setRenameDialog] = useState({
    isOpen: false,
    session: null,
  });
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
  const [configReadonly, setConfigReadonly] = useState(false); // True when --config flag was used or using RC file
  const [rcFilePath, setRcFilePath] = useState(null); // Path to RC file when config is read-only due to RC file
  const [swipeDirection, setSwipeDirection] = useState(null); // 'left' or 'right' for animation
  const [swipeArrow, setSwipeArrow] = useState(null); // 'left' or 'right' for arrow indicator
  const [toastVisible, setToastVisible] = useState(false);
  const [toastData, setToastData] = useState(null); // { sessionId, sessionName }
  const [runnerFallbackWarning, setRunnerFallbackWarning] = useState(null); // { requestedType, fallbackType, reason }
  const [loadingMore, setLoadingMore] = useState(false);
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

  // Compute all sessions for navigation using shared helper function
  const allSessions = useMemo(
    () => computeAllSessions(activeSessions, storedSessions),
    [activeSessions, storedSessions],
  );

  // Compute merged prompts: workspace prompts (highest priority) + global prompts + server-specific prompts
  // Workspace prompts override global/server prompts with the same name
  // Prompts are filtered by the current ACP server using the "acps" field
  const predefinedPrompts = useMemo(() => {
    const currentAcpServer = sessionInfo?.acp_server?.toLowerCase() || "";

    // Helper to check if a prompt is allowed for the current ACP server
    // If acps is empty, the prompt is allowed for all servers
    // Otherwise, check if the current server is in the comma-separated list
    const isAllowedForACP = (prompt) => {
      if (!prompt.acps || prompt.acps.trim() === "") {
        return true; // No restriction, allowed for all
      }
      if (!currentAcpServer) {
        return true; // No ACP server selected, show all prompts
      }
      // Parse comma-separated list and check for match (case-insensitive)
      const allowedServers = prompt.acps.split(",").map((s) => s.trim().toLowerCase());
      return allowedServers.includes(currentAcpServer);
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

  // Initialize CSRF protection on mount
  // This pre-fetches a CSRF token so subsequent state-changing requests are protected
  useEffect(() => {
    initCSRF();
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

  // Cleanup timer on unmount
  useEffect(() => {
    return () => {
      if (toastTimerRef.current) {
        clearTimeout(toastTimerRef.current);
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
  const handleLoadMore = useCallback(async () => {
    if (loadingMore || !activeSessionId || !hasMoreMessages) return;

    // Remember scroll position to maintain it after loading
    const container = messagesContainerRef.current;
    const scrollHeightBefore = container?.scrollHeight || 0;

    setLoadingMore(true);
    await loadMoreMessages(activeSessionId);
    setLoadingMore(false);

    // Restore scroll position (keep user at same visual position)
    if (container) {
      const scrollHeightAfter = container.scrollHeight;
      container.scrollTop = scrollHeightAfter - scrollHeightBefore;
    }
  }, [loadingMore, activeSessionId, hasMoreMessages, loadMoreMessages]);

  // Navigate to previous/next session with animation direction (wraps around for swipe gestures)
  const navigateToPreviousSession = useCallback(() => {
    if (allSessions.length <= 1) return;
    const currentIndex = allSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    if (currentIndex === -1) return;
    const prevIndex =
      currentIndex === 0 ? allSessions.length - 1 : currentIndex - 1;
    setSwipeDirection("right"); // Content slides in from left
    setSwipeArrow("right"); // Show right arrow (user swiped right)
    switchSession(allSessions[prevIndex].session_id);
  }, [allSessions, activeSessionId, switchSession]);

  const navigateToNextSession = useCallback(() => {
    if (allSessions.length <= 1) return;
    const currentIndex = allSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    if (currentIndex === -1) return;
    const nextIndex =
      currentIndex === allSessions.length - 1 ? 0 : currentIndex + 1;
    setSwipeDirection("left"); // Content slides in from right
    setSwipeArrow("left"); // Show left arrow (user swiped left)
    switchSession(allSessions[nextIndex].session_id);
  }, [allSessions, activeSessionId, switchSession]);

  // Navigate to session above in the list (no wrap-around, for keyboard shortcuts)
  // Note: No swipe animation - only swipe gestures should trigger horizontal scroll effect
  const navigateToSessionAbove = useCallback(() => {
    if (allSessions.length <= 1) return;
    const currentIndex = allSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    if (currentIndex === -1 || currentIndex === 0) return; // Already at top or not found
    switchSession(allSessions[currentIndex - 1].session_id);
  }, [allSessions, activeSessionId, switchSession]);

  // Navigate to session below in the list (no wrap-around, for keyboard shortcuts)
  // Note: No swipe animation - only swipe gestures should trigger horizontal scroll effect
  const navigateToSessionBelow = useCallback(() => {
    if (allSessions.length <= 1) return;
    const currentIndex = allSessions.findIndex(
      (s) => s.session_id === activeSessionId,
    );
    if (currentIndex === -1 || currentIndex === allSessions.length - 1) return; // Already at bottom or not found
    switchSession(allSessions[currentIndex + 1].session_id);
  }, [allSessions, activeSessionId, switchSession]);

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
  const navigateToSessionByIndex = useCallback(
    (index) => {
      if (index >= 0 && index < allSessions.length) {
        const targetSession = allSessions[index];
        if (targetSession.session_id !== activeSessionId) {
          switchSession(targetSession.session_id);
        }
      }
    },
    [allSessions, activeSessionId, switchSession],
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
        console.log("[config] ui.mac.notifications:", config?.ui?.mac?.notifications);
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
        // Check if ACP servers or workspaces are configured - if not, force open settings
        // Skip this if config is read-only (user manages config via file)
        const noAcpServers =
          !config?.acp_servers || config.acp_servers.length === 0;
        const noWorkspaces =
          !config?.workspaces || config.workspaces.length === 0;
        if ((noAcpServers || noWorkspaces) && !config?.config_readonly) {
          setSettingsDialog({ isOpen: true, forceOpen: true });
        }
      })
      .catch((err) => console.error("Failed to fetch config:", err));
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

  // Threshold for considering user "at bottom" (in pixels)
  const SCROLL_THRESHOLD = 100;

  // Check if the user is at the bottom of the messages container
  const checkIfAtBottom = useCallback(() => {
    const container = messagesContainerRef.current;
    if (!container) return true;
    const { scrollTop, scrollHeight, clientHeight } = container;
    return scrollHeight - scrollTop - clientHeight <= SCROLL_THRESHOLD;
  }, []);

  // Scroll to bottom handler
  const scrollToBottom = useCallback((smooth = true) => {
    if (messagesEndRef.current) {
      messagesEndRef.current.scrollIntoView({
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

    const handleScroll = () => {
      const atBottom = checkIfAtBottom();
      setIsUserAtBottom(atBottom);
      // Clear new messages indicator when user scrolls to bottom
      if (atBottom) {
        setHasNewMessages(false);
      }
    };

    container.addEventListener("scroll", handleScroll, { passive: true });
    return () => container.removeEventListener("scroll", handleScroll);
  }, [checkIfAtBottom]);

  // Track the active session to detect when we switch sessions
  const prevActiveSessionIdRef = useRef(activeSessionId);
  // Track if we're still in the initial load phase after a session switch
  const sessionJustSwitchedRef = useRef(false);

  // Position at bottom synchronously BEFORE paint when switching sessions
  // This prevents any visible "jump" - the content appears already at the bottom
  useLayoutEffect(() => {
    const currentLength = messages.length;
    const container = messagesContainerRef.current;

    // Helper to scroll to bottom instantly (bypassing CSS scroll-behavior: smooth)
    const scrollToBottomInstant = () => {
      if (!container) return;
      // Temporarily disable smooth scrolling to make scroll instant
      const originalBehavior = container.style.scrollBehavior;
      container.style.scrollBehavior = "auto";
      container.scrollTop = container.scrollHeight;
      // Restore original behavior after the scroll completes
      container.style.scrollBehavior = originalBehavior;
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
  ]);

  const handleNewSession = async () => {
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
    console.log("[DEBUG] handleToggleQueueDropdown called, current showQueueDropdown:", showQueueDropdown);
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

  // Handle adding current draft to queue
  const handleAddToQueue = useCallback(async () => {
    if (!currentDraft?.trim() || isAddingToQueue) return;

    setIsAddingToQueue(true);
    try {
      const result = await addToQueue(currentDraft);
      if (result.success) {
        // Clear the draft after successful addition
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
      }
    } finally {
      setIsAddingToQueue(false);
    }
  }, [currentDraft, isAddingToQueue, addToQueue, updateDraft, activeSessionId, fetchQueueMessages]);

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
  }, [sessionInfo?.working_dir, sessionInfo?.acp_server, fetchWorkspacePrompts]);

  const handleSelectSession = (sessionId) => {
    switchSession(sessionId);
    setShowSidebar(false);
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

  const handleRenameSession = (session) => {
    setRenameDialog({ isOpen: true, session });
  };

  const handleSaveRename = (newName) => {
    const session = renameDialog.session;
    if (!session) return;

    // Rename via WebSocket - this persists to storage and broadcasts to all clients
    renameSession(session.session_id, newName);

    setRenameDialog({ isOpen: false, session: null });
  };

  const handleDeleteSession = async (session) => {
    // If confirmation is disabled, delete immediately
    if (!confirmDeleteSession) {
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

    // removeSession handles: closing WebSocket, updating local state,
    // switching to another session (or creating new if none left), and calling DELETE API
    await removeSession(session.session_id);

    // Refresh the stored sessions list
    fetchStoredSessions();
  };

  const handlePinSession = async (session, pinned) => {
    await pinSession(session.session_id, pinned);
  };

  return html`
    <div class="h-screen-safe flex">
      <!-- Session Properties Dialog -->
      <${SessionPropertiesDialog}
        isOpen=${renameDialog.isOpen}
        session=${renameDialog.session}
        onSave=${handleSaveRename}
        onCancel=${() => setRenameDialog({ isOpen: false, session: null })}
        workspaces=${workspaces}
      />

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
                Requested: <strong>${runnerFallbackWarning.requested_type}</strong>
              </div>
              <div>
                Using: <strong>${runnerFallbackWarning.fallback_type}</strong> (no restrictions)
              </div>
              <div class="mt-1 text-white/70">
                ${runnerFallbackWarning.reason}
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
          onRename=${handleRenameSession}
          onDelete=${handleDeleteSession}
          onPin=${handlePinSession}
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
              onRename=${handleRenameSession}
              onDelete=${handleDeleteSession}
              onPin=${handlePinSession}
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
            class="font-bold text-xl truncate max-w-[300px] sm:max-w-[400px] ${!activeSessionId
              ? "text-gray-500"
              : ""}"
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

        <!-- Messages -->
        <div
          ref=${messagesContainerRef}
          class="flex-1 overflow-y-auto scroll-smooth scrollbar-hide p-4 relative"
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
            class="max-w-2xl mx-auto ${swipeDirection
              ? `swipe-slide-${swipeDirection}`
              : ""}"
          >
            ${hasMoreMessages &&
            html`
              <div class="flex justify-center mb-4">
                ${loadingMore
                  ? html`
                      <div
                        class="px-4 py-2 text-sm text-gray-400 flex items-center gap-2"
                      >
                        <${SpinnerIcon} className="w-4 h-4" />
                        <span>Loading earlier messages...</span>
                      </div>
                    `
                  : html`
                      <button
                        onClick=${handleLoadMore}
                        class="px-4 py-2 text-sm text-gray-400 hover:text-white bg-slate-800 hover:bg-slate-700 rounded-full transition-colors flex items-center gap-2"
                      >
                        <${ChevronUpIcon} className="w-4 h-4" />
                        <span>Load earlier messages</span>
                      </button>
                    `}
              </div>
            `}
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
                          Get started by creating a workspace in Settings (<span
                            class="inline-block align-middle"
                          >
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
                                    You'll be able to choose which workspace to
                                    use
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
            ${messages.map(
              (msg, i) => html`
                <${Message}
                  key=${msg.timestamp + "-" + i}
                  message=${msg}
                  isLast=${i === messages.length - 1}
                  isStreaming=${isStreaming}
                />
              `,
            )}
            <div ref=${messagesEndRef} />
          </div>

          <!-- Scroll to bottom button -->
          ${(!isUserAtBottom || hasNewMessages) &&
          messages.length > 0 &&
          html`
            <button
              onClick=${() => scrollToBottom(true)}
              class="scroll-to-bottom-btn ${hasNewMessages ? "has-new" : ""}"
              title="Scroll to bottom"
            >
              <${ArrowDownIcon} className="w-5 h-5" />
              ${hasNewMessages &&
              html` <span class="new-messages-indicator"></span> `}
            </button>
          `}
        </div>

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
          onSend=${sendPrompt}
          onCancel=${cancelPrompt}
          disabled=${!connected || !activeSessionId}
          isStreaming=${isStreaming}
          isReadOnly=${sessionInfo?.isReadOnly}
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
        />
        </div>
      </div>
    </div>
  `;
}

// =============================================================================
// Mount Application
// =============================================================================

render(html`<${App} />`, document.getElementById("app"));
