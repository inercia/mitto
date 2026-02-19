// Mitto Web Interface - Confirm Dialog Component
// A reusable confirmation modal dialog that works in both web and native macOS app

const { html } = window.preact;

import { CloseIcon } from "./Icons.js";

/**
 * ConfirmDialog component - a simple confirmation modal dialog
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the dialog is visible
 * @param {string} props.title - Dialog title (optional, defaults to "Confirm")
 * @param {string} props.message - The confirmation message to display
 * @param {string} props.confirmLabel - Label for the confirm button (optional, defaults to "Yes")
 * @param {string} props.cancelLabel - Label for the cancel button (optional, defaults to "Cancel")
 * @param {string} props.confirmVariant - Variant for confirm button: "primary" (blue) or "danger" (red)
 * @param {boolean} props.isLoading - Whether the confirm action is in progress
 * @param {Function} props.onConfirm - Callback when user confirms
 * @param {Function} props.onCancel - Callback when user cancels or closes
 */
export function ConfirmDialog({
  isOpen,
  title = "Confirm",
  message,
  confirmLabel = "Yes",
  cancelLabel = "Cancel",
  confirmVariant = "primary",
  isLoading = false,
  onConfirm,
  onCancel,
}) {
  if (!isOpen) return null;

  const handleBackdropClick = (e) => {
    // Only close if clicking the backdrop itself, not the dialog content
    if (e.target === e.currentTarget && !isLoading) {
      onCancel?.();
    }
  };

  const handleConfirm = () => {
    if (!isLoading) {
      onConfirm?.();
    }
  };

  const handleCancel = () => {
    if (!isLoading) {
      onCancel?.();
    }
  };

  // Button styles based on variant
  const confirmButtonClass =
    confirmVariant === "danger"
      ? "bg-red-600 hover:bg-red-500 focus:ring-red-500"
      : "bg-blue-600 hover:bg-blue-500 focus:ring-blue-500";

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${handleBackdropClick}
      data-testid="confirm-dialog-backdrop"
    >
      <div
        class="bg-mitto-sidebar rounded-xl w-[400px] max-w-[90vw] overflow-hidden shadow-2xl flex flex-col"
        onClick=${(e) => e.stopPropagation()}
        data-testid="confirm-dialog"
      >
        <!-- Header -->
        <div
          class="flex items-center justify-between p-4 border-b border-slate-700"
        >
          <h3 class="text-lg font-semibold">${title}</h3>
          <button
            onClick=${handleCancel}
            disabled=${isLoading}
            class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${isLoading
              ? "opacity-50 cursor-not-allowed"
              : ""}"
            data-testid="confirm-dialog-close"
          >
            <${CloseIcon} className="w-5 h-5" />
          </button>
        </div>

        <!-- Content -->
        <div class="p-4">
          <p class="text-gray-300">${message}</p>
        </div>

        <!-- Footer with buttons -->
        <div class="flex justify-end gap-3 p-4 border-t border-slate-700">
          <button
            onClick=${handleCancel}
            disabled=${isLoading}
            class="px-4 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors ${isLoading
              ? "opacity-50 cursor-not-allowed"
              : ""}"
            data-testid="confirm-dialog-cancel"
          >
            ${cancelLabel}
          </button>
          <button
            onClick=${handleConfirm}
            disabled=${isLoading}
            class="px-4 py-2 text-sm ${confirmButtonClass} text-white rounded-lg transition-colors flex items-center gap-2 ${isLoading
              ? "opacity-75"
              : ""}"
            data-testid="confirm-dialog-confirm"
          >
            ${isLoading &&
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
            ${confirmLabel}
          </button>
        </div>
      </div>
    </div>
  `;
}
