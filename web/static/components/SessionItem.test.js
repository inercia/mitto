/**
 * Regression tests for the mitto-8sk conversation accent stripe in
 * SessionItem.js.
 *
 * SessionItem.js cannot be imported directly under jsdom (it reads
 * `window.preact` at module load time, same limitation documented in
 * BeadsView.test.js), so — mirroring that file's and styles.test.js's
 * approach — these tests read the raw source and assert on the exact
 * markup/wiring rather than rendering the component.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { hexToRgb } from "../lib.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const sessionItemJs = readFileSync(resolve(__dirname, "SessionItem.js"), "utf8");

describe("SessionItem.js: conversation accent color (mitto-8sk)", () => {
  test("imports hexToRgb from lib.js", () => {
    expect(sessionItemJs).toMatch(/import\s*{[^}]*hexToRgb[^}]*}\s*from\s*["']\.\.\/lib\.js["']/s);
  });

  test("derives accentRgb from session.background_color", () => {
    expect(sessionItemJs).toMatch(
      /const accentRgb = useMemo\(\s*\(\)\s*=>\s*hexToRgb\(session\.background_color\)/,
    );
  });

  test("renders a left accent stripe using accentRgb, suppressed when isActive", () => {
    // The stripe is only rendered when accentRgb is truthy AND the row is
    // NOT active (bg-mitto-accent already owns the active row's background).
    expect(sessionItemJs).toMatch(/\$\{accentRgb && !isActive/);
    // The stripe itself: a thin, non-interactive, rounded-left bar pinned to
    // the left edge — never a full row fill (would risk text contrast).
    expect(sessionItemJs).toMatch(
      /class="absolute left-0 top-0 bottom-0 w-1 rounded-l-lg z-0 pointer-events-none"/,
    );
    expect(sessionItemJs).toMatch(
      /style="background: rgb\(\$\{accentRgb\.r\}, \$\{accentRgb\.g\}, \$\{accentRgb\.b\}\);"/,
    );
  });

  test("the accent stripe div is a sibling of the loopProgressBg overlay (same z-0 layer)", () => {
    const loopIdx = sessionItemJs.indexOf("${loopProgressBg");
    const accentIdx = sessionItemJs.indexOf("${accentRgb && !isActive");
    expect(loopIdx).toBeGreaterThan(-1);
    expect(accentIdx).toBeGreaterThan(loopIdx);
  });
});

describe("hexToRgb wiring used by the accent stripe", () => {
  test("a valid #RRGGBB target.backgroundColor resolves to an rgb triple", () => {
    expect(hexToRgb("#E1BEE7")).toEqual({ r: 225, g: 190, b: 231 });
  });

  test("an unset background_color (undefined) resolves to null (no stripe)", () => {
    expect(hexToRgb(undefined)).toBeNull();
  });
});
