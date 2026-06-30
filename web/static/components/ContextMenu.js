// Mitto Web Interface - ContextMenu Component
// Right-click menus with viewport-aware positioning and hover-flyout submenus.
// Shared by the conversation/group menus (app.js) and the Beads issue list.

const { html, useState, useEffect, useLayoutEffect, useRef, render } =
  window.preact;

import {
  ChevronRightIcon,
  getPromptIconOrDefault,
  PeriodicIcon,
} from "./Icons.js";
import { flattenPrompts } from "../utils/prompts.js";

// Build ContextMenu submenu items that group `prompts` by their `group`
// attribute (ungrouped prompts fall under "Other"), each group sorted by name.
// Every group becomes one ContextMenu entry whose `submenu` lists its prompts.
// `onRun(prompt)` handles selection; `groupIcon` is shown on each group entry.
// Returns [] when there are no prompts. Shared by the conversation menu and the
// Beads issue menus so all three surfaces present identical grouped submenus.
export function buildPromptGroupMenuItems(prompts, onRun, groupIcon) {
  const { groups } = flattenPrompts(prompts || [], {});
  return groups.map((g) => ({
    label: g.name,
    icon: groupIcon,
    submenu: g.prompts.map((p) => ({
      label: p.name,
      icon: html`<${getPromptIconOrDefault(p.icon)} className="w-4 h-4" />`,
      trailing: p.periodic
        ? html`<span
            class="shrink-0 text-success opacity-80"
            title="Periodic prompt — sets the conversation to recurring mode"
            ><${PeriodicIcon} className="w-3.5 h-3.5"
          /></span>`
        : null,
      onClick: () => onRun(p),
    })),
  }));
}

// Renders `children` into a fresh <div> appended to document.body so the menu
// escapes any ancestor stacking context / containing block. The sidebar lives
// inside daisyUI's .drawer-side panel, whose child carries `translate` and
// `will-change: transform`; both establish a containing block for
// position:fixed descendants AND a stacking context, which traps the menu's
// `fixed z-50` inside the sidebar's width and paints it BEHIND the chat panel.
// Rendering at the document.body level sidesteps this entirely.
export function Portal({ children }) {
  const containerRef = useRef(null);
  if (containerRef.current === null) {
    containerRef.current = document.createElement("div");
  }

  // Attach the container on mount; unmount the subtree and detach on cleanup.
  useLayoutEffect(() => {
    const el = containerRef.current;
    document.body.appendChild(el);
    return () => {
      render(null, el);
      el.remove();
    };
  }, []);

  // Reconcile the menu subtree into the body-level container on every render.
  useLayoutEffect(() => {
    render(children, containerRef.current);
  });

  return null;
}

// A portal-rendered tooltip bubble anchored near a cursor position. Used for
// multi-line metadata tooltips on elements whose ancestors clip CSS tooltips —
// e.g. the swipeable conversation rows, which need `overflow-hidden` for the
// swipe-to-archive reveal and sit inside the sidebar's `overflow-x: hidden`.
// daisyUI's CSS `::before` tooltip cannot escape those overflow boundaries and
// gets cropped; rendering at document.body (via Portal) does. Styled with the
// same CSS variables daisyUI's tooltip uses (var(--color-neutral) bubble,
// var(--color-neutral-content) text, var(--radius-field) corners) so it matches
// the other tooltips visually, and clamped to the viewport so it never spills
// off any edge. `text` may contain "\n"; rendered with white-space: pre-line.
export function PortalTooltip({ x, y, text }) {
  const ref = useRef(null);
  const [pos, setPos] = useState({ x: x + 14, y: y + 18 });

  // Clamp inside the viewport before paint (useLayoutEffect runs synchronously
  // after the Portal child mounts but before the browser paints, so the parked
  // initial offset is never visible). Prefer below-right of the cursor; flip to
  // the left and/or pin to an edge so the bubble is never cropped — the exact
  // failure this component fixes.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const margin = 8;
    let nx = x + 14;
    let ny = y + 18;
    if (nx + rect.width > window.innerWidth - margin) {
      nx = x - rect.width - 14;
    }
    if (nx < margin) nx = margin;
    if (ny + rect.height > window.innerHeight - margin) {
      ny = window.innerHeight - rect.height - margin;
    }
    if (ny < margin) ny = margin;
    setPos((prev) =>
      prev.x === nx && prev.y === ny ? prev : { x: nx, y: ny },
    );
  }, [x, y, text]);

  return html`
    <${Portal}>
      <div
        ref=${ref}
        class="fixed pointer-events-none"
        style="left: ${pos.x}px; top: ${pos.y}px; z-index: 9999; max-width: 20rem; white-space: pre-line; background: var(--color-neutral); color: var(--color-neutral-content); border-radius: var(--radius-field); padding: .375rem .625rem; font-size: .8125rem; line-height: 1.4; box-shadow: 0 4px 14px rgba(0, 0, 0, 0.35);"
      >
        ${text}
      </div>
    <//>
  `;
}

