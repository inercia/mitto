// Mitto Web Interface - Chat Input Component
// Handles message composition, image uploads, and predefined prompts

const { useState, useEffect, useRef, useCallback, useMemo, html } =
  window.preact;

import {
  hasNativeImagePicker,
  pickImages,
  hasNativeFilePicker,
  pickFiles,
  isNativeApp,
  getAPIPrefix,
} from "../utils/native.js";
import { secureFetch, authFetch } from "../utils/csrf.js";
import { apiUrl, errorMessageFromData } from "../utils/api.js";
import { endpoints } from "../utils/index.js";
import { getContextWindowSize } from "../utils/models.js";
import {
  getPromptSortMode,
  getUIPromptPanelHeight,
  setUIPromptPanelHeight,
  getTextareaMinHeight as getStoredTextareaMinHeight,
  setTextareaMinHeight as setStoredTextareaMinHeight,
} from "../utils/storage.js";
import { useResizeHandle } from "../hooks/useResizeHandle.js";
import { SlashCommandPicker } from "./SlashCommandPicker.js";
import { PeriodicFrequencyPanel } from "./PeriodicFrequencyPanel.js";
import { SavePromptDialog } from "./SavePromptDialog.js";
import { GripIcon } from "./Icons.js";
import { PromptsMenu } from "./PromptsMenu.js";
import { flattenPrompts, getMissingPromptParameters, fetchCachedParamNames, effectiveMissingParams } from "../utils/prompts.js";
import { Tooltip } from "./Tooltip.js";

/**
 * wireMittoFileMarkers - Convert inert <span data-mitto-file="..." data-mitto-line="..."> markers
 * inside a sanitized mitto_ui_form into clickable links that open Mitto's internal file viewer.
 *
 * The agent never emits anchors/hrefs — the URL is built from the trusted current workspace
 * UUID + a validated workspace-relative path + optional line number. Idempotent: only wires
 * elements that haven't been wired yet (guarded by dataset.mittoFileLinkWired).
 */
function wireMittoFileMarkers(root) {
  if (!root || typeof root.querySelectorAll !== "function") return;
  const markers = root.querySelectorAll("[data-mitto-file]");
  if (!markers.length) return;

  const apiPrefix = getAPIPrefix();
  const workspaceUUID =
    window.mittoCurrentWorkspaceUUID ||
    sessionStorage.getItem("mittoCurrentWorkspaceUUID") ||
    "";
  const wsPath = window.mittoCurrentWorkspace || "";
  if (!workspaceUUID) return;

  markers.forEach((el) => {
    if (el.dataset.mittoFileLinkWired === "true") return;
    const rel = el.getAttribute("data-mitto-file");
    if (!rel) return;
    // Defensive re-validation: the backend sanitizer already enforces this,
    // but never trust agent-supplied content even after sanitization.
    if (rel.startsWith("/") || rel.includes("..") || rel.includes("://")) return;
    const lower = rel.toLowerCase();
    if (
      lower.startsWith("javascript:") ||
      lower.startsWith("data:") ||
      lower.startsWith("file:") ||
      lower.startsWith("mailto:")
    ) return;

    const lineRaw = el.getAttribute("data-mitto-line") || "";
    const line = /^\d+$/.test(lineRaw) ? lineRaw : "";

    let viewerUrl = `${apiPrefix}/viewer.html?ws=${encodeURIComponent(workspaceUUID)}&path=${encodeURIComponent(rel)}`;
    if (line) viewerUrl += `&line=${encodeURIComponent(line)}`;
    if (wsPath) viewerUrl += `&ws_path=${encodeURIComponent(wsPath)}`;

    el.classList.add("file-link");
    el.style.cursor = "pointer";
    el.dataset.mittoFileLinkWired = "true";
    el.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
      if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
        const fullUrl = new URL(viewerUrl, window.location.origin).href;
        window.mittoOpenViewer(fullUrl);
      } else {
        window.open(viewerUrl, "_blank", "noopener,noreferrer");
      }
    });
  });
}

/**
 * ChatInputConfigSelect - Select dropdown for a config option with optimistic local state.
 * Prevents the select from reverting to the old value while waiting for the server's
 * config_option_changed WebSocket response.
 */
function ChatInputConfigSelect({ configOption, onSetConfigOption, isStreaming }) {
  const [localValue, setLocalValue] = useState(configOption.current_value);

  // Sync local value when server confirms the change
  useEffect(() => {
    setLocalValue(configOption.current_value);
  }, [configOption.current_value]);

  const handleInput = useCallback(
    (e) => {
      const newValue = e.target.value;
      setLocalValue(newValue); // Update immediately (optimistic)
      onSetConfigOption?.(configOption.id, newValue);
    },
    [configOption.id, onSetConfigOption],
  );

  return html`
    <${Tooltip}
      tip=${isStreaming
        ? configOption.name + " will apply to the next prompt"
        : configOption.description ||
          "Select " + configOption.name.toLowerCase()}
      placement="top"
    >
      <select
        class="select select-ghost select-xs max-w-[200px]"
        value=${localValue || ""}
        onInput=${handleInput}
      >
        ${configOption.options.map(
          (opt) => html` <option value=${opt.value}>${opt.name}</option> `,
        )}
      </select>
    </${Tooltip}>
  `;
}

/**
 * PromptStopButton - Stop button shown inside an active MCP UI prompt panel.
 * Aborts the pending prompt and stops the agent turn. Replaces the former
 * show/hide chat-input toggle: the composition area and an active UI prompt are
 * mutually exclusive, so there is nothing to toggle to.
 */
function PromptStopButton({ onStop }) {
  return html`
    <button
      type="button"
      onClick=${onStop}
      class="chat-input-action stop-active tooltip tooltip-top"
      data-tip="Stop the agent"
      aria-label="Stop the agent"
    >
      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <rect x="6" y="6" width="12" height="12" rx="2" stroke-width="2" />
      </svg>
    </button>
  `;
}

/**
 * ChatInput component - message composition with image support
 * @param {Object} props
 * @param {Function} props.onSend - Callback when message is sent (text, images)
 * @param {Function} props.onCancel - Callback to cancel streaming
 * @param {boolean} props.disabled - Whether input is disabled
 * @param {boolean} props.isStreaming - Whether agent is currently streaming
 * @param {boolean} props.isReadOnly - Whether session is read-only
 * @param {boolean} props.isArchived - Whether session is archived (disables input)
 * @param {boolean} props.isArchivePending - Whether archive is pending (waiting for agent to finish)
 * @param {Array} props.predefinedPrompts - Array of predefined prompts (ChatInput dropup)
 * @param {Array} props.periodicPrompts - Array of prompts for the periodic prompt selector
 * @param {Object} props.inputRef - Ref for external focus control
 * @param {boolean} props.noSession - Whether there's no active session
 * @param {string} props.sessionId - Current session ID
 * @param {string} props.draft - Current draft text
 * @param {Function} props.onDraftChange - Callback when draft changes
 * @param {Function} props.onPromptsOpen - Callback when prompts dropdown is opened (refreshes global and workspace prompts)
 * @param {number} props.queueLength - Current number of messages in queue
 * @param {Object} props.queueConfig - Queue configuration { enabled, max_size, delay_seconds }
 * @param {Function} props.onAddToQueue - Callback to add message to queue (Cmd/Ctrl+Enter)
 * @param {Function} props.onToggleQueue - Callback to toggle queue panel visibility
 * @param {boolean} props.showQueueDropdown - Whether the queue dropdown is currently visible
 * @param {Array} props.actionButtons - Array of action buttons from agent response { label, response }
 * @param {Array} props.availableCommands - Array of available slash commands { name, description, input_hint }
 * @param {boolean} props.periodicConfigured - Whether a periodic config exists (shows editor, disables queue buttons)
 * @param {Function} [props.onPeriodicPrompt] - Called with (prompt) when a periodic-flagged prompt is selected. Routes to app-level branching (decidePeriodicAction). When absent, periodic prompts fall through to the normal send path.
 * @param {Object} props.activeUIPrompt - Active UI prompt from MCP tool { requestId, promptType, question, options, timeoutSeconds, receivedAt }
 * @param {Function} props.onUIPromptAnswer - Callback when user answers a UI prompt (requestId, optionId, label)
 * @param {string} props.workingDir - Workspace directory path (for smart file path insertion on native app drag & drop)
 * @param {string} props.sendKeyMode - Key mode for sending messages: "enter" (default) or "ctrl-enter"
 */
