// Unit tests for scripts/perf-ab.mjs (mitto-sus.2 Test phase).
//
// Covers buildComparison()'s cross-join/ratio logic and renderReport()'s
// Markdown rendering — the core of the A/B comparative report's
// "quantified gap" and "webkit/chromium, wkwebview/webkit ratio" acceptance
// criteria. Not wired into `make test-js` (scoped to `web/static`, see
// Makefile); run directly with:
//   bun test scripts/perf-ab.test.mjs
import { describe, test, expect } from "bun:test";
import { buildComparison, renderReport } from "./perf-ab.mjs";

describe("buildComparison", () => {
  test("cross-joins scenarios/metrics present in only one leg", () => {
    const rows = buildComparison(
      { "a.scenario": { p50: 1 } },
      { "b.scenario": { p50: 2 } },
      { "c.scenario": { p50: 3 } },
    );
    expect(rows).toHaveLength(3);
    expect(rows.map((r) => r.scenario)).toEqual([
      "a.scenario",
      "b.scenario",
      "c.scenario",
    ]);
  });

  test("computes webkit/chromium and wkwebview/webkit ratios", () => {
    const rows = buildComparison(
      { s: { p50: 10 } },
      { s: { p50: 15 } },
      { s: { p50: 30 } },
    );
    expect(rows).toHaveLength(1);
    expect(rows[0].webkitOverChromium).toBeCloseTo(1.5);
    expect(rows[0].wkwebviewOverWebkit).toBeCloseTo(2);
  });

  test("ratio is null when either side is missing", () => {
    const rows = buildComparison({ s: { p50: 10 } }, null, null);
    expect(rows[0].webkit).toBeUndefined();
    expect(rows[0].webkitOverChromium).toBeNull();
    expect(rows[0].wkwebviewOverWebkit).toBeNull();
  });

  test("ratio is null (not Infinity) when the base is zero", () => {
    const rows = buildComparison({ s: { p50: 0 } }, { s: { p50: 5 } }, {});
    expect(rows[0].webkitOverChromium).toBeNull();
  });

  test("skips the 'n' pseudo-metric", () => {
    const rows = buildComparison(
      { s: { p50: 1, n: 5 } },
      { s: { p50: 2, n: 5 } },
      {},
    );
    expect(rows.map((r) => r.metric)).toEqual(["p50"]);
  });

  test("sorts scenarios and metrics alphabetically", () => {
    const rows = buildComparison(
      { z: { p95: 1, p50: 2 }, a: { p50: 3 } },
      {},
      {},
    );
    expect(rows.map((r) => `${r.scenario}.${r.metric}`)).toEqual([
      "a.p50",
      "z.p50",
      "z.p95",
    ]);
  });

  test("treats null/undefined legs as empty rather than throwing", () => {
    expect(() => buildComparison(undefined, null, undefined)).not.toThrow();
    expect(buildComparison(undefined, null, undefined)).toEqual([]);
  });
});

describe("renderReport", () => {
  test("renders the header, table, and per-row values/ratios", () => {
    const rows = buildComparison(
      { "composer.keystroke": { p50: 10 } },
      { "composer.keystroke": { p50: 15 } },
      { "composer.keystroke": { p50: 30 } },
    );
    const md = renderReport(rows);

    expect(md).toContain("# UI Responsiveness A/B Report (mitto-sus.2)");
    expect(md).toContain(
      "| Scenario | Metric | Chromium | WebKit | WKWebView | WebKit/Chromium | WKWebView/WebKit |",
    );
    expect(md).toContain(
      "| composer.keystroke | p50 | 10.00 | 15.00 | 30.00 | 1.50x | 2.00x |",
    );
  });

  test("renders missing values and ratios as an em dash", () => {
    const rows = buildComparison({ s: { p50: 10 } }, null, null);
    const md = renderReport(rows);
    expect(md).toContain("| s | p50 | 10.00 | — | — | — | — |");
  });

  test("renders an empty table body (header only) for no rows", () => {
    const md = renderReport([]);
    expect(md).toContain("|---|---|---|---|---|---|---|");
    expect(md).not.toMatch(/\| .+ \| .+ \| .+ \| .+ \| .+ \| .+x \|/);
  });
});
