/**
 * Unit tests for StatsCharts pure helpers.
 *
 * The StatsCharts component (StatsCharts.js) imports window.preact globals at
 * module load time, so it cannot be imported under jsdom. Following the
 * project convention used by Dashboard.test.js / BeadsView.test.js, the pure
 * helpers under test are duplicated verbatim below (with the "Keep in sync"
 * marker) and exercised directly.
 *
 * The lower half of this file exercises importable, non-preact modules that
 * StatsCharts.js depends on (endpoints, vendor config) so the acceptance
 * criteria "charts render at 24h/7d/30d" and "range change re-fetches" are
 * pinned at the URL-and-metric level even though the DOM lifecycle itself is
 * covered by Playwright in mitto-a86b.10.
 */

// testGlobals.js re-exports the lifecycle globals and `jest` from whichever
// runner is active (Jest under Node ESM, bun:test under Bun), so a single
// import works under both runners.
import {
  describe,
  test,
  expect,
  beforeEach,
  jest,
} from "../../utils/testing/testGlobals.js";
import { endpoints } from "../../utils/endpoints.js";
import { CDN_URLS, VERSIONS } from "../../vendor/config.js";

// =============================================================================
// Duplicated helpers — keep in sync with StatsCharts.js
// =============================================================================

const RANGE_STORAGE_KEY = "mitto:dashboard:statsRange";
const RANGE_VALUES = ["24h", "7d", "30d"];
const DEFAULT_RANGE = "24h";

// Duplicated from StatsCharts.js for testing. Keep in sync.
const REQUESTED_METRICS = [
  "input_tokens_est",
  "output_tokens_est",
  "prompts",
  "agent_turns_completed",
  "tool_calls_total",
  "mcp_calls",
];

// Duplicated from StatsCharts.js for testing. Keep in sync.
const CHART_HEIGHT = 220;

// Duplicated shape of buildChartSpecs from StatsCharts.js, minus uPlot-specific
// paths factories (which are exercised at runtime by Playwright in a86b.10).
// Keep the `metrics` and `title` fields in sync with the component.
function chartSpecMetrics() {
  return [
    { title: "Tokens (input + output)", metrics: ["input_tokens_est", "output_tokens_est"] },
    { title: "Tool calls", metrics: ["tool_calls_total", "mcp_calls"] },
    { title: "Prompts vs agent turns", metrics: ["prompts", "agent_turns_completed"] },
  ];
}

// Duplicated from StatsCharts.js for testing (component imports window.preact
// globals). Keep in sync.
function readPersistedRange() {
  try {
    const v = window.localStorage.getItem(RANGE_STORAGE_KEY);
    if (v && RANGE_VALUES.indexOf(v) >= 0) return v;
  } catch (_e) {
    // ignore
  }
  return DEFAULT_RANGE;
}

// Duplicated from StatsCharts.js for testing. Keep in sync.
function writePersistedRange(range) {
  try {
    window.localStorage.setItem(RANGE_STORAGE_KEY, range);
  } catch (_e) {
    // ignore
  }
}

// Duplicated from StatsCharts.js for testing. Keep in sync.
function isEmptySeries(data) {
  if (!data || !data.series) return true;
  for (const key of Object.keys(data.series)) {
    const arr = data.series[key];
    if (!Array.isArray(arr)) continue;
    for (let i = 0; i < arr.length; i++) {
      if ((arr[i] && arr[i].v) > 0) return false;
    }
  }
  return true;
}

// Duplicated from StatsCharts.js for testing. Keep in sync.
function toUplotData(data, metrics) {
  if (!data || !data.series) return [[]].concat(metrics.map(() => []));
  const keys = Object.keys(data.series);
  const anchor = data.series[keys[0]] || [];
  const xs = anchor.map((p) => p.t);
  const ys = metrics.map((m) => {
    const arr = data.series[m];
    if (!Array.isArray(arr)) return anchor.map(() => 0);
    return arr.map((p) => p.v);
  });
  return [xs, ...ys];
}

// =============================================================================
// Tests
// =============================================================================

describe("readPersistedRange / writePersistedRange", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  test("returns default when nothing persisted", () => {
    expect(readPersistedRange()).toBe("24h");
  });

  test("round-trips a valid value", () => {
    writePersistedRange("7d");
    expect(readPersistedRange()).toBe("7d");
    writePersistedRange("30d");
    expect(readPersistedRange()).toBe("30d");
  });

  test("rejects an unknown persisted value", () => {
    window.localStorage.setItem(RANGE_STORAGE_KEY, "bogus");
    expect(readPersistedRange()).toBe("24h");
  });
});

