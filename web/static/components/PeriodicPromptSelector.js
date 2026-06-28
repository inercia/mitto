// Mitto Web Interface - Periodic Prompt Selector Component
// Dropdown for selecting a workspace prompt as the periodic prompt.
// Renders inline (no outer panel chrome) — meant to be embedded in PeriodicFrequencyPanel header.

const { useState, useEffect, useCallback, useRef, html } = window.preact;

import { PromptsMenu } from "./PromptsMenu.js";
import { PortalTooltip } from "./ContextMenu.js";
import { getPromptSortMode } from "../utils/storage.js";

/** Max characters of the free-text body preview rendered on the trigger button. */
const FREE_TEXT_PREVIEW_MAX = 40;

/**
 * PeriodicPromptSelector - inline dropdown for selecting a workspace prompt as the periodic prompt.
 * Renders just the trigger button + dropdown popover (no outer panel chrome).
 * The parent card (PeriodicFrequencyPanel) controls visibility via its own isOpen logic.
 *
 * @param {Object} props
 * @param {Array} props.prompts - Available workspace prompts (same as predefinedPrompts)
 * @param {string} props.selectedPromptName - Currently selected prompt name (from periodic config)
 * @param {string} props.selectedPromptBody - Free-text periodic prompt body (used when no named prompt is set)
 * @param {boolean} props.disabled - Whether the selector is read-only
 * @param {Function} props.onSelect - Callback when a prompt is selected: (promptName) => void
 * @param {boolean} props.isOpen - Kept for API compat; parent card controls visibility now (ignored here)
 */
