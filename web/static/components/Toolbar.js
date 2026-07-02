// Mitto Web Interface - Portable Toolbar Component
// A config-driven, reusable toolbar rendered as a segmented "pill": grouped
// icon buttons (with optional active state), caret dropdowns, thin vertical
// separators between groups, an optional flex spacer, and a trailing overflow
// ("...") menu. Visual language mirrors modern floating tool palettes.
//
// Item kinds (each entry in `items`):
//   { kind: "button",    testId, icon, tip, ariaLabel, active, disabled, danger, onClick, className }
//   { kind: "dropdown",  testId, icon, tip, ariaLabel, active, caret, align, open, onToggle, menu }
//   { kind: "overflow",  testId, tip, ariaLabel, align, open, onToggle, items: [{ testId, icon, label, active, disabled, onClick }] }
//   { kind: "separator" }
//   { kind: "spacer" }
//   { kind: "custom",    content } — arbitrary node rendered inline in the pill
//                        (e.g. a count/status label). It carries no button
//                        styling, so it is unaffected by the borderless-item
//                        rules and simply sits in the flex row.
//
// Props:
//   items     - array of item descriptors (see above)
//   variant   - "floating" (auto-width pill) | "block" (fills width). Default "floating".
//   size      - daisyUI btn size: "xs" | "sm" | "md". Default "sm".
//   surface   - background utility for the pill container. Default
//               "bg-mitto-surface-2". Pass an elevated tone (e.g.
//               "bg-mitto-surface-3") to make the pill "float" above a tinted
//               panel. Kept as a single class so it never collides with a
//               second bg-* utility (equal-specificity utilities are resolved
//               by stylesheet order, not class-attribute order).
//   ariaLabel - accessible label for the container (role="toolbar").
//   testId    - data-testid for the container.
//   className - extra classes on the container.

const { html, Fragment } = window.preact;

import { ChevronDownIcon, EllipsisIcon } from "./Icons.js";

const SIZE = { xs: "btn-xs", sm: "btn-sm", md: "btn-md" };

// Shared class list for a toolbar trigger (button or <summary>). The pill
// container carries the border; items are borderless ghosts (see styles-v2.css
// .mitto-toolbar rules) so groups read as one continuous surface.
function triggerClasses(
  size,
  { active, danger, square = true, tip, extra = "" },
) {
  return [
    "btn btn-ghost",
    SIZE[size] || "btn-sm",
    square ? "btn-square" : "",
    active
      ? "btn-active text-mitto-accent-400"
      : danger
        ? "text-error hover:text-error"
        : "text-mitto-text-muted hover:text-mitto-text-strong",
    tip ? "tooltip tooltip-bottom" : "",
    extra,
  ]
    .filter(Boolean)
    .join(" ");
}

function ToolbarButton({ item, size }) {
  const disabled = !!item.disabled;
  return html`
    <button
      type="button"
      data-testid=${item.testId || null}
      onClick=${disabled ? null : item.onClick}
      aria-disabled=${disabled ? "true" : "false"}
      aria-pressed=${item.active ? "true" : "false"}
      aria-label=${item.ariaLabel || item.tip || null}
      class="${triggerClasses(size, {
        active: item.active,
        danger: item.danger,
        tip: item.tip,
        extra: item.className,
      })} ${disabled ? "opacity-40 pointer-events-none" : ""}"
      data-tip=${item.tip || null}
    >
      ${item.icon}
    </button>
  `;
}

function ToolbarDropdown({ item, size }) {
  return html`
    <details
      class="dropdown ${item.align === "end" ? "dropdown-end" : ""}"
      open=${!!item.open}
      onToggle=${(e) => {
        const open = e.currentTarget.open;
        if (open !== !!item.open) item.onToggle && item.onToggle(open);
      }}
    >
      <summary
        data-testid=${item.testId || null}
        aria-label=${item.ariaLabel || item.tip || null}
        class="${triggerClasses(size, {
          active: item.active,
          square: !item.caret,
          tip: item.tip,
          extra: `list-none ${item.caret ? "gap-1 px-2" : ""}`,
        })}"
        data-tip=${item.tip || null}
      >
        ${item.icon}
        ${item.caret
          ? html`<${ChevronDownIcon} className="w-3 h-3 opacity-70" />`
          : null}
      </summary>
      ${item.menu}
    </details>
  `;
}

function ToolbarOverflow({ item, size }) {
  return html`
    <details
      class="dropdown ${item.align === "start" ? "" : "dropdown-end"}"
      open=${!!item.open}
      onToggle=${(e) => {
        const open = e.currentTarget.open;
        if (open !== !!item.open) item.onToggle && item.onToggle(open);
      }}
    >
      <summary
        data-testid=${item.testId || null}
        aria-label=${item.ariaLabel || "More actions"}
        class="${triggerClasses(size, { tip: item.tip, extra: "list-none" })}"
        data-tip=${item.tip || null}
      >
        <${EllipsisIcon} className="w-4 h-4" />
      </summary>
      <ul
        class="dropdown-content menu menu-sm bg-mitto-surface-2 rounded-box z-10 mt-1 w-52 p-2 shadow border border-mitto-border-1"
      >
        ${(item.items || []).map(
          (mi, i) => html`
            <li
              key=${mi.testId || mi.label || i}
              class=${mi.disabled ? "menu-disabled" : ""}
            >
              <button
                type="button"
                data-testid=${mi.testId || null}
                class=${mi.active ? "menu-active" : ""}
                onClick=${mi.disabled ? null : mi.onClick}
              >
                ${mi.icon
                  ? html`<span
                      class="w-4 h-4 inline-flex items-center justify-center"
                      >${mi.icon}</span
                    >`
                  : null}
                <span>${mi.label}</span>
              </button>
            </li>
          `,
        )}
      </ul>
    </details>
  `;
}

export function Toolbar({
  items = [],
  variant = "floating",
  size = "sm",
  surface = "bg-mitto-surface-2",
  ariaLabel = "Toolbar",
  testId,
  className = "",
}) {
  const container = [
    "mitto-toolbar items-center gap-1 p-1",
    `${surface} border border-mitto-border-1 rounded-box shadow`,
    variant === "block" ? "flex w-full" : "inline-flex",
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return html`
    <div
      class=${container}
      role="toolbar"
      aria-label=${ariaLabel}
      data-testid=${testId || null}
    >
      ${items.map((item, i) => {
        const key = item.testId || `${item.kind}-${i}`;
        if (item.kind === "separator")
          return html`<span
            key=${key}
            class="mitto-toolbar-sep"
            aria-hidden="true"
          ></span>`;
        if (item.kind === "spacer")
          return html`<span key=${key} class="flex-1" aria-hidden="true"></span>`;
        if (item.kind === "custom")
          return html`<${Fragment} key=${key}>${item.content}</${Fragment}>`;
        if (item.kind === "dropdown")
          return html`<${ToolbarDropdown} key=${key} item=${item} size=${size} />`;
        if (item.kind === "overflow")
          return html`<${ToolbarOverflow} key=${key} item=${item} size=${size} />`;
        return html`<${ToolbarButton} key=${key} item=${item} size=${size} />`;
      })}
    </div>
  `;
}
