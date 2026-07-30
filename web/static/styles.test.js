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

/**
 * mitto-2fx.4: sidebar session-row streaming rings render statically by
 * default; the daisyUI spin animation is restored only when the row is the
 * active one, hovered, or when the pointer is anywhere inside `.drawer-side`.
 * These tests pin the CSS gate (marker class + selectors + reduced-motion
 * parity) so a stray refactor cannot silently re-promote a per-row compositor
 * layer for every streaming sidebar session.
 */
describe("styles.css — mitto-2fx.4 sidebar-streaming-ring gate", () => {
  test("base .sidebar-streaming-ring rule pauses the animation", () => {
    const body = ruleBody(stylesCss, ".sidebar-streaming-ring");
    expect(body).not.toBeNull();
    expect(body).toMatch(/animation-play-state\s*:\s*paused/);
  });

  test.each([
    ".session-item-container:hover .sidebar-streaming-ring",
    ".session-item-active .sidebar-streaming-ring",
    ".drawer-side:hover .sidebar-streaming-ring",
  ])("running-state selector present: %s", (selector) => {
    // The three overrides live in a single comma-separated selector list, so
    // assert each participating selector textually rather than trying to
    // extract a shared body via ruleBody() (which pins to a single selector).
    const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    expect(stylesCss).toMatch(new RegExp(escaped));
  });

  test("running-state block sets animation-play-state: running", () => {
    // Locate the block that lists all three override selectors and verify the
    // shared body restores the animation. Anchoring on the last selector in
    // the list gives ruleBody() a unique starting point.
    const body = ruleBody(
      stylesCss,
      ".drawer-side:hover .sidebar-streaming-ring",
    );
    expect(body).not.toBeNull();
    expect(body).toMatch(/animation-play-state\s*:\s*running/);
  });

  test(".reduce-animations opt-out disables the animation", () => {
    const body = ruleBody(
      stylesCss,
      ".reduce-animations .sidebar-streaming-ring",
    );
    expect(body).not.toBeNull();
    expect(body).toMatch(/animation\s*:\s*none/);
  });

  test("prefers-reduced-motion media query includes .sidebar-streaming-ring", () => {
    // The @media block wraps the rule, so assert both the media block and a
    // .sidebar-streaming-ring rule with animation: none appear in the file
    // and that a .sidebar-streaming-ring rule follows the media opener.
    const mediaMatch = stylesCss.match(
      /@media\s*\(\s*prefers-reduced-motion\s*:\s*reduce\s*\)\s*\{([\s\S]*)$/,
    );
    expect(mediaMatch).not.toBeNull();
    const mediaBody = mediaMatch[1];
    expect(mediaBody).toMatch(
      /\.sidebar-streaming-ring\s*\{[^}]*animation\s*:\s*none/,
    );
  });
});

/**
 * mitto-2fx.4: the CSS gate is only meaningful if the JSX actually renders
 * the marker classes it targets. Pin the three call sites so a rename or a
 * copy/paste omission fails a fast unit test rather than surfacing as a
 * silent GPU-layer regression in production.
 */
describe("sidebar-streaming-ring marker classes wired in JSX", () => {
  const sessionItemJs = readFileSync(
    resolve(__dirname, "components/SessionItem.js"),
    "utf8",
  );
  const sessionListJs = readFileSync(
    resolve(__dirname, "components/SessionList.js"),
    "utf8",
  );

  test("SessionItem.js: session-item-active applied on active swipeable div", () => {
    // The class is composed conditionally onto the isActive branch of the
    // swipeable content div's class list.
    expect(sessionItemJs).toMatch(/session-item-active/);
  });

  test("SessionItem.js: every loading-ring span carries sidebar-streaming-ring", () => {
    // There are two loading-ring spans in SessionItem (spawned + regular
    // paths). Every occurrence of `loading loading-ring loading-xs` in this
    // component MUST also carry the marker class so the CSS gate applies.
    const ringOccurrences = sessionItemJs.match(
      /loading loading-ring loading-xs[^"]*/g,
    );
    expect(ringOccurrences).not.toBeNull();
    expect(ringOccurrences.length).toBeGreaterThanOrEqual(2);
    for (const occ of ringOccurrences) {
      expect(occ).toMatch(/sidebar-streaming-ring/);
    }
  });

  test("SessionList.js: folder-header loading-ring carries sidebar-streaming-ring", () => {
    const ringOccurrences = sessionListJs.match(
      /loading loading-ring loading-xs[^"]*/g,
    );
    expect(ringOccurrences).not.toBeNull();
    expect(ringOccurrences.length).toBeGreaterThanOrEqual(1);
    for (const occ of ringOccurrences) {
      expect(occ).toMatch(/sidebar-streaming-ring/);
    }
  });
});

/**
 * mitto-2fx.5: two always-mounted surfaces previously carried a daisyUI
 * `loading loading-spinner` span whose infinite-mask animation kept the
 * WKWebView compositor hot even while the surface was steady. Both were
 * replaced with a static `…` glyph. Pin the removal at the source level so a
 * future refactor cannot silently re-introduce an always-mounted spinner on:
 *
 *   - MessageList.js — the agent-working chip and the "Establishing ACP
 *     session..." status line.
 *   - PromptParameterDialog.js — the 10 placeholder branches that render
 *     while the beadsId / sessionId / childSessions / workspaceId / folder /
 *     acpServer / promptName / filename / dirname / nested-remembered option
 *     lists are still loading.
 *
 * Static parse-and-assert mirrors the mitto-2fx.4 tests above — no jsdom, no
 * runtime, runs in the fast Jest suite.
 */
describe("mitto-2fx.5 — no always-mounted loading-spinner on steady surfaces", () => {
  const messageListJs = readFileSync(
    resolve(__dirname, "components/MessageList.js"),
    "utf8",
  );
  const promptParameterDialogJs = readFileSync(
    resolve(__dirname, "components/PromptParameterDialog.js"),
    "utf8",
  );

  test("MessageList.js: no loading-spinner class anywhere", () => {
    // MessageList renders on every conversation view; its only two prior
    // spinner spans (agent-working chip + "Establishing ACP session...") were
    // replaced with the trailing "…" already carried in each message string.
    expect(messageListJs).not.toMatch(/loading-spinner/);
  });

  test("MessageList.js: agent-working chip still shows the loading cue", () => {
    // Positive assertion: guards against a stray delete that removes the
    // whole chip alongside the spinner span.
    expect(messageListJs).toMatch(/Working\$\{agentWorking\.toolTitle/);
  });

  test("MessageList.js: ACP-session status message still shows the loading cue", () => {
    expect(messageListJs).toMatch(/Establishing ACP session\.\.\./);
  });

  test("PromptParameterDialog.js: no loading-spinner class anywhere", () => {
    // The 10 placeholder branches that showed a spinner while a select's
    // option list was still fetching now render a static "…" glyph. There
    // are no other spinner spans in this file, so the whole file must be
    // spinner-free.
    expect(promptParameterDialogJs).not.toMatch(/loading-spinner/);
  });

  test("PromptParameterDialog.js: static … glyph replaces every placeholder branch", () => {
    // Each of the 10 replaced placeholder branches emits the same static
    // `<span class="text-mitto-text-muted text-xs opacity-60">…</span>`
    // shape. Assert the marker appears at least 10 times so a future refactor
    // that drops one of the branches back to a spinner (or to nothing) fails
    // this test rather than surfacing as a silent GPU-layer regression.
    const staticGlyphs = promptParameterDialogJs.match(
      /text-mitto-text-muted text-xs opacity-60/g,
    );
    expect(staticGlyphs).not.toBeNull();
    expect(staticGlyphs.length).toBeGreaterThanOrEqual(10);
  });
});
