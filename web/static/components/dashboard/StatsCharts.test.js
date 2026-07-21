/**
 * Unit tests for StatsCharts pure helpers.
 *
 * The StatsCharts component (StatsCharts.js) imports window.preact globals at
 * module load time, so it cannot be imported under jsdom. Following the
 * project convention used by Dashboard.test.js / BeadsView.test.js, the pure
 * helpers under test are duplicated verbatim below (with the "Keep in sync"
 * marker) and exercised directly.
 */

import { jest } from "@jest/globals";

// =============================================================================
// Duplicated helpers — keep in sync with StatsCharts.js
// =============================================================================

const RANGE_STORAGE_KEY = "mitto:dashboard:statsRange";
const RANGE_VALUES = ["24h", "7d", "30d"];
const DEFAULT_RANGE = "24h";

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
