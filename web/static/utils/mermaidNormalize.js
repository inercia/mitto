// Normalizes marked.js's mermaid code-block output into the shape the
// shared Mermaid rendering pipeline expects (mitto-xbz).
//
// Two markdown pipelines exist in Mitto and they emit different HTML for a
// ```mermaid fenced block:
//   - The backend goldmark+mermaid extension (agent chat messages) emits
//     <pre class="mermaid">...</pre> placeholders directly.
//   - marked.js (used by web/static/components/beads/CommentBody.js
//     renderMarkdown() for beads descriptions/comments/notes) emits
//     <pre><code class="language-mermaid">...</code></pre> instead.
//
// renderMermaidInContainer() in web/static/preact-loader.js only looks for
// the first shape (pre.mermaid). This helper rewrites the marked shape into
// that same shape in-place, so every marked-rendered consumer gets mermaid
// support from the one shared renderer without a second rendering pipeline.
//
// Extracted into its own pure, side-effect-free (besides DOM mutation)
// module so it is unit-testable without importing the side-effectful
// preact-loader.js (CDN script injection, service worker registration).

/**
 * Find marked-shaped mermaid code blocks inside `container` and rewrite each
 * one into a `<pre class="mermaid">` element containing the decoded diagram
 * source, in place of the original `<pre><code>` pair.
 *
 * @param {HTMLElement|null} container - The container to search within.
 */
export function normalizeMarkedMermaidBlocks(container) {
  if (!container || typeof container.querySelectorAll !== "function") {
    return;
  }

  // marked emits class="language-mermaid" on the <code> element for a
  // ```mermaid fence; also accept a bare "mermaid" class for robustness.
  const codeBlocks = container.querySelectorAll(
    'pre > code[class*="language-mermaid"], pre > code.mermaid',
  );

  codeBlocks.forEach((code) => {
    const pre = code.parentElement;
    if (!pre || pre.tagName !== "PRE") return;

    // code.textContent already has HTML entities decoded, matching what
    // renderMermaidInContainer() expects to read from a native pre.mermaid.
    const source = code.textContent || "";

    const normalized = document.createElement("pre");
    normalized.className = "mermaid";
    normalized.textContent = source;

    pre.replaceWith(normalized);
  });
}