export function ChatInput({
  onSend,
  onCancel,
  disabled,
  isStreaming,
  isRunning = true,
  isReadOnly,
  isArchived = false,
  isArchivePending = false,
  predefinedPrompts = [],
  periodicPrompts = [],
  inputRef,
  noSession = false,
  sessionId,
  draft = "",
  onDraftChange,
  onPromptsOpen,
  queueLength = 0,
  queueConfig = { enabled: true, max_size: 10, delay_seconds: 0 },
  onAddToQueue,
  onToggleQueue,
  showQueueDropdown = false,
  actionButtons = [],
  availableCommands = [],
  periodicConfigured = false,
  onPeriodicPrompt,
  agentSupportsImages = false,
  acpReady = true,
  gcSuspended = false,
  onResume,
  activeUIPrompt = null,
  onUIPromptAnswer,
  workingDir = "",
  sendKeyMode = "enter",
  configOptions = [],
  onSetConfigOption,
  contextUsage = null,
  tokenUsage = null,
  onOpenPromptParamDialog,
}) {
  // Use the draft from parent state instead of local state
  const text = draft;
  const setText = useCallback(
    (newText) => {
      if (onDraftChange) {
        onDraftChange(sessionId, newText);
      }
    },
    [onDraftChange, sessionId],
  );

  const [showDropup, setShowDropup] = useState(false);
  const [promptFilterText, setPromptFilterText] = useState("");
  const [promptSelectedIndex, setPromptSelectedIndex] = useState(-1);
  const [shiftHeld, setShiftHeld] = useState(false);
  const promptFilterInputRef = useRef(null);
  const selectedPromptItemRef = useRef(null);
  // Tracks whether resume was already triggered to avoid firing onResume multiple times
  const resumeTriggeredRef = useRef(false);

  // State for prompt sort mode (alphabetical or color)
  const [promptSortMode, setPromptSortMode] = useState(() =>
    getPromptSortMode(),
  );

  // Find all "select" type config options with options (e.g. "Mode", "Model")
  const selectConfigOptions = useMemo(() => {
    return configOptions?.filter((o) => o.type === "select" && o.options?.length > 0) || [];
  }, [configOptions]);

  // The "model" config option, used to surface a per-prompt model-override chip
  // in the prompts dropup (prompts whose preferredModels resolve to a different
  // model than the current conversation model).
  const modelOption = useMemo(
    () => configOptions?.find((o) => o.category === "model") || null,
    [configOptions],
  );

  // Compute context window usage percentage (null when no data available).
  // Prefers context_usage from ACP SessionUsageUpdate (exact size/used).
  // Falls back to input_tokens from PromptResponse.Usage + static model context window.
  const contextPct = useMemo(() => {
    // Primary: use SessionUsageUpdate data if available
    if (contextUsage?.size > 0 && contextUsage?.used != null) {
      return Math.min(Math.round((contextUsage.used / contextUsage.size) * 100), 100);
    }
    // Fallback: compute from input_tokens + known model context window
    if (tokenUsage?.input_tokens) {
      // Find current model ID from config options
      const modelOpt = configOptions?.find((o) => o.category === "model");
      const modelId = modelOpt?.current_value;
      if (modelId) {
        const ctxWindow = getContextWindowSize(modelId);
        if (ctxWindow) {
          return Math.min(Math.round((tokenUsage.input_tokens / ctxWindow) * 100), 100);
        }
      }
    }
    return null;
  }, [contextUsage, tokenUsage, configOptions]);

  // Compute flat ordered prompt list for keyboard navigation
  const flatFilteredPrompts = useMemo(() => {
    return flattenPrompts(predefinedPrompts, {
      filterText: promptFilterText,
      sortMode: promptSortMode,
    }).flat;
  }, [predefinedPrompts, promptFilterText, promptSortMode]);

  // Handler for toggling the prompts dropdown
  // Calls onPromptsOpen callback when opening to trigger prompt refresh
  const handleTogglePrompts = useCallback(() => {
    const willOpen = !showDropup;
    setShowDropup(willOpen);
    if (willOpen) {
      setPromptFilterText("");
      setPromptSelectedIndex(-1);
      if (onPromptsOpen) {
        onPromptsOpen();
      }
      // Auto-focus the filter input after the dropdown renders
      setTimeout(() => {
        promptFilterInputRef.current?.focus();
      }, 50);
    }
  }, [showDropup, onPromptsOpen]);

  // Track ongoing prompt improvements per session (persists across session switches)
  // Map: sessionId -> { abortController }
  const improvingSessionsRef = useRef(new Map());
  // Force re-render when improving state changes
  const [improvingVersion, setImprovingVersion] = useState(0);
  const [improveError, setImproveError] = useState(null);
  const textareaRef = useRef(null);
  const dropupRef = useRef(null);

  // Save prompt dialog state
  const [showSaveDialog, setShowSaveDialog] = useState(false);

  // Track textarea focus for action toolbar visibility
  const [isTextareaFocused, setIsTextareaFocused] = useState(false);
  const toolbarHideTimeoutRef = useRef(null);
  const toolbarRef = useRef(null);

  // Check if the current session has an active improve request
  const isImproving = improvingSessionsRef.current.has(sessionId);

  // Sending state for message delivery tracking
  const [isSending, setIsSending] = useState(false);
  const [sendError, setSendError] = useState(null);
  // Store message text during send for retry capability
  const [pendingSendText, setPendingSendText] = useState("");
  const [pendingSendImages, setPendingSendImages] = useState([]);

  // Image upload state
  const [pendingImages, setPendingImages] = useState([]); // Array of { id, url, name, mimeType, uploading }
  const [isDragOver, setIsDragOver] = useState(false);
  const [uploadError, setUploadError] = useState(null);
  const imageInputRef = useRef(null); // For image file input
  const fileInputRef = useRef(null); // For general file input

  // File upload state (non-image files)
  const [pendingFiles, setPendingFiles] = useState([]); // Array of { id, name, mimeType, size, category, uploading }
  const [pendingSendFiles, setPendingSendFiles] = useState([]);

  // Slash command autocomplete state
  const [showSlashPicker, setShowSlashPicker] = useState(false);
  const [slashSelectedIndex, setSlashSelectedIndex] = useState(0);

  // UI prompt combo box selection state
  const [comboSelectedId, setComboSelectedId] = useState("");

  // UI prompt free text input state (for mitto_ui_options with allow_free_text)
  const [freeTextInput, setFreeTextInput] = useState("");

  // UI textbox state (for mitto_ui_textbox)
  const [textboxValue, setTextboxValue] = useState("");
  const textboxRef = useRef(null);
  const [isPromptCollapsed, setIsPromptCollapsed] = useState(false);
  const prevCollapsedBeforeUIRef = useRef(false);
  // Expand/collapse state for the periodic settings body (chevron). Lifted here so
  // it stays mutually exclusive with the prompt composition area: only one may be
  // expanded at a time.
  const [periodicExpanded, setPeriodicExpanded] = useState(false);

  // Resize handle for UI prompt panels (textbox, form, options)
  const {
    height: uiPromptHeight,
    isDragging: isPromptDragging,
    handleProps: promptHandleProps,
  } = useResizeHandle({
    initialHeight: getUIPromptPanelHeight(),
    minHeight: 150,
    maxHeight: 600,
    onDragEnd: (finalHeight) => {
      setUIPromptPanelHeight(finalHeight);
    },
  });

  // Periodic prompt lock state
  // When locked, the prompt is saved to the periodic config and textarea is read-only
  const [isPeriodicLocked, setIsPeriodicLocked] = useState(false);
  const [isPeriodicSaving, setIsPeriodicSaving] = useState(false);
  const [periodicPromptName, setPeriodicPromptName] = useState("");

  // Resize handle for textarea min height (controls the visual size of the input area)
  // Hard max for auto-grow (scrollbar appears beyond this)
  const textareaHardMax = 500;
  const {
    height: textareaMinHeight,
    isDragging: isTextareaDragging,
    handleProps: textareaHandleProps,
  } = useResizeHandle({
    initialHeight: getStoredTextareaMinHeight(),
    minHeight: 80,
    maxHeight: 400,
    onHeightChange: (newHeight) => {
      // During drag, directly set textarea height for real-time visual feedback
      const textarea = textareaRef.current;
      if (textarea) {
        textarea.style.height = newHeight + "px";
      }
    },
    onDragEnd: (finalHeight) => {
      setStoredTextareaMinHeight(finalHeight);
    },
  });

  // Scroll selected prompt into view when keyboard selection changes
  useEffect(() => {
    if (promptSelectedIndex >= 0) {
      selectedPromptItemRef.current?.scrollIntoView({ block: "nearest" });
    }
  }, [promptSelectedIndex]);

  // Listen for prompt sort mode changes from settings
  useEffect(() => {
    const handleSortModeChange = (e) => {
      setPromptSortMode(e.detail.mode);
    };
    window.addEventListener(
      "mitto-prompt-sort-mode-changed",
      handleSortModeChange,
    );
    return () => {
      window.removeEventListener(
        "mitto-prompt-sort-mode-changed",
        handleSortModeChange,
      );
    };
  }, []);
  const [periodicPrompt, setPeriodicPrompt] = useState(""); // The saved periodic prompt
  const [periodicFrequency, setPeriodicFrequency] = useState({
    value: 1,
    unit: "hours",
  });
  const [periodicNextScheduledAt, setPeriodicNextScheduledAt] = useState(null);
  const [periodicFreshContext, setPeriodicFreshContext] = useState(false);
  const [periodicMaxIterations, setPeriodicMaxIterations] = useState(0);
  const [periodicIterationCount, setPeriodicIterationCount] = useState(0);
  const [periodicTrigger, setPeriodicTrigger] = useState("schedule");
  const [periodicDelaySeconds, setPeriodicDelaySeconds] = useState(5);
  const [periodicMaxDurationSeconds, setPeriodicMaxDurationSeconds] = useState(0);
  // Reason the periodic loop was auto-stopped (e.g. "maxDuration", "maxIterations",
  // "iterationSafeguard"); empty when running. Drives the restore-dialog wording.
  const [periodicStoppedReason, setPeriodicStoppedReason] = useState("");

  // Track window width for responsive placeholder
  const [isSmallWindow, setIsSmallWindow] = useState(window.innerWidth < 640);
  useEffect(() => {
    const handleResize = () => setIsSmallWindow(window.innerWidth < 640);
    window.addEventListener("resize", handleResize);
    return () => window.removeEventListener("resize", handleResize);
  }, []);

  // Clear pending images/files and sending state when session changes
  // Note: improving state is tracked per-session in improvingSessionsRef and persists
  useEffect(() => {
    setPendingImages([]);
    setPendingFiles([]);
    setUploadError(null);
    setIsSending(false);
    setSendError(null);
    setPendingSendText("");
    setPendingSendImages([]);
    setPendingSendFiles([]);
    setImproveError(null);
    setShowSlashPicker(false);
    setSlashSelectedIndex(0);
    setComboSelectedId(""); // Reset combo box selection
    // Reset periodic lock state when session changes
    setIsPeriodicLocked(false);
    setIsPeriodicSaving(false);
    setPeriodicPrompt("");
    setPeriodicPromptName("");
    setPeriodicFrequency({ value: 1, unit: "hours" });
    setPeriodicNextScheduledAt(null);
    setPeriodicMaxIterations(0);
    setPeriodicIterationCount(0);
    setPeriodicTrigger("schedule");
    setPeriodicDelaySeconds(5);
    setPeriodicMaxDurationSeconds(0);
    setPeriodicStoppedReason("");
    // Collapse the periodic properties body by default when switching
    // conversations (the prompt composition area is collapsed separately by
    // the periodicConfigured effect below).
    setPeriodicExpanded(false);
  }, [sessionId]);

  // Reset combo box selection and free text input when UI prompt changes
  useEffect(() => {
    setComboSelectedId("");
    setFreeTextInput("");
  }, [activeUIPrompt?.requestId]);

  // Auto-hide chat input when MCP UI prompts (textbox, form, options) are active
  useEffect(() => {
    const promptType = activeUIPrompt?.promptType;
    // Auto-collapse for all MCP UI prompt types except permission
    // (permission prompts are inline button-based and don't need the chat input hidden)
    const isMCPUI = promptType && promptType !== "permission";

    if (isMCPUI) {
      if (promptType === "textbox") {
        setTextboxValue(activeUIPrompt.text || "");
      }
      // Save current collapsed state before auto-collapsing
      setIsPromptCollapsed((prev) => {
        prevCollapsedBeforeUIRef.current = prev;
        return true;
      });
    } else if (!periodicConfigured) {
      // Restore previous collapsed state when MCP UI dismisses
      setIsPromptCollapsed(prevCollapsedBeforeUIRef.current);
    }
  }, [activeUIPrompt?.requestId, periodicConfigured]);

  // Fetch periodic config when periodic is configured for this session
  useEffect(() => {
    if (!periodicConfigured || !sessionId) {
      setIsPeriodicLocked(false);
      setPeriodicPrompt("");
      setPeriodicPromptName("");
      setPeriodicFrequency({ value: 1, unit: "hours" });
      setPeriodicNextScheduledAt(null);
      setPeriodicTrigger("schedule");
      setPeriodicDelaySeconds(5);
      setPeriodicMaxDurationSeconds(0);
      setPeriodicStoppedReason("");
      // Don't clear the draft when disabling periodic - preserve user's text
      return;
    }

    // Default to collapsed prompt area for periodic conversations
    setIsPromptCollapsed(true);

    const fetchPeriodicConfig = async () => {
      try {
        const response = await authFetch(
          endpoints.sessions.periodic(sessionId),
        );
        const ct = response.headers.get("content-type");
        if (!response.ok || !ct || !ct.includes("application/json")) {
          console.warn(
            "Periodic config fetch returned non-JSON response:",
            response.status,
            ct,
          );
          return;
        }
        const config = await response.json();
        // Always update frequency
        if (config.frequency) {
          setPeriodicFrequency(config.frequency);
        }
        // Update next_scheduled_at (only set if enabled)
        if (config.enabled && config.next_scheduled_at) {
          setPeriodicNextScheduledAt(config.next_scheduled_at);
        } else {
          setPeriodicNextScheduledAt(null);
        }
        // Update prompt name and fresh context from config
        setPeriodicPromptName(config.prompt_name || "");
        setPeriodicFreshContext(config.fresh_context === true);
        setPeriodicMaxIterations(config.max_iterations ?? 0);
        setPeriodicIterationCount(config.iteration_count ?? 0);
        setPeriodicTrigger(config.trigger || "schedule");
        setPeriodicDelaySeconds(config.delay_seconds ?? 5);
        setPeriodicMaxDurationSeconds(config.max_duration_seconds ?? 0);
        setPeriodicStoppedReason(config.stopped_reason || "");
        // Set lock state based on the enabled field
        const isLocked = config.enabled === true;
        setIsPeriodicLocked(isLocked);
        // Set prompt state based on config
        const isPendingPlaceholder = config.prompt === "(pending)";
        if (config.prompt && !isPendingPlaceholder) {
          setPeriodicPrompt(config.prompt);
        } else {
          setPeriodicPrompt("");
        }
      } catch (err) {
        console.error("Failed to fetch periodic config:", err);
      }
    };

    fetchPeriodicConfig();
  }, [periodicConfigured, sessionId]);

  // Listen for periodic config updates from other clients via WebSocket
  useEffect(() => {
    const handlePeriodicConfigUpdated = (event) => {
      const {
        sessionId: updatedSessionId,
        periodicConfigured,
        periodicEnabled: newPeriodicEnabled,
        frequency,
        nextScheduledAt,
        iterationCount,
        maxIterations,
        stoppedReason,
      } = event.detail;
      // Only update if this is for our session
      if (updatedSessionId !== sessionId) return;

      // Update frequency if provided
      if (frequency) {
        setPeriodicFrequency(frequency);
      }
      if (iterationCount !== undefined) setPeriodicIterationCount(iterationCount);
      if (maxIterations !== undefined) setPeriodicMaxIterations(maxIterations);

      // If periodic config was deleted (not configured), reset state
      if (periodicConfigured === false) {
        setIsPeriodicLocked(false);
        setPeriodicNextScheduledAt(null);
        setPeriodicPrompt("");
        return;
      }

      // If periodic run is disabled (unlocked), update lock state
      if (newPeriodicEnabled === false) {
        setIsPeriodicLocked(false);
        setPeriodicNextScheduledAt(null);
        // Capture why the loop stopped so the restore dialog can offer to reset
        // the elapsed iterations/time when a max-iterations/max-duration cap was hit.
        setPeriodicStoppedReason(stoppedReason || "");
        // Don't clear the prompt - user may want to re-enable without re-typing
        return;
      }

      // If periodic run is enabled (locked), fetch the full config for the prompt
      if (newPeriodicEnabled === true) {
        // Update next scheduled time
        if (nextScheduledAt) {
          setPeriodicNextScheduledAt(nextScheduledAt);
        }
        // Fetch the full config to get the prompt name and fresh_context
        authFetch(endpoints.sessions.periodic(sessionId))
          .then(async (response) => {
            if (!response.ok) return null;
            const ct = response.headers.get("content-type");
            if (!ct || !ct.includes("application/json")) {
              console.warn(
                "Periodic config fetch returned non-JSON response:",
                response.status,
                ct,
              );
              return null;
            }
            return response.json();
          })
          .then((config) => {
            if (!config) return;
            setPeriodicPromptName(config.prompt_name || "");
            setPeriodicFreshContext(config.fresh_context === true);
            setPeriodicMaxIterations(config.max_iterations ?? 0);
            setPeriodicIterationCount(config.iteration_count ?? 0);
            setPeriodicTrigger(config.trigger || "schedule");
            setPeriodicDelaySeconds(config.delay_seconds ?? 5);
            setPeriodicMaxDurationSeconds(config.max_duration_seconds ?? 0);
            setPeriodicStoppedReason(config.stopped_reason || "");
            const isPendingPlaceholder = config.prompt === "(pending)";
            if (config.prompt && !isPendingPlaceholder) {
              setPeriodicPrompt(config.prompt);
              setIsPeriodicLocked(true);
            }
          })
          .catch((err) =>
            console.error("Failed to fetch periodic config:", err),
          );
      }
    };

    window.addEventListener(
      "mitto:periodic_config_updated",
      handlePeriodicConfigUpdated,
    );
    return () => {
      window.removeEventListener(
        "mitto:periodic_config_updated",
        handlePeriodicConfigUpdated,
      );
    };
  }, [sessionId]);

  // Compute slash command filter from current text (text after '/' when text starts with '/')
  const slashFilter =
    text.startsWith("/") && showSlashPicker ? text.slice(1).split(/\s/)[0] : "";

  // Get filtered commands for the picker
  const filteredSlashCommands = availableCommands.filter((cmd) =>
    cmd.name.toLowerCase().startsWith(slashFilter.toLowerCase()),
  );

  // Reset selection index when filter changes
  useEffect(() => {
    setSlashSelectedIndex(0);
  }, [slashFilter]);

  // Determine if input should be fully disabled (no session, explicitly disabled, archived, or archive pending)
  const isFullyDisabled =
    disabled || noSession || isSending || isArchived || isArchivePending;

  // Session exists but ACP agent hasn't started yet (e.g., during resume).
  // Blocks sending and action buttons, but allows typing so drafts are preserved.
  // GC-suspended sessions are intentionally paused — don't show the "Resuming" banner.
  const isResuming = !isRunning && !isArchived && !noSession && !disabled && !gcSuspended;

  // Expose focus and togglePrompts methods via inputRef for external control
  useEffect(() => {
    if (inputRef) {
      inputRef.current = {
        focus: () => {
          if (textareaRef.current) {
            textareaRef.current.focus();
          }
        },
        togglePrompts: () => {
          handleTogglePrompts();
        },
      };
    }
  }, [inputRef, handleTogglePrompts]);

  // Close dropup when clicking outside
  useEffect(() => {
    const handleClickOutside = (e) => {
      if (dropupRef.current && !dropupRef.current.contains(e.target)) {
        setShowDropup(false);
      }
    };
    if (showDropup) {
      document.addEventListener("mousedown", handleClickOutside);
      return () =>
        document.removeEventListener("mousedown", handleClickOutside);
    }
  }, [showDropup]);

  // Track Shift key state while prompt dropdown is open
  useEffect(() => {
    if (!showDropup) {
      setShiftHeld(false);
      return;
    }
    const onKey = (e) => setShiftHeld(e.shiftKey);
    window.addEventListener("keydown", onKey);
    window.addEventListener("keyup", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("keyup", onKey);
    };
  }, [showDropup]);

  // Adjust textarea height when draft changes (e.g., switching sessions)
  // Also re-adjusts when periodic lock state changes (collapse when locked, expand when unlocked)
  // Auto-sizing: grow to content, but respect min-height from resize handle and hard max
  useEffect(() => {
    if (isTextareaDragging) return; // Skip auto-sizing during drag (onHeightChange handles it)
    const textarea = textareaRef.current;
    if (textarea) {
      textarea.style.height = "auto";
      const targetHeight = Math.max(
        textareaMinHeight,
        Math.min(textarea.scrollHeight, textareaHardMax),
      );
      textarea.style.height = targetHeight + "px";
    }
  }, [text, textareaMinHeight, isTextareaDragging, textareaHardMax]);

  // Auto-size the mitto_ui_textbox textarea to fit its content on activation.
  // The panel uses max-height, so short content stays compact; long content is
  // capped by the panel and scrolls within the container.
  useEffect(() => {
    if (activeUIPrompt?.promptType !== "textbox") return;
    const ta = textboxRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = ta.scrollHeight + "px";
  }, [activeUIPrompt?.requestId, activeUIPrompt?.promptType]);

  // Clean up toolbar hide timeout on unmount
  useEffect(() => {
    return () => {
      if (toolbarHideTimeoutRef.current) {
        clearTimeout(toolbarHideTimeoutRef.current);
      }
    };
  }, []);

  // Reset the resume-triggered guard whenever gcSuspended becomes false
  // (i.e., session resumes), so future suspensions can trigger again.
  useEffect(() => {
    if (!gcSuspended) {
      resumeTriggeredRef.current = false;
    }
  }, [gcSuspended]);

  // Trigger resume when user interacts with a gc_suspended session.
  // Uses a ref guard so onResume is only called once per suspension period.
  const handleResumeOnInteraction = useCallback(() => {
    if (gcSuspended && !acpReady && onResume && !resumeTriggeredRef.current) {
      resumeTriggeredRef.current = true;
      onResume();
    }
  }, [gcSuspended, acpReady, onResume]);

  // Handle textarea focus - show toolbar and close prompts menu
  const handleTextareaFocus = useCallback(() => {
    if (toolbarHideTimeoutRef.current) {
      clearTimeout(toolbarHideTimeoutRef.current);
      toolbarHideTimeoutRef.current = null;
    }
    setIsTextareaFocused(true);
    setShowDropup(false);
    // Trigger resume if session is gc_suspended
    handleResumeOnInteraction();
  }, [handleResumeOnInteraction]);

  // Handle textarea blur - hide toolbar with delay to allow button clicks
  const handleTextareaBlur = useCallback((e) => {
    // Check if the new focus target is within the toolbar
    const toolbar = toolbarRef.current;
    if (toolbar && toolbar.contains(e.relatedTarget)) {
      // Don't hide if clicking a toolbar button
      return;
    }
    // Delay hiding to allow button clicks to register
    toolbarHideTimeoutRef.current = setTimeout(() => {
      setIsTextareaFocused(false);
    }, 150);
  }, []);

  // Check if queue is at capacity (only relevant when streaming, as messages get queued)
  const isQueueFull = isStreaming && queueLength >= queueConfig.max_size;

  const handleSubmit = async (e) => {
    e.preventDefault();
    // Allow sending if there's text OR images OR files (or any combination)
    const hasContent =
      text.trim() ||
      pendingImages.some((img) => !img.uploading) ||
      pendingFiles.some((f) => !f.uploading);

    // Check queue capacity when agent is streaming (message will be queued)
    if (isQueueFull) {
      setSendError(
        `Queue is full (${queueConfig.max_size}/${queueConfig.max_size}). Wait for the agent to finish or clear the queue.`,
      );
      setTimeout(() => setSendError(null), 10000);
      return;
    }

    if (isResuming) {
      // Session is resuming — don't send, don't show error (banner is visible)
      return;
    }

    if (hasContent && !disabled && !isReadOnly && !isSending) {
      // Filter out images/files that are still uploading
      const readyImages = pendingImages.filter((img) => !img.uploading);
      const readyFiles = pendingFiles.filter((f) => !f.uploading);
      const messageText = text.trim();

      // When agent is streaming, automatically add to queue instead of sending directly
      // This prevents "prompt already in progress" errors
      if (isStreaming && onAddToQueue) {
        try {
          const result = await onAddToQueue(
            messageText,
            readyImages,
            readyFiles,
          );
          if (result?.success) {
            // Success! Clear the text, images, and files
            setText("");
            setPendingImages([]);
            setPendingFiles([]);
            if (textareaRef.current) {
              textareaRef.current.style.height = "auto";
            }
            // In periodic conversations, hide the composition area after a
            // successful enqueue; the user re-opens it via the Mitto bubble.
            if (periodicConfigured) setIsPromptCollapsed(true);
          }
        } catch (err) {
          console.error("Failed to add to queue:", err);
          setSendError(err.message || "Failed to add to queue");
          setTimeout(() => setSendError(null), 10000);
        }
        return;
      }

      // Store the message for retry capability
      setPendingSendText(messageText);
      setPendingSendImages(readyImages);
      setPendingSendFiles(readyFiles);
      setIsSending(true);
      setSendError(null);

      try {
        // onSend now returns a Promise that resolves on ACK
        await onSend(messageText, readyImages, readyFiles);

        // Success! Clear the text, images, and files
        setText("");
        setPendingImages([]);
        setPendingFiles([]);
        setPendingSendText("");
        setPendingSendImages([]);
        setPendingSendFiles([]);
        if (textareaRef.current) {
          textareaRef.current.style.height = "auto";
        }
        // In periodic conversations, hide the composition area after a
        // successful send; the user re-opens it via the Mitto bubble.
        if (periodicConfigured) setIsPromptCollapsed(true);
      } catch (err) {
        // Failed - show error and keep text for retry
        console.error("Failed to send message:", err);
        setSendError(err.message || "Failed to send message");
        // Auto-clear error after 10 seconds
        setTimeout(() => setSendError(null), 10000);
      } finally {
        setIsSending(false);
      }
    }
  };

  // Handle adding message to queue (with images and files)
  const handleAddToQueueClick = async () => {
    // Allow queueing if there's text OR images OR files (or any combination)
    const hasContent =
      text.trim() ||
      pendingImages.some((img) => !img.uploading) ||
      pendingFiles.some((f) => !f.uploading);

    if (hasContent && onAddToQueue && !disabled && !isReadOnly) {
      // Filter out images/files that are still uploading
      const readyImages = pendingImages.filter((img) => !img.uploading);
      const readyFiles = pendingFiles.filter((f) => !f.uploading);
      const messageText = text.trim();

      try {
        // onAddToQueue returns a Promise that resolves on success
        const result = await onAddToQueue(messageText, readyImages, readyFiles);
        if (result?.success) {
          // Success! Clear the text, images, and files
          setText("");
          setPendingImages([]);
          setPendingFiles([]);
          if (textareaRef.current) {
            textareaRef.current.style.height = "auto";
          }
          // In periodic conversations, hide the composition area after a
          // successful enqueue; the user re-opens it via the Mitto bubble.
          if (periodicConfigured) setIsPromptCollapsed(true);
        }
      } catch (err) {
        console.error("Failed to add to queue:", err);
      }
    }
  };

  // Handle locking the periodic prompt (saves to backend and enables periodic run)
  const handleLockPeriodicPrompt = useCallback(async () => {
    if (!sessionId || !text.trim() || isPeriodicSaving) return;

    setIsPeriodicSaving(true);
    try {
      const response = await secureFetch(
        endpoints.sessions.periodic(sessionId),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ prompt: text.trim(), enabled: true }),
        },
      );
      if (response.ok) {
        const data = await response.json();
        setPeriodicPrompt(text.trim());
        setIsPeriodicLocked(true);
        // Update next scheduled time from server response (keep as ISO string for consistency)
        if (data.next_scheduled_at) {
          setPeriodicNextScheduledAt(data.next_scheduled_at);
        }
      } else {
        console.error("Failed to lock periodic prompt");
      }
    } catch (err) {
      console.error("Failed to lock periodic prompt:", err);
    } finally {
      setIsPeriodicSaving(false);
    }
  }, [sessionId, text, isPeriodicSaving]);

  // Handle unlocking the periodic prompt (allows editing and disables periodic run)
  const handleUnlockPeriodicPrompt = useCallback(async () => {
    if (!sessionId || isPeriodicSaving) return;

    setIsPeriodicSaving(true);
    try {
      const response = await secureFetch(
        endpoints.sessions.periodic(sessionId),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ enabled: false }),
        },
      );
      if (response.ok) {
        setIsPeriodicLocked(false);
        setPeriodicNextScheduledAt(null); // Clear next scheduled time when disabled
        // Focus the textarea so user can start editing
        if (textareaRef.current) {
          textareaRef.current.focus();
        }
      } else {
        console.error("Failed to unlock periodic prompt");
      }
    } catch (err) {
      console.error("Failed to unlock periodic prompt:", err);
    } finally {
      setIsPeriodicSaving(false);
    }
  }, [sessionId, isPeriodicSaving]);

  // Handle periodic prompt selection from PeriodicPromptSelector
  const handlePeriodicPromptSelect = useCallback(async (promptName) => {
    if (!sessionId || isPeriodicSaving) return;

    // Helper that performs the actual PATCH, optionally with arguments.
    const doPatch = async (extraArgs) => {
      setIsPeriodicSaving(true);
      try {
        const body = { prompt_name: promptName, enabled: true };
        if (extraArgs && Object.keys(extraArgs).length > 0) {
          body.arguments = extraArgs;
        }
        const response = await secureFetch(
          endpoints.sessions.periodic(sessionId),
          {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          },
        );
        if (response.ok) {
          const data = await response.json();
          setPeriodicPromptName(promptName);
          setIsPeriodicLocked(true);
          if (data.next_scheduled_at) {
            setPeriodicNextScheduledAt(data.next_scheduled_at);
          }
        }
      } catch (err) {
        console.error("Failed to save periodic prompt selection:", err);
      } finally {
        setIsPeriodicSaving(false);
      }
    };

    // Check if the prompt declares parameters that need user input before saving.
    const fullPrompt = periodicPrompts.find((p) => p.name === promptName);
    let missing = fullPrompt ? getMissingPromptParameters(fullPrompt, "conversation") : [];
    if (missing.length > 0 && sessionId && fullPrompt) {
      const cached = await fetchCachedParamNames(sessionId, fullPrompt.name);
      missing = effectiveMissingParams(missing, cached);
    }
    if (missing.length > 0 && onOpenPromptParamDialog) {
      onOpenPromptParamDialog(fullPrompt, missing, async (userArgs) => {
        await doPatch(userArgs);
      });
      return;
    }

    await doPatch(undefined);
  }, [sessionId, isPeriodicSaving, periodicPrompts, onOpenPromptParamDialog]);

  // Handle frequency change from the PeriodicFrequencyPanel
  const handlePeriodicFrequencyChange = useCallback(
    (newFrequency, newNextScheduledAt) => {
      setPeriodicFrequency(newFrequency);
      if (newNextScheduledAt) {
        setPeriodicNextScheduledAt(newNextScheduledAt);
      }
    },
    [],
  );

  // Handle max iterations change from the PeriodicFrequencyPanel
  const handlePeriodicMaxIterationsChange = useCallback((newValue) => {
    setPeriodicMaxIterations(newValue);
  }, []);

  // Handle pause/resume toggle from the PeriodicFrequencyPanel
  const handlePeriodicEnabledChange = useCallback((newEnabled) => {
    setIsPeriodicLocked(newEnabled);
    if (!newEnabled) {
      setPeriodicNextScheduledAt(null);
    }
  }, []);

  // Handle slash command selection
  const handleSlashCommandSelect = useCallback(
    (command) => {
      setText(`/${command.name} `);
      setShowSlashPicker(false);
      setSlashSelectedIndex(0);
      // Focus textarea and move cursor to end
      if (textareaRef.current) {
        textareaRef.current.focus();
        const len = `/${command.name} `.length;
        textareaRef.current.setSelectionRange(len, len);
      }
    },
    [setText],
  );

  const handleKeyDown = (e) => {
    // Handle slash command picker keyboard navigation
    if (showSlashPicker && filteredSlashCommands.length > 0) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSlashSelectedIndex((prev) =>
          prev < filteredSlashCommands.length - 1 ? prev + 1 : prev,
        );
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setSlashSelectedIndex((prev) => (prev > 0 ? prev - 1 : prev));
        return;
      }
      if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
        e.preventDefault();
        const selectedCommand = filteredSlashCommands[slashSelectedIndex];
        if (selectedCommand) {
          handleSlashCommandSelect(selectedCommand);
        }
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setShowSlashPicker(false);
        return;
      }
    }

    // Handle Enter key based on sendKeyMode
    if (e.key === "Enter") {
      const hasCtrlOrCmd = e.metaKey || e.ctrlKey;

      if (sendKeyMode === "ctrl-enter") {
        // Ctrl/Cmd+Enter mode:
        // - Ctrl/Cmd+Shift+Enter: add to queue
        // - Ctrl/Cmd+Enter (no shift): send message
        // - Enter alone: new line (default textarea behavior)
        if (hasCtrlOrCmd && e.shiftKey) {
          e.preventDefault();
          handleAddToQueueClick();
          return;
        }
        if (hasCtrlOrCmd && !e.shiftKey) {
          e.preventDefault();
          handleSubmit(e);
          return;
        }
        // Plain Enter - let it create a new line (don't prevent default)
      } else {
        // Default "enter" mode:
        // - Cmd/Ctrl+Enter: add to queue
        // - Enter (no modifiers): send message
        // - Shift+Enter: new line (don't prevent default)
        if (hasCtrlOrCmd && !e.shiftKey) {
          e.preventDefault();
          handleAddToQueueClick();
          return;
        }
        if (!e.shiftKey && !hasCtrlOrCmd) {
          e.preventDefault();
          handleSubmit(e);
          return;
        }
        // Shift+Enter - let it create a new line (don't prevent default)
      }
    }
    // Close dropup on Escape
    if (e.key === "Escape") {
      setShowDropup(false);
      setShowSlashPicker(false);
    }
    // Ctrl+P to improve prompt (magic wand)
    if (e.ctrlKey && e.key === "p") {
      e.preventDefault();
      handleImprovePrompt();
    }
  };

  const handleInput = (e) => {
    const newValue = e.target.value;
    setText(newValue);
    const textarea = e.target;
    textarea.style.height = "auto";
    textarea.style.height =
      Math.max(textareaMinHeight, Math.min(textarea.scrollHeight, textareaHardMax)) + "px";

    // Show slash command picker when typing '/' at the start
    if (
      newValue.startsWith("/") &&
      availableCommands.length > 0 &&
      !newValue.includes(" ")
    ) {
      setShowSlashPicker(true);
    } else {
      setShowSlashPicker(false);
    }
  };

  const handlePredefinedPrompt = async (prompt, event) => {
    setShowDropup(false);

    // Shift+click/Enter = insert into composition area (legacy behavior)
    if (event && event.shiftKey) {
      const textarea = textareaRef.current;
      if (textarea) {
        // Get cursor position and insert prompt text at that position
        const start = textarea.selectionStart;
        const end = textarea.selectionEnd;
        const newText =
          text.substring(0, start) + prompt.prompt + text.substring(end);
        setText(newText);

        // Set cursor position after inserted text and adjust textarea height
        requestAnimationFrame(() => {
          const newCursorPos = start + prompt.prompt.length;
          textarea.selectionStart = newCursorPos;
          textarea.selectionEnd = newCursorPos;
          textarea.focus();
          // Adjust height to fit content
          textarea.style.height = "auto";
          textarea.style.height =
            Math.max(textareaMinHeight, Math.min(textarea.scrollHeight, textareaHardMax)) + "px";
        });
      }
      return;
    }

    // Periodic-flagged prompts: route to app-level branching (decidePeriodicAction).
    // This handles make-periodic / one-shot / new-periodic without duplicating logic here.
    if (prompt && prompt.periodic && onPeriodicPrompt) {
      onPeriodicPrompt(prompt);
      return;
    }

    // When agent is streaming, queue the prompt instead of sending immediately
    if (isStreaming && onAddToQueue && prompt.name) {
      if (isQueueFull) {
        setSendError(
          `Queue is full (${queueConfig.max_size}/${queueConfig.max_size}). Wait for the agent to finish or clear the queue.`,
        );
        setTimeout(() => setSendError(null), 10000);
        return;
      }
      try {
        await onAddToQueue("", [], [], { promptName: prompt.name });
      } catch (err) {
        console.error("Failed to add to queue:", err);
        setSendError(err.message || "Failed to add to queue");
        setTimeout(() => setSendError(null), 10000);
      }
      return;
    }

    // Default: send prompt immediately by name.
    // When the prompt declares parameters the "prompts" menu cannot auto-fill,
    // open the parameter dialog so the user can supply them.  On submit the
    // options.arguments map is passed to onSend, which routes through the queue
    // API so the backend can apply ${VAR} substitution.
    if (onSend && prompt.name) {
      let missing = getMissingPromptParameters(prompt, "prompts");
      if (missing.length > 0 && sessionId) {
        const cached = await fetchCachedParamNames(sessionId, prompt.name);
        missing = effectiveMissingParams(missing, cached);
      }
      if (missing.length > 0 && onOpenPromptParamDialog) {
        onOpenPromptParamDialog(prompt, missing, async (userArgs) => {
          onSend("", [], [], { promptName: prompt.name, arguments: userArgs });
        });
        return;
      }
      onSend("", [], [], { promptName: prompt.name });
    }
  };

  const handleImprovePrompt = async () => {
    if (!text.trim() || isImproving) return;

    // Capture the current sessionId - this is the session the improvement is for
    const targetSessionId = sessionId;
    const controller = new AbortController();

    // Track that this session has an active improve request
    improvingSessionsRef.current.set(targetSessionId, {
      abortController: controller,
    });
    setImprovingVersion((v) => v + 1); // Force re-render
    setImproveError(null);

    try {
      const timeoutId = setTimeout(() => controller.abort(), 65000); // 65s timeout

      const response = await secureFetch(endpoints.aux.improvePrompt(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          prompt: text,
          workspace_uuid:
            window.mittoCurrentWorkspaceUUID ||
            sessionStorage.getItem("mittoCurrentWorkspaceUUID") ||
            "",
        }),
        signal: controller.signal,
      });

      clearTimeout(timeoutId);

      if (!response.ok) {
        const errData = await response.json().catch(() => ({}));
        throw new Error(errorMessageFromData(errData, "Failed to improve prompt"));
      }

      const data = await response.json();
      if (data.improved_prompt && onDraftChange) {
        onDraftChange(targetSessionId, data.improved_prompt);
        if (targetSessionId === sessionId) {
          requestAnimationFrame(() => {
            const textarea = textareaRef.current;
            if (textarea) {
              textarea.style.height = "auto";
              textarea.style.height =
                Math.max(textareaMinHeight, Math.min(textarea.scrollHeight, textareaHardMax)) + "px";
              textarea.focus();
            }
          });
        }
      }
    } catch (err) {
      console.error("Failed to improve prompt:", err);
      // Only show error if we're still on the session that had the error
      if (targetSessionId === sessionId) {
        if (err.name === "AbortError") {
          setImproveError("Request timed out. Please try again.");
        } else {
          const msg = err.message || "Failed to improve prompt";
          const hasCrashHint = msg.includes("crashed") || msg.includes("try again");
          setImproveError(hasCrashHint ? msg : msg + " \u2014 please try again.");
        }
        setTimeout(() => setImproveError(null), 5000);
      }
    } finally {
      // Remove this session from the improving map
      improvingSessionsRef.current.delete(targetSessionId);
      setImprovingVersion((v) => v + 1); // Force re-render
    }
  };

  const getPlaceholder = () => {
    if (noSession) return "Create a new conversation to start chatting...";
    if (isArchivePending)
      return "Archiving... waiting for agent to finish responding.";
    if (isArchived)
      return "This conversation is archived. Unarchive to send messages.";
    if (isReadOnly)
      return "This is a read-only session. Create a new session to chat.";
    if (isSending) return "Sending message...";
    if (gcSuspended && !acpReady) return "Click or type to resume session...";
    if (!acpReady) return "Waiting for AI agent to connect...";
    if (isQueueFull)
      return `Queue full (${queueConfig.max_size}/${queueConfig.max_size})...`;
    if (isStreaming) {
      return "Agent responding... (messages will be queued)";
    }
    if (isImproving) return "Improving prompt...";
    if (isDragOver) return "Drop files here...";
    return isSmallWindow
      ? "Type your message..."
      : "Type your message... (drop or paste images/files)";
  };

  // Upload an image file to the session
  const uploadImage = async (file) => {
    if (!sessionId) return null;

    if (!agentSupportsImages) {
      setUploadError("⚠️ This agent may not support images — attaching anyway");
      setTimeout(() => setUploadError(null), 5000);
    }

    const validTypes = ["image/png", "image/jpeg", "image/gif", "image/webp"];
    if (!validTypes.includes(file.type)) {
      setUploadError("Only PNG, JPEG, GIF, and WebP images are supported");
      setTimeout(() => setUploadError(null), 5000);
      return null;
    }

    if (file.size > 10 * 1024 * 1024) {
      setUploadError("Image exceeds 10MB limit");
      setTimeout(() => setUploadError(null), 5000);
      return null;
    }

    const tempId = `temp_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
    const previewUrl = URL.createObjectURL(file);
    const tempImage = {
      id: tempId,
      url: previewUrl,
      name: file.name,
      mimeType: file.type,
      uploading: true,
    };
    setPendingImages((prev) => [...prev, tempImage]);

    try {
      const formData = new FormData();
      formData.append("image", file);

      const response = await secureFetch(
        endpoints.sessions.images(sessionId),
        {
          method: "POST",
          body: formData,
        },
      );

      if (!response.ok) {
        const error = await response.json();
        throw new Error(errorMessageFromData(error, "Failed to upload image"));
      }

      const data = await response.json();
      setPendingImages((prev) =>
        prev.map((img) =>
          img.id === tempId
            ? {
                id: data.id,
                url: apiUrl(data.url),
                name: data.name,
                mimeType: data.mime_type,
                uploading: false,
              }
            : img,
        ),
      );
      URL.revokeObjectURL(previewUrl);
      return data;
    } catch (err) {
      console.error("Failed to upload image:", err);
      setUploadError(err.message || "Failed to upload image");
      setTimeout(() => setUploadError(null), 5000);
      setPendingImages((prev) => prev.filter((img) => img.id !== tempId));
      URL.revokeObjectURL(previewUrl);
      return null;
    }
  };

  // Upload images from file paths (for native macOS app)
  const uploadImagesFromPaths = async (paths) => {
    if (!sessionId || !paths || paths.length === 0) return [];

    const tempImages = paths.map((path) => {
      const filename = path.split("/").pop() || "image";
      const tempId = `temp_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
      return { id: tempId, filename, path };
    });

    tempImages.forEach(({ id, filename }) => {
      setPendingImages((prev) => [
        ...prev,
        { id, url: "", name: filename, mimeType: "", uploading: true },
      ]);
    });

    try {
      const response = await secureFetch(
        endpoints.sessions.imagesFromPath(sessionId),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ paths }),
        },
      );

      if (!response.ok) {
        const error = await response.json();
        throw new Error(errorMessageFromData(error, "Failed to upload images"));
      }

      const results = await response.json();
      const tempIds = tempImages.map((t) => t.id);
      setPendingImages((prev) =>
        prev.filter((img) => !tempIds.includes(img.id)),
      );

      for (const data of results) {
        setPendingImages((prev) => [
          ...prev,
          {
            id: data.id,
            url: apiUrl(data.url),
            name: data.name,
            mimeType: data.mime_type,
            uploading: false,
          },
        ]);
      }
      return results;
    } catch (err) {
      console.error("Failed to upload images from paths:", err);
      setUploadError(err.message || "Failed to upload images");
      setTimeout(() => setUploadError(null), 5000);
      const tempIds = tempImages.map((t) => t.id);
      setPendingImages((prev) =>
        prev.filter((img) => !tempIds.includes(img.id)),
      );
      return [];
    }
  };

  // Upload a single file (non-image)
  const uploadFile = async (file) => {
    const tempId = `temp_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
    const tempFile = {
      id: tempId,
      name: file.name,
      mimeType: file.type || "application/octet-stream",
      size: file.size,
      category: "text", // Will be updated from server response
      uploading: true,
    };
    setPendingFiles((prev) => [...prev, tempFile]);

    try {
      const formData = new FormData();
      formData.append("file", file);

      const response = await secureFetch(
        endpoints.sessions.files(sessionId),
        { method: "POST", body: formData },
      );

      if (!response.ok) {
        const error = await response.json();
        throw new Error(errorMessageFromData(error, "Failed to upload file"));
      }

      const data = await response.json();
      setPendingFiles((prev) =>
        prev.map((f) =>
          f.id === tempId
            ? {
                id: data.id,
                name: data.name,
                mimeType: data.mime_type,
                size: data.size,
                category: data.category,
                uploading: false,
              }
            : f,
        ),
      );
      return data;
    } catch (err) {
      console.error("Failed to upload file:", err);
      setUploadError(err.message || "Failed to upload file");
      setTimeout(() => setUploadError(null), 5000);
      setPendingFiles((prev) => prev.filter((f) => f.id !== tempId));
      return null;
    }
  };

  // Upload files from file paths (native macOS app)
  const uploadFilesFromPaths = async (paths) => {
    const tempFiles = paths.map((path, index) => {
      const filename = path.split("/").pop();
      const tempId = `temp_${Date.now()}_${index}_${Math.random().toString(36).substr(2, 9)}`;
      return { id: tempId, filename, path };
    });

    tempFiles.forEach(({ id, filename }) => {
      setPendingFiles((prev) => [
        ...prev,
        {
          id,
          name: filename,
          mimeType: "",
          size: 0,
          category: "text",
          uploading: true,
        },
      ]);
    });

    try {
      const response = await secureFetch(
        endpoints.sessions.filesFromPath(sessionId),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ paths }),
        },
      );

      if (!response.ok) {
        const error = await response.json();
        throw new Error(errorMessageFromData(error, "Failed to upload files"));
      }

      const results = await response.json();
      const tempIds = tempFiles.map((t) => t.id);
      setPendingFiles((prev) => prev.filter((f) => !tempIds.includes(f.id)));

      for (const data of results) {
        setPendingFiles((prev) => [
          ...prev,
          {
            id: data.id,
            name: data.name,
            mimeType: data.mime_type,
            size: data.size,
            category: data.category,
            uploading: false,
          },
        ]);
      }
      return results;
    } catch (err) {
      console.error("Failed to upload files from paths:", err);
      setUploadError(err.message || "Failed to upload files");
      setTimeout(() => setUploadError(null), 5000);
      const tempIds = tempFiles.map((t) => t.id);
      setPendingFiles((prev) => prev.filter((f) => !tempIds.includes(f.id)));
      return [];
    }
  };

  // Remove a pending file
  const removeFile = (fileId) => {
    setPendingFiles((prev) => prev.filter((f) => f.id !== fileId));
  };

  // Handle attach image button click - uses native picker on macOS, file input otherwise
  const handleAttachImageClick = async () => {
    if (hasNativeImagePicker()) {
      const paths = await pickImages();
      if (paths && paths.length > 0) {
        await uploadImagesFromPaths(paths);
      }
    } else {
      if (imageInputRef.current) {
        imageInputRef.current.click();
      }
    }
  };

  // Handle attach file button click - uses native picker on macOS, file input otherwise
  const handleAttachFileClick = async () => {
    if (hasNativeFilePicker()) {
      const paths = await pickFiles();
      if (paths && paths.length > 0) {
        await uploadFilesFromPaths(paths);
      }
    } else {
      if (fileInputRef.current) {
        fileInputRef.current.click();
      }
    }
  };

  /**
   * Extract file paths from drag event data.
   * On macOS native app, files dragged from Finder include file:// URLs.
   * @param {DataTransfer} dataTransfer - The drag event's dataTransfer object
   * @returns {string[]} Array of absolute file paths, or empty array if none found
   */
  const extractFilePathsFromDrag = (dataTransfer) => {
    // Try to get file URLs from text/uri-list (macOS Finder drag provides this)
    const uriList = dataTransfer.getData("text/uri-list");
    if (uriList) {
      const paths = uriList
        .split(/[\r\n]+/)
        .filter((line) => line && !line.startsWith("#")) // Filter empty lines and comments
        .map((uri) => {
          // Convert file:// URL to path
          if (uri.startsWith("file://")) {
            try {
              const url = new URL(uri);
              // decodeURIComponent handles spaces and special characters
              return decodeURIComponent(url.pathname);
            } catch {
              return null;
            }
          }
          return null;
        })
        .filter(Boolean);
      if (paths.length > 0) {
        return paths;
      }
    }
    return [];
  };

  /**
   * Check if a file path is inside the workspace directory.
   * @param {string} filePath - Absolute file path
   * @param {string} workspacePath - Workspace directory path
   * @returns {string|null} Relative path if inside workspace, null otherwise
   */
  const getRelativePathIfInWorkspace = (filePath, workspacePath) => {
    if (!filePath || !workspacePath) return null;
    // Normalize paths (remove trailing slashes)
    const normalizedFile = filePath.replace(/\/+$/, "");
    const normalizedWorkspace = workspacePath.replace(/\/+$/, "");
    // Check if file is inside workspace
    if (normalizedFile.startsWith(normalizedWorkspace + "/")) {
      // Return relative path (without leading slash)
      return normalizedFile.slice(normalizedWorkspace.length + 1);
    }
    return null;
  };

  /**
   * Insert text at the current cursor position in the textarea.
   * @param {string} textToInsert - Text to insert
   */
  const insertTextAtCursor = (textToInsert) => {
    const textarea = internalRef.current;
    if (!textarea) return;

    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const currentText = text;

    // Build new text with insertion
    const newText =
      currentText.slice(0, start) + textToInsert + currentText.slice(end);
    setText(newText);

    // Set cursor position after inserted text (after React re-render)
    requestAnimationFrame(() => {
      textarea.focus();
      const newCursorPos = start + textToInsert.length;
      textarea.setSelectionRange(newCursorPos, newCursorPos);
    });
  };

  // Handle file drop - supports both images and other files
  // On native macOS app, files dropped from within the workspace are inserted as relative paths
  const handleDrop = async (e) => {
    e.preventDefault();
    setIsDragOver(false);
    if (isFullyDisabled || isReadOnly || !sessionId) return;

    // Smart path insertion for native macOS app
    // When dropping files from the current workspace, insert relative path instead of uploading
    if (isNativeApp() && workingDir) {
      const filePaths = extractFilePathsFromDrag(e.dataTransfer);
      if (filePaths.length > 0) {
        // Check if all dropped files are within the workspace
        const relativePaths = filePaths
          .map((fp) => getRelativePathIfInWorkspace(fp, workingDir))
          .filter(Boolean);

        // If we found relative paths for ALL files, insert them as text
        if (
          relativePaths.length === filePaths.length &&
          relativePaths.length > 0
        ) {
          // Insert relative paths, separated by spaces if multiple
          const pathsText = relativePaths.join(" ");
          insertTextAtCursor(pathsText);
          return; // Don't upload, we've handled the drop
        }
        // If some files are outside workspace, fall through to upload behavior
      }
    }

    const files = Array.from(e.dataTransfer.files);
    const imageFiles = files.filter((f) => f.type.startsWith("image/"));
    const otherFiles = files.filter((f) => !f.type.startsWith("image/"));

    // Upload images
    for (const file of imageFiles) {
      await uploadImage(file);
    }

    // Upload other files
    for (const file of otherFiles) {
      await uploadFile(file);
    }
  };

  const handleDragOver = (e) => {
    e.preventDefault();
    if (!isFullyDisabled && !isReadOnly && sessionId) {
      setIsDragOver(true);
    }
  };

  const handleDragLeave = (e) => {
    e.preventDefault();
    setIsDragOver(false);
  };

  // Handle paste (for clipboard images)
  const handlePaste = async (e) => {
    if (isFullyDisabled || isReadOnly || !sessionId) return;

    const items = Array.from(e.clipboardData.items);
    const imageItems = items.filter((item) => item.type.startsWith("image/"));

    if (imageItems.length > 0) {
      if (!agentSupportsImages) {
        setUploadError(
          "⚠️ This agent may not support images — attaching anyway",
        );
        setTimeout(() => setUploadError(null), 5000);
      }
      e.preventDefault();
      for (const item of imageItems) {
        const file = item.getAsFile();
        if (file) {
          await uploadImage(file);
        }
      }
    }
  };

  // Remove a pending image
  const removeImage = (imageId) => {
    setPendingImages((prev) => {
      const img = prev.find((i) => i.id === imageId);
      if (img && img.url.startsWith("blob:")) {
        URL.revokeObjectURL(img.url);
      }
      return prev.filter((i) => i.id !== imageId);
    });
  };

  // Handle image input change (for image file picker)
  const handleImageInputChange = async (e) => {
    const files = Array.from(e.target.files);
    for (const file of files) {
      await uploadImage(file);
    }
    e.target.value = "";
  };

  // Handle file input change (for general file picker)
  const handleFileInputChange = async (e) => {
    const files = Array.from(e.target.files);
    for (const file of files) {
      // Route images to image upload, others to file upload
      if (file.type.startsWith("image/")) {
        await uploadImage(file);
      } else {
        await uploadFile(file);
      }
    }
    e.target.value = "";
  };

  const hasPrompts = predefinedPrompts && predefinedPrompts.length > 0;
  const hasPendingImages = pendingImages.length > 0;
  const hasPendingFiles = pendingFiles.length > 0;
  const hasPendingAttachments = hasPendingImages || hasPendingFiles;
  const hasActionButtons = actionButtons && actionButtons.length > 0;
  // UI prompts from MCP tools should be shown WHILE streaming (the tool is waiting for user input)
  const hasActiveUIPrompt = !!activeUIPrompt;

  // Handle UI prompt answer click
  const handleUIPromptAnswer = useCallback(
    (optionId, label, freeText = "") => {
      if (activeUIPrompt && onUIPromptAnswer) {
        console.log("[UIPrompt] User clicked:", {
          requestId: activeUIPrompt.requestId,
          optionId,
          label,
          freeText,
        });
        // Immediately expand the prompt area (don't wait for dismiss from backend)
        setIsPromptCollapsed(prevCollapsedBeforeUIRef.current);
        onUIPromptAnswer(activeUIPrompt.requestId, optionId, label, freeText);
      }
    },
    [activeUIPrompt, onUIPromptAnswer],
  );

  // Stop the agent turn from within an active MCP UI prompt panel. Aborts the
  // pending prompt (so the blocking tool call resolves) and stops streaming.
  const handleUIPromptStop = useCallback(() => {
    if (hasActiveUIPrompt) {
      handleUIPromptAnswer("abort", "Abort");
    }
    if (onCancel) onCancel();
  }, [hasActiveUIPrompt, handleUIPromptAnswer, onCancel]);

  // Debug logging for action buttons — only log when buttons actually change
  useEffect(() => {
    if (actionButtons && actionButtons.length > 0) {
      console.log("[ActionButtons] ChatInput received buttons:", {
        count: actionButtons.length,
        labels: actionButtons.map((b) => b.label),
      });
    }
  }, [actionButtons]);

  // Handle action button click - populate the textarea with the response text
  const handleActionButtonClick = useCallback(
    (response) => {
      setText(response);
      // Focus the textarea and adjust height
      requestAnimationFrame(() => {
        const textarea = textareaRef.current;
        if (textarea) {
          textarea.focus();
          textarea.style.height = "auto";
          textarea.style.height =
            Math.max(textareaMinHeight, Math.min(textarea.scrollHeight, textareaHardMax)) + "px";
        }
      });
    },
    [setText, textareaMinHeight, textareaHardMax],
  );

  return html`
    <form
      onSubmit=${handleSubmit}
      onDrop=${handleDrop}
      onDragOver=${handleDragOver}
      onDragLeave=${handleDragLeave}
      class="px-4 pt-0 pb-3 bg-mitto-input border-t border-mitto-border-1 shrink-0 relative ${isDragOver
        ? "ring-2 ring-mitto-accent-500 ring-inset"
        : ""}"
    >
      <!-- Resize handle for ChatInput height -->
      <div
        class="flex items-center justify-center h-2 cursor-ns-resize hover:bg-mitto-surface-4/30 transition-colors select-none touch-none ${isTextareaDragging ? 'bg-mitto-surface-4/30' : ''}"
        ...${textareaHandleProps}
        title="Drag to resize input area"
      >
        <div class="w-8 h-0.5 rounded-full bg-mitto-surface-4 ${isTextareaDragging ? 'bg-slate-400' : ''}"></div>
      </div>
      <!-- Hidden file input for images -->
      <input
        ref=${imageInputRef}
        type="file"
        accept="image/png,image/jpeg,image/gif,image/webp"
        multiple
        class="hidden"
        onChange=${handleImageInputChange}
      />
      <!-- Hidden file input for general files -->
      <input
        ref=${fileInputRef}
        type="file"
        multiple
        class="hidden"
        onChange=${handleFileInputChange}
      />

      <!-- UI Prompt from MCP tool (unified menu or permission) -->
      ${hasActiveUIPrompt &&
      html`
        <div class="max-w-4xl mx-auto mb-3">
          ${
            /* Permission prompts keep original button-based rendering */
            activeUIPrompt.promptType === "permission"
              ? html`
                  <div
                    class="ui-prompt-panel p-4 rounded-lg border border-amber-500/50 shadow-lg"
                  >
                    ${activeUIPrompt.title &&
                    html`
                      <div class="mb-2">
                        <span
                          class="text-xs font-medium text-amber-400 uppercase tracking-wide"
                          >Permission Required</span
                        >
                      </div>
                      <p
                        class="text-sm font-mono bg-mitto-surface-2/50 p-2 rounded mb-3 break-all"
                      >
                        ${activeUIPrompt.title}
                      </p>
                    `}
                    <div class="flex flex-wrap gap-2">
                      ${activeUIPrompt.options?.map((opt) => {
                        const kind = opt.kind || "";
                        let colorClass;
                        if (
                          kind === "allow_once" ||
                          kind === "allow_always" ||
                          opt.style === "success"
                        ) {
                          colorClass = "btn-success";
                        } else if (
                          kind === "reject_once" ||
                          opt.style === "danger"
                        ) {
                          colorClass = "btn-error";
                        } else {
                          colorClass = "btn-ghost";
                        }
                        return html`
                          <button
                            key=${opt.id}
                            type="button"
                            onClick=${() =>
                              handleUIPromptAnswer(opt.id, opt.label)}
                            class="btn btn-sm ${colorClass}"
                          >
                            ${opt.label}
                          </button>
                        `;
                      })}
                    </div>
                  </div>
                `
              : activeUIPrompt.promptType === "textbox"
                ? html`
                    <!-- Textbox editor for mitto_ui_textbox -->
                    <div
                      class="ui-prompt-panel rounded-lg border border-mitto-accent-500/50 shadow-lg overflow-hidden flex flex-col"
                      style="max-height: ${uiPromptHeight}px;"
                    >
                      <!-- Resize handle at top edge -->
                      <div
                        class="flex items-center justify-center py-1 cursor-ns-resize hover:bg-mitto-surface-4/50 transition-colors select-none touch-none shrink-0 ${isPromptDragging
                          ? "bg-mitto-surface-4/50"
                          : ""}"
                        ...${promptHandleProps}
                        title="Drag to resize"
                      >
                        <${GripIcon} className="w-6 h-1.5 text-mitto-text-muted" />
                      </div>

                      <!-- Title -->
                      <div class="px-4 pt-2 pb-2 shrink-0">
                        <p class="ui-prompt-question text-sm font-medium" style="white-space: pre-wrap">
                          ${(activeUIPrompt.title || activeUIPrompt.question)?.replace(/\\n/g, '\n')}
                        </p>
                      </div>

                      <!-- Textarea (auto-sizes to content; scrolls when capped) -->
                      <div class="px-4 pb-2 flex-1 min-h-0 overflow-y-auto">
                        <textarea
                          ref=${textboxRef}
                          autocorrect="off"
                          class="ui-textbox-textarea textarea textarea-sm font-mono w-full resize-none"
                          style="min-height: 120px;"
                          maxlength=${16384}
                          onInput=${(e) => {
                            setTextboxValue(e.target.value);
                            e.target.style.height = "auto";
                            e.target.style.height = e.target.scrollHeight + "px";
                          }}
                        >
