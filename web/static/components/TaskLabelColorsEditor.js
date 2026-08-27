// Ordered global task-label background-color editor.

const { html } = window.preact;

import { SpinnerIcon, TrashIcon } from "./Icons.js";

// Preset swatches for the anchored color-picker popover (mitto-19m). A native
// color-type input hands popover positioning entirely to the OS/browser
// (macOS NSColorPanel via WKWebView), which detaches it from the swatch inside
// the constrained Settings modal — this fixed palette keeps the picker fully
// Mitto-rendered and anchored via a daisyUI CSS dropdown (IconPicker.js
// pattern) instead.
const TASK_LABEL_COLOR_PRESETS = [
  "#ef4444", // red
  "#f97316", // orange
  "#f59e0b", // amber
  "#eab308", // yellow
  "#84cc16", // lime
  "#22c55e", // green
  "#14b8a6", // teal
  "#06b6d4", // cyan
  "#3b82f6", // blue
  "#6366f1", // indigo
  "#a855f7", // purple
  "#ec4899", // pink
];

export function TaskLabelColorsEditor({
  entries = [],
  loading = false,
  error = "",
  onAdd,
  onUpdate,
  onRemove,
  onMove,
}) {
  if (loading) {
    return html`<div class="flex items-center justify-center p-4">
      <${SpinnerIcon} className="w-5 h-5 animate-spin" />
    </div>`;
  }

  return html`
    <fieldset class="fieldset pt-2">
      <legend class="fieldset-legend">Task title colors</legend>
      <p class="text-sm text-mitto-text-muted mb-3">
        The first configured entry matching a task label sets that task's title
        background.
      </p>
      <div class="space-y-2" data-testid="task-label-color-rows">
        ${entries.map(
          (entry, idx) => html`
            <div
              key=${idx}
              class="join w-full"
              data-testid="task-label-color-row-${idx}"
            >
              <input
                type="text"
                value=${entry.label}
                onInput=${(e) => onUpdate(idx, { label: e.target.value })}
                placeholder="needs-human"
                aria-label="Task label"
                class="input input-sm join-item flex-1"
              />
              <div class="dropdown join-item">
                <div
                  tabindex="0"
                  role="button"
                  data-testid="task-label-color-swatch-${idx}"
                  aria-label="Choose task title background color"
                  aria-haspopup="true"
                  class="w-6 h-6 rounded border border-mitto-border-2 cursor-pointer"
                  style="background-color: ${/^#[0-9a-fA-F]{6}$/.test(
                    entry.color,
                  )
                    ? entry.color
                    : "#ef4444"}"
                ></div>
                <div
                  tabindex="0"
                  class="dropdown-content z-50 p-2 w-48 bg-base-200 rounded-box shadow-xl"
                  role="listbox"
                  aria-label="Preset colors"
                >
                  <div class="flex flex-wrap gap-1">
                    ${TASK_LABEL_COLOR_PRESETS.map((hex) => {
                      const isSelected =
                        (entry.color || "").toLowerCase() === hex;
                      return html`
                        <button
                          key=${hex}
                          type="button"
                          role="option"
                          aria-selected=${isSelected}
                          aria-label=${hex}
                          title=${hex}
                          onClick=${(e) => {
                            onUpdate(idx, { color: hex });
                            e.currentTarget.blur();
                            if (document.activeElement)
                              document.activeElement.blur();
                          }}
                          class="w-6 h-6 rounded border ${isSelected
                            ? "ring-2 ring-mitto-accent"
                            : "border-mitto-border-2"}"
                          style="background-color: ${hex}"
                        ></button>
                      `;
                    })}
                  </div>
                </div>
              </div>
              <input
                type="text"
                value=${entry.color}
                onInput=${(e) => onUpdate(idx, { color: e.target.value })}
                placeholder="#ef4444"
                aria-label="Task title background hex color"
                class="input input-sm join-item flex-1 font-mono"
              />
              <button
                type="button"
                class="btn btn-ghost btn-square btn-sm join-item"
                disabled=${idx === 0}
                onClick=${() => onMove(idx, -1)}
                aria-label="Move up"
              >
                ↑
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-square btn-sm join-item"
                disabled=${idx === entries.length - 1}
                onClick=${() => onMove(idx, 1)}
                aria-label="Move down"
              >
                ↓
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-square btn-sm join-item text-mitto-danger"
                onClick=${() => onRemove(idx)}
                aria-label="Remove"
              >
                <${TrashIcon} className="w-4 h-4" />
              </button>
            </div>
          `,
        )}
      </div>
      <div class="mt-3">
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          onClick=${onAdd}
          data-testid="task-label-color-add"
        >
          + Add label color
        </button>
      </div>
      ${error && html`<p class="text-sm text-mitto-danger">${error}</p>`}
    </fieldset>
  `;
}

export default TaskLabelColorsEditor;
