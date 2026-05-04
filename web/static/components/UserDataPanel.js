// Mitto Web Interface - User Data Panel
// Fixed overlay panel on the RIGHT side for viewing and editing user data attributes.
// Uses the same overlay/panel UI pattern as ConversationPropertiesPanel.

const { html, useState, useEffect, useCallback, useRef } = window.preact;

import { CloseIcon, EditIcon, CheckIcon } from "./Icons.js";
import { apiUrl } from "../utils/api.js";
import { secureFetch, authFetch } from "../utils/csrf.js";

/**
 * UserDataPanel - Right-side overlay panel for viewing/editing user data attributes.
 *
 * @param {boolean} isOpen - Whether the panel is open
 * @param {Function} onClose - Callback to close the panel
 * @param {string} sessionId - Active session ID
 * @param {Object} sessionInfo - Session info object (needs sessionInfo.working_dir)
 */
export function UserDataPanel({ isOpen, onClose, sessionId, sessionInfo }) {
  const [userData, setUserData] = useState({ attributes: [] });
  const [userDataSchema, setUserDataSchema] = useState(null);
  const [isLoadingUserData, setIsLoadingUserData] = useState(false);
  const [editingAttribute, setEditingAttribute] = useState(null);
  const [editedAttributeValue, setEditedAttributeValue] = useState("");
  const [isSavingAttribute, setIsSavingAttribute] = useState(false);
  const [userDataError, setUserDataError] = useState(null);
  const attributeInputRef = useRef(null);

  // Animation state: track if we're closing to play exit animation
  const [isClosing, setIsClosing] = useState(false);
  const [shouldRender, setShouldRender] = useState(isOpen);

  // Handle open/close transitions
  useEffect(() => {
    if (isOpen) {
      setShouldRender(true);
      setIsClosing(false);
    } else if (shouldRender) {
      setIsClosing(true);
      const timer = setTimeout(() => {
        setShouldRender(false);
        setIsClosing(false);
      }, 150);
      return () => clearTimeout(timer);
    }
  }, [isOpen, shouldRender]);

  // Reset state when session changes or panel closes
  useEffect(() => {
    setEditingAttribute(null);
    setUserDataError(null);
  }, [sessionId, isOpen]);

  // Fetch user data and schema when panel opens
  useEffect(() => {
    if (!isOpen || !sessionId || !sessionInfo?.working_dir) return;

    const fetchData = async () => {
      setIsLoadingUserData(true);
      setUserDataError(null);

      try {
        const [userDataRes, schemaRes] = await Promise.all([
          authFetch(apiUrl(`/api/sessions/${sessionId}/user-data`)),
          authFetch(
            apiUrl(
              `/api/workspace/user-data-schema?working_dir=${encodeURIComponent(sessionInfo.working_dir)}`,
            ),
          ),
        ]);

        if (userDataRes.ok) {
          const data = await userDataRes.json();
          setUserData(data);
        }

        if (schemaRes.ok) {
          const schema = await schemaRes.json();
          setUserDataSchema(schema);
        } else if (schemaRes.status === 404) {
          setUserDataSchema({ fields: [] });
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

  // Focus attribute input when entering edit mode
  useEffect(() => {
    if (editingAttribute && attributeInputRef.current) {
      attributeInputRef.current.focus();
      attributeInputRef.current.select();
    }
  }, [editingAttribute]);

  // Handle close with animation
  const handleClose = useCallback(() => {
    setIsClosing(true);
    setTimeout(() => {
      onClose();
    }, 150);
  }, [onClose]);

  const handleStartEditAttribute = useCallback((attr) => {
    setEditingAttribute(attr.name);
    setEditedAttributeValue(attr.value || "");
  }, []);

  const handleSaveAttribute = useCallback(async () => {
    if (!sessionId || isSavingAttribute || !editingAttribute) return;

    setIsSavingAttribute(true);
    setUserDataError(null);

    try {
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
  }, [sessionId, editingAttribute, editedAttributeValue, userData.attributes, isSavingAttribute]);

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

  const getAttributeValue = useCallback(
    (name) => {
      const attr = userData.attributes.find((a) => a.name === name);
      return attr?.value || "";
    },
    [userData.attributes],
  );

  const hasSchema = userDataSchema && userDataSchema.fields?.length > 0;

  if (!shouldRender) return null;

  return html`
    <div
      class="fixed inset-0 z-50 flex"
      onClick=${(e) => {
        if (e.target === e.currentTarget) handleClose();
      }}
    >
      <!-- Backdrop on the left -->
      <div
        class="flex-1 bg-black/50 properties-backdrop ${isClosing ? "closing" : ""}"
        onClick=${handleClose}
      />
      <!-- Panel on the right -->
      <div
        class="w-80 bg-mitto-sidebar flex-shrink-0 shadow-2xl h-full overflow-y-auto border-l border-slate-700 properties-panel ${isClosing ? "closing" : ""}"
      >
        <!-- Header -->
        <div class="p-4 border-b border-slate-700 flex items-center justify-between flex-shrink-0">
          <h2 class="font-semibold text-lg">User Data</h2>
          <button
            class="p-1 hover:bg-slate-700 rounded transition-colors"
            onClick=${handleClose}
            title="Close"
          >
            <${CloseIcon} className="w-5 h-5" />
          </button>
        </div>

        <!-- Content -->
        <div class="flex-1 overflow-y-auto p-4">
          ${renderContent()}
        </div>
      </div>
    </div>
  `;

  function renderContent() {
    if (isLoadingUserData) {
      return html`<div class="text-sm text-slate-500">Loading...</div>`;
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
              <label class="block text-xs text-slate-500 mb-1" title=${field.description || ""}>
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
                            if (editingAttribute === field.name && !isSavingAttribute) {
                              setEditingAttribute(null);
                            }
                          }, 150);
                        }}
                        disabled=${isSavingAttribute}
                        placeholder=${field.description
                          ? field.description
                          : field.type === "url"
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
                              class="flex-1 text-sm truncate ${!value ? "text-slate-500 italic" : ""}"
                              title=${value}
                            >
                              ${value || "Not set"}
                            </span>
                          `}
                      <button
                        class="p-1 hover:bg-slate-700 rounded transition-colors opacity-0 group-hover:opacity-100"
                        onClick=${() => handleStartEditAttribute({ name: field.name, value })}
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
