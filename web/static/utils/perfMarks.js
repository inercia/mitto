// Mitto Web Interface - UI Responsiveness Performance Marks (mitto-sus.1)
//
// Opt-in instrumentation for the UI responsiveness benchmark suite. Disabled
// by default and negligible-cost when disabled: every exported function
// short-circuits behind isPerfEnabled() before touching the Performance API,
// so this module has no effect on the production data path unless explicitly
// enabled.
//
// Enable via `?perf=1` in the page URL, or by setting `window.__mittoPerf =
// true` before the app bootstraps (e.g. a Playwright `page.addInitScript`).
//
// See docs/devel/ui-responsiveness-benchmarks.md for the seam catalogue,
// environmental controls, and budget table this instrumentation supports.

const MITTO_MARK_PREFIX = "mitto.";
const PERF_BUFFER_CAP = 4096;
const OBSERVED_ENTRY_TYPES = [
  "longtask",
  "event",
  "paint",
  "first-input",
  "mark",
  "measure",
];

let cachedEnabled = null;

/**
 * Whether perf instrumentation is enabled for this page load. Cached after
 * the first check so repeated calls (one per keystroke, per chunk, ...)
 * don't re-parse the URL.
 */
export function isPerfEnabled() {
  if (cachedEnabled !== null) return cachedEnabled;
  let enabled = false;
  try {
    enabled =
      window.__mittoPerf === true ||
      new URLSearchParams(window.location.search).get("perf") === "1";
  } catch {
    enabled = false;
  }
  cachedEnabled = enabled;
  return enabled;
}

/** Test-only: reset the cached enabled flag so specs can toggle it. */
export function _resetPerfEnabledCacheForTests() {
  cachedEnabled = null;
}

/**
 * Records a named `mitto.<name>` performance mark. No-op unless perf
 * instrumentation is enabled. Never throws — instrumentation must not break
 * the production data path.
 */
export function perfMark(name, detail) {
  if (!isPerfEnabled()) return;
  try {
    performance.mark(
      `${MITTO_MARK_PREFIX}${name}`,
      detail ? { detail } : undefined,
    );
  } catch {
    // Unsupported browser / detail shape — degrade silently.
  }
}

/**
 * Records a `mitto.<name>` measure between two previously-recorded marks
 * (also auto-prefixed with `mitto.`). No-op unless enabled; never throws
 * (e.g. a missing start mark after a mid-sequence reload must not break
 * the caller).
 */
export function perfMeasure(name, startMark, endMark) {
  if (!isPerfEnabled()) return;
  try {
    performance.measure(
      `${MITTO_MARK_PREFIX}${name}`,
      `${MITTO_MARK_PREFIX}${startMark}`,
      endMark ? `${MITTO_MARK_PREFIX}${endMark}` : undefined,
    );
  } catch {
    // Missing/cleared marks — degrade silently.
  }
}

/**
 * One-shot bootstrap: mounts a bounded PerformanceObserver that mirrors
 * longtask/event/paint/first-input/mark/measure entries onto
 * `window.__mittoPerfBuffer` (a capped ring buffer) for the Playwright
 * benchmark harness to drain. No-op unless perf instrumentation is enabled.
 * Idempotent — safe to call more than once (e.g. hot reload).
 */
export function installPerfBuffer() {
  if (!isPerfEnabled()) return;
  if (window.__mittoPerfBuffer) return;
  window.__mittoPerfBuffer = [];
  try {
    const observer = new PerformanceObserver((list) => {
      const buffer = window.__mittoPerfBuffer;
      if (!buffer) return;
      for (const entry of list.getEntries()) {
        buffer.push({
          name: entry.name,
          entryType: entry.entryType,
          startTime: entry.startTime,
          duration: entry.duration,
          // User Timing L3 detail (e.g. perfMark's { count } payload — see
          // sessionUpdateScheduler.js's "ws.chunk.applied" mark, mitto-sus.3).
          // Not all entry types carry detail; omit rather than serialize
          // `undefined` so consumers can rely on `"detail" in entry`.
          ...(entry.detail !== undefined ? { detail: entry.detail } : {}),
        });
      }
      if (buffer.length > PERF_BUFFER_CAP) {
        buffer.splice(0, buffer.length - PERF_BUFFER_CAP);
      }
    });
    observer.observe({ entryTypes: OBSERVED_ENTRY_TYPES, buffered: true });
    window.__mittoPerfObserver = observer;
  } catch {
    // PerformanceObserver (or one of the requested entry types) may be
    // unsupported — degrade silently rather than breaking app bootstrap.
  }
}
