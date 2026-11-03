/**
 * Pure helpers for the per-conversation "Remember responses" control in the
 * prompt parameter dialog.
 *
 * Precedence when the control is active: caller/contextual values (initial
 * values) > user edits > last saved conversation responses > declared
 * defaults. Late remembered-args responses must never overwrite either a
 * contextual value or a field the user already touched.
 */

/**
 * Keys whose contextual initial value is non-empty. Empty strings do not
 * count as context, so remembered values may still fill them.
 * @param {Object} [initialValues]
 * @returns {Set<string>}
 */
export function contextualKeys(initialValues) {
  const keys = new Set();
  if (!initialValues || typeof initialValues !== "object") return keys;
  for (const [k, v] of Object.entries(initialValues)) {
    if (v !== undefined && v !== null && String(v) !== "") keys.add(k);
  }
  return keys;
}

/**
 * Merge remembered values into the current dialog values without overriding
 * contextual values or fields the user has touched.
 * @param {Object} prev - current `values` map
 * @param {Object} remembered - arguments returned by the remembered-args GET
 * @param {{ contextKeys: Set<string>, touchedKeys: Set<string> }} guards
 * @returns {Object}
 */
export function applyRememberedValues(prev, remembered, { contextKeys, touchedKeys }) {
  let next = prev;
  for (const [k, v] of Object.entries(remembered || {})) {
    if (contextKeys.has(k) || touchedKeys.has(k)) continue;
    if (next === prev) next = { ...prev };
    next[k] = v;
  }
  return next;
}

/**
 * Value for the `rememberResponses` submit option.
 *  - control not shown: undefined (caller unchanged, no persistence change)
 *  - user toggled the checkbox: the checkbox value
 *  - untouched with a saved preference: the saved preference
 *  - untouched without a saved preference: undefined (no persistence change)
 * @param {{ showControl: boolean, touched: boolean, checked: boolean, savedPreference?: boolean }} state
 * @returns {boolean|undefined}
 */
export function resolveRememberResponses({ showControl, touched, checked, savedPreference }) {
  if (!showControl) return undefined;
  if (touched) return checked === true;
  if (typeof savedPreference === "boolean") return savedPreference;
  return undefined;
}
