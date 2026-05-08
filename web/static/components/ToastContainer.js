// web/static/components/ToastContainer.js
// Renders the active toast stack from the useToast hook.
const { html } = window.preact;
import { CloseIcon } from "./Icons.js";

// Style config: Tailwind background/text class and icon emoji
const STYLE_CONFIG = {
  info:    { bg: "bg-blue-600 text-white",   icon: "ℹ️" },
  success: { bg: "bg-green-600 text-white",  icon: "✓" },
  warning: { bg: "bg-amber-500 text-white",  icon: "⚠️" },
  error:   { bg: "bg-red-600 text-white",    icon: "❌" },
};

/**
 * Renders all active toasts stacked from top-center.
 *
 * @param {Object} props
 * @param {Array}    props.toasts    - Array of toast objects from useToast
 * @param {Function} props.onDismiss - Called with toast id to dismiss
 */
export function ToastContainer({ toasts, onDismiss }) {
  if (!toasts || toasts.length === 0) return null;

  return html`
    <div style="position:fixed;top:1rem;right:1rem;z-index:50;display:flex;flex-direction:column;gap:0.5rem;align-items:flex-end;">
      ${toasts.map((toast) => {
        const config = STYLE_CONFIG[toast.style] || STYLE_CONFIG.info;
        return html`
          <div
            key=${toast.id}
            class="toast-enter ${toast.onClick ? "cursor-pointer" : ""}"
            onClick=${toast.onClick
              ? () => {
                  toast.onClick();
                  onDismiss(toast.id);
                }
              : undefined}
          >
            <div
              class="flex flex-col gap-1 px-4 py-2.5 rounded-lg shadow-lg max-w-md ${config.bg}"
            >
              <div class="flex items-center gap-2">
                <span class="text-lg">${config.icon}</span>
                <span class="text-sm font-medium">${toast.title}</span>
                ${toast.dismissable !== false &&
                html`
                  <button
                    onClick=${(e) => {
                      e.stopPropagation();
                      onDismiss(toast.id);
                    }}
                    class="ml-auto p-1 text-white/80 hover:text-white rounded transition-colors"
                    title="Dismiss"
                  >
                    <${CloseIcon} className="w-4 h-4" />
                  </button>
                `}
              </div>
              ${toast.message &&
              html`
                <div class="text-xs opacity-90 ml-7 break-words">
                  ${toast.message}
                </div>
              `}
            </div>
          </div>
        `;
      })}
    </div>
  `;
}
