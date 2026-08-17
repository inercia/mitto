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

  test("retains run-now, pause, restore, message-input, and settings actions", () => {
    expect(barJs).toMatch(/sessions\.loop\.runNow\(sessionId, resetTimer\)/);
    expect(barJs).toMatch(
      /sessions\.loop\.update\(sessionId, \{ enabled: false \}\)/,
    );
    expect(barJs).toMatch(/const patch = \{ enabled: true \}/);
    expect(barJs).toMatch(/data-testid="loop-toggle-prompt-area"/);
    expect(barJs).toMatch(/data-testid="loop-open-settings"/);
  });

  test("preserves the cap-stop reset behavior when restoring", () => {
    expect(barJs).toMatch(/"maxDuration"/);
    expect(barJs).toMatch(/"maxIterations"/);
    expect(barJs).toMatch(/patch\.reset_counters = true/);
  });
});
