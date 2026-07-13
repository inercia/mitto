// Markdown rendering + link-click interception + comment body wrapper used
// across the Beads list, detail panel, and comments thread. Extracted from
// components/BeadsView.js in mitto-90f.3 E-5. This module depends on the
// frontend runtime (window.preact + htm) plus the workspace-viewer helpers
// from ../../utils/index.js so it lives under components/, not utils/.
//
// Rendered markup, class names and event shapes are preserved byte-for-byte
// from the original definitions so this move is behaviorally invisible.

const { html } = window.preact;

import {
  openExternalURL,
  openFileURL,
  buildWorkspaceViewerURL,
  openViewerUrl,
} from "../../utils/index.js";

// Render markdown text via the marked + DOMPurify globals loaded from the
// vendored CDN scripts. Returns a sanitized HTML string suitable for
// dangerouslySetInnerHTML, or null when marked/DOMPurify aren't available or
// the input is empty. Callers fall back to a <pre> block when this returns
// null so raw text is still visible.
export function renderMarkdown(text) {
  if (!text) return null;
  if (typeof window !== "undefined" && window.marked && window.DOMPurify) {
    const raw = window.marked.parse(text);
    return window.DOMPurify.sanitize(raw, { USE_PROFILES: { html: true } });
  }
  return null;
}

// Intercept clicks on links inside rendered beads markdown (description,
// comments, notes). Relative links reference files in the workspace and must
// open in the internal viewer — otherwise the SPA router follows the bare href
// and renders a blank "Not Found" page. External URLs open in the system
// browser. Returns true when a link was handled so callers can skip any
// edit-mode toggle on the surrounding container.
export function handleBeadsContentClick(e, workspacePath) {
  const target = e.target;
  const link = target && target.closest ? target.closest("a") : null;
  if (!link) return false;
  const href = link.getAttribute("href");
  if (!href || href.startsWith("#")) return false;

  // A link was clicked: prevent SPA navigation and edit-mode toggles.
  e.preventDefault();
  e.stopPropagation();

  if (/^(https?:|mailto:|tel:)/i.test(href)) {
    openExternalURL(href);
    return true;
  }
  if (/^file:/i.test(href)) {
    openFileURL(href);
    return true;
  }
  // Everything else is treated as a workspace-relative file → internal viewer.
  const viewerUrl = buildWorkspaceViewerURL(href, workspacePath);
  if (viewerUrl) openViewerUrl(viewerUrl);
  return true;
}

export function commentBody(text, workspacePath) {
  const m = renderMarkdown(text);
  if (m)
    return html`<div
      class="markdown-content text-mitto-text text-sm max-w-none"
      onClick=${(e) => handleBeadsContentClick(e, workspacePath)}
      dangerouslySetInnerHTML=${{ __html: m }}
    />`;
  return html`<pre
    class="whitespace-pre-wrap wrap-break-word text-sm text-mitto-text"
  >
${text || ""}</pre
  >`;
}
