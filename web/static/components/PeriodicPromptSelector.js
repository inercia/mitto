// Mitto Web Interface - Periodic Prompt Selector Component
// Dropdown for selecting a workspace prompt as the periodic prompt

const {
  useState,
  useEffect,
  useCallback,
  useMemo,
  useRef,
  html,
} = window.preact;

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

  // Filter and group prompts
  const { groupedPrompts, ungroupedPrompts, sortedGroupNames, hasResults } =
    useMemo(() => {
      const lowerFilter = filterText.toLowerCase().trim();
      const filtered = lowerFilter
        ? prompts.filter(
            (p) =>
              (p.name || "").toLowerCase().includes(lowerFilter) ||
              (p.description || "").toLowerCase().includes(lowerFilter),
          )
        : prompts;

      const grouped = {};
      const ungrouped = [];
      filtered.forEach((prompt) => {
        if (prompt.group) {
          if (!grouped[prompt.group]) grouped[prompt.group] = [];
          grouped[prompt.group].push(prompt);
        } else {
          ungrouped.push(prompt);
        }
      });
      // Sort within groups
      Object.keys(grouped).forEach((g) => {
        grouped[g].sort((a, b) => a.name.localeCompare(b.name));
      });
      const sortedUngrouped = [...ungrouped].sort((a, b) =>
        a.name.localeCompare(b.name),
      );

      return {
        groupedPrompts: grouped,
        ungroupedPrompts: sortedUngrouped,
        sortedGroupNames: Object.keys(grouped).sort(),
        hasResults: filtered.length > 0,
      };
    }, [prompts, filterText]);

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
  const panelClasses = `periodic-prompt-selector w-full bg-slate-100 dark:bg-slate-700/95 backdrop-blur-sm border border-slate-300 dark:border-slate-600 rounded-lg overflow-visible transition-all duration-300 ease-out ${
    isOpen
      ? "opacity-100 mb-3"
      : "opacity-0 pointer-events-none h-0 border-0 mb-0"
  }`;

  const panelStyle = isOpen ? "height: 44px; position: relative;" : "height: 0px;";

  const displayName = selectedPromptName || "Select a prompt...";

  // Render a single prompt item in the dropdown
  const renderPromptItem = (prompt) => {
    const isSelected = prompt.name === selectedPromptName;
    return html`
      <button
        key=${"periodic-prompt-" + prompt.name}
        type="button"
        onClick=${() => handleSelect(prompt)}
        title=${prompt.description || prompt.name}
        class="w-full text-left px-4 py-2.5 text-sm transition-all flex items-center gap-2 ${isSelected
          ? "bg-mitto-accent-600/20 text-white"
          : "text-mitto-text hover:bg-slate-600/50"}"
      >
        <svg class="w-4 h-4 shrink-0 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
        </svg>
        <span class="truncate flex-1">${prompt.name}</span>
        ${isSelected && html`
          <svg class="w-4 h-4 shrink-0 text-mitto-accent" fill="currentColor" viewBox="0 0 20 20">
            <path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd" />
          </svg>
        `}
      </button>
    `;
  };


  return html`
    <div
      class="${panelClasses}"
      style="${panelStyle}"
      data-testid="periodic-prompt-selector"
      ref=${dropdownRef}
    >
      <div class="h-full px-4 flex items-center gap-3 text-sm">
        <!-- Label -->
        <span class="text-slate-600 dark:text-gray-300 shrink-0 font-medium">Prompt:</span>

        <!-- Dropdown trigger button -->
        <button
          type="button"
          onClick=${handleToggle}
          disabled=${disabled}
          class="flex-1 h-8 px-3 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-600 rounded text-sm text-left flex items-center gap-2 focus:outline-none focus:ring-1 focus:ring-mitto-accent-500 transition-colors ${
            disabled
              ? "opacity-50 cursor-not-allowed"
              : "cursor-pointer hover:border-mitto-accent-500/50"
          }"
          data-testid="periodic-prompt-selector-button"
        >
          <span class="truncate flex-1 ${selectedPromptName ? "text-slate-900 dark:text-white" : "text-slate-400 dark:text-gray-500"}">${displayName}</span>
          <svg class="w-4 h-4 shrink-0 text-slate-400 transition-transform ${showDropdown ? "rotate-180" : ""}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
          </svg>
        </button>

        <!-- Toggle prompt composition area button -->
        ${onTogglePromptArea && html`
          <button
            type="button"
            onClick=${onTogglePromptArea}
            class="shrink-0 p-1.5 text-mitto-text-muted hover:text-white transition-colors rounded hover:bg-mitto-surface-hover"
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
          class="absolute bottom-full left-0 right-0 mb-1 bg-mitto-surface-2 border border-mitto-border-2 rounded-lg shadow-xl z-50 overflow-hidden"
          style="max-height: 360px; display: flex; flex-direction: column;"
          data-testid="periodic-prompt-selector-dropdown"
        >
          <!-- Search filter -->
          <div class="p-2 border-b border-mitto-border-1">
            <input
              ref=${filterInputRef}
              type="text"
              value=${filterText}
              onInput=${(e) => setFilterText(e.target.value)}
              placeholder="Search prompts..."
              class="w-full h-8 px-3 bg-mitto-surface-1 border border-mitto-border-2 rounded text-sm text-mitto-text-strong placeholder-gray-500 focus:outline-none focus:ring-1 focus:ring-mitto-accent-500"
              data-testid="periodic-prompt-selector-search"
            />
          </div>

          <!-- Prompt list -->
          <div class="overflow-y-auto flex-1" style="max-height: 310px;">
            ${sortedGroupNames.map(
              (groupName) => html`
                <div key=${"pps-group-" + groupName}>
                  <div class="px-4 py-2 text-xs font-semibold text-mitto-text-muted uppercase tracking-wider bg-slate-700/30">
                    ${groupName}
                  </div>
                  ${groupedPrompts[groupName].map((prompt) => renderPromptItem(prompt))}
                </div>
              `,
            )}
            ${ungroupedPrompts.length > 0 ? html`
              <div key="pps-group-other">
                <div class="px-4 py-2 text-xs font-semibold text-mitto-text-muted uppercase tracking-wider bg-slate-700/30">
                  Other
                </div>
                ${ungroupedPrompts.map((prompt) => renderPromptItem(prompt))}
              </div>
            ` : ""}
            ${!hasResults ? html`
              <div class="px-4 py-3 text-xs text-mitto-text-muted text-center">
                No matching prompts
              </div>
            ` : ""}
          </div>
        </div>
      `}
    </div>
  `;
}