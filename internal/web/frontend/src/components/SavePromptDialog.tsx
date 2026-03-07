import React, { useState, useEffect, useRef } from 'react';
import { apiFetch } from '../api';
import ConfirmDialog from './ConfirmDialog';
import './SavePromptDialog.css';

interface SavePromptDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onSaved?: () => void;
  initialText: string;
  workingDir?: string;
}

/**
 * Sanitize a prompt name into a safe filename.
 * Lowercases, replaces spaces/special chars with hyphens, adds .md extension.
 */
function nameToFilename(name: string): string {
  return (
    name
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9\s-]/g, '')
      .replace(/\s+/g, '-')
      .replace(/-+/g, '-')
      .replace(/^-|-$/g, '') + '.md'
  );
}

/**
 * Build the file content with YAML frontmatter.
 */
function buildFileContent(name: string, description: string, promptText: string): string {
  let frontmatter = `---\nname: "${name.replace(/"/g, '\\"')}"`;
  if (description.trim()) {
    frontmatter += `\ndescription: "${description.trim().replace(/"/g, '\\"')}"`;
  }
  frontmatter += '\n---\n\n';
  return frontmatter + promptText;
}

const SavePromptDialog: React.FC<SavePromptDialogProps> = ({
  isOpen,
  onClose,
  onSaved,
  initialText,
  workingDir = '',
}) => {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [filename, setFilename] = useState('');
  const [directory, setDirectory] = useState('');
  const [filenameManuallyEdited, setFilenameManuallyEdited] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState('');
  const [showOverwriteConfirm, setShowOverwriteConfirm] = useState(false);
  const nameInputRef = useRef<HTMLInputElement>(null);

  // Reset state when dialog opens
  useEffect(() => {
    if (isOpen) {
      setName('');
      setDescription('');
      setFilename('');
      setFilenameManuallyEdited(false);
      setIsSaving(false);
      setError('');
      setShowOverwriteConfirm(false);
      // Set default directory to workspace/.mitto/prompts/
      const defaultDir = workingDir
        ? workingDir.replace(/\/+$/, '') + '/.mitto/prompts'
        : '';
      setDirectory(defaultDir);
      // Focus the name input after render
      setTimeout(() => nameInputRef.current?.focus(), 100);
    }
  }, [isOpen, workingDir]);

  // Auto-generate filename from name (unless manually edited)
  useEffect(() => {
    if (!filenameManuallyEdited && name.trim()) {
      setFilename(nameToFilename(name));
    } else if (!filenameManuallyEdited && !name.trim()) {
      setFilename('');
    }
  }, [name, filenameManuallyEdited]);

  const fullPath = directory && filename ? directory + '/' + filename : '';

  const handleFilenameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setFilenameManuallyEdited(true);
    setFilename(e.target.value);
  };

  const handleClose = () => {
    if (!isSaving) {
      onClose();
    }
  };

  // Core save logic - called directly or after overwrite confirmation
  const doSave = async () => {
    if (!fullPath) return;

    setIsSaving(true);
    setError('');

    try {
      const content = buildFileContent(name, description, initialText);
      const response = await apiFetch('/api/save-prompt-file', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: fullPath, content }),
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || `Save failed (${response.status})`);
      }

      // Success - close dialog
      onSaved?.();
      onClose();
    } catch (err: any) {
      console.error('[SavePromptDialog] save error:', err);
      setError(err.message || 'Failed to save file');
    } finally {
      setIsSaving(false);
    }
  };

  // Handle save button click - check for existing file first
  const handleSave = async () => {
    // Validate
    if (!name.trim()) {
      setError('Name is required');
      return;
    }
    if (!fullPath) {
      setError('Invalid file path');
      return;
    }

    setError('');

    try {
      // Check if file already exists
      const checkResponse = await apiFetch(
        `/api/check-prompt-file?path=${encodeURIComponent(fullPath)}`
      );

      if (checkResponse.ok) {
        const data = await checkResponse.json();
        if (data.exists) {
          // File exists - ask for overwrite confirmation
          setShowOverwriteConfirm(true);
          return;
        }
      }

      // File doesn't exist - save directly
      await doSave();
    } catch (err) {
      console.error('[SavePromptDialog] check error:', err);
      // If check fails, try to save anyway
      await doSave();
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey && name.trim() && fullPath) {
      e.preventDefault();
      handleSave();
    } else if (e.key === 'Escape') {
      handleClose();
    }
  };

  if (!isOpen) return null;

  const canSave = name.trim() && fullPath && !isSaving;

  return (
    <>
      <div className="save-prompt-overlay" onClick={handleClose}>
        <div className="save-prompt-dialog" onClick={(e) => e.stopPropagation()}>
          <div className="save-prompt-header">
            <h3>Save Prompt as File</h3>
            <button
              className="save-prompt-close"
              onClick={handleClose}
              disabled={isSaving}
            >
              ×
            </button>
          </div>

          <div className="save-prompt-body">
            {/* Name field */}
            <div className="save-prompt-field">
              <label htmlFor="prompt-name">
                Name <span className="required">*</span>
              </label>
              <input
                ref={nameInputRef}
                id="prompt-name"
                type="text"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  setError('');
                }}
                onKeyDown={handleKeyDown}
                placeholder="e.g., Code Review, Bug Fix..."
                disabled={isSaving}
                className={error && !name.trim() ? 'error' : ''}
              />
            </div>

            {/* Description field */}
            <div className="save-prompt-field">
              <label htmlFor="prompt-description">Description (optional)</label>
              <input
                id="prompt-description"
                type="text"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="Brief description of this prompt"
                disabled={isSaving}
              />
            </div>

            {/* Filename field */}
            <div className="save-prompt-field">
              <label htmlFor="prompt-filename">Filename</label>
              <input
                id="prompt-filename"
                type="text"
                value={filename}
                onChange={handleFilenameChange}
                onKeyDown={handleKeyDown}
                placeholder="my-prompt.md"
                disabled={isSaving}
              />
            </div>

            {/* Save directory */}
            <div className="save-prompt-field">
              <label htmlFor="prompt-directory">Save to</label>
              <input
                id="prompt-directory"
                type="text"
                value={directory}
                onChange={(e) => setDirectory(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="/path/to/save/directory"
                disabled={isSaving}
                className="monospace"
              />
              {fullPath && (
                <span className="save-prompt-path" title={fullPath}>
                  {fullPath}
                </span>
              )}
            </div>

            {/* Preview of prompt text */}
            <div className="save-prompt-preview">
              <label>Prompt text</label>
              <div className="save-prompt-preview-text">
                {initialText.length > 200
                  ? initialText.substring(0, 200) + '...'
                  : initialText}
              </div>
            </div>

            {/* Error message */}
            {error && (
              <div className="save-prompt-error-banner">{error}</div>
            )}
          </div>

          <div className="save-prompt-footer">
            <button
              className="save-prompt-cancel"
              onClick={handleClose}
              disabled={isSaving}
            >
              Cancel
            </button>
            <button
              className="save-prompt-save"
              onClick={handleSave}
              disabled={!canSave}
            >
              {isSaving ? 'Saving...' : 'Save Prompt'}
            </button>
          </div>
        </div>
      </div>

      {/* Overwrite confirmation dialog */}
      <ConfirmDialog
        isOpen={showOverwriteConfirm}
        title="File Already Exists"
        message="A file with this name already exists at the specified location. Do you want to overwrite it?"
        confirmText="Overwrite"
        cancelText="Cancel"
        variant="danger"
        onConfirm={() => {
          setShowOverwriteConfirm(false);
          doSave();
        }}
        onCancel={() => setShowOverwriteConfirm(false)}
      />
    </>
  );
};

export default SavePromptDialog;
