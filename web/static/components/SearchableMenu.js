// Generic filter/list chrome. Callers own filtering, rows and focus lifecycle.
const { html, Fragment } = window.preact;

export function SearchableMenu({
  items = [],
  filterText = "",
  onFilterChange,
  onFilterKeyDown,
  filterInputRef,
  placeholder,
  emptyText,
  filterTestId,
  listTestId,
  footer = null,
  children,
}) {
  return html`
    <${Fragment}>
      <div class="px-2 pt-2 pb-1 shrink-0">
        <input
          ref=${filterInputRef}
          type="text"
          value=${filterText}
          onInput=${(e) => onFilterChange && onFilterChange(e.target.value)}
          onKeyDown=${onFilterKeyDown}
          placeholder=${placeholder}
          aria-label=${placeholder}
          autocomplete="off"
          autocorrect="off"
          autocapitalize="off"
          spellcheck=${false}
          data-testid=${filterTestId}
          class="input input-sm w-full"
        />
      </div>
      <div
        class="px-2 py-1 overflow-y-auto flex-1 min-h-0"
        style="scrollbar-gutter: stable;"
        data-testid=${listTestId}
      >
        <ul class="flex flex-col w-full p-0 m-0 list-none">
          ${children}
        </ul>
        ${items.length === 0 &&
        html`<div class="px-4 py-3 text-xs text-mitto-text-muted text-center">
          ${emptyText}
        </div>`}
      </div>
      ${footer &&
      html`<div class="px-3 py-1.5 border-t border-mitto-border-1 shrink-0">
        ${footer}
      </div>`}
    </${Fragment}>
  `;
}