// Renders a single context menu entry. Entries with a non-empty `submenu`
// array expand a flyout submenu on hover (positioned to the right, flipping
// left or shifting up when it would overflow the viewport).
function ContextMenuItem({ item, onClose }) {
  const hasSubmenu = !!(item.submenu && item.submenu.length > 0);
  const submenuCount = hasSubmenu ? item.submenu.length : 0;
  const [submenuOpen, setSubmenuOpen] = useState(false);
  const [submenuPos, setSubmenuPos] = useState({ left: 0, top: 0 });
  const itemRef = useRef(null);
  const submenuRef = useRef(null);
  const closeTimerRef = useRef(null);

  // Open the flyout. When it is ALREADY open (e.g. the pointer briefly grazed a
  // sibling row or the gap and re-entered this item) only cancel the pending
  // close — do NOT reset the position. Re-setting submenuOpen to true here is a
  // no-op, so the reposition effect below would not re-run and the flyout would
  // stay parked at the unmeasured left edge, jumping away from the cursor and
  // becoming impossible to click. The reposition effect handles all placement.
  const openSubmenu = () => {
    if (!hasSubmenu) return;
    clearTimeout(closeTimerRef.current);
    setSubmenuOpen(true);
  };

  // Close after a short grace period so the pointer can cross the diagonal gap
  // between this row and a lower flyout row without the flyout vanishing.
  const scheduleClose = () => {
    clearTimeout(closeTimerRef.current);
    closeTimerRef.current = setTimeout(() => setSubmenuOpen(false), 250);
  };

  useEffect(() => () => clearTimeout(closeTimerRef.current), []);

  // Position the flyout once it (and its current items) are laid out. Re-runs
  // when it opens AND when its item count changes — e.g. Tasks/prompt entries
  // that load asynchronously and grow the flyout after it has already opened,
  // mirroring the main menu's clamp keyed on items.length. Parking the flyout
  // near the left edge before measuring lets this shrink-to-fit (position:fixed
  // with no explicit width) element report its true, max-width-capped width;
  // measuring it at its final spot near the right edge under-reports the width
  // and flips it on top of the parent menu. Mutating the style and reading
  // layout here both run before paint, so the parked position is never visible.
  useLayoutEffect(() => {
    if (!submenuOpen) return;
    const el = submenuRef.current;
    const anchor = itemRef.current;
    if (!el || !anchor) return;
    el.style.left = "8px";
    const rect = anchor.getBoundingClientRect();
    const sub = el.getBoundingClientRect();
    const margin = 8;
    // Prefer opening to the right of the parent item; flip to the left when the
    // flyout would overflow the right edge of the viewport.
    let left = rect.right - 4;
    if (left + sub.width > window.innerWidth - margin) {
      left = rect.left - sub.width + 4;
    }
    // If flipping left pushed it past the left edge, pin it back inside.
    if (left < margin) left = margin;
    // Shift up if it would overflow the bottom of the viewport.
    let top = rect.top;
    if (top + sub.height > window.innerHeight - margin) {
      top = Math.max(margin, window.innerHeight - sub.height - margin);
    }
    // Apply the computed position imperatively, not only via state. The parking
    // step above mutated el.style.left directly, so the DOM no longer matches the
    // declarative style. When the flyout is reopened on the same anchor, the
    // freshly computed position equals the value persisted in submenuPos from the
    // previous open, so setSubmenuPos is a no-op and Preact bails out of
    // re-rendering — leaving the DOM stuck at the parked left edge. Writing the
    // final coordinates here guarantees the DOM is correct regardless, and
    // setSubmenuPos keeps state consistent for subsequent renders.
    el.style.left = left + "px";
    el.style.top = top + "px";
    setSubmenuPos({ left, top });
  }, [submenuOpen, submenuCount]);

  if (hasSubmenu) {
    return html`
      <li
        ref=${itemRef}
        class="relative"
        onMouseEnter=${openSubmenu}
        onMouseLeave=${scheduleClose}
      >
        <button
          onClick=${(e) => {
            e.stopPropagation();
            openSubmenu();
          }}
        >
          ${item.icon && html`<span class="w-4 h-4">${item.icon}</span>`}
          <span class="flex-1">${item.label}</span>
          <${ChevronRightIcon} className="w-4 h-4 opacity-50" />
        </button>
        ${submenuOpen &&
        html`
          <ul
            ref=${submenuRef}
            class="menu bg-base-200 rounded-box shadow-xl fixed min-w-[140px] max-h-[60vh] overflow-y-auto"
            style="left: ${submenuPos.left}px; top: ${submenuPos.top}px; max-width: min(20rem, 92vw); z-index: 9999;"
            onMouseEnter=${() => clearTimeout(closeTimerRef.current)}
            onMouseLeave=${scheduleClose}
          >
            ${item.submenu.map(
              (sub) => html`
                <li
                  key=${sub.label}
                  class="${sub.disabled ? "menu-disabled" : ""}"
                >
                  <button
                    onClick=${(e) => {
                      e.stopPropagation();
                      if (!sub.disabled) {
                        sub.onClick();
                        onClose();
                      }
                    }}
                    disabled=${sub.disabled}
                    class="${sub.danger ? "text-error" : ""}"
                  >
                    ${sub.icon &&
                    html`<span class="w-4 h-4">${sub.icon}</span>`}
                    <span class="flex-1">${sub.label}</span>
                    ${sub.trailing}
                  </button>
                </li>
              `,
            )}
          </ul>
        `}
      </li>
    `;
  }

  return html`
    <li
      class="${item.disabled ? "menu-disabled" : ""}"
      title=${item.title || undefined}
    >
      <button
        onClick=${(e) => {
          e.stopPropagation();
          if (!item.disabled) {
            item.onClick();
            onClose();
          }
        }}
        disabled=${item.disabled}
        class="${item.danger ? "text-error" : ""}"
      >
        ${item.icon && html`<span class="w-4 h-4">${item.icon}</span>`}
        <span class="flex-1">${item.label}</span>
        ${item.trailing}
      </button>
    </li>
  `;
}

