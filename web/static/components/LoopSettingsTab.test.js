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

  test("includes Arguments button for named prompts", () => {
    expect(tabJs).toMatch(/Arguments/);
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
