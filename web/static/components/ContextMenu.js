// Mitto Web Interface - ContextMenu Component
// Right-click menus with viewport-aware positioning and hover-flyout submenus.
// Shared by the conversation/group menus (app.js) and the Beads issue list.

const { html, useState, useEffect, useLayoutEffect, useRef, render } =
  window.preact;

import { ChevronRightIcon } from "./Icons.js";

// Renders `children` into a fresh <div> appended to document.body so the menu
// escapes any ancestor stacking context / containing block. The sidebar lives
// inside daisyUI's .drawer-side panel, whose child carries `translate` and
// `will-change: transform`; both establish a containing block for
// position:fixed descendants AND a stacking context, which traps the menu's
// `fixed z-50` inside the sidebar's width and paints it BEHIND the chat panel.
// Rendering at the document.body level sidesteps this entirely.
function Portal({ children }) {
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

// Renders a single context menu entry. Entries with a non-empty `submenu`
// array expand a flyout submenu on hover (positioned to the right, flipping
// left or shifting up when it would overflow the viewport).
function ContextMenuItem({ item, onClose }) {
  const hasSubmenu = !!(item.submenu && item.submenu.length > 0);
  const [submenuOpen, setSubmenuOpen] = useState(false);
  const [submenuPos, setSubmenuPos] = useState({ left: 0, top: 0 });
  const itemRef = useRef(null);
  const submenuRef = useRef(null);
  const closeTimerRef = useRef(null);

  const openSubmenu = () => {
    if (!hasSubmenu) return;
    clearTimeout(closeTimerRef.current);
    if (itemRef.current) {
      const rect = itemRef.current.getBoundingClientRect();
      // Provisional placement only: anchor the left near the viewport's left
      // edge so the submenu is laid out with ample horizontal room and can be
      // measured at its true (max-width-capped) width. The real viewport-aware
      // flip/clamp happens in the useLayoutEffect below, which runs before paint
      // so this provisional position is never visible. This matters because the
      // submenu is shrink-to-fit (position:fixed with no explicit width), so its
      // width depends on the room to its right — measuring it where it will
      // finally sit can under-report the width and break the flip math.
      setSubmenuPos({ left: 8, top: rect.top });
    }
    setSubmenuOpen(true);
  };

  const scheduleClose = () => {
    clearTimeout(closeTimerRef.current);
    closeTimerRef.current = setTimeout(() => setSubmenuOpen(false), 150);
  };

  useEffect(() => () => clearTimeout(closeTimerRef.current), []);

  // Once the submenu has rendered (provisionally near the left edge with full
  // room), measure its ACTUAL, max-width-capped size and clamp/flip it into the
  // viewport. Measuring where it has ample room avoids the shrink-to-fit trap:
  // a position:fixed element with no explicit width sizes to the room on its
  // right, so measuring it at its final spot near the right edge under-reports
  // the width and the flip lands the flyout on top of the parent menu.
  // useLayoutEffect runs before paint, so the correction is invisible (mirrors
  // the main menu's clamp).
  useLayoutEffect(() => {
    if (!submenuOpen) return;
    const el = submenuRef.current;
    const anchor = itemRef.current;
    if (!el || !anchor) return;
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
    setSubmenuPos({ left, top });
  }, [submenuOpen]);

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
            class="menu bg-base-200 rounded-box shadow-xl fixed z-50 min-w-[140px] max-h-[60vh] overflow-y-auto"
            style="left: ${submenuPos.left}px; top: ${submenuPos.top}px; max-width: min(20rem, 92vw);"
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
                    ${sub.label}
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
    <li class="${item.disabled ? "menu-disabled" : ""}">
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
        ${item.label}
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
        class="menu bg-base-200 rounded-box shadow-xl fixed z-50 min-w-[140px] max-h-[95vh] overflow-y-auto flex-nowrap"
        style="left: ${position.x}px; top: ${position.y}px;"
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
