// mitto-66r — pure helper for deriving the plan→implement→test→review phase
// progression of a per-bead loop from its label set.
//
// Label conventions (from config/prompts/builtin/beads-issue-loop-*.prompt.yaml):
//   feature: planned → implemented → tested → verified (terminal)
//   bug:     researched → reproduced → fixed (terminal)
//
// Given an issue_type and its labels array, this returns a phases[] list plus a
// small summary used by both the conversation-list card pill (SessionItem.js)
// and the issue-detail phase timeline (BeadsView.js). The helper is pure /
// framework-free so it can be unit-tested under jsdom / jest without importing
// window.preact.

// Ordered phase definitions. `label` is the beads label that marks the phase
// COMPLETE (the driver adds it after finishing the phase). `nextName` is the
// human-readable name of the NEXT step the driver would dispatch once this
// phase's label lands — that's what surfaces in the compact card pill.
// `tier` groups phases by preferredModel family used in the per-phase prompts
// (reasoning vs. coding) so the UI can color-tier them consistently.
const FEATURE_PHASES = [
  { label: "planned", nextName: "plan", displayName: "Plan", tier: "reasoning" },
  { label: "implemented", nextName: "implement", displayName: "Implement", tier: "coding" },
  { label: "tested", nextName: "test", displayName: "Test", tier: "coding" },
  { label: "verified", nextName: "review", displayName: "Review", tier: "reasoning" },
];

const BUG_PHASES = [
  { label: "researched", nextName: "investigate", displayName: "Investigate", tier: "reasoning" },
  { label: "reproduced", nextName: "reproduce", displayName: "Reproduce", tier: "coding" },
  { label: "fixed", nextName: "fix", displayName: "Fix", tier: "coding" },
];

// Tailwind class hints for each tier. Consumers may map these or ignore them
// and derive their own palette; the strings here are the canonical Mitto
// accents used by BeadsView.js (see `text-purple-300` for epic child badges,
// `text-mitto-accent`/`bg-mitto-accent/20` for standard highlight, and
// `text-mitto-success` for terminal/done).
export const PHASE_TIER_CLASSES = {
  reasoning: {
    text: "text-purple-300",
    bg: "bg-purple-500/20",
    border: "border-purple-500/40",
  },
  coding: {
    text: "text-mitto-accent",
    bg: "bg-mitto-accent/20",
    border: "border-mitto-accent/40",
  },
  terminal: {
    text: "text-mitto-success",
    bg: "bg-mitto-success/20",
    border: "border-mitto-success/40",
  },
};

function phasesForType(issueType) {
  if (issueType === "feature") return FEATURE_PHASES;
  if (issueType === "bug") return BUG_PHASES;
  return null;
}

/**
 * Derive the phase state for a feature/bug issue from its labels.
 *
 * Returns null for non-feature/non-bug issue types (task, epic, chore, etc.)
 * so callers can trivially null-check to hide the UI. For feature/bug issues
 * the shape is:
 *   {
 *     phases: [{ name, displayName, status, tier, tierClasses }],
 *     currentIndex: number,     // index into phases; equals phases.length when terminal
 *     currentLabel: string,     // "plan" | "implement" | ... | "done"
 *     currentDisplayName: string, // "Plan" | "Implement" | ... | "Done"
 *     currentTier: "reasoning" | "coding" | "terminal",
 *     isTerminal: boolean,
 *     kindLabel: string,        // "Feature" | "Bug"
 *   }
 *
 * Semantics:
 * - "done" phases are those whose completion label is present in labels[].
 *   The FIRST phase whose label is missing is the "current" phase (the next
 *   step the driver would dispatch); all phases after it are "upcoming".
 * - The current label from FEATURE_PHASES/BUG_PHASES `nextName` is the
 *   human-readable name of that NEXT step (e.g. "plan", "implement").
 * - Extra/unknown labels in `labels` are ignored. Order in `labels` is not
 *   significant; we scan for each phase label by presence.
 * - When ALL phase labels are present the issue is "terminal": currentIndex
 *   equals phases.length, currentTier === "terminal", isTerminal === true.
 *
 * @param {string} issueType
 * @param {string[]} labels
 * @returns {object|null}
 */
export function derivePhaseState(issueType, labels) {
  const phaseDefs = phasesForType(issueType);
  if (!phaseDefs) return null;

  const labelSet = new Set(Array.isArray(labels) ? labels : []);
  const kindLabel = issueType === "feature" ? "Feature" : "Bug";

  // First phase whose completion label is NOT yet present is the current one.
  let currentIndex = phaseDefs.findIndex((p) => !labelSet.has(p.label));
  const isTerminal = currentIndex === -1;
  if (isTerminal) currentIndex = phaseDefs.length;

  const phases = phaseDefs.map((p, i) => {
    let status;
    if (i < currentIndex) status = "done";
    else if (i === currentIndex) status = "current";
    else status = "upcoming";
    // Done phases render with the terminal (success) tier so the timeline
    // reads "green trail behind the current highlight". The current phase
    // keeps its native tier so the color signals which model class the next
    // dispatch will use. Upcoming phases fall back to their native tier as
    // a muted preview; consumers apply opacity to distinguish them.
    const tier = status === "done" ? "terminal" : p.tier;
    return {
      name: p.nextName,
      displayName: p.displayName,
      completionLabel: p.label,
      status,
      tier,
      tierClasses: PHASE_TIER_CLASSES[tier],
    };
  });

  if (isTerminal) {
    return {
      phases,
      currentIndex,
      currentLabel: "done",
      currentDisplayName: "Done",
      currentTier: "terminal",
      isTerminal: true,
      kindLabel,
    };
  }

  const cur = phaseDefs[currentIndex];
  return {
    phases,
    currentIndex,
    currentLabel: cur.nextName,
    currentDisplayName: cur.displayName,
    currentTier: cur.tier,
    isTerminal: false,
    kindLabel,
  };
}