export function ContextMenu({ x, y, items, onClose }) {
  const menuRef = useRef(null);
  const [position, setPosition] = useState({ x, y });

  // Close menu when clicking outside - delay to avoid catching the click that opened the menu
  useEffect(() => {
    const handleClickOutside = (e) => {
      if (menuRef.current && !menuRef.current.contains(e.target)) {
        onClose();
      }
    };
    const handleEscape = (e) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    // Delay to avoid catching the opening right-click
    const timeoutId = setTimeout(() => {
      document.addEventListener("mousedown", handleClickOutside);
    }, 10);
    document.addEventListener("keydown", handleEscape);
    return () => {
      clearTimeout(timeoutId);
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [onClose]);

  // Clamp the menu inside the viewport once it (and its current items) are laid
  // out. useLayoutEffect runs synchronously BEFORE paint, so the correction is
  // invisible — no position jump (the failure mode of the useEffect approach)
  // and no stale measurement (the failure mode of useMemo keyed on a ref, which
  // never recomputes because refs don't trigger re-renders). Re-runs when the
  // anchor moves or the item count changes, e.g. conversation prompts that load
  // asynchronously and grow the menu after it has already opened.
  useLayoutEffect(() => {
    const el = menuRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const margin = 8;
    let newX = x;
    let newY = y;
    if (newX + rect.width > window.innerWidth) {
      newX = window.innerWidth - rect.width - margin;
    }
    if (newY + rect.height > window.innerHeight) {
      newY = window.innerHeight - rect.height - margin;
    }
    // Never push the top-left off-screen; menus taller than the viewport pin to
    // the top edge and scroll (max-h + overflow) instead of spilling upward.
    newX = Math.max(margin, newX);
    newY = Math.max(margin, newY);
    setPosition((prev) =>
      prev.x === newX && prev.y === newY ? prev : { x: newX, y: newY },
    );
  }, [x, y, items.length]);

  return html`
    <${Portal}>
      <ul
        ref=${menuRef}
        class="menu bg-base-200 rounded-box shadow-xl fixed min-w-[140px] max-h-[95vh] overflow-y-auto flex-nowrap"
        style="left: ${position.x}px; top: ${position.y}px; z-index: 9999;"
      >
        ${items.map(
          (item) => html`
            <${ContextMenuItem}
              key=${item.label}
              item=${item}
              onClose=${onClose}
            />
          `,
        )}
      </ul>
    <//>
  `;
}
