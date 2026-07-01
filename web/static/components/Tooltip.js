// Mitto Web Interface - Tooltip Component
const { html, Fragment, useState, useRef, useCallback, useEffect } =
  window.preact;

import { PortalTooltip } from "./ContextMenu.js";

// Hover-only tooltips are pointless on touch devices (no hover); gate the portal
// variant the same way daisyUI gates its CSS tooltips so taps never trigger a
// stuck bubble.
const TOOLTIP_SUPPORTS_HOVER =
  typeof window !== "undefined" &&
  typeof window.matchMedia === "function" &&
  window.matchMedia("(hover: hover)").matches;

// Delay before a portal tooltip appears on hover (ms).
const PORTAL_TOOLTIP_DELAY_MS = 250;

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
 * @param {string} placement - 'bottom' (default), 'top', 'left', 'right'.
 *   Bottom is the project default so labels never overlap the content above the
 *   trigger; pass 'top' explicitly for controls anchored at the bottom edge.
 * @param {string} color - Optional daisyUI color: 'primary', 'secondary',
 *   'accent', 'info', 'success', 'warning', 'error'.
 * @param {boolean} open - Force the tooltip open (adds tooltip-open).
 * @param {string} className - Extra classes for the wrapper element.
 * @param {boolean} portal - Render the bubble at the document root instead of
 *   as a CSS pseudo-element. Use where a CSS tooltip would be clipped by an
 *   `overflow:hidden` ancestor or occluded by a sibling stacking context (e.g.
 *   header buttons next to the side panel). Cursor-anchored and viewport-clamped
 *   (via PortalTooltip); `placement`/`color`/`open` are ignored in this mode.
 */
export function Tooltip({
  tip,
  placement = "bottom",
  color,
  open = false,
  className = "",
  portal = false,
  children,
}) {
  // Hooks must run unconditionally (tip can toggle between empty/non-empty
  // across renders), so declare the portal hover state before any early return.
  const [tipPos, setTipPos] = useState(null);
  const tipTimerRef = useRef(null);
  const showPortalTip = useCallback(
    (e) => {
      if (!TOOLTIP_SUPPORTS_HOVER || !tip) return;
      const x = e.clientX;
      const y = e.clientY;
      clearTimeout(tipTimerRef.current);
      tipTimerRef.current = setTimeout(
        () => setTipPos({ x, y }),
        PORTAL_TOOLTIP_DELAY_MS,
      );
    },
    [tip],
  );
  const hidePortalTip = useCallback(() => {
    clearTimeout(tipTimerRef.current);
    setTipPos(null);
  }, []);
  useEffect(() => () => clearTimeout(tipTimerRef.current), []);

  if (tip === undefined || tip === null || tip === "") {
    return html`<${Fragment}>${children}<//>`;
  }

  if (portal) {
    const wrapperClasses = ["inline-flex", className].filter(Boolean).join(" ");
    return html`<${Fragment}>
      <span
        class=${wrapperClasses}
        data-tip=${tip}
        onMouseEnter=${showPortalTip}
        onMouseLeave=${hidePortalTip}
        onMouseDown=${hidePortalTip}
        >${children}</span
      >
      ${tipPos &&
      html`<${PortalTooltip} x=${tipPos.x} y=${tipPos.y} text=${tip} />`}
    <//>`;
  }

  const classes = [
    "tooltip",
    PLACEMENT_CLASS[placement] || PLACEMENT_CLASS.bottom,
    color ? COLOR_CLASS[color] : "",
    open ? "tooltip-open" : "",
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return html` <div class=${classes} data-tip=${tip}>${children}</div> `;
}
