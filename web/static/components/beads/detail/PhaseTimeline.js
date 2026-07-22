// mitto-66r — PhaseTimeline
//
// Renders the plan→implement→test→review (feature) or investigate→reproduce→fix
// (bug) phase progression of a beads issue as a compact segmented pill strip.
// Consumed by the issue-detail panel (PanelBody.js) alongside the small pill
// rendered on conversation cards (SessionItem.js) — both derive their state
// from utils/phaseState.derivePhaseState so the label semantics stay in sync.
//
// Renders nothing (returns null) for non-feature/non-bug issue types so the
// caller can drop it in unconditionally.

const { html, Fragment } = window.preact;

import { derivePhaseState } from "../../../utils/phaseState.js";

// Per-phase pill class table. Done phases use the terminal (success) accent so
// the trail behind the current phase reads as "completed"; the current phase
// keeps its native tier (reasoning=purple, coding=mitto-accent) so the color
// signals which model class the next dispatch will use; upcoming phases fade
// to a muted neutral surface.
function pillClasses(status, tier) {
  if (status === "done") {
    return "bg-mitto-success/20 border-mitto-success/40 text-mitto-success";
  }
  if (status === "current") {
    if (tier === "reasoning") {
      return "bg-purple-500/25 border-purple-500/50 text-purple-800 dark:text-purple-200 font-semibold";
    }
    return "bg-mitto-accent/25 border-mitto-accent/50 text-mitto-accent font-semibold";
  }
  return "bg-mitto-surface-3/40 border-mitto-border/40 text-mitto-text-muted";
}

export function PhaseTimeline({ issueType, labels, status }) {
  const state = derivePhaseState(issueType, labels, status);
  if (!state) return null;

  return html`
    <${Fragment}>
      <div
        class="flex items-center gap-1.5 flex-wrap py-1"
        aria-label=${`${state.kindLabel} phase timeline: ${state.currentDisplayName}`}
      >
        <span class="text-xs text-mitto-text-muted mr-1"
          >${state.kindLabel} phase:</span
        >
        ${state.phases.map(
          (p, i) => html`
            <${Fragment}>
              ${i > 0 &&
              html`
                <span class="text-mitto-text-muted/60 text-xs" aria-hidden="true"
                  >›</span
                >
              `}
              <span
                class="badge badge-sm border ${pillClasses(p.status, p.tier)}"
                title=${`${p.displayName} — ${p.status}`}
                >${p.displayName}</span
              >
            </${Fragment}>
          `,
        )}
        ${state.isTerminal &&
        html`
          <span class="text-xs text-mitto-success ml-1" aria-hidden="true"
            >✓ done</span
          >
        `}
      </div>
    </${Fragment}>
  `;
}
