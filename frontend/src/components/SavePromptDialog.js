import React, { useState, useEffect, useCallback, useRef } from "react";
import { Save, FolderOpen, X, Loader2 } from "lucide-react";
import { isNativeApp, pickFolder } from "../utils/native";
import ConfirmDialog from "./ConfirmDialog";

/**
 * SavePromptDialog – a modal dialog for saving the current prompt text as
 * a Markdown file with YAML frontmatter (name + description).
 *
 * Props:
 *   open         – whether the dialog is visible
 *   onClose      – callback to close the dialog
 *   promptText   – the current text from the composition area
 *   workDir      – the workspace directory (for default save path)
 */
export default function SavePromptDialog({ open, onClose, promptText, workDir }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [filename, setFilename] = useState("");
  const [directory, setDirectory] = useState("");
  const [filenameManuallyEdited, setFilenameManuallyEdited] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  // For overwrite confirmation
  const [showOverwrite, setShowOverwrite] = useState(false);
  const [pendingPath, setPendingPath] = useState("");

  const nameInputRef = useRef(null);

  // Reset state when dialog opens
  useEffect(() => {
    if (open) {
      setName("");
      setDescription("");
      setFilename("");
      setFilenameManuallyEdited(false);
      setSaving(false);
      setError("");
      setShowOverwrite(false);
      setPendingPath("");
      // Set default directory
      const defaultDir = workDir
        ? `${workDir}/.mitto/prompts`
        : "";
      setDirectory(defaultDir);
      // Focus the name input after render
      setTimeout(() => nameInputRef.current?.focus(), 100);
    }
  }, [open, workDir]);

  // Auto-generate filename from name (unless manually edited)
  useEffect(() => {
    if (!filenameManuallyEdited && name) {
      const generated = name
        .toLowerCase()
        .trim()
        .replace(/[^a-z0-9\s-]/g, "")
        .replace(/\s+/g, "-")
        .replace(/-+/g, "-");
      setFilename(generated ? `${generated}.md` : "");
    } else if (!filenameManuallyEdited && !name) {
      setFilename("");
    }
  }, [name, filenameManuallyEdited]);

  const handleFilenameChange = useCallback((e) => {
    setFilenameManuallyEdited(true);
    setFilename(e.target.value);
  }, []);

  const handleBrowse = useCallback(async () => {
    const folder = await pickFolder();
    if (folder) {
      setDirectory(folder);
    }
  }, []);

  const buildContent = useCallback(() => {
    let frontmatter = `---\nname: "${name.replace(/"/g, '\\"')}"`;
    if (description.trim()) {
      frontmatter += `\ndescription: "${description.trim().replace(/"/g, '\\"')}"`;
    }
    frontmatter += "\n---\n\n";
    return frontmatter + promptText;
  }, [name, description, promptText]);

  const doSave = useCallback(
    async (fullPath) => {
      setSaving(true);
      setError("");
      try {
        const content = buildContent();
        const res = await fetch("/api/save-prompt-file", {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: fullPath, content }),
        });
        if (!res.ok) {
          const text = await res.text().catch(() => "");
          throw new Error(text || `Save failed (${res.status})`);
        }
        onClose();
      } catch (err) {
        setError(err.message || "Failed to save file");
      } finally {
        setSaving(false);
      }
    },
    [buildContent, onClose],
  );

  const handleSave = useCallback(async () => {
    if (!name.trim()) {
      setError("Name is required");
      return;
    }
    if (!filename.trim()) {
      setError("Filename is required");
      return;
    }
    if (!directory.trim()) {
      setError("Directory is required");
      return;
    }

    const fullPath = `${directory}/${filename}`;
    setError("");

    // Check if the file already exists
    try {
      const res = await fetch(
        `/api/check-file-exists?path=${encodeURIComponent(fullPath)}`,
        { credentials: "include" },
      );
      if (res.ok) {
        const data = await res.json();
        if (data.exists) {
          // Show overwrite confirmation
          setPendingPath(fullPath);
          setShowOverwrite(true);
          return;
        }
      }
    } catch {
      // If check fails, proceed with save anyway
    }

    await doSave(fullPath);
  }, [name, filename, directory, doSave]);

  const handleOverwriteConfirm = useCallback(() => {
    setShowOverwrite(false);
    doSave(pendingPath);
  }, [pendingPath, doSave]);

  const handleOverwriteCancel = useCallback(() => {
    setShowOverwrite(false);
    setPendingPath("");
  }, []);

  const handleKeyDown = useCallback(
    (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        handleSave();
      }
    },
    [onClose, handleSave],
  );

  if (!open) return null;

  const canSave = name.trim() && filename.trim() && directory.trim() && !saving;
  const nativeApp = isNativeApp();

  return (
    <>
      {/* Backdrop */}
      <div
        className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
        onClick={onClose}
        onKeyDown={handleKeyDown}
      >
        {/* Dialog */}
        <div
          className="w-full max-w-md rounded-xl border border-gray-200 bg-white p-6 shadow-2xl dark:border-gray-700 dark:bg-gray-800"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Header */}
          <div className="mb-4 flex items-center justify-between">
            <h2 className="flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-gray-100">
              <Save size={20} />
              Save Prompt
            </h2>
            <button
              onClick={onClose}
              className="rounded p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700 dark:hover:text-gray-300"
            >
              <X size={18} />
            </button>
          </div>

          {/* Name field */}
          <div className="mb-3">
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
              Name <span className="text-red-500">*</span>
            </label>
            <input
              ref={nameInputRef}
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="e.g. Code Review Prompt"
              className="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors placeholder:text-gray-400 focus:border-blue-300 focus:ring-1 focus:ring-blue-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-gray-500 dark:focus:border-blue-500 dark:focus:ring-blue-500"
            />
          </div>

          {/* Description field */}
          <div className="mb-3">
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
              Description
            </label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Optional: describe what this prompt does…"
              rows={2}
              className="w-full resize-none rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors placeholder:text-gray-400 focus:border-blue-300 focus:ring-1 focus:ring-blue-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-gray-500 dark:focus:border-blue-500 dark:focus:ring-blue-500"
            />
          </div>

          {/* Filename field */}
          <div className="mb-3">
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
              Filename
            </label>
            <div className="flex gap-2">
              <input
                type="text"
                value={filename}
                onChange={handleFilenameChange}
                onKeyDown={handleKeyDown}
                placeholder="my-prompt.md"
                className="flex-1 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm outline-none transition-colors placeholder:text-gray-400 focus:border-blue-300 focus:ring-1 focus:ring-blue-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-gray-500 dark:focus:border-blue-500 dark:focus:ring-blue-500"
              />
            </div>
          </div>

          {/* Directory field */}
          <div className="mb-4">
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
              Save Location
            </label>
            <div className="flex gap-2">
              <input
                type="text"
                value={directory}
                onChange={(e) => setDirectory(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="/path/to/save/directory"
                className="flex-1 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-mono outline-none transition-colors placeholder:text-gray-400 focus:border-blue-300 focus:ring-1 focus:ring-blue-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-gray-500 dark:focus:border-blue-500 dark:focus:ring-blue-500"
              />
              {nativeApp && (
                <button
                  type="button"
                  onClick={handleBrowse}
                  className="flex items-center gap-1 whitespace-nowrap rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-gray-600 transition-colors hover:bg-gray-100 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                >
                  <FolderOpen size={14} />
                  Browse…
                </button>
              )}
            </div>
          </div>

          {/* Preview of what will be saved */}
          <div className="mb-4">
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
              Preview
            </label>
            <div className="max-h-24 overflow-y-auto rounded-lg border border-gray-200 bg-gray-50 p-2 text-xs font-mono text-gray-600 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-400">
              {promptText
                ? promptText.length > 200
                  ? promptText.slice(0, 200) + "…"
                  : promptText
                : "(empty prompt)"}
            </div>
          </div>

          {/* Error message */}
          {error && (
            <div className="mb-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">
              {error}
            </div>
          )}

          {/* Actions */}
          <div className="flex justify-end gap-2">
            <button
              onClick={onClose}
              className="rounded-lg border border-gray-200 bg-white px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              Cancel
            </button>
            <button
              onClick={handleSave}
              disabled={!canSave}
              className="flex items-center gap-1.5 rounded-lg bg-blue-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-600 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-blue-600 dark:hover:bg-blue-700"
            >
              {saving ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Saving…
                </>
              ) : (
                <>
                  <Save size={14} />
                  Save
                </>
              )}
            </button>
          </div>

          {/* Keyboard hint */}
          <p className="mt-2 text-center text-[10px] text-gray-400 dark:text-gray-500">
            ⌘+Enter to save · Esc to cancel
          </p>
        </div>
      </div>

      {/* Overwrite confirmation */}
      <ConfirmDialog
        open={showOverwrite}
        title="File Already Exists"
        message={`A file already exists at "${pendingPath}". Do you want to overwrite it?`}
        confirmText="Overwrite"
        cancelText="Cancel"
        confirmColor="error"
        onConfirm={handleOverwriteConfirm}
        onCancel={handleOverwriteCancel}
      />
    </>
  );
}
