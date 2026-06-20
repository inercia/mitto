// Mitto Web Interface - Modal Component
// Generic Preact-controlled modal wrapper built on daisyUI modal classes.
//
// Accessibility: the modal-box advertises role="dialog" + aria-modal="true",
// is aria-labelledby the title heading (when titled), receives initial focus on
// open and restores focus to the trigger on close, traps Tab/Shift+Tab within
// itself, and locks background scroll while any modal is open. ESC is handled
// topmost-only, so stacked modals do not all close on a single press.
//
// Title-less close affordance: a modal without a `title` renders no header and
// therefore no ✕ button. Such modals are still dismissable via ESC and backdrop
// click; if a visible close control is required, the caller must provide its own
// dismissal affordance (e.g. a button in `footer`).
//
// Props:
//   isOpen   {boolean}  - controls visibility; renders nothing when false
//   onClose  {Function} - called on backdrop click, ✕ button, or Escape key
//   title    {string}   - optional header title; header omitted when absent
//   children {any}      - modal body content
//   footer   {any}      - optional footer node (action buttons, etc.)
//   testid         {string} - optional data-testid applied to the modal box
//   closeTestid    {string} - optional data-testid applied to the header ✕ button
//   backdropTestid {string} - optional data-testid applied to the backdrop
//   boxClass {string} - optional extra classes appended to the modal-box, e.g.
//                       sizing overrides for large dialogs (w-[70vw] h-[70vh]).
//   bodyClass {string} - optional override for the body wrapper classes. Defaults
//                        to "p-4 overflow-y-auto"; pass a full-height flex value
//                        (e.g. "flex flex-col flex-1 min-h-0 overflow-hidden") for
//                        dialogs that own their internal scroll/layout.

const { html, useEffect, useRef } = window.preact;

import { CloseIcon } from "./Icons.js";

// Selector for elements that can receive keyboard focus inside the modal box;
// used for initial focus placement and the Tab/Shift+Tab focus trap.
const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

function getFocusable(container) {
  if (!container) return [];
  return Array.from(container.querySelectorAll(FOCUSABLE_SELECTOR)).filter(
    (el) => el.offsetParent !== null || el === document.activeElement,
  );
}

// Shared stack of open modals (most-recent last). Enables topmost-only ESC so
// stacked modals do not all close on a single press, and coordinates the
// background scroll-lock (locked while any modal is open, released on the last).
const modalStack = [];
let savedBodyOverflow = "";
let titleIdCounter = 0;

export function Modal({
  isOpen,
  onClose,
  title,
  children,
  footer,
  testid,
  closeTestid,
  backdropTestid,
  boxClass = "",
  bodyClass = "p-4 overflow-y-auto",
}) {
  // Hooks must run unconditionally (Rules of Hooks); the open/close behaviour is
  // guarded inside the effect body and the early return happens after the hooks.
  const boxRef = useRef(null);
  const prevFocusRef = useRef(null);

  // Keep the latest onClose without re-running the open/focus effect when the
  // caller passes a fresh handler identity on each render.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  // Stable id for aria-labelledby (only emitted when titled) and a unique token
  // identifying this instance within the shared modal stack.
  const titleIdRef = useRef(null);
  if (titleIdRef.current === null) {
    titleIdRef.current = `mitto-modal-title-${++titleIdCounter}`;
  }
  const tokenRef = useRef(null);
  if (tokenRef.current === null) tokenRef.current = {};

  useEffect(() => {
    if (!isOpen) return undefined;

    const token = tokenRef.current;
    const box = boxRef.current;

    // Remember the trigger so focus can be restored on close.
    prevFocusRef.current = document.activeElement;

    // Register on the shared stack; lock background scroll for the first modal.
    modalStack.push(token);
    if (modalStack.length === 1) {
      savedBodyOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
    }

    // Move initial focus into the modal (first focusable child, else the box).
    const focusables = getFocusable(box);
    (focusables[0] || box)?.focus();

    const handleKeyDown = (e) => {
      // Only the topmost modal reacts to keyboard events.
      if (modalStack[modalStack.length - 1] !== token) return;

      if (e.key === "Escape") {
        onCloseRef.current?.();
        return;
      }

      if (e.key === "Tab") {
        const items = getFocusable(box);
        if (items.length === 0) {
          // Keep focus pinned to the box when there is nothing focusable inside.
          e.preventDefault();
          box?.focus();
          return;
        }
        const first = items[0];
        const last = items[items.length - 1];
        const active = document.activeElement;
        if (e.shiftKey) {
          if (active === first || !box.contains(active)) {
            e.preventDefault();
            last.focus();
          }
        } else if (active === last || !box.contains(active)) {
          e.preventDefault();
          first.focus();
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);

    return () => {
      window.removeEventListener("keydown", handleKeyDown);

      const idx = modalStack.indexOf(token);
      if (idx !== -1) modalStack.splice(idx, 1);

      // Release the scroll-lock once the last modal closes.
      if (modalStack.length === 0) {
        document.body.style.overflow = savedBodyOverflow;
        savedBodyOverflow = "";
      }

      // Restore focus to the element that opened the modal.
      const prev = prevFocusRef.current;
      if (prev && typeof prev.focus === "function") prev.focus();
    };
  }, [isOpen]);

  if (!isOpen) return null;

  const titleId = titleIdRef.current;

  return html`
    <div class="modal modal-open">
      <div
        class="modal-box flex flex-col gap-0 p-0 overflow-hidden ${boxClass}"
        role="dialog"
        aria-modal="true"
        aria-labelledby=${title ? titleId : undefined}
        tabindex="-1"
        ref=${boxRef}
        data-testid=${testid}
      >
        ${title &&
        html`
          <div
            class="flex items-center justify-between p-4 border-b border-mitto-border"
          >
            <h3 id=${titleId} class="text-lg font-semibold">${title}</h3>
            <button
              onClick=${onClose}
              class="btn btn-ghost btn-square btn-sm tooltip tooltip-left"
              data-tip="Close"
              aria-label="Close"
              data-testid=${closeTestid}
            >
              <${CloseIcon} className="w-5 h-5" />
            </button>
          </div>
        `}

        <div class=${bodyClass}>${children}</div>

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
      <div
        class="modal-backdrop"
        onClick=${onClose}
        data-testid=${backdropTestid}
      ></div>
    </div>
  `;
}
