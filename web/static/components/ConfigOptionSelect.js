// Mitto Web Interface - Shared config-option selector
// A themed daisyUI dropdown for a single session config option (Mode, Model, ...)
// used by the composition toolbar (ChatInput) and the properties panels
// (ConversationPropertiesPanel, SessionPanel).

const { html, Fragment, useState, useEffect, useRef, useCallback } =
  window.preact;

import { ChevronDownIcon, CheckIcon } from "./Icons.js";
import { SearchableMenu } from "./SearchableMenu.js";
import { useSearchableMenu } from "../hooks/useSearchableMenu.js";
import { registerExclusivePicker } from "../utils/pickerExclusion.js";

function modelOptionMatches(option, query) {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return (
    String(option.name ?? "").toLowerCase().includes(needle) ||
    String(option.value ?? "").toLowerCase().includes(needle)
  );
}

function defaultModelHighlight(options, query, currentValue) {
  if (!options.length) return -1;
  if (!query.trim()) {
    const current = options.findIndex((opt) => opt.value === currentValue);
    return current >= 0 ? current : 0;
  }
  return 0;
}

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
 * @param {Function} [props.onShowToast]           - ({message, style}) => void, used to surface an optimistic-
 *                                                    update rollback (the server rejected the change).
 */
export function ConfigOptionSelect({
  configOption,
  onSetConfigOption,
  isStreaming = false,
  variant = "block",
  placement = "bottom",
  showDescription = false,
  disableWhileStreaming = false,
  onShowToast,
}) {
  const [localValue, setLocalValue] = useState(configOption.current_value);
  const [open, setOpen] = useState(false);
  const detailsRef = useRef(null);
  // Tracks an in-flight optimistic selection: { sentValue, previousValue }.
  // Cleared once the corresponding server update (accept or reject) lands.
  const pendingRef = useRef(null);
  // Skips the sync effect's body on the component's very first (mount)
  // firing — see comment below.
  const isFirstEffectRunRef = useRef(true);
  const [filterText, setFilterText] = useState("");
  const filterInputRef = useRef(null);
  const triggerRef = useRef(null);
  const isModel = configOption.category === "model";

  // Sync local value with the server's view of this option. Depends on the
  // WHOLE `configOption` object (not just `current_value`) because a
  // rejected optimistic change is re-broadcast with the value UNCHANGED —
  // if this effect only depended on `current_value`, that case would never
  // re-fire and `localValue` would stay stuck at the rejected optimistic
  // value. Every genuine server-originated update (full-snapshot replace or
  // single-option patch) produces a new `configOption` object reference, so
  // depending on the object itself reliably detects a same-value rejection.
  //
  // This effect always fires once on mount (standard hook semantics), even
  // though `localValue`'s initial state already equals
  // `configOption.current_value` — that mount firing is a no-op by
  // definition. Crucially, a user can click an option (synchronously
  // setting `pendingRef.current`) before this mount effect actually runs
  // (effects are deferred relative to the synchronous event handler), which
  // would otherwise make the mount firing misread the fresh optimistic
  // selection as a server response and immediately revert it. Skip the body
  // entirely on that first run to avoid this race.
  useEffect(() => {
    if (isFirstEffectRunRef.current) {
      isFirstEffectRunRef.current = false;
      return;
    }
    const pending = pendingRef.current;
    if (!pending) {
      setLocalValue(configOption.current_value);
      return;
    }
    pendingRef.current = null;
    if (configOption.current_value === pending.sentValue) {
      // Server confirmed the optimistic change.
      setLocalValue(configOption.current_value);
      return;
    }
    // Server rejected (or otherwise overrode) the optimistic change —
    // revert to the authoritative value and surface the failure.
    setLocalValue(configOption.current_value);
    onShowToast?.({
      message: `Could not change ${configOption.name} — reverted to the current value.`,
      style: "error",
    });
  }, [configOption]);

  // Close on outside click / Escape while open (native <details> does not)
  useEffect(() => {
    if (!open) return undefined;
    const onDocPointer = (e) => {
      if (detailsRef.current && !detailsRef.current.contains(e.target)) {
        setOpen(false);
      }
    };
    const onKey = (e) => {
      if (e.key !== "Escape") return;
      setOpen(false);
      if (isModel) triggerRef.current?.focus();
    };
    document.addEventListener("mousedown", onDocPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDocPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, isModel]);

  const handleSelect = useCallback(
    (newValue, restoreFocus = false) => {
      pendingRef.current = {
        sentValue: newValue,
        previousValue: configOption.current_value,
      };
      setLocalValue(newValue); // optimistic
      onSetConfigOption?.(configOption.id, newValue);
      setOpen(false);
      if (restoreFocus) triggerRef.current?.focus();
    },
    [configOption.id, configOption.current_value, onSetConfigOption],
  );

  const closeSearchableMenu = useCallback(() => {
    setOpen(false);
    triggerRef.current?.focus();
  }, []);

  const disabled = disableWhileStreaming && isStreaming;
  const isToolbar = variant === "toolbar";

  const currentOption = configOption.options?.find(
    (o) => o.value === localValue,
  );
  const currentLabel = currentOption
    ? currentOption.name
    : localValue || configOption.name;
  const sortedOptions = isModel
    ? [...(configOption.options || [])].sort((a, b) =>
        a.name.localeCompare(b.name, undefined, { sensitivity: "base" }),
      )
    : configOption.options;
  const menuOptions = isModel
    ? (sortedOptions || []).filter((opt) => modelOptionMatches(opt, filterText))
    : sortedOptions;

  const {
    selectedIndex,
    selectedItemRef,
    handleKeyDown,
    resetSelection,
    highlightIndex,
  } = useSearchableMenu({
    items: isModel ? menuOptions || [] : [],
    onAccept: (option) => handleSelect(option.value, true),
    onClose: closeSearchableMenu,
  });

  const highlightedIndex = isModel
    ? menuOptions?.length
      ? Math.min(selectedIndex, menuOptions.length - 1)
      : -1
    : -1;

  const handleFilterChange = (value) => {
    setFilterText(value);
    if (!isModel) return;
    const next = (sortedOptions || []).filter((opt) =>
      modelOptionMatches(opt, value),
    );
    highlightIndex(
      next.length ? defaultModelHighlight(next, value, localValue) : -1,
    );
  };

  useEffect(() => {
    if (!open) {
      setFilterText("");
      resetSelection();
      return;
    }
    if (!isModel) return;
    setFilterText("");
    highlightIndex(defaultModelHighlight(sortedOptions || [], "", localValue));
    filterInputRef.current?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) return undefined;
    return registerExclusivePicker(() => setOpen(false));
  }, [open]);

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
    placement === "top"
      ? "chat-input-config-dropdown"
      : "config-dropdown-block",
    isToolbar ? "" : "w-full",
    isModel && isToolbar ? "picker-model-toolbar" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const menuBaseClass = "dropdown-content menu menu-sm";
  const plainMenuClass = [
    menuBaseClass,
    "bg-mitto-surface-2 rounded-box p-2 shadow border border-mitto-border-1",
    "max-h-64 overflow-y-auto flex-nowrap",
    isToolbar ? "w-[250px]" : "w-full",
  ].join(" ");
  const searchableMenuClass = [
    menuBaseClass,
    "picker-popover",
    isToolbar ? "picker-popover--toolbar" : "w-full",
  ].join(" ");

  const renderOption = (opt, index) => {
    const isSelected = opt.value === localValue;
    const isHighlighted = index === highlightedIndex;
    return html`
      <li key=${opt.value}>
        <button
          type="button"
          class=${[
            "picker-row w-full",
            isSelected ? "menu-active" : "",
            isHighlighted ? "picker-row--highlighted" : "",
          ]
            .filter(Boolean)
            .join(" ")}
          ref=${isModel
            ? (element) => {
                if (element && isHighlighted) selectedItemRef.current = element;
              }
            : undefined}
          onClick=${() => handleSelect(opt.value)}
        >
          ${isSelected
            ? html`<${CheckIcon} className="w-4 h-4" />`
            : html`<span class="inline-block w-4 h-4"></span>`}
          <span class="truncate-left min-w-0 flex-1"
            ><bdi dir="ltr">${opt.name}</bdi></span
          >
        </button>
      </li>
    `;
  };

  const trigger = isToolbar
    ? html`
        <summary
          ref=${triggerRef}
          class="btn btn-ghost btn-xs font-normal list-none flex-nowrap ${configOption.category ===
          "mode"
            ? "max-w-[264px]"
            : "max-w-[200px]"} ${open ? "" : "tooltip tooltip-top"}"
          data-tip=${tip}
          aria-label=${configOption.name}
        >
          <span class="truncate-left min-w-0"
            ><bdi dir="ltr">${currentLabel}</bdi></span
          >
          <${ChevronDownIcon} className="w-3 h-3 opacity-60" />
        </summary>
      `
    : html`
        <summary
          ref=${triggerRef}
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
          ${isModel
            ? html`
                <div class=${searchableMenuClass}>
                  <${SearchableMenu}
                    items=${menuOptions || []}
                    filterText=${filterText}
                    onFilterChange=${handleFilterChange}
                    onFilterKeyDown=${handleKeyDown}
                    filterInputRef=${filterInputRef}
                    placeholder="Filter models..."
                    emptyText="No matching models"
                    filterTestId=${`config-option-${configOption.id}-filter`}
                    listTestId=${`config-option-${configOption.id}-list`}
                  >
                    ${(menuOptions || []).map(renderOption)}
                  </${SearchableMenu}>
                </div>
              `
            : html`
                <ul class=${plainMenuClass}>
                  ${configOption.options?.map(renderOption)}
                </ul>
              `}
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
