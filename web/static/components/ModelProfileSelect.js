// Mitto Web Interface - Model Profile Select Component
const { html } = window.preact;

// Sentinel value used for the (disabled) legacy option so a controlled
// <select> can display it as selected without colliding with the real
// "-- None --" option (value="").
const LEGACY_VALUE = "__legacy__";
// Sentinel for the disabled hint shown when there are no profiles yet.
const HINT_VALUE = "__hint__";

/**
 * ModelProfileSelect — single dropdown for choosing a named Model profile.
 *
 * Replaces the old match-mode + pattern pair (see ModelSelection.js, still
 * used by the Models tab to edit a profile's own criteria) wherever a
 * *consumer* of profiles (ACP server, workspace auxiliary model) just needs
 * to pick one by name.
 *
 * Props:
 *   value       {string}   — currently selected profile name ("" = none)
 *   profiles    {Array}    — model profiles from config.models: {name, criteria, tags}
 *   legacyLabel {string?}  — when set, renders a disabled option (shown as
 *                            selected when value is "") describing a legacy
 *                            raw matchMode/pattern constraint that doesn't
 *                            map to any profile, so it isn't silently lost.
 *   onChange    {function} — called with the newly selected profile name ("" = none)
 */
export function ModelProfileSelect({
  value,
  profiles = [],
  legacyLabel,
  onChange,
}) {
  const hasLegacy = !!legacyLabel;
  const selectValue = value ? value : hasLegacy ? LEGACY_VALUE : "";

  const handleChange = (e) => {
    const v = e.target.value;
    if (v === LEGACY_VALUE || v === HINT_VALUE) return;
    onChange(v);
  };

  return html`
    <select
      value=${selectValue}
      onInput=${handleChange}
      class="select select-sm"
    >
      <option value="">-- None --</option>
      ${hasLegacy &&
      html`<option value=${LEGACY_VALUE} disabled selected>
        ${legacyLabel}
      </option>`}
      ${profiles.length === 0 &&
      html`<option value=${HINT_VALUE} disabled>
        (define profiles in the Models tab)
      </option>`}
      ${profiles.map(
        (p) => html`<option key=${p.name} value=${p.name}>${p.name}</option>`,
      )}
    </select>
  `;
}
