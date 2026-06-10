// Mitto Web Interface - Confirm Dialog Component
// A reusable confirmation modal dialog that works in both web and native macOS app

const { html } = window.preact;

import { Modal } from "./Modal.js";

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
 * @param {any} props.children - Optional additional content rendered below the message
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
  children,
}) {
  const handleCancel = () => {
    if (!isLoading) onCancel?.();
  };

  const handleConfirm = () => {
    if (!isLoading) onConfirm?.();
  };

  // daisyUI button variant: danger → btn-error, default/primary → btn-primary
  const confirmBtnClass =
    confirmVariant === "danger" ? "btn btn-error btn-sm" : "btn btn-primary btn-sm";

  const footer = html`
    <button
      onClick=${handleCancel}
      disabled=${isLoading}
      class="btn btn-ghost btn-sm"
      data-testid="confirm-dialog-cancel"
    >
      ${cancelLabel}
    </button>
    <button
      onClick=${handleConfirm}
      disabled=${isLoading}
      class=${confirmBtnClass}
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
  `;

  return html`
    <${Modal} isOpen=${isOpen} onClose=${handleCancel} title=${title} footer=${footer}>
      <p class="text-mitto-text-300">${message}</p>
      ${children}
    </${Modal}>
  `;
}
