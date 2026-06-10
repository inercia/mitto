// Mitto Web Interface - Shared Model Selection Component
const { html } = window.preact;

/**
 * ModelSelection — shared component for model constraint configuration.
 *
 * Renders a match-mode <select> and a pattern <input>.
 * Used in both the ACP server editor (SettingsDialog) and the workspace
 * editor (WorkspacesDialog) to configure which model to auto-select.
 *
 * Props:
 *   matchMode  {string}   — current match mode value ("", "contains", "exact", ...)
 *   pattern    {string}   — current pattern value
 *   onChange   {function} — called with (matchMode, pattern) on any change
 */
export function ModelSelection({ matchMode, pattern, onChange }) {
  const handleModeChange = (e) => {
    const newMode = e.target.value;
    onChange(newMode, pattern);
  };

  const handlePatternChange = (e) => {
    onChange(matchMode, e.target.value);
  };

  return html`
    <div class="flex gap-2 items-center">
      <select
        value=${matchMode}
        onInput=${handleModeChange}
        class="px-3 py-2 bg-mitto-surface-3 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        style="flex: 0 0 auto; min-width: 140px; max-width: 160px;"
      >
        <option value="">-- None --</option>
        <option value="contains">contains</option>
        <option value="exact">exact</option>
        <option value="startsWith">starts with</option>
        <option value="regex">regex</option>
        <option value="lookAlike">look alike</option>
      </select>
      <input
        type="text"
        value=${pattern}
        onInput=${handlePatternChange}
        placeholder="e.g., Opus 4.6"
        disabled=${!matchMode}
        class="flex-1 px-3 py-2 bg-mitto-surface-3 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${!matchMode ? "opacity-50 cursor-not-allowed" : ""}"
      />
    </div>
  `;
}
