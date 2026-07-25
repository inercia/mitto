// Mitto Web Interface - Dashboard timeseries charts (mitto-a86b.8).
// Three uPlot charts (tokens, tool calls, prompts) driven by
// GET /api/dashboard/timeseries with a 24h/7d/30d range selector. uPlot is
// loaded on-demand from the CDN (mirrors the mermaid loader in
// preact-loader.js); charts silently degrade when the CDN is unreachable.
const { html, useEffect, useMemo, useRef, useState } = window.preact;

import { authFetch } from "../../utils/csrf.js";
import { endpoints } from "../../utils/endpoints.js";
import { modelColor, UNKNOWN_MODEL_NAME } from "../../utils/palette.js";
import { CDN_URLS } from "../../vendor/config.js";
import { useDashboardHiddenCharts } from "../../hooks/useDashboardHiddenCharts.js";
import { Toolbar } from "../Toolbar.js";

const RANGE_STORAGE_KEY = "mitto:dashboard:statsRange";
const RANGE_VALUES = ["24h", "7d", "30d"];
const DEFAULT_RANGE = "24h";

// Metric list mirrors internal/web/handlers/dashboard_timeseries.go v1MetricSet
// order. We only request the six the UI actually uses so payloads are lean.
const REQUESTED_METRICS = [
  "input_tokens_est",
  "output_tokens_est",
  "prompts",
  "agent_turns_completed",
  "tool_calls_total",
  "mcp_calls",
];

// Metrics summed per model for the "Model usage" card. Kept in sync with the
// composite series keys "<metric>:<model>" produced by
// internal/web/handlers/dashboard_timeseries.go when groupBy=model.
export const REQUESTED_MODEL_METRICS = ["input_tokens_est", "output_tokens_est"];

// Chart height in px (fixed so narrow viewports do not collapse). Kept as a
// number so uPlot can size its canvas directly.
const CHART_HEIGHT = 140;

// --- Pure helpers (exported for testing) -----------------------------------

/** Return the persisted range from localStorage, or DEFAULT_RANGE. */
export function readPersistedRange() {
  try {
    const v = window.localStorage.getItem(RANGE_STORAGE_KEY);
    if (v && RANGE_VALUES.indexOf(v) >= 0) return v;
  } catch (_e) {
    // localStorage may throw in private mode / storage-disabled contexts.
  }
  return DEFAULT_RANGE;
}

/** Persist range; failure is silent (matches other storage helpers). */
export function writePersistedRange(range) {
  try {
    window.localStorage.setItem(RANGE_STORAGE_KEY, range);
  } catch (_e) {
    // ignore
  }
}

