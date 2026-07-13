// Small pill/badge sub-components used across the Beads list, detail panel,
// and dependency list. Extracted from components/BeadsView.js in mitto-90f.3
// E-4. This module depends on the frontend runtime (window.preact + htm) so
// it lives under components/, not utils/. Pure-data constants (colors, labels)
// are imported from utils/beads.js — see mitto-90f.3 E-3.
//
// Rendered markup, class names and event shapes are preserved byte-for-byte
// from the original definitions so this move is behaviorally invisible.

const { html } = window.preact;

import {
  PRIORITY_LABELS,
  PRIORITY_COLORS,
  STATUS_COLORS,
  TYPE_COLORS,
} from "../../utils/beads.js";

// Generic pill used by all the other badges. Kept exported so consumers that
// need an ad-hoc pill (arbitrary text + Tailwind color class) don't have to
// re-declare the base class string.
export function badge(text, colorClass) {
  return html`<span
    class="badge badge-sm font-medium px-2.5 py-0.5 ${colorClass}"
    >${text}</span
  >`;
}

export function priorityBadge(p) {
  const n = typeof p === "number" ? p : 3;
  return badge(
    PRIORITY_LABELS[n] ?? String(p),
    PRIORITY_COLORS[n] ?? PRIORITY_COLORS[3],
  );
}

export function statusBadge(s) {
  const label = (s || "open").replace(/_/g, " ");
  return badge(
    label,
    STATUS_COLORS[s] ?? "bg-mitto-surface-4 text-mitto-text-strong",
  );
}

// Status badge for the (narrow) dependencies list: shows the full status label
// on normal screens and collapses to a single-letter abbreviation on small
// screens (see .beads-badge-abbr / .beads-badge-full in styles.css). The full
// label is kept in `title` for hover/accessibility.
export function depStatusBadge(s) {
  const label = (s || "open").replace(/_/g, " ");
  const colorClass =
    STATUS_COLORS[s] ?? "bg-mitto-surface-4 text-mitto-text-strong";
  return html`<span
    class="badge badge-sm font-medium px-2.5 py-0.5 ${colorClass}"
    title=${label}
  >
    <span class="beads-badge-abbr">${label.charAt(0)}</span
    ><span class="beads-badge-full">${label}</span>
  </span>`;
}

export function typeBadge(t) {
  return badge(t || "task", TYPE_COLORS[t] ?? TYPE_COLORS.task);
}
