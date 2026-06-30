// Mitto Web Interface - Delete Dialog Component
const { html } = window.preact;

import { Modal } from "./Modal.js";

// =============================================================================
// Delete Confirmation Dialog
// =============================================================================

export function DeleteDialog({
  isOpen,
  sessionName,
  isActive,
  isStreaming,
  onConfirm,
  onCancel,
}) {
  const footer = html`
    <button type="button" onClick=${onCancel} class="btn btn-ghost btn-sm">
      Cancel
    </button>
    <button type="button" onClick=${onConfirm} class="btn btn-error btn-sm">
      Delete
    </button>
  `;

  return html`
    <${Modal} isOpen=${isOpen} onClose=${onCancel} title="Delete Session" footer=${footer}>
      <p class="text-mitto-text-muted text-sm">
        Are you sure you want to delete "${sessionName}"?
        ${
          isStreaming &&
          html`<br /><span class="text-orange-400"
              >⚠️ This session is still receiving a response.</span
            >`
        }
        ${
          isActive &&
          !isStreaming &&
          html`<br /><span class="text-mitto-warning"
              >This is the active session.</span
            >`
        }
      </p>
    </${Modal}>
  `;
}
