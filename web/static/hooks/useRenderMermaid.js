// Shared trigger for the Mermaid rendering pipeline (mitto-xbz).
//
// web/static/preact-loader.js exposes window.renderMermaidDiagrams() as the
// single entry point for turning <pre class="mermaid"> (and, via
// normalizeMarkedMermaidBlocks(), marked's <pre><code class="language-mermaid">)
// blocks into rendered SVGs. Message.js already calls it directly for agent
// chat messages; this hook gives every other markdown-rendering consumer
// (beads description, notes, comments) the same trigger without duplicating
// the effect boilerplate at each call site.
const { useEffect } = window.preact;

/**
 * Render mermaid diagrams inside `ref.current` whenever `deps` change.
 *
 * @param {{current: HTMLElement|null}} ref - Ref to the container holding
 *   the already-inserted, rendered markdown HTML.
 * @param {Array} deps - Effect dependency array (e.g. the markdown source
 *   and any view/edit-mode flag that gates when the container is mounted).
 * @param {boolean} [enabled=true] - Skip triggering the render when false
 *   (e.g. while a field is in edit mode). Safe to leave true even when the
 *   container isn't mounted, or `ref` itself is null/undefined (e.g. a
 *   create-mode variant of a component that never wires up the view ref):
 *   the effect guards on both.
 */
export function useRenderMermaid(ref, deps, enabled = true) {
  useEffect(() => {
    if (!enabled) return;
    if (!ref || !ref.current) return;
    if (typeof window.renderMermaidDiagrams !== "function") return;
    window.renderMermaidDiagrams(ref.current);
  }, deps);
}
