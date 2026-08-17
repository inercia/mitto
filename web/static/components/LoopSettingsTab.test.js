/**
 * Contract tests for LoopSettingsTab.js — raw source assertions.
 *
 * LoopSettingsTab.js cannot be imported directly under jsdom (it reads
 * `window.preact` at module load time). These tests read the raw source
 * and assert on the exact wiring, following the SessionPanel.test.js pattern.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { describe, test, expect } from "../utils/testing/testGlobals.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const tabJs = readFileSync(resolve(__dirname, "LoopSettingsTab.js"), "utf8");

// =============================================================================
// Import wiring tests
// =============================================================================

describe("LoopSettingsTab.js: imports and dependencies", () => {
  test("imports LoopPromptSelector from ./LoopPromptSelector.js", () => {
    expect(tabJs).toMatch(
      /import \{ LoopPromptSelector \} from "\.\/LoopPromptSelector\.js";/,
    );
  });

  test("imports promptDialogParameters from ../utils/prompts.js", () => {
    expect(tabJs).toMatch(
      /import \{ promptDialogParameters \} from "\.\.\/utils\/prompts\.js";/,
    );
  });

  test("imports getSdkClient from ../utils/sdkClient.js", () => {
    expect(tabJs).toMatch(
      /import \{ getSdkClient \} from "\.\.\/utils\/sdkClient\.js";/,
    );
  });

  test("imports validateLoopDraft, buildLoopPatch, isDangerousUnboundedLoop from ../utils/loopSettings.js", () => {
    expect(tabJs).toMatch(
      /import \{[\s\S]*validateLoopDraft[\s\S]*\} from "\.\.\/utils\/loopSettings\.js";/,
    );
    expect(tabJs).toMatch(
      /import \{[\s\S]*buildLoopPatch[\s\S]*\} from "\.\.\/utils\/loopSettings\.js";/,
    );
    expect(tabJs).toMatch(
      /import \{[\s\S]*isDangerousUnboundedLoop[\s\S]*\} from "\.\.\/utils\/loopSettings\.js";/,
    );
  });

  test("imports normalizeLoopConfig from ../utils/loopSettings.js", () => {
    expect(tabJs).toMatch(
      /import \{[\s\S]*normalizeLoopConfig[\s\S]*\} from "\.\.\/utils\/loopSettings\.js";/,
    );
  });
});

// =============================================================================
// Common settings visibility tests
// =============================================================================

describe("LoopSettingsTab.js: common settings visibility", () => {
  test("includes Enabled toggle", () => {
    expect(tabJs).toMatch(/label="Enabled"/);
  });

  test("includes Fresh context toggle", () => {
    expect(tabJs).toMatch(/label="Fresh context"/);
  });

  test("includes Run on start toggle", () => {
    expect(tabJs).toMatch(/label="Run on start"/);
  });

  test("includes Max runs number field", () => {
    expect(tabJs).toMatch(/label="Max runs"/);
  });

  test("includes Max duration field", () => {
    expect(tabJs).toMatch(/Max duration/);
    expect(tabJs).toMatch(/maxDuration\.value/);
    expect(tabJs).toMatch(/maxDuration\.unit/);
  });
});

// =============================================================================
// Trigger section titles tests
// =============================================================================

describe("LoopSettingsTab.js: four trigger section titles", () => {
  test("includes Schedule trigger section", () => {
    expect(tabJs).toMatch(/trigger="schedule"/);
    expect(tabJs).toMatch(/title="Schedule"/);
  });

  test("includes On completion trigger section", () => {
    expect(tabJs).toMatch(/trigger="onCompletion"/);
    expect(tabJs).toMatch(/title="On completion"/);
  });

  test("includes On tasks trigger section", () => {
    expect(tabJs).toMatch(/trigger="onTasks"/);
    expect(tabJs).toMatch(/title="On tasks"/);
  });

  test("includes On child trigger section", () => {
    expect(tabJs).toMatch(/trigger="onChild"/);
    expect(tabJs).toMatch(/title="On child"/);
  });

  test("renders injected callback content inside the trigger-card stack", () => {
    const childIdx = tabJs.lastIndexOf("${children}");
    const onSlackIdx = tabJs.indexOf('trigger="onSlack"');
    const triggerFieldsetEnd = tabJs.indexOf("</fieldset>", onSlackIdx);
    expect(childIdx).toBeGreaterThan(onSlackIdx);
    expect(childIdx).toBeLessThan(triggerFieldsetEnd);
  });
});

// =============================================================================
// SDK client usage tests
// =============================================================================

describe("LoopSettingsTab.js: SDK client usage", () => {
  test("uses getSdkClient().sessions.loop.get() for fetching loop config", () => {
    expect(tabJs).toMatch(/getSdkClient\(\)\s*\n?\s*\.sessions\.loop\.get\(/);
  });

  test("uses getSdkClient().sessions.loop.update() for saving loop config", () => {
    expect(tabJs).toMatch(/getSdkClient\(\)\s*\.sessions\.loop\.update\(/);
  });

  test("uses getSdkClient().sessions.loop.runNow() for run-now action", () => {
    expect(tabJs).toMatch(/getSdkClient\(\)\s*\.sessions\.loop\.runNow\(/);
  });
});

// =============================================================================
// Validation and patch building tests
// =============================================================================

describe("LoopSettingsTab.js: validation and patch building", () => {
  test("calls validateLoopDraft before saving", () => {
    const requestSaveIdx = tabJs.indexOf("const requestSave");
    expect(requestSaveIdx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(requestSaveIdx, requestSaveIdx + 500);
    expect(snippet).toMatch(/validateLoopDraft\(draft\)/);
  });

  test("calls buildLoopPatch to create the PATCH payload", () => {
    const requestSaveIdx = tabJs.indexOf("const requestSave");
    expect(requestSaveIdx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(requestSaveIdx, requestSaveIdx + 500);
    expect(snippet).toMatch(/buildLoopPatch\(draft/);
  });

  test("calls isDangerousUnboundedLoop to check for dangerous loops", () => {
    const requestSaveIdx = tabJs.indexOf("const requestSave");
    expect(requestSaveIdx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(requestSaveIdx, requestSaveIdx + 500);
    expect(snippet).toMatch(/isDangerousUnboundedLoop\(draft\)/);
  });
});

// =============================================================================
// Child event controls tests
// =============================================================================

describe("LoopSettingsTab.js: child event controls", () => {
  test("exposes anyEndResponse child event control", () => {
    expect(tabJs).toMatch(/anyEndResponse/);
    expect(tabJs).toMatch(/Any child finishes a response/);
  });

  test("exposes anyDeleted child event control", () => {
    expect(tabJs).toMatch(/anyDeleted/);
    expect(tabJs).toMatch(/Any child is deleted/);
  });

  test("exposes anyLoopStopped child event control", () => {
    expect(tabJs).toMatch(/anyLoopStopped/);
    expect(tabJs).toMatch(/Any child loop stops/);
  });
});

// =============================================================================
// Error display tests
// =============================================================================

describe("LoopSettingsTab.js: error display", () => {
  test("displays inline condition error", () => {
    expect(tabJs).toMatch(/conditionError/);
    // Check it's rendered with error styling
    expect(tabJs).toMatch(/conditionError &&[\s\S]*?text-mitto-danger/);
  });

  test("displays save error in alert", () => {
    expect(tabJs).toMatch(/saveError &&[\s\S]*?alert alert-error/);
  });

  test("displays validation error in alert", () => {
    expect(tabJs).toMatch(
      /validation &&[\s\S]*?!validation\.valid[\s\S]*?alert alert-error/,
    );
  });
});

// =============================================================================
// Dialog confirmation flow tests
// =============================================================================

describe("LoopSettingsTab.js: confirmation flows", () => {
  test("includes run-now dialog with reset timer option", () => {
    expect(tabJs).toMatch(/showRunDialog/);
    expect(tabJs).toMatch(/Run now/);
    expect(tabJs).toMatch(/resetTimer/);
    expect(tabJs).toMatch(/Reset countdown for the next scheduled run/);
  });

  test("includes restore dialog for re-enabling stopped loops", () => {
    expect(tabJs).toMatch(/showRestoreDialog/);
    expect(tabJs).toMatch(/Restore loop schedule/);
    expect(tabJs).toMatch(/resetCounters/);
  });

  test("includes danger confirmation dialog for unbounded loops", () => {
    expect(tabJs).toMatch(/showDangerDialog/);
    expect(tabJs).toMatch(/Save unbounded loop/);
    expect(tabJs).toMatch(/could keep running indefinitely/);
    expect(tabJs).toMatch(/confirmVariant="danger"/);
  });
});

// =============================================================================
// Prompt selector and arguments tests
// =============================================================================

describe("LoopSettingsTab.js: prompt selector and arguments", () => {
  test("uses LoopPromptSelector component", () => {
    expect(tabJs).toMatch(/<\${LoopPromptSelector}/);
  });

  test("uses promptDialogParameters for selected prompt", () => {
    const idx = tabJs.indexOf("selectedPromptParams");
    expect(idx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(idx, idx + 200);
    expect(snippet).toMatch(/promptDialogParameters\(selectedPrompt\)/);
  });

  test("calls onOpenPromptParamDialog with proper arguments", () => {
    const idx = tabJs.indexOf("const openArguments");
    expect(idx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(idx, idx + 400);
    expect(snippet).toMatch(/onOpenPromptParamDialog\(/);
    expect(snippet).toMatch(/selectedPrompt/);
    expect(snippet).toMatch(/selectedPromptParams/);
  });

  test("includes a recognizable parameter button beside the named prompt selector", () => {
    expect(tabJs).toMatch(/<\$\{SlidersIcon\}/);
    expect(tabJs).toMatch(/aria-label="Configure prompt parameters"/);
    expect(tabJs).toMatch(/Parameters/);
    expect(tabJs).toMatch(/onClick=\${openArguments}/);
  });
});

// =============================================================================
// Prompt mode tests
// =============================================================================

describe("LoopSettingsTab.js: prompt mode handling", () => {
  test("includes Named prompt radio option", () => {
    expect(tabJs).toMatch(/Named prompt/);
    expect(tabJs).toMatch(/promptMode === "named"/);
  });

  test("includes Free text radio option", () => {
    expect(tabJs).toMatch(/Free text/);
    expect(tabJs).toMatch(/promptMode === "freeText"/);
  });

  test("shows textarea for free text mode", () => {
    expect(tabJs).toMatch(/<textarea/);
    expect(tabJs).toMatch(/promptBody/);
  });
});

// =============================================================================
// Narrow drawer layout tests (mitto-w7hh.2 follow-up: fix Loop-tab overflow)
// =============================================================================

describe("LoopSettingsTab.js: narrow drawer layout", () => {
  test("all native Loop selects use one explicit chevron wrapper", () => {
    const idx = tabJs.indexOf("function NativeSelectWithChevron(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(idx, idx + 1500);
    expect(snippet).toMatch(/class="select select-sm w-full pr-8"/);
    expect(snippet).toMatch(
      /style="appearance:none;-webkit-appearance:none;background-image:none;"/,
    );
    expect(snippet).toMatch(
      /data-testid="\$\{testId\}-chevron"[\s\S]*?style="position:absolute;right:0\.5rem;top:50%;transform:translateY\(-50%\);pointer-events:none;"[\s\S]*?<\$\{ChevronDownIcon\}/,
    );
    expect(tabJs.match(/<\$\{NativeSelectWithChevron\}/g)).toHaveLength(3);
    expect(tabJs.match(/<select/g)).toHaveLength(1);
  });

  test("ToggleRow explicitly constrains wrapping copy beside its non-shrinking toggle", () => {
    const idx = tabJs.indexOf("function ToggleRow(");
    expect(idx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(idx, idx + 1100);
    expect(snippet).toMatch(
      /class="label cursor-pointer gap-4 w-full min-w-0"/,
    );
    expect(snippet).toMatch(/class="flex-1 min-w-0 whitespace-normal"/);
    expect(snippet).toMatch(
      /style="min-width:0;flex:1 1 0%;white-space:normal;overflow-wrap:anywhere;"/,
    );
    expect(snippet).toMatch(/class="toggle toggle-sm shrink-0"/);
    expect(snippet).toMatch(/style="flex-shrink:0;"/);
  });

  test("Trigger headers and On child event rows explicitly wrap beside their checkboxes", () => {
    const triggerIdx = tabJs.indexOf("function TriggerSection(");
    expect(triggerIdx).toBeGreaterThan(-1);
    const triggerSnippet = tabJs.slice(triggerIdx, triggerIdx + 2200);
    expect(triggerSnippet).toMatch(
      /data-testid="loop-settings-trigger-copy-\$\{trigger\}"/,
    );
    expect(triggerSnippet).toMatch(
      /style="min-width:0;flex:1 1 0%;white-space:normal;overflow-wrap:anywhere;"/,
    );

    const childIdx = tabJs.indexOf("KNOWN_CHILD_EVENTS.map");
    expect(childIdx).toBeGreaterThan(-1);
    const childSnippet = tabJs.slice(childIdx, childIdx + 2200);
    expect(childSnippet).toMatch(
      /data-testid="loop-settings-child-event-copy-\$\{eventName\}"/,
    );
    expect(childSnippet).toMatch(
      /style="min-width:0;flex:1 1 0%;white-space:normal;overflow-wrap:anywhere;"/,
    );
  });

  test("General limits (Max runs / Max duration) use a single-column grid, not a viewport-responsive sm:grid-cols-2", () => {
    const idx = tabJs.indexOf('legend class="fieldset-legend">General<');
    expect(idx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(idx, idx + 2000);
    expect(snippet).toMatch(/class="grid grid-cols-1 gap-3"/);
    expect(snippet).not.toMatch(/sm:grid-cols-2/);
  });

  test("onTasks Cooldown/Settle window fields use a single-column grid, not sm:grid-cols-2", () => {
    const idx = tabJs.indexOf('trigger="onTasks"');
    expect(idx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(idx, idx + 3500);
    expect(snippet).toMatch(/Cooldown \(seconds\)/);
    expect(snippet).toMatch(/class="grid grid-cols-1 gap-3"/);
    expect(snippet).not.toMatch(/sm:grid-cols-2/);
  });

  test("Max duration gives the number a usable width and renders a compact unit dropdown with a chevron", () => {
    const idx = tabJs.indexOf("Max duration");
    expect(idx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(idx, idx + 2400);
    expect(snippet).toMatch(/class="flex items-center gap-2"/);
    expect(snippet).toMatch(
      /class="input input-sm w-24 min-w-0 shrink-0"[\s\S]*?data-testid="loop-settings-max-duration-value"/,
    );
    expect(snippet).toMatch(
      /<\$\{NativeSelectWithChevron\}[\s\S]*?ariaLabel="Max duration unit"[\s\S]*?testId="loop-settings-max-duration-unit"[\s\S]*?wrapperClass="w-28 shrink-0"/,
    );
  });

  test("Schedule unit and Fire when use the shared dropdown affordance", () => {
    expect(tabJs).toMatch(
      /ariaLabel="Schedule unit"[\s\S]*?testId="loop-settings-schedule-unit"[\s\S]*?wrapperClass="w-28 shrink-0"/,
    );
    expect(tabJs).toMatch(
      /ariaLabel="Fire when"[\s\S]*?testId="loop-settings-fire-when"[\s\S]*?wrapperClass="w-full"/,
    );
  });

  test("no sm:grid-cols-2 breakpoints remain anywhere in the file (drawer width, not viewport width, governs layout)", () => {
    expect(tabJs).not.toMatch(/sm:grid-cols-2/);
  });
});
