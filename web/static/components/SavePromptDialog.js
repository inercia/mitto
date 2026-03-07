// Mitto Web Interface - Save Prompt Dialog Component
// Modal dialog for saving the current prompt text as a markdown file with frontmatter

const { useState, useEffect, useCallback, useRef, html } = window.preact;

import { hasNativeFolderPicker, pickFolder } from "../utils/native.js";
import { secureFetch, authFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { CloseIcon } from "./Icons.js";

/**
 * Sanitize a prompt name into a safe filename.
 * Lowercases, replaces spaces/special chars with hyphens, adds .md extension.
 * @param {string} name - The prompt name
 * @returns {string} A sanitized filename like "my-prompt-name.md"
 */
function nameToFilename(name) {
  return (
    name
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9\s-]/g, "")
      .replace(/\s+/g, "-")
      .replace(/-+/g, "-")
      .replace(/^-|-$/g, "") + ".md"
  );
}

/**
 * Build the file content with YAML frontmatter.
 * @param {string} name - Prompt name
 * @param {string} description - Optional description
 * @param {string} promptText - The prompt body text
 * @returns {string} Markdown content with frontmatter
 */
function buildFileContent(name, description, promptText) {
  let frontmatter = `---\nname: "${name.replace(/"/g, '\\"')}"`;
  if (description.trim()) {
    frontmatter += `\ndescription: "${description.trim().replace(/"/g, '\\"')}"`;
  }
  frontmatter += "\n---\n\n";
  return frontmatter + promptText;
}

/**
 * SavePromptDialog component - saves the current prompt text as a markdown file
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the dialog is visible
 * @param {Function} props.onClose - Callback to close the dialog
 * @param {string} props.promptText - Current prompt text from composition area
 * @param {string} props.workingDir - Current workspace directory
 */