/** True when every requested series in `data.series` has only zero values. */
export function isEmptySeries(data) {
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

/**
 * Convert a tsResponse to the [xs, ...ys] arrays uPlot expects.
 * @param {object} data - tsResponse from GET /api/dashboard/timeseries.
 * @param {string[]} metrics - ordered list of series names to extract.
 * @returns {number[][]} [xs, ys1, ys2, ...] arrays of equal length. Missing
 *   series yield all-zero rows so uPlot's alignment invariant holds.
 */
export function toUplotData(data, metrics) {
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

/**
 * Convert a grouped-by-model tsResponse to per-model summed-token rows.
 * The backend emits composite keys "<metric>:<model>" plus a synthetic
 * ":unknown" slot for pre-migration data and a bare "<metric>:" slot for
 * metrics without a model dimension. We ignore the bare-key slots here
 * (this chart only requests per-model metrics) and sum input+output tokens
 * per bucket per model on the shared xs from the first grouped series.
 * @param {object} data - tsResponse from /api/dashboard/timeseries?groupBy=model.
 * @returns {{xs: number[], models: Array<{name: string, values: number[], total: number}>}}
 */
export function toModelUplotData(data) {
  if (!data || !data.series) return { xs: [], models: [] };
  const wanted = new Set(REQUESTED_MODEL_METRICS);
  // Collect the per-model buckets, keeping the first non-empty series as the
  // xs anchor so all model rows align even when a model appears only under a
  // subset of the requested metrics.
  const perModel = new Map();
  let anchor = null;
  for (const key of Object.keys(data.series)) {
    const idx = key.indexOf(":");
    if (idx < 0) continue;
    const metric = key.slice(0, idx);
    const model = key.slice(idx + 1);
    if (!wanted.has(metric)) continue;
    if (model === "") continue; // bare "<metric>:" slot — skip.
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
    if (total <= 0) continue; // drop all-zero rows (empty models).
    models.push({ name: entry.name, values, total });
  }
  // Stable order: highest total first so the legend leads with the busiest
  // model. Ties fall back to name for deterministic ordering across renders.
  models.sort((a, b) => b.total - a.total || a.name.localeCompare(b.name));
  return { xs, models };
}

/** True when the grouped payload contains no non-zero model rows. */
export function isEmptyModelSeries(data) {
  const { models } = toModelUplotData(data);
  return models.length === 0;
}

// --- uPlot CDN loader ------------------------------------------------------

let uplotLoadPromise = null;

/** Load uPlot (CSS + IIFE) from the CDN once; resolves to window.uPlot. */
function loadUplot() {
  if (typeof window !== "undefined" && window.uPlot) return Promise.resolve(window.uPlot);
  if (uplotLoadPromise) return uplotLoadPromise;
  uplotLoadPromise = new Promise((resolve, reject) => {
    if (!document.querySelector('link[data-mitto-uplot]')) {
      const link = document.createElement("link");
      link.rel = "stylesheet";
      link.href = CDN_URLS.uplotCss;
      link.setAttribute("data-mitto-uplot", "1");
      document.head.appendChild(link);
    }
    const script = document.createElement("script");
    script.src = CDN_URLS.uplot;
    script.async = true;
    script.onload = () => resolve(window.uPlot);
    script.onerror = (err) => {
      uplotLoadPromise = null;
      reject(err);
    };
    document.head.appendChild(script);
  });
  return uplotLoadPromise;
}


// --- Chart specs -----------------------------------------------------------

// Read a CSS custom property from :root, with a fallback. uPlot draws on a
// canvas so it cannot inherit CSS colors — we must resolve theme vars to
// concrete strings and re-read them on theme flips (see MutationObserver
// in ChartCard). Function-form so uPlot re-invokes on every redraw.
function cssVar(name, fallback) {
  if (typeof document === "undefined") return fallback;
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}
const axisStroke = () => cssVar("--mitto-text-muted", "#71717a");
const gridStroke = () => cssVar("--mitto-border-1", "#e4e4e7");

// Each spec describes one card: title, the tsResponse metric keys it consumes
// in order, and a factory that returns the uPlot opts (excluding width/height,
// which are supplied by the component). Keeping the specs declarative keeps
// the render loop below uniform and makes future chart additions cheap.
function buildChartSpecs(u) {
  // Compact axis sizes (uPlot defaults are 50/50 which eats ~70% of a 140px
  // chart, leaving the plot area a stubby band at the bottom of the card).
  // xAxis: 32px accommodates the tick line + gap + label baseline for the
  // HH:MM tick labels (24px was cropping the bottom of glyphs like "12pm"
  // against the card's overflow:hidden edge). yAxis: 44px fits 5-digit
  // numbers like "20,000" without truncation. stroke/grid/ticks are set to
  // function-form theme colors so the labels ("12am", "20,000") stay legible
  // against both light and dark surfaces (was defaulting to uPlot's #000).
  const xAxis = {
    space: 60,
    size: 32,
    stroke: axisStroke,
    grid: { stroke: gridStroke, width: 1 },
    ticks: { stroke: gridStroke, width: 1, size: 5 },
  };
  const yAxis = {
    size: 44,
    stroke: axisStroke,
    grid: { stroke: gridStroke, width: 1 },
    ticks: { stroke: gridStroke, width: 1, size: 5 },
  };
  const stroke = (v) => v;
  // Chart is compact: kill the uPlot legend (the card title already names the
  // chart), and use tight padding so the plot area fills the container top-to-
  // bottom instead of leaving a large empty gutter above the y-axis labels.
  // padding is [top, right, bottom, left] in px.
  const commonOpts = (w, h) => ({
    width: w,
    height: h,
    legend: { show: false },
    padding: [6, 8, 0, 0],
  });
  // yScale computes a dynamic y-axis range from the actual data so a single
  // spike on an otherwise-zero series doesn't glue the line to the bottom of
  // the plot area. uPlot passes us (dataMin, dataMax) as fallbacks; we widen
  // by ~8% headroom top and (when there's a positive floor) a small pad below.
  // Kept as a bare object so uPlot's own range fn receives it and re-evaluates
  // on every setData / dataset change — no hardcoded ceilings.
  const yScale = {
    range: (_up, dataMin, dataMax) => {
      if (!isFinite(dataMin) || !isFinite(dataMax)) return [0, 1];
      if (dataMin === dataMax) {
        // Flat series: center it with 1-unit padding (or 10% if large).
        const pad = Math.max(1, Math.abs(dataMax) * 0.1);
        return [dataMin - pad, dataMax + pad];
      }
      const span = dataMax - dataMin;
      const topPad = span * 0.08;
      // Anchor at 0 when data is non-negative and min is already near zero
      // (typical for counter series like tokens/tool-calls); otherwise zoom to
      // [dataMin - small pad, dataMax + top pad] so the line uses the full
      // vertical band.
      if (dataMin >= 0 && dataMin <= span * 0.05) {
        return [0, dataMax + topPad];
      }
      const bottomPad = span * 0.05;
      return [dataMin - bottomPad, dataMax + topPad];
    },
  };
  // `id` is the canonical chart identifier mirrored across the frontend +
  // backend registries (KNOWN_DASHBOARD_CHART_IDS in storage.js and
  // KnownDashboardChartIDs in internal/web/handlers/dashboard_charts.go).
  // It is what the Settings ▸ Dashboard tab writes into
  // `dashboard_hidden_charts`, and what StatsCharts filters `visibleSpecs`
  // against. Never rename without updating both registries in lockstep.
  return [
    {
      id: "tokens",
      title: "Tokens (input + output)",
      metrics: ["input_tokens_est", "output_tokens_est"],
      opts: (w, h) => ({
        ...commonOpts(w, h),
        scales: { x: { time: true }, y: yScale },
        axes: [xAxis, yAxis],
        series: [
          { label: "time" },
          { label: "input", stroke: stroke("#38bdf8"), fill: "rgba(56,189,248,0.15)" },
          { label: "output", stroke: stroke("#a78bfa"), fill: "rgba(167,139,250,0.15)" },
        ],
      }),
    },
    {
      id: "tool_calls",
      title: "Tool calls",
      metrics: ["tool_calls_total", "mcp_calls"],
      opts: (w, h) => ({
        ...commonOpts(w, h),
        scales: { x: { time: true }, y: yScale },
        axes: [xAxis, yAxis],
        series: [
          { label: "time" },
          {
            label: "all tools",
            stroke: stroke("#22c55e"),
            paths: u && u.paths && u.paths.bars ? u.paths.bars({ size: [0.7, 40] }) : undefined,
          },
          { label: "mcp", stroke: stroke("#f59e0b") },
        ],
      }),
    },
    {
      id: "prompts_vs_turns",
      title: "Prompts vs agent turns",
      metrics: ["prompts", "agent_turns_completed"],
      opts: (w, h) => ({
        ...commonOpts(w, h),
        scales: { x: { time: true }, y: yScale },
        axes: [xAxis, yAxis],
        series: [
          { label: "time" },
          {
            label: "prompts",
            stroke: stroke("#0ea5e9"),
            paths: u && u.paths && u.paths.bars ? u.paths.bars({ size: [0.5, 30] }) : undefined,
          },
          {
            label: "agent turns",
            stroke: stroke("#f472b6"),
            paths: u && u.paths && u.paths.bars ? u.paths.bars({ size: [0.5, 30] }) : undefined,
          },
        ],
      }),
    },
  ];
}

// --- Single-chart card component -------------------------------------------

function ChartCard({ title, metrics, optsFor, data, uplot, empty }) {
  const containerRef = useRef(null);
  const chartRef = useRef(null);
  const roRef = useRef(null);

  // Create / destroy the uPlot instance when uplot arrives or unmounts.
  // A change of `data` re-uses the same instance via setData below; only a
  // series-shape change (never in the current metric set) would need recreate.
  useEffect(() => {
    if (!uplot || !containerRef.current) return undefined;
    // When the series is empty we render the "No activity" message inside the
    // container instead of a chart. Instantiating uPlot here anyway appends a
    // `.u-wrap` after the flow-sized empty div, and uPlot's absolutely-
    // positioned canvas then draws ~CHART_HEIGHT px BELOW the card — landing
    // on top of the lists section (visible bug: charts appear under lists).
    if (empty) return undefined;
    const el = containerRef.current;
    const width = Math.max(120, el.clientWidth || 300);
    const rows = toUplotData(data, metrics);
    const opts = optsFor(width, CHART_HEIGHT);
    // Guard against a zero-length x-axis (uPlot throws on empty data).
    if (!rows[0] || rows[0].length === 0) {
      return () => {};
    }
    chartRef.current = new uplot(opts, rows, el);
    // Theme flips (light/dark, or a data-theme change) mutate <html>'s class
    // and data-theme attributes. uPlot draws on a canvas that cannot inherit
    // CSS, so watch those attributes and call redraw() — the function-form
    // stroke/grid colors above re-read the CSS vars on each draw.
    const themeObserver = new MutationObserver(() => {
      if (chartRef.current) chartRef.current.redraw();
    });
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class", "data-theme"],
    });
    if (typeof ResizeObserver !== "undefined") {
      const ro = new ResizeObserver(() => {
        if (chartRef.current) {
          const w = Math.max(120, el.clientWidth || 300);
          chartRef.current.setSize({ width: w, height: CHART_HEIGHT });
        }
      });
      ro.observe(el);
      roRef.current = ro;
    }
    return () => {
      themeObserver.disconnect();
      if (roRef.current) {
        roRef.current.disconnect();
        roRef.current = null;
      }
      if (chartRef.current) {
        chartRef.current.destroy();
        chartRef.current = null;
      }
    };
  }, [uplot, data, metrics, optsFor, empty]);

  return html`
    <!-- Carousel item: fixed basis so cards keep a readable width while the
         parent .stats-carousel handles horizontal overflow. width:min(...)
         keeps cards from getting too wide on desktops (multiple cards fit
         side-by-side) while still filling the viewport on narrow screens. -->
    <div
      class="rounded-lg shadow bg-mitto-surface-2 p-3 flex flex-col gap-2"
      style="width: min(360px, 100%); flex: 0 0 auto;"
    >
      <div class="text-xs text-mitto-text-muted truncate">${title}</div>
      <!-- shrink-0 + min-height belt-and-braces: the ChartCard is nested inside
           the dashboard's outer 'flex flex-col overflow-y-auto' container which
           default-shrinks its children (flex-shrink:1). Without this, the
           container collapses below CHART_HEIGHT in the layout, the lists grid
           rides UP into the same vertical band, and uPlot's absolutely-
           positioned canvas (sized to CHART_HEIGHT) then draws THROUGH/behind
           the lists. Also position:relative so overflow:hidden reliably clips
           uPlot's .u-wrap children in every browser. -->
      <div
        ref=${containerRef}
        class="w-full overflow-hidden shrink-0 relative"
        style=${`height: ${CHART_HEIGHT}px; min-height: ${CHART_HEIGHT}px;`}
      >
        ${empty
          ? html`<div class="w-full h-full flex items-center justify-center text-xs text-mitto-text-muted">
              No activity in this range
            </div>`
          : null}
      </div>
    </div>
  `;
}

