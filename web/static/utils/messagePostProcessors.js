// Mitto Web Interface - Agent Message Post-Processors (mitto-sus.5)
//
// Idempotent per-block post-processors extracted from AgentMessageBlock's
// useEffect (Message.js). Each processor is scoped to a single block's DOM
// subtree and relies on an existing DOM marker as its own idempotence guard,
// so calling it again on content it has already handled is a cheap no-op:
//   - processTables:      `.table-wrapper` ancestor check
//   - processMermaid:     `data-mermaid-processed="true"` attribute
//     (checked by the renderer installed in preact-loader.js) plus its
//     content-hash SVG cache for repeats
//   - processBeadsLinks:  `<a>`/`<pre>`/`.beads-link` ancestor check
//     (beadsLinkify.js's hasSkipAncestor)
//
// Keeping these as small pure(ish) functions lets AgentMessageBlock's effect
// stay a thin orchestrator, and lets each processor's cost and idempotence be
// unit-tested in isolation (messagePostProcessors.test.js) without mounting
// the full component.

import { linkifyBeadsRefs } from "./beadsLinkify.js";
import { getBeadsKnownIds } from "./beadsKnownIds.js";
import { preloadBeadsIssues } from "./beadsPreload.js";
import { perfMark, perfMeasure } from "./perfMarks.js";

/**
 * Wrap every not-yet-wrapped `<table>` in `blockEl` with a `.table-wrapper`
 * div. Idempotent: a table already inside `.table-wrapper` is skipped, so
 * re-running against unchanged content wraps nothing.
 * @param {Element|null} blockEl
 * @returns {number} Count of tables newly wrapped this call.
 */
export function processTables(blockEl) {
  if (!blockEl) return 0;
  perfMark("render.postprocess.tables.start");
  const tables = blockEl.querySelectorAll("table:not(.table-wrapper table)");
  let processed = 0;
  tables.forEach((table) => {
    if (table.parentElement?.classList.contains("table-wrapper")) return;
    const wrapper = document.createElement("div");
    wrapper.className = "table-wrapper";
    table.parentNode.insertBefore(wrapper, table);
    wrapper.appendChild(table);
    processed++;
  });
  perfMark("render.postprocess.tables.end", { processed });
  perfMeasure(
    "render.postprocess.tables",
    "render.postprocess.tables.start",
    "render.postprocess.tables.end",
  );
  return processed;
}

/**
 * Trigger Mermaid rendering for `blockEl` via the global renderer installed
 * by preact-loader.js. Idempotent: the renderer itself skips any
 * `pre.mermaid[data-mermaid-processed="true"]` block and reuses the
 * content-hash SVG cache for diagrams it has already rendered.
 * @param {Element|null} blockEl
 * @returns {number} Count of not-yet-processed diagram blocks found at call
 *   time (an upper bound on this call's rendering work — the async renderer
 *   may still short-circuit further via the SVG cache).
 */
export function processMermaid(blockEl) {
  if (!blockEl) return 0;
  perfMark("render.postprocess.mermaid.start");
  const pending = blockEl.querySelectorAll(
    'pre.mermaid:not([data-mermaid-processed="true"]), pre code.language-mermaid',
  );
  const processed = pending.length;
  if (typeof window.renderMermaidDiagrams === "function") {
    window.renderMermaidDiagrams(blockEl);
  }
  perfMark("render.postprocess.mermaid.end", { processed });
  perfMeasure(
    "render.postprocess.mermaid",
    "render.postprocess.mermaid.start",
    "render.postprocess.mermaid.end",
  );
  return processed;
}

/**
 * Linkify Beads IDs in `blockEl` and warm the issue-preview cache for each
 * newly-linked ID. Idempotent: `linkifyBeadsRefs` skips text already inside
 * an `<a>`/`<pre>`/`.beads-link` ancestor, so re-running against unchanged
 * content linkifies nothing and preloads nothing new.
 * @param {Element|null} blockEl
 * @param {string} workingDir
 * @returns {number} Count of IDs newly linkified this call.
 */
export function processBeadsLinks(blockEl, workingDir) {
  if (!blockEl) return 0;
  perfMark("render.postprocess.beadsLinks.start");
  const { ids, meta } = getBeadsKnownIds(workingDir);
  const linkified = linkifyBeadsRefs(blockEl, ids, meta);
  preloadBeadsIssues(linkified, workingDir);
  perfMark("render.postprocess.beadsLinks.end", {
    processed: linkified.length,
  });
  perfMeasure(
    "render.postprocess.beadsLinks",
    "render.postprocess.beadsLinks.start",
    "render.postprocess.beadsLinks.end",
  );
  return linkified.length;
}