export function SavePromptDialog({ isOpen, onClose, promptText, workingDir }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [filename, setFilename] = useState("");
  const [directory, setDirectory] = useState("");
  const [filenameManuallyEdited, setFilenameManuallyEdited] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState("");
  const [showOverwriteConfirm, setShowOverwriteConfirm] = useState(false);

  const nameInputRef = useRef(null);

  // Reset state when dialog opens
  useEffect(() => {
    if (isOpen) {
      setName("");
      setDescription("");
      setFilename("");
      setFilenameManuallyEdited(false);
      setIsSaving(false);
      setError("");
      setShowOverwriteConfirm(false);
      // Set default directory to workspace/.mitto/prompts/
      const defaultDir = workingDir
        ? workingDir.replace(/\/+$/, "") + "/.mitto/prompts"
        : "";
      setDirectory(defaultDir);
      // Focus the name input after render
      setTimeout(() => {
        if (nameInputRef.current) {
          nameInputRef.current.focus();
        }
      }, 100);
    }
  }, [isOpen, workingDir]);

  // Auto-generate filename from name (unless manually edited)
  useEffect(() => {
    if (!filenameManuallyEdited && name.trim()) {
      setFilename(nameToFilename(name));
    } else if (!filenameManuallyEdited && !name.trim()) {
      setFilename("");
    }
  }, [name, filenameManuallyEdited]);

  const fullPath = directory && filename ? directory + "/" + filename : "";

  const handleFilenameChange = useCallback((e) => {
    setFilenameManuallyEdited(true);
    setFilename(e.target.value);
  }, []);

  const handleBrowse = useCallback(async () => {
    try {
      const folder = await pickFolder();
      if (folder) {
        setDirectory(folder);
      }
    } catch (err) {
      console.error("[SavePromptDialog] pickFolder error:", err);
    }
  }, []);

  const handleClose = useCallback(() => {
    if (!isSaving) {
      onClose?.();
    }
  }, [isSaving, onClose]);

  const handleBackdropClick = useCallback(
    (e) => {
      if (e.target === e.currentTarget && !isSaving) {
        onClose?.();
      }
    },
    [isSaving, onClose],
  );

  // Core save logic - called directly or after overwrite confirmation
  const doSave = useCallback(async () => {
    if (!fullPath) return;

    setIsSaving(true);
    setError("");

    try {
      const content = buildFileContent(name, description, promptText);
      const response = await secureFetch(apiUrl("/api/save-file-to-path"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: fullPath, content }),
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `Save failed (${response.status})`);
      }

      // Success - close dialog
      onClose?.();
    } catch (err) {
      console.error("[SavePromptDialog] save error:", err);
      setError(err.message || "Failed to save file");
    } finally {
      setIsSaving(false);
    }
  }, [fullPath, name, description, promptText, onClose]);

  // Handle save button click - check for existing file first
  const handleSave = useCallback(async () => {
    // Validate
    if (!name.trim()) {
      setError("Name is required");
      return;
    }
    if (!fullPath) {
      setError("Invalid file path");
      return;
    }

    setError("");
    setIsSaving(true);

    try {
      // Check if file already exists
      const checkUrl =
        apiUrl("/api/check-file-exists") +
        "?path=" +
        encodeURIComponent(fullPath);
      const checkResponse = await authFetch(checkUrl);

      if (checkResponse.ok) {
        const data = await checkResponse.json();
        if (data.exists) {
          // File exists - ask for overwrite confirmation
          setIsSaving(false);
          setShowOverwriteConfirm(true);
          return;
        }
      }

      // File doesn't exist - save directly
      setIsSaving(false);
      await doSave();
    } catch (err) {
      console.error("[SavePromptDialog] check error:", err);
      // If check fails, try to save anyway
      setIsSaving(false);
      await doSave();
    }
  }, [name, fullPath, doSave]);

  const handleOverwriteConfirm = useCallback(() => {
    setShowOverwriteConfirm(false);
    doSave();
  }, [doSave]);

  const handleOverwriteCancel = useCallback(() => {
    setShowOverwriteConfirm(false);
  }, []);

  // Handle Enter key in form fields
  const handleKeyDown = useCallback(
    (e) => {
      if (e.key === "Enter" && !e.shiftKey && name.trim() && fullPath) {
        e.preventDefault();
        handleSave();
      } else if (e.key === "Escape") {
        e.preventDefault();
        handleClose();
      }
    },
    [handleSave, handleClose, name, fullPath],
  );

  if (!isOpen) return null;

  const canSave = name.trim() && fullPath && !isSaving;

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${handleBackdropClick}
      data-testid="save-prompt-dialog-backdrop"
    >
      <div
        class="bg-mitto-sidebar rounded-xl w-[500px] max-w-[90vw] overflow-hidden shadow-2xl flex flex-col"
        onClick=${(e) => e.stopPropagation()}
        data-testid="save-prompt-dialog"
      >
        <!-- Header -->
        <div
          class="flex items-center justify-between p-4 border-b border-slate-700"
        >
          <h3 class="text-lg font-semibold">Save Prompt</h3>
          <button
            onClick=${handleClose}
            disabled=${isSaving}
            class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${isSaving
              ? "opacity-50 cursor-not-allowed"
              : ""}"
            data-testid="save-prompt-dialog-close"
          >
            <${CloseIcon} className="w-5 h-5" />
          </button>
        </div>

        <!-- Content -->
        <div class="p-4 space-y-4">
          <!-- Name field -->
          <div class="space-y-1.5">
            <label class="block text-sm font-medium text-gray-300">
              Name
              <span class="text-red-400 ml-0.5">*</span>
            </label>
            <input
              ref=${nameInputRef}
              type="text"
              value=${name}
              onInput=${(e) => setName(e.target.value)}
              onKeyDown=${handleKeyDown}
              placeholder="My Prompt"
              disabled=${isSaving}
              class="w-full px-3 py-2 bg-mitto-input border border-slate-600 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent placeholder-gray-500 disabled:opacity-50"
              data-testid="save-prompt-name-input"
            />
          </div>

          <!-- Description field -->
          <div class="space-y-1.5">
            <label class="block text-sm font-medium text-gray-300">
              Description
              <span class="text-gray-500 text-xs ml-1">(optional)</span>
            </label>
            <textarea
              value=${description}
              onInput=${(e) => setDescription(e.target.value)}
              onKeyDown=${handleKeyDown}
              placeholder="A brief description of what this prompt does..."
              disabled=${isSaving}
              rows="2"
              class="w-full px-3 py-2 bg-mitto-input border border-slate-600 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent placeholder-gray-500 disabled:opacity-50 resize-none"
              data-testid="save-prompt-description-input"
            />
          </div>

          <!-- Filename field with optional Browse button -->
          <div class="space-y-1.5">
            <label class="block text-sm font-medium text-gray-300">
              Filename
            </label>
            <div class="flex gap-2">
              <input
                type="text"
                value=${filename}
                onInput=${handleFilenameChange}
                onKeyDown=${handleKeyDown}
                placeholder="my-prompt.md"
                disabled=${isSaving}
                class="flex-1 px-3 py-2 bg-mitto-input border border-slate-600 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent placeholder-gray-500 disabled:opacity-50"
                data-testid="save-prompt-filename-input"
              />
            </div>
          </div>

          <!-- Save directory with optional Browse button -->
          <div class="space-y-1.5">
            <label class="block text-sm font-medium text-gray-300">
              Save to
            </label>
            <div class="flex gap-2">
              <input
                type="text"
                value=${directory}
                onInput=${(e) => setDirectory(e.target.value)}
                onKeyDown=${handleKeyDown}
                placeholder="/path/to/save/directory"
                disabled=${isSaving}
                class="flex-1 px-3 py-2 bg-mitto-input border border-slate-600 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent placeholder-gray-500 disabled:opacity-50 font-mono text-xs"
                data-testid="save-prompt-directory-input"
              />
              ${hasNativeFolderPicker() &&
              html`
                <button
                  type="button"
                  onClick=${handleBrowse}
                  disabled=${isSaving}
                  class="px-3 py-2 text-sm bg-slate-700 hover:bg-slate-600 rounded-lg transition-colors whitespace-nowrap disabled:opacity-50"
                  data-testid="save-prompt-browse-btn"
                >
                  Browse…
                </button>
              `}
            </div>
            ${fullPath &&
            html`
              <p
                class="text-xs text-gray-500 mt-1 font-mono truncate"
                title=${fullPath}
              >
                ${fullPath}
              </p>
            `}
          </div>

          <!-- Error message -->
          ${error &&
          html`
            <div
              class="text-red-400 text-sm bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2"
              data-testid="save-prompt-error"
            >
              ${error}
            </div>
          `}
        </div>

        <!-- Footer with buttons -->
        <div class="flex justify-end gap-3 p-4 border-t border-slate-700">
          <button
            onClick=${handleClose}
            disabled=${isSaving}
            class="px-4 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors ${isSaving
              ? "opacity-50 cursor-not-allowed"
              : ""}"
            data-testid="save-prompt-cancel-btn"
          >
            Cancel
          </button>
          <button
            onClick=${handleSave}
            disabled=${!canSave}
            class="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors flex items-center gap-2 ${!canSave
              ? "opacity-50 cursor-not-allowed"
              : ""}"
            data-testid="save-prompt-save-btn"
          >
            ${isSaving &&
            html`
              <svg class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
                <circle
                  class="opacity-25"
                  cx="12"
                  cy="12"
                  r="10"
                  stroke="currentColor"
                  stroke-width="4"
                ></circle>
                <path
                  class="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                ></path>
              </svg>
            `}
            Save
          </button>
        </div>
      </div>
    </div>

    <!-- Overwrite confirmation dialog -->
    <${ConfirmDialog}
      isOpen=${showOverwriteConfirm}
      title="File Already Exists"
      message="A file with this name already exists at the specified location. Do you want to overwrite it?"
      confirmLabel="Overwrite"
      cancelLabel="Cancel"
      confirmVariant="danger"
      onConfirm=${handleOverwriteConfirm}
      onCancel=${handleOverwriteCancel}
    />
  `;
}
