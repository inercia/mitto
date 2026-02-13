// Mitto Web Interface - Slash Command Picker Component
// Displays available slash commands for autocomplete when user types '/' at start of input

const { useEffect, useRef, useCallback, html } = window.preact;

/**
 * SlashCommandPicker component - displays filtered slash commands for autocomplete
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the picker is visible
 * @param {Function} props.onClose - Callback to close the picker
 * @param {Function} props.onSelect - Callback when a command is selected (command) => void
 * @param {Array} props.commands - Array of available commands { name, description, input_hint }
 * @param {string} props.filter - Current filter text (text after '/')
 * @param {number} props.selectedIndex - Currently highlighted command index
 * @param {Function} props.onSelectedIndexChange - Callback when selection changes
 */
export function SlashCommandPicker({
  isOpen,
  onClose,
  onSelect,
  commands = [],
  filter = "",
  selectedIndex = 0,
  onSelectedIndexChange,
}) {
  const pickerRef = useRef(null);
  const listRef = useRef(null);

  // Filter commands based on the filter text (case-insensitive prefix match on name)
  const filteredCommands = commands.filter((cmd) =>
    cmd.name.toLowerCase().startsWith(filter.toLowerCase()),
  );

  // Scroll selected item into view
  useEffect(() => {
    if (!isOpen || !listRef.current || filteredCommands.length === 0) return;

    const selectedItem = listRef.current.querySelector(
      `[data-index="${selectedIndex}"]`,
    );
    if (selectedItem) {
      selectedItem.scrollIntoView({ block: "nearest", behavior: "smooth" });
    }
  }, [selectedIndex, isOpen, filteredCommands.length]);

  // Close picker when clicking outside
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event) => {
      if (pickerRef.current && !pickerRef.current.contains(event.target)) {
        onClose();
      }
    };

    // Add listener with a small delay to avoid catching the triggering keypress
    const timeoutId = setTimeout(() => {
      document.addEventListener("click", handleClickOutside);
    }, 10);

    return () => {
      clearTimeout(timeoutId);
      document.removeEventListener("click", handleClickOutside);
    };
  }, [isOpen, onClose]);

  // Handle command selection
  const handleSelect = useCallback(
    (command) => {
      if (onSelect) {
        onSelect(command);
      }
    },
    [onSelect],
  );

  // Handle mouse hover to change selection
  const handleMouseEnter = useCallback(
    (index) => {
      if (onSelectedIndexChange) {
        onSelectedIndexChange(index);
      }
    },
    [onSelectedIndexChange],
  );

  // Calculate panel height based on number of items
  // Each item is ~40px, header is ~36px, show max 8 items
  const maxVisibleItems = 8;
  const itemHeight = 40;
  const headerHeight = 36;
  const visibleItems = Math.min(filteredCommands.length, maxVisibleItems);
  const panelHeight = headerHeight + visibleItems * itemHeight;

  // Panel classes - positioned as floating overlay above the input
  const pickerClasses = `slash-command-picker absolute bottom-full left-0 right-0 w-full bg-slate-700/95 backdrop-blur-sm border-t border-l border-r border-slate-600 rounded-t-lg overflow-hidden z-20 transition-all duration-200 ease-out ${
    isOpen && filteredCommands.length > 0
      ? "opacity-100"
      : "opacity-0 pointer-events-none border-0"
  }`;

  const pickerStyle =
    isOpen && filteredCommands.length > 0
      ? `height: ${panelHeight}px; box-shadow: 0 -8px 16px rgba(0, 0, 0, 0.3);`
      : "height: 0px;";

  return html`
    <div
      ref=${pickerRef}
      class=${pickerClasses}
      style="transform-origin: bottom; ${pickerStyle}"
      data-testid="slash-command-picker"
    >
      <div
        class="slash-picker-header px-3 py-2 border-b border-slate-700 flex items-center justify-between"
      >
        <span class="text-xs font-medium text-gray-400 uppercase tracking-wide">
          Commands ${filter ? `(/${filter})` : ""}
        </span>
        <span class="text-xs text-gray-500">
          ${filteredCommands.length}
          command${filteredCommands.length !== 1 ? "s" : ""}
        </span>
      </div>
      <div
        ref=${listRef}
        class="slash-picker-list overflow-y-auto"
        style="max-height: ${maxVisibleItems * itemHeight}px;"
      >
        ${filteredCommands.map(
          (cmd, index) => html`
            <div
              key=${cmd.name}
              data-index=${index}
              class="slash-picker-item flex items-center gap-3 px-3 py-2.5 cursor-pointer transition-colors ${index ===
              selectedIndex
                ? "bg-blue-600/40"
                : "hover:bg-slate-600/50"}"
              onClick=${() => handleSelect(cmd)}
              onMouseEnter=${() => handleMouseEnter(index)}
            >
              <span
                class="slash-command-name font-mono text-sm text-blue-300 flex-shrink-0"
              >
                /${cmd.name}
              </span>
              <span
                class="slash-command-desc text-sm text-gray-400 truncate flex-1"
              >
                ${cmd.description || ""}
              </span>
            </div>
          `,
        )}
      </div>
    </div>
  `;
}
