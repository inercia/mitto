/**
 * Contract tests for LoopSettingsTab.js — raw source assertions, plus a
 * focused mounted-behavior section (mitto-xh79) for the On Slack card
 * header action. Most of this file predates the mounted-DOM harness other
 * components use (window.preact set before a dynamic import — see
 * SlackSettingsTab.test.js/SlackSubscriptionEditor.test.js) and instead
 * reads the raw source and asserts on the exact wiring, following the
 * SessionPanel.test.js pattern; the mitto-xh79 section below adopts the
 * mounted-DOM approach for the one behavior that needs a real click/DOM
 * assertion (label-vs-sibling placement, checkbox untouched).
 */

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import {
  describe,
  test,
  expect,
  jest,
} from "../utils/testing/testGlobals.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const tabJs = readFileSync(resolve(__dirname, "LoopSettingsTab.js"), "utf8");
const nativeSelectJs = readFileSync(
  resolve(__dirname, "NativeSelectWithChevron.js"),
  "utf8",
);

// =============================================================================
// Import wiring tests
// =============================================================================

describe("LoopSettingsTab.js: imports and dependencies", () => {
  test("imports LoopPromptSelector from ./LoopPromptSelector.js", () => {
    expect(tabJs).toMatch(
      /import \{ LoopPromptSelector \} from "\.\/LoopPromptSelector\.js";/,
    );
  });

  test("imports the shared native-select dropdown affordance", () => {
    expect(tabJs).toMatch(
      /import \{ NativeSelectWithChevron \} from "\.\/NativeSelectWithChevron\.js";/,
    );
  });

  test("imports promptDialogParameters and unmetRequiredParams from ../utils/prompts.js", () => {
    // The import was widened from a single named import to a multi-line block
    // when unmetRequiredParams was added (loop Enabled-toggle gating). Assert
    // both names are imported from the same module rather than pinning the
    // exact single-line shape.
    expect(tabJs).toMatch(/import \{[\s\S]*?\} from "\.\.\/utils\/prompts\.js";/);
    expect(tabJs).toMatch(/promptDialogParameters/);
    expect(tabJs).toMatch(/unmetRequiredParams/);
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
// mitto-xh79: On Slack card header action (gear button) wiring
// =============================================================================

describe("LoopSettingsTab.js: On Slack card header action (mitto-xh79)", () => {
  test("TriggerSection accepts an optional headerAction rendered beside (not inside) the label", () => {
    const triggerIdx = tabJs.indexOf("function TriggerSection(");
    expect(triggerIdx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(triggerIdx, triggerIdx + 2400);
    expect(snippet).toMatch(/headerAction/);
    // The label carries flex-1 (shares the row with the action) instead of
    // the old full-width label, and the action is a sibling rendered after
    // </label> closes, not a child of it.
    const labelIdx = snippet.indexOf("<label");
    const labelCloseIdx = snippet.indexOf("</label>", labelIdx);
    const actionIdx = snippet.indexOf("headerAction &&", labelCloseIdx);
    expect(labelIdx).toBeGreaterThan(-1);
    expect(labelCloseIdx).toBeGreaterThan(labelIdx);
    expect(actionIdx).toBeGreaterThan(labelCloseIdx);
    expect(snippet.slice(labelIdx, labelCloseIdx)).toMatch(
      /class="label cursor-pointer gap-3 justify-start flex-1 min-w-0"/,
    );
  });

  test("imports SettingsIcon, Tooltip, and openSettingsTab for the gear button", () => {
    expect(tabJs).toMatch(/SettingsIcon/);
    expect(tabJs).toMatch(
      /import \{ Tooltip \} from "\.\/Tooltip\.js";/,
    );
    expect(tabJs).toMatch(
      /import \{ openSettingsTab \} from "\.\.\/utils\/slackEvents\.js";/,
    );
  });

  test("onSlack passes a compact gear headerAction wired to openSettingsTab('slack')", () => {
    const onSlackIdx = tabJs.indexOf('trigger="onSlack"');
    expect(onSlackIdx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(onSlackIdx, onSlackIdx + 900);
    expect(snippet).toMatch(/headerAction=\$\{html`<\$\{Tooltip\}/);
    expect(snippet).toMatch(
      /tip="Manage Slack integrations"/,
    );
    expect(snippet).toMatch(/class="btn btn-ghost btn-square btn-sm"/);
    expect(snippet).toMatch(
      /data-testid="slack-manage-integrations"/,
    );
    expect(snippet).toMatch(
      /aria-label="Manage Slack integrations"/,
    );
    expect(snippet).toMatch(/title="Manage Slack integrations"/);
    expect(snippet).toMatch(/onClick=\$\{\(\) => openSettingsTab\("slack"\)\}/);
    expect(snippet).toMatch(/<\$\{SettingsIcon\}/);
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
    // Window widened from 500 to 1200: requestSave gained an unmet-required
    // parameter guard (with an explanatory comment) between validateLoopDraft
    // and buildLoopPatch/isDangerousUnboundedLoop.
    const snippet = tabJs.slice(requestSaveIdx, requestSaveIdx + 1200);
    expect(snippet).toMatch(/validateLoopDraft\(draft\)/);
  });

  test("calls buildLoopPatch to create the PATCH payload", () => {
    const requestSaveIdx = tabJs.indexOf("const requestSave");
    expect(requestSaveIdx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(requestSaveIdx, requestSaveIdx + 1200);
    expect(snippet).toMatch(/buildLoopPatch\(draft/);
  });

  test("calls isDangerousUnboundedLoop to check for dangerous loops", () => {
    const requestSaveIdx = tabJs.indexOf("const requestSave");
    expect(requestSaveIdx).toBeGreaterThan(-1);
    const snippet = tabJs.slice(requestSaveIdx, requestSaveIdx + 1200);
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
    expect(tabJs).toMatch(/dropdownPlacement="below"/);
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

  test("includes a compact sliders-only parameter button beside the named prompt selector", () => {
    expect(tabJs).toMatch(/<\$\{SlidersIcon\}/);
    expect(tabJs).toMatch(/aria-label="Configure prompt parameters"/);
    expect(tabJs).toMatch(/btn-square/);
    expect(tabJs).toMatch(/<\$\{SlidersIcon\}[^>]*\/>\s*<\/button>/);
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
    expect(nativeSelectJs).toMatch(/class="select select-sm w-full pr-8"/);
    expect(nativeSelectJs).toMatch(
      /style="appearance:none;-webkit-appearance:none;background-image:none;"/,
    );
    expect(nativeSelectJs).toMatch(
      /data-testid="\$\{testId\}-chevron"[\s\S]*?style="position:absolute;right:0\.5rem;top:50%;transform:translateY\(-50%\);pointer-events:none;"[\s\S]*?<\$\{ChevronDownIcon\}/,
    );
    expect(tabJs.match(/<\$\{NativeSelectWithChevron\}/g)).toHaveLength(3);
    expect(tabJs).not.toMatch(/<select/g);
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

// =============================================================================
// mitto-xh79: On Slack card header action — mounted DOM behavior
//
// Runs in an isolated child process (own happy-dom via bunfig preload) so
// setting window.preact before the dynamic import never leaks into the raw
// source-string tests above, mirroring SlackSettingsTab.test.js /
// SlackSubscriptionEditor.test.js.
// =============================================================================

const isMountedChildRun =
  process.env.MITTO_LOOP_SETTINGS_COMPONENT_TEST_CHILD === "1";

if (isMountedChildRun) {
  const preact = await import("../vendor/preact.js");
  const hooks = await import("../vendor/preact-hooks.js");
  const htm = (await import("../vendor/htm.js")).default;
  const previousPreact = window.preact;
  window.preact = { ...preact, ...hooks, html: htm.bind(preact.h) };
  const { LoopSettingsTab } = await import(
    "./LoopSettingsTab.js?mitto-xh79-mounted-tests"
  );
  const { _resetSdkClientForTests } = await import("../utils/sdkClient.js");
  window.preact = previousPreact;

  const html = htm.bind(preact.h);

  function jsonResponse(body) {
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }

  async function waitForMounted(predicate, container, message = "condition") {
    for (let i = 0; i < 150; i++) {
      if (predicate()) return;
      await new Promise((r) => setTimeout(r, 2));
    }
    throw new Error(`Timed out waiting for ${message}: ${container.innerHTML}`);
  }

  async function mountLoopSettingsTab() {
    window.mittoApiPrefix = "";
    global.fetch = jest.fn((url) => {
      const u = String(url);
      if (u.includes("/api/slack/apps")) return jsonResponse({ apps: [] });
      throw new Error(`Unexpected fetch: ${u}`);
    });
    const container = document.createElement("div");
    document.body.appendChild(container);
    preact.render(
      html`<${LoopSettingsTab}
        sessionId="session-1"
        loopConfig=${{ triggers: ["onSlack"] }}
      />`,
      container,
    );
    await waitForMounted(
      () => container.querySelector('[data-testid="loop-settings-onSlack"]'),
      container,
      "loop settings onSlack card",
    );
    return container;
  }

  function unmount(container) {
    preact.render(null, container);
    container.remove();
  }

  describe("LoopSettingsTab mounted: On Slack card header gear (mitto-xh79)", () => {
    test("renders the gear button in the header action area, not inside SlackSubscriptionEditor, and it is absent from other trigger cards", async () => {
      _resetSdkClientForTests();
      const container = await mountLoopSettingsTab();
      try {
        const actionArea = container.querySelector(
          '[data-testid="loop-settings-trigger-header-action-onSlack"]',
        );
        expect(actionArea).not.toBeNull();
        const gear = actionArea.querySelector(
          '[data-testid="slack-manage-integrations"]',
        );
        expect(gear).not.toBeNull();
        expect(gear.getAttribute("aria-label")).toBe(
          "Manage Slack integrations",
        );
        expect(gear.getAttribute("title")).toBe("Manage Slack integrations");

        // Not owned by SlackSubscriptionEditor anymore.
        const editor = container.querySelector(
          '[data-testid="slack-subscription-editor"]',
        );
        expect(editor).not.toBeNull();
        expect(
          editor.querySelector('[data-testid="slack-manage-integrations"]'),
        ).toBeNull();

        // Other trigger cards never get a header action area.
        for (const trigger of ["schedule", "onCompletion", "onTasks", "onChild"]) {
          expect(
            container.querySelector(
              `[data-testid="loop-settings-trigger-header-action-${trigger}"]`,
            ),
          ).toBeNull();
        }
      } finally {
        unmount(container);
      }
    });

    test("clicking the gear dispatches mitto:open_settings with {tab:'slack'} and never toggles the On Slack checkbox", async () => {
      _resetSdkClientForTests();
      const container = await mountLoopSettingsTab();
      let detail;
      const onOpen = (event) => {
        detail = event.detail;
      };
      window.addEventListener("mitto:open_settings", onOpen);
      try {
        const checkbox = container.querySelector(
          '[data-testid="loop-settings-trigger-onSlack"]',
        );
        expect(checkbox.checked).toBe(true);

        container
          .querySelector('[data-testid="slack-manage-integrations"]')
          .click();

        expect(detail).toEqual({ tab: "slack" });
        // Checkbox state is unchanged by the click (still armed from the
        // loopConfig fixture; a wrongly-nested action would have toggled it
        // off via the <label>'s native click-to-activate behavior).
        expect(checkbox.checked).toBe(true);
      } finally {
        window.removeEventListener("mitto:open_settings", onOpen);
        unmount(container);
      }
    });
  });
} else {
  describe("LoopSettingsTab mounted: On Slack card header gear (mitto-xh79)", () => {
    test("passes mounted behavior tests in an isolated happy-dom process", () => {
      const result = spawnSync(
        process.execPath,
        ["test", fileURLToPath(import.meta.url)],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            MITTO_LOOP_SETTINGS_COMPONENT_TEST_CHILD: "1",
          },
          timeout: 30_000,
        },
      );
      if (result.status !== 0) {
        throw new Error(
          `Isolated LoopSettingsTab mounted tests failed:\n${result.stdout}\n${result.stderr}`,
        );
      }
    });
  });
}
