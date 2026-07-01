// Mitto Web Interface - Shared config-option selector
// A themed daisyUI dropdown for a single session config option (Mode, Model, ...)
// used by the composition toolbar (ChatInput) and the properties panels
// (ConversationPropertiesPanel, SessionPanel).

const { html, Fragment, useState, useEffect, useRef, useCallback } =
  window.preact;

import { ChevronDownIcon, CheckIcon } from "./Icons.js";

/**
 * ConfigOptionSelect - daisyUI dropdown for a config option with optimistic
 * local state (the trigger label does not revert to the old value while waiting
 * for the server's config_option_changed WebSocket response).
 *
 * @param {Object}   props.configOption           - { id, name, description, current_value, options: [{value,name,description}] }
 * @param {Function} props.onSetConfigOption       - (id, value) => void
 * @param {boolean}  [props.isStreaming]           - whether the session is streaming
 * @param {"toolbar"|"block"} [props.variant]      - "toolbar" = compact ghost pill (ChatInput bottom bar);
 *                                                    "block" = full-width field (properties panels). Default "block".
 * @param {"top"|"bottom"} [props.placement]       - menu open direction. Default "bottom".
 * @param {boolean}  [props.showDescription]       - render the selected option's description below. Default false.
 * @param {boolean}  [props.disableWhileStreaming] - disable the control while streaming. Default false.
 */
export function ConfigOptionSelect({
  configOption,
  onSetConfigOption,
  isStreaming = false,
  variant = "block",
  placement = "bottom",
  showDescription = false,
  disableWhileStreaming = false,
}) {
  const [localValue, setLocalValue] = useState(configOption.current_value);
  const [open, setOpen] = useState(false);
  const detailsRef = useRef(null);

  // Sync local value when the server confirms the change
  useEffect(() => {
    setLocalValue(configOption.current_value);
  }, [configOption.current_value]);

  // Close on outside click / Escape while open (native <details> does not)
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
    (newValue) => {
      setLocalValue(newValue); // optimistic
      onSetConfigOption?.(configOption.id, newValue);
      setOpen(false);
    },
    [configOption.id, onSetConfigOption],
  );

  const disabled = disableWhileStreaming && isStreaming;
  const isToolbar = variant === "toolbar";

  const currentOption = configOption.options?.find(
    (o) => o.value === localValue,
  );
  const currentLabel = currentOption
    ? currentOption.name
    : localValue || configOption.name;

  const tip = disabled
    ? `Cannot change ${configOption.name.toLowerCase()} while streaming`
    : isStreaming
      ? `${configOption.name} will apply to the next prompt`
      : configOption.description || `Select ${configOption.name.toLowerCase()}`;

  // Placement classes reuse explicit-position scoped CSS (styles.css) because
  // daisyUI's default dropdown positioning relies on CSS anchor `position-area`,
  // which is unreliable in WKWebView.
  const detailsClass = [
    "dropdown",
    placement === "top" ? "chat-input-config-dropdown" : "config-dropdown-block",
    isToolbar ? "" : "w-full",
  ]
    .filter(Boolean)
    .join(" ");

  const menuClass = [
    "dropdown-content menu menu-sm bg-mitto-surface-2 rounded-box p-2 shadow",
    "border border-mitto-border-1 max-h-64 overflow-y-auto flex-nowrap",
    isToolbar ? "w-52" : "w-full",
  ].join(" ");

  const trigger = isToolbar
    ? html`
        <summary
          class="btn btn-ghost btn-xs font-normal list-none flex-nowrap max-w-[200px] ${open
            ? ""
            : "tooltip tooltip-top"}"
          data-tip=${tip}
          aria-label=${configOption.name}
        >
          <span class="truncate min-w-0">${currentLabel}</span>
          <${ChevronDownIcon} className="w-3 h-3 opacity-60" />
        </summary>
      `
    : html`
        <summary
          class="flex w-full items-center justify-between gap-2 rounded-lg border border-mitto-border-2 bg-mitto-surface-3 px-3 py-2 text-sm list-none transition-colors ${disabled
            ? "opacity-50 cursor-not-allowed pointer-events-none"
            : "cursor-pointer hover:bg-mitto-surface-hover"}"
          title=${tip}
          aria-label=${configOption.name}
          onClick=${disabled ? (e) => e.preventDefault() : undefined}
        >
          <span class="truncate min-w-0">${currentLabel}</span>
          <${ChevronDownIcon} className="w-4 h-4 opacity-60 shrink-0" />
        </summary>
      `;

  return html`
    <${Fragment}>
      <details
        ref=${detailsRef}
        class=${detailsClass}
        open=${open}
        onToggle=${(e) => {
          const isOpen = e.currentTarget.open;
          if (isOpen !== open) setOpen(isOpen);
        }}
      >
        ${trigger}
        ${!disabled &&
        html`
          <ul class=${menuClass}>
            ${configOption.options?.map(
              (opt) => html`
                <li key=${opt.value}>
                  <button
                    type="button"
                    class=${opt.value === localValue ? "menu-active" : ""}
                    onClick=${() => handleSelect(opt.value)}
                  >
                    ${opt.value === localValue
                      ? html`<${CheckIcon} className="w-4 h-4" />`
                      : html`<span class="inline-block w-4 h-4"></span>`}
                    <span class="truncate">${opt.name}</span>
                  </button>
                </li>
              `,
            )}
          </ul>
        `}
      </details>
      ${showDescription &&
      currentOption?.description &&
      html`
        <p class="mt-1 text-xs text-mitto-text-500">
          ${currentOption.description}
        </p>
      `}
    <//>
  `;
}
