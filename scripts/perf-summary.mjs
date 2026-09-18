#!/usr/bin/env node
// Aggregates `make bench-ui` per-run samples (mitto-sus.1.3) into a
// diff-against-baseline report and/or the committed
// tests/ui/perf/baseline.json, plus a rendered Markdown table.
//
// Reads `tests/ui/perf/results/<runId>/samples.jsonl` (one JSON object per
// line: {scenario, metric, value, meta?, ts}), written by
// tests/ui/utils/perf.ts's writePerfSample(). Mirrors
// scripts/gen-perf-histories.mjs's style: no new npm deps, deterministic,
// safe to re-run.
//
// Usage:
//   node scripts/perf-summary.mjs
//       --results <dir>            required: results/<runId>/ dir
//       [--baseline <path>]        diff current results vs. this baseline
//       [--diff-out <path>]        write diff Markdown here (else stdout)
//       [--write-baseline <path>]  WRITE current results as the new baseline
//       [--render <md-path>]       render a baseline (--write-baseline's
//                                  result, or --baseline if reading only) as
//                                  a human-readable Markdown table
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(__dirname, "..");

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i];
    if (!key.startsWith("--")) continue;
    const name = key.slice(2);
    const value = argv[i + 1] && !argv[i + 1].startsWith("--") ? argv[++i] : "true";
    args[name] = value;
  }
  return args;
}

/** Reads every *.jsonl file under `resultsDir` and aggregates by (scenario, metric). */
export function aggregateResults(resultsDir) {
  const samples = {};
  if (!existsSync(resultsDir)) return samples;
  for (const file of readdirSync(resultsDir)) {
    if (!file.endsWith(".jsonl")) continue;
    const lines = readFileSync(join(resultsDir, file), "utf-8").split("\n");
    for (const line of lines) {
      if (!line.trim()) continue;
      const rec = JSON.parse(line);
      const bucket = (samples[rec.scenario] ??= { n: 0 });
      (bucket[rec.metric] ??= []).push(rec.value);
      if (rec.meta && typeof rec.meta.n === "number") {
        bucket.n = Math.max(bucket.n, rec.meta.n);
      }
    }
  }
  // Collapse each metric's recorded values: direct value if only one sample
  // was recorded for that (scenario, metric) this run, else the median.
  const collapsed = {};
  for (const [scenario, metrics] of Object.entries(samples)) {
    collapsed[scenario] = {};
    for (const [metric, values] of Object.entries(metrics)) {
      if (metric === "n") {
        collapsed[scenario].n = values;
        continue;
      }
      const sorted = [...values].sort((a, b) => a - b);
      collapsed[scenario][metric] =
        sorted.length % 2 === 1
          ? sorted[(sorted.length - 1) / 2]
          : (sorted[sorted.length / 2 - 1] + sorted[sorted.length / 2]) / 2;
    }
  }
  return collapsed;
}

export function detectEnvironment() {
  let playwrightVersion = "unknown";
  try {
    const pkg = JSON.parse(readFileSync(join(repoRoot, "package.json"), "utf-8"));
    playwrightVersion =
      pkg.devDependencies?.["@playwright/test"] ??
      pkg.dependencies?.["@playwright/test"] ??
      "unknown";
  } catch {
    // best-effort only
  }
  let chromiumVersion = "unknown";
  try {
    chromiumVersion = execFileSync(
      "bunx",
      ["playwright", "--version"],
      { cwd: repoRoot, stdio: ["ignore", "pipe", "ignore"] },
    )
      .toString()
      .trim();
  } catch {
    // best-effort only
  }
  return {
    os: process.platform,
    arch: process.arch,
    playwright: playwrightVersion,
    chromium: chromiumVersion,
  };
}

