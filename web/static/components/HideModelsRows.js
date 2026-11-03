// Mitto Web Interface - Hide Models rows
// Shared "Hide Models (optional)" editor used by the agent editor
// (SettingsDialog) and the workspace Models tab (WorkspaceEditor).
const { useState, useRef, html } = window.preact;

import { hiddenModelsToRows } from "../utils/hiddenModels.js";

/**
 * Hide Models rule rows: one regex per row. Draft rows only; serialization
 * and validation are authoritative on the backend (no JS regex engine).
 */
export function HideModelsRows({ ownerName, hiddenModels, error, onChange }) {
  const nextId = useRef(0);
  const addRef = useRef(null);
  const makeRows = (list) =>
    hiddenModelsToRows(list).map((value) => ({ id: nextId.current++, value }));
  const [rows, setRows] = useState(() => makeRows(hiddenModels));
  const update = (next) => {
    setRows(next);
    onChange(next.map((r) => r.value));
  };
  const errId = (id) => `hide-models-error-${ownerName}-${id}`;
  return html`
    <div class="mt-3" data-testid="hide-models">
      <label class="label">Hide Models (optional)</label>
      <p class="text-xs text-mitto-text-muted mb-2">
        Each row is one regular expression (Go/RE2). A model is hidden when any
        rule matches its model ID or display name, case-insensitively. Commas
        and regex syntax are literal parts of the rule, not separators. For
        example, <code>.*Terra.*</code> matches Terra in the model ID or display
        name.
      </p>
      ${rows.map((row, i) => {
        const hasErr = error && error.rowIndex === i;
        return html`
          <div key=${row.id} class="mb-2">
            <div class="flex items-center gap-2">
              <input
                type="text"
                class="input input-bordered input-sm flex-1"
                value=${row.value}
                aria-label=${`Hide models rule ${i + 1} for ${ownerName}`}
                aria-invalid=${hasErr ? "true" : "false"}
                aria-describedby=${hasErr ? errId(row.id) : undefined}
                onInput=${(e) =>
                  update(
                    rows.map((r) =>
                      r.id === row.id ? { ...r, value: e.target.value } : r,
                    ),
                  )}
              />
              <button
                type="button"
                class="btn btn-ghost btn-sm"
                aria-label=${`Remove hide models rule ${i + 1}`}
                onClick=${() => {
                  update(rows.filter((r) => r.id !== row.id));
                  if (addRef.current) addRef.current.focus();
                }}
              >
                Remove
              </button>
            </div>
            ${hasErr &&
            html`<p
              id=${errId(row.id)}
              role="alert"
              class="text-xs text-error mt-1"
            >
              ${error.message}
            </p>`}
          </div>
        `;
      })}
      <button
        type="button"
        ref=${addRef}
        class="btn btn-outline btn-sm"
        aria-label=${`Add hide models rule for ${ownerName}`}
        onClick=${() => update([...rows, { id: nextId.current++, value: "" }])}
      >
        Add rule
      </button>
    </div>
  `;
}
