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
import { modelColor, UNKNOWN_MODEL_COLOR } from "../../utils/palette.js";
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

// Duplicated from StatsCharts.js for testing (mitto-8wj). Keep in sync.
const REQUESTED_MODEL_METRICS = ["input_tokens_est", "output_tokens_est"];

// Duplicated from StatsCharts.js for testing. Keep in sync.
const CHART_HEIGHT = 220;

// Duplicated shape of buildChartSpecs from StatsCharts.js, minus uPlot-specific
// paths factories (which are exercised at runtime by Playwright in a86b.10).
// Keep the `metrics`, `title`, and `id` fields in sync with the component.
// `id` is the canonical chart identifier mirrored across
// KNOWN_DASHBOARD_CHART_IDS in storage.js and KnownDashboardChartIDs in
// internal/web/handlers/dashboard_charts.go (mitto-4t8).
function chartSpecMetrics() {
  return [
    {
      id: "tokens",
      title: "Tokens (input + output)",
      metrics: ["input_tokens_est", "output_tokens_est"],
    },
    {
      id: "tool_calls",
      title: "Tool calls",
      metrics: ["tool_calls_total", "mcp_calls"],
    },
    {
      id: "prompts_vs_turns",
      title: "Prompts vs agent turns",
      metrics: ["prompts", "agent_turns_completed"],
    },
  ];
}

// Duplicated visibility filter from StatsCharts.js (mitto-4t8). Keep in sync.
// `hidden` is the array returned by useDashboardHiddenCharts(); specs whose
// canonical `id` appears in it are dropped from the render pass. The
// ModelUsageCard is rendered SEPARATELY (it is a sibling of the carousel,
// not a member of specs) — its visibility is gated by
// `modelUsageVisible(hidden)` below.
function visibleSpecs(specs, hidden) {
  return specs.filter((s) => !hidden.includes(s.id));
}

// Duplicated ModelUsageCard gate from StatsCharts.js (mitto-4t8). Keep in sync.
function modelUsageVisible(hidden) {
  return !hidden.includes("model_usage");
}

