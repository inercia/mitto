// Mitto Web Interface - Periodic Prompt Selector Component
// Dropdown for selecting a workspace prompt as the periodic prompt

const { useState, useEffect, useCallback, useRef, html } = window.preact;

import { PromptsMenu } from "./PromptsMenu.js";
import { getPromptSortMode } from "../utils/storage.js";

/**
 * PeriodicPromptSelector - dropdown for selecting a workspace prompt as the periodic prompt
 * @param {Object} props
 * @param {Array} props.prompts - Available workspace prompts (same as predefinedPrompts)
 * @param {string} props.selectedPromptName - Currently selected prompt name (from periodic config)
 * @param {boolean} props.disabled - Whether the selector is read-only
 * @param {Function} props.onSelect - Callback when a prompt is selected: (promptName) => void
 * @param {boolean} props.isOpen - Whether the panel is visible
 * @param {boolean} props.isPromptAreaVisible - Whether the prompt composition area below is visible
 * @param {Function} props.onTogglePromptArea - Callback to toggle prompt composition area visibility
 */
export function PeriodicPromptSelector({
  prompts = [],
  selectedPromptName = "",
  disabled = false,
  onSelect,
  isOpen = false,
  isPromptAreaVisible = false,
  onTogglePromptArea,
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

  // Panel classes - matches PeriodicFrequencyPanel style
  const panelClasses = `periodic-prompt-selector w-full bg-mitto-surface-hover dark:bg-mitto-surface-3/95 backdrop-blur-sm border border-mitto-border dark:border-mitto-border-2 rounded-lg overflow-visible transition-all duration-300 ease-out ${
    isOpen
      ? "opacity-100 mb-3"
      : "opacity-0 pointer-events-none h-0 border-0 mb-0"
  }`;

  const panelStyle = isOpen ? "height: 44px; position: relative;" : "height: 0px;";

  const displayName = selectedPromptName || "Select a prompt...";

  // Respect the user's global prompt sort preference (name vs color).
  const sortMode = getPromptSortMode();

  return html`
    <div
      class="${panelClasses}"
      style="${panelStyle}"
      data-testid="periodic-prompt-selector"
      ref=${dropdownRef}
    >
      <div class="h-full px-4 flex items-center gap-3 text-sm">
        <!-- Label -->
        <span class="text-mitto-text-muted dark:text-mitto-text-300 shrink-0 font-medium">Prompt:</span>

        <!-- Dropdown trigger button -->
        <button
          type="button"
          onClick=${handleToggle}
          disabled=${disabled}
          class="flex-1 h-8 px-3 bg-white dark:bg-mitto-surface-2 border border-mitto-border dark:border-mitto-border-2 rounded text-sm text-left flex items-center gap-2 focus:outline-none focus:ring-1 focus:ring-mitto-accent-500 transition-colors ${
            disabled
              ? "opacity-50 cursor-not-allowed"
              : "cursor-pointer hover:border-mitto-accent-500/50"
          }"
          data-testid="periodic-prompt-selector-button"
        >
          <span class="truncate flex-1 ${selectedPromptName ? "text-mitto-text-strong" : "text-mitto-text-secondary dark:text-mitto-text-500"}">${displayName}</span>
          <svg class="w-4 h-4 shrink-0 text-mitto-text-secondary transition-transform ${showDropdown ? "rotate-180" : ""}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
          </svg>
        </button>

        <!-- Toggle prompt composition area button -->
        ${onTogglePromptArea && html`
          <button
            type="button"
            onClick=${onTogglePromptArea}
            class="btn btn-ghost btn-square btn-sm shrink-0 text-mitto-text-muted hover:text-mitto-text-strong"
            title=${isPromptAreaVisible
              ? "Hide message input"
              : "Show message input"}
            data-testid="periodic-toggle-prompt-area"
          >
            <svg
              class="w-4 h-4 transition-transform ${isPromptAreaVisible ? "rotate-180" : ""}"
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
        `}
      </div>

      <!-- Dropdown panel (appears ABOVE the selector) -->
      ${showDropdown && html`
        <div
          class="absolute bottom-full left-0 right-0 mb-1 bg-base-200 rounded-box shadow-xl z-50 overflow-hidden flex flex-col"
          style="max-height: 360px;"
          data-testid="periodic-prompt-selector-dropdown"
        >
          <${PromptsMenu}
            prompts=${prompts}
            filterText=${filterText}
            onFilterChange=${(value) => setFilterText(value)}
            filterInputRef=${filterInputRef}
            sortMode=${sortMode}
            onSelect=${(prompt) => handleSelect(prompt)}
            selectedName=${selectedPromptName}
            placeholder="Search prompts..."
            emptyText="No matching prompts"
            keyPrefix="periodic-prompts"
            filterTestId="periodic-prompt-selector-search"
            listTestId="periodic-prompt-selector-list"
          />
        </div>
      `}
    </div>
  `;
}