// Mitto Web Interface - Prompt Parameter Dialog Component
// Collects values for prompt parameters that a menu cannot auto-fill.
// Renders type-specific controls (textarea, beads selector, session selector,
// plain text input) and calls onSubmit with the collected arguments map.

const { useState, useEffect, useCallback, html, Fragment } = window.preact;

import { apiUrl } from "../utils/api.js";
import { getSdkClient } from "../utils/sdkClient.js";
import { Modal } from "./Modal.js";
import { getBasename } from "../lib.js";
import { SlidersIcon } from "./Icons.js";
// mitto-47y.6.1: recursive nested-picker helpers live under utils/ so they can
// be unit-tested without hitting the `window.preact` module-load import gate.
// See utils/promptNestedArgs.js for the full contract; MAX_NESTED_LEVEL stays
// in sync with backend `promptTextMaxDepth` in internal/cel/templatefuncs.go.
import {
  MAX_NESTED_LEVEL,
  updateNestedTree,
  pruneNestedTree,
  buildInnerArgs,
  collectPickedPaths,
} from "../utils/promptNestedArgs.js";
// mitto-boio: pure grouping helpers live in utils/ so they are unit-tested
// without hitting the `window.preact` module-load import gate — see
// groupDialogParameters' doc comment for the tab gate/ordering contract.
import {
  groupDialogParameters,
  unmetRequiredByGroup,
} from "../utils/prompts.js";

/**
 * Render one parameter field based on its type.
 * @param {Object} param     - { name, type, description?, required?, multiLine?, options? }
 * @param {string} value     - current field value
 * @param {Function} onChange - (name, value) => void
 * @param {Array} beadsIssues - loaded beads issues (may be [])
 * @param {boolean} loadingBeads
 * @param {Array} sessions    - loaded sessions (may be [])
 * @param {boolean} loadingSessions
 * @param {Array} workspaces  - loaded workspaces (may be [])
 * @param {boolean} loadingWorkspaces
 * @param {string} workingDir - current workspace directory (for "(current)" label)
 * @param {Array} acpServers  - loaded ACP servers (may be [])
 * @param {string} hostSessionId - host conversation id (for childSessionId filtering)
 * @param {Array} promptsList - loaded workspace prompts (may be []); each entry has a `name`
 * @param {boolean} loadingPrompts
 * @param {Object} filesByParam - loaded file lists keyed by param.name (may be undefined)
 * @param {Object} loadingFilesByParam - per-param loading flag keyed by param.name
 * @param {Object} dirsByParam - loaded dir lists keyed by param.name (may be undefined)
 * @param {Object} loadingDirsByParam - per-param loading flag keyed by param.name
 * @param {Object} [nestedNode] - recursive nested-args tree slot for this field
 *   (mitto-47y.6.1). Shape: `{ values: { [innerName]: value }, sub: { [innerPicker]: node } }`.
 *   Undefined for non-picker fields or when this picker has no picked value yet.
 * @param {Function} [onNestedTreeChange] - `(pathFromRoot, innerName, val) => void`
 *   where `pathFromRoot` is the array of outer picker names from the root down
 *   to (and including) this field's parent picker. A leaf write at level L uses
 *   a path of length L. Single canonical callback for every depth — the root
 *   dialog updates the correct subtree slot in `nestedValues`.
 * @param {Object} [loadingRememberedByPath] - `{ [pathKey]: bool }` map keyed
 *   by the picker-name path (e.g. `"outer/inner"`) so per-level remembered-args
 *   spinners never collide when two pickers at different depths share a name.
 * @param {Array<string>} [ancestorPath] - picker-name chain from root to this
 *   field's parent picker (empty at level 0). Used both to build spinner-lookup
 *   keys and to compose the path passed to `onNestedTreeChange` for children.
 * @param {number} [level] - 0 at the outermost prompt, incremented per nested
 *   block. When `level === MAX_NESTED_LEVEL`, `type: prompts` fields render
 *   as the disabled "nested prompt pickers are not supported here" note.
 */

// mitto-l78: recursively summarize a nested-args tree node — how many leaf
// values are filled, and how many *required* leaves (at any depth) are still
// unset. Powers the compact sliders button's badge/danger-flag next to a
// `type: prompts` picker, since its inner parameters are no longer always
// visible on the main form. Deliberately duplicated (not added to
// utils/promptNestedArgs.js) — this bead is presentation-only and must not
// change that module's wire-format contract; the walk mirrors
// buildInnerArgs's structure (booleans always "answered", required-empty
// strings count as missing, `type: prompts` recurses into `sub`) but counts
// instead of building an args map. Naturally bounded by the same
// MAX_NESTED_LEVEL cap as buildInnerArgs: a picker at the cap renders as a
// disabled placeholder (see the `type === "prompts"` branch below) and can
// never acquire a picked value, so this function never recurses past what
// the UI can actually produce.
function summarizeNestedNode(innerParams, node, promptsList) {
  let filled = 0;
  let missingRequired = 0;
  const values = (node && node.values) || {};
  const sub = (node && node.sub) || {};
  const paramsList = Array.isArray(innerParams) ? innerParams : [];
  for (const ip of paramsList) {
    if (!ip || !ip.name) continue;
    if (ip.type === "prompts") {
      const pickedName = (values[ip.name] || "").toString().trim();
      if (pickedName === "") {
        if (ip.required) missingRequired += 1;
        continue;
      }
      filled += 1;
      // mitto-48c: collectInnerArgs: false discards this picker's inner
      // values entirely — count the picked name as filled but never recurse
      // into (or flag missing-required leaves from) its discarded subtree.
      if (ip.collectInnerArgs === false) continue;
      const pickedPrompt = (promptsList || []).find(
        (wp) => wp && wp.name === pickedName,
      );
      const deeperInner =
        pickedPrompt && Array.isArray(pickedPrompt.parameters)
          ? pickedPrompt.parameters
          : [];
      const deeper = summarizeNestedNode(
        deeperInner,
        sub[ip.name],
        promptsList,
      );
      filled += deeper.filled;
      missingRequired += deeper.missingRequired;
      continue;
    }
    if (ip.type === "boolean") {
      // A checkbox always has a definite answer (default unchecked); only
      // count it toward "filled" when explicitly checked so the badge
      // reflects meaningful state rather than every boolean by default.
      if (values[ip.name] === true || values[ip.name] === "true") filled += 1;
      continue;
    }
    const iv = (values[ip.name] || "").toString().trim();
    if (iv !== "") {
      filled += 1;
    } else if (ip.required) {
      missingRequired += 1;
    }
  }
  return { filled, missingRequired };
}

