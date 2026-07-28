// Mitto Web Interface - Prompt Parameter Dialog Component
// Collects values for prompt parameters that a menu cannot auto-fill.
// Renders type-specific controls (textarea, beads selector, session selector,
// plain text input) and calls onSubmit with the collected arguments map.

const { useState, useEffect, useCallback, html, Fragment } = window.preact;

import { authFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";
import { endpoints } from "../utils/endpoints.js";
import { Modal } from "./Modal.js";

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
 * @param {Array}  [nestedParams] - inner PromptParameter[] to render below a
 *   `type: prompts` picker (may be [] or undefined). mitto-47y.2.
 * @param {Object} [nestedValues] - { [innerName]: value } for the picked prompt's inner fields.
 * @param {Function} [onNestedChange] - (pickerName, innerName, val) => void.
 * @param {boolean} [loadingNestedRemembered] - remembered-args fetch in flight for this picker.
 * @param {string}  [pickedPromptName] - display name of the picked prompt (for the legend).
 * @param {boolean} [isNested] - true when this field is an inner nested field;
 *   suppresses further recursion into `type: prompts` (rendered as a disabled note).
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
  nestedParams,
  nestedValues,
  onNestedChange,
  loadingNestedRemembered,
  pickedPromptName,
  isNested,
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
    // mitto-47y.2: when this is an inner (nested) field, we do NOT recurse
    // into another picker — render a disabled note instead (depth-1 cap).
    if (isNested) {
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

  // mitto-47y.2: for a `type: prompts` picker that is NOT itself nested and
  // has a non-empty picked value with inner params, render an inline nested
  // block reusing ParamField for each inner param. Inner `type: prompts` is
  // rendered as a disabled note (depth-1 cap; see the isNested guard above).
  const showNested =
    type === "prompts" &&
    !isNested &&
    Array.isArray(nestedParams) &&
    nestedParams.length > 0;

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
          data-testid=${`nested-params-${name}`}
        >
          <legend class="fieldset-legend text-mitto-text-secondary">
            Parameters for ${pickedPromptName || value}
          </legend>
          ${loadingNestedRemembered &&
          html`<span class="loading loading-spinner loading-xs"></span>`}
          ${nestedParams.map(
            (inner) =>
              html`<${ParamField}
                key=${inner.name}
                param=${inner}
                value=${(nestedValues && nestedValues[inner.name]) || ""}
                onChange=${(innerName, val) =>
                  onNestedChange && onNestedChange(name, innerName, val)}
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
                isNested=${true}
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
  // mitto-47y.2: nested-param state for `type: prompts` pickers. Mirrors the
  // per-param map shape (filesByParam/dirsByParam) so a single dialog can
  // host multiple pickers with disjoint inner scopes without key collisions.
  //   nestedValues:                    { [pickerName]: { [innerName]: value } }
  //   loadingNestedRememberedByPicker: { [pickerName]: bool }
  const [nestedValues, setNestedValues] = useState({});
  const [loadingNestedRememberedByPicker, setLoadingNestedRememberedByPicker] =
    useState({});

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
    setLoadingNestedRememberedByPicker({});
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

  // mitto-47y.2: derive the inner-param list for each `type: prompts` picker
  // from the picked value against the already-loaded promptsList. No extra
  // API call — WebPrompt already carries `parameters` in the list response.
  // Recomputed on every render (cheap: N pickers × M prompts) and is the
  // single source of truth for what the outer render passes to ParamField.
  const nestedParamsByPicker = {};
  const pickedPromptNameByPicker = {};
  for (const p of parameters) {
    if (p.type !== "prompts") continue;
    const picked = (values[p.name] || "").trim();
    if (!picked) continue;
    const found = (promptsList || []).find((wp) => wp && wp.name === picked);
    if (!found) continue;
    pickedPromptNameByPicker[p.name] = found.name;
    // Filter out inner `type: prompts` params from the fetch/derive set so
    // the depth-1 cap is enforced at the data layer too (defense in depth
    // — the ParamField render branch also renders them as a disabled note).
    const innerParams = Array.isArray(found.parameters) ? found.parameters : [];
    nestedParamsByPicker[p.name] = innerParams;
  }

  // mitto-47y.2: when a picker's value changes to a prompt whose declared
  // inner params drop out, clear the corresponding nestedValues[pickerName]
  // slot so a subsequent submit does not carry stale inner args.
  useEffect(() => {
    if (!isOpen) return;
    setNestedValues((prev) => {
      let mutated = false;
      const next = { ...prev };
      for (const key of Object.keys(prev)) {
        if (!nestedParamsByPicker[key]) {
          delete next[key];
          mutated = true;
        }
      }
      return mutated ? next : prev;
    });
    // Dependency intentionally on the JSON-serialised picker → prompt map so
    // the effect fires when the picked prompt for any picker changes but not
    // on unrelated re-renders (e.g. typing into an outer text field).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, JSON.stringify(pickedPromptNameByPicker)]);

  // mitto-47y.2: when a picker's value changes to a non-empty prompt name,
  // fetch that inner prompt's remembered-args and seed nestedValues[picker]
  // for fields the user has not filled yet. Fail-open: any error leaves
  // nestedValues untouched. Skipped when the picked prompt declares no
  // parameters (nothing to seed).
  useEffect(() => {
    if (!isOpen || !workingDir) return;
    const cancels = [];
    for (const [pickerName, pickedName] of Object.entries(
      pickedPromptNameByPicker,
    )) {
      const inner = nestedParamsByPicker[pickerName];
      if (!inner || inner.length === 0) continue;
      const needsRemember = inner.some((p) => p.remember === "folder");
      if (!needsRemember) continue;
      let cancelled = false;
      cancels.push(() => {
        cancelled = true;
      });
      setLoadingNestedRememberedByPicker((prev) => ({
        ...prev,
        [pickerName]: true,
      }));
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
            const existing = prev[pickerName] || {};
            const merged = { ...existing };
            for (const [k, v] of Object.entries(remembered)) {
              if (
                merged[k] === undefined ||
                merged[k] === null ||
                merged[k] === ""
              ) {
                merged[k] = v;
              }
            }
            return { ...prev, [pickerName]: merged };
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
          setLoadingNestedRememberedByPicker((prev) => ({
            ...prev,
            [pickerName]: false,
          }));
        });
    }
    return () => {
      for (const c of cancels) c();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, workingDir, JSON.stringify(pickedPromptNameByPicker)]);

  const handleNestedFieldChange = useCallback((pickerName, innerName, val) => {
    setNestedValues((prev) => {
      const existing = prev[pickerName] || {};
      return {
        ...prev,
        [pickerName]: { ...existing, [innerName]: val },
      };
    });
  }, []);

  const handleSubmit = useCallback(() => {
    // Build args map; omit empty optional fields
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
      // mitto-47y.2: for `type: prompts` pickers with a non-empty picked
      // value and inner values collected, serialize the inner map as a JSON
      // string under `<PickerName>_Args`. Consumed on the backend by
      // `ArgsMap "<PickerName>_Args"` inside PromptTextWithArgs (Phase A).
      // Skip when the inner map is empty or the picked prompt declared no
      // params — an empty _Args field would just decode back to an empty
      // map, so omitting is equivalent and cleaner on the wire.
      if (p.type === "prompts" && v !== "") {
        const inner = nestedValues[p.name] || {};
        // Filter out empty inner values and boolean-normalize (mirrors the
        // outer loop above): booleans become "true"/"false", strings are
        // trimmed and dropped when empty and not required.
        const innerParams =
          (promptsList || []).find((wp) => wp && wp.name === v)?.parameters ||
          [];
        const innerOut = {};
        for (const ip of innerParams) {
          if (ip.type === "prompts") continue; // depth-1 cap
          if (ip.type === "boolean") {
            const ck = inner[ip.name] === true || inner[ip.name] === "true";
            innerOut[ip.name] = ck ? "true" : "false";
            continue;
          }
          const iv = (inner[ip.name] || "").toString().trim();
          if (iv !== "" || ip.required) {
            innerOut[ip.name] = iv;
          }
        }
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
                nestedParams=${nestedParamsByPicker[param.name]}
                nestedValues=${nestedValues[param.name]}
                onNestedChange=${handleNestedFieldChange}
                loadingNestedRemembered=${!!loadingNestedRememberedByPicker[
                  param.name
                ]}
                pickedPromptName=${pickedPromptNameByPicker[param.name]}
              />`,
          )}
        </div>
      </${Modal}>
    </${Fragment}>
  `;
}
