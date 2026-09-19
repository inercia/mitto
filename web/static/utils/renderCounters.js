// Mitto Web Interface - Dev-only render-count instrumentation (mitto-sus.7)
//
// Counts how many times each named render domain's component function body
// runs, so the render-isolation regression specs (tests/ui/specs/perf/) can
// assert that a background-session chunk, keepalive ack, or active-stream
// tick does not force unrelated domains (e.g. the sidebar) to reconcile.
// See docs/devel/frontend-render-domains.md for the domain catalogue.
//
// No-op unless perf instrumentation is enabled (see utils/perfMarks.js's
// isPerfEnabled) -- production bundles pay no measurable cost from this
// module: bumpRender checks isPerfEnabled() before touching the Map or
// window.__mittoRenderCounts, mirroring perfMarks.js's own opt-in gate.

import { isPerfEnabled } from "./perfMarks.js";

/** @type {Map<string, number>} */
const counts = new Map();

/**
 * Increments the render count for a named domain (e.g. "App",
 * "SessionList", "MessageList", "ChatInput", "ToastContainer"). No-op unless
 * perf instrumentation is enabled. Never throws -- instrumentation must not
 * break the production render path.
 */
export function bumpRender(regionName) {
  if (!isPerfEnabled()) return;
  try {
    counts.set(regionName, (counts.get(regionName) || 0) + 1);
    if (typeof window !== "undefined") {
      window.__mittoRenderCounts = Object.fromEntries(counts);
    }
  } catch {
    // Non-browser environment or a misbehaving Map -- degrade silently.
  }
}

/**
 * Returns a plain-object snapshot of all render counts recorded so far,
 * keyed by region name. Intended for the Playwright render-count specs to
 * read after exercising a scenario (mirrors perfMarks.js's
 * window.__mittoPerfBuffer drain pattern).
 */
export function getRenderCounts() {
  return Object.fromEntries(counts);
}

/** Resets all render counts to zero. Test-only / scenario-boundary use. */
export function resetRenderCounts() {
  counts.clear();
  if (typeof window !== "undefined") {
    window.__mittoRenderCounts = {};
  }
}

/**
 * One-shot bootstrap (mitto-b1k): exposes resetRenderCounts() on
 * `window.__mittoResetRenderCounts`, mirroring how `installPerfBuffer()`
 * (utils/perfMarks.js) exposes `window.__mittoPerfBuffer` -- called once
 * from app.js at module scope. No-op unless perf instrumentation is
 * enabled; lets a before/after render-isolation Playwright spec reset
 * counters mid-run (e.g. after initial mount has settled, before
 * exercising the scenario under measurement) without a full page reload.
 * Idempotent -- safe to call more than once (e.g. hot reload).
 */
export function installRenderCountsReset() {
  if (!isPerfEnabled()) return;
  if (typeof window === "undefined") return;
  window.__mittoResetRenderCounts = resetRenderCounts;
}
