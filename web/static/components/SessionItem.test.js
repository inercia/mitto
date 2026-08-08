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
import { getConversationAccentStyles, rgbToHsl } from "../lib.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const sessionItemJs = readFileSync(resolve(__dirname, "SessionItem.js"), "utf8");

describe("SessionItem.js: conversation accent color (mitto-8sk)", () => {
  test("imports getConversationAccentStyles from lib.js", () => {
    expect(sessionItemJs).toMatch(
      /import\s*{[^}]*getConversationAccentStyles[^}]*}\s*from\s*["']\.\.\/lib\.js["']/s,
    );
  });

  test("derives accentStyles from session.background_color and the active theme", () => {
    expect(sessionItemJs).toMatch(
      /const accentStyles = useMemo\(\s*\(\)\s*=>\s*getConversationAccentStyles\(\s*session\.background_color,\s*isLightTheme,?\s*\)/,
    );
    // The theme must be a dependency, otherwise toggling light/dark leaves the
    // previously derived colors in place.
    expect(sessionItemJs).toMatch(
      /\[session\.background_color, isLightTheme\],/,
    );
  });

  test("renders a row tint plus a left accent stripe, suppressed when isActive", () => {
    // Rendered only when a color is set AND the row is NOT active
    // (bg-mitto-accent already owns the active row's background).
    expect(sessionItemJs).toMatch(/\$\{accentStyles && !isActive/);
    expect(sessionItemJs).toMatch(
      /class="absolute inset-0 z-0 pointer-events-none"\s*\n\s*style="background: \$\{accentStyles\.tint\};"/,
    );
    // The stripe itself: a thin, non-interactive bar pinned to the left edge.
    expect(sessionItemJs).toMatch(
      /class="absolute left-0 top-0 bottom-0 w-1 z-0 pointer-events-none"\s*\n\s*style="background: \$\{accentStyles\.stripe\};"/,
    );
  });

  test("the accent overlays sit before the loopProgressBg overlay (same z-0 layer)", () => {
    const loopIdx = sessionItemJs.indexOf("${loopProgressBg");
    const accentIdx = sessionItemJs.indexOf("${accentStyles && !isActive");
    expect(accentIdx).toBeGreaterThan(-1);
    expect(loopIdx).toBeGreaterThan(accentIdx);
  });
});

describe("SessionItem.js: protected-conversation archive suppression (mitto-yvel.4)", () => {
  test("derives isProtected from session.no_archive", () => {
    expect(sessionItemJs).toMatch(
      /const isProtected = !!session\.no_archive;/,
    );
  });

  test("canArchive is direction-aware: protection blocks archive only, never unarchive", () => {
    expect(sessionItemJs).toMatch(
      /const canArchive =\s*\n\s*isArchived \|\| \(!isProtected && !hasQueuedMessages && !isSessionStreaming\);/,
    );
  });

  test("archiveBlockedReason surfaces the protected reason first, only when not archived", () => {
    const idx = sessionItemJs.indexOf("const archiveBlockedReason =");
    expect(idx).toBeGreaterThan(-1);
    const snippet = sessionItemJs.slice(idx, idx + 400);
    expect(snippet).toMatch(
      /!isArchived && isProtected\s*\n\s*\? "This conversation is protected from archiving"/,
    );
    // The protected check must come before the queued/streaming checks so its
    // reason wins when a conversation is both protected and queued/streaming.
    const protectedIdx = snippet.indexOf("isProtected");
    const queuedIdx = snippet.indexOf("hasQueuedMessages");
    expect(protectedIdx).toBeGreaterThan(-1);
    expect(queuedIdx).toBeGreaterThan(protectedIdx);
  });

  test("swipe-to-action is disabled for protected sessions, but only in the archive direction", () => {
    // Swipe-to-delete (archived tab / spawned children) must stay enabled even
    // when isProtected is true — protection only suppresses swipe-to-archive.
    expect(sessionItemJs).toMatch(
      /disabled: !isSwipeToDelete && isProtected,/,
    );
  });
});

describe("getConversationAccentStyles", () => {
  test("an unset background_color resolves to null (no accent at all)", () => {
    expect(getConversationAccentStyles(undefined, false)).toBeNull();
    expect(getConversationAccentStyles("", true)).toBeNull();
  });

  test("light theme keeps the pastel itself (translucent tint, solid stripe)", () => {
    expect(getConversationAccentStyles("#E1BEE7", true)).toEqual({
      tint: "rgba(225, 190, 231, 0.45)",
      stripe: "rgb(225, 190, 231)",
    });
  });

  test("dark theme rebuilds the color from its hue: dark tint, bright stripe", () => {
    const styles = getConversationAccentStyles("#C5CAE9", false);
    const { h } = rgbToHsl(197, 202, 233);
    const hue = Math.round(h);
    expect(styles.tint).toBe(`hsla(${hue}, 55%, 22%, 0.85)`);
    expect(styles.stripe).toBe(`hsl(${hue}, 60%, 62%)`);
  });

  test("dark theme keeps distinct hues distinguishable across the palette", () => {
    const hues = ["#FFCDD2", "#C8E6C9", "#B3E5FC", "#E1BEE7"].map(
      (hex) => getConversationAccentStyles(hex, false).tint,
    );
    expect(new Set(hues).size).toBe(hues.length);
  });

  test("the achromatic Grey swatch stays grey on dark (never clamped to red)", () => {
    const styles = getConversationAccentStyles("#E0E0E0", false);
    expect(styles.tint).toBe("hsla(0, 0%, 22%, 0.85)");
    expect(styles.stripe).toBe("hsl(0, 0%, 62%)");
  });
});