export function writeDiff(current, baseline, diffOutPath) {
  const rows = [];
  const scenarios = new Set([
    ...Object.keys(current),
    ...Object.keys(baseline?.samples ?? {}),
  ]);
  for (const scenario of [...scenarios].sort()) {
    const curMetrics = current[scenario];
    const baseMetrics = baseline?.samples?.[scenario];
    const metrics = new Set([
      ...Object.keys(curMetrics ?? {}),
      ...Object.keys(baseMetrics ?? {}),
    ]);
    for (const metric of [...metrics].sort()) {
      if (metric === "n") continue;
      const curVal = curMetrics?.[metric];
      const baseVal = baseMetrics?.[metric];
      if (curVal === undefined) {
        rows.push([scenario, metric, baseVal, "—", "—", "—", "DROPPED"]);
      } else if (baseVal === undefined) {
        rows.push([scenario, metric, "—", curVal.toFixed(2), "—", "—", "NEW"]);
      } else {
        const delta = curVal - baseVal;
        const pct = baseVal !== 0 ? (delta / baseVal) * 100 : 0;
        let verdict = "ok";
        if (pct > 10) verdict = "regression";
        else if (pct < -10) verdict = "improvement";
        rows.push([
          scenario,
          metric,
          baseVal.toFixed(2),
          curVal.toFixed(2),
          delta.toFixed(2),
          `${pct.toFixed(1)}%`,
          verdict,
        ]);
      }
    }
  }
  const header = "| Scenario | Metric | Baseline | Current | Δ | % | Verdict |";
  const sep = "|---|---|---|---|---|---|---|";
  const body = rows.map((r) => `| ${r.join(" | ")} |`).join("\n");
  const md = `${header}\n${sep}\n${body}\n`;
  if (diffOutPath) {
    mkdirSync(dirname(diffOutPath), { recursive: true });
    writeFileSync(diffOutPath, md);
    console.log(`wrote diff: ${diffOutPath}`);
  } else {
    console.log(md);
  }
}

export function renderBaseline(baseline, renderPath) {
  const lines = [];
  lines.push("# UI Responsiveness Baseline (mitto-sus.1.3)");
  lines.push("");
  lines.push(
    "Auto-generated by `make bench-ui-baseline` from `tests/ui/perf/baseline.json`. " +
      "Do not edit by hand — re-run `make bench-ui-baseline` to refresh.",
  );
  lines.push("");
  lines.push(`Recorded at: \`${baseline.recorded_at}\``);
  lines.push("");
  lines.push("## Recorded environment");
  lines.push("");
  for (const [key, value] of Object.entries(baseline.environment)) {
    lines.push(`- **${key}**: ${value}`);
  }
  lines.push("");
  lines.push("## Samples");
  lines.push("");
  lines.push("| Scenario | Metric | Value | n |");
  lines.push("|---|---|---|---|");
  for (const scenario of Object.keys(baseline.samples).sort()) {
    const metrics = baseline.samples[scenario];
    const n = metrics.n ?? "—";
    for (const metric of Object.keys(metrics).sort()) {
      if (metric === "n") continue;
      lines.push(`| ${scenario} | ${metric} | ${metrics[metric].toFixed(2)} | ${n} |`);
    }
  }
  lines.push("");
  lines.push(
    "See [ui-responsiveness-benchmarks.md](./ui-responsiveness-benchmarks.md) " +
      "\"Proposed budgets\" for how each scenario's budget verdict (gate / " +
      "record-only) was decided against these numbers.",
  );
  lines.push("");
  mkdirSync(dirname(renderPath), { recursive: true });
  writeFileSync(renderPath, lines.join("\n"));
  console.log(`wrote rendered baseline: ${renderPath}`);
}

export function main() {
  const args = parseArgs(process.argv.slice(2));
  if (!args.results) {
    console.error("usage: perf-summary.mjs --results <dir> [--baseline <path>] " +
      "[--diff-out <path>] [--write-baseline <path>] [--render <md-path>]");
    process.exit(1);
  }
  const resultsDir = resolve(args.results);
  const current = aggregateResults(resultsDir);

  let baseline = null;
  if (args.baseline && existsSync(resolve(args.baseline))) {
    baseline = JSON.parse(readFileSync(resolve(args.baseline), "utf-8"));
  }

  if (args.baseline) {
    writeDiff(current, baseline, args["diff-out"] ? resolve(args["diff-out"]) : null);
  }

  if (args["write-baseline"]) {
    const newBaseline = {
      recorded_at: new Date().toISOString(),
      environment: detectEnvironment(),
      samples: current,
    };
    const outPath = resolve(args["write-baseline"]);
    mkdirSync(dirname(outPath), { recursive: true });
    writeFileSync(outPath, JSON.stringify(newBaseline, null, 2) + "\n");
    console.log(`wrote baseline: ${outPath}`);
    baseline = newBaseline;
  }

  if (args.render) {
    if (!baseline) {
      console.error("--render requires --write-baseline or an existing --baseline to read");
      process.exit(1);
    }
    renderBaseline(baseline, resolve(args.render));
  }
}

// Only run the CLI when executed directly (`node scripts/perf-summary.mjs ...`),
// not when imported for unit testing (scripts/perf-summary.test.mjs).
if (resolve(fileURLToPath(import.meta.url)) === resolve(process.argv[1] ?? "")) {
  main();
}
