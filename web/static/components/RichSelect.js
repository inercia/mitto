// Mitto Web Interface - Generic rich daisyUI dropdown (select replacement)
// Controlled <details>-based dropdown that supports rich menu-item content
// (icons/badges) while behaving like a single-select. Mirrors the proven
// positioning/close pattern of ConfigOptionSelect (the `config-dropdown-block`
// scoped CSS in styles.css, because daisyUI's CSS-anchor placement is
// unreliable in WKWebView).

const { html, useState, useEffect, useRef, useCallback } = window.preact;

import { ChevronDownIcon, CheckIcon } from "./Icons.js";

/**
 * RichSelect — controlled daisyUI dropdown allowing rich item rendering.
 *
 * Props:
 *   value         {string}                          currently selected value
 *   options       {Array<{value,label,render?}>}    render()=>htmlNode for the menu row (falls back to label)
 *   onChange      {function}                         (value) => void
 *   renderTrigger {function?}                        (selectedOption|null) => htmlNode; default shows label/placeholder
 *   placeholder   {string?}                          trigger text when nothing selected. Default "Select…"
 *   ariaLabel     {string?}
 *   className     {string?}                          extra classes on the .dropdown container (the <details>). To
 *                                                     weld into a daisyUI `join`, put "join-item" HERE (the <details>
 *                                                     is a direct child of the join, so it receives the -1px weld
 *                                                     margin natively) — not on triggerClass.
 *   triggerClass  {string?}                          overrides the <summary> box classes. To match a join row, pass
 *                                                     "input input-sm rounded-none …" (square middle-item box; corner
 *                                                     rounding is handled by the join on the first/last items).
 *                                                     Defaults to a standalone bordered box.
 */
export function RichSelect({
  value,
  options = [],
  onChange,
  renderTrigger,
  placeholder = "Select…",
  ariaLabel,
  className = "",
  triggerClass,
}) {
  const [open, setOpen] = useState(false);
  const detailsRef = useRef(null);

  // Close on outside click / Escape while open (native <details> does not).
  useEffect(() => {
    if (!open) return undefined;
    const onDocPointer = (e) => {
      if (detailsRef.current && !detailsRef.current.contains(e.target)) {
        setOpen(false);
      }
    };
    const onKey = (e) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDocPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDocPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const handleSelect = useCallback(
    (v) => {
      onChange?.(v);
      setOpen(false);
    },
    [onChange],
  );

  const selected = options.find((o) => o.value === value) || null;
  const detailsClass = ["dropdown config-dropdown-block", className]
    .filter(Boolean)
    .join(" ");
  const summaryClass =
    triggerClass ||
    "flex w-full items-center justify-between gap-2 rounded-lg border border-mitto-border-2 bg-mitto-surface-3 px-3 py-2 text-sm list-none cursor-pointer transition-colors hover:bg-mitto-surface-hover";

  return html`
    <details
      ref=${detailsRef}
      class=${detailsClass}
      open=${open}
      onToggle=${(e) => {
        const isOpen = e.currentTarget.open;
        if (isOpen !== open) setOpen(isOpen);
      }}
    >
      <summary class=${summaryClass} aria-label=${ariaLabel}>
        <span class="min-w-0 flex-1 flex items-center gap-2 truncate">
          ${renderTrigger
            ? renderTrigger(selected)
            : selected
              ? selected.label
              : placeholder}
        </span>
        <${ChevronDownIcon} className="w-4 h-4 opacity-60 shrink-0" />
      </summary>
      <ul
        class="dropdown-content menu menu-sm bg-mitto-surface-2 rounded-box p-2 shadow border border-mitto-border-1 max-h-64 overflow-y-auto flex-nowrap w-full z-50"
      >
        ${options.map(
          (opt) => html`
            <li key=${opt.value}>
              <button
                type="button"
                class=${opt.value === value ? "menu-active" : ""}
                onClick=${() => handleSelect(opt.value)}
              >
                ${opt.value === value
                  ? html`<${CheckIcon} className="w-4 h-4 shrink-0" />`
                  : html`<span class="inline-block w-4 h-4 shrink-0"></span>`}
                <span class="min-w-0 flex-1">
                  ${opt.render ? opt.render() : opt.label}
                </span>
              </button>
            </li>
          `,
        )}
      </ul>
    </details>
  `;
}