// --- Model usage card ------------------------------------------------------

// Mirrors ChartCard's uPlot shell (create/destroy, ResizeObserver, theme
// MutationObserver) but computes its series list from the grouped model data
// so each model gets its own colored line via modelColor(). Kept as a sibling
// component rather than extending buildChartSpecs so the pinned three cards
// (mitto-a86b.10) stay byte-identical.
function ModelUsageCard({ modelData, uplot, empty, hidden, onToggleModel }) {
  const containerRef = useRef(null);
  const chartRef = useRef(null);
  const roRef = useRef(null);

  useEffect(() => {
    if (!uplot || !containerRef.current) return undefined;
    if (empty) return undefined;
    const el = containerRef.current;
    const { xs, models } = modelData;
    if (!xs || xs.length === 0 || models.length === 0) return undefined;
    const width = Math.max(120, el.clientWidth || 300);
    const rows = [xs, ...models.map((m) => m.values)];
    const xAxis = {
      space: 60,
      size: 32,
      stroke: axisStroke,
      grid: { stroke: gridStroke, width: 1 },
      ticks: { stroke: gridStroke, width: 1, size: 5 },
    };
    const yAxis = {
      size: 44,
      stroke: axisStroke,
      grid: { stroke: gridStroke, width: 1 },
      ticks: { stroke: gridStroke, width: 1, size: 5 },
    };
    const yScale = {
      range: (_up, dataMin, dataMax) => {
        if (!isFinite(dataMin) || !isFinite(dataMax)) return [0, 1];
        if (dataMin === dataMax) {
          const pad = Math.max(1, Math.abs(dataMax) * 0.1);
          return [dataMin - pad, dataMax + pad];
        }
        const span = dataMax - dataMin;
        const topPad = span * 0.08;
        if (dataMin >= 0 && dataMin <= span * 0.05) return [0, dataMax + topPad];
        return [dataMin - span * 0.05, dataMax + topPad];
      },
    };
    const series = [
      { label: "time" },
      ...models.map((m) => ({
        label: m.name,
        stroke: modelColor(m.name),
        show: !hidden[m.name],
      })),
    ];
    const opts = {
      width,
      height: CHART_HEIGHT,
      legend: { show: false },
      padding: [6, 8, 0, 0],
      scales: { x: { time: true }, y: yScale },
      axes: [xAxis, yAxis],
      series,
    };
    chartRef.current = new uplot(opts, rows, el);
    const themeObserver = new MutationObserver(() => {
      if (chartRef.current) chartRef.current.redraw();
    });
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class", "data-theme"],
    });
    if (typeof ResizeObserver !== "undefined") {
      const ro = new ResizeObserver(() => {
        if (chartRef.current) {
          const w = Math.max(120, el.clientWidth || 300);
          chartRef.current.setSize({ width: w, height: CHART_HEIGHT });
        }
      });
      ro.observe(el);
      roRef.current = ro;
    }
    return () => {
      themeObserver.disconnect();
      if (roRef.current) {
        roRef.current.disconnect();
        roRef.current = null;
      }
      if (chartRef.current) {
        chartRef.current.destroy();
        chartRef.current = null;
      }
    };
    // `hidden` intentionally excluded: toggling visibility uses setSeries in
    // the sibling effect below rather than tearing down the chart, so re-
    // creating on every legend click would be wasteful.
  }, [uplot, modelData, empty]);

  // Reflect the hidden map onto the live chart without recreating it.
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart || !modelData || !modelData.models) return;
    modelData.models.forEach((m, i) => {
      const seriesIdx = i + 1; // series[0] is the x/time series.
      const shouldShow = !hidden[m.name];
      const cur = chart.series[seriesIdx];
      if (cur && cur.show !== shouldShow) chart.setSeries(seriesIdx, { show: shouldShow });
    });
  }, [hidden, modelData]);

  const models = (modelData && modelData.models) || [];
  return html`
    <div
      class="rounded-lg shadow bg-mitto-surface-2 p-3 flex flex-col gap-2"
      style="width: min(360px, 100%); flex: 0 0 auto;"
    >
      <div class="text-xs text-mitto-text-muted truncate">Model usage (total tokens)</div>
      <div
        ref=${containerRef}
        class="w-full overflow-hidden shrink-0 relative"
        style=${`height: ${CHART_HEIGHT}px; min-height: ${CHART_HEIGHT}px;`}
      >
        ${empty
          ? html`<div class="w-full h-full flex flex-col items-center justify-center gap-1 text-xs text-mitto-text-muted">
              <div>No activity in this range</div>
              <div class="opacity-70">No model usage recorded yet — this appears after your next agent turn.</div>
            </div>`
          : null}
      </div>
      ${!empty && models.length > 0
        ? html`<div class="flex flex-wrap gap-2 text-xs" data-testid="model-usage-legend">
            ${models.map(
              (m) => html`<button
                key=${m.name}
                type="button"
                class=${`flex items-center gap-1 px-1.5 py-0.5 rounded ${hidden[m.name] ? "opacity-40" : ""}`}
                aria-pressed=${!hidden[m.name]}
                onClick=${() => onToggleModel(m.name)}
              >
                <span
                  class="inline-block w-2 h-2 rounded-sm"
                  style=${`background: ${modelColor(m.name)};`}
                ></span>
                <span class="text-mitto-text-strong">${m.name}</span>
                <span class="text-mitto-text-muted">${m.total.toLocaleString()}</span>
              </button>`,
            )}
          </div>`
        : null}
    </div>
  `;
}

