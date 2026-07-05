// Mitto Web Interface — ShortcutsEditor component
// A reusable per-section shortcut-button editor shared by the folder-level
// (Workspaces dialog) and global-level (Settings dialog) shortcut panels.

const { html } = window.preact;

import { IconPicker } from "./IconPicker.js";
import { SpinnerIcon, TrashIcon } from "./Icons.js";

/**
 * ShortcutsEditor renders one fieldset per section, each with an ordered list of
 * editable shortcut rows (icon picker + prompt select + move/remove) and an
 * "+ Add shortcut" button.
 *
 * Props:
 *   sections          {Array}   - [{ id, label, desc }] section descriptors.
 *   shortcutsSections {Object}  - { [sectionId]: [{ icon, prompt }] } current rows.
 *   sectionPrompts    {Object}  - { [sectionId]: [promptObj] } available prompts.
 *   loading           {boolean} - Show a spinner instead of the sections.
 *   error             {string}  - Error message shown below the sections.
 *   maxPerSection     {number}  - Cap on rows per section (default 10).
 *   onAdd             {function}(sectionId)
 *   onUpdate          {function}(sectionId, idx, patch)
 *   onRemove          {function}(sectionId, idx)
 *   onMove            {function}(sectionId, idx, dir)  // dir = -1 up, +1 down
 *   redundantPromptNames {Object} - { [sectionId]: Set<string> } prompt names
 *       configured at a higher (global) level. Rows referencing them render
 *       greyed-out, and those prompts are omitted from the add dropdown so they
 *       are not configured twice.
 */
export function ShortcutsEditor({
  sections = [],
  shortcutsSections = {},
  sectionPrompts = {},
  loading = false,
  error = "",
  maxPerSection = 10,
  onAdd,
  onUpdate,
  onRemove,
  onMove,
  redundantPromptNames = {},
}) {
  if (loading) {
    return html`<div class="flex items-center justify-center p-4">
      <${SpinnerIcon} className="w-5 h-5 animate-spin" />
    </div>`;
  }

  return html`
    <div class="space-y-4">
      ${sections.map(({ id: section, label, desc }) => {
        const rows = shortcutsSections[section] || [];
        const prompts = sectionPrompts[section] || [];
        const redundant = redundantPromptNames[section] || new Set();
        return html`
          <fieldset key=${section} class="fieldset pt-2">
            <legend class="fieldset-legend">${label}</legend>
            <p class="text-sm text-mitto-text-muted mb-3">${desc}</p>

            <div class="space-y-2" data-testid="shortcut-rows-${section}">
              ${rows.map((row, idx) => {
                const linkedPrompt = prompts.find((p) => p.name === row.prompt);
                // A row whose prompt is also configured globally is redundant at
                // this (folder) level: grey it out to signal the global entry wins.
                const isRedundant = redundant.has(row.prompt);
                // Build the prompt options: exclude prompts configured at the
                // higher level, but always keep this row's current value so it
                // still renders (even if now redundant).
                const options = prompts.filter(
                  (p) => !redundant.has(p.name) || p.name === row.prompt,
                );
                return html`
                  <div
                    key=${idx}
                    class="join w-full ${isRedundant ? "opacity-50" : ""}"
                    data-testid="shortcut-row-${section}-${idx}"
                    title=${isRedundant
                      ? "Also configured globally — the global shortcut is used"
                      : ""}
                  >
                    <${IconPicker}
                      value=${row.icon}
                      defaultIconName=${linkedPrompt?.icon || ""}
                      className="join-item border-mitto-border"
                      onChange=${(name) =>
                        onUpdate(section, idx, { icon: name })}
                    />
                    <select
                      class="select select-sm join-item flex-1"
                      value=${row.prompt}
                      onChange=${(e) =>
                        onUpdate(section, idx, { prompt: e.target.value })}
                    >
                      <option value="">Select a prompt…</option>
                      ${options.map(
                        (p) => html`
                          <option key=${p.name} value=${p.name}>
                            ${p.name}
                          </option>
                        `,
                      )}
                    </select>
                    <button
                      type="button"
                      class="btn btn-ghost btn-square btn-sm join-item"
                      disabled=${idx === 0}
                      onClick=${() => onMove(section, idx, -1)}
                      aria-label="Move up"
                      title="Move up"
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      class="btn btn-ghost btn-square btn-sm join-item"
                      disabled=${idx === rows.length - 1}
                      onClick=${() => onMove(section, idx, 1)}
                      aria-label="Move down"
                      title="Move down"
                    >
                      ↓
                    </button>
                    <button
                      type="button"
                      class="btn btn-ghost btn-square btn-sm join-item text-mitto-danger"
                      onClick=${() => onRemove(section, idx)}
                      aria-label="Remove"
                      title="Remove"
                    >
                      <${TrashIcon} className="w-4 h-4" />
                    </button>
                  </div>
                `;
              })}
            </div>

            <div class="mt-3 flex items-center gap-2">
              <button
                type="button"
                class="btn btn-sm btn-ghost"
                disabled=${rows.length >= maxPerSection}
                onClick=${() => onAdd(section)}
                data-testid="shortcut-add-${section}"
              >
                + Add shortcut
              </button>
              ${rows.length >= maxPerSection &&
              html`<span class="text-xs text-mitto-text-muted"
                >Maximum ${maxPerSection}</span
              >`}
            </div>
          </fieldset>
        `;
      })}
      ${error && html`<p class="text-sm text-mitto-danger">${error}</p>`}
    </div>
  `;
}

export default ShortcutsEditor;
