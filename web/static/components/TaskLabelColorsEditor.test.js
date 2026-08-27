/**
 * Reproduction test for mitto-19m — Task label color picker opens in a
 * detached/weird location.
 *
 * Root cause (investigate phase, see bead comments): the swatch in
 * TaskLabelColorsEditor.js is a native HTML <input type="color">, whose
 * popover is positioned entirely by the OS/browser (macOS NSColorPanel via
 * WKWebView) — not by Mitto. Inside the constrained Settings modal this
 * produces a detached, disconnected placement (screenshot: panel appears at
 * the bottom-center of the dialog, far from the swatch).
 *
 * A JS unit test cannot assert *where* a native OS panel renders on screen —
 * that's outside the DOM/JS boundary entirely. The narrowest testable,
 * behavioral contract (per the bead's suggested direction) is: the swatch
 * must NOT be a native <input type="color"> — it must be a Mitto-rendered,
 * anchored in-app picker trigger instead, so popover positioning is under
 * Mitto's control.
 *
 * Follows the raw-source-assertion convention used by
 * HeaderArchiveButton.test.js / LoopSettingsTab.test.js rather than mounting
 * the component, since the property under test (absence of a native color
 * input / presence of an anchored trigger) is fully determined by the
 * component's static markup.
 *
 * This test currently FAILS because the native <input type="color"> is still
 * present. It will pass once the fix phase replaces it with an anchored
 * in-app popover exposing a stable data-testid trigger. Do not delete or
 * skip this test — it verifies the upcoming fix.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const componentSrc = readFileSync(
  resolve(__dirname, "TaskLabelColorsEditor.js"),
  "utf8",
);

describe("TaskLabelColorsEditor — swatch picker anchoring (mitto-19m)", () => {
  test('does not render a native <input type="color"> swatch (OS panel positioning can\'t be anchored by Mitto)', () => {
    // The bug: a native color input hands popover positioning entirely to
    // the browser/OS, which detaches it from the swatch inside the
    // constrained Settings modal. The fix must replace it with an in-app,
    // Mitto-anchored picker so positioning stays under Mitto's control.
    expect(componentSrc).not.toMatch(/type="color"/);
  });

  test("exposes a stable, anchored picker trigger for the color swatch", () => {
    // The replacement trigger must carry its own stable per-row test id so
    // the anchored popover can be located/asserted independently of the
    // native input removed above, mirroring the row-scoped id convention
    // already used by data-testid="task-label-color-row-${idx}" above it.
    expect(componentSrc).toMatch(/data-testid="task-label-color-swatch-/);
  });
});
