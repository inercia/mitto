// Mitto Web Interface — IconPicker component
// A small inline dropdown that lets the user pick an icon from PROMPT_ICONS.

const { html } = window.preact;

import { PROMPT_ICONS, getPromptIconOrDefault } from "./Icons.js";

/**
 * IconPicker renders a small daisyUI dropdown for selecting a PROMPT_ICONS key.
 *
 * Props:
 *   value           {string}   - Current icon name (may be empty).
 *   onChange        {function} - Called with the new icon name string.
 *   disabled        {boolean}  - When true, the trigger button is disabled.
 *   defaultIconName {string}   - Icon to preview when no explicit icon is set
 *                                (e.g. the linked prompt's own icon). Selecting
 *                                the "default" option clears value to "" so this
 *                                fallback is used at render time.
 *   className       {string}   - Extra classes for the trigger button (e.g.
 *                                "join-item" to fit inside a daisyUI join group).
 */
export function IconPicker({
  value,
  onChange,
  disabled,
  defaultIconName,
  className = "",
}) {
  const hasIcon = !!(value && String(value).trim());
  // When no explicit icon is chosen, preview the prompt's own icon (dimmed) so
  // the user sees what will actually render; fall back to the generic default.
  const CurrentIcon = hasIcon
    ? getPromptIconOrDefault(value)
    : getPromptIconOrDefault(defaultIconName);
  const DefaultIcon = getPromptIconOrDefault(defaultIconName);
  const iconNames = Object.keys(PROMPT_ICONS);

  const handleSelect = (ev, name) => {
    onChange && onChange(name);
    // Close the dropdown by blurring the focused element (daisyUI CSS pattern).
    ev.currentTarget.blur();
    if (document.activeElement) document.activeElement.blur();
  };

  // daisyUI CSS-driven dropdown: trigger is a <div role="button"> (a real
  // <button> does NOT reliably receive focus on click in WebKit/Safari, so
  // :focus-within never fires). Content is always rendered; visibility is
  // controlled purely by focus.
  return html`
    <div class="dropdown">
      <div
        tabindex=${disabled ? "-1" : "0"}
        role="button"
        aria-label="Pick icon"
        aria-haspopup="true"
        aria-disabled=${disabled ? "true" : "false"}
        class="btn btn-ghost btn-square btn-sm ${className} ${disabled ? "btn-disabled" : ""}"
      >
        <span class="w-4 h-4 ${hasIcon ? "" : "opacity-40"}">
          <${CurrentIcon} className="w-4 h-4" />
        </span>
      </div>
      <div
        tabindex="0"
        class="dropdown-content z-50 flex flex-wrap gap-1 p-2 w-64 bg-base-200 rounded-box shadow-xl"
        role="listbox"
        aria-label="Available icons"
      >
        <!-- Default option: clears the override so the prompt's own icon is
             used. Styled distinctively (accent dashed border + accent tint) to
             stand apart from the concrete icon choices. -->
        <button
          type="button"
          role="option"
          aria-selected=${!hasIcon}
          aria-label="Use the prompt's own icon"
          title="Use the prompt's own icon"
          onClick=${(ev) => handleSelect(ev, "")}
          class="btn btn-ghost btn-square btn-sm border-2 border-dashed border-mitto-accent text-mitto-accent ${!hasIcon ? "bg-base-300 ring-1 ring-mitto-accent" : ""}"
        >
          <span class="w-4 h-4">
            <${DefaultIcon} className="w-4 h-4" />
          </span>
        </button>
        ${iconNames.map((name) => {
          const Icon = PROMPT_ICONS[name];
          const isSelected =
            (value || "").trim().toLowerCase() === name.trim().toLowerCase();
          return html`
            <button
              key=${name}
              type="button"
              role="option"
              aria-selected=${isSelected}
              aria-label=${name}
              title=${name}
              onClick=${(ev) => handleSelect(ev, name)}
              class="btn btn-ghost btn-square btn-sm ${isSelected ? "bg-base-300" : ""}"
            >
              <span class="w-4 h-4">
                <${Icon} className="w-4 h-4" />
              </span>
            </button>
          `;
        })}
      </div>
    </div>
  `;
}

export default IconPicker;
