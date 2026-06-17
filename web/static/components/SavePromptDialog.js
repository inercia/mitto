// Mitto Web Interface - Save Prompt Dialog Component
// Modal dialog for saving the current prompt text as a markdown file with frontmatter

const { useState, useEffect, useCallback, useRef, html, Fragment } = window.preact;

import { hasNativeFolderPicker, pickFolder } from "../utils/native.js";
import { secureFetch, authFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { Modal } from "./Modal.js";

/**
 * Sanitize a prompt name into a safe filename.
 * Lowercases, replaces spaces/special chars with hyphens, adds .prompt.yaml extension.
 * @param {string} name - The prompt name
 * @returns {string} A sanitized filename like "my-prompt-name.prompt.yaml"
 */
function nameToFilename(name) {
  return (
    name
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9\s-]/g, "")
      .replace(/\s+/g, "-")
      .replace(/-+/g, "-")
      .replace(/^-|-$/g, "") + ".prompt.yaml"
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

  const footer = html`
    <button
      onClick=${handleClose}
      disabled=${isSaving}
      class="btn btn-sm btn-ghost"
      data-testid="save-prompt-cancel-btn"
    >
      Cancel
    </button>
    <button
      onClick=${handleSave}
      disabled=${!canSave}
      class="btn btn-sm btn-primary"
      data-testid="save-prompt-save-btn"
    >
      ${isSaving && html`<span class="loading loading-spinner loading-xs"></span>`}
      Save
    </button>
  `;

  return html`
    <${Fragment}>
      <${Modal}
        isOpen=${isOpen}
        onClose=${handleClose}
        title="Save Prompt"
        testid="save-prompt-dialog"
        backdropTestid="save-prompt-dialog-backdrop"
        closeTestid="save-prompt-dialog-close"
        footer=${footer}
      >
        <div class="space-y-4">
          <!-- Name field -->
          <fieldset class="fieldset">
            <legend class="fieldset-legend text-mitto-text-secondary">
              Name
              <span class="text-mitto-danger ml-0.5">*</span>
            </legend>
            <input
              ref=${nameInputRef}
              type="text"
              value=${name}
              onInput=${(e) => setName(e.target.value)}
              onKeyDown=${handleKeyDown}
              placeholder="My Prompt"
              disabled=${isSaving}
              class="input input-sm w-full"
              data-testid="save-prompt-name-input"
            />
          </fieldset>

          <!-- Description field -->
          <fieldset class="fieldset">
            <legend class="fieldset-legend text-mitto-text-secondary">
              Description
              <span class="text-mitto-text-muted text-xs ml-1">(optional)</span>
            </legend>
            <textarea
              value=${description}
              onInput=${(e) => setDescription(e.target.value)}
              onKeyDown=${handleKeyDown}
              placeholder="A brief description of what this prompt does..."
              disabled=${isSaving}
              rows="2"
              class="textarea textarea-sm w-full resize-none disabled:opacity-50"
              data-testid="save-prompt-description-input"
            />
          </fieldset>

          <!-- Filename field -->
          <fieldset class="fieldset">
            <legend class="fieldset-legend text-mitto-text-secondary">
              Filename
            </legend>
            <input
              type="text"
              value=${filename}
              onInput=${handleFilenameChange}
              onKeyDown=${handleKeyDown}
              placeholder="my-prompt.prompt.yaml"
              disabled=${isSaving}
              class="input input-sm w-full"
              data-testid="save-prompt-filename-input"
            />
          </fieldset>

          <!-- Save directory with optional Browse button -->
          <fieldset class="fieldset">
            <legend class="fieldset-legend text-mitto-text-secondary">
              Save to
            </legend>
            <div class="join w-full">
              <input
                type="text"
                value=${directory}
                onInput=${(e) => setDirectory(e.target.value)}
                onKeyDown=${handleKeyDown}
                placeholder="/path/to/save/directory"
                disabled=${isSaving}
                class="input input-sm join-item flex-1 font-mono text-xs"
                data-testid="save-prompt-directory-input"
              />
              ${hasNativeFolderPicker() &&
              html`
                <button
                  type="button"
                  onClick=${handleBrowse}
                  disabled=${isSaving}
                  class="btn btn-sm join-item whitespace-nowrap"
                  data-testid="save-prompt-browse-btn"
                >
                  Browse…
                </button>
              `}
            </div>
            ${fullPath &&
            html`
              <p
                class="text-xs text-mitto-text-muted mt-1 font-mono truncate"
                title=${fullPath}
              >
                ${fullPath}
              </p>
            `}
          </fieldset>

          <!-- Error message -->
          ${error &&
          html`
            <div
              role="alert"
              class="alert alert-error alert-soft text-sm"
              data-testid="save-prompt-error"
            >
              ${error}
            </div>
          `}
        </div>
      </${Modal}>

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
    </${Fragment}>
  `;
}
