/**
 * Unit tests for linkifyBeadsRefs utility.
 */

import { linkifyBeadsRefs } from "./beadsLinkify.js";

const KNOWN_IDS = new Set([
  "mitto-aaa",
  "mitto-123",
  "mitto-123.4",
  "mitto-uxn",
  "on-call-u2i",
]);
const META = new Map([
  ["mitto-aaa", { title: "Test Issue", status: "open" }],
  ["mitto-123", { title: "Bug Report", status: "closed" }],
  ["mitto-123.4", { title: "Bug Report sub-task", status: "open" }],
  ["mitto-uxn", { title: "Beads Linking", status: "open" }],
  ["on-call-u2i", { title: "Hyphenated prefix project", status: "open" }],
]);

function makeDiv(html) {
  const div = document.createElement("div");
  div.innerHTML = html;
  return div;
}

describe("linkifyBeadsRefs", () => {
  test("wraps known ID in plain text with a.beads-link", () => {
    const root = makeDiv("<p>See mitto-aaa for details.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    const links = root.querySelectorAll("a.beads-link");
    expect(links).toHaveLength(1);
    expect(links[0].dataset.beadsId).toBe("mitto-aaa");
    expect(links[0].textContent).toBe("mitto-aaa");
  });

  test("does not wrap unknown IDs", () => {
    const root = makeDiv("<p>See foo-bar for details.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    expect(root.querySelectorAll("a.beads-link")).toHaveLength(0);
  });

  test("wraps ID inside inline <code> (markdown backticks)", () => {
    const root = makeDiv("<p>Run <code>mitto-aaa</code> check.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    const links = root.querySelectorAll("a.beads-link");
    expect(links).toHaveLength(1);
    expect(links[0].dataset.beadsId).toBe("mitto-aaa");
    expect(links[0].textContent).toBe("mitto-aaa");
    // The link stays nested inside the original <code> element.
    expect(links[0].closest("code")).toBeTruthy();
  });

  test("does not wrap ID inside <pre>", () => {
    const root = makeDiv("<pre>mitto-aaa</pre>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    expect(root.querySelectorAll("a.beads-link")).toHaveLength(0);
  });

  test("does not wrap ID inside fenced code block (<pre><code>)", () => {
    const root = makeDiv("<pre><code>bd show mitto-aaa</code></pre>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    expect(root.querySelectorAll("a.beads-link")).toHaveLength(0);
  });

  test("does not wrap ID inside existing <a>", () => {
    const root = makeDiv('<p><a href="#">mitto-aaa</a> link.</p>');
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    // The <a> already exists; no beads-link should be added
    const beadsLinks = root.querySelectorAll("a.beads-link");
    expect(beadsLinks).toHaveLength(0);
  });

  test("is idempotent — running twice does not double-wrap", () => {
    const root = makeDiv("<p>mitto-aaa is tracked.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    expect(root.querySelectorAll("a.beads-link")).toHaveLength(1);
  });

  test("wraps multiple distinct IDs in one paragraph", () => {
    const root = makeDiv("<p>Issues mitto-aaa and mitto-uxn are related.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    expect(root.querySelectorAll("a.beads-link")).toHaveLength(2);
  });

  test("preserves surrounding text", () => {
    const root = makeDiv("<p>Before mitto-aaa after.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    expect(root.querySelector("p").textContent).toBe("Before mitto-aaa after.");
  });

  test("sets data-beads-id to lowercased ID", () => {
    const root = makeDiv("<p>MITTO-AAA is uppercase.</p>");
    // The regex is case-insensitive; the ID in the set is lowercase
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    const link = root.querySelector("a.beads-link");
    expect(link).toBeTruthy();
    expect(link.dataset.beadsId).toBe("mitto-aaa");
    // Visible text should preserve original casing
    expect(link.textContent).toBe("MITTO-AAA");
  });

  test("no-ops when ids Set is empty", () => {
    const root = makeDiv("<p>mitto-aaa here.</p>");
    linkifyBeadsRefs(root, new Set(), META);
    expect(root.querySelectorAll("a.beads-link")).toHaveLength(0);
  });

  test("no-ops when rootEl is null", () => {
    expect(() => linkifyBeadsRefs(null, KNOWN_IDS, META)).not.toThrow();
  });

  test("longest match: mitto-123.4 links to mitto-123.4, not mitto-123", () => {
    const root = makeDiv("<p>See mitto-123.4 for details.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    const links = root.querySelectorAll("a.beads-link");
    expect(links).toHaveLength(1);
    expect(links[0].dataset.beadsId).toBe("mitto-123.4");
    expect(links[0].textContent).toBe("mitto-123.4");
  });

  test("mitto-123 still links when it appears alone (not as a prefix)", () => {
    const root = makeDiv("<p>See mitto-123 here.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    const links = root.querySelectorAll("a.beads-link");
    expect(links).toHaveLength(1);
    expect(links[0].dataset.beadsId).toBe("mitto-123");
  });

  // mitto-rix: CANDIDATE_RE only matched a single hyphen, so hyphenated-prefix
  // project IDs like "on-call-u2i" were truncated to "on-call" and failed the
  // known-IDs membership check, producing no link — anywhere, including tables.
  test("mitto-rix: wraps hyphenated-prefix ID (on-call-u2i) in plain text", () => {
    const root = makeDiv("<p>See on-call-u2i for details.</p>");
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    const links = root.querySelectorAll("a.beads-link");
    expect(links).toHaveLength(1);
    expect(links[0].dataset.beadsId).toBe("on-call-u2i");
    expect(links[0].textContent).toBe("on-call-u2i");
  });

  test("mitto-rix: wraps hyphenated-prefix ID (on-call-u2i) inside a markdown table cell", () => {
    const root = makeDiv(
      "<table><tr><td>on-call-u2i</td><td>Fix the thing</td></tr></table>",
    );
    linkifyBeadsRefs(root, KNOWN_IDS, META);
    const links = root.querySelectorAll("a.beads-link");
    expect(links).toHaveLength(1);
    expect(links[0].dataset.beadsId).toBe("on-call-u2i");
  });

  // mitto-h8a.2: linkifyBeadsRefs now returns the list of newly-wrapped
  // lowercased IDs so callers (e.g. Message.js) can warm the beads cache
  // via preloadBeadsIssues. The additions below pin that contract.
  describe("return value (mitto-h8a.2)", () => {
    test("returns array of lowercased IDs newly wrapped", () => {
      const root = makeDiv("<p>See mitto-aaa and mitto-uxn here.</p>");
      const result = linkifyBeadsRefs(root, KNOWN_IDS, META);
      expect(Array.isArray(result)).toBe(true);
      expect(result.sort()).toEqual(["mitto-aaa", "mitto-uxn"]);
    });

    test("returns [] when rootEl is null", () => {
      expect(linkifyBeadsRefs(null, KNOWN_IDS, META)).toEqual([]);
    });

    test("returns [] when ids Set is empty", () => {
      const root = makeDiv("<p>mitto-aaa here.</p>");
      expect(linkifyBeadsRefs(root, new Set(), META)).toEqual([]);
    });

    test("returns [] when no known IDs match", () => {
      const root = makeDiv("<p>See foo-bar for details.</p>");
      expect(linkifyBeadsRefs(root, KNOWN_IDS, META)).toEqual([]);
    });

    test("deduplicates: same ID mentioned twice returns single entry", () => {
      const root = makeDiv("<p>mitto-aaa then mitto-aaa again.</p>");
      const result = linkifyBeadsRefs(root, KNOWN_IDS, META);
      expect(result).toEqual(["mitto-aaa"]);
    });

    test("lowercases IDs regardless of input casing", () => {
      const root = makeDiv("<p>MITTO-AAA is here.</p>");
      const result = linkifyBeadsRefs(root, KNOWN_IDS, META);
      expect(result).toEqual(["mitto-aaa"]);
    });

    test("idempotent second call returns [] (nothing newly wrapped)", () => {
      const root = makeDiv("<p>mitto-aaa here.</p>");
      const first = linkifyBeadsRefs(root, KNOWN_IDS, META);
      const second = linkifyBeadsRefs(root, KNOWN_IDS, META);
      expect(first).toEqual(["mitto-aaa"]);
      expect(second).toEqual([]);
    });
  });
});
