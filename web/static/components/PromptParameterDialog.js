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
 * @param {Object} param     - { name, type, description?, required? }
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
}) {
  const { name, type, description, required } = param;

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
                ${s.title || s.session_id}
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
                ${s.title || s.session_id}
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
  } else if (type === "text") {
    control = html`
      <textarea
        class="textarea textarea-sm w-full resize-none"
        rows="3"
        value=${value}
        onInput=${(e) => onChange(name, e.target.value)}
      ></textarea>
    `;
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

  return html`
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-mitto-text-secondary">
        ${name}
        ${required &&
        type !== "boolean" &&
        html`<span class="text-mitto-danger ml-0.5">*</span>`}
      </legend>
      ${control}
      ${description &&
      html`<p class="text-xs text-mitto-text-muted mt-1">${description}</p>`}
    </fieldset>
  `;
}

/**
 * PromptParameterDialog — collects values for prompt parameters that a menu
 * could NOT auto-fill, then returns them as an arguments map via onSubmit.
 *
 * @param {boolean}  isOpen     - controls visibility
 * @param {Function} onClose    - called on dismiss (no onSubmit)
 * @param {Function} onSubmit   - called with { [paramName]: string } on Save
 * @param {Array}    parameters - missing params: [{ name, type, description?, required? }]
 * @param {string}   workingDir - workspace directory (needed for beadsId selector)
 * @param {string}   [title]    - dialog title; defaults to "Prompt parameters"
 */
export function PromptParameterDialog({
  isOpen,
  onClose,
  onSubmit,
  parameters = [],
  workingDir,
  hostSessionId,
  title = "Prompt parameters",
}) {
  const [values, setValues] = useState({});
  const [beadsIssues, setBeadsIssues] = useState([]);
  const [loadingBeads, setLoadingBeads] = useState(false);
  const [sessions, setSessions] = useState([]);
  const [loadingSessions, setLoadingSessions] = useState(false);
  const [workspaces, setWorkspaces] = useState([]);
  const [loadingWorkspaces, setLoadingWorkspaces] = useState(false);
  const [acpServers, setAcpServers] = useState([]);

  // Reset state each time the dialog opens
  useEffect(() => {
    if (!isOpen) return;
    setValues({});
    setBeadsIssues([]);
    setSessions([]);
    setWorkspaces([]);
    setAcpServers([]);
    setLoadingBeads(false);
    setLoadingSessions(false);
    setLoadingWorkspaces(false);
  }, [isOpen]);

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
  }, [isOpen, workingDir]); // eslint-disable-line react-hooks/exhaustive-deps

  // Fetch sessions when dialog opens (only if a sessionId param is present)
  useEffect(() => {
    if (!isOpen) return;
    const needsSessions = parameters.some(
      (p) => p.type === "sessionId" || p.type === "childSessionId",
    );
    if (!needsSessions) return;

    setLoadingSessions(true);
    authFetch(apiUrl("/api/sessions"))
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
  }, [isOpen]); // eslint-disable-line react-hooks/exhaustive-deps

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
    const wsUrl = workingDir
      ? apiUrl("/api/workspaces") +
        "?working_dir=" +
        encodeURIComponent(workingDir)
      : apiUrl("/api/workspaces");
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
  }, [isOpen, workingDir]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleFieldChange = useCallback((fieldName, val) => {
    setValues((prev) => ({ ...prev, [fieldName]: val }));
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
    }
    onSubmit?.(args);
    onClose?.();
  }, [parameters, values, onSubmit, onClose]);

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
              />`,
          )}
        </div>
      </${Modal}>
    </${Fragment}>
  `;
}