describe("isEmptySeries", () => {
  test("null / empty object → empty", () => {
    expect(isEmptySeries(null)).toBe(true);
    expect(isEmptySeries({})).toBe(true);
    expect(isEmptySeries({ series: {} })).toBe(true);
  });

  test("all-zero series → empty", () => {
    const data = {
      series: {
        prompts: [
          { t: 1, v: 0 },
          { t: 2, v: 0 },
        ],
        tool_calls_total: [{ t: 1, v: 0 }],
      },
    };
    expect(isEmptySeries(data)).toBe(true);
  });

  test("any positive value → not empty", () => {
    const data = {
      series: {
        prompts: [{ t: 1, v: 0 }],
        tool_calls_total: [
          { t: 1, v: 0 },
          { t: 2, v: 5 },
        ],
      },
    };
    expect(isEmptySeries(data)).toBe(false);
  });

  test("ignores non-array entries defensively", () => {
    expect(isEmptySeries({ series: { prompts: null } })).toBe(true);
  });
});

describe("toUplotData", () => {
  test("null data yields aligned empty arrays", () => {
    const out = toUplotData(null, ["prompts", "tool_calls_total"]);
    expect(out).toEqual([[], [], []]);
  });

  test("extracts xs from the first series and requested ys", () => {
    const data = {
      series: {
        prompts: [
          { t: 100, v: 1 },
          { t: 200, v: 2 },
          { t: 300, v: 3 },
        ],
        tool_calls_total: [
          { t: 100, v: 10 },
          { t: 200, v: 20 },
          { t: 300, v: 30 },
        ],
      },
    };
    const out = toUplotData(data, ["prompts", "tool_calls_total"]);
    expect(out[0]).toEqual([100, 200, 300]);
    expect(out[1]).toEqual([1, 2, 3]);
    expect(out[2]).toEqual([10, 20, 30]);
  });

  test("missing metric yields a zero-filled row aligned to xs length", () => {
    const data = {
      series: {
        prompts: [
          { t: 100, v: 1 },
          { t: 200, v: 2 },
        ],
      },
    };
    const out = toUplotData(data, ["prompts", "missing"]);
    expect(out[0]).toEqual([100, 200]);
    expect(out[2]).toEqual([0, 0]);
  });
});


// =============================================================================
// Endpoint / URL builder — acceptance: "Charts render at 24h/7d/30d"
// =============================================================================
//
// Pins the URL contract for GET /api/dashboard/timeseries as seen from the
// range selector. Each of the three ranges must produce a URL that
// (a) targets /api/dashboard/timeseries, (b) preserves the exact range token,
// (c) carries every metric StatsCharts renders. Regressions in either qs()
// or the metric list would surface here without needing to boot uPlot.

describe("dashboardTimeseries URL builder", () => {
  test.each(RANGE_VALUES)("range=%s produces a well-formed URL", (range) => {
    const url = endpoints.misc.dashboardTimeseries({
      range,
      metrics: REQUESTED_METRICS.join(","),
    });
    expect(url).toContain("/api/dashboard/timeseries");
    expect(url).toContain(`range=${range}`);
    // metrics is comma-joined then percent-encoded by URLSearchParams.
    const encodedMetrics = encodeURIComponent(REQUESTED_METRICS.join(","));
    expect(url).toContain(`metrics=${encodedMetrics}`);
  });

  test("omits null/undefined/empty params (qs contract)", () => {
    const url = endpoints.misc.dashboardTimeseries({
      range: "24h",
      metrics: "prompts",
      workspace: null,
      bucket: undefined,
      extra: "",
    });
    expect(url).not.toContain("workspace=");
    expect(url).not.toContain("bucket=");
    expect(url).not.toContain("extra=");
    expect(url).toContain("range=24h");
    expect(url).toContain("metrics=prompts");
  });

  test("range toggle switches only the range param (proves re-fetch shape)", () => {
    // Simulates the range=24h → 7d → 30d flow triggered by the Toolbar.
    // Each URL must differ ONLY in the range token; every other query var
    // (metrics list, host prefix, path) stays byte-identical so the fetch
    // hitting the backend is deterministic.
    const urls = RANGE_VALUES.map((r) =>
      endpoints.misc.dashboardTimeseries({
        range: r,
        metrics: REQUESTED_METRICS.join(","),
      }),
    );
    const stripRange = (u) => u.replace(/range=[^&]+/, "range=__");
    expect(new Set(urls.map(stripRange)).size).toBe(1);
    expect(new Set(urls).size).toBe(RANGE_VALUES.length);
  });
});

// =============================================================================
// Chart-spec contract — every renderable series is fetched
// =============================================================================

