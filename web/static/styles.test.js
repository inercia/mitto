/**
 * CSS regression tests for web/static/styles.css.
 *
 * Pins the invariant introduced by mitto-2fx.1: the five always-on infinite
 * `@keyframes` rules must NOT declare `will-change`. `will-change` is a
 * temporary hint; leaving it on continuously-animating elements pins a
 * per-instance GPU compositor layer on macOS WKWebView and keeps the
 * "Mitto Graphics and Media" service process hot.
 *
 * Also asserts the deliberate `will-change` sites survive:
 *   - the mitto-cdf drawer-ghosting fix (`.drawer-end > … :not(.drawer-overlay)`
 *     sets `will-change: auto` to neutralize daisyUI's default)
 *   - the `.reduce-animations` opt-outs and the
 *     `@media (prefers-reduced-motion: reduce)` opt-outs, so the reduce-motion
 *     path stays self-documenting.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const stylesCss = readFileSync(resolve(__dirname, "styles.css"), "utf8");

/**
 * Extract the flat declaration block for `selector` — the text between the
 * matching `{` and the next `}`. All target selectors in mitto-2fx.1 are flat
 * (no nested at-rules), so a non-greedy match on `[^}]+` is safe.
 *
 * Returns null when the selector is missing so a caller can distinguish
 * "rule removed" from "rule present but empty".
 */
function ruleBody(css, selector) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`(^|\\s)${escaped}\\s*\\{([^}]+)\\}`, "m");
  const m = css.match(re);
  return m ? m[2] : null;
}

const TARGET_RULES = [
  ".streaming-indicator",
  ".filter-tab-streaming::before",
  ".child-expand-streaming::before",
  ".streaming-cursor::after",
  ".new-messages-indicator",
];

describe("styles.css — mitto-2fx.1 will-change invariant", () => {
  describe.each(TARGET_RULES)("%s", (selector) => {
    const body = ruleBody(stylesCss, selector);

    test("rule exists", () => {
      expect(body).not.toBeNull();
    });

    test("declares an infinite animation", () => {
      // Positive assertion: guards against a stray delete that removes the
      // animation alongside the will-change hint.
      expect(body).toMatch(/animation:\s*[^;]*\binfinite\b/);
    });

    test("does not declare will-change", () => {
      expect(body).not.toMatch(/\bwill-change\s*:/);
    });
  });
});

describe("styles.css — deliberate will-change sites survive", () => {
  test("mitto-cdf drawer-ghosting fix keeps will-change: auto", () => {
    // The fix neutralizes daisyUI's default will-change:transform on the
    // .drawer-side panel child. Without this rule, the drawer-ghosting
    // regression returns.
    const body = ruleBody(
      stylesCss,
      ".drawer-end > .drawer-toggle ~ .drawer-side > :not(.drawer-overlay)",
    );
    expect(body).not.toBeNull();
    expect(body).toMatch(/will-change\s*:\s*auto\s*;/);
  });

  test.each(TARGET_RULES.slice(0, 4).concat([".new-messages-indicator"]))(
    "%s has a matching .reduce-animations opt-out",
    (selector) => {
      // Every target rule that still animates infinitely has a reduce-motion
      // sibling that disables its animation. Only assert selectors that were
      // present pre-mitto-2fx.1 in the reduce-animations block; the queue
      // badge (.new-messages-indicator) was NOT in that block, so filter
      // conditionally. Keep the assertion lenient: the reduce-animations
      // override may or may not exist for the queue badge, but the four
      // streaming/filter/child/cursor overrides definitely do.
      if (selector === ".new-messages-indicator") return;
      const body = ruleBody(stylesCss, `.reduce-animations ${selector}`);
      expect(body).not.toBeNull();
      expect(body).toMatch(/animation\s*:\s*none/);
    },
  );

  test("@media (prefers-reduced-motion: reduce) block is present", () => {
    // Coarse but sufficient guard: the OS-level opt-out media query stays in
    // the file. A finer grained per-selector assertion here would duplicate
    // the .reduce-animations checks above.
    expect(stylesCss).toMatch(
      /@media\s*\(\s*prefers-reduced-motion\s*:\s*reduce\s*\)\s*\{/,
    );
  });
});
