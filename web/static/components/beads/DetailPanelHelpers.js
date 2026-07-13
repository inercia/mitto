// Small rendering helpers used across the Beads detail side panel. Extracted
// from components/BeadsView.js in mitto-90f.3 E-6. These helpers depend on the
// frontend runtime (window.preact + htm) so they live under components/, not
// utils/ (utils/beads.js is intentionally framework-free — see the header
// comment in that file).
//
// Rendered markup and class names are preserved byte-for-byte from the
// original definitions so this move is behaviorally invisible.

const { html } = window.preact;

// labelValue renders a small two-line label/value block used throughout the
// detail side panel (Owner, Created, Updated, etc.). Returns null for empty
// values so callers can concatenate results without conditional wrappers.
export function labelValue(label, value) {
  if (value === null || value === undefined || value === "") return null;
  return html`
    <div>
      <div class="text-xs text-mitto-text-secondary mb-0.5">${label}</div>
      <div class="text-sm text-mitto-text wrap-break-word">${value}</div>
    </div>
  `;
}