// --- Main component --------------------------------------------------------

/**
 * StatsCharts — dashboard uPlot cards driven by GET /api/dashboard/timeseries.
 * Renders three fixed cards (tokens, tool calls, prompts vs turns) plus a
 * dynamic "Model usage" card (mitto-8wj) fed by a second grouped-by-model
 * fetch.
 * @param {Function} showToast - Toast dispatcher; called on fetch/load error.
 */
export function StatsCharts({ showToast }) {
  const [range, setRange] = useState(readPersistedRange);
  const [data, setData] = useState(null);
  const [modelDataRaw, setModelDataRaw] = useState(null);
  const [hiddenModels, setHiddenModels] = useState({});
  const [uplot, setUplot] = useState(() =>
    typeof window !== "undefined" && window.uPlot ? window.uPlot : null,
  );
  const mountedRef = useRef(true);

  // Load uPlot from CDN on mount (mirrors the mermaid loader pattern).
  useEffect(() => {
    if (uplot) return undefined;
    let cancelled = false;
    loadUplot().then(
      (mod) => {
        if (!cancelled) setUplot(() => mod);
      },
      (err) => {
        if (cancelled) return;
        if (showToast) {
          showToast({
            style: "error",
            title: "Charts unavailable",
            message: "Failed to load uPlot from CDN: " + (err && err.message ? err.message : String(err)),
          });
        }
      },
    );
    return () => {
      cancelled = true;
    };
    // Load exactly once per mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Fetch (or refetch) on range change; AbortController cancels stale calls.
  useEffect(() => {
    mountedRef.current = true;
    const controller = new AbortController();
    (async () => {
      try {
        const url = endpoints.misc.dashboardTimeseries({
          range,
          metrics: REQUESTED_METRICS.join(","),
        });
        const res = await authFetch(url, { signal: controller.signal });
        if (!mountedRef.current) return;
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const json = await res.json();
        if (!mountedRef.current) return;
        setData(json);
      } catch (err) {
        if (err && err.name === "AbortError") return;
        if (!mountedRef.current) return;
        if (showToast) {
          showToast({
            style: "error",
            title: "Timeseries fetch failed",
            message: err && err.message ? err.message : String(err),
          });
        }
      }
    })();
    return () => {
      mountedRef.current = false;
      controller.abort();
    };
  }, [range, showToast]);

  // Second fetch: groupBy=model for the Model usage card (mitto-8wj). Kept
  // separate from the ungrouped fetch so cards 1–3 keep byte-identical URLs
  // and payload shape (their tests are pinned) and the backend caches the two
  // response shapes independently.
  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    (async () => {
      try {
        const url = endpoints.misc.dashboardTimeseries({
          range,
          metrics: REQUESTED_MODEL_METRICS.join(","),
          groupBy: "model",
        });
        const res = await authFetch(url, { signal: controller.signal });
        if (cancelled) return;
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const json = await res.json();
        if (cancelled) return;
        setModelDataRaw(json);
      } catch (err) {
        if (err && err.name === "AbortError") return;
        if (cancelled) return;
        if (showToast) {
          showToast({
            style: "error",
            title: "Model usage fetch failed",
            message: err && err.message ? err.message : String(err),
          });
        }
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [range, showToast]);

  const empty = useMemo(() => isEmptySeries(data), [data]);
  const specs = useMemo(() => buildChartSpecs(uplot), [uplot]);
  // Chart-visibility preference (mitto-3i2 / mitto-4t8). Live-updates via the
  // `mitto-dashboard-hidden-charts-changed` window event dispatched by
  // storage.js:setDashboardHiddenCharts, and hydrates from the async
  // server → localStorage first-load path via onUIPreferencesLoaded.
  const hiddenCharts = useDashboardHiddenCharts();
  const visibleSpecs = useMemo(
    () => specs.filter((s) => !hiddenCharts.includes(s.id)),
    [specs, hiddenCharts],
  );
  const modelUsageVisible = !hiddenCharts.includes("model_usage");
  const modelData = useMemo(() => toModelUplotData(modelDataRaw), [modelDataRaw]);
  const modelEmpty = modelData.models.length === 0;
  const backfill = data && data.meta && data.meta.backfill_in_progress;
  const note = (data && data.meta && data.meta.note) || "";

  const toggleModel = (name) =>
    setHiddenModels((prev) => ({ ...prev, [name]: !prev[name] }));

  const rangeItems = RANGE_VALUES.map((r) => ({
    kind: "button",
    testId: `stats-range-${r}`,
    ariaLabel: `Show last ${r}`,
    tip: `Last ${r}`,
    active: r === range,
    icon: html`<span class="text-xs font-medium px-1">${r}</span>`,
    onClick: () => {
      writePersistedRange(r);
      setRange(r);
    },
  }));

  return html`
    <div class="flex flex-col gap-2 w-full shrink-0">
      <div class="flex items-center gap-2">
        <span class="text-sm font-medium text-mitto-text-strong">Activity</span>
        ${backfill
          ? html`<span class="badge badge-xs badge-warning">Backfilling history…</span>`
          : null}
        <span class="flex-1"></span>
        <${Toolbar}
          items=${rangeItems}
          variant="floating"
          size="xs"
          ariaLabel="Timeseries range"
          testId="stats-range-toolbar"
        />
      </div>
      <!-- Horizontal carousel of chart cards (mitto-a86b.8 UX iteration).
           Cards scroll horizontally when they overflow; a thin native
           scrollbar fades in only on hover / focus / active scrolling (see
           .mitto-carousel in styles.css). Each card carries its own min-width
           so multiple cards still fit side-by-side on wide viewports and
           only overflow on narrow ones (phone, split panes). -->
      <div class="mitto-carousel shrink-0 gap-3 w-full">
        ${visibleSpecs.length === 0 && !modelUsageVisible
          ? html`<div class="text-xs text-mitto-text-muted italic p-3">
              All charts are hidden. Enable at least one in Settings ▸ Dashboard.
            </div>`
          : visibleSpecs.map(
              (s) => html`<${ChartCard}
                key=${s.title}
                title=${s.title}
                metrics=${s.metrics}
                optsFor=${s.opts}
                data=${data}
                uplot=${uplot}
                empty=${empty}
              />`,
            )}
        ${modelUsageVisible
          ? html`<${ModelUsageCard}
              modelData=${modelData}
              uplot=${uplot}
              empty=${modelEmpty}
              hidden=${hiddenModels}
              onToggleModel=${toggleModel}
            />`
          : null}
      </div>
      ${note
        ? html`<div class="text-xs text-mitto-text-muted">${note}</div>`
        : null}
    </div>
  `;
}

// Silence unused-import lint: UNKNOWN_MODEL_NAME is re-exported for callers
// that want to compare against the synthetic bucket without importing the
// palette module separately.
export { UNKNOWN_MODEL_NAME };
