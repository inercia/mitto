// web/static/components/ToastContainer.js
// Renders the active toast stack. Self-subscribes to the module-level
// stores/notificationsStore.js (mitto-sus.11) via useToasts()/dismissToast
// instead of taking `toasts`/`onDismiss` as props from App, so a toast
// show/dismiss re-renders only this component -- not App's whole subtree.
const { html } = window.preact;
import { CloseIcon } from "./Icons.js";
import { useRenderCounter } from "../hooks/useRenderCounter.js";
import { useToasts, useToast } from "../hooks/useToast.js";

// Style config: daisyUI alert variant (severity -> semantic color via --mitto-*
// token bridge) and icon emoji. The alert-* class carries both background and
// content color per theme, replacing the old fixed bg-*/text-white pairs.
const STYLE_CONFIG = {
  info: { alert: "alert-info", icon: "ℹ️" },
  success: { alert: "alert-success", icon: "✓" },
  warning: { alert: "alert-warning", icon: "⚠️" },
  error: { alert: "alert-error", icon: "❌" },
};

/** Renders all active toasts stacked from top-center. Takes no props. */
export function ToastContainer() {
  // Dev-only render-count instrumentation (mitto-sus.7). No-op unless perf
  // instrumentation is enabled; see docs/devel/frontend-render-domains.md.
  // Placed before the early return so a render that bails on an empty toast
  // stack is still counted.
  useRenderCounter("ToastContainer");
  const toasts = useToasts();
  const { dismissToast: onDismiss } = useToast();
  if (!toasts || toasts.length === 0) return null;

  return html`
    <div class="toast toast-top toast-end items-end z-50">
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
                  class="btn btn-ghost btn-xs btn-circle tooltip tooltip-bottom"
                  data-tip="Dismiss"
                  aria-label="Dismiss"
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