// mitto-l78: does ANY `type: prompts` picker in `parameters` (at any depth,
// via summarizeNestedNode's recursion) currently have an unmet required
// inner parameter? Required inner params are hidden behind the sliders
// sub-dialog once picked, so Save must not succeed while one is unset —
// without this the operator could silently submit an incomplete nested
// prompt invocation with no visual cue on the main form.
function hasUnmetNestedRequired(parameters, values, nestedValues, promptsList) {
  for (const p of parameters || []) {
    if (!p || p.type !== "prompts") continue;
    // mitto-48c: an opted-out picker's inner params are discarded, so an
    // unmet required inner param there must never block Save.
    if (p.collectInnerArgs === false) continue;
    const pickedName = (values[p.name] || "").toString().trim();
    if (!pickedName) continue;
    const pickedPrompt = (promptsList || []).find(
      (wp) => wp && wp.name === pickedName,
    );
    const innerParams =
      pickedPrompt && Array.isArray(pickedPrompt.parameters)
        ? pickedPrompt.parameters
        : [];
    const node = nestedValues && nestedValues[p.name];
    const summary = summarizeNestedNode(innerParams, node, promptsList);
    if (summary.missingRequired > 0) return true;
  }
  return false;
}

function ParamField({
  param,
  value,
  onChange,
  beadsIssues,
  loadingBeads,
  sessions,
  loadingSessions,
  workspaces,
  loadingWorkspaces,
  workingDir,
  acpServers,
  hostSessionId,
  promptsList,
  loadingPrompts,
  filesByParam,
  loadingFilesByParam,
  dirsByParam,
  loadingDirsByParam,
  nestedNode,
  onNestedTreeChange,
  loadingRememberedByPath,
  ancestorPath = [],
  level = 0,
}) {
  const { name, type, description, required, multiLine, options, readOnly } =
    param;
  const hasOptions = Array.isArray(options) && options.length > 0;

  // mitto-l78: nested sub-dialog open state for `type: prompts` pickers.
  // Declared unconditionally (Rules of Hooks) even though only the
  // `prompts` branch below ever toggles it — ParamField is always rendered
  // as a Preact component (via `<${ParamField} .../>`), never called as a
  // plain function, so this is safe regardless of `type`.
  const [nestedModalOpen, setNestedModalOpen] = useState(false);

  // mitto-l78: resolve the picked prompt + its declared parameters once, up
  // front, so both the sliders-button state computed in the `prompts`
  // control branch below and the nested-modal wiring at the end of this
  // function share the same values (previously this lookup lived only at
  // the bottom of this function, for the inline fieldset it replaces).
  const trimmedValue = typeof value === "string" ? value.trim() : "";
  const pickedPrompt =
    type === "prompts" && trimmedValue && Array.isArray(promptsList)
      ? promptsList.find((wp) => wp && wp.name === trimmedValue)
      : null;
  const innerParams =
    pickedPrompt && Array.isArray(pickedPrompt.parameters)
      ? pickedPrompt.parameters
      : null;
  // mitto-48c: a picker declaring `collectInnerArgs: false` never opens the
  // nested sub-dialog — its picked prompt's own parameters are discarded
  // (e.g. a picker used only as a name/edit-subject reference), so asking
  // for them would be wasted interaction.
  const collectInnerArgs = param.collectInnerArgs !== false;
  const canOpenNested =
    type === "prompts" &&
    collectInnerArgs &&
    level < MAX_NESTED_LEVEL &&
    innerParams &&
    innerParams.length > 0;

  // mitto-l78: if the picker's value changes (or its picked prompt loses its
  // parameters) while the sub-dialog is open, close it — otherwise a later
  // change back to a prompt with parameters would silently reopen a stale
  // modal without the user clicking the sliders button again.
  useEffect(() => {
    if (!canOpenNested) setNestedModalOpen(false);
  }, [canOpenNested]);

  // mitto-9rff: a menu-supplied (or otherwise pre-resolved) parameter renders
  // as a disabled display of its prefilled value instead of the normal
  // type-specific control — it shows the value the dialog opened with (from
  // `initialValues`) without letting the user override context another
  // surface already resolved, and it is excluded from the required-field
  // check in PromptParameterDialog's `canSave` below. `show: always`
  // (checked by the caller building the `parameters` array — see
  // promptDialogParameters) promotes the param to the normal editable path,
  // so this branch is never reached for it. Placed after all hooks above so
  // this early return never violates the Rules of Hooks.
  if (readOnly) {
    const displayValue =
      type === "boolean"
        ? value === true || value === "true"
          ? "Yes"
          : "No"
        : typeof value === "string" && value !== ""
          ? value
          : "—";
    return html`
      <fieldset class="fieldset">
        <legend class="fieldset-legend text-mitto-text-secondary">
          ${name}
        </legend>
        <input
          type="text"
          class="input input-sm w-full opacity-70"
          value=${displayValue}
          disabled
        />
        ${description &&
        html`<p class="text-xs text-mitto-text-muted mt-1">${description}</p>`}
      </fieldset>
    `;
  }

  let control;
  if (type === "beadsId") {
    if (loadingBeads) {
      control = html`<span class="text-mitto-text-muted text-xs opacity-60"
        >…</span
      >`;
    } else if (beadsIssues.length === 0) {
      // Fallback to text input when list is unavailable
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Issue ID (e.g. mitto-42)"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select an issue…</option>
          ${beadsIssues.map(
            (issue) =>
              html`<option key=${issue.id} value=${issue.id}>
                ${issue.title} (${issue.id})
              </option>`,
          )}
        </select>
      `;
    }
  } else if (type === "sessionId") {
    if (loadingSessions) {
      control = html`<span class="text-mitto-text-muted text-xs opacity-60"
        >…</span
      >`;
    } else if (sessions.length === 0) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Conversation ID"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select a conversation…</option>
          ${sessions.map(
            (s) =>
              html`<option key=${s.session_id} value=${s.session_id}>
                ${s.name || s.description || s.session_id}
              </option>`,
          )}
        </select>
      `;
    }
  } else if (type === "childSessionId") {
    const childSessions = (sessions || []).filter(
      (s) => hostSessionId && s.parent_session_id === hostSessionId,
    );
    if (loadingSessions) {
      control = html`<span class="text-mitto-text-muted text-xs opacity-60"
        >…</span
      >`;
    } else if (childSessions.length === 0) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Child conversation ID"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select a child conversation…</option>
          ${childSessions.map(
            (s) =>
              html`<option key=${s.session_id} value=${s.session_id}>
                ${s.name || s.description || s.session_id}
              </option>`,
          )}
        </select>
      `;
    }
  } else if (type === "workspaceId") {
    if (loadingWorkspaces) {
      control = html`<span class="text-mitto-text-muted text-xs opacity-60"
        >…</span
      >`;
    } else if (workspaces.length === 0) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Workspace ID"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select a workspace…</option>
          ${workspaces.map(
            (ws) =>
              html`<option key=${ws.uuid} value=${ws.uuid}>
                ${ws.name || ws.working_dir}${ws.working_dir === workingDir
                  ? " (current)"
                  : ""}
              </option>`,
          )}
        </select>
      `;
    }
  } else if (type === "workspaceFolder") {
    const seen = new Set();
    const folders = (workspaces || []).filter((ws) => {
      if (!ws.working_dir || seen.has(ws.working_dir)) return false;
      seen.add(ws.working_dir);
      return true;
    });
    if (loadingWorkspaces) {
      control = html`<span class="text-mitto-text-muted text-xs opacity-60"
        >…</span
      >`;
    } else if (folders.length === 0) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Absolute folder path"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select a folder…</option>
          ${folders.map(
            (ws) =>
              html`<option
                key=${ws.working_dir}
                value=${ws.working_dir}
                title=${ws.working_dir}
              >
                ${ws.name || getBasename(ws.working_dir)}${ws.working_dir ===
                workingDir
                  ? " (current)"
                  : ""}
              </option>`,
          )}
        </select>
      `;
    }
  } else if (type === "acpServer") {
    if (loadingWorkspaces) {
      control = html`<span class="text-mitto-text-muted text-xs opacity-60"
        >…</span
      >`;
    } else if (!acpServers || acpServers.length === 0) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Agent (ACP server) name"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select an agent…</option>
          ${acpServers.map(
            (s) =>
              html`<option key=${s.name} value=${s.name}>${s.name}</option>`,
          )}
        </select>
      `;
    }
  } else if (type === "boolean") {
    // Checkbox: a definite yes/no. value is a JS boolean (true) or "" (unchecked).
    // The collected value is emitted as the string "true"/"false" in handleSubmit.
    control = html`
      <input
        type="checkbox"
        class="checkbox checkbox-sm"
        checked=${value === true || value === "true"}
        onChange=${(e) => onChange(name, e.target.checked)}
      />
    `;
  } else if (type === "prompts") {
    // Value is the NAME of another workspace prompt. Mirrors the beadsId
    // pattern: spinner while loading, text-input fallback when the list is
    // unavailable, otherwise a select of prompt names.
    // mitto-l78: the picked prompt's own parameters are no longer expanded
    // inline — a compact sliders button (daisyUI `join`, next to the select)
    // opens them in a nested Modal at `level + 1` instead. The button badge
    // shows the count of currently-filled inner values (or, if any required
    // inner param is unset at any depth, a danger-colored count of those
    // instead — see summarizeNestedNode above and hasUnmetNestedRequired,
    // which also gates the outer Save button).
    // mitto-47y.6.1: at level === MAX_NESTED_LEVEL a picker would drive a
    // backend sub-render past `promptTextMaxDepth` — render the pre-existing
    // disabled placeholder instead (defense-in-depth alongside the submit
    // serializer skip at the same depth). This preserves the v1 (`mitto-47y.2`)
    // Phase B note at the new deepest level; the button stays disabled here
    // too, carrying the same "not supported" text as a tooltip.
    const capped = level >= MAX_NESTED_LEVEL;
    const hasInnerParams = !!(innerParams && innerParams.length > 0);
    // mitto-48c: collectInnerArgs: false discards the picked prompt's own
    // parameters entirely — never summarize/warn/open for it.
    const nestedSummary =
      hasInnerParams && collectInnerArgs
        ? summarizeNestedNode(innerParams, nestedNode, promptsList)
        : { filled: 0, missingRequired: 0 };
    const sliderDisabled = capped || !collectInnerArgs || !hasInnerParams;
    const sliderHasWarning =
      !sliderDisabled && nestedSummary.missingRequired > 0;
    const sliderTip = capped
      ? "nested prompt pickers are not supported here"
      : !collectInnerArgs
        ? "This prompt's parameters are not used here"
        : !trimmedValue
          ? "Pick a prompt to configure its parameters"
          : !hasInnerParams
            ? "This prompt has no parameters"
            : sliderHasWarning
              ? `${nestedSummary.missingRequired} required parameter${nestedSummary.missingRequired === 1 ? "" : "s"} unset`
              : "Prompt parameters";
    const sliderBadgeCount = sliderHasWarning
      ? nestedSummary.missingRequired
      : nestedSummary.filled;
    const sliderButton = html`
      <button
        type="button"
        class="btn btn-sm btn-square join-item tooltip tooltip-left ${sliderHasWarning
          ? "btn-error"
          : ""}"
        data-tip=${sliderTip}
        disabled=${sliderDisabled}
        onClick=${() => setNestedModalOpen(true)}
        data-testid="nested-params-btn-${name}"
      >
        <span class="indicator">
          <${SlidersIcon} className="w-4 h-4" />
          ${!sliderDisabled &&
          sliderBadgeCount > 0 &&
          html`<span
            class="indicator-item badge badge-sm ${sliderHasWarning
              ? "badge-error"
              : "badge-primary"}"
            data-testid="nested-params-badge-${name}"
          >
            ${sliderBadgeCount}
          </span>`}
        </span>
      </button>
    `;
    if (capped) {
      control = html`
        <div class="join w-full">
          <input
            type="text"
            class="input input-sm join-item flex-1"
            value=""
            disabled
            placeholder="nested prompt pickers are not supported here"
          />
          ${sliderButton}
        </div>
      `;
    } else if (loadingPrompts) {
      control = html`
        <div class="join w-full">
          <span
            class="text-mitto-text-muted text-xs opacity-60 join-item flex-1 flex items-center px-2"
            >…</span
          >
          ${sliderButton}
        </div>
      `;
    } else if (!promptsList || promptsList.length === 0) {
      control = html`
        <div class="join w-full">
          <input
            type="text"
            class="input input-sm join-item flex-1"
            value=${value}
            onInput=${(e) => onChange(name, e.target.value)}
            placeholder="Prompt name"
          />
          ${sliderButton}
        </div>
      `;
    } else {
      const sortedPrompts = [...promptsList].sort((a, b) =>
        String(a?.name ?? "").localeCompare(String(b?.name ?? "")),
      );
      control = html`
        <div class="join w-full">
          <select
            class="select select-sm join-item flex-1"
            value=${value}
            onChange=${(e) => onChange(name, e.target.value)}
          >
            <option value="">Select a prompt…</option>
            ${sortedPrompts.map(
              (p) =>
                html`<option key=${p.name} value=${p.name}>${p.name}</option>`,
            )}
          </select>
          ${sliderButton}
        </div>
      `;
    }
  } else if (type === "filename") {
    // Value is a workspace-relative file path (e.g. "docs/a.md"). Mirrors the
    // beadsId/prompts pattern: spinner while loading, text-input fallback
    // when the list is empty or the fetch failed, otherwise a select of paths.
    // Per-param state keyed by param.name — different filename params may
    // have different dir/glob and therefore different candidate lists.
    const files = (filesByParam && filesByParam[name]) || [];
    const loadingFiles = !!(loadingFilesByParam && loadingFilesByParam[name]);
    if (loadingFiles) {
      control = html`<span class="text-mitto-text-muted text-xs opacity-60"
        >…</span
      >`;
    } else if (files.length === 0) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Workspace-relative file path"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select a file…</option>
          ${files.map(
            (path) => html`<option key=${path} value=${path}>${path}</option>`,
          )}
        </select>
      `;
    }
  } else if (type === "dirname") {
    // Value is a workspace-relative directory path (e.g. "docs/instructions").
    // Mirrors the filename branch: spinner while loading, text-input fallback
    // when the list is empty or the fetch failed, otherwise a select of paths.
    // Per-param state keyed by param.name — different dirname params may have
    // different dir/glob and therefore different candidate lists.
    const dirs = (dirsByParam && dirsByParam[name]) || [];
    const loadingDirs = !!(loadingDirsByParam && loadingDirsByParam[name]);
    if (loadingDirs) {
      control = html`<span class="text-mitto-text-muted text-xs opacity-60"
        >…</span
      >`;
    } else if (dirs.length === 0) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Workspace-relative directory path"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select a directory…</option>
          ${dirs.map(
            (path) => html`<option key=${path} value=${path}>${path}</option>`,
          )}
        </select>
      `;
    }
  } else if (type === "text") {
    // options (dropdown) wins over multiLine when both are set — this mirrors
    // the backend validation which rejects that combination, so this branch
    // order is defensive rather than user-visible.
    if (hasOptions) {
      const current = value || param.default || "";
      control = html`
        <select
          class="select select-sm select-bordered w-full"
          value=${current}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          ${!required && html`<option value="">${"— select —"}</option>`}
          ${options.map((opt) => html`<option value=${opt}>${opt}</option>`)}
        </select>
      `;
    } else {
      // Default: single-line input. multiLine renders a resizable textarea for
      // naturally multi-line values (e.g. instructions).
      control = multiLine
        ? html`
            <textarea
              class="textarea textarea-sm w-full resize-y"
              rows="3"
              value=${value}
              onInput=${(e) => onChange(name, e.target.value)}
            ></textarea>
          `
        : html`
            <input
              type="text"
              class="input input-sm w-full"
              value=${value}
              onInput=${(e) => onChange(name, e.target.value)}
            />
          `;
    }
  } else {
    // beadsTitle, unknown → plain text input
    control = html`
      <input
        type="text"
        class="input input-sm w-full"
        value=${value}
        onInput=${(e) => onChange(name, e.target.value)}
      />
    `;
  }

  if (type === "boolean") {
    // Coherent checkbox layout: checkbox + name on one row (clickable label),
    // with the description below aligned under the name.
    return html`
      <fieldset class="fieldset">
        <label class="flex items-center gap-2 cursor-pointer">
          ${control}
          <span class="fieldset-legend text-mitto-text-secondary p-0">
            ${name}
          </span>
        </label>
        ${description &&
        html`<p class="text-xs text-mitto-text-muted mt-1 ml-6">
          ${description}
        </p>`}
      </fieldset>
    `;
  }

  // mitto-l78: recursive nested-block render. A `type: prompts` picker that
  // is below the depth cap AND has a non-empty picked value can open the
  // picked prompt's parameters — rendered via ParamField at `level + 1` — in
  // a nested Modal (opened by the sliders button built above) instead of the
  // old inline indented fieldset. `pickedPrompt`/`innerParams` are computed
  // once at the top of this function so the button and this modal agree.
  // Path from root down to and INCLUDING this picker — used both to build
  // spinner-lookup keys and to compose the path children pass back when they
  // write via onNestedTreeChange. Empty at level 0's non-pickers.
  const pathIncludingSelf = [...ancestorPath, name];
  const pathKey = pathIncludingSelf.join("/");
  const nestedValues = (nestedNode && nestedNode.values) || null;
  const nestedSub = (nestedNode && nestedNode.sub) || null;
  const loadingNestedRemembered = !!(
    loadingRememberedByPath && loadingRememberedByPath[pathKey]
  );
  // Testid: preserve the pre-existing `nested-params-<name>` shape at level 0
  // for regression compatibility; deeper blocks include the level for tests
  // that need to target a specific depth.
  const nestedTestid =
    level === 0
      ? `nested-params-${name}`
      : `nested-params-L${level + 1}-${name}`;

  return html`
    <${Fragment}>
      <fieldset class="fieldset">
        <legend class="fieldset-legend text-mitto-text-secondary">
          ${name}
          ${required && html`<span class="text-mitto-danger ml-0.5">*</span>`}
        </legend>
        ${control}
        ${
          description &&
          html`<p class="text-xs text-mitto-text-muted mt-1">${description}</p>`
        }
      </fieldset>
      ${
        canOpenNested &&
        nestedModalOpen &&
        html`
        <${Modal}
          isOpen=${nestedModalOpen}
          onClose=${() => setNestedModalOpen(false)}
          title=${`Parameters for ${(pickedPrompt && pickedPrompt.name) || value}`}
          testid=${nestedTestid}
          closeTestid="nested-params-close-${name}"
          backdropTestid="nested-params-backdrop-${name}"
          footer=${html`
            <button
              onClick=${() => setNestedModalOpen(false)}
              class="btn btn-sm btn-primary"
              data-testid="nested-params-done-${name}"
            >
              Done
            </button>
          `}
        >
          <div class="space-y-4">
            ${
              loadingNestedRemembered &&
              html`<span class="text-mitto-text-muted text-xs opacity-60"
                >…</span
              >`
            }
            ${innerParams.map(
              (inner) =>
                html`<${ParamField}
                  key=${inner.name}
                  param=${inner}
                  value=${(nestedValues && nestedValues[inner.name]) || ""}
                  onChange=${(innerName, val) =>
                    onNestedTreeChange &&
                    onNestedTreeChange(pathIncludingSelf, innerName, val)}
                  beadsIssues=${beadsIssues}
                  loadingBeads=${loadingBeads}
                  sessions=${sessions}
                  loadingSessions=${loadingSessions}
                  workspaces=${workspaces}
                  loadingWorkspaces=${loadingWorkspaces}
                  workingDir=${workingDir}
                  acpServers=${acpServers}
                  hostSessionId=${hostSessionId}
                  promptsList=${promptsList}
                  loadingPrompts=${loadingPrompts}
                  filesByParam=${filesByParam}
                  loadingFilesByParam=${loadingFilesByParam}
                  dirsByParam=${dirsByParam}
                  loadingDirsByParam=${loadingDirsByParam}
                  nestedNode=${nestedSub && nestedSub[inner.name]}
                  onNestedTreeChange=${onNestedTreeChange}
                  loadingRememberedByPath=${loadingRememberedByPath}
                  ancestorPath=${pathIncludingSelf}
                  level=${level + 1}
                />`,
            )}
          </div>
        </${Modal}>
      `
      }
    </${Fragment}>
  `;
}

