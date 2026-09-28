// Mitto Web Interface - UI prompt input draft store (mitto-osmb)
//
// Per-session, per-request drafts for the inputs rendered by MCP UI prompts
// (mitto_ui_options free text, mitto_ui_textbox, mitto_ui_form). ChatInput is
// a single component instance shared across sessions, so plain local state
// was wiped whenever the user switched conversations while a prompt was still
// pending. Modelled on draftStore.js (the main composer draft), but each entry
// is also tagged with the prompt's requestId: a draft is only returned for the
// request it was typed into, so a genuinely new prompt always starts fresh.
//
// Deliberately free of window/preact dependencies so it can be unit tested.

const NO_SESSION_KEY = "__no_session__";

/**
 * @typedef {Object} UIPromptDraftFields
 * @property {string} [freeText] - mitto_ui_options free-text input
 * @property {string} [textbox] - mitto_ui_textbox textarea content
 * @property {string} [combo] - selected combo-box option id
 * @property {Object<string, string>} [form] - mitto_ui_form named field values
 */

/** @type {Map<string, { requestId: string, fields: UIPromptDraftFields }>} */
const drafts = new Map();

function resolveKey(sessionId) {
  return sessionId ?? NO_SESSION_KEY;
}

function sameRequest(a, b) {
  return a != null && b != null && String(a) === String(b);
}

/**
 * Returns the draft fields for `sessionId` if they were saved for
 * `requestId`, else null.
 * @returns {UIPromptDraftFields | null}
 */
export function getUIPromptDraft(sessionId, requestId) {
  const entry = drafts.get(resolveKey(sessionId));
  if (!entry || !sameRequest(entry.requestId, requestId)) return null;
  return entry.fields;
}

/**
 * Saves one draft field. If the session's stored entry belongs to a
 * different request, it is discarded and replaced.
 */
export function setUIPromptDraftField(sessionId, requestId, field, value) {
  if (requestId == null) return;
  const key = resolveKey(sessionId);
  let entry = drafts.get(key);
  if (!entry || !sameRequest(entry.requestId, requestId)) {
    entry = { requestId: String(requestId), fields: {} };
    drafts.set(key, entry);
  }
  entry.fields = { ...entry.fields, [field]: value };
}

/**
 * Clears the session's draft. When `requestId` is given, only clears if the
 * stored entry belongs to that request (a mismatch is a no-op).
 */
export function clearUIPromptDraft(sessionId, requestId) {
  const key = resolveKey(sessionId);
  const entry = drafts.get(key);
  if (!entry) return;
  if (requestId != null && !sameRequest(entry.requestId, requestId)) return;
  drafts.delete(key);
}

/**
 * Current input values ChatInput should render for `prompt` in `sessionId`:
 * the saved draft when one exists for this exact request, otherwise the
 * defaults (the textbox defaults to the agent-supplied `prompt.text`).
 */
export function resolveUIPromptValues(sessionId, prompt) {
  const draft = prompt ? getUIPromptDraft(sessionId, prompt.requestId) : null;
  return {
    freeText: draft?.freeText ?? "",
    textbox: draft?.textbox ?? (prompt?.text || ""),
    combo: draft?.combo ?? "",
    form: draft?.form ?? null,
  };
}

const FORM_FIELD_SELECTOR = "input[name], select[name], textarea[name]";

/**
 * Collects named field values from a mitto_ui_form container, in the shape
 * submitted to the agent: checkbox → "true"/"false", radio → checked value,
 * everything else → el.value.
 * @returns {Object<string, string>}
 */
export function collectFormValues(container) {
  const values = {};
  if (!container || typeof container.querySelectorAll !== "function") {
    return values;
  }
  container.querySelectorAll(FORM_FIELD_SELECTOR).forEach((el) => {
    const name = el.name;
    if (!name) return;
    if (el.type === "checkbox") {
      values[name] = el.checked ? "true" : "false";
    } else if (el.type === "radio") {
      if (el.checked) values[name] = el.value;
    } else {
      values[name] = el.value;
    }
  });
  return values;
}

/**
 * Re-applies values produced by collectFormValues to a freshly injected form.
 * Fields absent from `values` keep their HTML defaults; file inputs are
 * skipped (their value cannot be set programmatically).
 */
export function applyFormValues(container, values) {
  if (!container || !values || typeof container.querySelectorAll !== "function")
    return;
  container.querySelectorAll(FORM_FIELD_SELECTOR).forEach((el) => {
    const name = el.name;
    if (!name || !Object.prototype.hasOwnProperty.call(values, name)) return;
    const saved = values[name];
    if (el.type === "file") return;
    if (el.type === "checkbox") {
      el.checked = saved === "true";
    } else if (el.type === "radio") {
      el.checked = el.value === saved;
    } else {
      el.value = saved;
    }
  });
}

/** Test-only: reset the shared store between test cases. */
export function _resetUIPromptDraftStoreForTests() {
  drafts.clear();
}
