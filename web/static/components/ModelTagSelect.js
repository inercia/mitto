// Mitto Web Interface - Model Tag Select Component
const { html } = window.preact;

// Sentinel for the disabled hint shown when no tags are defined on any profile.
const HINT_VALUE = "__hint__";

/**
 * ModelTagSelect — single dropdown for choosing a capability Tag (e.g. "Fast",
 * "Cheap") that any Model profile carrying that tag can satisfy.
 *
 * Mirrors ModelProfileSelect but for the tag axis. Used alongside
 * ModelProfileSelect as a mutually-exclusive way to pick an auxiliary model:
 * select a specific profile by name, or delegate to whichever profile matches
 * a requested tag.
 *
 * Props:
 *   value    {string}   — currently selected tag ("" = none)
 *   profiles {Array}    — model profiles from config.models: {name, criteria, tags}
 *   onChange {function} — called with the newly selected tag ("" = none)
 */
export function ModelTagSelect({ value, profiles = [], onChange }) {
  const handleChange = (e) => {
    const v = e.target.value;
    if (v === HINT_VALUE) return;
    onChange(v);
  };

  // Deduplicated, case-insensitively unique, sorted union of tags across all
  // profiles. Preserves the first-seen original casing for each tag.
  const seen = new Map(); // lowercased -> original
  for (const p of profiles) {
    const tags = Array.isArray(p && p.tags) ? p.tags : [];
    for (const t of tags) {
      if (typeof t !== "string") continue;
      const trimmed = t.trim();
      if (!trimmed) continue;
      const key = trimmed.toLowerCase();
      if (!seen.has(key)) seen.set(key, trimmed);
    }
  }
  const tags = Array.from(seen.values()).sort((a, b) =>
    a.localeCompare(b, undefined, { sensitivity: "base" }),
  );

  return html`
    <select
      value=${value || ""}
      onInput=${handleChange}
      class="select select-sm"
    >
      <option value="">-- None --</option>
      ${tags.length === 0 &&
      html`<option value=${HINT_VALUE} disabled>
        (no tags defined on any profile)
      </option>`}
      ${tags.map((t) => html`<option key=${t} value=${t}>${t}</option>`)}
    </select>
  `;
}