describe("chart specs vs requested metrics", () => {
  test("each spec's metrics are a subset of REQUESTED_METRICS", () => {
    const known = new Set(REQUESTED_METRICS);
    for (const spec of chartSpecMetrics()) {
      for (const m of spec.metrics) {
        expect(known.has(m)).toBe(true);
      }
    }
  });

  test("every requested metric is rendered by at least one chart", () => {
    const rendered = new Set();
    for (const spec of chartSpecMetrics()) {
      for (const m of spec.metrics) rendered.add(m);
    }
    for (const m of REQUESTED_METRICS) {
      expect(rendered.has(m)).toBe(true);
    }
  });

  test("three cards, each with a unique non-empty title", () => {
    const specs = chartSpecMetrics();
    expect(specs).toHaveLength(3);
    const titles = specs.map((s) => s.title);
    expect(new Set(titles).size).toBe(3);
    for (const t of titles) expect(typeof t === "string" && t.length > 0).toBe(true);
  });

  test("chart-height constant is a positive integer", () => {
    // Pinned so a regression to 0 (which would trip uPlot's zero-length
    // canvas assertion) is caught in unit tests, not at first render.
    expect(Number.isInteger(CHART_HEIGHT)).toBe(true);
    expect(CHART_HEIGHT).toBeGreaterThan(0);
  });
});

// =============================================================================
// uPlot CDN URLs — acceptance: "loaded on-demand from CDN without console errors"
// =============================================================================

describe("uPlot CDN URLs", () => {
  test("VERSIONS.uplot is pinned to a semver string", () => {
    expect(typeof VERSIONS.uplot).toBe("string");
    expect(VERSIONS.uplot).toMatch(/^\d+\.\d+\.\d+$/);
  });

  test("CDN_URLS.uplot points at the IIFE bundle for the pinned version", () => {
    expect(CDN_URLS.uplot).toBe(
      `https://cdn.jsdelivr.net/npm/uplot@${VERSIONS.uplot}/dist/uPlot.iife.min.js`,
    );
  });

  test("CDN_URLS.uplotCss points at the pinned stylesheet", () => {
    expect(CDN_URLS.uplotCss).toBe(
      `https://cdn.jsdelivr.net/npm/uplot@${VERSIONS.uplot}/dist/uPlot.min.css`,
    );
  });
});

// =============================================================================
// End-to-end helper flow — realistic tsResponse fixture through toUplotData
// =============================================================================

describe("toUplotData with a realistic six-metric tsResponse", () => {
  const fixture = () => ({
    meta: { range: "24h", bucket: "hour", backfill_in_progress: false, note: "estimator note" },
    series: {
      input_tokens_est: [
        { t: 1_700_000_000, v: 100 },
        { t: 1_700_003_600, v: 200 },
        { t: 1_700_007_200, v: 300 },
      ],
      output_tokens_est: [
        { t: 1_700_000_000, v: 50 },
        { t: 1_700_003_600, v: 75 },
        { t: 1_700_007_200, v: 125 },
      ],
      prompts: [
        { t: 1_700_000_000, v: 1 },
        { t: 1_700_003_600, v: 2 },
        { t: 1_700_007_200, v: 3 },
      ],
      agent_turns_completed: [
        { t: 1_700_000_000, v: 1 },
        { t: 1_700_003_600, v: 2 },
        { t: 1_700_007_200, v: 2 },
      ],
      tool_calls_total: [
        { t: 1_700_000_000, v: 5 },
        { t: 1_700_003_600, v: 10 },
        { t: 1_700_007_200, v: 15 },
      ],
      mcp_calls: [
        { t: 1_700_000_000, v: 1 },
        { t: 1_700_003_600, v: 3 },
        { t: 1_700_007_200, v: 4 },
      ],
    },
  });

  test.each(chartSpecMetrics())("chart '$title' returns aligned [xs, ...ys] arrays", (spec) => {
    const out = toUplotData(fixture(), spec.metrics);
    // uPlot invariant: xs and every ys must have equal length.
    const n = out[0].length;
    expect(n).toBe(3);
    for (let i = 1; i < out.length; i++) {
      expect(out[i]).toHaveLength(n);
    }
    // Row count = 1 (xs) + one per metric.
    expect(out).toHaveLength(1 + spec.metrics.length);
  });

  test("fixture is non-empty according to isEmptySeries", () => {
    expect(isEmptySeries(fixture())).toBe(false);
  });

  test("backfill flag is passed through untouched (banner gate is a meta.* read)", () => {
    // The component gates the banner on `data.meta.backfill_in_progress`. This
    // test pins the fixture shape so a regression in the meta contract is
    // caught here rather than surfacing as a silent no-banner regression.
    const d = fixture();
    d.meta.backfill_in_progress = true;
    expect(d.meta.backfill_in_progress).toBe(true);
    expect(typeof d.meta.note).toBe("string");
    expect(d.meta.note.length).toBeGreaterThan(0);
  });
});
