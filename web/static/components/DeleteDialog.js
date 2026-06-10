// Mitto Web Interface - Delete Dialog Component
const { html } = window.preact;

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
  if (!isOpen) return null;

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${onCancel}
    >
      <div
        class="bg-mitto-sidebar rounded-xl p-6 w-80 shadow-2xl"
        onClick=${(e) => e.stopPropagation()}
      >
        <h3 class="text-lg font-semibold mb-2">Delete Session</h3>
        <p class="text-mitto-text-muted text-sm mb-4">
          Are you sure you want to delete "${sessionName}"?
          ${isStreaming &&
          html`<br /><span class="text-orange-400"
              >⚠️ This session is still receiving a response.</span
            >`}
          ${isActive &&
          !isStreaming &&
          html`<br /><span class="text-mitto-warning"
              >This is the active session.</span
            >`}
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            onClick=${onCancel}
            class="px-4 py-2 rounded-lg hover:bg-mitto-surface-hover transition-colors"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick=${onConfirm}
            class="px-4 py-2 bg-mitto-danger hover:bg-mitto-danger-hover text-mitto-danger-fg rounded-lg transition-colors"
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  `;
}
