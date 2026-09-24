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
 * Whether the mitto-sus.8 virtualization-spike `content-visibility`
 * prototype is enabled for this page load (separate from isPerfEnabled() —
 * this one gates a CSS/layout experiment, not just measurement).
 * Enable via `?perf-cv=1` in the page URL, or `window.__mittoPerfCV = true`
 * before bootstrap. See docs/devel/virtualization-spike.md.
 */
export function isPerfCVEnabled() {
  try {
    return (
      window.__mittoPerfCV === true ||
      new URLSearchParams(window.location.search).get("perf-cv") === "1"
    );
  } catch {
    return false;
  }
}

/**
 * One-shot bootstrap: toggles the `.mitto-perf-cv` class on the document
 * root when isPerfCVEnabled() — the scope styles.css's
 * `.mitto-perf-cv .mitto-msg-row` content-visibility rule targets. No-op
 * (and no DOM mutation at all) unless enabled, so this has zero effect on
 * the production data path when the flag is off.
 */
export function applyPerfCVFlag() {
  if (!isPerfCVEnabled()) return;
  try {
    document.documentElement.classList.add("mitto-perf-cv");
  } catch {
    // Non-browser environment (e.g. SSR/test) — degrade silently.
  }
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

// --- WKWebView leg dump (mitto-sus.2 A/B profile) ---------------------------
//
// There is no Playwright automation surface for the packaged native macOS
// app, so the WKWebView leg of the Chromium-vs-WebKit-vs-WKWebView A/B
// profile is driven by a documented manual playbook (see
// docs/devel/ui-responsiveness-benchmarks.md): an operator reproduces each
// deterministic fixture scenario by hand with `?perf=1` active, then calls
// `window.mittoPerfDump(scenario, path)` from the DevTools console to
// persist `window.__mittoPerfBuffer` in the same JSONL shape
// tests/ui/utils/perf.ts's writePerfSample() produces, so the existing
// scripts/perf-summary.mjs aggregator can consume it unchanged.

/**
 * Serializes `window.__mittoPerfBuffer` into one JSON line per entry
 * (matching writePerfSample's {scenario, metric, value, meta, ts} shape).
 * Shared by both perf-dump delivery legs (native file bind and the
 * POST /api/perf/dump endpoint, mitto-sus.12) so they never drift. Returns
 * an empty string for an empty/missing buffer; never throws.
 */
export function serializePerfBufferToJSONL(scenario) {
  const buffer = window.__mittoPerfBuffer || [];
  const lines = buffer.map((entry) =>
    JSON.stringify({
      scenario,
      metric: entry.name,
      value: entry.duration,
      meta: {
        entryType: entry.entryType,
        startTime: entry.startTime,
        ...(entry.detail !== undefined ? { detail: entry.detail } : {}),
      },
      ts: new Date().toISOString(),
    }),
  );
  return lines.length ? lines.join("\n") + "\n" : "";
}

/**
 * Writes `window.__mittoPerfBuffer` (serialized via
 * serializePerfBufferToJSONL) via the native `window.mittoDumpPerfBuffer`
 * bind (only present in the macOS app when launched with MITTO_PERF_DUMP=1
 * — see cmd/mitto-app/main.go's dumpPerfBufferToFile). No-op (returns
 * false) unless perf instrumentation is enabled or the native bind is
 * absent (e.g. running under Playwright/Chromium, or a normal app launch)
 * — never throws.
 */
export function dumpPerfBufferToFile(scenario, path) {
  if (!isPerfEnabled()) return false;
  if (typeof window.mittoDumpPerfBuffer !== "function") return false;
  try {
    window.mittoDumpPerfBuffer(path, serializePerfBufferToJSONL(scenario));
    return true;
  } catch {
    return false;
  }
}

/**
 * POSTs `window.__mittoPerfBuffer` (serialized via
 * serializePerfBufferToJSONL) to `POST /api/perf/dump?label=<label>&scenario=<scenario>`
 * — the reusable dev-only perf-dump endpoint (mitto-sus.12,
 * internal/web/handlers/perf_dump.go). Used by browser legs with no native
 * file-write bind, e.g. iOS Simulator Safari (inspectable via macOS
 * Safari's Develop menu). Resolves `false` (never throws) unless perf
 * instrumentation is enabled, the endpoint is disabled/unreachable
 * (MITTO_PERF_DUMP unset → 404), or the response is otherwise non-2xx —
 * an operator calling this from the console gets a boolean, not an
 * unhandled rejection.
 */
export async function dumpPerfBufferToServer(scenario, label) {
  if (!isPerfEnabled()) return false;
  try {
    const prefix = window.mittoApiPrefix || "";
    const qs = new URLSearchParams({ label, scenario }).toString();
    const response = await fetch(`${prefix}/api/perf/dump?${qs}`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/x-ndjson" },
      body: serializePerfBufferToJSONL(scenario),
    });
    return response.ok;
  } catch {
    return false;
  }
}

/**
 * One-shot bootstrap: exposes `dumpPerfBufferToFile` as `window.mittoPerfDump`
 * and `dumpPerfBufferToServer` as `window.mittoPerfDumpServer` so an
 * operator running a manual playbook (WKWebView or iOS Simulator Safari)
 * can call either from the DevTools/Web Inspector console. No-op unless
 * perf instrumentation is enabled.
 */
export function exposePerfDumpForConsole() {
  if (!isPerfEnabled()) return;
  window.mittoPerfDump = dumpPerfBufferToFile;
  window.mittoPerfDumpServer = dumpPerfBufferToServer;
}
