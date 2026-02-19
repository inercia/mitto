// Mitto Web Interface - Settings Dialog Component
const { useState, useEffect, html } = window.preact;

// Import utilities
import {
  secureFetch,
  apiUrl,
  hasNativeFolderPicker,
  pickFolder,
  openExternalURL,
} from "../utils/index.js";

// Import shared library functions
import {
  validateUsername,
  validatePassword,
  getWorkspaceVisualInfo,
  getBasename,
} from "../lib.js";

// Import components
import {
  SpinnerIcon,
  CloseIcon,
  SettingsIcon,
  PlusIcon,
  TrashIcon,
  EditIcon,
  ServerEmptyIcon,
  FolderIcon,
  LightningIcon,
  DragHandleIcon,
  LockIcon,
} from "./Icons.js";

// Import constants
import { CYCLING_MODE, CYCLING_MODE_OPTIONS } from "../constants.js";

// Import WorkspaceBadge from app.js - we'll need to pass it as a prop or extract it
// For now, we'll receive it as a prop

/**
 * Helper component for editing a server inline
 * Server-specific prompts are read-only (managed via prompt files with acps: field)
 */
function ServerEditForm({ server, onSave, onCancel }) {
  const [name, setName] = useState(server.name);
  const [command, setCommand] = useState(server.command);
  // All prompts are now file-based (read-only)
  const filePrompts = server.prompts || [];

  const handleSave = () => {
    onSave(name, command);
  };

  return html`
    <div class="space-y-3">
      <div>
        <label class="block text-sm text-gray-400 mb-1">Server Name</label>
        <input
          type="text"
          value=${name}
          onInput=${(e) => setName(e.target.value)}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1">Command</label>
        <input
          type="text"
          value=${command}
          onInput=${(e) => setCommand(e.target.value)}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>

      <!-- Server-specific prompts (read-only, from files with acps: field) -->
      ${filePrompts.length > 0 &&
      html`
        <div>
          <label class="text-sm text-gray-400 mb-2 block"
            >Server-specific prompts
            <span class="text-xs text-gray-500"
              >(from prompt files)</span
            ></label
          >
          <div class="space-y-1">
            ${filePrompts.map(
              (p, idx) => html`
                <div
                  key=${idx}
                  class="flex items-center gap-2 p-2 bg-slate-800/50 rounded text-sm border border-slate-700/50"
                  title="From prompts file with acps: ${server.name}"
                >
                  <div class="flex-1 min-w-0">
                    <div class="font-medium text-xs">${p.name}</div>
                    <div
                      class="text-xs text-gray-500 truncate"
                      title=${p.prompt}
                    >
                      ${p.description || p.prompt}
                    </div>
                  </div>
                </div>
              `,
            )}
          </div>
        </div>
      `}

      <div class="flex justify-end gap-2">
        <button
          onClick=${onCancel}
          class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
        >
          Cancel
        </button>
        <button
          onClick=${handleSave}
          class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
        >
          Save
        </button>
      </div>
    </div>
  `;
}

/**
 * Helper component for editing a prompt inline
 */
function PromptEditForm({ prompt, onSave, onCancel, readOnly = false }) {
  const [name, setName] = useState(prompt.name);
  const [text, setText] = useState(prompt.prompt);
  const [backgroundColor, setBackgroundColor] = useState(
    prompt.backgroundColor || "",
  );

  return html`
    <div class="space-y-3">
      <div>
        <label class="block text-sm text-gray-400 mb-1">Button Label</label>
        <input
          type="text"
          value=${name}
          onInput=${(e) => setName(e.target.value)}
          disabled=${readOnly}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${readOnly
            ? "opacity-60 cursor-not-allowed"
            : ""}"
        />
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1">Prompt Text</label>
        <textarea
          value=${text}
          onInput=${(e) => setText(e.target.value)}
          rows="3"
          disabled=${readOnly}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none ${readOnly
            ? "opacity-60 cursor-not-allowed"
            : ""}"
        />
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1"
          >Background Color (optional)</label
        >
        <div class="flex items-center gap-2">
          <input
            type="color"
            value=${backgroundColor || "#334155"}
            onInput=${(e) => setBackgroundColor(e.target.value)}
            disabled=${readOnly}
            class="w-10 h-10 rounded cursor-pointer border border-slate-600 ${readOnly
              ? "opacity-60 cursor-not-allowed"
              : ""}"
            title="Choose background color"
          />
          <input
            type="text"
            value=${backgroundColor}
            onInput=${(e) => setBackgroundColor(e.target.value)}
            placeholder="#E8F5E9"
            disabled=${readOnly}
            class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono ${readOnly
              ? "opacity-60 cursor-not-allowed"
              : ""}"
          />
          ${backgroundColor &&
          !readOnly &&
          html`
            <button
              type="button"
              onClick=${() => setBackgroundColor("")}
              class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
              title="Clear color"
            >
              <svg
                class="w-4 h-4 text-gray-400"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M6 18L18 6M6 6l12 12"
                />
              </svg>
            </button>
          `}
        </div>
      </div>
      <div class="flex justify-end gap-2">
        <button
          onClick=${onCancel}
          class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
        >
          ${readOnly ? "Close" : "Cancel"}
        </button>
        ${!readOnly &&
        html`
          <button
            onClick=${() => onSave(name, text, backgroundColor)}
            disabled=${!name.trim() || !text.trim()}
            class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50"
          >
            Save
          </button>
        `}
      </div>
    </div>
  `;
}

/**
 * Settings Dialog Component
 * Manages ACP servers, workspaces, prompts, web access, and UI settings.
 */
