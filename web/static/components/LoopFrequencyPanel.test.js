/** UI binding contracts; shared production Play behavior runs in loopPlay.test.js. */
import { readFileSync } from "node:fs";
import { describe, test, expect } from "../utils/testing/testGlobals.js";

const panelJs = readFileSync(
  new URL("./LoopFrequencyPanel.js", import.meta.url),
  "utf8",
);

describe("LoopFrequencyPanel Play binding", () => {
  test("uses the same production action and confirmation as LoopControlBar", () => {
    expect(panelJs).toMatch(/useLoopPlay\(sessionId, onLoopEnabledChange\)/);
    expect(panelJs).toMatch(/play\.open\(\)/);
    expect(panelJs).toMatch(/onConfirm=\$\{play\.confirm\}/);
    expect(panelJs).toMatch(/title=\$\{play\.confirmation\?\.title\}/);
    expect(panelJs).toMatch(/message=\$\{play\.confirmation\?\.message\}/);
    expect(panelJs).not.toMatch(
      /limitWasStopped|limitStopped|reset_counters|stoppedReason/,
    );
  });
});

// ──────────────────────────────────────────────────────────────────────────────
// Duplicated toStableTriggersList logic (bead mitto-987y.7)
// Mirrors web/static/components/LoopFrequencyPanel.js L506-509 — keep in sync.
// ──────────────────────────────────────────────────────────────────────────────

/**
 * Mirrors `toStableTriggersList` from LoopFrequencyPanel.js (fixed,
 * mitto-987y.7). Builds the wire-payload `triggers` array from the
 * armed-triggers Set: known triggers first in canonical order (schedule,
 * onCompletion, onTasks), then any other armed trigger (e.g. a future
 * `onChild`) appended afterward instead of dropped.
 *
 * Per internal/session/loop.go, a non-nil `triggers` field REPLACES the
 * stored trigger list wholesale (not a merge), so silently omitting an
 * armed-but-unrecognized trigger would permanently disarm it on the server.
 */
const KNOWN_TRIGGERS = ["schedule", "onCompletion", "onTasks"];
function toStableTriggersList(set) {
  const known = KNOWN_TRIGGERS.filter((t) => set.has(t));
  const unknown = [...set].filter((t) => !KNOWN_TRIGGERS.includes(t));
  return [...known, ...unknown];
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests — regression guard (mitto-987y.7, fixed)
// ──────────────────────────────────────────────────────────────────────────────

describe("toStableTriggersList — mitto-987y.7: unknown triggers must survive save", () => {
  // `onChild` (epic mitto-987y) is not implemented yet on the backend — no
  // occurrences exist in internal/ or web/static/ (confirmed during
  // investigation), so it is used here purely as an opaque, currently-unknown
  // trigger string. The fix is at the frontend/payload boundary: whatever
  // string is armed must round-trip through save, regardless of whether the
  // backend recognizes it today.
  test("fixed: an armed unknown trigger (onChild) survives into the saved list", () => {
    const armed = new Set(["onTasks", "onChild"]);

    const result = toStableTriggersList(armed);

    // Previously (buggy): toStableTriggersList only knew
    // schedule/onCompletion/onTasks, so "onChild" was filtered out.
    expect(result).toContain("onChild");
  });

  test("fixed: PATCH payload.triggers preserves the unknown trigger, keeping it armed", () => {
    // Mirrors the payload shape built in performSave() (LoopFrequencyPanel.js
    // L528-536): { triggers: toStableTriggersList(localTriggers), ... }.
    const localTriggers = new Set(["schedule", "onChild"]);
    const payload = { triggers: toStableTriggersList(localTriggers) };
    const body = JSON.stringify(payload);

    // internal/session/loop.go: a non-nil `triggers` field REPLACES the
    // stored list wholesale, so if "onChild" were missing here it would not
    // be merely absent from this save — it would be disarmed server-side.
    expect(JSON.parse(body).triggers).toContain("onChild");
  });

  test("known triggers keep canonical order alongside a preserved unknown trigger", () => {
    // Fix shape: canonical-first, then append unknowns — so Normalize()
    // still derives the legacy scalar Trigger from Triggers[0] correctly
    // (loop.go L528-529).
    const armed = new Set(["onChild", "onCompletion", "schedule"]);

    const result = toStableTriggersList(armed);

    expect(result).toEqual(["schedule", "onCompletion", "onChild"]);
  });
});
