/** Contract tests for the compact loop conversation controls. */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { describe, test, expect } from "../utils/testing/testGlobals.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const barJs = readFileSync(resolve(__dirname, "LoopControlBar.js"), "utf8");

describe("LoopControlBar", () => {
  test("is compact and has no expandable editor", () => {
    expect(barJs).toMatch(/data-testid="loop-control-bar"/);
    expect(barJs).not.toMatch(/LoopFrequencyPanel|loop-expand-toggle/);
  });

  test("uses one state-driven pause and restore control", () => {
    expect(barJs).not.toMatch(/data-testid="loop-run-now-button"/);
    expect(barJs.match(/data-testid="loop-pause-resume-button"/g)).toHaveLength(
      1,
    );
    expect(barJs).toMatch(
      /sessions\.loop\.update\(sessionId, \{ enabled: false \}\)/,
    );
    expect(barJs).toMatch(/const patch = \{ enabled: true \}/);
    expect(barJs).toMatch(/sessions\.loop\.runNow\(sessionId, true\)/);
    expect(barJs).toMatch(/busy === "pause" \|\| busy === "restore"/);
  });

  test("retains message-input and settings actions", () => {
    expect(barJs).toMatch(/data-testid="loop-toggle-prompt-area"/);
    expect(barJs).toMatch(/data-testid="loop-open-settings"/);
  });

  test("preserves the cap-stop reset behavior when restoring", () => {
    expect(barJs).toMatch(/"maxDuration"/);
    expect(barJs).toMatch(/"maxIterations"/);
    expect(barJs).toMatch(/patch\.reset_counters = true/);
  });
});