${activeUIPrompt.text || ""}</textarea
                        >
                      </div>

                      <!-- Counter + Buttons on same row -->
                      <div
                        class="flex items-center justify-between px-4 pt-2 pb-3 shrink-0"
                      >
                        <span
                          class="text-xs ${textboxValue.length > 15000
                            ? "text-amber-400"
                            : "text-mitto-text-muted"}"
                        >
                          ${textboxValue.length > 15000
                            ? "⚠ "
                            : ""}${textboxValue.length.toLocaleString()}
                          / 16,384
                        </span>
                        <div class="flex gap-2 items-center">
                          <${PromptStopButton} onStop=${handleUIPromptStop} />
                          <button
                            type="button"
                            onClick=${() =>
                              handleUIPromptAnswer("abort", "Abort")}
                            class="btn btn-ghost btn-sm"
                          >
                            Abort
                          </button>
                          <button
                            type="button"
                            onClick=${() =>
                              handleUIPromptAnswer(
                                "submit",
                                "Submit",
                                textboxValue,
                              )}
                            class="btn btn-primary btn-sm"
                          >
                            Submit
                          </button>
                        </div>
                      </div>
                    </div>
                  `
                : activeUIPrompt.promptType === "form"
                  ? html`
                      <!-- HTML Form for mitto_ui_form -->
                      <div
                        class="ui-prompt-panel rounded-lg border border-mitto-accent-500/50 shadow-lg overflow-hidden flex flex-col"
                        style="max-height: ${uiPromptHeight}px;"
                      >
                        <!-- Resize handle at top edge -->
                        <div
                          class="flex items-center justify-center py-1 cursor-ns-resize hover:bg-mitto-surface-4/50 transition-colors select-none touch-none shrink-0 ${isPromptDragging
                            ? "bg-mitto-surface-4/50"
                            : ""}"
                          ...${promptHandleProps}
                          title="Drag to resize"
                        >
                          <${GripIcon} className="w-6 h-1.5 text-mitto-text-muted" />
                        </div>

                        <!-- Title -->
                        <div class="px-4 pt-2 pb-2 shrink-0">
                          <p class="ui-prompt-question text-sm font-medium" style="white-space: pre-wrap">
                            ${(activeUIPrompt.title || activeUIPrompt.question)?.replace(/\\n/g, '\n')}
                          </p>
                        </div>

                        <!-- Sanitized HTML form content (scrollable) -->
                        <div
                          class="ui-form-content px-4 pb-2 flex-1 min-h-0 overflow-y-auto"
                          ref=${(el) => {
                            if (
                              el &&
                              activeUIPrompt.formHTML &&
                              !el.dataset.formInitialized
                            ) {
                              el.innerHTML = activeUIPrompt.formHTML;
                              el.dataset.formInitialized = "true";
                              wireMittoFileMarkers(el);
                            }
                          }}
                        ></div>

                        <!-- Submit / Cancel / Toggle buttons -->
                        <div
                          class="flex items-center justify-end gap-2 px-4 pt-2 pb-3 shrink-0"
                        >
                          <div class="flex gap-2 items-center">
                            <${PromptStopButton} onStop=${handleUIPromptStop} />
                            <button
                              type="button"
                              onClick=${() =>
                                handleUIPromptAnswer("cancel", "Cancel", "")}
                              class="btn btn-ghost btn-sm"
                            >
                              Cancel
                            </button>
                            <button
                              type="button"
                              onClick=${(e) => {
                                // Find the form container and extract all named field values
                                const container = e.target
                                  .closest(".ui-prompt-panel")
                                  ?.querySelector(".ui-form-content");
                                if (!container) return;
                                const values = {};
                                container
                                  .querySelectorAll(
                                    "input[name], select[name], textarea[name]",
                                  )
                                  .forEach((el) => {
                                    const name = el.name;
                                    if (!name) return;
                                    if (el.type === "checkbox") {
                                      values[name] = el.checked
                                        ? "true"
                                        : "false";
                                    } else if (el.type === "radio") {
                                      if (el.checked) values[name] = el.value;
                                    } else {
                                      values[name] = el.value;
                                    }
                                  });
                                handleUIPromptAnswer(
                                  "submit",
                                  "Submit",
                                  JSON.stringify(values),
                                );
                              }}
                              class="btn btn-primary btn-sm"
                            >
                              Submit
                            </button>
                          </div>
                        </div>
                      </div>
                    `
                  : html`
                      <!-- Unified Claude Code-style menu for yes_no, options_buttons, select -->
                      <div
                        class="ui-prompt-panel rounded-lg border border-mitto-accent-500/50 shadow-lg overflow-hidden flex flex-col"
                        style="max-height: ${uiPromptHeight}px;"
                      >
                        <!-- Resize handle at top edge -->
                        <div
                          class="flex items-center justify-center py-1 cursor-ns-resize hover:bg-mitto-surface-4/50 transition-colors select-none touch-none shrink-0 ${isPromptDragging
                            ? "bg-mitto-surface-4/50"
                            : ""}"
                          ...${promptHandleProps}
                          title="Drag to resize"
                        >
                          <${GripIcon} className="w-6 h-1.5 text-mitto-text-muted" />
                        </div>

                        <!-- Question -->
                        <div class="px-4 pt-2 pb-2 shrink-0">
                          <p class="ui-prompt-question text-sm font-medium" style="white-space: pre-wrap">
                            ${activeUIPrompt.question?.replace(/\\n/g, '\n')}
                          </p>
                        </div>

                        <!-- Options list (scrollable) -->
                        <div
                          class="divide-y divide-slate-700/50 flex-1 min-h-0 overflow-y-auto"
                        >
                          ${activeUIPrompt.options?.map(
                            (opt, idx) => html`
                              <button
                                key=${opt.id}
                                type="button"
                                onClick=${() =>
                                  handleUIPromptAnswer(opt.id, opt.label)}
                                class="w-full text-left px-4 py-3 hover:bg-mitto-surface-3/50 transition-colors flex items-start gap-3 group"
                              >
                                <span
                                  class="shrink-0 w-6 h-6 rounded flex items-center justify-center text-xs font-bold ${idx ===
                                  0
                                    ? "bg-mitto-accent text-mitto-accent-fg"
                                    : "bg-mitto-surface-4/80 text-mitto-text-secondary group-hover:bg-slate-500"} transition-colors"
                                >
                                  ${idx + 1}
                                </span>
                                <div class="min-w-0 flex-1">
                                  <span class="text-sm font-medium text-mitto-text-strong"
                                    >${opt.label}</span
                                  >
                                  ${opt.description &&
                                  html`<span
                                    class="block text-xs text-mitto-text-muted mt-0.5"
                                    >${opt.description}</span
                                  >`}
                                </div>
                              </button>
                            `,
                          )}

                          <!-- Free text input (if allowed) -->
                          ${activeUIPrompt.allowFreeText &&
                          html`
                            <div class="px-4 py-3 flex items-center gap-2">
                              <input
                                type="text"
                                value=${freeTextInput}
                                onInput=${(e) =>
                                  setFreeTextInput(e.target.value)}
                                onKeyDown=${(e) => {
                                  if (e.key === "Enter") {
                                    // Consume the Enter keypress so it doesn't
                                    // propagate to the native layer (WKWebView),
                                    // which beeps on unhandled keys in inputs
                                    // that aren't inside a <form>.
                                    e.preventDefault();
                                    if (freeTextInput.trim()) {
                                      handleUIPromptAnswer(
                                        "free_text",
                                        freeTextInput.trim(),
                                        freeTextInput.trim(),
                                      );
                                      setFreeTextInput("");
                                    }
                                  }
                                }}
                                placeholder=${activeUIPrompt.freeTextPlaceholder ||
                                "Type a custom response..."}
                                class="flex-1 min-w-0 bg-transparent text-sm text-mitto-text-secondary outline-none"
                              />
                              <button
                                type="button"
                                onClick=${() => {
                                  if (freeTextInput.trim()) {
                                    handleUIPromptAnswer(
                                      "free_text",
                                      freeTextInput.trim(),
                                      freeTextInput.trim(),
                                    );
                                    setFreeTextInput("");
                                  }
                                }}
                                disabled=${!freeTextInput.trim()}
                                class="btn btn-primary btn-square btn-sm shrink-0 tooltip tooltip-left"
                                data-tip="Send response"
                                aria-label="Send response"
                              >
                                <svg
                                  class="w-4 h-4"
                                  fill="none"
                                  stroke="currentColor"
                                  viewBox="0 0 24 24"
                                >
                                  <path
                                    stroke-linecap="round"
                                    stroke-linejoin="round"
                                    stroke-width="2"
                                    d="M22 2L11 13M22 2l-7 20-4-9-9-4 20-7z"
                                  />
                                </svg>
                              </button>
                            </div>
                          `}
                        </div>

                        <!-- Toggle button to show/hide chat input -->
                        <div
                          class="flex items-center justify-end px-4 pt-2 pb-3 shrink-0"
                        >
                          <${PromptStopButton} onStop=${handleUIPromptStop} />
                        </div>
                      </div>
                    `
          }
        </div>
      `}

      <!-- Periodic settings card (shown when periodic is enabled) -->
      <!-- Part of normal document flow - pushes conversation area up. -->
      <!-- Single merged card: compact header always visible; body expands on demand. -->
      <div class="max-w-4xl mx-auto">
        <${PeriodicFrequencyPanel}
          isOpen=${periodicConfigured && !hasActiveUIPrompt}
          disabled=${isPeriodicLocked}
          sessionId=${sessionId}
          frequency=${periodicFrequency}
          onFrequencyChange=${handlePeriodicFrequencyChange}
          nextScheduledAt=${periodicNextScheduledAt}
          isStreaming=${isStreaming}
          freshContext=${periodicFreshContext}
          onFreshContextChange=${setPeriodicFreshContext}
          maxIterations=${periodicMaxIterations}
          iterationCount=${periodicIterationCount}
          onMaxIterationsChange=${handlePeriodicMaxIterationsChange}
          onPeriodicEnabledChange=${handlePeriodicEnabledChange}
          prompts=${periodicPrompts}
          selectedPromptName=${periodicPromptName}
          selectedPromptBody=${periodicPrompt}
          onPromptSelect=${handlePeriodicPromptSelect}
          isPromptAreaVisible=${!isPromptCollapsed}
          onTogglePromptArea=${() =>
            setIsPromptCollapsed((v) => {
              const nextCollapsed = !v;
              // Expanding the prompt area collapses the periodic properties.
              if (!nextCollapsed) setPeriodicExpanded(false);
              return nextCollapsed;
            })}
          expanded=${periodicExpanded}
          onToggleExpanded=${() =>
            setPeriodicExpanded((v) => {
              const next = !v;
              // Expanding the periodic properties collapses the prompt area.
              if (next) setIsPromptCollapsed(true);
              return next;
            })}
          trigger=${periodicTrigger}
          delaySeconds=${periodicDelaySeconds}
          maxDurationSeconds=${periodicMaxDurationSeconds}
          stoppedReason=${periodicStoppedReason}
          minDelaySeconds=${5}
          onTriggerChange=${setPeriodicTrigger}
          onDelayChange=${setPeriodicDelaySeconds}
          onMaxDurationChange=${setPeriodicMaxDurationSeconds}
        />
      </div>

      ${hasActionButtons &&
      !isStreaming &&
      !isReadOnly &&
      !noSession &&
      !periodicConfigured &&
      !isResuming &&
      html`
        <div class="max-w-4xl mx-auto mb-3">
          <div class="flex flex-wrap gap-2">
            ${actionButtons.map(
              (btn, idx) => html`
                <button
                  key=${idx}
                  type="button"
                  onClick=${() => handleActionButtonClick(btn.response)}
                  class="btn btn-primary btn-sm gap-1.5"
                  title=${btn.response}
                >
                  <svg
                    class="w-3.5 h-3.5 shrink-0"
                    fill="none"
                    stroke="currentColor"
                    viewBox="0 0 24 24"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      stroke-width="2"
                      d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z"
                    />
                  </svg>
                  <span class="truncate max-w-[200px]">${btn.label}</span>
                </button>
              `,
            )}
          </div>
        </div>
      `}
      ${uploadError &&
      html`
        <div class="max-w-4xl mx-auto mb-2">
          <div
            class="bg-red-900/50 border border-red-700 text-red-200 px-4 py-2 rounded-lg text-sm flex items-center gap-2"
          >
            <svg
              class="w-4 h-4 shrink-0"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"
              />
            </svg>
            <span>${uploadError}</span>
            <button
              type="button"
              onClick=${() => setUploadError(null)}
              class="btn btn-ghost btn-square btn-xs ml-auto"
            >
              <svg
                class="w-4 h-4"
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
          </div>
        </div>
      `}
      ${improveError &&
      html`
        <div class="max-w-4xl mx-auto mb-2">
          <div
            class="bg-red-900/50 border border-red-700 text-red-200 px-4 py-2 rounded-lg text-sm flex items-center gap-2"
          >
            <svg
              class="w-4 h-4 shrink-0"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
              />
            </svg>
            <span>${improveError}</span>
            <button
              type="button"
              onClick=${() => setImproveError(null)}
              class="btn btn-ghost btn-square btn-xs ml-auto"
            >
              <svg
                class="w-4 h-4"
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
          </div>
        </div>
      `}
      ${sendError &&
      html`
        <div class="max-w-4xl mx-auto mb-2">
          <div
            class="alert alert-warning text-sm"
          >
            <svg
              class="w-4 h-4 shrink-0"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
              />
            </svg>
            <span>${sendError}</span>
            <span class="text-xs ml-1 opacity-80"
              >(Your message is preserved - click Send to retry)</span
            >
            <button
              type="button"
              onClick=${() => setSendError(null)}
              class="btn btn-ghost btn-xs btn-circle ml-auto"
            >
              <svg
                class="w-4 h-4"
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
          </div>
        </div>
      `}
      ${!(isPromptCollapsed && (periodicConfigured || hasActiveUIPrompt)) &&
      html`
        <div class="max-w-4xl mx-auto chat-input-container">
          <div class="chat-input-box" ref=${dropupRef}>
            <!-- Slash command picker - expands from bottom of the box -->
            <${SlashCommandPicker}
              isOpen=${showSlashPicker}
              onClose=${() => setShowSlashPicker(false)}
              onSelect=${handleSlashCommandSelect}
              commands=${availableCommands}
              filter=${slashFilter}
              selectedIndex=${slashSelectedIndex}
              onSelectedIndexChange=${setSlashSelectedIndex}
            />

            <!-- Textarea - borderless, transparent background; relative wrapper for improving overlay -->
            <div class="relative">
              <textarea
                ref=${textareaRef}
                autocorrect=${isNativeApp() ? "off" : "on"}
                autocomplete=${isNativeApp() ? "off" : "on"}
                autocapitalize=${isNativeApp() ? "off" : "sentences"}
                spellcheck=${isNativeApp() ? "false" : "true"}
                ...${isNativeApp() ? {} : { inputmode: "text", enterkeyhint: sendKeyMode === "ctrl-enter" ? "enter" : "send" }}
                value=${text}
                onInput=${handleInput}
                onKeyDown=${handleKeyDown}
                onPaste=${handlePaste}
                onFocus=${handleTextareaFocus}
                onClick=${handleResumeOnInteraction}
                onBlur=${handleTextareaBlur}
                placeholder=${getPlaceholder()}
                rows="3"
                style="min-height: ${textareaMinHeight}px; max-height: ${textareaHardMax}px;"
                class="chat-input-textarea overflow-y-auto ${isFullyDisabled ||
                isReadOnly ||
                isImproving
                  ? "opacity-50 cursor-not-allowed"
                  : ""}"
                disabled=${isFullyDisabled ||
                isReadOnly ||
                isImproving}
              />

              <!-- Improving prompt overlay with spinner -->
              ${isImproving &&
              html`
                <div class="textarea-improving-overlay">
                  <span class="loading loading-spinner w-6 h-6 text-mitto-accent"></span>
                  <span class="text-sm text-mitto-accent-300 mt-2">Improving prompt...</span>
                </div>
              `}
            </div>

            <!-- Pending images preview -->
            ${hasPendingImages &&
            html`
              <div class="px-4 pt-2 pb-1">
                <div class="flex flex-wrap gap-2">
                  ${pendingImages.map(
                    (img) => html`
                      <div key=${img.id} class="relative group">
                        ${img.url
                          ? html`<img
                              src=${img.url}
                              alt=${img.name || "Pending image"}
                              class="w-16 h-16 rounded-lg object-cover border border-mitto-border-2 ${img.uploading ? "opacity-50" : ""}"
                            />`
                          : html`<div
                              class="w-16 h-16 rounded-lg bg-mitto-surface-3 border border-mitto-border-2 flex items-center justify-center"
                            >
                              <svg class="w-6 h-6 text-mitto-text-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
                              </svg>
                            </div>`}
                        ${img.uploading
                          ? html`
                              <div class="absolute inset-0 flex items-center justify-center">
                                <span class="loading loading-spinner w-5 h-5 text-mitto-text-strong"></span>
                              </div>
                            `
                          : html`
                              <button
                                type="button"
                                onClick=${() => removeImage(img.id)}
                                class="absolute -top-1 -right-1 w-5 h-5 bg-mitto-danger hover:bg-mitto-danger-hover rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity tooltip tooltip-left"
                                data-tip="Remove image"
                                aria-label="Remove image"
                              >
                                <svg class="w-3 h-3 text-mitto-danger-fg" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                                </svg>
                              </button>
                            `}
                      </div>
                    `,
                  )}
                </div>
              </div>
            `}

            <!-- Pending files preview -->
            ${hasPendingFiles &&
            html`
              <div class="px-4 pt-1 pb-1">
                <div class="flex flex-wrap gap-2">
                  ${pendingFiles.map(
                    (file) => html`
                      <div key=${file.id} class="relative group flex items-center gap-2 bg-mitto-surface-3 rounded-lg px-3 py-2 border border-mitto-border-2">
                        <svg class="w-5 h-5 text-mitto-text-muted shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                        </svg>
                        <span class="text-sm text-mitto-text-secondary max-w-[150px] truncate" title=${file.name}>${file.name}</span>
                        ${file.category && html`
                          <span class="text-xs px-1.5 py-0.5 rounded ${file.category === "text" ? "bg-green-900 text-green-300" : "bg-mitto-accent-900 text-mitto-accent-300"}">${file.category}</span>
                        `}
                        ${file.uploading
                          ? html`
                              <div class="flex items-center justify-center">
                                <span class="loading loading-spinner w-4 h-4 text-mitto-accent"></span>
                              </div>
                            `
                          : html`
                              <button
                                type="button"
                                onClick=${() => removeFile(file.id)}
                                class="w-5 h-5 bg-mitto-danger hover:bg-mitto-danger-hover rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity tooltip tooltip-left"
                                data-tip="Remove file"
                                aria-label="Remove file"
                              >
                                <svg class="w-3 h-3 text-mitto-danger-fg" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                                </svg>
                              </button>
                            `}
                      </div>
                    `,
                  )}
                </div>
              </div>
            `}

            <!-- Bottom toolbar bar -->
            <div class="chat-input-bottom-bar">
              <!-- Left action buttons: improve, attach-image, attach-file, save, clear -->
              <div class="chat-input-actions-left">
                <!-- Magic Wand / Improve Prompt Button -->
                <button
                  type="button"
                  onClick=${handleImprovePrompt}
                  onMouseDown=${(e) => e.preventDefault()}
                  disabled=${isFullyDisabled || !text.trim() || isReadOnly || isImproving}
                  class="chat-input-action tooltip tooltip-top ${isImproving ? "improving" : ""}"
                  data-tip="Improve prompt with AI (Ctrl+P)"
                  aria-label="Improve prompt with AI (Ctrl+P)"
                >
                  ${isImproving
                    ? html`
                        <span class="loading loading-spinner w-4 h-4"></span>
                      `
                    : html`
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z" />
                        </svg>
                      `}
                </button>

                <!-- Attach Image Button -->
                <button
                  type="button"
                  onClick=${handleAttachImageClick}
                  onMouseDown=${(e) => e.preventDefault()}
                  disabled=${isFullyDisabled || isReadOnly || isImproving}
                  class="chat-input-action tooltip tooltip-top"
                  data-tip="Attach image"
                  aria-label="Attach image"
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
                  </svg>
                </button>

                <!-- Attach File Button -->
                <button
                  type="button"
                  onClick=${handleAttachFileClick}
                  onMouseDown=${(e) => e.preventDefault()}
                  disabled=${isFullyDisabled || isReadOnly || isImproving}
                  class="chat-input-action tooltip tooltip-top"
                  data-tip="Attach file"
                  aria-label="Attach file"
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15.172 7l-6.586 6.586a2 2 0 102.828 2.828l6.414-6.586a4 4 0 00-5.656-5.656l-6.415 6.585a6 6 0 108.486 8.486L20.5 13" />
                  </svg>
                </button>

                <!-- Save Prompt Button (macOS native only) -->
                ${isNativeApp() &&
                window.mittoIsExternal !== true &&
                html`
                  <button
                    type="button"
                    onClick=${() => setShowSaveDialog(true)}
                    onMouseDown=${(e) => e.preventDefault()}
                    disabled=${isFullyDisabled || !text.trim() || isReadOnly || isImproving}
                    class="chat-input-action tooltip tooltip-top"
                    data-tip="Save prompt as file"
                    aria-label="Save prompt as file"
                  >
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7H5a2 2 0 00-2 2v9a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-3m-1 4l-3 3m0 0l-3-3m3 3V4" />
                    </svg>
                  </button>
                `}

                <!-- Clear Button -->
                <button
                  type="button"
                  onClick=${() => {
                    setText("");
                    setPendingImages([]);
                    setPendingFiles([]);
                  }}
                  onMouseDown=${(e) => e.preventDefault()}
                  disabled=${isFullyDisabled || isReadOnly || isImproving || (!text.trim() && !hasPendingAttachments)}
                  class="chat-input-action tooltip tooltip-top"
                  data-tip="Clear message"
                  aria-label="Clear message"
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                  </svg>
                </button>
              </div>

              <!-- Center: Config selectors and context usage (shown when either is available) -->
              ${(selectConfigOptions.length > 0 || contextPct !== null) && html`
                <div class="chat-input-model-selector">
                  ${selectConfigOptions.map((configOpt) => html`
                    <${ChatInputConfigSelect}
                      key=${configOpt.id}
                      configOption=${configOpt}
                      onSetConfigOption=${onSetConfigOption}
                      isStreaming=${isStreaming}
                    />
                  `)}
                  ${contextPct !== null && html`
                    <span
                      class="chat-input-context-pct tooltip tooltip-top"
                      style=${"color: " + (contextPct > 80 ? "#ef4444" : contextPct > 50 ? "#f59e0b" : "#64748b")}
                      data-tip=${contextUsage?.size
                        ? "Context: " + (contextUsage.used || 0).toLocaleString() + " / " + contextUsage.size.toLocaleString() + " tokens"
                        : "Context: ~" + (tokenUsage?.input_tokens || 0).toLocaleString() + " input tokens"}
                    >${contextPct}%</span>
                  `}
                </div>
              `}

              <!-- Right action buttons: queue-toggle, prompts, enqueue, send/stop/lock -->
              <div class="chat-input-actions-right">
                <!-- Queue toggle button: shown when queue has items OR dropdown is open -->
                ${(queueLength > 0 || showQueueDropdown) && html`
                <button
                  type="button"
                  onClick=${() => {
                    if (!periodicConfigured && onToggleQueue) onToggleQueue();
                  }}
                  disabled=${periodicConfigured}
                  data-queue-toggle
                  class="chat-input-action relative tooltip tooltip-top"
                  style="${showQueueDropdown && !periodicConfigured ? "background: #2563eb !important; color: white !important;" : ""}"
                  data-tip=${periodicConfigured
                    ? "Queue disabled for periodic sessions"
                    : `${queueLength}/${queueConfig.max_size} queued - Click to ${showQueueDropdown ? "hide" : "show"} queue`}
                  aria-label=${periodicConfigured
                    ? "Queue disabled for periodic sessions"
                    : `${queueLength}/${queueConfig.max_size} queued - Click to ${showQueueDropdown ? "hide" : "show"} queue`}
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 10h16M4 14h16M4 18h16" />
                  </svg>
                  ${!periodicConfigured && html`<span
                    class="absolute -top-1 -right-1 pointer-events-none"
                    style="display:flex;align-items:center;justify-content:center;min-width:16px;height:16px;padding:0 4px;border-radius:9999px;font-size:10px;font-weight:600;line-height:1;background:var(--mitto-accent,#dc2626);color:var(--mitto-accent-fg,#ffffff);box-sizing:border-box;"
                  >${queueLength}</span>`}
                </button>
                `}

                <!-- Prompts Toggle Button -->
                ${hasPrompts &&
                html`
                  <div class="relative">
                    <!-- Prompts dropdown - anchored above the button -->
                    ${showDropup &&
                    html`
                      <div
                        class="absolute bottom-full right-0 mb-2 bg-mitto-surface-2 border border-mitto-border-2 rounded-lg overflow-hidden z-50 flex flex-col"
                        style="width: 20rem; min-width: 20rem; max-width: 20rem; max-height: 400px; box-shadow: 0 20px 40px rgba(0, 0, 0, 0.5), 0 8px 16px rgba(0, 0, 0, 0.4), 0 0 0 1px rgba(255, 255, 255, 0.1);"
                      >
                        <${PromptsMenu}
                          prompts=${predefinedPrompts}
                          modelOption=${modelOption}
                          filterText=${promptFilterText}
                          onFilterChange=${(value) => {
                            setPromptFilterText(value);
                            setPromptSelectedIndex(-1);
                          }}
                          onFilterKeyDown=${(e) => {
                            // Prevent the event from bubbling to the textarea
                            e.stopPropagation();
                            if (e.key === "Escape") {
                              setShowDropup(false);
                              return;
                            }
                            if (e.key === "ArrowDown") {
                              e.preventDefault();
                              setPromptSelectedIndex((prev) =>
                                Math.min(prev + 1, flatFilteredPrompts.length - 1),
                              );
                              return;
                            }
                            if (e.key === "ArrowUp") {
                              e.preventDefault();
                              setPromptSelectedIndex((prev) => Math.max(-1, prev - 1));
                              return;
                            }
                            if (e.key === "Enter") {
                              e.preventDefault();
                              if (
                                promptSelectedIndex >= 0 &&
                                flatFilteredPrompts.length > 0
                              ) {
                                const clampedIndex = Math.min(
                                  Math.max(promptSelectedIndex, 0),
                                  flatFilteredPrompts.length - 1,
                                );
                                handlePredefinedPrompt(
                                  flatFilteredPrompts[clampedIndex],
                                  e,
                                );
                              }
                              return;
                            }
                          }}
                          filterInputRef=${promptFilterInputRef}
                          sortMode=${promptSortMode}
                          selectedIndex=${promptSelectedIndex}
                          selectedItemRef=${selectedPromptItemRef}
                          onSelect=${(prompt, e) =>
                            handlePredefinedPrompt(prompt, e)}
                          showSourceBadge=${true}
                          shiftHeld=${shiftHeld}
                          placeholder="Filter prompts..."
                          emptyText="No matching prompts"
                          keyPrefix="chat-prompts"
                          footer=${html`<span
                            class="text-[10px] ${shiftHeld
                              ? "text-mitto-accent"
                              : "text-mitto-text-muted"}"
                            >${shiftHeld
                              ? "✏️ Will insert into editor"
                              : "⇧ Hold Shift to edit before sending"}</span
                          >`}
                        />
                      </div>
                    `}
                    <button
                      type="button"
                      onClick=${handleTogglePrompts}
                      onMouseDown=${(e) => e.preventDefault()}
                      disabled=${isFullyDisabled || isReadOnly}
                      class="chat-input-action tooltip tooltip-top"
                      data-tip="Insert predefined prompt"
                      aria-label="Insert predefined prompt"
                    >
                      <svg
                        class="w-4 h-4 transition-transform ${showDropup ? "rotate-180" : ""}"
                        fill="none"
                        stroke="currentColor"
                        viewBox="0 0 24 24"
                      >
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" />
                      </svg>
                    </button>
                  </div>
                `}

                <!-- Enqueue button: shown when streaming (so user can enqueue while agent works) -->
                ${isStreaming && html`
                <button
                  type="button"
                  onClick=${handleAddToQueueClick}
                  disabled=${isFullyDisabled || (!text.trim() && !hasPendingAttachments) || isReadOnly || isImproving || periodicConfigured}
                  class="chat-input-action tooltip tooltip-top"
                  data-tip=${periodicConfigured ? "Queue disabled for periodic sessions" : "Add to queue (⌘/Ctrl+Enter)"}
                  aria-label=${periodicConfigured ? "Queue disabled for periodic sessions" : "Add to queue (⌘/Ctrl+Enter)"}
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
                  </svg>
                </button>
                `}

                <!-- Send/Stop button -->
                ${isStreaming
                    ? html`
                        <!-- Stop button -->
                        <button
                          type="button"
                          onClick=${() => {
                            if (hasActiveUIPrompt) {
                              handleUIPromptAnswer("abort", "Abort");
                            }
                            onCancel();
                          }}
                          class="chat-input-action stop-active tooltip tooltip-top"
                          data-tip=${hasActiveUIPrompt ? "Dismiss prompt and stop" : "Stop streaming"}
                          aria-label=${hasActiveUIPrompt ? "Dismiss prompt and stop" : "Stop streaming"}
                        >
                          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <rect x="6" y="6" width="12" height="12" rx="2" stroke-width="2" />
                          </svg>
                        </button>
                      `
                    : isSending
                      ? html`
                          <!-- Sending spinner -->
                          <button type="button" disabled class="chat-input-action">
                            <span class="loading loading-spinner w-4 h-4"></span>
                          </button>
                        `
                      : html`
                          <!-- Send button -->
                          <button
                            type="submit"
                            disabled=${isFullyDisabled || isResuming || !acpReady || (!text.trim() && !hasPendingAttachments) || isReadOnly || isImproving || isQueueFull}
                            class="chat-input-action tooltip tooltip-top ${(!text.trim() && !hasPendingAttachments) || isQueueFull ? "" : "send-active"} ${isQueueFull ? "queue-full" : ""}"
                            style="${isQueueFull ? "background: #ea580c !important; color: white !important;" : ""}"
                            data-tip=${isQueueFull ? `Queue full (${queueConfig.max_size}/${queueConfig.max_size})` : "Send message"}
                            aria-label=${isQueueFull ? `Queue full (${queueConfig.max_size}/${queueConfig.max_size})` : "Send message"}
                          >
                            ${isQueueFull
                              ? html`
                                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
                                  </svg>
                                `
                              : html`
                                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M22 2L11 13M22 2l-7 20-4-9-9-4 20-7z" />
                                  </svg>
                                `}
                          </button>
                        `}
              </div>
            </div>
          </div>
        </div>
      `}

      <!-- Save Prompt Dialog -->
      <${SavePromptDialog}
        isOpen=${showSaveDialog}
        onClose=${() => setShowSaveDialog(false)}
        promptText=${text}
        workingDir=${workingDir}
      />
    </form>
  `;
}

