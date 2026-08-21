// Mitto Web Interface - Conversation Properties Panel
// Fixed overlay panel on the RIGHT side for viewing and editing conversation properties
// Always appears above other panels (like the conversations sidebar)

const { html, useState, useEffect, useCallback, useRef, useMemo, Fragment } =
  window.preact;

import {
  CloseIcon,
  EditIcon,
  CheckIcon,
  FolderIcon,
  LoopFilledIcon,
} from "./Icons.js";
import { apiUrl } from "../utils/api.js";
import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { formatTimeAgo } from "../lib.js";
import { Drawer } from "./Drawer.js";
import { Tooltip } from "./Tooltip.js";
import { canRevealInFinder, revealInFinder } from "../utils/native.js";
import { getContextWindowSize } from "../utils/models.js";
import { ConfigOptionSelect } from "./ConfigOptionSelect.js";

/**
 * Format a token count into a compact human-readable string.
 * @param {number|undefined|null} count
 * @returns {string} e.g. "1.2k", "3.4M", "512", or "—" for missing values
 */
function formatTokenCount(count) {
  if (count === undefined || count === null) return "—";
  if (count >= 1000000) return `${(count / 1000000).toFixed(1)}M`;
  if (count >= 1000) return `${(count / 1000).toFixed(1)}k`;
  return count.toString();
}

/**
 * Format a duration given in milliseconds into a compact human-readable string.
 * @param {number} ms
 * @returns {string} e.g. "850ms", "12.3s", "1m2s"
 */