export function PeriodicPromptSelector({
  prompts = [],
  selectedPromptName = "",
  selectedPromptBody = "",
  disabled = false,
  onSelect,
  isOpen = false,
  // When true the trigger expands to fill its container (used in the mobile
  // expanded-properties row); otherwise it stays compact (header placement).
  fullWidth = false,
  // Testid roots. Distinct prefixes let multiple instances (header + mobile
  // body) coexist in the DOM without breaking strict-mode Playwright locators.
  idPrefix = "periodic-prompt-selector",
}) {
  const [showDropdown, setShowDropdown] = useState(false);
  const [filterText, setFilterText] = useState("");
  const dropdownRef = useRef(null);
  const filterInputRef = useRef(null);

  // Close dropdown when clicking outside
  useEffect(() => {
    if (!showDropdown) return;
    const handleClickOutside = (e) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target)) {
        setShowDropdown(false);
        setFilterText("");
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [showDropdown]);

  // Focus filter input when dropdown opens
  useEffect(() => {
    if (showDropdown && filterInputRef.current) {
      filterInputRef.current.focus();
    }
  }, [showDropdown]);

  const handleToggle = useCallback(() => {
    if (disabled) return;
    setShowDropdown((prev) => !prev);
    setFilterText("");
  }, [disabled]);

  const handleSelect = useCallback(
    (prompt) => {
      if (onSelect) {
        onSelect(prompt.name);
      }
      setShowDropdown(false);
      setFilterText("");
    },
    [onSelect],
  );

  // Three display modes: named prompt > free-text body preview > empty placeholder.
  // The free-text case shows the first non-empty line, trimmed and truncated to
  // FREE_TEXT_PREVIEW_MAX, with the full body available on hover via PortalTooltip.
  const freeTextBody = !selectedPromptName ? (selectedPromptBody || "").trim() : "";
  let freeTextPreview = "";
  if (freeTextBody) {
    const firstLine = freeTextBody.split(/\r?\n/, 1)[0].trim();
    freeTextPreview =
      firstLine.length > FREE_TEXT_PREVIEW_MAX
        ? firstLine.slice(0, FREE_TEXT_PREVIEW_MAX) + "…"
        : firstLine;
  }
  const hasFreeText = freeTextPreview.length > 0;
  const displayName = selectedPromptName || freeTextPreview || "Select a prompt...";
  const isConfigured = !!selectedPromptName || hasFreeText;

  // Cursor-anchored tooltip showing the full free-text body on hover. Gated on
  // hover-capable pointers so taps on touch devices don't strand a bubble.
  const [bodyTip, setBodyTip] = useState(null);
  const bodyTipTimerRef = useRef(null);
  const supportsHover =
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(hover: hover)").matches;
  const showBodyTip = useCallback(
    (e) => {
      if (!supportsHover || !hasFreeText) return;
      const x = e.clientX;
      const y = e.clientY;
      clearTimeout(bodyTipTimerRef.current);
      bodyTipTimerRef.current = setTimeout(
        () => setBodyTip({ x, y, text: freeTextBody }),
        250,
      );
    },
    [supportsHover, hasFreeText, freeTextBody],
  );
  const hideBodyTip = useCallback(() => {
    clearTimeout(bodyTipTimerRef.current);
    setBodyTip(null);
  }, []);
  useEffect(() => () => clearTimeout(bodyTipTimerRef.current), []);

  // Respect the user's global prompt sort preference (name vs color).
  const sortMode = getPromptSortMode();

  // Inline: relative container anchors the dropdown; ref covers both trigger and toggle
  // so click-outside detection works correctly.
  return html`
    <div
      class="relative flex items-center gap-1 min-w-0 ${fullWidth
        ? "w-full"
        : ""}"
      data-testid=${idPrefix}
      ref=${dropdownRef}
    >
      <!-- Trigger button -->
      <button
        type="button"
        onClick=${handleToggle}
        disabled=${disabled}
        onMouseEnter=${showBodyTip}
        onMouseLeave=${hideBodyTip}
        onMouseDown=${hideBodyTip}
        class="h-8 px-3 bg-white dark:bg-mitto-surface-2 border border-mitto-border dark:border-mitto-border-2 rounded text-sm text-left flex items-center gap-2 focus:outline-none focus:ring-1 focus:ring-mitto-accent-500 transition-colors min-w-0 ${fullWidth
          ? "w-full flex-1"
          : "max-w-48"} ${disabled
          ? "opacity-50 cursor-not-allowed"
          : "cursor-pointer hover:border-mitto-accent-500/50"}"
        data-testid="${idPrefix}-button"
      >
        <span
          class="truncate flex-1 ${isConfigured
            ? "text-mitto-text-strong"
            : "text-mitto-text-secondary dark:text-mitto-text-500"}"
          >${displayName}</span
        >
        <svg
          class="w-4 h-4 shrink-0 text-mitto-text-secondary transition-transform ${showDropdown
            ? "rotate-180"
            : ""}"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M19 9l-7 7-7-7"
          />
        </svg>
      </button>

      ${bodyTip &&
      html`<${PortalTooltip} x=${bodyTip.x} y=${bodyTip.y} text=${bodyTip.text} />`}

      <!-- Dropdown panel (appears ABOVE the trigger button) -->
      ${showDropdown &&
      html`
        <div
          class="absolute bottom-full left-0 mb-1 bg-mitto-surface-2 border border-mitto-border-2 rounded-lg z-50 overflow-hidden flex flex-col"
          style="width: 20rem; min-width: 20rem; max-width: 20rem; max-height: 400px; box-shadow: 0 20px 40px rgba(0, 0, 0, 0.5), 0 8px 16px rgba(0, 0, 0, 0.4), 0 0 0 1px rgba(255, 255, 255, 0.1);"
          data-testid="${idPrefix}-dropdown"
        >
          <${PromptsMenu}
            prompts=${prompts}
            filterText=${filterText}
            onFilterChange=${(value) => setFilterText(value)}
            filterInputRef=${filterInputRef}
            sortMode=${sortMode}
            onSelect=${(prompt) => handleSelect(prompt)}
            selectedName=${selectedPromptName}
            showSourceBadge=${true}
            placeholder="Filter prompts..."
            emptyText="No matching prompts"
            keyPrefix="periodic-prompts"
            filterTestId="${idPrefix}-search"
            listTestId="${idPrefix}-list"
          />
        </div>
      `}
    </div>
  `;
}
