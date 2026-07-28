// Mitto Web Interface - Prompt Parameter Dialog Component
// Collects values for prompt parameters that a menu cannot auto-fill.
// Renders type-specific controls (textarea, beads selector, session selector,
// plain text input) and calls onSubmit with the collected arguments map.

const { useState, useEffect, useCallback, html, Fragment } = window.preact;

import { authFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";
import { endpoints } from "../utils/endpoints.js";
import { Modal } from "./Modal.js";

// mitto-47y.6.1: maximum picker-nesting level the UI will render normally.
// Mirrors backend `promptTextMaxDepth = 3` in internal/cel/templatefuncs.go —
// each picker level N produces `<Picker>_Args` that the backend consumes via a
// PromptTextWithArgs sub-render at depth N+1 (the outer prompt renders at
// depth 0). A picker at level 3 would drive a depth-4 sub-render which the
// backend fail-closes, so at level === MAX_NESTED_LEVEL the picker itself
// renders as a disabled placeholder — matching the pre-existing v1 note used
// at level 1 (Phase B `mitto-47y.2`). Keep in sync with promptTextMaxDepth.
const MAX_NESTED_LEVEL = 3;

// mitto-47y.6.1: write `val` into the nested-args tree at `path`.`innerName`.
// `tree` shape: `{ [pickerName]: { values, sub } }` (the "sub" of an implicit
// root). `path` is the picker-name chain from the outermost picker down to the
// picker that owns the field being written (length === level of the field's
// parent picker). Missing intermediate nodes are created on write; existing
// nodes are shallow-cloned so callers can rely on referential-equality checks
// for cheap change detection. Returns a NEW tree — the input is never mutated.
export function updateNestedTree(tree, path, innerName, val) {
  if (!Array.isArray(path) || path.length === 0) return tree;
  const next = { ...(tree || {}) };
  let parent = next;
  for (let i = 0; i < path.length; i++) {
    const pickerName = path[i];
    const existing = parent[pickerName] || { values: {}, sub: {} };
    const clone = {
      values: { ...(existing.values || {}) },
      sub: { ...(existing.sub || {}) },
    };
    parent[pickerName] = clone;
    if (i === path.length - 1) {
      clone.values = { ...clone.values, [innerName]: val };
    } else {
      parent = clone.sub;
    }
  }
  return next;
}

// mitto-47y.6.1: prune subtree slots whose picker-value no longer matches a
// prompt in `promptsList`, walking from the root down. `outerParams` is the
// PromptParameter[] at the current level; `outerValues` is the values map at
// this level (for the root, this is the top-level `values` state). Recurses
// into each still-valid picker using the picked prompt's inner parameters.
// Returns a NEW tree (or the same reference when nothing changed). Pure —
// mirrors the v1 stale-clear effect but walks the whole tree.
export function pruneNestedTree(tree, outerParams, outerValues, promptsList) {
  if (!tree || typeof tree !== "object") return tree;
  let mutated = false;
  const next = { ...tree };
  const paramsList = Array.isArray(outerParams) ? outerParams : [];
  const promptByName = new Map(
    (promptsList || [])
      .filter((wp) => wp && wp.name)
      .map((wp) => [wp.name, wp]),
  );
  // Any subtree keyed under a name that is NOT a `type: prompts` param at this
  // level, or whose picked value at this level is empty / no longer matches a
  // known prompt, is stale and gets dropped.
  const pickerParamByName = new Map(
    paramsList.filter((p) => p && p.type === "prompts").map((p) => [p.name, p]),
  );
  for (const key of Object.keys(next)) {
    const pickerParam = pickerParamByName.get(key);
    if (!pickerParam) {
      delete next[key];
      mutated = true;
      continue;
    }
    const pickedName = ((outerValues && outerValues[key]) || "")
      .toString()
      .trim();
    if (!pickedName) {
      delete next[key];
      mutated = true;
      continue;
    }
    const pickedPrompt = promptByName.get(pickedName);
    if (!pickedPrompt) {
      delete next[key];
      mutated = true;
      continue;
    }
    // Descend into this picker's own subtree. The recursive call uses the
    // picked prompt's inner parameters as the next level's outerParams and
    // this node's own `values` map as outerValues at that deeper level.
    const node = next[key];
    const innerParams = Array.isArray(pickedPrompt.parameters)
      ? pickedPrompt.parameters
      : [];
    const prunedSub = pruneNestedTree(
      node.sub || {},
      innerParams,
      node.values || {},
      promptsList,
    );
    if (prunedSub !== node.sub) {
      next[key] = { ...node, sub: prunedSub };
      mutated = true;
    }
  }
  return mutated ? next : tree;
}

// mitto-47y.6.1: build the JSON-string payload for a picker at any level.
// Mirrors the v1 outer serializer per-field rules (booleans → "true"/"false",
// strings → trim, drop-if-empty-and-not-required), and recursively emits
// `<Picker>` / `<Picker>_Args` companions for inner `type: prompts` fields.
// `level` is the depth of the BLOCK being processed — the level at which
// these `innerParams` live. Level 0 = the dialog's top-level `parameters`;
// level 1 = the inner block opened by a level-0 picker; and so on. A picker
// at `level >= MAX_NESTED_LEVEL` cannot open another block (its own `_Args`
// would drive a backend sub-render past `promptTextMaxDepth`), so its inner
// `type: prompts` field is skipped entirely (defense-in-depth alongside the
// ParamField disabled render at the same level). Returns a plain object;
// JSON encoding is the caller's job so an empty result can be detected and
// the `_Args` companion omitted from the outer args map (matches v1 wire).
export function buildInnerArgs(innerParams, node, promptsList, level) {
  const out = {};
  const values = (node && node.values) || {};
  const sub = (node && node.sub) || {};
  const paramsList = Array.isArray(innerParams) ? innerParams : [];
  for (const ip of paramsList) {
    if (!ip || !ip.name) continue;
    if (ip.type === "prompts") {
      // Deepest allowed picker: skip inner `type: prompts` entirely — its
      // `_Args` would need a backend sub-render at depth level+1 which
      // exceeds promptTextMaxDepth once level >= MAX_NESTED_LEVEL.
      if (level >= MAX_NESTED_LEVEL) continue;
      const pickedName = (values[ip.name] || "").toString().trim();
      if (pickedName === "") {
        if (ip.required) out[ip.name] = "";
        continue;
      }
      out[ip.name] = pickedName;
      const pickedPrompt = (promptsList || []).find(
        (wp) => wp && wp.name === pickedName,
      );
      const deeperInner =
        pickedPrompt && Array.isArray(pickedPrompt.parameters)
          ? pickedPrompt.parameters
          : [];
      const deeperNode = sub[ip.name];
      const deeperOut = buildInnerArgs(
        deeperInner,
        deeperNode,
        promptsList,
        level + 1,
      );
      if (Object.keys(deeperOut).length > 0) {
        out[`${ip.name}_Args`] = JSON.stringify(deeperOut);
      }
      continue;
    }
    if (ip.type === "boolean") {
      const checked = values[ip.name] === true || values[ip.name] === "true";
      out[ip.name] = checked ? "true" : "false";
      continue;
    }
    const iv = (values[ip.name] || "").toString().trim();
    if (iv !== "" || ip.required) {
      out[ip.name] = iv;
    }
  }
  return out;
}

// mitto-47y.6.1: walk the nested tree collecting `{ path, pickedPromptName }`
// entries for every currently-picked node — used by the remembered-args fetch
// effect to fire one request per non-empty picker at every depth. `outerParams`
// / `outerValues` describe the level whose picker keys are stored in `tree`;
// for the root that's the dialog's `parameters` prop and `values` state.
export function collectPickedPaths(
  tree,
  outerParams,
  outerValues,
  promptsList,
  parentPath = [],
) {
  const out = [];
  if (!tree || typeof tree !== "object") return out;
  const pickerParams = (outerParams || []).filter(
    (p) => p && p.type === "prompts",
  );
  for (const p of pickerParams) {
    const pickedName = ((outerValues && outerValues[p.name]) || "")
      .toString()
      .trim();
    if (!pickedName) continue;
    const pickedPrompt = (promptsList || []).find(
      (wp) => wp && wp.name === pickedName,
    );
    if (!pickedPrompt) continue;
    const path = [...parentPath, p.name];
    out.push({ path, pickedPromptName: pickedName, prompt: pickedPrompt });
    const node = tree[p.name];
    if (!node) continue;
    const innerParams = Array.isArray(pickedPrompt.parameters)
      ? pickedPrompt.parameters
      : [];
    out.push(
      ...collectPickedPaths(
        node.sub || {},
        innerParams,
        node.values || {},
        promptsList,
        path,
      ),
    );
  }
  return out;
}

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
  const { name, type, description, required, multiLine, options } = param;
  const hasOptions = Array.isArray(options) && options.length > 0;

  let control;
  if (type === "beadsId") {
    if (loadingBeads) {
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
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
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
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
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
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
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
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
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
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
              html`<option key=${ws.working_dir} value=${ws.working_dir}>
                ${ws.working_dir}${ws.working_dir === workingDir
                  ? " (current)"
                  : ""}
              </option>`,
          )}
        </select>
      `;
    }
  } else if (type === "acpServer") {
    if (loadingWorkspaces) {
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
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
    // mitto-47y.6.1: at level === MAX_NESTED_LEVEL a picker would drive a
    // backend sub-render past `promptTextMaxDepth` — render the pre-existing
    // disabled placeholder instead (defense-in-depth alongside the submit
    // serializer skip at the same depth). This preserves the v1 (`mitto-47y.2`)
    // Phase B note at the new deepest level.
    if (level >= MAX_NESTED_LEVEL) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=""
          disabled
          placeholder="nested prompt pickers are not supported here"
        />
      `;
    } else if (loadingPrompts) {
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
    } else if (!promptsList || promptsList.length === 0) {
      control = html`
        <input
          type="text"
          class="input input-sm w-full"
          value=${value}
          onInput=${(e) => onChange(name, e.target.value)}
          placeholder="Prompt name"
        />
      `;
    } else {
      control = html`
        <select
          class="select select-sm w-full"
          value=${value}
          onChange=${(e) => onChange(name, e.target.value)}
        >
          <option value="">Select a prompt…</option>
          ${promptsList.map(
            (p) =>
              html`<option key=${p.name} value=${p.name}>${p.name}</option>`,
          )}
        </select>
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
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
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
      control = html`<span class="loading loading-spinner loading-xs"></span>`;
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

  // mitto-47y.6.1: recursive nested-block render. A `type: prompts` picker
  // that is below the depth cap AND has a non-empty picked value opens an
  // inline block for the picked prompt's parameters, reusing ParamField at
  // `level + 1`. Inner-param lookup is against the shared promptsList prop
  // (single source of truth for parameters at every level). At the cap level
  // the block is not rendered (the control above is the disabled placeholder).
  const trimmedValue = typeof value === "string" ? value.trim() : "";
  const pickedPrompt =
    type === "prompts" && trimmedValue && Array.isArray(promptsList)
      ? promptsList.find((wp) => wp && wp.name === trimmedValue)
      : null;
  const innerParams =
    pickedPrompt && Array.isArray(pickedPrompt.parameters)
      ? pickedPrompt.parameters
      : null;
  const showNested =
    type === "prompts" &&
    level < MAX_NESTED_LEVEL &&
    innerParams &&
    innerParams.length > 0;
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
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-mitto-text-secondary">
        ${name}
        ${required && html`<span class="text-mitto-danger ml-0.5">*</span>`}
      </legend>
      ${control}
      ${description &&
      html`<p class="text-xs text-mitto-text-muted mt-1">${description}</p>`}
      ${showNested &&
      html`
        <fieldset
          class="fieldset mt-3 pl-4 border-l-2 border-mitto-border space-y-3"
          data-testid=${nestedTestid}
        >
          <legend class="fieldset-legend text-mitto-text-secondary">
            Parameters for ${(pickedPrompt && pickedPrompt.name) || value}
          </legend>
          ${loadingNestedRemembered &&
          html`<span class="loading loading-spinner loading-xs"></span>`}
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
        </fieldset>
      `}
    </fieldset>
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

  // Reset state each time the dialog opens; seed from initialValues when provided.
  // Seeds on the open transition only — initialValues is intentionally NOT a
  // dependency: callers may pass a fresh object literal each render (e.g. `|| {}`),
  // which would otherwise re-run this effect on every parent render and wipe
  // user-typed values.
  useEffect(() => {
    if (!isOpen) return;
    setValues(initialValues ? { ...initialValues } : {});
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

  // mitto-x8v: when the dialog opens for a prompt that declares any
  // `remember: folder` parameter, fetch previously-remembered values for the
  // current workspace and merge them into `values`. Remembered entries
  // override any seeded initialValues (per spec). Fail-open: any fetch
  // failure leaves values untouched.
  useEffect(() => {
    if (!isOpen || !workingDir) return;
    const promptName = title;
    if (!promptName || promptName === "Prompt parameters") return;
    const needsRemember = parameters.some((p) => p.remember === "folder");
    if (!needsRemember) return;
    let cancelled = false;
    authFetch(endpoints.workspacePrompts.rememberedArgs(workingDir, promptName))
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
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
  }, [isOpen, workingDir, title]);

  // Fetch beads issues when dialog opens (only if a beadsId param is present)
  useEffect(() => {
    if (!isOpen) return;
    const needsBeads = parameters.some((p) => p.type === "beadsId");
    if (!needsBeads || !workingDir) return;

    setLoadingBeads(true);
    authFetch(endpoints.issues.list({ working_dir: workingDir }))
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
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
    authFetch(endpoints.sessions.list())
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
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
    const wsUrl = endpoints.workspaces.list(
      workingDir ? { working_dir: workingDir } : undefined,
    );
    authFetch(wsUrl)
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
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
    authFetch(endpoints.workspacePrompts.list({ working_dir: workingDir }))
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
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
      if (p.glob) params.glob = p.glob;
      const paramName = p.name;
      authFetch(endpoints.workspaceFiles.list(params))
        .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
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
      if (p.glob) params.glob = p.glob;
      const paramName = p.name;
      authFetch(endpoints.workspaceDirs.list(params))
        .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
      const needsRemember = inner.some((p) => p.remember === "folder");
      if (!needsRemember) continue;
      const pathKey = entry.path.join("/");
      const pickedName = entry.pickedPromptName;
      const path = entry.path;
      let cancelled = false;
      cancels.push(() => {
        cancelled = true;
      });
      setLoadingRememberedByPath((prev) => ({ ...prev, [pathKey]: true }));
      authFetch(
        endpoints.workspacePrompts.rememberedArgs(workingDir, pickedName),
      )
        .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
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
              if (existing === undefined || existing === null || existing === "") {
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    isOpen,
    workingDir,
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
      if (p.type === "prompts" && v !== "") {
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
  const canSave = parameters
    .filter((p) => p.required && p.type !== "boolean")
    .every((p) => (values[p.name] || "").trim() !== "");

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
        <div class="space-y-4">
          ${parameters.map(
            (param) =>
              html`<${ParamField}
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
              />`,
          )}
        </div>
      </${Modal}>
    </${Fragment}>
  `;
}
