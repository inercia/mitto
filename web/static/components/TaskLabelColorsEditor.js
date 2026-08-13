// Ordered global task-label background-color editor.

const { html } = window.preact;

import { SpinnerIcon, TrashIcon } from "./Icons.js";

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
              <input
                type="color"
                value=${/^#[0-9a-fA-F]{6}$/.test(entry.color)
                  ? entry.color
                  : "#ef4444"}
                onInput=${(e) => onUpdate(idx, { color: e.target.value })}
                aria-label="Choose task title background color"
                class="join-item w-6 h-6 rounded border-0 cursor-pointer p-0"
              />
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
