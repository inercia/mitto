/**
 * Unit tests for the tick-interval decision logic (bead mitto-2fx.3).
 *
 * Both MessageList.js and CountdownDisplay.js import window.preact globals at
 * module load, so they cannot be imported directly under jsdom. Following the
 * project convention (see LoopFrequencyPanel.test.js, PromptParameterDialog.test.js,
 * Message.test.js), the small tick-interval decision expressions are duplicated
 * here and tested directly. A source-scan guard at the bottom also asserts that
 * the production files still contain the expected constants, so the duplicate
 * cannot silently drift out of sync with the real components.
 *
 * Acceptance criteria pinned by these tests (from mitto-2fx.3):
 *   - MessageList "Working…" chip tick advances every 2000ms while an agent
 *     heartbeat is visible (was 1000ms).
 *   - CountdownDisplay ticks every 60000ms for "days", 5000ms for "hours",
 *     and 1000ms for any other unit (i.e. "minutes"). Only the "hours" branch
 *     changed (was 1000ms); "days" and "minutes" are unchanged.
 */

import {
  describe,
  test,
  expect,
} from "../utils/testing/testGlobals.js";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// =============================================================================
// Duplicated tick-interval decision logic
// Mirrors:
//   - web/static/components/MessageList.js   (Working chip tick)
//   - web/static/components/CountdownDisplay.js (adaptive countdown tick)
// Keep in sync — the source-scan guard below will fail if the production
// constants drift.
// =============================================================================

/** Working-chip tick delay used by MessageList.js while an agent is working. */
function workingChipTickDelayMs() {
  return 2000;
}

/**
 * Countdown tick delay used by CountdownDisplay.js. Mirrors the ternary at
 * CountdownDisplay.js: `unit === "days" ? 60000 : unit === "hours" ? 5000 : 1000`.
 */
function countdownTickDelayMs(unit) {
  return unit === "days" ? 60000 : unit === "hours" ? 5000 : 1000;
}

// =============================================================================
// Tests
// =============================================================================

describe("MessageList working-chip tick (mitto-2fx.3)", () => {
  test("advances every 2000ms (halved from the previous 1000ms)", () => {
    expect(workingChipTickDelayMs()).toBe(2000);
  });
});

describe("CountdownDisplay adaptive tick (mitto-2fx.3)", () => {
  test("daily schedule ticks every 60000ms", () => {
    expect(countdownTickDelayMs("days")).toBe(60000);
  });

  test("hourly schedule ticks every 5000ms (only minute resolution is visible)", () => {
    expect(countdownTickDelayMs("hours")).toBe(5000);
  });

  test("minute schedule keeps 1000ms tick so seconds visibly count down", () => {
    expect(countdownTickDelayMs("minutes")).toBe(1000);
  });

  test("unknown/empty unit falls through to the 1000ms default", () => {
    expect(countdownTickDelayMs("weeks")).toBe(1000);
    expect(countdownTickDelayMs("")).toBe(1000);
    expect(countdownTickDelayMs(undefined)).toBe(1000);
  });
});

// =============================================================================
// Source-scan guard — fail if the production files diverge from the duplicated
// values above. This is the safety net that catches "test still passes but the
// component was reverted / changed" drift.
// =============================================================================

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

function readComponent(name) {
  return readFileSync(join(__dirname, name), "utf8");
}

describe("source-scan guard — production constants match duplicated logic", () => {
  test("MessageList.js passes 2000 to useVisibleInterval", () => {
    const src = readComponent("MessageList.js");
    // Match the exact call form: `useVisibleInterval(() => setWorkingNow(Date.now()), 2000, {`
    expect(src).toMatch(
      /useVisibleInterval\(\s*\(\)\s*=>\s*setWorkingNow\(Date\.now\(\)\)\s*,\s*2000\s*,/,
    );
    // And explicitly NOT the old 1000 form for this callsite.
    expect(src).not.toMatch(
      /useVisibleInterval\(\s*\(\)\s*=>\s*setWorkingNow\(Date\.now\(\)\)\s*,\s*1000\s*,/,
    );
  });

  test("CountdownDisplay.js encodes the three-way days/hours/else ternary", () => {
    const src = readComponent("CountdownDisplay.js");
    // Match the exact ternary shape: `unit === "days" ? 60000 : unit === "hours" ? 5000 : 1000`
    expect(src).toMatch(
      /unit\s*===\s*"days"\s*\?\s*60000\s*:\s*unit\s*===\s*"hours"\s*\?\s*5000\s*:\s*1000/,
    );
  });
});