function formatDuration(ms) {
  if (!ms || ms < 0) return "0ms";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const totalSeconds = ms / 1000;
  if (totalSeconds < 60) return `${totalSeconds.toFixed(1)}s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = Math.round(totalSeconds % 60);
  return `${minutes}m${seconds}s`;
}

/**
 * TriStateCheckbox - A checkbox with three states: unset, enabled, disabled
 * @param {Object} props
 * @param {boolean|null} props.value - Current value (null = unset, true = enabled, false = disabled)
 * @param {Function} props.onChange - Callback when value changes
 * @param {boolean} props.disabled - Whether the checkbox is disabled
 * @param {string} props.title - Tooltip text
 */
function TriStateCheckbox({ value, onChange, disabled = false, title = "" }) {
  const handleClick = useCallback(() => {
    if (disabled) return;
    // Cycle through: unset -> true -> false -> unset
    // But for simplicity, clicking always toggles between true/false
    // If unset, set to true; if true, set to false; if false, set to true
    if (value === null || value === undefined) {
      onChange(true);
    } else {
      onChange(!value);
    }
  }, [value, onChange, disabled]);

  const isUnset = value === null || value === undefined;
  const isEnabled = value === true;

  return html`
    <button
      type="button"
      class="relative w-5 h-5 rounded border-2 transition-colors flex items-center justify-center
        ${disabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer"}
        ${isUnset
        ? "border-mitto-border-3 bg-mitto-surface-3"
        : isEnabled
          ? "border-mitto-accent bg-mitto-accent"
          : "border-mitto-border-3 bg-mitto-surface-3"}"
      onClick=${handleClick}
      disabled=${disabled}
      title=${title}
    >
      ${isUnset
        ? html`<span class="text-mitto-text-500 text-xs font-medium">—</span>`
        : isEnabled
          ? html`<svg
              class="w-3 h-3 text-mitto-accent-fg"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="3"
                d="M5 13l4 4L19 7"
              />
            </svg>`
          : null}
    </button>
  `;
}

/**
 * Convert UTC time (HH:MM) to local time for display.
 * @param {string} utcTime - Time in HH:MM format (UTC)
 * @returns {string} Time formatted for display in local time
 */
function utcToLocalTimeDisplay(utcTime) {
  if (!utcTime) return "";
  const [hours, minutes] = utcTime.split(":").map(Number);
  // Create a Date object for today at the UTC time
  const now = new Date();
  const utcDate = new Date(
    Date.UTC(
      now.getUTCFullYear(),
      now.getUTCMonth(),
      now.getUTCDate(),
      hours,
      minutes,
      0,
    ),
  );
  // Format in local time with AM/PM
  return utcDate.toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });
}

/**
 * Format a frequency object into a human-readable string.
 * @param {Object} frequency - The frequency object with value, unit, and optional at (at is in UTC)
 * @returns {string} Human-readable frequency description (times shown in local time)
 */
function formatFrequency(frequency) {
  if (!frequency) return "";

  const { value, unit, at } = frequency;
  let text = "";

  if (value === 1) {
    // Singular form
    switch (unit) {
      case "minutes":
        text = "Every minute";
        break;
      case "hours":
        text = "Every hour";
        break;
      case "days":
        text = "Every day";
        break;
      default:
        text = `Every ${unit}`;
    }
  } else {
    // Plural form
    text = `Every ${value} ${unit}`;
  }

  // Add "at" time for daily schedules (convert from UTC to local time)
  if (unit === "days" && at) {
    text += ` at ${utcToLocalTimeDisplay(at)}`;
  }

  return text;
}

/**
 * Format a relative time from now to a future date.
 * @param {Date|string} targetDate - The target date
 * @returns {string} Human-readable relative time (e.g., "in 7 minutes", "in 2 hours", "in 3 days")
 */
function formatRelativeTime(targetDate) {
  if (!targetDate) return "";

  const target = targetDate instanceof Date ? targetDate : new Date(targetDate);
  const now = new Date();
  const diffMs = target.getTime() - now.getTime();

  // If in the past, show "now" or "overdue"
  if (diffMs <= 0) {
    return "now";
  }

  const diffMinutes = Math.floor(diffMs / (1000 * 60));
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));

  if (diffMinutes < 60) {
    return diffMinutes === 1 ? "in 1 minute" : `in ${diffMinutes} minutes`;
  } else if (diffHours < 24) {
    return diffHours === 1 ? "in 1 hour" : `in ${diffHours} hours`;
  } else {
    return diffDays === 1 ? "in 1 day" : `in ${diffDays} days`;
  }
}

// ConfigOptionSelect is imported from the shared ./ConfigOptionSelect.js
// component (used here with the "block" variant, showing the option description
// and disabling the control while the session is streaming).

/**
 * ConversationPropertiesPanel - Fixed overlay panel for conversation properties
 * Always appears on the left side, above other panels
 *
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the panel is open
 * @param {Function} props.onClose - Callback to close the panel
 * @param {string} props.sessionId - Current session ID
 * @param {Object} props.sessionInfo - Session metadata (name, working_dir, etc.)
 * @param {Function} props.onRename - Callback to rename the session
 * @param {boolean} props.isStreaming - Whether the session is currently streaming
 * @param {Array} props.configOptions - Session config options (unified format for modes and other settings)
 * @param {Function} props.onSetConfigOption - Callback to change a config option value
 */
export function ConversationPropertiesPanel({
  isOpen,
  onClose,
  sessionId,
  sessionInfo,
  onRename,
  isStreaming = false,
  configOptions = [],
  onSetConfigOption,
  mcpTools = [],
}) {
  // Title editing state
  const [isEditingTitle, setIsEditingTitle] = useState(false);
  const [editedTitle, setEditedTitle] = useState("");
  const [isSavingTitle, setIsSavingTitle] = useState(false);
  const titleInputRef = useRef(null);

  // Loop config state
  const [loopConfig, setLoopConfig] = useState(null);
  const [callbackConfig, setCallbackConfig] = useState(null);
  const [callbackCopied, setCallbackCopied] = useState(false);

  // Confirmation dialog state
  const [confirmDialog, setConfirmDialog] = useState(null);

  // MCP Tools collapsible state
  const [isMcpToolsExpanded, setIsMcpToolsExpanded] = useState(false);

  // Advanced settings (feature flags) state
  const [isAdvancedExpanded, setIsAdvancedExpanded] = useState(false);
  const [availableFlags, setAvailableFlags] = useState([]);
  const [sessionSettings, setSessionSettings] = useState({});
  const [isLoadingFlags, setIsLoadingFlags] = useState(false);
  const [savingFlags, setSavingFlags] = useState({}); // Track which flags are being saved
  const [flagsError, setFlagsError] = useState(null);

  // State for dynamic relative time updates (triggers re-render every 30 seconds)
  const [, setTimeNow] = useState(Date.now());

  // Derive current model ID from the "model" config option (if present).
  // Used to look up the context window size for the progress bar.
  const currentModelId = useMemo(() => {
    if (!configOptions?.length) return null;
    const modelOpt = configOptions.find((opt) => opt.id === "model");
    return modelOpt?.current_value || null;
  }, [configOptions]);

  // Update relative time display every 30 seconds while panel is open
  useEffect(() => {
    if (!isOpen || !loopConfig?.next_scheduled_at) {
      return;
    }

    const intervalId = setInterval(() => {
      setTimeNow(Date.now());
    }, 30000); // Update every 30 seconds

    return () => clearInterval(intervalId);
  }, [isOpen, loopConfig?.next_scheduled_at]);

  // Reset state when session changes or panel closes
  useEffect(() => {
    setIsEditingTitle(false);
    setLoopConfig(null);
    setCallbackConfig(null);
    setCallbackCopied(false);
    setFlagsError(null);
    setSavingFlags({});
  }, [sessionId, isOpen]);

  // Fetch loop config, callback config, flags, and session settings when panel opens
  useEffect(() => {
    if (!isOpen || !sessionId) return;

    const fetchData = async () => {
      setIsLoadingFlags(true);
      setFlagsError(null);

      // Loop + callback endpoints only exist for loop conversations.
      // Gating on loop_configured avoids 404 noise on regular sessions.
      const loopConfigured = sessionInfo?.loop_configured === true;

      try {
        // Fetch loop config, callback config, available flags, and session
        // settings in parallel. Each call swallows its own failure (mirrors
        // the old per-Response `x && x.ok` tolerance — the SDK throws on a
        // non-2xx status where the old raw fetch() would just resolve with
        // `res.ok === false`), so one endpoint failing does not blank out
        // the other three.
        const [loopData, callbackData, flagsData, settingsData] =
          await Promise.all([
            loopConfigured
              ? getSdkClient()
                  .sessions.loop.get(sessionId)
                  .catch(() => null)
              : Promise.resolve(null),
            loopConfigured
              ? getSdkClient()
                  .sessions.getCallback(sessionId)
                  .catch(() => null)
              : Promise.resolve(null),
            getSdkClient()
              .misc.advancedFlags()
              .catch(() => null),
            getSdkClient()
              .sessions.getSettings(sessionId)
              .catch(() => null),
          ]);

        // No loop config or error - clear state
        setLoopConfig(loopData || null);
        setCallbackConfig(callbackData || null);

        if (flagsData) {
          // API returns { flags: [...], configured_defaults: {...} }
          setAvailableFlags(flagsData.flags || flagsData || []);
        }

        if (settingsData) setSessionSettings(settingsData.settings || {});
      } catch (err) {
        console.error("Failed to fetch panel data:", err);
        setFlagsError("Failed to load settings");
      } finally {
        setIsLoadingFlags(false);
      }
    };

    fetchData();
  }, [isOpen, sessionId, sessionInfo?.loop_configured]);

  // Focus title input when entering edit mode
  useEffect(() => {
    if (isEditingTitle && titleInputRef.current) {
      titleInputRef.current.focus();
      titleInputRef.current.select();
    }
  }, [isEditingTitle]);

  // Listen for WebSocket session_settings_updated events to keep UI in sync
  useEffect(() => {
    if (!isOpen || !sessionId) return;

    const handleSettingsUpdated = (event) => {
      const { session_id, settings } = event.detail || {};
      if (session_id === sessionId && settings) {
        console.log(
          "[ConversationPropertiesPanel] Settings updated via WebSocket:",
          settings,
        );
        setSessionSettings(settings);
      }
    };

    window.addEventListener(
      "mitto:session_settings_updated",
      handleSettingsUpdated,
    );
    return () => {
      window.removeEventListener(
        "mitto:session_settings_updated",
        handleSettingsUpdated,
      );
    };
  }, [isOpen, sessionId]);

  // Listen for WebSocket loop_updated events so the loop section (and the
  // fresh-context toggle) stays in sync when changed from another panel/client.
  useEffect(() => {
    if (!isOpen || !sessionId) return;

    const handleLoopUpdated = (event) => {
      const {
        sessionId: updatedSessionId,
        loopConfigured,
        loopEnabled,
        frequency,
        nextScheduledAt,
        freshContext,
        iterationCount,
        maxIterations,
      } = event.detail || {};
      if (updatedSessionId !== sessionId) return;

      // Loop config was deleted — clear local state.
      if (loopConfigured === false) {
        setLoopConfig(null);
        return;
      }

      // Merge into existing config (the panel fetches the full config on open).
      setLoopConfig((prev) =>
        prev
          ? {
              ...prev,
              enabled: loopEnabled,
              frequency: frequency || prev.frequency,
              next_scheduled_at: nextScheduledAt ?? prev.next_scheduled_at,
              fresh_context:
                typeof freshContext === "boolean"
                  ? freshContext
                  : prev.fresh_context,
              ...(iterationCount !== undefined && {
                iteration_count: iterationCount,
              }),
              ...(maxIterations !== undefined && {
                max_iterations: maxIterations,
              }),
            }
          : prev,
      );
    };

    window.addEventListener("mitto:loop_config_updated", handleLoopUpdated);
    return () => {
      window.removeEventListener(
        "mitto:loop_config_updated",
        handleLoopUpdated,
      );
    };
  }, [isOpen, sessionId]);

  // Handle title edit start
  const handleStartEditTitle = useCallback(() => {
    setEditedTitle(sessionInfo?.name || "");
    setIsEditingTitle(true);
  }, [sessionInfo?.name]);

  // Handle title save
  const handleSaveTitle = useCallback(async () => {
    if (!sessionId || isSavingTitle) return;

    const newTitle = editedTitle.trim();
    if (!newTitle || newTitle === sessionInfo?.name) {
      setIsEditingTitle(false);
      return;
    }

    setIsSavingTitle(true);
    try {
      await onRename(sessionId, newTitle);
      setIsEditingTitle(false);
    } catch (err) {
      console.error("Failed to save title:", err);
    } finally {
      setIsSavingTitle(false);
    }
  }, [sessionId, editedTitle, sessionInfo?.name, onRename, isSavingTitle]);

  // Handle title key press
  const handleTitleKeyDown = useCallback(
    (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        handleSaveTitle();
      } else if (e.key === "Escape") {
        e.preventDefault();
        setIsEditingTitle(false);
      }
    },
    [handleSaveTitle],
  );

  // Handle flag value change
  const handleFlagChange = useCallback(
    async (flagName, newValue) => {
      if (!sessionId) return;

      // Mark flag as saving
      setSavingFlags((prev) => ({ ...prev, [flagName]: true }));
      setFlagsError(null);

      try {
        const data = await getSdkClient().sessions.updateSettings(sessionId, {
          [flagName]: newValue,
        });
        setSessionSettings(data.settings || {});
      } catch (err) {
        console.error("Failed to save flag:", err);
        setFlagsError(errorMessage(err, "Failed to save setting"));
      } finally {
        setSavingFlags((prev) => ({ ...prev, [flagName]: false }));
      }
    },
    [sessionId],
  );

  // Toggle "fresh context" for a loop conversation. PATCHes the loop
  // config so each scheduled run starts with a clean agent context (no history
  // injection, new ACP session). Updates local state optimistically from the
  // server-authoritative response.
  const handleFreshContextChange = useCallback(
    async (e) => {
      const newValue = e.target.checked;
      if (!sessionId) return;
      try {
        const data = await getSdkClient().sessions.loop.update(sessionId, {
          fresh_context: newValue,
        });
        setLoopConfig((prev) =>
          prev
            ? { ...prev, fresh_context: data.fresh_context ?? newValue }
            : prev,
        );
      } catch (err) {
        console.error("Failed to update fresh_context:", err);
      }
    },
    [sessionId],
  );

  const handleEnableCallback = useCallback(async () => {
    try {
      const data = await getSdkClient().sessions.createCallback(sessionId);
      setCallbackConfig(data);
      try {
        await navigator.clipboard.writeText(data.callback_url);
        setCallbackCopied(true);
        setTimeout(() => setCallbackCopied(false), 2000);
      } catch (e) {
        // Clipboard may not be available in some contexts
        console.warn("Failed to copy to clipboard:", e);
      }
    } catch (_err) {
      /* mirrors the prior !res.ok no-op */
    }
  }, [sessionId]);

  const handleCopyCallbackUrl = useCallback(async () => {
    if (callbackConfig?.callback_url) {
      try {
        await navigator.clipboard.writeText(callbackConfig.callback_url);
        setCallbackCopied(true);
        setTimeout(() => setCallbackCopied(false), 2000);
      } catch (e) {
        console.warn("Failed to copy to clipboard:", e);
      }
    }
  }, [callbackConfig]);

  const handleRotateCallback = useCallback(() => {
    setConfirmDialog({
      title: "Rotate Callback URL",
      message:
        "Rotate callback URL? The old URL will stop working immediately.",
      confirmLabel: "Rotate",
      confirmVariant: "danger",
      onConfirm: async () => {
        setConfirmDialog(null);
        try {
          const data = await getSdkClient().sessions.createCallback(sessionId);
          setCallbackConfig(data);
          try {
            await navigator.clipboard.writeText(data.callback_url);
            setCallbackCopied(true);
            setTimeout(() => setCallbackCopied(false), 2000);
          } catch (e) {
            console.warn("Failed to copy to clipboard:", e);
          }
        } catch (_err) {
          /* mirrors the prior !res.ok no-op */
        }
      },
    });
  }, [sessionId]);

  const handleRevokeCallback = useCallback(() => {
    setConfirmDialog({
      title: "Revoke Callback URL",
      message: "Revoke callback URL? It will stop working immediately.",
      confirmLabel: "Revoke",
      confirmVariant: "danger",
      onConfirm: async () => {
        setConfirmDialog(null);
        try {
          await getSdkClient().sessions.revokeCallback(sessionId);
          setCallbackConfig(null);
        } catch (_err) {
          /* mirrors the prior !res.ok no-op */
        }
      },
    });
  }, [sessionId]);

  // Animation state: track if we're closing to play exit animation
  const [isClosing, setIsClosing] = useState(false);
  const [shouldRender, setShouldRender] = useState(isOpen);

  // Handle open/close transitions
  useEffect(() => {
    if (isOpen) {
      // Opening: render immediately
      setShouldRender(true);
      setIsClosing(false);
    } else if (shouldRender) {
      // Closing: start exit animation, then unmount after delay
      setIsClosing(true);
      const timer = setTimeout(() => {
        setShouldRender(false);
        setIsClosing(false);
      }, 150); // Match animation duration
      return () => clearTimeout(timer);
    }
  }, [isOpen, shouldRender]);

  // Handle close with animation
  const handleClose = useCallback(() => {
    setIsClosing(true);
    setTimeout(() => {
      onClose();
    }, 150);
  }, [onClose]);

  if (!shouldRender) return null;

  // Side drawer docked to the RIGHT, above all other panels
  return html`
    <${Fragment}>
      <${Drawer}
        side="end"
        isClosing=${isClosing}
        onClose=${handleClose}
        widthClass="w-80"
        panelClass="bg-mitto-sidebar border-l border-mitto-border-1 h-full overflow-y-auto"
      >
        ${renderPanelContent()}
      <//>

      <${ConfirmDialog}
        isOpen=${!!confirmDialog}
        title=${confirmDialog?.title || "Confirm"}
        message=${confirmDialog?.message || ""}
        confirmLabel=${confirmDialog?.confirmLabel || "Yes"}
        cancelLabel=${confirmDialog?.cancelLabel || "Cancel"}
        confirmVariant=${confirmDialog?.confirmVariant || "primary"}
        onConfirm=${confirmDialog?.onConfirm}
        onCancel=${() => setConfirmDialog(null)}
      />
    <//>
  `;

  function renderPanelContent() {
    return html`
      <!-- Header -->
      <div
        class="p-4 border-b border-mitto-border-1 flex items-center justify-between shrink-0"
      >
        <h2 class="font-semibold text-lg">Properties</h2>
        <${Tooltip} tip="Close" placement="bottom">
          <button
            class="p-1 hover:bg-mitto-surface-hover rounded transition-colors"
            onClick=${handleClose}
            aria-label="Close"
          >
            <${CloseIcon} className="w-5 h-5" />
          </button>
        </${Tooltip}>
      </div>

      <!-- Content -->
      <div class="flex-1 overflow-y-auto p-4 space-y-6">
        <!-- Title Section -->
        <div>
          <label class="block text-sm font-medium text-mitto-text-secondary mb-2">
            Title
          </label>
          ${
            isEditingTitle
              ? html`
                <div class="flex items-center gap-2">
                  <input
                    ref=${titleInputRef}
                    type="text"
                    class="input input-sm flex-1"
                    value=${editedTitle}
                    onInput=${(e) => setEditedTitle(e.target.value)}
                    onKeyDown=${handleTitleKeyDown}
                    onBlur=${() => {
                      // Delay to allow click on save button
                      setTimeout(() => {
                        if (isEditingTitle && !isSavingTitle) {
                          setIsEditingTitle(false);
                        }
                      }, 150);
                    }}
                    disabled=${isSavingTitle}
                  />
                  <${Tooltip} tip="Save" placement="bottom">
                    <button
                      class="p-2 hover:bg-mitto-surface-hover rounded transition-colors text-mitto-success"
                      onClick=${handleSaveTitle}
                      aria-label="Save"
                      disabled=${isSavingTitle}
                    >
                      <${CheckIcon} className="w-4 h-4" />
                    </button>
                  </${Tooltip}>
                </div>
              `
              : html`
                <div class="flex items-center gap-2 group">
                  <span
                    class="flex-1 text-sm truncate cursor-pointer hover:text-mitto-accent transition-colors tooltip tooltip-bottom"
                    onClick=${handleStartEditTitle}
                    data-tip="Click to edit title"
                  >
                    ${sessionInfo?.name || "New conversation"}
                  </span>
                  <${Tooltip} tip="Edit title" placement="bottom">
                    <button
                      class="p-1 hover:bg-mitto-surface-hover rounded transition-colors opacity-0 group-hover:opacity-100"
                      onClick=${handleStartEditTitle}
                      aria-label="Edit title"
                    >
                      <${EditIcon} className="w-4 h-4" />
                    </button>
                  </${Tooltip}>
                </div>
              `
          }
        </div>

        <!-- Status & Runner Badges Section -->
        <div class="flex items-center gap-2 flex-wrap">
          <!-- Status Badge -->
          ${
            isStreaming
              ? html`
                  <span
                    class="badge badge-sm gap-1.5 bg-mitto-accent-500/20 text-mitto-accent"
                  >
                    <span
                      class="w-2 h-2 bg-mitto-accent-400 rounded-full streaming-indicator"
                    ></span>
                    Streaming
                  </span>
                `
              : sessionInfo?.archived
                ? html`
                    <span
                      class="badge badge-sm gap-1.5 bg-mitto-surface-3 text-mitto-text-secondary"
                    >
                      <span class="w-2 h-2 bg-slate-500 rounded-full"></span>
                      Archived
                    </span>
                  `
                : sessionInfo?.status === "active"
                  ? html`
                      <span
                        class="badge badge-sm gap-1.5 bg-green-500/20 text-mitto-success"
                      >
                        <span class="w-2 h-2 bg-green-400 rounded-full"></span>
                        Active
                      </span>
                    `
                  : html`
                      <span
                        class="badge badge-sm gap-1.5 bg-mitto-surface-3 text-mitto-text-secondary"
                      >
                        Stored
                      </span>
                    `
          }
          <!-- ACP Server Badge (e.g., "auggie") -->
          ${
            sessionInfo?.acp_server &&
            html`
              <span
                class="badge badge-sm bg-mitto-accent-500/20 text-mitto-accent tooltip tooltip-bottom"
                data-tip="ACP Server"
              >
                ${sessionInfo.acp_server}
              </span>
            `
          }
          <!-- Runner Type Badge (e.g., "exec") -->
          ${
            sessionInfo?.runner_type &&
            html`
              <span
                class="badge badge-sm tooltip tooltip-bottom ${sessionInfo.runner_restricted
                  ? "bg-yellow-500/20 text-mitto-warning"
                  : "bg-purple-500/20 text-purple-400"}"
                data-tip="${sessionInfo.runner_restricted
                  ? "Restricted execution mode"
                  : "Sandbox type"}"
              >
                ${sessionInfo.runner_type}
              </span>
            `
          }
        </div>

        <!-- Statistics Section (messages, time, processors, token usage) -->
        <div>
          <label class="block text-sm font-medium text-mitto-text-secondary mb-1">
            Statistics
          </label>
          <div class="text-xs text-mitto-text-secondary space-y-0.5">
            ${
              sessionInfo?.messageCount !== undefined &&
              html`
                <div class="flex justify-between">
                  <span>Messages</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.messageCount}</span
                  >
                </div>
              `
            }
            ${
              sessionInfo?.created_at &&
              html`
                <div class="flex justify-between">
                  <span>Created</span>
                  <span
                    class="text-mitto-text-300"
                    title=${new Date(sessionInfo.created_at).toLocaleString()}
                  >
                    ${formatTimeAgo(sessionInfo.created_at)}
                  </span>
                </div>
              `
            }
            ${
              sessionInfo?.processor_count > 0 &&
              html`
                <div
                  class="flex justify-between"
                  title=${sessionInfo?.processor_last_names?.length
                    ? `Last applied: ${sessionInfo.processor_last_names.join(", ")}`
                    : "No processors applied yet"}
                >
                  <span>Processors</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.processor_count}${sessionInfo?.processor_activations >
                    0
                      ? ` (${sessionInfo.processor_activations} runs)`
                      : ""}</span
                  >
                </div>
              `
            }
            ${
              sessionInfo?.mcp_calls_total > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Mitto MCP calls</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.mcp_calls_total}</span
                  >
                </div>
              `
            }
            ${
              sessionInfo?.mcp_ui_calls > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Mitto UI calls</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.mcp_ui_calls}</span
                  >
                </div>
              `
            }
            ${
              sessionInfo?.mcp_children_wait_calls > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Wait-for-children calls</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.mcp_children_wait_calls}</span
                  >
                </div>
              `
            }
            ${
              sessionInfo?.children_spawned > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Children</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.children_spawned}</span
                  >
                </div>
              `
            }
            ${
              sessionInfo?.child_wait_count > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Child wait</span>
                  <span class="text-mitto-text-300"
                    >${formatDuration(
                      sessionInfo.child_wait_total_ms /
                        sessionInfo.child_wait_count,
                    )}
                    avg (${formatDuration(sessionInfo.child_wait_total_ms)}
                    total)</span
                  >
                </div>
              `
            }
            ${
              sessionInfo?.turns > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Turns</span>
                  <span class="text-mitto-text-300">${sessionInfo.turns}</span>
                </div>
              `
            }
            ${
              sessionInfo?.acp_tool_calls > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Agent tool calls</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.acp_tool_calls}</span
                  >
                </div>
              `
            }
            ${
              (sessionInfo?.permissions_allowed > 0 ||
                sessionInfo?.permissions_denied > 0) &&
              html`
                <div class="flex justify-between">
                  <span>Permissions</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.permissions_allowed || 0} allowed /
                    ${sessionInfo.permissions_denied || 0} denied</span
                  >
                </div>
              `
            }
            ${
              sessionInfo?.errors > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Errors</span>
                  <span class="text-mitto-text-300">${sessionInfo.errors}</span>
                </div>
              `
            }
            ${
              sessionInfo?.images_uploaded > 0 &&
              html`
                <div class="flex justify-between">
                  <span>Images</span>
                  <span class="text-mitto-text-300"
                    >${sessionInfo.images_uploaded}</span
                  >
                </div>
              `
            }
          </div>

          ${
            sessionInfo?.usage &&
            html`
              <div class="mt-2 pt-2 border-t border-mitto-border-1/50">
                <!-- Context usage bar -->
                ${(() => {
                  const contextTokens = sessionInfo.usage.input_tokens;
                  const contextWindow = getContextWindowSize(currentModelId);
                  const pct = contextWindow
                    ? Math.min((contextTokens / contextWindow) * 100, 100)
                    : null;
                  const barColor =
                    pct === null
                      ? "bg-mitto-accent"
                      : pct > 80
                        ? "bg-mitto-danger"
                        : pct > 50
                          ? "bg-yellow-500"
                          : "bg-mitto-success";
                  const textColor =
                    pct === null
                      ? "text-mitto-text-300"
                      : pct > 80
                        ? "text-mitto-danger"
                        : pct > 50
                          ? "text-mitto-warning"
                          : "text-mitto-success";
                  return html`
                    <div class="mb-2">
                      <div class="flex justify-between items-baseline mb-1">
                        <span
                          class="text-xs font-medium text-mitto-text-secondary"
                          >Context</span
                        >
                        <span class="text-xs ${textColor}">
                          ${formatTokenCount(contextTokens)}${contextWindow
                            ? html` / ${formatTokenCount(contextWindow)}`
                            : ""}
                        </span>
                      </div>
                      <div
                        class="w-full h-1.5 bg-mitto-surface-3 rounded-full overflow-hidden"
                      >
                        <div
                          class="h-full ${barColor} rounded-full transition-all duration-300"
                          style="width: ${pct !== null ? pct : 0}%"
                        />
                      </div>
                      ${pct !== null &&
                      html`
                        <div class="text-right mt-0.5">
                          <span class="text-[10px] text-mitto-text-500"
                            >${pct.toFixed(0)}%</span
                          >
                        </div>
                      `}
                    </div>
                  `;
                })()}

                <!-- Last Turn Tokens breakdown -->
                <label
                  class="block text-xs font-medium text-mitto-text-500 mb-1"
                >
                  Last Turn Tokens
                </label>
                <div class="text-xs text-mitto-text-secondary space-y-0.5">
                  <div class="flex justify-between">
                    <span>Input</span>
                    <span class="text-mitto-text-300"
                      >${formatTokenCount(sessionInfo.usage.input_tokens)}</span
                    >
                  </div>
                  <div class="flex justify-between">
                    <span>Output</span>
                    <span class="text-mitto-text-300"
                      >${formatTokenCount(
                        sessionInfo.usage.output_tokens,
                      )}</span
                    >
                  </div>
                  <div class="flex justify-between">
                    <span>Total</span>
                    <span class="text-mitto-text-300 font-medium"
                      >${formatTokenCount(sessionInfo.usage.total_tokens)}</span
                    >
                  </div>
                  ${sessionInfo.usage.cached_read_tokens !== undefined &&
                  html`
                    <div class="flex justify-between">
                      <span>Cache Read</span>
                      <span class="text-mitto-text-300"
                        >${formatTokenCount(
                          sessionInfo.usage.cached_read_tokens,
                        )}</span
                      >
                    </div>
                  `}
                  ${sessionInfo.usage.cached_write_tokens !== undefined &&
                  html`
                    <div class="flex justify-between">
                      <span>Cache Write</span>
                      <span class="text-mitto-text-300"
                        >${formatTokenCount(
                          sessionInfo.usage.cached_write_tokens,
                        )}</span
                      >
                    </div>
                  `}
                  ${sessionInfo.usage.thought_tokens !== undefined &&
                  html`
                    <div class="flex justify-between">
                      <span>Thinking</span>
                      <span class="text-mitto-text-300"
                        >${formatTokenCount(
                          sessionInfo.usage.thought_tokens,
                        )}</span
                      >
                    </div>
                  `}
                </div>
              </div>
            `
          }

          ${
            sessionInfo?.usage_cumulative &&
            html`
              <div class="mt-2 pt-2 border-t border-mitto-border-1/50">
                <label
                  class="block text-xs font-medium text-mitto-text-500 mb-1"
                >
                  Cumulative Tokens
                </label>
                <div class="text-xs text-mitto-text-secondary space-y-0.5">
                  <div class="flex justify-between">
                    <span>Input</span>
                    <span class="text-mitto-text-300"
                      >${formatTokenCount(
                        sessionInfo.usage_cumulative.input_tokens,
                      )}</span
                    >
                  </div>
                  <div class="flex justify-between">
                    <span>Output</span>
                    <span class="text-mitto-text-300"
                      >${formatTokenCount(
                        sessionInfo.usage_cumulative.output_tokens,
                      )}</span
                    >
                  </div>
                  <div class="flex justify-between">
                    <span>Total</span>
                    <span class="text-mitto-text-300 font-medium"
                      >${formatTokenCount(
                        sessionInfo.usage_cumulative.total_tokens,
                      )}</span
                    >
                  </div>
                </div>
              </div>
            `
          }
        </div>

        <!-- Workspace Section -->
        <div>
          <label class="block text-sm font-medium text-mitto-text-secondary mb-2">
            Workspace
          </label>
          <div class="flex items-center gap-2 text-sm text-mitto-text-300">
            <${FolderIcon} className="w-4 h-4 shrink-0 text-mitto-text-500" />
            ${
              canRevealInFinder() && sessionInfo?.working_dir
                ? html`
                    <button
                      type="button"
                      class="truncate text-left hover:text-mitto-accent hover:underline transition-colors cursor-pointer"
                      title="Open in Finder: ${sessionInfo.working_dir}"
                      onClick=${() => revealInFinder(sessionInfo.working_dir)}
                    >
                      ${sessionInfo.working_dir}
                    </button>
                  `
                : html`
                    <span
                      class="truncate"
                      title=${sessionInfo?.working_dir || ""}
                    >
                      ${sessionInfo?.working_dir || "Unknown"}
                    </span>
                  `
            }
          </div>
        </div>

        <!-- Session Config Options Section -->
        <!-- Renders all config options dynamically based on type -->
        <!-- Supports: select (dropdown), toggle (future), unknown types gracefully ignored -->
        ${
          configOptions?.length > 0 &&
          configOptions.map(
            (configOption) => html`
              <div key=${configOption.id}>
                <label
                  class="block text-sm font-medium text-mitto-text-secondary mb-2"
                >
                  ${configOption.name}
                </label>

                <!-- Select type: dropdown with options -->
                ${configOption.type === "select" &&
                html`
                  <${ConfigOptionSelect}
                    configOption=${configOption}
                    onSetConfigOption=${onSetConfigOption}
                    isStreaming=${isStreaming}
                    showDescription=${true}
                    disableWhileStreaming=${true}
                  />
                `}

                <!-- Toggle type (future): boolean switch -->
                ${configOption.type === "toggle" &&
                html`
                  <div class="flex items-center justify-between">
                    <input
                      type="checkbox"
                      role="switch"
                      class="toggle toggle-primary"
                      checked=${configOption.current_value === "true"}
                      aria-checked=${configOption.current_value === "true"}
                      onChange=${() =>
                        onSetConfigOption?.(
                          configOption.id,
                          configOption.current_value === "true"
                            ? "false"
                            : "true",
                        )}
                      disabled=${isStreaming}
                      title=${isStreaming
                        ? `Cannot change ${configOption.name.toLowerCase()} while streaming`
                        : configOption.description ||
                          `Toggle ${configOption.name.toLowerCase()}`}
                    />
                  </div>
                  ${configOption.description &&
                  html`
                    <p class="mt-1 text-xs text-mitto-text-500">
                      ${configOption.description}
                    </p>
                  `}
                `}

                <!-- Unknown types: show current value as read-only -->
                ${configOption.type !== "select" &&
                configOption.type !== "toggle" &&
                html`
                  <div
                    class="w-full bg-mitto-surface-3/50 text-mitto-text-secondary rounded-lg px-3 py-2 text-sm border border-mitto-border-2"
                    title=${`Unsupported config type: ${configOption.type}`}
                  >
                    ${configOption.current_value || "(not set)"}
                  </div>
                  ${configOption.description &&
                  html`
                    <p class="mt-1 text-xs text-mitto-text-500">
                      ${configOption.description}
                    </p>
                  `}
                `}
              </div>
            `,
          )
        }

        <!-- Loop Prompts Section (only shown when configured and enabled) -->
        ${
          loopConfig?.enabled &&
          html`
            <div>
              <label
                class="block text-sm font-medium text-mitto-text-secondary mb-2"
              >
                Loop Prompts
              </label>
              <div class="flex items-center gap-2 text-sm text-mitto-text-300">
                <${LoopFilledIcon}
                  className="w-4 h-4 shrink-0 text-mitto-accent"
                />
                <span>${formatFrequency(loopConfig.frequency)}</span>
              </div>
              ${loopConfig.last_sent_at &&
              html`
                <p class="mt-1 text-xs text-mitto-text-500">
                  Last run:
                  ${new Date(loopConfig.last_sent_at).toLocaleString()}
                </p>
              `}
              ${loopConfig.next_scheduled_at &&
              html`
                <p class="mt-1 text-xs text-mitto-text-500">
                  Next run:
                  ${new Date(loopConfig.next_scheduled_at).toLocaleString()}
                  <span class="text-mitto-text-secondary ml-1">
                    (${formatRelativeTime(loopConfig.next_scheduled_at)})
                  </span>
                </p>
              `}
              <p class="mt-1 text-xs text-mitto-text-500">
                ${(loopConfig.max_iterations ?? 0) > 0
                  ? `Run ${loopConfig.iteration_count ?? 0} of ${loopConfig.max_iterations}`
                  : `${loopConfig.iteration_count ?? 0} run${(loopConfig.iteration_count ?? 0) !== 1 ? "s" : ""} · unlimited`}
              </p>
              <!-- Fresh context toggle: each scheduled run starts with a clean agent context -->
              <div class="mt-3 flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  id="properties-fresh-context-checkbox-${sessionId}"
                  checked=${!!loopConfig.fresh_context}
                  onInput=${handleFreshContextChange}
                  class="w-4 h-4 rounded border-mitto-border-3 text-mitto-accent focus:ring-mitto-accent-500 cursor-pointer shrink-0"
                  data-testid="properties-fresh-context-checkbox"
                />
                <label
                  for="properties-fresh-context-checkbox-${sessionId}"
                  class="text-mitto-text-300 cursor-pointer select-none"
                >
                  Start each run with a fresh context
                </label>
              </div>
            </div>
          `
        }

        <!-- MCP Tools Section (Collapsible) -->
        ${
          mcpTools &&
          mcpTools.length > 0 &&
          html`
            <div class="pt-4">
              <div
                class="collapse collapse-plus ${isMcpToolsExpanded
                  ? "collapse-open"
                  : "collapse-close"}"
              >
                <div
                  class="collapse-title flex items-center gap-2 px-0 py-0 pr-12 min-h-0 cursor-pointer text-sm font-medium text-mitto-text-secondary hover:text-mitto-text-300 transition-colors"
                  onClick=${() => setIsMcpToolsExpanded(!isMcpToolsExpanded)}
                >
                  <span>MCP Tools</span>
                  <span class="text-xs text-mitto-text-500"
                    >(${mcpTools.length})</span
                  >
                </div>

                <div class="collapse-content px-0">
                  ${isMcpToolsExpanded &&
                  html`
                    <div class="mt-3 space-y-1 max-h-64 overflow-y-auto">
                      ${mcpTools.map(
                        (tool) => html`
                          <div
                            key=${tool.name}
                            class="text-xs text-mitto-text-300 bg-mitto-surface-3/50 rounded px-2 py-1"
                            title=${tool.description || tool.name}
                          >
                            <span class="font-mono">${tool.name}</span>
                            ${tool.description &&
                            html`
                              <p class="text-mitto-text-500 mt-0.5 truncate">
                                ${tool.description}
                              </p>
                            `}
                          </div>
                        `,
                      )}
                    </div>
                  `}
                </div>
              </div>
            </div>
          `
        }

        <!-- Advanced Section (Collapsible) -->
        ${renderAdvancedSection()}
      </div>
    `;
  }

  function renderAdvancedSection() {
    // Only show if there are available flags or loop config (for callback URL)
    if ((!availableFlags || availableFlags.length === 0) && !loopConfig) {
      return null;
    }

    return html`
      <div class="pt-4">
        <!-- Callback URL Section (only for loop conversations) -->
        ${loopConfig &&
        html`
          <div class="mb-4">
            <label
              class="block text-sm font-medium text-mitto-text-secondary mb-2"
              >Callback URL</label
            >
            ${loopConfig.enabled
              ? html`
                  ${callbackConfig?.callback_url
                    ? html`
                <div class="flex items-center gap-1.5">
                  <${Tooltip} tip="Copy callback URL to clipboard" placement="top">
                    <button onClick=${handleCopyCallbackUrl} class="text-xs px-2 py-1 rounded bg-mitto-surface-3 hover:bg-mitto-surface-hover text-mitto-text-300 transition-colors">
                      ${callbackCopied ? "✓ Copied!" : "📋 Copy URL"}
                    </button>
                  </${Tooltip}>
                  <${Tooltip} tip="Generate new callback URL (invalidates old one)" placement="top"><button onClick=${handleRotateCallback} class="text-xs px-2 py-1 rounded bg-mitto-surface-3 hover:bg-mitto-surface-hover text-mitto-text-300 transition-colors">🔄 Rotate</button></${Tooltip}>
                  <${Tooltip} tip="Revoke callback URL" placement="top"><button onClick=${handleRevokeCallback} class="text-xs px-2 py-1 rounded bg-mitto-surface-3 hover:bg-red-900/50 text-mitto-text-secondary hover:text-red-300 transition-colors" aria-label="Revoke callback URL">✕</button></${Tooltip}>
                </div>
              `
                    : html`
                <${Tooltip} tip="Generate a callback URL for triggering this loop conversation externally" placement="top">
                  <button onClick=${handleEnableCallback} class="text-xs px-2 py-1 rounded bg-mitto-surface-3 hover:bg-mitto-surface-hover text-mitto-text-300 transition-colors">
                    🔗 Enable Callback URL
                  </button>
                </${Tooltip}>
              `}
                `
              : html`
                  ${callbackConfig?.callback_url
                    ? html`
                        <p class="text-xs text-mitto-text-muted mb-1.5 italic">
                          Preserved but inactive while loop is disabled
                        </p>
                        <div class="flex items-center gap-1.5">
                          <button
                            onClick=${handleCopyCallbackUrl}
                            class="text-xs px-2 py-1 rounded bg-mitto-surface-2 text-mitto-text-500 hover:text-mitto-text-secondary transition-colors"
                          >
                            ${callbackCopied ? "✓ Copied!" : "📋 Copy URL"}
                          </button>
                          <button
                            onClick=${handleRevokeCallback}
                            class="text-xs px-2 py-1 rounded bg-mitto-surface-2 text-mitto-text-500 hover:text-mitto-danger transition-colors"
                          >
                            ✕ Revoke
                          </button>
                        </div>
                      `
                    : html`
                        <p class="text-xs text-mitto-text-500">
                          No callback URL configured.
                        </p>
                      `}
                `}
          </div>
        `}

        <!-- Collapsible Section -->
        <div
          class="collapse collapse-plus ${isAdvancedExpanded
            ? "collapse-open"
            : "collapse-close"}"
        >
          <div
            class="collapse-title flex items-center gap-2 px-0 py-0 pr-12 min-h-0 cursor-pointer text-sm font-medium text-mitto-text-secondary hover:text-mitto-text-300 transition-colors"
            onClick=${() => setIsAdvancedExpanded(!isAdvancedExpanded)}
          >
            <span>Advanced</span>
          </div>

          <div class="collapse-content px-0">
            ${isAdvancedExpanded &&
            html`
              <div class="mt-3 space-y-3">
                ${isLoadingFlags
                  ? html`<div class="text-sm text-mitto-text-500">
                      Loading...
                    </div>`
                  : html`
                      ${flagsError &&
                      html`
                        <div
                          role="alert"
                          class="alert alert-error alert-soft text-sm"
                        >
                          ${flagsError}
                        </div>
                      `}
                      ${availableFlags.map((flag) => {
                        const currentValue = sessionSettings[flag.name];
                        const isSaving = savingFlags[flag.name];

                        return html`
                          <div key=${flag.name} class="flex items-start gap-3">
                            <div class="pt-0.5">
                              ${isSaving
                                ? html`<span
                                    class="loading loading-spinner w-5 h-5 text-mitto-accent"
                                  ></span>`
                                : html`
                                    <${TriStateCheckbox}
                                      value=${currentValue}
                                      onChange=${(newValue) =>
                                        handleFlagChange(flag.name, newValue)}
                                      title=${flag.description || flag.label}
                                    />
                                  `}
                            </div>
                            <div class="flex-1 min-w-0">
                              <label
                                class="block text-sm text-mitto-text-300 cursor-pointer"
                                onClick=${() =>
                                  !isSaving &&
                                  handleFlagChange(
                                    flag.name,
                                    currentValue === true ? false : true,
                                  )}
                              >
                                ${flag.label}
                              </label>
                              ${flag.description &&
                              html`
                                <p class="text-xs text-mitto-text-500 mt-0.5">
                                  ${flag.description}
                                </p>
                              `}
                            </div>
                          </div>
                        `;
                      })}
                    `}
              </div>
            `}
          </div>
        </div>
      </div>
    `;
  }
}
