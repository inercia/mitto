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

/**
 * Row-layout polish (mitto-m5f.1) — raw-source assertions pinning the
 * acceptance criteria from the bead description:
 *
 *   - outer row: single edge-to-edge "join w-full" -> "flex items-center
 *     gap-2 w-full" (breathing room via gap instead of flush join-items).
 *   - label input: stays flex-1, no longer a join-item.
 *   - swatch + hex: grouped into one inner "join" unit so they read as a
 *     single "color" control, with the swatch resized to h-8 w-8 (was
 *     w-6 h-6, shorter than input-sm height) and the hex input constrained
 *     to a narrow fixed width (w-28, was flex-1 - a #rrggbb value is only
 *     7 chars and doesn't need label-width space).
 *   - up/down/trash: right-aligned cluster (no longer join-items of the
 *     single outer join).
 *
 * Kept as raw-source assertions (same convention as the anchoring tests
 * above and HeaderArchiveButton.test.js) since the property under test is
 * fully determined by the component's static markup/classes, not runtime
 * behavior — mounting would require the full preact/htm global setup this
 * file deliberately avoids.
 */
describe("TaskLabelColorsEditor — row layout polish (mitto-m5f.1)", () => {
  test("outer row uses a flex gap layout, not a single edge-to-edge join", () => {
    expect(componentSrc).toMatch(/class="flex items-center gap-2 w-full"/);
    // The bug this replaces: a lone "join w-full" wrapping all six segments
    // flush against each other. There must be no top-level "join w-full"
    // left over from the old layout.
    expect(componentSrc).not.toMatch(/class="join w-full"/);
  });

  test("label input keeps flex-1 and is no longer a join-item", () => {
    expect(componentSrc).toMatch(/class="input input-sm flex-1"/);
  });

  test("swatch and hex input are grouped into one inner join unit", () => {
    // The dropdown (swatch trigger + popover) and the hex input must be
    // wrapped together so they visually read as a single "color" control,
    // distinct from the outer row's flex gap layout.
    expect(componentSrc).toMatch(/<div class="join">/);
  });

  test("swatch trigger is resized to full input-sm height (h-8 w-8)", () => {
    // Was "w-6 h-6 rounded border ..." - shorter than the input-sm row
    // height, causing the wedged/misaligned look reported in the bead.
    expect(componentSrc).toMatch(
      /class="h-8 w-8 rounded border border-mitto-border-2 cursor-pointer"/,
    );
    expect(componentSrc).not.toMatch(
      /class="w-6 h-6 rounded border border-mitto-border-2 cursor-pointer"/,
    );
  });

  test("hex input is constrained to a narrow fixed width, not flex-1", () => {
    expect(componentSrc).toMatch(
      /class="input input-sm join-item w-28 font-mono"/,
    );
    // Guard against a regression back to the too-wide flex-1 hex input.
    expect(componentSrc).not.toMatch(/class="input input-sm flex-1 join-item/);
  });

  test("up/down/trash controls form a right-aligned cluster, not outer join-items", () => {
    // The three action buttons must share a dedicated wrapper (they no
    // longer rely on the single outer "join" for flush grouping).
    expect(componentSrc).toMatch(
      /<div class="flex items-center">\s*<button[\s\S]*?aria-label="Move up"/,
    );
  });
});
