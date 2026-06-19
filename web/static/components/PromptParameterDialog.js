// Mitto Web Interface - Prompt Parameter Dialog Component
// Collects values for prompt parameters that a menu cannot auto-fill.
// Renders type-specific controls (textarea, beads selector, session selector,
// plain text input) and calls onSubmit with the collected arguments map.

const { useState, useEffect, useCallback, html, Fragment } = window.preact;

import { authFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";
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
 */
function ParamField({
  param,
  value,
  onChange,
  beadsIssues,
  loadingBeads,
  sessions,
  loadingSessions,
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
    // beadsTitle, workspaceId, workspaceFolder, unknown → plain text input
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
        ${required && html`<span class="text-mitto-danger ml-0.5">*</span>`}
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
  title = "Prompt parameters",
}) {
  const [values, setValues] = useState({});
  const [beadsIssues, setBeadsIssues] = useState([]);
  const [loadingBeads, setLoadingBeads] = useState(false);
  const [sessions, setSessions] = useState([]);
  const [loadingSessions, setLoadingSessions] = useState(false);

  // Reset state each time the dialog opens
  useEffect(() => {
    if (!isOpen) return;
    setValues({});
    setBeadsIssues([]);
    setSessions([]);
    setLoadingBeads(false);
    setLoadingSessions(false);
  }, [isOpen]);

  // Fetch beads issues when dialog opens (only if a beadsId param is present)
  useEffect(() => {
    if (!isOpen) return;
    const needsBeads = parameters.some((p) => p.type === "beadsId");
    if (!needsBeads || !workingDir) return;

    setLoadingBeads(true);
    const url =
      apiUrl("/api/beads/list") +
      "?working_dir=" +
      encodeURIComponent(workingDir);
    authFetch(url)
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
    const needsSessions = parameters.some((p) => p.type === "sessionId");
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

  const handleFieldChange = useCallback((fieldName, val) => {
    setValues((prev) => ({ ...prev, [fieldName]: val }));
  }, []);

  const handleSubmit = useCallback(() => {
    // Build args map; omit empty optional fields
    const args = {};
    for (const p of parameters) {
      const v = (values[p.name] || "").trim();
      if (v !== "" || p.required) {
        args[p.name] = v;
      }
    }
    onSubmit?.(args);
    onClose?.();
  }, [parameters, values, onSubmit, onClose]);

  // Save enabled only when all required params have non-empty trimmed values
  const canSave = parameters
    .filter((p) => p.required)
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
              />`,
          )}
        </div>
      </${Modal}>
    </${Fragment}>
  `;
}