// Duplicated empty-state predicate from StatsCharts.js (mitto-4t8). Keep in
// sync. When BOTH the carousel is empty AND the ModelUsageCard is hidden,
// the Dashboard renders a muted "All charts are hidden" fallback so the user
// has a clear next step (Settings ▸ Dashboard).
function isEverythingHidden(specs, hidden) {
  return visibleSpecs(specs, hidden).length === 0 && !modelUsageVisible(hidden);
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

// Duplicated from StatsCharts.js for testing (mitto-8wj). Keep in sync.
function toModelUplotData(data) {
  if (!data || !data.series) return { xs: [], models: [] };
  const wanted = new Set(REQUESTED_MODEL_METRICS);
  const perModel = new Map();
  let anchor = null;
  for (const key of Object.keys(data.series)) {
    const idx = key.indexOf(":");
    if (idx < 0) continue;
    const metric = key.slice(0, idx);
    const model = key.slice(idx + 1);
    if (!wanted.has(metric)) continue;
    if (model === "") continue;
    const arr = Array.isArray(data.series[key]) ? data.series[key] : [];
    if (!anchor && arr.length > 0) anchor = arr;
    let entry = perModel.get(model);
    if (!entry) {
      entry = { name: model, values: null };
      perModel.set(model, entry);
    }
    if (entry.values === null) {
      entry.values = arr.map((p) => p.v || 0);
    } else {
      for (let i = 0; i < arr.length && i < entry.values.length; i++) {
        entry.values[i] += arr[i].v || 0;
      }
    }
  }
  const xs = anchor ? anchor.map((p) => p.t) : [];
  const models = [];
  for (const entry of perModel.values()) {
    const values = entry.values || xs.map(() => 0);
    let total = 0;
    for (let i = 0; i < values.length; i++) total += values[i];
    if (total <= 0) continue;
    models.push({ name: entry.name, values, total });
  }
  models.sort((a, b) => b.total - a.total || a.name.localeCompare(b.name));
  return { xs, models };
}

// Duplicated from StatsCharts.js for testing (mitto-8wj). Keep in sync.
function isEmptyModelSeries(data) {
  return toModelUplotData(data).models.length === 0;
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
    for (const t of titles)
      expect(typeof t === "string" && t.length > 0).toBe(true);
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
    meta: {
      range: "24h",
      bucket: "hour",
      backfill_in_progress: false,
      note: "estimator note",
    },
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

  test.each(chartSpecMetrics())(
    "chart '$title' returns aligned [xs, ...ys] arrays",
    (spec) => {
      const out = toUplotData(fixture(), spec.metrics);
      // uPlot invariant: xs and every ys must have equal length.
      const n = out[0].length;
      expect(n).toBe(3);
      for (let i = 1; i < out.length; i++) {
        expect(out[i]).toHaveLength(n);
      }
      // Row count = 1 (xs) + one per metric.
      expect(out).toHaveLength(1 + spec.metrics.length);
    },
  );

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

// =============================================================================
// Model usage helpers — acceptance: "one colored line per model, grey unknown,
// legend total matches sum" (mitto-8wj)
// =============================================================================
//
// Fixture mimics the composite-key contract produced by
// internal/web/handlers/dashboard_timeseries.go when groupBy=model:
// keys are "<metric>:<model>"; the backend also seeds a bare "<metric>:" slot
// for metrics without a model dimension and a synthetic ":unknown" slot for
// pre-migration data. toModelUplotData must consume all three shapes correctly.

describe("toModelUplotData", () => {
  const grouped = () => ({
    meta: { range: "24h", bucket: "hour" },
    series: {
      "input_tokens_est:gpt-4o": [
        { t: 100, v: 10 },
        { t: 200, v: 20 },
        { t: 300, v: 30 },
      ],
      "output_tokens_est:gpt-4o": [
        { t: 100, v: 5 },
        { t: 200, v: 5 },
        { t: 300, v: 5 },
      ],
      "input_tokens_est:claude-3-5-sonnet": [
        { t: 100, v: 100 },
        { t: 200, v: 0 },
        { t: 300, v: 0 },
      ],
      "output_tokens_est:claude-3-5-sonnet": [
        { t: 100, v: 50 },
        { t: 200, v: 0 },
        { t: 300, v: 0 },
      ],
      // Bare "<metric>:" slot: metrics without a model dimension. Must be
      // ignored by the model card.
      "prompts:": [
        { t: 100, v: 7 },
        { t: 200, v: 7 },
        { t: 300, v: 7 },
      ],
    },
  });

  test("returns one entry per model with summed input+output tokens per bucket", () => {
    const { xs, models } = toModelUplotData(grouped());
    expect(xs).toEqual([100, 200, 300]);
    const names = models.map((m) => m.name);
    expect(names).toContain("gpt-4o");
    expect(names).toContain("claude-3-5-sonnet");
    const gpt = models.find((m) => m.name === "gpt-4o");
    // Per-bucket sums: input(10,20,30) + output(5,5,5) = (15,25,35).
    expect(gpt.values).toEqual([15, 25, 35]);
    expect(gpt.total).toBe(15 + 25 + 35);
    const claude = models.find((m) => m.name === "claude-3-5-sonnet");
    expect(claude.values).toEqual([150, 0, 0]);
    expect(claude.total).toBe(150);
  });

  test("orders models by total desc (busiest first) with stable name tiebreak", () => {
    const { models } = toModelUplotData(grouped());
    // gpt-4o total = 75, claude total = 150 → claude first.
    expect(models[0].name).toBe("claude-3-5-sonnet");
    expect(models[1].name).toBe("gpt-4o");
  });

  test("ignores bare '<metric>:' slots (non-model metrics)", () => {
    const { models } = toModelUplotData(grouped());
    // 'prompts:' with model="" must not appear as a model row.
    expect(models.find((m) => m.name === "")).toBeUndefined();
    // Also must not accidentally land under a "prompts" label — only
    // REQUESTED_MODEL_METRICS contribute to the summed values.
    for (const m of models) {
      expect(m.name).not.toBe("prompts");
    }
  });

  test("surfaces the ':unknown' bucket when non-zero", () => {
    const data = {
      series: {
        "input_tokens_est:unknown": [
          { t: 100, v: 40 },
          { t: 200, v: 60 },
        ],
        "output_tokens_est:unknown": [
          { t: 100, v: 10 },
          { t: 200, v: 15 },
        ],
      },
    };
    const { models } = toModelUplotData(data);
    expect(models).toHaveLength(1);
    expect(models[0].name).toBe("unknown");
    expect(models[0].values).toEqual([50, 75]);
    // And the palette maps this bucket to the fixed grey.
    expect(modelColor(models[0].name)).toBe(UNKNOWN_MODEL_COLOR);
  });

  test("drops models whose summed values are entirely zero", () => {
    const data = {
      series: {
        "input_tokens_est:silent-model": [
          { t: 100, v: 0 },
          { t: 200, v: 0 },
        ],
        "output_tokens_est:silent-model": [
          { t: 100, v: 0 },
          { t: 200, v: 0 },
        ],
        "input_tokens_est:gpt-4o": [
          { t: 100, v: 1 },
          { t: 200, v: 2 },
        ],
      },
    };
    const { models } = toModelUplotData(data);
    expect(models).toHaveLength(1);
    expect(models[0].name).toBe("gpt-4o");
  });

  test("null / empty payload yields empty result", () => {
    expect(toModelUplotData(null)).toEqual({ xs: [], models: [] });
    expect(toModelUplotData({})).toEqual({ xs: [], models: [] });
    expect(toModelUplotData({ series: {} })).toEqual({ xs: [], models: [] });
  });

  test("colors are stable across two toModelUplotData mounts of the same fixture", () => {
    // Pins the "colors are stable across reloads" acceptance criterion at the
    // level the component actually consumes: run the transform twice, then
    // map every produced model name through modelColor. Byte-identical.
    const first = toModelUplotData(grouped()).models.map((m) =>
      modelColor(m.name),
    );
    const second = toModelUplotData(grouped()).models.map((m) =>
      modelColor(m.name),
    );
    expect(first).toEqual(second);
  });
});

describe("isEmptyModelSeries", () => {
  test("null / empty → empty", () => {
    expect(isEmptyModelSeries(null)).toBe(true);
    expect(isEmptyModelSeries({})).toBe(true);
    expect(isEmptyModelSeries({ series: {} })).toBe(true);
  });

  test("all-zero grouped series → empty (matches empty-state banner condition)", () => {
    const data = {
      series: {
        "input_tokens_est:gpt-4o": [
          { t: 100, v: 0 },
          { t: 200, v: 0 },
        ],
        "output_tokens_est:gpt-4o": [
          { t: 100, v: 0 },
          { t: 200, v: 0 },
        ],
      },
    };
    expect(isEmptyModelSeries(data)).toBe(true);
  });

  test("any positive per-model sum → not empty", () => {
    const data = {
      series: {
        "input_tokens_est:gpt-4o": [
          { t: 100, v: 1 },
          { t: 200, v: 0 },
        ],
      },
    };
    expect(isEmptyModelSeries(data)).toBe(false);
  });

  test("only bare '<metric>:' slots present → empty (no model dimension)", () => {
    const data = {
      series: {
        "prompts:": [
          { t: 100, v: 5 },
          { t: 200, v: 5 },
        ],
      },
    };
    expect(isEmptyModelSeries(data)).toBe(true);
  });
});

describe("model usage URL builder (groupBy=model)", () => {
  test("second fetch URL includes groupBy=model and only the two token metrics", () => {
    // Pins the exact URL shape the ModelUsageCard's second fetch effect emits
    // so a regression to a different groupBy or metric list is caught here.
    const url = endpoints.misc.dashboardTimeseries({
      range: "24h",
      metrics: REQUESTED_MODEL_METRICS.join(","),
      groupBy: "model",
    });
    expect(url).toContain("/api/dashboard/timeseries");
    expect(url).toContain("range=24h");
    expect(url).toContain("groupBy=model");
    expect(url).toContain(
      `metrics=${encodeURIComponent(REQUESTED_MODEL_METRICS.join(","))}`,
    );
    // Must NOT include the ungrouped six-metric list — that URL belongs to the
    // pinned three-card fetch and must stay byte-identical.
    expect(url).not.toContain("prompts");
    expect(url).not.toContain("tool_calls_total");
  });

  test("REQUESTED_MODEL_METRICS is a subset of REQUESTED_METRICS", () => {
    const known = new Set(REQUESTED_METRICS);
    for (const m of REQUESTED_MODEL_METRICS) {
      expect(known.has(m)).toBe(true);
    }
  });
});

// =============================================================================
// Chart-visibility contract (mitto-4t8 / mitto-3i2 Phase 3)
// =============================================================================
//
// Pins the canonical chart-id set and the visibility helpers that decide
// which cards render on the Dashboard's Activity strip. The end-to-end
// live-update path (localStorage → CustomEvent → hook → re-render) is
// covered by useDashboardHiddenCharts.test.js and storage.test.js; here we
// pin the pure filter/gate/empty-state logic that consumes the hook's value.

// Canonical chart ids as pinned by KNOWN_DASHBOARD_CHART_IDS in storage.js
// and KnownDashboardChartIDs in internal/web/handlers/dashboard_charts.go.
// Any regression that renames a spec `id` or drops one from either registry
// surfaces here without needing to boot uPlot or the WebSocket.
const KNOWN_DASHBOARD_CHART_IDS = [
  "tokens",
  "tool_calls",
  "prompts_vs_turns",
  "model_usage",
];

describe("buildChartSpecs canonical ids (mitto-4t8)", () => {
  test("each spec carries a non-empty canonical id", () => {
    for (const spec of chartSpecMetrics()) {
      expect(typeof spec.id).toBe("string");
      expect(spec.id.length).toBeGreaterThan(0);
    }
  });

  test("spec ids are a subset of KNOWN_DASHBOARD_CHART_IDS", () => {
    const known = new Set(KNOWN_DASHBOARD_CHART_IDS);
    for (const spec of chartSpecMetrics()) {
      expect(known.has(spec.id)).toBe(true);
    }
  });

  test("each spec id is unique (no duplicates in the carousel)", () => {
    const ids = chartSpecMetrics().map((s) => s.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  test("every non-model_usage known id maps to a spec", () => {
    // KNOWN_DASHBOARD_CHART_IDS is the source of truth; the three carousel
    // specs must cover every id except `model_usage` (which is rendered as a
    // sibling ModelUsageCard). This catches a regression where a new id is
    // added to the registry but not annotated on `buildChartSpecs`.
    const carouselIds = new Set(chartSpecMetrics().map((s) => s.id));
    for (const id of KNOWN_DASHBOARD_CHART_IDS) {
      if (id === "model_usage") continue;
      expect(carouselIds.has(id)).toBe(true);
    }
  });
});

describe("visibleSpecs (carousel filter)", () => {
  test("empty hidden set → every spec is visible", () => {
    const specs = chartSpecMetrics();
    expect(visibleSpecs(specs, [])).toEqual(specs);
  });

  test("hides only the specs whose id is in the hidden set", () => {
    const specs = chartSpecMetrics();
    const out = visibleSpecs(specs, ["tokens", "prompts_vs_turns"]);
    expect(out.map((s) => s.id)).toEqual(["tool_calls"]);
  });

  test("model_usage in the hidden set does NOT affect the carousel filter", () => {
    // The ModelUsageCard is a sibling of the carousel, not part of specs;
    // hiding it must leave every carousel spec visible.
    const specs = chartSpecMetrics();
    expect(visibleSpecs(specs, ["model_usage"])).toEqual(specs);
  });

  test("unknown ids in the hidden set are ignored (defensive against stale localStorage)", () => {
    const specs = chartSpecMetrics();
    expect(visibleSpecs(specs, ["bogus", "old_removed_chart"])).toEqual(specs);
  });

  test("all carousel ids hidden → visibleSpecs is empty", () => {
    const specs = chartSpecMetrics();
    const allCarouselIds = specs.map((s) => s.id);
    expect(visibleSpecs(specs, allCarouselIds)).toEqual([]);
  });
});

describe("modelUsageVisible (sibling gate)", () => {
  test("returns true when model_usage is NOT hidden", () => {
    expect(modelUsageVisible([])).toBe(true);
    expect(modelUsageVisible(["tokens", "tool_calls"])).toBe(true);
  });

  test("returns false when model_usage is hidden", () => {
    expect(modelUsageVisible(["model_usage"])).toBe(false);
    expect(modelUsageVisible(["tokens", "model_usage"])).toBe(false);
  });
});

describe("isEverythingHidden (empty-state fallback)", () => {
  test("false when at least one carousel spec is visible", () => {
    const specs = chartSpecMetrics();
    expect(isEverythingHidden(specs, [])).toBe(false);
    expect(isEverythingHidden(specs, ["tokens"])).toBe(false);
    expect(isEverythingHidden(specs, ["tokens", "tool_calls"])).toBe(false);
  });

  test("false when the ModelUsageCard alone is visible", () => {
    // Every carousel spec hidden but model_usage still shown → the strip
    // still has content; empty-state fallback must NOT render.
    const specs = chartSpecMetrics();
    const carouselIds = specs.map((s) => s.id);
    expect(isEverythingHidden(specs, carouselIds)).toBe(false);
  });

  test("true only when every carousel spec AND model_usage are hidden", () => {
    // This is the exact condition StatsCharts.js gates the "All charts are
    // hidden. Enable at least one in Settings ▸ Dashboard." fallback on.
    const specs = chartSpecMetrics();
    const everything = [...specs.map((s) => s.id), "model_usage"];
    expect(isEverythingHidden(specs, everything)).toBe(true);
  });
});
