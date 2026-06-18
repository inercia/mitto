// Mitto Web Interface - Tooltip Component
const { html, Fragment } = window.preact;

// =============================================================================
// Tooltip Component (daisyUI)
// =============================================================================

// Full literal class-name maps. These MUST be complete strings (not built via
// string interpolation like `tooltip-${placement}`) so Tailwind v4's source
// scanner detects them and compiles the corresponding daisyUI utilities into
// tailwind.css. See web/static/tailwind.src.css for the build config.
const PLACEMENT_CLASS = {
  top: "tooltip-top",
  bottom: "tooltip-bottom",
  left: "tooltip-left",
  right: "tooltip-right",
};

const COLOR_CLASS = {
  primary: "tooltip-primary",
  secondary: "tooltip-secondary",
  accent: "tooltip-accent",
  info: "tooltip-info",
  success: "tooltip-success",
  warning: "tooltip-warning",
  error: "tooltip-error",
};

/**
 * A reusable daisyUI tooltip wrapper.
 *
 * Wraps its children in `<div class="tooltip" data-tip="...">`, which is the
 * markup daisyUI expects (see .augment/skills/daisyui/components/tooltip.md).
 *
 * Caveats (intentionally surfaced for callers):
 * - The wrapper is an extra DOM node. For flex/grid parents it becomes the new
 *   flex/grid item, so pass layout classes (e.g. "flex", sizing) via
 *   `className` if the wrapped element previously was the direct item.
 * - daisyUI tooltips are CSS-positioned and get clipped by `overflow:hidden`
 *   or scroll containers (sidebars, dialogs, lists). Prefer this in
 *   non-clipping areas; choose `placement` to point away from clipping edges.
 * - Tooltips do not show on touch devices — keep critical info elsewhere too.
 *
 * If `tip` is empty/nullish, children render unwrapped (no empty tooltip).
 *
 * @param {string} tip - Tooltip text (rendered as data-tip).
 * @param {string} placement - 'top' (default), 'bottom', 'left', 'right'.
 * @param {string} color - Optional daisyUI color: 'primary', 'secondary',
 *   'accent', 'info', 'success', 'warning', 'error'.
 * @param {boolean} open - Force the tooltip open (adds tooltip-open).
 * @param {string} className - Extra classes for the wrapper element.
 */
export function Tooltip({
  tip,
  placement = "top",
  color,
  open = false,
  className = "",
  children,
}) {
  if (tip === undefined || tip === null || tip === "") {
    return html`<${Fragment}>${children}<//>`;
  }

  const classes = [
    "tooltip",
    PLACEMENT_CLASS[placement] || PLACEMENT_CLASS.top,
    color ? COLOR_CLASS[color] : "",
    open ? "tooltip-open" : "",
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return html`
    <div class=${classes} data-tip=${tip}>${children}</div>
  `;
}
