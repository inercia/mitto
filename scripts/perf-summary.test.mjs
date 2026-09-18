// Unit tests for scripts/perf-summary.mjs (mitto-sus.1.3 Test phase).
//
// Covers the aggregation/diff/render logic that turns raw
// tests/ui/perf/results/<runId>/samples.jsonl records into the
// baseline-vs-current diff verdicts (NEW/DROPPED/regression/improvement/ok)
// and the committed tests/ui/perf/baseline.json shape — the core of the
// "record baseline.json ... promote proposed budgets to gate/record-only
// decisions" acceptance criteria. Not wired into `make test-js` (scoped to
// `web/static`, see Makefile); run directly with:
//   bun test scripts/perf-summary.test.mjs
import { describe, test, expect, beforeEach, afterEach } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  aggregateResults,
  writeDiff,
  renderBaseline,
} from "./perf-summary.mjs";

let tmpDir;

beforeEach(() => {
  tmpDir = mkdtempSync(join(tmpdir(), "perf-summary-test-"));
});

afterEach(() => {
  rmSync(tmpDir, { recursive: true, force: true });
});

describe("aggregateResults", () => {
  test("returns an empty object when the results dir does not exist", () => {
    expect(aggregateResults(join(tmpDir, "missing"))).toEqual({});
  });

  test("collapses a single sample per (scenario, metric) to its raw value", () => {
    writeFileSync(
      join(tmpDir, "samples.jsonl"),
      `${JSON.stringify({ scenario: "composer.keystroke", metric: "p95", value: 6.3 })}\n`,
    );
    const result = aggregateResults(tmpDir);
    expect(result["composer.keystroke"].p95).toBe(6.3);
  });

  test("collapses multiple samples to the median (odd count)", () => {
    const lines = [1, 5, 3]
      .map((value) =>
        JSON.stringify({ scenario: "s", metric: "m", value }),
      )
      .join("\n");
    writeFileSync(join(tmpDir, "samples.jsonl"), lines + "\n");
    expect(aggregateResults(tmpDir).s.m).toBe(3);
  });

  test("collapses multiple samples to the median (even count, averages middle two)", () => {
    const lines = [1, 2, 3, 4]
      .map((value) =>
        JSON.stringify({ scenario: "s", metric: "m", value }),
      )
      .join("\n");
    writeFileSync(join(tmpDir, "samples.jsonl"), lines + "\n");
    expect(aggregateResults(tmpDir).s.m).toBe(2.5);
  });

  test("reads every *.jsonl file in the dir and ignores non-.jsonl files", () => {
    writeFileSync(
      join(tmpDir, "a.jsonl"),
      `${JSON.stringify({ scenario: "s", metric: "m", value: 10 })}\n`,
    );
    writeFileSync(
      join(tmpDir, "b.jsonl"),
      `${JSON.stringify({ scenario: "s", metric: "m", value: 20 })}\n`,
    );
    writeFileSync(join(tmpDir, "README.txt"), "not a sample file\n");
    expect(aggregateResults(tmpDir).s.m).toBe(15);
  });

  test("takes the max meta.n across samples for a scenario", () => {
    const lines = [
      { scenario: "s", metric: "m", value: 1, meta: { n: 2 } },
      { scenario: "s", metric: "m", value: 2, meta: { n: 5 } },
    ]
      .map((r) => JSON.stringify(r))
      .join("\n");
    writeFileSync(join(tmpDir, "samples.jsonl"), lines + "\n");
    expect(aggregateResults(tmpDir).s.n).toBe(5);
  });

  test("skips blank lines", () => {
    writeFileSync(
      join(tmpDir, "samples.jsonl"),
      `${JSON.stringify({ scenario: "s", metric: "m", value: 1 })}\n\n\n`,
    );
    expect(aggregateResults(tmpDir).s.m).toBe(1);
  });
});

describe("writeDiff verdicts", () => {
  function diffRows(current, baseline) {
    const outPath = join(tmpDir, "diff.md");
    writeDiff(current, baseline, outPath);
    return readFileSync(outPath, "utf-8");
  }

  test("marks a scenario absent from baseline as NEW", () => {
    const md = diffRows({ s: { m: 1.234 } }, { samples: {} });
    expect(md).toContain("| s | m | — | 1.23 | — | — | NEW |");
  });

  test("marks a scenario absent from current as DROPPED", () => {
    const md = diffRows({}, { samples: { s: { m: 1.5 } } });
    expect(md).toContain("| s | m | 1.5 | — | — | — | DROPPED |");
  });

  test("marks a >10% increase as a regression", () => {
    const md = diffRows({ s: { m: 15 } }, { samples: { s: { m: 10 } } });
    expect(md).toContain("regression");
    expect(md).not.toContain("| ok |");
  });

  test("marks a >10% decrease as an improvement", () => {
    const md = diffRows({ s: { m: 8 } }, { samples: { s: { m: 10 } } });
    expect(md).toContain("improvement");
  });

  test("marks a small delta (<=10%) as ok", () => {
    const md = diffRows({ s: { m: 10.5 } }, { samples: { s: { m: 10 } } });
    expect(md).toContain("| ok |");
  });

  test("skips the 'n' pseudo-metric", () => {
    const md = diffRows(
      { s: { m: 10, n: 3 } },
      { samples: { s: { m: 10, n: 3 } } },
    );
    expect(md).not.toMatch(/\| s \| n \|/);
  });
});

describe("renderBaseline", () => {
  test("renders scenarios/metrics sorted, with environment and a doc pointer", () => {
    const baseline = {
      recorded_at: "2026-01-01T00:00:00.000Z",
      environment: { os: "darwin", arch: "arm64" },
      samples: {
        "z.scenario": { p95: 5, n: 3 },
        "a.scenario": { p50: 1.005, n: 7 },
      },
    };
    const outPath = join(tmpDir, "baseline.md");
    renderBaseline(baseline, outPath);
    const md = readFileSync(outPath, "utf-8");

    expect(md).toContain("Recorded at: `2026-01-01T00:00:00.000Z`");
    expect(md).toContain("- **os**: darwin");
    expect(md).toContain("- **arch**: arm64");
    // a.scenario sorts before z.scenario.
    expect(md.indexOf("a.scenario")).toBeLessThan(md.indexOf("z.scenario"));
    expect(md).toContain("| a.scenario | p50 | 1.00 | 7 |");
    expect(md).toContain("| z.scenario | p95 | 5.00 | 3 |");
    expect(md).toContain("ui-responsiveness-benchmarks.md");
  });
});
