// web/static/components/ToastContainer.js
// Renders the active toast stack from the useToast hook.
const { html } = window.preact;
import { CloseIcon } from "./Icons.js";

// Style config: daisyUI alert variant (severity -> semantic color via --mitto-*
// token bridge) and icon emoji. The alert-* class carries both background and
// content color per theme, replacing the old fixed bg-*/text-white pairs.
const STYLE_CONFIG = {
  info:    { alert: "alert-info",    icon: "ℹ️" },
  success: { alert: "alert-success", icon: "✓" },
  warning: { alert: "alert-warning", icon: "⚠️" },
  error:   { alert: "alert-error",   icon: "❌" },
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
            <div role="alert" class="alert ${config.alert} shadow-lg max-w-md">
              <span class="text-lg">${config.icon}</span>
              <div class="flex flex-col gap-0.5 min-w-0">
                <span class="text-sm font-medium">${toast.title}</span>
                ${toast.message &&
                html`
                  <span class="text-xs opacity-90 wrap-break-word">
                    ${toast.message}
                  </span>
                `}
              </div>
              ${toast.dismissable !== false &&
              html`
                <button
                  onClick=${(e) => {
                    e.stopPropagation();
                    onDismiss(toast.id);
                  }}
                  class="btn btn-ghost btn-xs btn-circle"
                  title="Dismiss"
                >
                  <${CloseIcon} className="w-4 h-4" />
                </button>
              `}
            </div>
          </div>
        `;
      })}
    </div>
  `;
}
