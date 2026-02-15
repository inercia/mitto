// Mitto Web Interface - Conversation Properties Panel
// Fixed overlay panel on the RIGHT side for viewing and editing conversation properties
// Always appears above other panels (like the conversations sidebar)

const { html, useState, useEffect, useCallback, useRef, Fragment } =
  window.preact;

import {
  CloseIcon,
  EditIcon,
  CheckIcon,
  FolderIcon,
  PeriodicFilledIcon,
} from "./Icons.js";
import { apiUrl } from "../utils/api.js";
import { secureFetch, authFetch } from "../utils/csrf.js";

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
    Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), hours, minutes, 0),
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

/**
 * ConfigOptionSelect - Select dropdown for a config option with immediate description update
 * Tracks local selected value so description updates immediately on change
 */
function ConfigOptionSelect({ configOption, onSetConfigOption, isStreaming }) {
  // Track local selected value for immediate description update
  const [localValue, setLocalValue] = useState(configOption.current_value);

  // Sync local value when server confirms the change
  useEffect(() => {
    setLocalValue(configOption.current_value);
  }, [configOption.current_value]);

  const handleChange = useCallback(
    (e) => {
      const newValue = e.target.value;
      setLocalValue(newValue); // Update immediately for description
      onSetConfigOption?.(configOption.id, newValue);
    },
    [configOption.id, onSetConfigOption],
  );

  // Find the option matching the local value for description display
  const selectedOpt = configOption.options?.find((o) => o.value === localValue);

  return html`
    <select
      class="w-full bg-slate-700 text-slate-200 rounded-lg px-3 py-2 text-sm border border-slate-600 focus:border-blue-500 focus:ring-1 focus:ring-blue-500 outline-none cursor-pointer"
      value=${localValue || ""}
      onChange=${handleChange}
      disabled=${isStreaming}
      title=${isStreaming
        ? `Cannot change ${configOption.name.toLowerCase()} while streaming`
        : configOption.description || `Select ${configOption.name.toLowerCase()}`}
    >
      ${configOption.options?.map(
        (opt) => html`
          <option value=${opt.value} title=${opt.description || ""}>
            ${opt.name}
          </option>
        `,
      )}
    </select>
    ${selectedOpt?.description &&
    html`
      <p class="mt-1 text-xs text-slate-500">${selectedOpt.description}</p>
    `}
  `;
}

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
}) {
  // Title editing state
  const [isEditingTitle, setIsEditingTitle] = useState(false);
  const [editedTitle, setEditedTitle] = useState("");
  const [isSavingTitle, setIsSavingTitle] = useState(false);
  const titleInputRef = useRef(null);

  // User data state
  const [userData, setUserData] = useState({ attributes: [] });
  const [userDataSchema, setUserDataSchema] = useState(null);
  const [isLoadingUserData, setIsLoadingUserData] = useState(false);
  const [editingAttribute, setEditingAttribute] = useState(null);
  const [editedAttributeValue, setEditedAttributeValue] = useState("");
  const [isSavingAttribute, setIsSavingAttribute] = useState(false);
  const [userDataError, setUserDataError] = useState(null);
  const attributeInputRef = useRef(null);

  // Periodic config state
  const [periodicConfig, setPeriodicConfig] = useState(null);

  // State for dynamic relative time updates (triggers re-render every 30 seconds)
  const [, setTimeNow] = useState(Date.now());

  // Update relative time display every 30 seconds while panel is open
  useEffect(() => {
    if (!isOpen || !periodicConfig?.next_scheduled_at) {
      return;
    }

    const intervalId = setInterval(() => {
      setTimeNow(Date.now());
    }, 30000); // Update every 30 seconds

    return () => clearInterval(intervalId);
  }, [isOpen, periodicConfig?.next_scheduled_at]);

  // Reset state when session changes or panel closes
  useEffect(() => {
    setIsEditingTitle(false);
    setEditingAttribute(null);
    setUserDataError(null);
    setPeriodicConfig(null);
  }, [sessionId, isOpen]);

  // Fetch user data, schema, and periodic config when panel opens
  useEffect(() => {
    if (!isOpen || !sessionId || !sessionInfo?.working_dir) return;

    const fetchData = async () => {
      setIsLoadingUserData(true);
      setUserDataError(null);

      try {
        // Fetch user data, schema, and periodic config in parallel
        const [userDataRes, schemaRes, periodicRes] = await Promise.all([
          authFetch(apiUrl(`/api/sessions/${sessionId}/user-data`)),
          authFetch(
            apiUrl(
              `/api/workspace/user-data-schema?working_dir=${encodeURIComponent(sessionInfo.working_dir)}`,
            ),
          ),
          authFetch(apiUrl(`/api/sessions/${sessionId}/periodic`)),
        ]);

        if (userDataRes.ok) {
          const data = await userDataRes.json();
          setUserData(data);
        }

        if (schemaRes.ok) {
          const schema = await schemaRes.json();
          setUserDataSchema(schema);
        } else if (schemaRes.status === 404) {
          // No schema defined
          setUserDataSchema({ fields: [] });
        }

        if (periodicRes.ok) {
          const periodic = await periodicRes.json();
          setPeriodicConfig(periodic);
        } else {
          // No periodic config or error - clear state
          setPeriodicConfig(null);
        }
      } catch (err) {
        console.error("Failed to fetch user data:", err);
        setUserDataError("Failed to load user data");
      } finally {
        setIsLoadingUserData(false);
      }
    };

    fetchData();
  }, [isOpen, sessionId, sessionInfo?.working_dir]);

  // Focus title input when entering edit mode
  useEffect(() => {
    if (isEditingTitle && titleInputRef.current) {
      titleInputRef.current.focus();
      titleInputRef.current.select();
    }
  }, [isEditingTitle]);

  // Focus attribute input when entering edit mode
  useEffect(() => {
    if (editingAttribute && attributeInputRef.current) {
      attributeInputRef.current.focus();
      attributeInputRef.current.select();
    }
  }, [editingAttribute]);

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
        setIsEditingTitle(false);
      }
    },
    [handleSaveTitle],
  );

  // Handle attribute edit start
  const handleStartEditAttribute = useCallback((attr) => {
    setEditingAttribute(attr.name);
    setEditedAttributeValue(attr.value || "");
  }, []);

  // Handle attribute save
  const handleSaveAttribute = useCallback(async () => {
    if (!sessionId || isSavingAttribute || !editingAttribute) return;

    setIsSavingAttribute(true);
    setUserDataError(null);

    try {
      // Update the attribute in the list
      const updatedAttributes = [...userData.attributes];
      const existingIndex = updatedAttributes.findIndex(
        (a) => a.name === editingAttribute,
      );

      if (existingIndex >= 0) {
        updatedAttributes[existingIndex] = {
          name: editingAttribute,
          value: editedAttributeValue,
        };
      } else {
        updatedAttributes.push({
          name: editingAttribute,
          value: editedAttributeValue,
        });
      }

      const res = await secureFetch(
        apiUrl(`/api/sessions/${sessionId}/user-data`),
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ attributes: updatedAttributes }),
        },
      );

      if (res.ok) {
        const data = await res.json();
        setUserData(data);
        setEditingAttribute(null);
      } else {
        const errorData = await res.json().catch(() => ({}));
        setUserDataError(errorData.message || "Failed to save attribute");
      }
    } catch (err) {
      console.error("Failed to save attribute:", err);
      setUserDataError("Failed to save attribute");
    } finally {
      setIsSavingAttribute(false);
    }
  }, [
    sessionId,
    editingAttribute,
    editedAttributeValue,
    userData.attributes,
    isSavingAttribute,
  ]);

  // Handle attribute key press
  const handleAttributeKeyDown = useCallback(
    (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        handleSaveAttribute();
      } else if (e.key === "Escape") {
        setEditingAttribute(null);
      }
    },
    [handleSaveAttribute],
  );

  // Get attribute value by name
  const getAttributeValue = useCallback(
    (name) => {
      const attr = userData.attributes.find((a) => a.name === name);
      return attr?.value || "";
    },
    [userData.attributes],
  );

  // Check if schema has fields
  const hasSchema = userDataSchema && userDataSchema.fields?.length > 0;

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

  // Fixed overlay on the RIGHT side, above all other panels
  return html`
    <div
      class="fixed inset-0 z-50 flex"
      onClick=${(e) => {
        if (e.target === e.currentTarget) handleClose();
      }}
    >
      <!-- Backdrop on the left -->
      <div
        class="flex-1 bg-black/50 properties-backdrop ${isClosing
          ? "closing"
          : ""}"
        onClick=${handleClose}
      />
      <!-- Panel on the right -->
      <div
        class="w-80 bg-mitto-sidebar flex-shrink-0 shadow-2xl h-full overflow-y-auto border-l border-slate-700 properties-panel ${isClosing
          ? "closing"
          : ""}"
      >
        ${renderPanelContent()}
      </div>
    </div>
  `;

  function renderPanelContent() {
    return html`
      <!-- Header -->
      <div
        class="p-4 border-b border-slate-700 flex items-center justify-between flex-shrink-0"
      >
        <h2 class="font-semibold text-lg">Properties</h2>
        <button
          class="p-1 hover:bg-slate-700 rounded transition-colors"
          onClick=${handleClose}
          title="Close"
        >
          <${CloseIcon} className="w-5 h-5" />
        </button>
      </div>

      <!-- Content -->
      <div class="flex-1 overflow-y-auto p-4 space-y-6">
        <!-- Title Section -->
        <div>
          <label class="block text-sm font-medium text-slate-400 mb-2">
            Title
          </label>
          ${isEditingTitle
            ? html`
                <div class="flex items-center gap-2">
                  <input
                    ref=${titleInputRef}
                    type="text"
                    class="flex-1 bg-slate-800 border border-slate-600 rounded px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
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
                  <button
                    class="p-2 hover:bg-slate-700 rounded transition-colors text-green-400"
                    onClick=${handleSaveTitle}
                    title="Save"
                    disabled=${isSavingTitle}
                  >
                    <${CheckIcon} className="w-4 h-4" />
                  </button>
                </div>
              `
            : html`
                <div class="flex items-center gap-2 group">
                  <span class="flex-1 text-sm truncate">
                    ${sessionInfo?.name || "New conversation"}
                  </span>
                  <button
                    class="p-1 hover:bg-slate-700 rounded transition-colors opacity-0 group-hover:opacity-100"
                    onClick=${handleStartEditTitle}
                    title="Edit title"
                  >
                    <${EditIcon} className="w-4 h-4" />
                  </button>
                </div>
              `}
        </div>

        <!-- Status & Runner Badges Section -->
        <div class="flex items-center gap-2 flex-wrap">
          <!-- Status Badge -->
          ${isStreaming
            ? html`
                <span
                  class="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-blue-500/20 text-blue-400 text-xs"
                >
                  <span
                    class="w-2 h-2 bg-blue-400 rounded-full streaming-indicator"
                  ></span>
                  Streaming
                </span>
              `
            : sessionInfo?.archived
              ? html`
                  <span
                    class="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-slate-700 text-slate-400 text-xs"
                  >
                    <span class="w-2 h-2 bg-slate-500 rounded-full"></span>
                    Archived
                  </span>
                `
              : sessionInfo?.status === "active"
                ? html`
                    <span
                      class="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-green-500/20 text-green-400 text-xs"
                    >
                      <span class="w-2 h-2 bg-green-400 rounded-full"></span>
                      Active
                    </span>
                  `
                : html`
                    <span
                      class="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-slate-700 text-slate-400 text-xs"
                    >
                      Stored
                    </span>
                  `}
          <!-- ACP Server Badge (e.g., "auggie") -->
          ${sessionInfo?.acp_server &&
          html`
            <span
              class="inline-flex items-center px-2 py-1 rounded bg-blue-500/20 text-blue-400 text-xs"
              title="ACP Server"
            >
              ${sessionInfo.acp_server}
            </span>
          `}
          <!-- Runner Type Badge (e.g., "exec") -->
          ${sessionInfo?.runner_type &&
          html`
            <span
              class="inline-flex items-center px-2 py-1 rounded ${sessionInfo.runner_restricted
                ? "bg-yellow-500/20 text-yellow-400"
                : "bg-purple-500/20 text-purple-400"} text-xs"
              title="${sessionInfo.runner_restricted
                ? "Restricted execution mode"
                : "Sandbox type"}"
            >
              ${sessionInfo.runner_type}
            </span>
          `}
        </div>

        <!-- Session Config Options Section -->
        <!-- Renders all config options dynamically based on type -->
        <!-- Supports: select (dropdown), toggle (future), unknown types gracefully ignored -->
        ${configOptions?.length > 0 &&
        configOptions.map(
          (configOption) => html`
            <div key=${configOption.id}>
              <label class="block text-sm font-medium text-slate-400 mb-2">
                ${configOption.name}
              </label>

              <!-- Select type: dropdown with options -->
              ${configOption.type === "select" &&
              html`
                <${ConfigOptionSelect}
                  configOption=${configOption}
                  onSetConfigOption=${onSetConfigOption}
                  isStreaming=${isStreaming}
                />
              `}

              <!-- Toggle type (future): boolean switch -->
              ${configOption.type === "toggle" &&
              html`
                <div class="flex items-center justify-between">
                  <button
                    class="relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 focus:ring-offset-slate-800 ${configOption.current_value === "true" ? "bg-blue-600" : "bg-slate-600"}"
                    role="switch"
                    aria-checked=${configOption.current_value === "true"}
                    onClick=${() =>
                      onSetConfigOption?.(
                        configOption.id,
                        configOption.current_value === "true" ? "false" : "true",
                      )}
                    disabled=${isStreaming}
                    title=${isStreaming
                      ? `Cannot change ${configOption.name.toLowerCase()} while streaming`
                      : configOption.description || `Toggle ${configOption.name.toLowerCase()}`}
                  >
                    <span
                      class="pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out ${configOption.current_value === "true" ? "translate-x-5" : "translate-x-0"}"
                    />
                  </button>
                </div>
                ${configOption.description &&
                html`
                  <p class="mt-1 text-xs text-slate-500">
                    ${configOption.description}
                  </p>
                `}
              `}

              <!-- Unknown types: show current value as read-only -->
              ${configOption.type !== "select" &&
              configOption.type !== "toggle" &&
              html`
                <div
                  class="w-full bg-slate-700/50 text-slate-400 rounded-lg px-3 py-2 text-sm border border-slate-600"
                  title=${`Unsupported config type: ${configOption.type}`}
                >
                  ${configOption.current_value || "(not set)"}
                </div>
                ${configOption.description &&
                html`
                  <p class="mt-1 text-xs text-slate-500">
                    ${configOption.description}
                  </p>
                `}
              `}
            </div>
          `,
        )}

        <!-- Periodic Prompts Section (only shown when configured and enabled) -->
        ${periodicConfig?.enabled &&
        html`
          <div>
            <label class="block text-sm font-medium text-slate-400 mb-2">
              Periodic Prompts
            </label>
            <div class="flex items-center gap-2 text-sm text-slate-300">
              <${PeriodicFilledIcon}
                className="w-4 h-4 flex-shrink-0 text-blue-400"
              />
              <span>${formatFrequency(periodicConfig.frequency)}</span>
            </div>
            ${periodicConfig.last_sent_at &&
            html`
              <p class="mt-1 text-xs text-slate-500">
                Last run:
                ${new Date(periodicConfig.last_sent_at).toLocaleString()}
              </p>
            `}
            ${periodicConfig.next_scheduled_at &&
            html`
              <p class="mt-1 text-xs text-slate-500">
                Next run:
                ${new Date(periodicConfig.next_scheduled_at).toLocaleString()}
                <span class="text-slate-400 ml-1">
                  (${formatRelativeTime(periodicConfig.next_scheduled_at)})
                </span>
              </p>
            `}
          </div>
        `}

        <!-- Workspace Section -->
        <div>
          <label class="block text-sm font-medium text-slate-400 mb-2">
            Workspace
          </label>
          <div class="flex items-center gap-2 text-sm text-slate-300">
            <${FolderIcon} className="w-4 h-4 flex-shrink-0 text-slate-500" />
            <span class="truncate" title=${sessionInfo?.working_dir || ""}>
              ${sessionInfo?.working_dir || "Unknown"}
            </span>
          </div>
        </div>

        <!-- User Data Section -->
        <div>
          <label class="block text-sm font-medium text-slate-400 mb-2">
            User Data
          </label>
          ${renderUserDataSection()}
        </div>
      </div>
    `;
  }

  function renderUserDataSection() {
    if (isLoadingUserData) {
      return html`
        <div class="text-sm text-slate-500">Loading...</div>
      `;
    }

    if (!hasSchema) {
      return html`
        <div class="text-sm text-slate-500 italic">
          No user data schema configured for this workspace.
        </div>
      `;
    }

    return html`
      <div class="space-y-3">
        ${userDataError &&
        html`
          <div class="text-sm text-red-400 bg-red-900/20 rounded px-2 py-1">
            ${userDataError}
          </div>
        `}
        ${userDataSchema.fields.map((field) => {
          const value = getAttributeValue(field.name);
          const isEditing = editingAttribute === field.name;

          return html`
            <div key=${field.name}>
              <label class="block text-xs text-slate-500 mb-1">
                ${field.name}
              </label>
              ${isEditing
                ? html`
                    <div class="flex items-center gap-2">
                      <input
                        ref=${attributeInputRef}
                        type=${field.type === "url" ? "url" : "text"}
                        class="flex-1 bg-slate-800 border border-slate-600 rounded px-2 py-1 text-sm focus:outline-none focus:border-blue-500"
                        value=${editedAttributeValue}
                        onInput=${(e) => setEditedAttributeValue(e.target.value)}
                        onKeyDown=${handleAttributeKeyDown}
                        onBlur=${() => {
                          setTimeout(() => {
                            if (
                              editingAttribute === field.name &&
                              !isSavingAttribute
                            ) {
                              setEditingAttribute(null);
                            }
                          }, 150);
                        }}
                        disabled=${isSavingAttribute}
                        placeholder=${field.type === "url"
                          ? "https://..."
                          : "Enter value..."}
                      />
                      <button
                        class="p-1 hover:bg-slate-700 rounded transition-colors text-green-400"
                        onClick=${handleSaveAttribute}
                        title="Save"
                        disabled=${isSavingAttribute}
                      >
                        <${CheckIcon} className="w-4 h-4" />
                      </button>
                    </div>
                  `
                : html`
                    <div class="flex items-center gap-2 group">
                      ${field.type === "url" && value
                        ? html`
                            <a
                              href=${value}
                              target="_blank"
                              rel="noopener noreferrer"
                              class="flex-1 text-sm text-blue-400 hover:underline truncate"
                              title=${value}
                            >
                              ${value}
                            </a>
                          `
                        : html`
                            <span
                              class="flex-1 text-sm truncate ${!value
                                ? "text-slate-500 italic"
                                : ""}"
                              title=${value}
                            >
                              ${value || "Not set"}
                            </span>
                          `}
                      <button
                        class="p-1 hover:bg-slate-700 rounded transition-colors opacity-0 group-hover:opacity-100"
                        onClick=${() =>
                          handleStartEditAttribute({ name: field.name, value })}
                        title="Edit"
                      >
                        <${EditIcon} className="w-4 h-4" />
                      </button>
                    </div>
                  `}
            </div>
          `;
        })}
      </div>
    `;
  }
}

