/**
 * Unit tests for normalizeMarkedMermaidBlocks (mitto-xbz).
 *
 * marked.js emits <pre><code class="language-mermaid"> for a fenced mermaid
 * block, but the shared renderer (renderMermaidInContainer in
 * preact-loader.js) only recognizes the backend goldmark shape
 * <pre class="mermaid">. This helper rewrites the former into the latter,
 * in place, so the one shared renderer/cache/theme/error pipeline applies to
 * both shapes.
 */

import { normalizeMarkedMermaidBlocks } from "./mermaidNormalize.js";

function makeDiv(html) {
  const div = document.createElement("div");
  div.innerHTML = html;
  return div;
}

describe("normalizeMarkedMermaidBlocks", () => {
  test("converts marked's pre>code.language-mermaid into pre.mermaid with the decoded source", () => {
    const root = makeDiv(
      '<pre><code class="language-mermaid">graph LR\n  A --&gt; B</code></pre>',
    );
    normalizeMarkedMermaidBlocks(root);

    // The original <pre><code> pair is fully replaced, not just re-classed.
    expect(root.querySelectorAll("code")).toHaveLength(0);

    const mermaidBlocks = root.querySelectorAll("pre.mermaid");
    expect(mermaidBlocks).toHaveLength(1);
    // textContent decodes the &gt; entity, matching what a native
    // <pre class="mermaid"> from the backend renderer would contain.
    expect(mermaidBlocks[0].textContent).toBe("graph LR\n  A --> B");
  });

  test("accepts a bare code.mermaid class", () => {
    const root = makeDiv(
      '<pre><code class="mermaid">graph TD\n  X --&gt; Y</code></pre>',
    );
    normalizeMarkedMermaidBlocks(root);
    expect(root.querySelectorAll("pre.mermaid")).toHaveLength(1);
    expect(root.querySelector("pre.mermaid").textContent).toBe(
      "graph TD\n  X --> Y",
    );
  });

  test("accepts language-mermaid combined with other classes (e.g. hljs)", () => {
    const root = makeDiv(
      '<pre><code class="hljs language-mermaid">sequenceDiagram</code></pre>',
    );
    normalizeMarkedMermaidBlocks(root);
    expect(root.querySelectorAll("pre.mermaid")).toHaveLength(1);
    expect(root.querySelector("pre.mermaid").textContent).toBe(
      "sequenceDiagram",
    );
  });

  test("handles multiple mermaid blocks in the same container", () => {
    const root = makeDiv(
      '<pre><code class="language-mermaid">graph LR\n  A --&gt; B</code></pre>' +
        "<p>some text</p>" +
        '<pre><code class="language-mermaid">graph TD\n  C --&gt; D</code></pre>',
    );
    normalizeMarkedMermaidBlocks(root);
    const mermaidBlocks = root.querySelectorAll("pre.mermaid");
    expect(mermaidBlocks).toHaveLength(2);
    expect(mermaidBlocks[0].textContent).toBe("graph LR\n  A --> B");
    expect(mermaidBlocks[1].textContent).toBe("graph TD\n  C --> D");
    // Non-mermaid content in between is left completely untouched.
    expect(root.querySelector("p").textContent).toBe("some text");
  });

  test("leaves non-mermaid pre/code blocks untouched", () => {
    const root = makeDiv(
      '<pre><code class="language-js">const x = 1;</code></pre>',
    );
    normalizeMarkedMermaidBlocks(root);
    expect(root.querySelectorAll("pre.mermaid")).toHaveLength(0);
    expect(root.querySelectorAll("code.language-js")).toHaveLength(1);
  });

  test("is a no-op on a native pre.mermaid block (backend goldmark shape)", () => {
    const root = makeDiv('<pre class="mermaid">graph LR\n  A --&gt; B</pre>');
    normalizeMarkedMermaidBlocks(root);
    expect(root.querySelectorAll("pre.mermaid")).toHaveLength(1);
    expect(root.querySelector("pre.mermaid").textContent).toBe(
      "graph LR\n  A --> B",
    );
  });

  test("ignores a <code class=language-mermaid> that is not inside a <pre>", () => {
    // Guards the `pre.tagName !== "PRE"` check: querySelectorAll always
    // matches `pre > code`, but stay defensive against malformed markup.
    const root = makeDiv('<code class="language-mermaid">graph LR</code>');
    expect(() => normalizeMarkedMermaidBlocks(root)).not.toThrow();
    expect(root.querySelectorAll("pre.mermaid")).toHaveLength(0);
  });

  test("does not throw on a null, undefined, or non-element container", () => {
    expect(() => normalizeMarkedMermaidBlocks(null)).not.toThrow();
    expect(() => normalizeMarkedMermaidBlocks(undefined)).not.toThrow();
    expect(() => normalizeMarkedMermaidBlocks({})).not.toThrow();
  });
});
