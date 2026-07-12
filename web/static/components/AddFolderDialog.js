// Mitto Web Interface - Add Folder Dialog
//
// Presents two ways to bring a folder into the sidebar:
//  1. Pin an existing configured workspace that is currently hidden
//     (no active/stored session AND folders.json `pinned !== true`). Calls
//     onPinExisting(workingDir); parent handles PUT /api/folders/pin and toast.
//  2. Create a net-new workspace — delegates to WorkspacesDialog via
//     onCreateNew(); parent closes this dialog and opens WorkspacesDialog.
//
// Reuses the shared Modal component (ESC / backdrop / focus trap handled).

const { html } = window.preact;

import { Modal } from "./Modal.js";

export function AddFolderDialog({
  isOpen,
  onClose,
  hiddenWorkspaces = [],
  onPinExisting,
  onCreateNew,
}) {
  const hasHidden = Array.isArray(hiddenWorkspaces) && hiddenWorkspaces.length > 0;

  const handlePickExisting = async (workingDir) => {
    if (!workingDir || !onPinExisting) return;
    try {
      await onPinExisting(workingDir);
    } finally {
      onClose && onClose();
    }
  };

  const footer = html`
    <button
      type="button"
      class="btn btn-ghost"
      onClick=${onClose}
      data-testid="add-folder-cancel-btn"
    >
      Cancel
    </button>
  `;

  return html`
    <${Modal}
      isOpen=${isOpen}
      onClose=${onClose}
      title="Add folder to sidebar"
      testid="add-folder-dialog"
      closeTestid="add-folder-close-btn"
      backdropTestid="add-folder-backdrop"
      footer=${footer}
    >
      ${hasHidden
        ? html`
            <p class="text-sm text-mitto-text-muted mb-3">
              Pick a configured workspace to show it in the sidebar:
            </p>
            <ul
              class="menu bg-mitto-surface-2 rounded-box max-h-64 overflow-y-auto p-2"
              data-testid="add-folder-hidden-list"
            >
              ${hiddenWorkspaces.map(
                (ws) => html`
                  <li key=${ws.working_dir}>
                    <button
                      type="button"
                      class="flex flex-col items-start gap-0.5 w-full text-left"
                      onClick=${() => handlePickExisting(ws.working_dir)}
                      data-testid=${`add-folder-pick-${ws.working_dir}`}
                    >
                      <span class="font-semibold truncate w-full"
                        >${ws.name || ws.working_dir}</span
                      >
                      <span
                        class="text-xs text-mitto-text-muted truncate w-full"
                        >${ws.working_dir}</span
                      >
                    </button>
                  </li>
                `,
              )}
            </ul>
          `
        : html`
            <p class="text-sm italic text-mitto-text-muted">
              All configured folders are already visible in the sidebar.
            </p>
          `}
      <div class="divider my-2 text-xs">or</div>
      <button
        type="button"
        class="btn btn-outline w-full"
        onClick=${() => onCreateNew && onCreateNew()}
        data-testid="add-folder-create-new-btn"
      >
        Create a new workspace…
      </button>
    </${Modal}>
  `;
}
