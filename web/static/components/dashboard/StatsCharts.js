// Mitto Web Interface - Dashboard timeseries charts (mitto-a86b.8).
// Three uPlot charts (tokens, tool calls, prompts) driven by
// GET /api/dashboard/timeseries with a 24h/7d/30d range selector. uPlot is
// loaded on-demand from the CDN (mirrors the mermaid loader in
// preact-loader.js); charts silently degrade when the CDN is unreachable.
const { html, useEffect, useMemo, useRef, useState } = window.preact;

import { authFetch } from "../../utils/csrf.js";
import { endpoints } from "../../utils/endpoints.js";
import { CDN_URLS } from "../../vendor/config.js";
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

// Chart height in px (fixed so narrow viewports do not collapse). Kept as a
// number so uPlot can size its canvas directly.
const CHART_HEIGHT = 220;

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

// Each spec describes one card: title, the tsResponse metric keys it consumes
// in order, and a factory that returns the uPlot opts (excluding width/height,
// which are supplied by the component). Keeping the specs declarative keeps
// the render loop below uniform and makes future chart additions cheap.
function buildChartSpecs(u) {
  const timeFmt = { space: 60 };
  const stroke = (v) => v;
  return [
    {
      title: "Tokens (input + output)",
      metrics: ["input_tokens_est", "output_tokens_est"],
      opts: (w, h) => ({
        width: w,
        height: h,
        legend: { show: true },
        scales: { x: { time: true } },
        axes: [{ ...timeFmt }, {}],
        series: [
          { label: "time" },
          { label: "input", stroke: stroke("#38bdf8"), fill: "rgba(56,189,248,0.15)" },
          { label: "output", stroke: stroke("#a78bfa"), fill: "rgba(167,139,250,0.15)" },
        ],
      }),
    },
    {
      title: "Tool calls",
      metrics: ["tool_calls_total", "mcp_calls"],
      opts: (w, h) => ({
        width: w,
        height: h,
        legend: { show: true },
        scales: { x: { time: true } },
        axes: [{ ...timeFmt }, {}],
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
      title: "Prompts vs agent turns",
      metrics: ["prompts", "agent_turns_completed"],
      opts: (w, h) => ({
        width: w,
        height: h,
        legend: { show: true },
        scales: { x: { time: true } },
        axes: [{ ...timeFmt }, {}],
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
    const el = containerRef.current;
    const width = Math.max(120, el.clientWidth || 300);
    const rows = toUplotData(data, metrics);
    const opts = optsFor(width, CHART_HEIGHT);
    // Guard against a zero-length x-axis (uPlot throws on empty data).
    if (!rows[0] || rows[0].length === 0) {
      return () => {};
    }
    chartRef.current = new uplot(opts, rows, el);
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
      if (roRef.current) {
        roRef.current.disconnect();
        roRef.current = null;
      }
      if (chartRef.current) {
        chartRef.current.destroy();
        chartRef.current = null;
      }
    };
  }, [uplot, data, metrics, optsFor]);

  return html`
    <div class="rounded-lg shadow bg-mitto-surface-2 p-3 flex flex-col gap-2 min-w-0">
      <div class="text-xs text-mitto-text-muted truncate">${title}</div>
      <div
        ref=${containerRef}
        class="w-full"
        style=${`height: ${CHART_HEIGHT}px;`}
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

// --- Main component --------------------------------------------------------

/**
 * StatsCharts — three uPlot cards driven by GET /api/dashboard/timeseries.
 * @param {Function} showToast - Toast dispatcher; called on fetch/load error.
 */
export function StatsCharts({ showToast }) {
  const [range, setRange] = useState(readPersistedRange);
  const [data, setData] = useState(null);
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

  const empty = useMemo(() => isEmptySeries(data), [data]);
  const specs = useMemo(() => buildChartSpecs(uplot), [uplot]);
  const backfill = data && data.meta && data.meta.backfill_in_progress;
  const note = (data && data.meta && data.meta.note) || "";

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
    <div class="flex flex-col gap-2 w-full">
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
      <div
        class="grid gap-3 w-full"
        style="grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));"
      >
        ${specs.map(
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
      </div>
      ${note
        ? html`<div class="text-xs text-mitto-text-muted">${note}</div>`
        : null}
    </div>
  `;
}