export function SettingsDialog({
  isOpen,
  onClose,
  onSave,
  forceOpen = false,
  WorkspaceBadge,
}) {
  const [activeTab, setActiveTab] = useState("servers");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [warning, setWarning] = useState("");

  // Configuration state
  const [workspaces, setWorkspaces] = useState([]);
  const [acpServers, setAcpServers] = useState([]);
  const [authEnabled, setAuthEnabled] = useState(false);
  const [authUsername, setAuthUsername] = useState("");
  const [authPassword, setAuthPassword] = useState("");
  const [externalPort, setExternalPort] = useState(""); // Empty string = random port
  const [currentExternalPort, setCurrentExternalPort] = useState(null); // Currently running external port
  const [externalEnabled, setExternalEnabled] = useState(false); // Is external listener currently running
  const [hookUpCommand, setHookUpCommand] = useState("");
  const [hookDownCommand, setHookDownCommand] = useState("");

  // Stored sessions for checking workspace usage
  const [storedSessions, setStoredSessions] = useState([]);

  // Orphaned workspaces (filtered out due to missing servers)
  const [orphanedWorkspaces, setOrphanedWorkspaces] = useState([]);

  // Supported runners (fetched from server based on platform)
  const [supportedRunners, setSupportedRunners] = useState([]);

  // Form state for adding new items
  const [showAddWorkspace, setShowAddWorkspace] = useState(false);
  const [newWorkspacePath, setNewWorkspacePath] = useState("");
  const [newWorkspaceServer, setNewWorkspaceServer] = useState("");
  const [newWorkspaceRunner, setNewWorkspaceRunner] = useState("exec");

  const [showAddServer, setShowAddServer] = useState(false);
  const [newServerName, setNewServerName] = useState("");
  const [newServerCommand, setNewServerCommand] = useState("");

  const [editingServer, setEditingServer] = useState(null);

  // Prompts state
  const [prompts, setPrompts] = useState([]);
  const [showAddPrompt, setShowAddPrompt] = useState(false);
  const [newPromptName, setNewPromptName] = useState("");
  const [newPromptText, setNewPromptText] = useState("");
  const [newPromptColor, setNewPromptColor] = useState("");
  const [editingPrompt, setEditingPrompt] = useState(null);

  // Prompt drag-and-drop state
  const [draggedPromptIndex, setDraggedPromptIndex] = useState(null);
  const [dragOverPromptIndex, setDragOverPromptIndex] = useState(null);

  // UI settings state (macOS only)
  const [agentCompletedSound, setAgentCompletedSound] = useState(false);
  const [nativeNotifications, setNativeNotifications] = useState(false);
  const [notificationPermissionStatus, setNotificationPermissionStatus] =
    useState(-1); // -1 = unknown, 0 = not determined, 1 = denied, 2 = authorized
  const [showInAllSpaces, setShowInAllSpaces] = useState(false);
  const [startAtLogin, setStartAtLogin] = useState(false);
  const [loginItemSupported, setLoginItemSupported] = useState(false);
  const [badgeClickEnabled, setBadgeClickEnabled] = useState(true);
  const [badgeClickCommand, setBadgeClickCommand] =
    useState("open ${WORKSPACE}");

  // Confirmation settings (all platforms)
  const [confirmDeleteSession, setConfirmDeleteSession] = useState(true);
  // Confirmation settings (macOS only)
  const [confirmQuitWithRunningSessions, setConfirmQuitWithRunningSessions] =
    useState(true);

  // Archive retention period setting
  const [archiveRetentionPeriod, setArchiveRetentionPeriod] = useState("never");

  // Follow-up suggestions settings (advanced) - enabled by default
  const [actionButtonsEnabled, setActionButtonsEnabled] = useState(true);

  // External images settings (advanced) - disabled by default for security
  const [externalImagesEnabled, setExternalImagesEnabled] = useState(false);

  // Input font family setting (web UI)
  const [inputFontFamily, setInputFontFamily] = useState("system");

  // Conversation cycling mode setting (web UI) - default: "all"
  const [conversationCyclingMode, setConversationCyclingMode] = useState(
    CYCLING_MODE.ALL,
  );

  // Follow system theme setting (client-side, stored in localStorage)
  const [followSystemTheme, setFollowSystemTheme] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto-follow-system-theme");
      return saved === null ? true : saved === "true";
    }
    return true;
  });

  // Check if running in the native macOS app
  const isMacApp = typeof window.mittoPickFolder === "function";

  // Handle follow system theme toggle
  const handleFollowSystemThemeChange = (enabled) => {
    setFollowSystemTheme(enabled);
    localStorage.setItem("mitto-follow-system-theme", String(enabled));
    // Dispatch a custom event so app.js can react to the change
    window.dispatchEvent(
      new CustomEvent("mitto-follow-system-theme-changed", {
        detail: { enabled },
      }),
    );
  };

  // Load configuration when dialog opens
  useEffect(() => {
    if (isOpen) {
      // Clear any previous messages when dialog opens
      setError("");
      setWarning("");
      setSuccess("");
      loadConfig();
      loadStoredSessions();
      loadSupportedRunners();
    }
  }, [isOpen]);

  // Load stored sessions to check workspace usage
  const loadStoredSessions = async () => {
    try {
      const res = await fetch(apiUrl("/api/sessions"), {
        credentials: "same-origin",
      });
      if (res.ok) {
        const sessions = await res.json();
        setStoredSessions(sessions || []);
      }
    } catch (err) {
      console.error("Failed to load stored sessions:", err);
    }
  };

  // Load supported runners from server
  const loadSupportedRunners = async () => {
    try {
      const res = await fetch(apiUrl("/api/supported-runners"), {
        credentials: "same-origin",
      });
      if (res.ok) {
        const runners = await res.json();
        setSupportedRunners(runners || []);
      }
    } catch (err) {
      console.error("Failed to load supported runners:", err);
      // Fallback to all runners if fetch fails
      setSupportedRunners([
        { type: "exec", label: "exec (no restrictions)", supported: true },
        {
          type: "sandbox-exec",
          label: "sandbox-exec (macOS)",
          supported: false,
        },
        { type: "firejail", label: "firejail (Linux)", supported: false },
        { type: "docker", label: "docker (all platforms)", supported: true },
      ]);
    }
  };

  // Count conversations using a specific workspace
  const getWorkspaceConversationCount = (workingDir) => {
    return storedSessions.filter((s) => s.working_dir === workingDir).length;
  };

  // Save prompts order to settings.json immediately
  const savePromptsOrder = async (newPrompts) => {
    try {
      // Get current config first
      const configRes = await fetch(apiUrl("/api/config"), {
        credentials: "same-origin",
      });
      const config = await configRes.json();

      // Build the config object with updated prompts
      const webConfig = {
        host: config.web?.host || "127.0.0.1",
        external_port: config.web?.external_port || 0,
        auth: config.web?.auth || null,
        hooks: config.web?.hooks || null,
      };

      // Filter prompts to only save settings-based prompts (not file-based ones)
      const settingsPrompts = newPrompts
        .filter((p) => !p.source || p.source === "settings")
        .map(({ source, ...rest }) => rest); // Remove source field before saving

      const saveConfig = {
        workspaces: config.workspaces || [],
        acp_servers: config.acp_servers || [],
        prompts: settingsPrompts,
        web: webConfig,
        ui: config.ui || {},
      };

      await secureFetch(apiUrl("/api/config"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(saveConfig),
      });
    } catch (err) {
      console.error("Failed to save prompts order:", err);
    }
  };

  // Prompt drag-and-drop handlers
  const handlePromptDragStart = (e, index) => {
    setDraggedPromptIndex(index);
    e.dataTransfer.effectAllowed = "move";
    // Set drag image data (required for Firefox)
    e.dataTransfer.setData("text/plain", index.toString());
  };

  const handlePromptDragEnd = () => {
    setDraggedPromptIndex(null);
    setDragOverPromptIndex(null);
  };

  const handlePromptDragOver = (e, index) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    if (draggedPromptIndex !== null && index !== draggedPromptIndex) {
      setDragOverPromptIndex(index);
    }
  };

  const handlePromptDragLeave = () => {
    setDragOverPromptIndex(null);
  };

  const handlePromptDrop = (e, dropIndex) => {
    e.preventDefault();
    if (draggedPromptIndex === null || draggedPromptIndex === dropIndex) {
      setDraggedPromptIndex(null);
      setDragOverPromptIndex(null);
      return;
    }

    // Reorder prompts
    const newPrompts = [...prompts];
    const [draggedItem] = newPrompts.splice(draggedPromptIndex, 1);
    newPrompts.splice(dropIndex, 0, draggedItem);

    setPrompts(newPrompts);
    setDraggedPromptIndex(null);
    setDragOverPromptIndex(null);

    // Save immediately
    savePromptsOrder(newPrompts);
  };

  const loadConfig = async () => {
    setLoading(true);
    setError("");
    try {
      // Fetch config and external status in parallel
      const [configRes, externalStatusRes] = await Promise.all([
        fetch(apiUrl("/api/config"), { credentials: "same-origin" }),
        fetch(apiUrl("/api/external-status"), { credentials: "same-origin" }),
      ]);
      const config = await configRes.json();

      // Load external status
      if (externalStatusRes.ok) {
        const externalStatus = await externalStatusRes.json();
        setExternalEnabled(externalStatus.enabled);
        setCurrentExternalPort(externalStatus.port || null);
      }

      // Load ACP servers first (needed for workspace validation)
      const servers = config.acp_servers || [];
      setAcpServers(servers);

      // Filter out invalid workspaces:
      // - Must have a non-empty working_dir
      // - Must reference an existing ACP server
      const serverNames = new Set(servers.map((s) => s.name));
      const rawWorkspaces = config.workspaces || [];
      const orphaned = []; // Track workspaces with missing servers
      const validWorkspaces = rawWorkspaces.filter((ws) => {
        // Check for valid working_dir
        if (
          !ws.working_dir ||
          typeof ws.working_dir !== "string" ||
          ws.working_dir.trim() === ""
        ) {
          console.warn("Filtering out workspace with invalid working_dir:", ws);
          return false;
        }
        // Check for valid ACP server reference
        if (!ws.acp_server || !serverNames.has(ws.acp_server)) {
          console.warn(
            "Filtering out workspace with invalid/missing ACP server:",
            ws,
          );
          // Track orphaned workspaces (those with missing servers)
          if (ws.acp_server) {
            orphaned.push({
              working_dir: ws.working_dir,
              missing_server: ws.acp_server,
            });
          }
          return false;
        }
        return true;
      });
      setWorkspaces(validWorkspaces);
      setOrphanedWorkspaces(orphaned);

      // Load auth settings - check if external access is enabled
      // External access is enabled if auth is configured OR host is 0.0.0.0
      const hasAuth = config.web?.auth?.simple;
      const isExternalHost = config.web?.host === "0.0.0.0";
      if (hasAuth || isExternalHost) {
        setAuthEnabled(true);
        setAuthUsername(config.web?.auth?.simple?.username || "");
        setAuthPassword(config.web?.auth?.simple?.password || "");
      } else {
        setAuthEnabled(false);
        setAuthUsername("");
        setAuthPassword("");
      }

      // Load external port setting (0 or empty = random)
      const extPort = config.web?.external_port;
      setExternalPort(extPort && extPort > 0 ? String(extPort) : "");

      // Load hook settings
      setHookUpCommand(config.web?.hooks?.up?.command || "");
      setHookDownCommand(config.web?.hooks?.down?.command || "");

      // Load prompts from top-level (not under web)
      setPrompts(config.prompts || []);

      // Load UI settings (macOS only)
      setAgentCompletedSound(
        config.ui?.mac?.notifications?.sounds?.agent_completed || false,
      );
      setNativeNotifications(
        config.ui?.mac?.notifications?.native_enabled || false,
      );
      setShowInAllSpaces(config.ui?.mac?.show_in_all_spaces || false);

      // Load badge click action settings (macOS only)
      setBadgeClickEnabled(
        config.ui?.mac?.badge_click_action?.enabled !== false,
      );
      setBadgeClickCommand(
        config.ui?.mac?.badge_click_action?.command || "open ${WORKSPACE}",
      );

      // Load notification permission status (macOS only) - used to show warning if denied
      if (typeof window.mittoGetNotificationPermissionStatus === "function") {
        const status = await window.mittoGetNotificationPermissionStatus();
        setNotificationPermissionStatus(status);
      }

      // Load login item state from native API (macOS 13+ only)
      if (typeof window.mittoIsLoginItemSupported === "function") {
        const supported = await window.mittoIsLoginItemSupported();
        setLoginItemSupported(supported);
        if (supported && typeof window.mittoIsLoginItemEnabled === "function") {
          const enabled = await window.mittoIsLoginItemEnabled();
          setStartAtLogin(enabled);
        }
      }

      // Load confirmation settings (all platforms, default to true)
      setConfirmDeleteSession(
        config.ui?.confirmations?.delete_session !== false,
      );
      // Load confirmation settings (macOS only, default to true)
      setConfirmQuitWithRunningSessions(
        config.ui?.confirmations?.quit_with_running_sessions !== false,
      );

      // Load archive retention period setting (default to "never")
      setArchiveRetentionPeriod(
        config.session?.archive_retention_period || "never",
      );

      // Load follow-up suggestions settings (advanced) - enabled by default
      setActionButtonsEnabled(
        config.conversations?.action_buttons?.enabled !== false,
      );

      // Load external images settings (advanced) - disabled by default for security
      setExternalImagesEnabled(
        config.conversations?.external_images?.enabled === true,
      );

      // Load input font family setting (web UI) - default to "system"
      setInputFontFamily(config.ui?.web?.input_font_family || "system");

      // Load conversation cycling mode setting (web UI) - default to "all"
      setConversationCyclingMode(
        config.ui?.web?.conversation_cycling_mode || CYCLING_MODE.ALL,
      );

      // Set default server for new workspace
      if (servers.length > 0) {
        setNewWorkspaceServer(servers[0].name);
      }
    } catch (err) {
      setError("Failed to load configuration: " + err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    setError("");
    setWarning("");
    setSuccess("");

    // Validation
    if (workspaces.length === 0) {
      setError("At least one workspace is required");
      setActiveTab("workspaces");
      return;
    }

    if (acpServers.length === 0) {
      setError("At least one ACP server is required");
      setActiveTab("servers");
      return;
    }

    if (authEnabled) {
      const usernameError = validateUsername(authUsername);
      if (usernameError) {
        setError(usernameError);
        setActiveTab("web");
        return;
      }
      const passwordError = validatePassword(authPassword);
      if (passwordError) {
        setError(passwordError);
        setActiveTab("web");
        return;
      }
    }

    setSaving(true);
    try {
      // Build web config
      const webConfig = {
        // Set host based on external access setting
        host: authEnabled ? "0.0.0.0" : "127.0.0.1",
        // External port: parse as int, 0 or empty means random
        external_port: externalPort ? parseInt(externalPort, 10) : 0,
        auth: authEnabled
          ? {
              simple: {
                username: authUsername.trim(),
                password: authPassword.trim(),
              },
            }
          : null,
      };

      // Add hooks if configured
      if (hookUpCommand.trim() || hookDownCommand.trim()) {
        webConfig.hooks = {};
        if (hookUpCommand.trim()) {
          webConfig.hooks.up = { command: hookUpCommand.trim() };
        }
        if (hookDownCommand.trim()) {
          webConfig.hooks.down = { command: hookDownCommand.trim() };
        }
      }

      // Build UI config
      const uiConfig = {
        // Confirmations (all platforms)
        confirmations: {
          delete_session: confirmDeleteSession,
        },
        // Web-specific UI settings
        web: {
          input_font_family: inputFontFamily,
          conversation_cycling_mode: conversationCyclingMode,
        },
      };

      // Add macOS-specific settings
      if (isMacApp) {
        // Add quit confirmation setting (macOS only)
        uiConfig.confirmations.quit_with_running_sessions =
          confirmQuitWithRunningSessions;
        uiConfig.mac = {
          notifications: {
            sounds: {
              agent_completed: agentCompletedSound,
            },
            native_enabled: nativeNotifications,
          },
          show_in_all_spaces: showInAllSpaces,
          start_at_login: startAtLogin,
          badge_click_action: {
            enabled: badgeClickEnabled,
            command: badgeClickCommand,
          },
        };
      }

      // Build conversations config with explicit enabled state
      const conversationsConfig = {
        action_buttons: {
          enabled: actionButtonsEnabled,
        },
        external_images: {
          enabled: externalImagesEnabled,
        },
      };

      // Build session config with archive retention period
      const sessionConfig = {
        archive_retention_period: archiveRetentionPeriod,
      };

      // Filter prompts to only save settings-based prompts (not file-based ones)
      // Prompts with source='settings' or no source (new prompts) should be saved
      // Prompts with source='file' or source='workspace' should not be saved to settings.json
      const settingsPrompts = prompts
        .filter((p) => !p.source || p.source === "settings")
        .map(({ source, ...rest }) => rest); // Remove source field before saving

      // ACP servers are saved with source field so backend can filter out RC file servers
      // (RC file servers are managed in .mittorc, not settings.json)
      const acpServersToSave = acpServers.map((srv) => ({
        name: srv.name,
        command: srv.command,
        source: srv.source || "settings", // Default to settings if not specified
      }));

      const config = {
        workspaces: workspaces,
        acp_servers: acpServersToSave,
        prompts: settingsPrompts, // Only settings-based prompts
        web: webConfig,
        ui: uiConfig,
        conversations: conversationsConfig,
        session: sessionConfig,
      };

      // DEBUG: Log config being saved
      console.log("DEBUG: Saving config:", JSON.stringify(config.ui, null, 2));
      console.log("DEBUG: nativeNotifications state:", nativeNotifications);

      const res = await secureFetch(apiUrl("/api/config"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });

      const result = await res.json();

      if (!res.ok) {
        throw new Error(result.error || "Failed to save configuration");
      }

      // Update the global sound and notification setting flags
      if (isMacApp) {
        window.mittoAgentCompletedSoundEnabled = agentCompletedSound;
        window.mittoNativeNotificationsEnabled = nativeNotifications;

        // Update quit confirmation setting via native API
        if (typeof window.mittoSetQuitConfirmEnabled === "function") {
          try {
            window.mittoSetQuitConfirmEnabled(confirmQuitWithRunningSessions);
          } catch (err) {
            console.error("Failed to update quit confirmation setting:", err);
          }
        }
      }

      // Apply login item setting via native API (macOS 13+ only)
      if (
        loginItemSupported &&
        typeof window.mittoSetLoginItemEnabled === "function"
      ) {
        try {
          await window.mittoSetLoginItemEnabled(startAtLogin);
        } catch (err) {
          console.error("Failed to update login item:", err);
          // Don't fail the save, just log the error
        }
      }

      // Fetch updated external status to get the actual running port
      let actualExternalPort = null;
      let externalAccessActive = false;
      try {
        const statusRes = await fetch(apiUrl("/api/external-status"), {
          credentials: "same-origin",
        });
        if (statusRes.ok) {
          const status = await statusRes.json();
          externalAccessActive = status.enabled;
          actualExternalPort = status.port;
          setExternalEnabled(status.enabled);
          setCurrentExternalPort(status.port || null);
        }
      } catch (e) {
        console.error("Failed to fetch external status:", e);
      }

      // Build success message based on what was applied
      let successMsg = "Configuration saved successfully";
      if (externalAccessActive && actualExternalPort) {
        successMsg = `Configuration saved. External access on port ${actualExternalPort}`;
      } else if (result.applied) {
        const details = [];
        if (result.applied.external_access_enabled) {
          details.push("external access enabled");
        }
        if (result.applied.auth_enabled) {
          details.push("authentication active");
        }
        if (details.length > 0) {
          successMsg += ` (${details.join(", ")})`;
        }
      }
      setSuccess(successMsg);

      onSave?.();

      // Close dialog after short delay
      setTimeout(() => onClose?.(), 500);
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  const handleClose = () => {
    // Always require at least one ACP server
    if (acpServers.length === 0) {
      setError("At least one ACP server is required");
      setActiveTab("servers");
      return;
    }
    // Always require at least one workspace
    if (workspaces.length === 0) {
      setError("At least one workspace is required");
      setActiveTab("workspaces");
      return;
    }
    onClose?.();
  };

  // Workspace management
  const addWorkspace = () => {
    if (!newWorkspacePath.trim()) {
      setError("Please enter a directory path");
      return;
    }
    if (!newWorkspaceServer) {
      setError("Please select an ACP server");
      return;
    }

    // Find the ACP command for this server
    const server = acpServers.find((s) => s.name === newWorkspaceServer);
    if (!server) {
      setError("Selected ACP server not found");
      return;
    }

    // Check for duplicate
    if (workspaces.some((ws) => ws.working_dir === newWorkspacePath.trim())) {
      setError("A workspace with this path already exists");
      return;
    }

    setWorkspaces([
      ...workspaces,
      {
        working_dir: newWorkspacePath.trim(),
        acp_server: newWorkspaceServer,
        acp_command: server.command,
        restricted_runner: newWorkspaceRunner,
      },
    ]);
    setNewWorkspacePath("");
    setNewWorkspaceRunner("exec");
    setShowAddWorkspace(false);
    setError("");
  };

  const removeWorkspace = (workingDir) => {
    if (workspaces.length <= 1) {
      setError("At least one workspace is required");
      return;
    }

    // Check if any conversations are using this workspace
    const conversationCount = getWorkspaceConversationCount(workingDir);
    if (conversationCount > 0) {
      setError(
        `Cannot remove workspace: ${conversationCount} conversation(s) are using it. Delete the conversations first.`,
      );
      return;
    }

    setWorkspaces(workspaces.filter((ws) => ws.working_dir !== workingDir));
  };

  // Update workspace color
  const updateWorkspaceColor = (workingDir, color) => {
    setWorkspaces(
      workspaces.map((ws) =>
        ws.working_dir === workingDir
          ? { ...ws, color: color || undefined } // undefined to omit from JSON if empty
          : ws,
      ),
    );
  };

  // Update workspace friendly name
  const updateWorkspaceName = (workingDir, name) => {
    setWorkspaces(
      workspaces.map((ws) =>
        ws.working_dir === workingDir
          ? { ...ws, name: name || undefined } // undefined to omit from JSON if empty
          : ws,
      ),
    );
  };

  // Update workspace code (three-letter abbreviation)
  const updateWorkspaceCode = (workingDir, code) => {
    // Ensure code is uppercase and max 3 characters
    const sanitizedCode = (code || "").toUpperCase().slice(0, 3);
    setWorkspaces(
      workspaces.map((ws) =>
        ws.working_dir === workingDir
          ? { ...ws, code: sanitizedCode || undefined } // undefined to omit from JSON if empty
          : ws,
      ),
    );
  };

  // Update workspace restricted runner
  const updateWorkspaceRunner = (workingDir, runner) => {
    setWorkspaces(
      workspaces.map((ws) =>
        ws.working_dir === workingDir
          ? { ...ws, restricted_runner: runner || "exec" }
          : ws,
      ),
    );
  };

  // ACP Server management
  const addServer = () => {
    if (!newServerName.trim()) {
      setError("Please enter a server name");
      return;
    }
    if (!newServerCommand.trim()) {
      setError("Please enter a server command");
      return;
    }
    if (acpServers.some((s) => s.name === newServerName.trim())) {
      setError("A server with this name already exists");
      return;
    }

    setAcpServers([
      ...acpServers,
      {
        name: newServerName.trim(),
        command: newServerCommand.trim(),
        source: "settings", // New servers added via UI are saved to settings.json
      },
    ]);
    setNewServerName("");
    setNewServerCommand("");
    setShowAddServer(false);
    setError("");
  };

  const updateServer = (oldName, newName, newCommand) => {
    if (!newName.trim() || !newCommand.trim()) {
      setError("Server name and command cannot be empty");
      return;
    }

    // Check for duplicate name (excluding current)
    if (
      newName !== oldName &&
      acpServers.some((s) => s.name === newName.trim())
    ) {
      setError("A server with this name already exists");
      return;
    }

    // Update server (prompts are now read-only from files)
    setAcpServers(
      acpServers.map((s) =>
        s.name === oldName
          ? {
              name: newName.trim(),
              command: newCommand.trim(),
              prompts: s.prompts, // Preserve existing prompts (read-only from files)
              source: s.source, // Preserve source (rcfile or settings)
            }
          : s,
      ),
    );

    // Update workspaces that reference this server
    if (newName !== oldName) {
      setWorkspaces(
        workspaces.map((ws) =>
          ws.acp_server === oldName
            ? { ...ws, acp_server: newName.trim() }
            : ws,
        ),
      );
    }

    setEditingServer(null);
    setError("");
  };

  const removeServer = (serverName) => {
    // Check if any workspace uses this server
    const usedBy = workspaces.filter((ws) => ws.acp_server === serverName);
    if (usedBy.length > 0) {
      // Build a helpful error message listing the workspaces using this server
      const workspacePaths = usedBy.map((ws) => ws.working_dir).slice(0, 3); // Show up to 3
      const pathList = workspacePaths.join(", ");
      const moreCount = usedBy.length - workspacePaths.length;
      const moreText = moreCount > 0 ? ` and ${moreCount} more` : "";
      setError(
        `Cannot delete "${serverName}": used by workspace(s): ${pathList}${moreText}. Remove or reassign these workspaces first.`,
      );
      setActiveTab("workspaces"); // Switch to workspaces tab to help user fix the issue
      return;
    }

    if (acpServers.length <= 1) {
      setError("At least one ACP server is required");
      return;
    }

    setAcpServers(acpServers.filter((s) => s.name !== serverName));
    setError(""); // Clear any previous errors
  };

  if (!isOpen) return null;

  // Can close if we have both ACP servers and workspaces configured
  const canClose = acpServers.length > 0 && workspaces.length > 0;

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${canClose ? handleClose : null}
    >
      <div
        class="bg-mitto-sidebar rounded-xl w-[600px] max-w-[95vw] max-h-[90vh] overflow-hidden shadow-2xl flex flex-col"
        onClick=${(e) => e.stopPropagation()}
      >
        <!-- Header -->
        <div
          class="flex items-center justify-between p-4 border-b border-slate-700"
        >
          <h3 class="text-lg font-semibold flex items-center gap-2">
            <${SettingsIcon} className="w-5 h-5" />
            Settings
          </h3>
          ${canClose &&
          html`
            <button
              onClick=${handleClose}
              class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors"
            >
              <${CloseIcon} className="w-5 h-5" />
            </button>
          `}
        </div>

        <!-- Tabs -->
        <div class="flex border-b border-slate-700">
          <button
            onClick=${() => setActiveTab("servers")}
            class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab ===
            "servers"
              ? "text-blue-400 border-b-2 border-blue-400"
              : "text-gray-400 hover:text-white"}"
          >
            ACP Servers
          </button>
          <button
            onClick=${() => setActiveTab("workspaces")}
            class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab ===
            "workspaces"
              ? "text-blue-400 border-b-2 border-blue-400"
              : "text-gray-400 hover:text-white"}"
          >
            Workspaces
          </button>
          <button
            onClick=${() => setActiveTab("prompts")}
            class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab ===
            "prompts"
              ? "text-blue-400 border-b-2 border-blue-400"
              : "text-gray-400 hover:text-white"}"
          >
            Prompts
          </button>
          <button
            onClick=${() => setActiveTab("web")}
            class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab ===
            "web"
              ? "text-blue-400 border-b-2 border-blue-400"
              : "text-gray-400 hover:text-white"}"
          >
            Web
          </button>
          <button
            onClick=${() => setActiveTab("ui")}
            class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab ===
            "ui"
              ? "text-blue-400 border-b-2 border-blue-400"
              : "text-gray-400 hover:text-white"}"
          >
            UI
          </button>
        </div>

        <!-- Content -->
        <div class="flex-1 overflow-y-auto p-4">
          ${loading
            ? html`
                <div class="flex items-center justify-center py-12">
                  <${SpinnerIcon} className="w-8 h-8 text-blue-400" />
                </div>
              `
            : html`
                <!-- Workspaces Tab -->
                ${activeTab === "workspaces" &&
                html`
                  <div class="space-y-4">
                    <!-- Workspace explanation -->
                    <div
                      class="p-3 bg-slate-800/50 rounded-lg border border-slate-700"
                    >
                      <p class="text-gray-300 text-sm leading-relaxed">
                        A${" "}
                        <span class="text-blue-400 font-medium">Workspace</span>
                        ${" "}pairs a directory with an ACP server. Each
                        workspace allows you to work on a specific project with
                        a chosen AI agent. You can configure multiple workspaces
                        to work on different projects simultaneously.
                      </p>
                    </div>

                    ${orphanedWorkspaces.length > 0 &&
                    html`
                      <div
                        class="p-3 bg-amber-500/10 border border-amber-500/30 rounded-lg"
                      >
                        <div class="flex items-start gap-2">
                          <span class="text-amber-400 text-lg">⚠️</span>
                          <div class="flex-1">
                            <p class="text-amber-400 text-sm font-medium mb-1">
                              ${orphanedWorkspaces.length}
                              workspace${orphanedWorkspaces.length > 1
                                ? "s"
                                : ""}
                              removed due to missing
                              server${orphanedWorkspaces.length > 1 ? "s" : ""}
                            </p>
                            <p class="text-amber-300/80 text-xs mb-2">
                              The following
                              workspace${orphanedWorkspaces.length > 1
                                ? "s reference servers that no longer exist"
                                : " references a server that no longer exists"}.
                              This can happen if a server was removed from your
                              .mittorc file.
                            </p>
                            <ul class="text-xs text-amber-300/70 space-y-1">
                              ${orphanedWorkspaces.map(
                                (ow) => html`
                                  <li
                                    key=${ow.working_dir}
                                    class="flex items-center gap-1"
                                  >
                                    <span class="text-amber-400">•</span>
                                    <span
                                      class="font-mono truncate"
                                      title=${ow.working_dir}
                                      >${ow.working_dir}</span
                                    >
                                    <span class="text-amber-500/70">→</span>
                                    <span class="text-red-400/80"
                                      >${ow.missing_server}</span
                                    >
                                    <span class="text-amber-500/50"
                                      >(missing)</span
                                    >
                                  </li>
                                `,
                              )}
                            </ul>
                          </div>
                        </div>
                      </div>
                    `}

                    <div class="flex items-center justify-between">
                      <p class="text-gray-400 text-sm">
                        Configured workspaces:
                      </p>
                      <button
                        onClick=${() =>
                          acpServers.length > 0 &&
                          setShowAddWorkspace(!showAddWorkspace)}
                        disabled=${acpServers.length === 0}
                        class="p-1.5 rounded-lg transition-colors ${acpServers.length ===
                        0
                          ? "opacity-50 cursor-not-allowed"
                          : "hover:bg-slate-700"} ${showAddWorkspace
                          ? "bg-slate-700"
                          : ""}"
                        title=${acpServers.length === 0
                          ? "Add an ACP server first"
                          : "Add Workspace"}
                      >
                        <${PlusIcon} className="w-5 h-5" />
                      </button>
                    </div>
                    ${acpServers.length === 0 &&
                    html`
                      <div
                        class="p-3 bg-yellow-500/10 border border-yellow-500/30 rounded-lg text-yellow-400 text-sm"
                      >
                        ⚠️ Add an ACP server first before creating workspaces.
                      </div>
                    `}
                    ${showAddWorkspace &&
                    html`
                      <div
                        class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3"
                      >
                        <div>
                          <label class="block text-sm text-gray-400 mb-1"
                            >Directory Path</label
                          >
                          <div class="flex gap-2">
                            <input
                              type="text"
                              value=${newWorkspacePath}
                              onInput=${(e) =>
                                setNewWorkspacePath(e.target.value)}
                              placeholder="/path/to/project"
                              class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                            />
                            ${hasNativeFolderPicker() &&
                            html`
                              <button
                                type="button"
                                onClick=${async () => {
                                  const path = await pickFolder();
                                  if (path) {
                                    setNewWorkspacePath(path);
                                  }
                                }}
                                class="px-3 py-2 bg-slate-600 hover:bg-slate-500 rounded-lg text-sm transition-colors whitespace-nowrap"
                              >
                                Browse…
                              </button>
                            `}
                          </div>
                        </div>
                        <div>
                          <label class="block text-sm text-gray-400 mb-1"
                            >ACP Server</label
                          >
                          <select
                            value=${newWorkspaceServer}
                            onChange=${(e) =>
                              setNewWorkspaceServer(e.target.value)}
                            class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                          >
                            ${acpServers.map(
                              (srv) => html`
                                <option key=${srv.name} value=${srv.name}>
                                  ${srv.name}
                                </option>
                              `,
                            )}
                          </select>
                        </div>
                        <div>
                          <label class="block text-sm text-gray-400 mb-1"
                            >Sandbox Type</label
                          >
                          <select
                            value=${newWorkspaceRunner}
                            onChange=${(e) =>
                              setNewWorkspaceRunner(e.target.value)}
                            class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                            title="Choose the runner type for sandboxing agent execution"
                          >
                            ${supportedRunners
                              .filter((r) => r.supported)
                              .map(
                                (r) =>
                                  html`<option value=${r.type}>
                                    ${r.label}
                                  </option>`,
                              )}
                          </select>
                          <p class="text-xs text-gray-500 mt-1">
                            Controls how the agent is sandboxed. "exec" runs
                            with no restrictions (recommended for most users).
                          </p>
                        </div>
                        <div class="flex justify-end gap-2">
                          <button
                            onClick=${() => {
                              setShowAddWorkspace(false);
                              setNewWorkspacePath("");
                              setNewWorkspaceRunner("exec");
                            }}
                            class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                          >
                            Cancel
                          </button>
                          <button
                            onClick=${addWorkspace}
                            class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
                          >
                            Add
                          </button>
                        </div>
                      </div>
                    `}
                    ${workspaces.length === 0
                      ? html`
                          <div class="text-center py-8 text-gray-500">
                            <${FolderIcon}
                              className="w-12 h-12 mx-auto mb-2 opacity-50"
                            />
                            <p>No workspaces configured.</p>
                            <p class="text-xs mt-1">
                              Click + to add a workspace.
                            </p>
                          </div>
                        `
                      : html`
                          <div class="space-y-3">
                            ${workspaces.map(
                              (ws) => html`
                                <div
                                  key=${ws.working_dir}
                                  class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 hover:bg-slate-700/30 transition-colors group"
                                >
                                  <!-- Top row: Badge preview, path, ACP server, delete button -->
                                  <div class="flex items-center gap-3 mb-3">
                                    <${WorkspaceBadge}
                                      path=${ws.working_dir}
                                      customColor=${ws.color}
                                      customCode=${ws.code}
                                      customName=${ws.name}
                                      size="sm"
                                    />
                                    <div class="flex-1 min-w-0">
                                      <div
                                        class="text-xs text-gray-500 truncate"
                                        title=${ws.working_dir}
                                      >
                                        ${ws.working_dir}
                                      </div>
                                    </div>
                                    <span
                                      class="px-2 py-1 bg-blue-500/20 text-blue-400 rounded text-xs flex-shrink-0"
                                    >
                                      ${ws.acp_server}
                                    </span>
                                    <span
                                      class="px-2 py-1 bg-purple-500/20 text-purple-400 rounded text-xs flex-shrink-0"
                                      title="Sandbox type"
                                    >
                                      ${ws.restricted_runner || "exec"}
                                    </span>
                                    <button
                                      onClick=${() =>
                                        removeWorkspace(ws.working_dir)}
                                      class="p-1.5 text-gray-500 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                      title="Remove workspace"
                                    >
                                      <${TrashIcon} className="w-4 h-4" />
                                    </button>
                                  </div>

                                  <!-- Customization row: Name, Code, Color inputs -->
                                  <div class="flex items-center gap-3 pl-11">
                                    <div class="flex-1 min-w-0">
                                      <input
                                        type="text"
                                        value=${ws.name || ""}
                                        onInput=${(e) =>
                                          updateWorkspaceName(
                                            ws.working_dir,
                                            e.target.value,
                                          )}
                                        placeholder=${getBasename(
                                          ws.working_dir,
                                        )}
                                        class="w-full px-2 py-1 bg-gray-100 dark:bg-slate-700/50 rounded text-sm text-gray-900 dark:text-gray-100 focus:outline-none focus:ring-1 focus:ring-blue-500 placeholder-gray-400 dark:placeholder-gray-500"
                                        title="Friendly name (optional)"
                                      />
                                    </div>
                                    <div class="flex items-center gap-2">
                                      <select
                                        value=${ws.restricted_runner || "exec"}
                                        onChange=${(e) =>
                                          updateWorkspaceRunner(
                                            ws.working_dir,
                                            e.target.value,
                                          )}
                                        class="px-2 py-1 bg-gray-100 dark:bg-slate-700/50 rounded text-sm text-gray-900 dark:text-gray-100 focus:outline-none focus:ring-1 focus:ring-blue-500"
                                        title="Sandbox type"
                                      >
                                        ${supportedRunners
                                          .filter((r) => r.supported)
                                          .map(
                                            (r) =>
                                              html`<option value=${r.type}>
                                                ${r.type}
                                              </option>`,
                                          )}
                                      </select>
                                      <input
                                        type="text"
                                        value=${ws.code || ""}
                                        onInput=${(e) =>
                                          updateWorkspaceCode(
                                            ws.working_dir,
                                            e.target.value,
                                          )}
                                        placeholder=${getWorkspaceVisualInfo(
                                          ws.working_dir,
                                        ).abbreviation}
                                        maxlength="3"
                                        class="w-12 px-2 py-1 bg-gray-100 dark:bg-slate-700/50 rounded text-sm text-center uppercase text-gray-900 dark:text-gray-100 focus:outline-none focus:ring-1 focus:ring-blue-500 placeholder-gray-400 dark:placeholder-gray-500"
                                        title="Three-letter code (optional)"
                                      />
                                      <input
                                        type="color"
                                        value=${ws.color ||
                                        getWorkspaceVisualInfo(ws.working_dir)
                                          .color.backgroundHex ||
                                        "#808080"}
                                        onChange=${(e) =>
                                          updateWorkspaceColor(
                                            ws.working_dir,
                                            e.target.value,
                                          )}
                                        class="w-8 h-8 rounded cursor-pointer border border-slate-600 bg-transparent p-0.5"
                                        title="Badge color"
                                      />
                                    </div>
                                  </div>
                                </div>
                              `,
                            )}
                          </div>
                        `}
                  </div>
                `}

                <!-- ACP Servers Tab -->
                ${activeTab === "servers" &&
                html`
                  <div class="space-y-4">
                    <div class="flex items-center justify-between">
                      <p class="text-gray-400 text-sm">
                        ACP servers are AI coding assistants.${" "}
                        <a
                          href="https://agentclientprotocol.com/overview/agents"
                          onClick=${(e) => {
                            e.preventDefault();
                            openExternalURL(
                              "https://agentclientprotocol.com/overview/agents",
                            );
                          }}
                          class="text-blue-400 hover:text-blue-300 underline cursor-pointer"
                          >Popular examples</a
                        >${" "} include Auggie and Claude Code. You can
                        configure multiple servers and choose which one to use
                        for each workspace.
                      </p>
                      <button
                        onClick=${() => setShowAddServer(!showAddServer)}
                        class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${showAddServer
                          ? "bg-slate-700"
                          : ""}"
                        title="Add Server"
                      >
                        <${PlusIcon} className="w-5 h-5" />
                      </button>
                    </div>

                    ${showAddServer &&
                    html`
                      <div
                        class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3"
                      >
                        <div>
                          <label class="block text-sm text-gray-400 mb-1"
                            >Server Name</label
                          >
                          <input
                            type="text"
                            value=${newServerName}
                            onInput=${(e) => setNewServerName(e.target.value)}
                            placeholder="e.g., claude-code"
                            class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                          />
                        </div>
                        <div>
                          <label class="block text-sm text-gray-400 mb-1"
                            >Command</label
                          >
                          <input
                            type="text"
                            value=${newServerCommand}
                            onInput=${(e) =>
                              setNewServerCommand(e.target.value)}
                            placeholder="e.g., npx -y @anthropic/claude-code-acp"
                            class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                          />
                        </div>
                        <div class="flex justify-end gap-2">
                          <button
                            onClick=${() => {
                              setShowAddServer(false);
                              setNewServerName("");
                              setNewServerCommand("");
                            }}
                            class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                          >
                            Cancel
                          </button>
                          <button
                            onClick=${addServer}
                            class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
                          >
                            Add
                          </button>
                        </div>
                      </div>
                    `}
                    ${acpServers.length === 0
                      ? html`
                          <div class="text-center py-8 text-gray-500">
                            <${ServerEmptyIcon}
                              className="w-12 h-12 mx-auto mb-2 opacity-50"
                            />
                            <p>No ACP servers configured.</p>
                            <p class="text-xs mt-1">Click + to add a server.</p>
                          </div>
                        `
                      : html`
                          <div class="space-y-2">
                            ${acpServers.map((srv) => {
                              // RC file servers are read-only (cannot edit/delete)
                              const isRCFile = srv.source === "rcfile";
                              return html`
                                <div
                                  key=${srv.name}
                                  class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 ${isRCFile
                                    ? ""
                                    : "hover:bg-slate-700/30"} transition-colors group ${isRCFile
                                    ? "opacity-80"
                                    : ""}"
                                >
                                  ${editingServer === srv.name && !isRCFile
                                    ? html`
                                        <${ServerEditForm}
                                          server=${srv}
                                          onSave=${(name, cmd) =>
                                            updateServer(srv.name, name, cmd)}
                                          onCancel=${() =>
                                            setEditingServer(null)}
                                        />
                                      `
                                    : html`
                                        <div class="flex items-center gap-3">
                                          <div class="flex-1 min-w-0">
                                            <div
                                              class="font-medium text-sm flex items-center gap-2"
                                            >
                                              ${srv.name}
                                              ${isRCFile &&
                                              html`
                                                <span
                                                  class="flex items-center gap-1 text-xs text-amber-400"
                                                  title="This server is defined in .mittorc and cannot be modified here"
                                                >
                                                  <${LockIcon}
                                                    className="w-3 h-3"
                                                  />
                                                </span>
                                              `}
                                              ${srv.prompts?.length > 0 &&
                                              html`
                                                <span
                                                  class="flex items-center gap-1 text-xs text-blue-400"
                                                  title="${srv.prompts
                                                    .length} server-specific prompt(s)"
                                                >
                                                  <${LightningIcon}
                                                    className="w-3.5 h-3.5"
                                                  />
                                                  ${srv.prompts.length}
                                                </span>
                                              `}
                                            </div>
                                            <div
                                              class="text-xs text-gray-500 truncate"
                                              title=${srv.command}
                                            >
                                              ${srv.command}
                                              ${isRCFile &&
                                              html`<span
                                                class="ml-2 text-amber-500/70"
                                                >(from .mittorc)</span
                                              >`}
                                            </div>
                                          </div>
                                          ${!isRCFile &&
                                          html`
                                            <button
                                              onClick=${() =>
                                                setEditingServer(srv.name)}
                                              class="p-1.5 text-gray-500 hover:text-blue-400 hover:bg-blue-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                              title="Edit server"
                                            >
                                              <${EditIcon}
                                                className="w-4 h-4"
                                              />
                                            </button>
                                            <button
                                              onClick=${() =>
                                                removeServer(srv.name)}
                                              class="p-1.5 text-gray-500 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                              title="Remove server"
                                            >
                                              <${TrashIcon}
                                                className="w-4 h-4"
                                              />
                                            </button>
                                          `}
                                        </div>
                                      `}
                                </div>
                              `;
                            })}
                          </div>
                        `}
                  </div>
                `}

                <!-- Prompts Tab -->
                ${activeTab === "prompts" &&
                html`
                  <div class="space-y-4">
                    <div class="flex items-center justify-between">
                      <p class="text-gray-400 text-sm">
                        Predefined prompts appear as quick-access buttons in the
                        chat input.
                      </p>
                      <button
                        onClick=${() => setShowAddPrompt(!showAddPrompt)}
                        class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${showAddPrompt
                          ? "bg-slate-700"
                          : ""}"
                        title="Add Prompt"
                      >
                        <${PlusIcon} className="w-5 h-5" />
                      </button>
                    </div>

                    <!-- Add New Prompt Form -->
                    ${showAddPrompt &&
                    html`
                      <div
                        class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3"
                      >
                        <div>
                          <label class="block text-sm text-gray-400 mb-1"
                            >Button Label</label
                          >
                          <input
                            type="text"
                            value=${newPromptName}
                            onInput=${(e) => setNewPromptName(e.target.value)}
                            placeholder="e.g., Continue"
                            class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                          />
                        </div>
                        <div>
                          <label class="block text-sm text-gray-400 mb-1"
                            >Prompt Text</label
                          >
                          <textarea
                            value=${newPromptText}
                            onInput=${(e) => setNewPromptText(e.target.value)}
                            placeholder="e.g., Please continue with the current task."
                            rows="3"
                            class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                          />
                        </div>
                        <div>
                          <label class="block text-sm text-gray-400 mb-1"
                            >Background Color (optional)</label
                          >
                          <div class="flex items-center gap-2">
                            <input
                              type="color"
                              value=${newPromptColor || "#334155"}
                              onInput=${(e) =>
                                setNewPromptColor(e.target.value)}
                              class="w-10 h-10 rounded cursor-pointer border border-slate-600"
                              title="Choose background color"
                            />
                            <input
                              type="text"
                              value=${newPromptColor}
                              onInput=${(e) =>
                                setNewPromptColor(e.target.value)}
                              placeholder="#E8F5E9"
                              class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                            />
                            ${newPromptColor &&
                            html`
                              <button
                                type="button"
                                onClick=${() => setNewPromptColor("")}
                                class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                                title="Clear color"
                              >
                                <svg
                                  class="w-4 h-4 text-gray-400"
                                  fill="none"
                                  stroke="currentColor"
                                  viewBox="0 0 24 24"
                                >
                                  <path
                                    stroke-linecap="round"
                                    stroke-linejoin="round"
                                    stroke-width="2"
                                    d="M6 18L18 6M6 6l12 12"
                                  />
                                </svg>
                              </button>
                            `}
                          </div>
                        </div>
                        <div class="flex justify-end gap-2">
                          <button
                            onClick=${() => {
                              setShowAddPrompt(false);
                              setNewPromptName("");
                              setNewPromptText("");
                              setNewPromptColor("");
                            }}
                            class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                          >
                            Cancel
                          </button>
                          <button
                            onClick=${() => {
                              if (
                                newPromptName.trim() &&
                                newPromptText.trim()
                              ) {
                                const newPrompt = {
                                  name: newPromptName.trim(),
                                  prompt: newPromptText.trim(),
                                };
                                if (newPromptColor) {
                                  newPrompt.backgroundColor = newPromptColor;
                                }
                                setPrompts([...prompts, newPrompt]);
                                setNewPromptName("");
                                setNewPromptText("");
                                setNewPromptColor("");
                                setShowAddPrompt(false);
                              }
                            }}
                            disabled=${!newPromptName.trim() ||
                            !newPromptText.trim()}
                            class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50"
                          >
                            Add Prompt
                          </button>
                        </div>
                      </div>
                    `}

                    <!-- Prompts List -->
                    <div class="space-y-2">
                      ${prompts.length === 0
                        ? html`
                            <div class="p-4 text-center text-gray-500 text-sm">
                              No prompts configured. Click + to add one.
                            </div>
                          `
                        : prompts.map(
                            (prompt, index) => html`
                              <div
                                key=${index}
                                draggable=${editingPrompt !== index}
                                onDragStart=${(e) =>
                                  handlePromptDragStart(e, index)}
                                onDragEnd=${handlePromptDragEnd}
                                onDragOver=${(e) =>
                                  handlePromptDragOver(e, index)}
                                onDragLeave=${handlePromptDragLeave}
                                onDrop=${(e) => handlePromptDrop(e, index)}
                                class="p-3 bg-slate-700/20 rounded-lg border transition-all ${draggedPromptIndex ===
                                index
                                  ? "opacity-50 border-blue-500 border-dashed"
                                  : dragOverPromptIndex === index
                                    ? "border-blue-400 border-2 bg-blue-500/10"
                                    : "border-slate-600/50"}"
                              >
                                ${editingPrompt === index
                                  ? html`
                                      <${PromptEditForm}
                                        prompt=${prompt}
                                        readOnly=${prompt.source === "file" ||
                                        prompt.source === "workspace"}
                                        onSave=${(name, text, bgColor) => {
                                          const updated = [...prompts];
                                          updated[index] = {
                                            ...prompts[index],
                                            name,
                                            prompt: text,
                                          };
                                          if (bgColor) {
                                            updated[index].backgroundColor =
                                              bgColor;
                                          }
                                          setPrompts(updated);
                                          setEditingPrompt(null);
                                        }}
                                        onCancel=${() => setEditingPrompt(null)}
                                      />
                                    `
                                  : html`
                                      <div
                                        class="flex items-start justify-between gap-3"
                                      >
                                        <div
                                          class="flex items-center gap-2 flex-1 min-w-0"
                                        >
                                          <div
                                            class="cursor-grab active:cursor-grabbing p-1 -ml-1 text-gray-500 hover:text-gray-300 transition-colors flex-shrink-0"
                                            title="Drag to reorder"
                                          >
                                            <${DragHandleIcon}
                                              className="w-4 h-4"
                                            />
                                          </div>
                                          ${prompt.backgroundColor &&
                                          html`
                                            <div
                                              class="w-4 h-4 rounded flex-shrink-0 border border-slate-500"
                                              style=${{
                                                backgroundColor:
                                                  prompt.backgroundColor,
                                              }}
                                              title=${prompt.backgroundColor}
                                            />
                                          `}
                                          <div class="flex-1 min-w-0">
                                            <div
                                              class="font-medium text-sm text-blue-400 flex items-center gap-2"
                                            >
                                              ${prompt.name}
                                              ${prompt.source === "file" &&
                                              html`
                                                <span
                                                  class="text-xs px-1.5 py-0.5 bg-slate-600 text-gray-300 rounded"
                                                  title="Loaded from file (read-only)"
                                                  >file</span
                                                >
                                              `}
                                              ${prompt.source === "workspace" &&
                                              html`
                                                <span
                                                  class="text-xs px-1.5 py-0.5 bg-purple-600/50 text-purple-200 rounded"
                                                  title="Defined in workspace .mittorc (read-only)"
                                                  >workspace</span
                                                >
                                              `}
                                            </div>
                                            <div
                                              class="text-xs text-gray-500 mt-1 truncate"
                                            >
                                              ${prompt.prompt}
                                            </div>
                                          </div>
                                        </div>
                                        <div class="flex items-center gap-1">
                                          ${prompt.source !== "file" &&
                                          prompt.source !== "workspace"
                                            ? html`
                                                <button
                                                  onClick=${() =>
                                                    setEditingPrompt(index)}
                                                  class="p-1.5 hover:bg-slate-700 rounded transition-colors"
                                                  title="Edit"
                                                >
                                                  <${EditIcon}
                                                    className="w-4 h-4 text-gray-400"
                                                  />
                                                </button>
                                                <button
                                                  onClick=${() => {
                                                    const updated =
                                                      prompts.filter(
                                                        (_, i) => i !== index,
                                                      );
                                                    setPrompts(updated);
                                                  }}
                                                  class="p-1.5 hover:bg-red-500/20 rounded transition-colors"
                                                  title="Delete"
                                                >
                                                  <${TrashIcon}
                                                    className="w-4 h-4 text-gray-400 hover:text-red-400"
                                                  />
                                                </button>
                                              `
                                            : html`
                                                <button
                                                  onClick=${() =>
                                                    setEditingPrompt(index)}
                                                  class="p-1.5 hover:bg-slate-700 rounded transition-colors"
                                                  title="View"
                                                >
                                                  <${EditIcon}
                                                    className="w-4 h-4 text-gray-500"
                                                  />
                                                </button>
                                              `}
                                        </div>
                                      </div>
                                    `}
                              </div>
                            `,
                          )}
                    </div>
                  </div>
                `}

                <!-- Web Tab -->
                ${activeTab === "web" &&
                html`
                  <div class="space-y-4">
                    <p class="text-gray-400 text-sm">
                      Configure external access
                      settings${authEnabled ? " and lifecycle hooks" : ""}.
                    </p>

                    <!-- External Access Section -->
                    <div class="space-y-3">
                      <h4 class="text-sm font-medium text-gray-300">
                        External Access
                      </h4>

                      <label
                        class="flex items-center gap-3 p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                      >
                        <input
                          type="checkbox"
                          checked=${authEnabled}
                          onChange=${(e) => setAuthEnabled(e.target.checked)}
                          class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                        />
                        <div>
                          <div class="font-medium text-sm">
                            Allow External Access
                          </div>
                          <div class="text-xs text-gray-500">
                            Listen on all interfaces (0.0.0.0) and require
                            authentication
                          </div>
                        </div>
                      </label>

                      ${authEnabled &&
                      html`
                        <div
                          class="p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 space-y-3"
                        >
                          <!-- Username and Password in same row -->
                          <div class="flex items-center gap-4">
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-gray-400"
                                >Username</label
                              >
                              <input
                                type="text"
                                value=${authUsername}
                                onInput=${(e) =>
                                  setAuthUsername(e.target.value)}
                                placeholder="admin"
                                class="w-28 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                              />
                            </div>
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-gray-400"
                                >Password</label
                              >
                              <input
                                type="password"
                                value=${authPassword}
                                onInput=${(e) =>
                                  setAuthPassword(e.target.value)}
                                placeholder="••••••••"
                                class="w-28 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                              />
                            </div>
                          </div>
                          <!-- Port setting -->
                          <div class="flex items-center gap-2">
                            <label class="text-sm text-gray-400">Port</label>
                            <input
                              type="number"
                              value=${externalPort}
                              onInput=${(e) => setExternalPort(e.target.value)}
                              placeholder="random"
                              min="1024"
                              max="65535"
                              class="w-24 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                            />
                            <span class="text-xs text-gray-500"
                              >(leave empty for random)</span
                            >
                          </div>
                          <!-- Status indicator -->
                          ${externalEnabled &&
                          currentExternalPort &&
                          html`
                            <div class="text-xs text-green-400">
                              ✓ External access active on port${" "}
                              ${currentExternalPort}
                            </div>
                          `}
                        </div>

                        <!-- Lifecycle Hooks -->
                        <div
                          class="p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 space-y-3"
                        >
                          <h5 class="text-sm font-medium text-gray-300">
                            Lifecycle Hooks
                          </h5>
                          <p class="text-xs text-gray-500">
                            Commands to run when external access starts/stops
                            (e.g., for tunneling).${" "}
                            <button
                              type="button"
                              onClick=${() =>
                                openExternalURL(
                                  "https://github.com/inercia/mitto/blob/main/docs/config/ext-access.md",
                                )}
                              class="text-blue-400 hover:text-blue-300 underline cursor-pointer"
                            >
                              Learn more
                            </button>
                          </p>
                          <div class="flex items-center gap-2">
                            <label class="text-sm text-gray-400 w-12">Up</label>
                            <input
                              type="text"
                              value=${hookUpCommand}
                              onInput=${(e) => setHookUpCommand(e.target.value)}
                              placeholder="e.g., cloudflared tunnel --url http://localhost:$PORT"
                              class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                            />
                          </div>
                          <div class="flex items-center gap-2">
                            <label class="text-sm text-gray-400 w-12"
                              >Down</label
                            >
                            <input
                              type="text"
                              value=${hookDownCommand}
                              onInput=${(e) =>
                                setHookDownCommand(e.target.value)}
                              placeholder="e.g., pkill cloudflared"
                              class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                            />
                          </div>
                        </div>
                      `}
                    </div>
                  </div>
                `}

                <!-- UI Tab -->
                ${activeTab === "ui" &&
                html`
                  <div class="space-y-4">
                    <!-- Appearance Settings (all platforms) -->
                    <div class="space-y-3">
                      <h4 class="text-sm font-medium text-gray-300">
                        Appearance
                      </h4>
                      <label
                        class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                      >
                        <input
                          type="checkbox"
                          checked=${followSystemTheme}
                          onChange=${(e) =>
                            handleFollowSystemThemeChange(e.target.checked)}
                          class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                        />
                        <div>
                          <div class="font-medium text-sm">
                            Follow system theme
                          </div>
                          <div class="text-xs text-gray-500">
                            Automatically switch between light and dark mode
                            based on your system preferences
                          </div>
                        </div>
                      </label>
                      <div
                        class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                      >
                        <div class="flex items-center justify-between">
                          <div>
                            <div class="font-medium text-sm">
                              Input box font
                            </div>
                            <div class="text-xs text-gray-500">
                              Font family for the message compose area
                            </div>
                          </div>
                          <select
                            value=${inputFontFamily}
                            onChange=${(e) =>
                              setInputFontFamily(e.target.value)}
                            class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500"
                          >
                            <option value="system">System Default</option>
                            <option value="sans-serif">Sans-Serif</option>
                            <option value="serif">Serif</option>
                            <option value="monospace">Monospace</option>
                            <option value="menlo">Menlo</option>
                            <option value="monaco">Monaco</option>
                            <option value="consolas">Consolas</option>
                            <option value="courier-new">Courier New</option>
                            <option value="jetbrains-mono">
                              JetBrains Mono
                            </option>
                            <option value="sf-mono">SF Mono</option>
                            <option value="cascadia-code">Cascadia Code</option>
                          </select>
                        </div>
                      </div>
                      <div
                        class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                      >
                        <div class="flex items-center justify-between">
                          <div>
                            <div class="font-medium text-sm">
                              Conversation cycling
                            </div>
                            <div class="text-xs text-gray-500">
                              Which conversations to include when using
                              keyboard/swipe navigation
                            </div>
                          </div>
                          <select
                            value=${conversationCyclingMode}
                            onChange=${(e) =>
                              setConversationCyclingMode(e.target.value)}
                            class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500"
                          >
                            ${CYCLING_MODE_OPTIONS.map(
                              (opt) => html`
                                <option key=${opt.value} value=${opt.value}>
                                  ${opt.label}
                                </option>
                              `,
                            )}
                          </select>
                        </div>
                      </div>
                    </div>

                    <!-- Confirmation Settings (all platforms) -->
                    <div class="space-y-3">
                      <h4 class="text-sm font-medium text-gray-300">
                        Confirmations
                      </h4>
                      <label
                        class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                      >
                        <input
                          type="checkbox"
                          checked=${confirmDeleteSession}
                          onChange=${(e) =>
                            setConfirmDeleteSession(e.target.checked)}
                          class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                        />
                        <div>
                          <div class="font-medium text-sm">
                            Confirm before deleting conversations
                          </div>
                          <div class="text-xs text-gray-500">
                            Show a confirmation dialog when deleting a
                            conversation
                          </div>
                        </div>
                      </label>
                      ${isMacApp &&
                      html`
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${confirmQuitWithRunningSessions}
                            onChange=${(e) =>
                              setConfirmQuitWithRunningSessions(
                                e.target.checked,
                              )}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Confirm before quitting with active conversations
                            </div>
                            <div class="text-xs text-gray-500">
                              Show a confirmation dialog when quitting while an
                              agent is responding
                            </div>
                          </div>
                        </label>
                      `}
                    </div>

                    <!-- Archive Settings -->
                    <div class="space-y-3">
                      <h4 class="text-sm font-medium text-gray-300">
                        Archive Settings
                      </h4>
                      <div
                        class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                      >
                        <div class="flex items-center justify-between">
                          <div>
                            <div class="font-medium text-sm">
                              Auto-delete archived conversations
                            </div>
                            <div class="text-xs text-gray-500">
                              Automatically delete archived conversations after
                              the specified period
                            </div>
                          </div>
                          <select
                            value=${archiveRetentionPeriod}
                            onChange=${(e) =>
                              setArchiveRetentionPeriod(e.target.value)}
                            class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500"
                          >
                            <option value="never">Never</option>
                            <option value="1d">After 1 day</option>
                            <option value="1w">After 1 week</option>
                            <option value="1m">After 1 month</option>
                            <option value="3m">After 3 months</option>
                          </select>
                        </div>
                      </div>
                    </div>

                    <!-- macOS-specific settings -->
                    ${isMacApp &&
                    html`
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          macOS Settings
                        </h4>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${agentCompletedSound}
                            onChange=${(e) =>
                              setAgentCompletedSound(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Play sound when agent completes
                            </div>
                            <div class="text-xs text-gray-500">
                              Play a notification sound when the AI finishes
                              responding
                            </div>
                          </div>
                        </label>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${nativeNotifications}
                            onChange=${(e) => {
                              // Simply save the preference - permission will be requested on app restart
                              setNativeNotifications(e.target.checked);
                            }}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Native notifications
                            </div>
                            <div class="text-xs text-gray-500">
                              Show notifications in macOS Notification Center
                              (requires restart)
                              ${notificationPermissionStatus === 1
                                ? html`<span class="text-yellow-500 ml-1"
                                    >(permission denied in System
                                    Settings)</span
                                  >`
                                : ""}
                            </div>
                          </div>
                        </label>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${showInAllSpaces}
                            onChange=${(e) =>
                              setShowInAllSpaces(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Show in all Spaces
                            </div>
                            <div class="text-xs text-gray-500">
                              Make the window visible in all macOS Spaces
                              (requires restart)
                            </div>
                          </div>
                        </label>
                        ${loginItemSupported &&
                        html`
                          <label
                            class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${startAtLogin}
                              onChange=${(e) =>
                                setStartAtLogin(e.target.checked)}
                              class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Start at Login
                              </div>
                              <div class="text-xs text-gray-500">
                                Launch Mitto automatically when you log in
                              </div>
                            </div>
                          </label>
                        `}

                        <!-- Badge Click Action -->
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${badgeClickEnabled}
                            onChange=${(e) =>
                              setBadgeClickEnabled(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div class="flex-1">
                            <div class="font-medium text-sm">
                              Workspace badge click action
                            </div>
                            <div class="text-xs text-gray-500">
                              Click workspace badge in conversation list to run
                              a command
                            </div>
                          </div>
                        </label>
                        ${badgeClickEnabled &&
                        html`
                          <div
                            class="p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 space-y-2"
                          >
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-gray-400 w-20"
                                >Command</label
                              >
                              <input
                                type="text"
                                value=${badgeClickCommand}
                                onInput=${(e) =>
                                  setBadgeClickCommand(e.target.value)}
                                placeholder="open \${WORKSPACE}"
                                class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                              />
                            </div>
                            <p class="text-xs text-gray-500">
                              Use${" "}
                              <code class="bg-slate-600 px-1 rounded"
                                >\${WORKSPACE}</code
                              >${" "} as placeholder for the workspace path
                            </p>
                          </div>
                        `}
                      </div>
                    `}

                    <!-- Advanced Settings -->
                    <div class="space-y-3 pt-4 border-t border-slate-700/50">
                      <h4 class="text-sm font-medium text-gray-300">
                        Advanced
                      </h4>
                      <label
                        class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                      >
                        <input
                          type="checkbox"
                          checked=${actionButtonsEnabled}
                          onChange=${(e) =>
                            setActionButtonsEnabled(e.target.checked)}
                          class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                        />
                        <div class="flex-1">
                          <div class="font-medium text-sm">
                            Follow-up Suggestions
                          </div>
                          <div class="text-xs text-gray-500">
                            Analyze agent responses to suggest clickable
                            follow-up options (uses auxiliary conversation)
                          </div>
                        </div>
                      </label>
                      <label
                        class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                      >
                        <input
                          type="checkbox"
                          checked=${externalImagesEnabled}
                          onChange=${(e) =>
                            setExternalImagesEnabled(e.target.checked)}
                          class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                        />
                        <div class="flex-1">
                          <div class="font-medium text-sm">
                            Allow External Images
                          </div>
                          <div class="text-xs text-gray-500">
                            Load images from external HTTPS sources in messages
                            (requires restart, may expose your IP to external
                            servers)
                          </div>
                        </div>
                      </label>
                    </div>
                  </div>
                `}
              `}
        </div>

        <!-- Footer -->
        <div class="p-4 border-t border-slate-700">
          ${error &&
          html`
            <div
              class="mb-3 p-3 bg-red-500/20 border border-red-500/50 rounded-lg text-red-400 text-sm"
            >
              ${error}
            </div>
          `}
          ${warning &&
          html`
            <div
              class="mb-3 p-3 bg-yellow-500/20 border border-yellow-500/50 rounded-lg text-yellow-400 text-sm"
            >
              ${warning}
            </div>
          `}
          ${success &&
          html`
            <div
              class="mb-3 p-3 bg-green-500/20 border border-green-500/50 rounded-lg text-green-400 text-sm"
            >
              ${success}
            </div>
          `}
          <div class="flex justify-end gap-3">
            ${canClose &&
            html`
              <button
                onClick=${handleClose}
                class="px-4 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors"
              >
                Cancel
              </button>
            `}
            <button
              onClick=${handleSave}
              disabled=${saving}
              class="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50 flex items-center gap-2"
            >
              ${saving
                ? html`
                    <${SpinnerIcon} className="w-4 h-4" />
                    Saving...
                  `
                : "Save Changes"}
            </button>
          </div>
        </div>
      </div>
    </div>
  `;
}