/**
 * PromptParameterDialog — collects values for prompt parameters that a menu
 * could NOT auto-fill, then returns them as an arguments map via onSubmit.
 *
 * @param {boolean}  isOpen         - controls visibility
 * @param {Function} onClose        - called on dismiss (no onSubmit)
 * @param {Function} onSubmit       - called with { [paramName]: string } on Save
 * @param {Array}    parameters     - params: [{ name, type, description?, required?, multiLine?, options? }]
 * @param {string}   workingDir     - workspace directory (needed for beadsId selector)
 * @param {string}   [title]        - dialog title; defaults to "Prompt parameters"
 * @param {Object}   [initialValues] - pre-seeded values keyed by parameter name
 */
export function PromptParameterDialog({
  isOpen,
  onClose,
  onSubmit,
  parameters = [],
  workingDir,
  hostSessionId,
  title = "Prompt parameters",
  initialValues = {},
}) {
  const [values, setValues] = useState({});
  const [beadsIssues, setBeadsIssues] = useState([]);
  const [loadingBeads, setLoadingBeads] = useState(false);
  const [sessions, setSessions] = useState([]);
  const [loadingSessions, setLoadingSessions] = useState(false);
  const [workspaces, setWorkspaces] = useState([]);
  const [loadingWorkspaces, setLoadingWorkspaces] = useState(false);
  const [acpServers, setAcpServers] = useState([]);
  const [promptsList, setPromptsList] = useState([]);
  const [loadingPrompts, setLoadingPrompts] = useState(false);
  // Filename params carry per-param dir/glob so each one has its own candidate
  // list, keyed by param.name. loadingFilesByParam tracks the in-flight fetch
  // per param so multiple filename params render independent spinners.
  const [filesByParam, setFilesByParam] = useState({});
  const [loadingFilesByParam, setLoadingFilesByParam] = useState({});
  // Dirname params mirror the filename per-param state — see the filename
  // pattern above. Kept as a separate map so the two picker types can coexist
  // in the same dialog without key collisions.
  const [dirsByParam, setDirsByParam] = useState({});
  const [loadingDirsByParam, setLoadingDirsByParam] = useState({});
  // mitto-47y.6.1: nested-param state for `type: prompts` pickers, restructured
  // from the v1 flat `{ [pickerName]: { [innerName]: value } }` map into a
  // recursive tree matching the on-the-wire nesting shape. Each node:
  //   { values: { [innerName]: value }, sub: { [innerPickerName]: node } }
  // The root `nestedValues` is `{ [outerPickerName]: node }` — the "sub" of an
  // implicit level-(-1) root. `updateNestedTree` walks the path and creates
  // missing intermediate nodes on write. Depth is capped by MAX_NESTED_LEVEL
  // at the render layer, so the tree is never deeper than that.
  //   loadingRememberedByPath: `{ [path]: bool }` keyed by the picker-name path
  // (e.g. `"outerPicker/innerPicker"`) so per-level remembered-args spinners
  // never collide when two pickers at different depths share a name.
  const [nestedValues, setNestedValues] = useState({});
  const [loadingRememberedByPath, setLoadingRememberedByPath] = useState({});
  // mitto-boio: which tab is active when the dialog renders a tab bar (see
  // groupDialogParameters). Unused (and irrelevant) when !tabbed.
  const [activeTab, setActiveTab] = useState("");

  // Reset state each time the dialog opens; seed from initialValues when provided.
  // Seeds on the open transition only — initialValues is intentionally NOT a
  // dependency: callers may pass a fresh object literal each render (e.g. `|| {}`),
  // which would otherwise re-run this effect on every parent render and wipe
  // user-typed values.
  useEffect(() => {
    if (!isOpen) return;
    // mitto-cwz.1: seed declared `default` values for text+options picker
    // params so the select's preselected option (see ParamField's
    // `value || param.default` display fallback) and `values`/`canSave` agree
    // from the first render — otherwise a required options param with a
    // declared default blocks Save until the user re-picks the value that is
    // already shown. Precedence: declared default < initialValues (merged
    // next) < remembered-args (merged by the effect below via `setValues(prev
    // => ...)`, which runs after and still wins per its documented spec).
    // mitto-9rff: seed declared `default` for every param type, not just
    // options-pickers — the render set now includes optional params whose
    // declared default must be visible (and agree with `values`/`canSave`)
    // from the first render, since they are no longer conditionally hidden.
    const optionDefaults = {};
    for (const p of parameters) {
      if (p.default !== undefined && p.default !== null && p.default !== "") {
        optionDefaults[p.name] = p.default;
      }
    }
    setValues({
      ...optionDefaults,
      ...(initialValues ? { ...initialValues } : {}),
    });
    setBeadsIssues([]);
    setSessions([]);
    setWorkspaces([]);
    setAcpServers([]);
    setPromptsList([]);
    setFilesByParam({});
    setLoadingFilesByParam({});
    setDirsByParam({});
    setLoadingDirsByParam({});
    setNestedValues({});
    setLoadingRememberedByPath({});
    setLoadingBeads(false);
    setLoadingSessions(false);
    setLoadingWorkspaces(false);
    setLoadingPrompts(false);
  }, [isOpen]);

  // mitto-boio: recompute tab groups from the (possibly readOnly-annotated)
  // rendered parameters. Recomputed every render — cheap for a parameter
  // dialog's small list, and keeps it correct if `parameters` prop changes
  // while the dialog stays open (e.g. a menu re-resolves readOnly flags).
  const { tabbed, groups } = groupDialogParameters(parameters);

  // Reset the active tab to the first group whenever the dialog (re)opens or
  // the set of group names changes (e.g. a different prompt's dialog reuses
  // this component instance). Keyed on the joined group-name list rather than
  // `groups` itself, since `groups` is a fresh array every render.
  const groupNamesKey = groups.map((g) => g.name).join("\u0000");
  useEffect(() => {
    if (!isOpen) return;
    setActiveTab(groups.length > 0 ? groups[0].name : "");
  }, [isOpen, groupNamesKey]);

  const unmetGroups = unmetRequiredByGroup(groups, values);

  // mitto-x8v: when the dialog opens for a prompt that declares any
  // `remember: folder` parameter, fetch previously-remembered values for the
  // current workspace and merge them into `values`. Remembered entries
  // override any seeded initialValues (per spec). Fail-open: any fetch
  // failure leaves values untouched.
  useEffect(() => {
    if (!isOpen || !workingDir) return;
    const promptName = title;
    if (!promptName || promptName === "Prompt parameters") return;
    // mitto-47y.6.2: gate also honors `remember: conversation` so the fetch
    // fires whenever any scoped-persistence mode is declared.
    const needsRemember = parameters.some(
      (p) => p.remember === "folder" || p.remember === "conversation",
    );
    if (!needsRemember) return;
    let cancelled = false;
    getSdkClient()
      .prompts.rememberedArgs({
        working_dir: workingDir,
        prompt: promptName,
        session_id: hostSessionId,
      })
      .then((data) => {
        if (cancelled) return;
        const remembered =
          data && data.arguments && typeof data.arguments === "object"
            ? data.arguments
            : null;
        if (!remembered) return;
        setValues((prev) => ({ ...prev, ...remembered }));
      })
      .catch((err) => {
        console.warn("[PromptParameterDialog] remembered-args error:", err);
      });
    return () => {
      cancelled = true;
    };
  }, [isOpen, workingDir, title, hostSessionId]);

  // Fetch beads issues when dialog opens (only if a beadsId param is present)
  useEffect(() => {
    if (!isOpen) return;
    const needsBeads = parameters.some((p) => p.type === "beadsId");
    if (!needsBeads || !workingDir) return;

    setLoadingBeads(true);
    getSdkClient()
      .issues.list({ working_dir: workingDir })
      .then((data) => {
        setBeadsIssues(Array.isArray(data) ? data : []);
      })
      .catch((err) => {
        console.warn("[PromptParameterDialog] beads list error:", err);
        setBeadsIssues([]);
      })
      .finally(() => setLoadingBeads(false));
  }, [isOpen, workingDir]);

  // Fetch sessions when dialog opens (only if a sessionId param is present)
  useEffect(() => {
    if (!isOpen) return;
    const needsSessions = parameters.some(
      (p) => p.type === "sessionId" || p.type === "childSessionId",
    );
    if (!needsSessions) return;

    setLoadingSessions(true);
    getSdkClient()
      .sessions.list()
      .then((data) => {
        const list = Array.isArray(data) ? data : (data?.sessions ?? []);
        setSessions(list.filter((s) => !s.archived));
      })
      .catch((err) => {
        console.warn("[PromptParameterDialog] sessions list error:", err);
        setSessions([]);
      })
      .finally(() => setLoadingSessions(false));
  }, [isOpen]);

  // Fetch workspaces/agents when dialog opens (only if a relevant param is present)
  useEffect(() => {
    if (!isOpen) return;
    const needsWsOrAgents = parameters.some(
      (p) =>
        p.type === "workspaceId" ||
        p.type === "workspaceFolder" ||
        p.type === "acpServer",
    );
    if (!needsWsOrAgents) return;
    setLoadingWorkspaces(true);
    // Scope the ACP server list to the current folder when known, so the
    // acpServer dropdown only offers agents configured for this workspace.
    getSdkClient()
      .workspaces.list(workingDir ? { working_dir: workingDir } : undefined)
      .then((data) => {
        setWorkspaces(Array.isArray(data?.workspaces) ? data.workspaces : []);
        setAcpServers(Array.isArray(data?.acp_servers) ? data.acp_servers : []);
      })
      .catch((err) => {
        console.warn("[PromptParameterDialog] workspaces list error:", err);
        setWorkspaces([]);
        setAcpServers([]);
      })
      .finally(() => setLoadingWorkspaces(false));
  }, [isOpen, workingDir]);

  // Fetch workspace prompts when dialog opens (only if a prompts param is present)
  useEffect(() => {
    if (!isOpen) return;
    const needsPrompts = parameters.some((p) => p.type === "prompts");
    if (!needsPrompts || !workingDir) return;

    setLoadingPrompts(true);
    getSdkClient()
      .prompts.list({ working_dir: workingDir })
      .then((data) => {
        setPromptsList(data?.prompts || []);
      })
      .catch((err) => {
        console.warn("[PromptParameterDialog] prompts list error:", err);
        setPromptsList([]);
      })
      .finally(() => setLoadingPrompts(false));
  }, [isOpen, workingDir]);

  // Fetch workspace files per filename param when dialog opens. Each filename
  // param may declare its own dir/glob so we issue one request per param and
  // store the results keyed by param.name. Failures degrade to an empty list
  // (dialog falls back to a text input via the ParamField render branch).
  useEffect(() => {
    if (!isOpen) return;
    const filenameParams = parameters.filter((p) => p.type === "filename");
    if (filenameParams.length === 0 || !workingDir) return;

    // Mark all filename params as loading before firing requests, so the
    // dialog renders spinners immediately instead of a brief empty-state
    // flash before the first response lands.
    setLoadingFilesByParam(() => {
      const next = {};
      for (const p of filenameParams) next[p.name] = true;
      return next;
    });

    for (const p of filenameParams) {
      const params = { working_dir: workingDir };
      if (p.dir) params.dir = p.dir;
      // mitto-ebb: glob is a list on the wire (repeated ?glob=…). qs()
      // fans an array out into repeated params; a defensive scalar branch
      // survives an old payload slipping through.
      if (p.glob) {
        if (Array.isArray(p.glob) ? p.glob.length : String(p.glob))
          params.glob = p.glob;
      }
      const paramName = p.name;
      getSdkClient()
        .files.workspaceFiles.list(params)
        .then((data) => {
          setFilesByParam((prev) => ({
            ...prev,
            [paramName]: Array.isArray(data?.files) ? data.files : [],
          }));
        })
        .catch((err) => {
          console.warn("[PromptParameterDialog] files list error:", err);
          setFilesByParam((prev) => ({ ...prev, [paramName]: [] }));
        })
        .finally(() => {
          setLoadingFilesByParam((prev) => ({ ...prev, [paramName]: false }));
        });
    }
  }, [isOpen, workingDir]);

  // Fetch workspace directories per dirname param when dialog opens. Mirrors
  // the filename fetch pattern above: one request per param, results stored
  // keyed by param.name, failures degrade to an empty list (dialog falls back
  // to a text input via the ParamField render branch).
  useEffect(() => {
    if (!isOpen) return;
    const dirnameParams = parameters.filter((p) => p.type === "dirname");
    if (dirnameParams.length === 0 || !workingDir) return;

    setLoadingDirsByParam(() => {
      const next = {};
      for (const p of dirnameParams) next[p.name] = true;
      return next;
    });

    for (const p of dirnameParams) {
      const params = { working_dir: workingDir };
      if (p.dir) params.dir = p.dir;
      // mitto-ebb: see comment in the filename fetch above.
      if (p.glob) {
        if (Array.isArray(p.glob) ? p.glob.length : String(p.glob))
          params.glob = p.glob;
      }
      const paramName = p.name;
      getSdkClient()
        .files.workspaceDirs.list(params)
        .then((data) => {
          setDirsByParam((prev) => ({
            ...prev,
            [paramName]: Array.isArray(data?.dirs) ? data.dirs : [],
          }));
        })
        .catch((err) => {
          console.warn("[PromptParameterDialog] dirs list error:", err);
          setDirsByParam((prev) => ({ ...prev, [paramName]: [] }));
        })
        .finally(() => {
          setLoadingDirsByParam((prev) => ({ ...prev, [paramName]: false }));
        });
    }
  }, [isOpen, workingDir]);

  const handleFieldChange = useCallback((fieldName, val) => {
    setValues((prev) => ({ ...prev, [fieldName]: val }));
  }, []);

  // mitto-47y.6.1: collect all currently-picked (path, promptName) pairs from
  // the root down. Powers both the stale-clear effect (drop subtree slots
  // whose picker is no longer picked or no longer matches a known prompt) and
  // the remembered-args fetch effect (fire one fetch per picked prompt at
  // every level). Recomputed on every render — cheap tree walk of
  // (# non-empty pickers) at each level.
  const pickedPaths = collectPickedPaths(
    nestedValues,
    parameters,
    values,
    promptsList,
  );

  // mitto-47y.6.1: when any picker's value changes to a prompt whose declared
  // inner params drop out, prune the corresponding subtree slot so a
  // subsequent submit does not carry stale inner args. Recurses over the full
  // tree — same fail-open behavior as the v1 outer-only effect.
  useEffect(() => {
    if (!isOpen) return;
    setNestedValues((prev) =>
      pruneNestedTree(prev, parameters, values, promptsList),
    );
    // Dependency on the JSON-serialised picked-path map so the effect fires
    // when any picker's picked prompt changes but not on unrelated re-renders
    // (e.g. typing into an outer text field).
  }, [
    isOpen,
    JSON.stringify(pickedPaths.map((e) => [e.path, e.pickedPromptName])),
  ]);

  // mitto-47y.6.1: when a picker's value changes to a non-empty prompt name,
  // fetch that inner prompt's remembered-args and seed the corresponding
  // subtree node's `values` map for fields the user has not filled yet.
  // Fail-open: any error leaves nestedValues untouched. Skipped when the
  // picked prompt declares no `remember: folder` params. Fires at every depth
  // via collectPickedPaths — the path is joined to key the spinner state so
  // pickers at different depths sharing a name never collide.
  useEffect(() => {
    if (!isOpen || !workingDir) return;
    const cancels = [];
    for (const entry of pickedPaths) {
      const inner = Array.isArray(entry.prompt.parameters)
        ? entry.prompt.parameters
        : [];
      if (inner.length === 0) continue;
      // mitto-47y.6.2: gate also honors `remember: conversation` at every
      // nested picker depth (same semantics as the top-level effect above).
      const needsRemember = inner.some(
        (p) => p.remember === "folder" || p.remember === "conversation",
      );
      if (!needsRemember) continue;
      const pathKey = entry.path.join("/");
      const pickedName = entry.pickedPromptName;
      const path = entry.path;
      let cancelled = false;
      cancels.push(() => {
        cancelled = true;
      });
      setLoadingRememberedByPath((prev) => ({ ...prev, [pathKey]: true }));
      getSdkClient()
        .prompts.rememberedArgs({
          working_dir: workingDir,
          prompt: pickedName,
          session_id: hostSessionId,
        })
        .then((data) => {
          if (cancelled) return;
          const remembered =
            data && data.arguments && typeof data.arguments === "object"
              ? data.arguments
              : null;
          if (!remembered) return;
          setNestedValues((prev) => {
            // Merge remembered values into the subtree node at `path`, but
            // only for keys the user has not filled yet (existing non-empty
            // values win). Mirrors the v1 semantics one level deeper.
            let next = prev;
            for (const [k, v] of Object.entries(remembered)) {
              // Read current value at path.k
              let node = next;
              let existing;
              for (let i = 0; i < path.length; i++) {
                const pn = path[i];
                if (!node || !node[pn]) {
                  existing = undefined;
                  node = null;
                  break;
                }
                if (i === path.length - 1) {
                  existing = (node[pn].values || {})[k];
                } else {
                  node = node[pn].sub || {};
                }
              }
              if (
                existing === undefined ||
                existing === null ||
                existing === ""
              ) {
                next = updateNestedTree(next, path, k, v);
              }
            }
            return next;
          });
        })
        .catch((err) => {
          console.warn(
            "[PromptParameterDialog] nested remembered-args error:",
            err,
          );
        })
        .finally(() => {
          if (cancelled) return;
          setLoadingRememberedByPath((prev) => ({ ...prev, [pathKey]: false }));
        });
    }
    return () => {
      for (const c of cancels) c();
    };
  }, [
    isOpen,
    workingDir,
    hostSessionId,
    JSON.stringify(pickedPaths.map((e) => [e.path, e.pickedPromptName])),
  ]);

  // mitto-47y.6.1: single canonical callback for every nested level. `path`
  // is the picker-name chain from the root to (and including) the picker that
  // owns `innerName`; length === level of that parent picker (>= 1 for any
  // nested write). Delegates to the pure `updateNestedTree` helper.
  const handleNestedTreeChange = useCallback((path, innerName, val) => {
    setNestedValues((prev) => updateNestedTree(prev, path, innerName, val));
  }, []);

  const handleSubmit = useCallback(() => {
    // Build args map; omit empty optional fields.
    const args = {};
    for (const p of parameters) {
      if (p.type === "boolean") {
        // Always emit a definite "true"/"false" string (default unchecked = false).
        const checked = values[p.name] === true || values[p.name] === "true";
        args[p.name] = checked ? "true" : "false";
        continue;
      }
      const v = (values[p.name] || "").trim();
      if (v !== "" || p.required) {
        args[p.name] = v;
      }
      // mitto-47y.6.1: for `type: prompts` pickers with a non-empty picked
      // value, delegate the inner-args JSON build to `buildInnerArgs`. The
      // helper recursively emits `<Picker>` / `<Picker>_Args` companions for
      // deeper picker levels, so a level-0 picker whose picked prompt itself
      // declares a `type: prompts` param produces JSON-strings-inside-JSON-
      // strings on the wire — exactly what the backend's `ArgsMap` +
      // PromptTextWithArgs sub-render decode chain expects. Empty inner map
      // is omitted (matches v1 wire behavior: no `_Args` field emitted).
      if (p.type === "prompts" && v !== "" && p.collectInnerArgs !== false) {
        const pickedPrompt = (promptsList || []).find(
          (wp) => wp && wp.name === v,
        );
        const innerParams =
          pickedPrompt && Array.isArray(pickedPrompt.parameters)
            ? pickedPrompt.parameters
            : [];
        // Outer picker sits at level 0, so its inner block is at level 1 —
        // pass that as buildInnerArgs's "level of the picker owning these
        // innerParams". A `type: prompts` inside those innerParams would be
        // a picker at level 2 (still openable), and its inner picker at
        // level 3 (disallowed — matches MAX_NESTED_LEVEL cap).
        const innerOut = buildInnerArgs(
          innerParams,
          nestedValues[p.name],
          promptsList,
          1,
        );
        if (Object.keys(innerOut).length > 0) {
          args[`${p.name}_Args`] = JSON.stringify(innerOut);
        }
      }
    }
    onSubmit?.(args);
    onClose?.();
  }, [parameters, values, nestedValues, promptsList, onSubmit, onClose]);

  // Save enabled only when all required params have non-empty trimmed values.
  // Boolean params are excluded: a checkbox always has a definite answer.
  // mitto-9rff: read-only params (menu-supplied or otherwise pre-resolved —
  // see promptDialogParameters) are also excluded: they are filled by
  // construction from initialValues and never user-editable, so they must
  // never block Save.
  // mitto-l78: also blocked while any `type: prompts` picker has an unmet
  // required inner parameter (at any depth) — those fields are now hidden
  // behind the sliders sub-dialog, so Save must not silently succeed while
  // one is unset (the outer sliders button also flags this — see ParamField).
  const canSave =
    parameters
      .filter((p) => p.required && p.type !== "boolean" && !p.readOnly)
      .every((p) => (values[p.name] || "").trim() !== "") &&
    !hasUnmetNestedRequired(parameters, values, nestedValues, promptsList);

  if (!isOpen) return null;

  const footer = html`
    <button
      onClick=${onClose}
      class="btn btn-sm btn-ghost"
      data-testid="prompt-param-close-btn"
    >
      Close
    </button>
    <button
      onClick=${handleSubmit}
      disabled=${!canSave}
      class="btn btn-sm btn-primary"
      data-testid="prompt-param-save-btn"
    >
      Save
    </button>
  `;

  // mitto-boio: single shared render call site for a parameter field, used by
  // both the flat and tabbed layouts below so they never drift.
  const renderField = (param) => html`
    <${ParamField}
      key=${param.name}
      param=${param}
      value=${values[param.name] || ""}
      onChange=${handleFieldChange}
      beadsIssues=${beadsIssues}
      loadingBeads=${loadingBeads}
      sessions=${sessions}
      loadingSessions=${loadingSessions}
      workspaces=${workspaces}
      loadingWorkspaces=${loadingWorkspaces}
      workingDir=${workingDir}
      acpServers=${acpServers}
      hostSessionId=${hostSessionId}
      promptsList=${promptsList}
      loadingPrompts=${loadingPrompts}
      filesByParam=${filesByParam}
      loadingFilesByParam=${loadingFilesByParam}
      dirsByParam=${dirsByParam}
      loadingDirsByParam=${loadingDirsByParam}
      nestedNode=${nestedValues[param.name]}
      onNestedTreeChange=${handleNestedTreeChange}
      loadingRememberedByPath=${loadingRememberedByPath}
      ancestorPath=${[]}
      level=${0}
    />
  `;

  // mitto-boio hard back-compat invariant: when no parameter declares a
  // `group`, render TODAY'S markup verbatim — no tab bar, no "General"
  // header, no wrapper chrome. This is an early branch, not a degenerate
  // case of the tabbed layout below (see groupDialogParameters doc comment).
  const body = !tabbed
    ? html`<div class="space-y-4">${parameters.map(renderField)}</div>`
    : html`
        <div role="tablist" class="tabs tabs-border px-1 shrink-0">
          ${groups.map((g) => {
            const unmet = unmetGroups.has(g.name);
            const label = unmet ? `${g.name} *` : g.name;
            return html`
              <input
                key=${g.name}
                type="radio"
                name="prompt-param-tabs"
                role="tab"
                aria-label=${label}
                data-testid=${`prompt-param-tab-${g.name}`}
                data-unmet=${unmet ? "true" : undefined}
                checked=${activeTab === g.name}
                onChange=${() => setActiveTab(g.name)}
                class="tab ${activeTab === g.name
                  ? "tab-active text-mitto-accent"
                  : ""}"
              />
            `;
          })}
        </div>
        <div class="pt-4">
          ${groups.map(
            (g) => html`
              <div
                key=${g.name}
                class="space-y-4"
                hidden=${activeTab !== g.name}
              >
                ${g.params.map(renderField)}
              </div>
            `,
          )}
        </div>
      `;

  return html`
    <${Fragment}>
      <${Modal}
        isOpen=${isOpen}
        onClose=${onClose}
        title=${title}
        testid="prompt-param-dialog"
        closeTestid="prompt-param-dialog-close"
        backdropTestid="prompt-param-dialog-backdrop"
        footer=${footer}
      >
        ${body}
      </${Modal}>
    </${Fragment}>
  `;
}
