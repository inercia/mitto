// Mitto Web Interface - Modal Component
// Generic Preact-controlled modal wrapper built on daisyUI modal classes.
// Props:
//   isOpen   {boolean}  - controls visibility; renders nothing when false
//   onClose  {Function} - called on backdrop click, ✕ button, or Escape key
//   title    {string}   - optional header title; header omitted when absent
//   children {any}      - modal body content
//   footer   {any}      - optional footer node (action buttons, etc.)
//   testid   {string}   - optional data-testid applied to the modal box

const { html, useEffect } = window.preact;

import { CloseIcon } from "./Icons.js";

export function Modal({ isOpen, onClose, title, children, footer, testid }) {
  if (!isOpen) return null;

  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === "Escape") onClose?.();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  return html`
    <div class="modal modal-open">
      <div
        class="modal-box flex flex-col gap-0 p-0 overflow-hidden"
        data-testid=${testid}
      >
        ${title &&
        html`
          <div
            class="flex items-center justify-between p-4 border-b border-mitto-border"
          >
            <h3 class="text-lg font-semibold">${title}</h3>
            <button
              onClick=${onClose}
              class="p-1.5 hover:bg-mitto-surface-hover rounded-lg transition-colors"
              title="Close"
            >
              <${CloseIcon} className="w-5 h-5" />
            </button>
          </div>
        `}

        <div class="p-4 overflow-y-auto">${children}</div>

        ${footer &&
        html`
          <div
            class="flex justify-end gap-3 p-4 border-t border-mitto-border modal-action mt-0"
          >
            ${footer}
          </div>
        `}
      </div>

      <!-- Backdrop: click to close -->
      <div class="modal-backdrop" onClick=${onClose}></div>
    </div>
  `;
}
