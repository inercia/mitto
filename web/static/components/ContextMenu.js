// Mitto Web Interface - ContextMenu Component
// Right-click menus with viewport-aware positioning and hover-flyout submenus.
// Shared by the conversation/group menus (app.js) and the Beads issue list.

const { html, useState, useEffect, useRef, useMemo } = window.preact;

import { ChevronRightIcon } from "./Icons.js";

// Renders a single context menu entry. Entries with a non-empty `submenu`
// array expand a flyout submenu on hover (positioned to the right, flipping
// left or shifting up when it would overflow the viewport).
function ContextMenuItem({ item, onClose }) {
  const hasSubmenu = !!(item.submenu && item.submenu.length > 0);
  const [submenuOpen, setSubmenuOpen] = useState(false);
  const [submenuPos, setSubmenuPos] = useState({ left: 0, top: 0 });
  const itemRef = useRef(null);
  const closeTimerRef = useRef(null);

  const openSubmenu = () => {
    if (!hasSubmenu) return;
    clearTimeout(closeTimerRef.current);
    if (itemRef.current) {
      const rect = itemRef.current.getBoundingClientRect();
      const submenuWidth = 180;
      // Cap the estimate so long submenus (e.g. the full issue list) pin to the
      // top of the viewport and scroll instead of overflowing off-screen.
      const submenuHeight = Math.min(item.submenu.length * 38 + 8, window.innerHeight * 0.6);
      // Prefer opening to the right; flip to the left if it would overflow
      let left = rect.right - 4;
      if (left + submenuWidth > window.innerWidth) {
        left = rect.left - submenuWidth + 4;
      }
      // Shift up if it would overflow the bottom of the viewport
      let top = rect.top;
      if (top + submenuHeight > window.innerHeight) {
        top = Math.max(8, window.innerHeight - submenuHeight - 8);
      }
      setSubmenuPos({ left, top });
    }
    setSubmenuOpen(true);
  };

  const scheduleClose = () => {
    clearTimeout(closeTimerRef.current);
    closeTimerRef.current = setTimeout(() => setSubmenuOpen(false), 150);
  };

  useEffect(() => () => clearTimeout(closeTimerRef.current), []);

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
          class="w-full px-3 py-2 text-left text-sm transition-colors flex items-center gap-2 text-mitto-text hover:bg-mitto-surface-hover"
        >
          ${item.icon && html`<span class="w-4 h-4">${item.icon}</span>`}
          <span class="flex-1">${item.label}</span>
          <${ChevronRightIcon} className="w-4 h-4 text-mitto-text-muted" />
        </button>
        ${submenuOpen &&
        html`
          <div
            class="fixed z-50 bg-mitto-surface-2 border border-mitto-border-2 rounded-lg shadow-xl py-1 min-w-[140px] max-h-[60vh] overflow-y-auto"
            style="left: ${submenuPos.left}px; top: ${submenuPos.top}px;"
            onMouseEnter=${() => clearTimeout(closeTimerRef.current)}
            onMouseLeave=${scheduleClose}
          >
            <ul class="menu menu-sm w-full p-0 gap-0">
              ${item.submenu.map(
                (sub) => html`
                  <li key=${sub.label}>
                    <button
                      onClick=${(e) => {
                        e.stopPropagation();
                        if (!sub.disabled) {
                          sub.onClick();
                          onClose();
                        }
                      }}
                      disabled=${sub.disabled}
                      class="w-full px-3 py-2 text-left text-sm transition-colors flex items-center gap-2 ${sub.disabled
                        ? "text-mitto-text-muted cursor-not-allowed"
                        : sub.danger
                          ? "text-mitto-danger hover:text-red-300 hover:bg-mitto-surface-hover"
                          : "text-mitto-text hover:bg-mitto-surface-hover"}"
                    >
                      ${sub.icon && html`<span class="w-4 h-4">${sub.icon}</span>`}
                      ${sub.label}
                    </button>
                  </li>
                `,
              )}
            </ul>
          </div>
        `}
      </li>
    `;
  }

  return html`
    <li>
      <button
        onClick=${(e) => {
          e.stopPropagation();
          if (!item.disabled) {
            item.onClick();
            onClose();
          }
        }}
        disabled=${item.disabled}
        class="w-full px-3 py-2 text-left text-sm transition-colors flex items-center gap-2 ${item.disabled
          ? "text-mitto-text-muted cursor-not-allowed"
          : item.danger
            ? "text-mitto-danger hover:text-red-300 hover:bg-mitto-surface-hover"
            : "text-mitto-text hover:bg-mitto-surface-hover"}"
      >
        ${item.icon && html`<span class="w-4 h-4">${item.icon}</span>`}
        ${item.label}
      </button>
    </li>
  `;
}

export function ContextMenu({ x, y, items, onClose }) {
  const menuRef = useRef(null);

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

  // Calculate adjusted position synchronously using useMemo
  // This avoids the useState + useEffect anti-pattern that causes the menu
  // to not appear on first render (see 28-anti-patterns-ui.md)
  const position = useMemo(() => {
    // On first render, menuRef.current is null - use raw position
    if (!menuRef.current) {
      return { x, y };
    }
    // Menu exists - calculate adjusted position to stay within viewport
    const rect = menuRef.current.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    let newX = x;
    let newY = y;
    if (x + rect.width > viewportWidth) {
      newX = viewportWidth - rect.width - 8;
    }
    if (y + rect.height > viewportHeight) {
      newY = viewportHeight - rect.height - 8;
    }
    return { x: newX, y: newY };
  }, [x, y, menuRef.current]);

  return html`
    <div
      ref=${menuRef}
      class="fixed z-50 bg-mitto-surface-2 border border-mitto-border-2 rounded-lg shadow-xl py-1 min-w-[140px]"
      style="left: ${position.x}px; top: ${position.y}px;"
    >
      <ul class="menu menu-sm w-full p-0 gap-0">
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
    </div>
  `;
}
