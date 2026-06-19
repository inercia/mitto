// Mitto Web Interface - Beads Issue Linkify Utility
// Scans DOM text nodes and wraps recognized beads issue IDs with clickable links.

// Matches beads IDs including optional dot-separated sub-IDs (e.g. mitto-123.4).
// The (?:\.[a-z0-9]+)* suffix ensures longest-match: "mitto-123.4" is captured
// as a single token so it is never confused with its prefix "mitto-123".
const CANDIDATE_RE = /\b([a-z][a-z0-9]*-[a-z0-9]+(?:\.[a-z0-9]+)*)\b/gi;
const SKIP_TAGS = new Set(["A", "CODE", "PRE"]);

function hasSkipAncestor(node, rootEl) {
  let el = node.parentElement;
  while (el && el !== rootEl) {
    if (SKIP_TAGS.has(el.tagName) || el.classList.contains("beads-link")) {
      return true;
    }
    el = el.parentElement;
  }
  return false;
}

/**
 * Linkify beads issue IDs in the given DOM element.
 * Only wraps IDs present in the `ids` Set (lowercased).
 * Idempotent: skips text nodes already inside A, CODE, PRE, or .beads-link.
 * @param {Element} rootEl
 * @param {Set<string>} ids - Lowercased known IDs.
 * @param {Map<string, {title: string, status: string}>} meta
 */
export function linkifyBeadsRefs(rootEl, ids, meta) {
  if (!rootEl || !ids || ids.size === 0) return;

  const walker = document.createTreeWalker(rootEl, NodeFilter.SHOW_TEXT);
  const textNodes = [];
  let node;
  while ((node = walker.nextNode())) {
    if (!hasSkipAncestor(node, rootEl)) {
      textNodes.push(node);
    }
  }

  for (const textNode of textNodes) {
    const text = textNode.nodeValue;
    if (!text) continue;

    const parts = [];
    let lastIndex = 0;
    CANDIDATE_RE.lastIndex = 0;
    let match;
    while ((match = CANDIDATE_RE.exec(text)) !== null) {
      const idLower = match[1].toLowerCase();
      if (!ids.has(idLower)) continue;
      parts.push({ type: "text", value: text.slice(lastIndex, match.index) });
      parts.push({ type: "link", value: match[1], id: idLower });
      lastIndex = match.index + match[0].length;
    }
    if (parts.length === 0) continue;
    parts.push({ type: "text", value: text.slice(lastIndex) });

    const frag = document.createDocumentFragment();
    for (const part of parts) {
      if (part.type === "text") {
        if (part.value) frag.appendChild(document.createTextNode(part.value));
      } else {
        const a = document.createElement("a");
        a.className = "beads-link";
        a.dataset.beadsId = part.id;
        a.href = "#";
        const m = meta && meta.get(part.id);
        a.title = m ? `${m.title || part.id} (${m.status || ""})` : part.id;
        a.textContent = part.value;
        frag.appendChild(a);
      }
    }
    textNode.parentNode.replaceChild(frag, textNode);
  }
}
