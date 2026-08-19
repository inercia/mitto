// Native select with the explicit dropdown affordance used in narrow panels.
const { html } = window.preact;

import { ChevronDownIcon } from "./Icons.js";

export function NativeSelectWithChevron({
  ariaLabel,
  value,
  onChange,
  testId,
  disabled = false,
  wrapperClass = "w-full",
  children,
}) {
  return html`
    <div
      class="relative ${wrapperClass}"
      style="position:relative;"
      data-testid="${testId}-wrap"
    >
      <select
        class="select select-sm w-full pr-8"
        style="appearance:none;-webkit-appearance:none;background-image:none;"
        aria-label=${ariaLabel}
        data-testid=${testId}
        value=${value}
        disabled=${disabled}
        onChange=${onChange}
      >
        ${children}
      </select>
      <span
        aria-hidden="true"
        data-testid="${testId}-chevron"
        style="position:absolute;right:0.5rem;top:50%;transform:translateY(-50%);pointer-events:none;"
      >
        <${ChevronDownIcon} className="w-4 h-4 opacity-60" />
      </span>
    </div>
  `;
}