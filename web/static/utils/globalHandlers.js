// Mitto Web Interface - Global Event Handlers
// This module registers global DOM event listeners as side effects on import,
// and exports predicate functions consumed by App's swipe gesture effect.

import {
  openExternalURL,
  openFileURL,
  convertFileURLToViewer,
  convertHTTPFileURLToViewer,
  isNativeApp,
  fixViewerURLIfNeeded,
} from "./index.js";

// =============================================================================
// Global Link Click Handler
// =============================================================================

// Intercept clicks on all link types and route them through the viewer.
// All file links in agent responses are now in viewer URL format.
// Backward compatibility is maintained for old file:// and /api/files? links
// from session recordings.
document.addEventListener("click", (e) => {
  // Find the closest anchor element (handles clicks on nested elements inside links)
  const link = e.target.closest("a");
  if (!link) return;

  const href = link.getAttribute("href");
  if (!href) return;

  // Handle beads issue links (inserted by linkifyBeadsRefs)
  if (link.dataset.beadsId) {
    e.preventDefault();
    e.stopPropagation();
    if (typeof window.mittoOpenBeadsIssue === "function") {
      window.mittoOpenBeadsIssue(link.dataset.beadsId);
    }
    return;
  }

  console.log("[Mitto] Link clicked:", href, "isNativeApp:", isNativeApp());

  // Handle viewer URLs (new format: /viewer.html?ws=...&path=...)
  if (href.includes("/viewer.html?")) {
    console.log("[Mitto] Handling as viewer URL");
    e.preventDefault();
    e.stopPropagation();
    if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
      // macOS native app — open in native viewer window
      const fullUrl = new URL(href, window.location.origin).href;
      window.mittoOpenViewer(fullUrl);
    } else {
      // Web browser — open in new tab
      window.open(href, "_blank", "noopener,noreferrer");
    }
    return;
  }

  // BACKWARD COMPAT: Handle old file:// URLs (from old session recordings)
  if (href.startsWith("file://")) {
    console.log("[Mitto] Handling as file:// URL (backward compat)");
    e.preventDefault();
    e.stopPropagation();
    if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
      const viewerUrl = convertFileURLToViewer(href);
      if (viewerUrl) {
        window.mittoOpenViewer(viewerUrl);
      } else {
        openFileURL(href); // fallback: open with system app
      }
    } else {
      const viewerUrl = convertFileURLToViewer(href);
      if (viewerUrl) {
        window.open(viewerUrl, "_blank", "noopener,noreferrer");
      }
    }
    return;
  }

  // BACKWARD COMPAT: Handle old /api/files? URLs (from old session recordings)
  if (href.includes("/api/files?")) {
    console.log("[Mitto] Handling as /api/files link (backward compat)");
    e.preventDefault();
    e.stopPropagation();
    const viewerUrl = convertHTTPFileURLToViewer(href);
    console.log("[Mitto] Converted to viewer URL:", viewerUrl);
    if (viewerUrl) {
      if (isNativeApp() && typeof window.mittoOpenViewer === "function") {
        window.mittoOpenViewer(new URL(viewerUrl, window.location.origin).href);
      } else {
        window.open(viewerUrl, "_blank", "noopener,noreferrer");
      }
    }
    return;
  }

  // Handle external URLs (http/https) - open in default browser
  if (href.startsWith("http://") || href.startsWith("https://")) {
    // Check if this is an old viewer URL that needs fixing (missing API prefix)
    // Old recordings may have URLs like https://host/viewer.html?... that need
    // to be converted to https://host/mitto/viewer.html?...
    const fixedUrl = fixViewerURLIfNeeded(href);
    if (fixedUrl) {
      console.log("[Mitto] Opening fixed viewer URL:", fixedUrl);
      e.preventDefault();
      e.stopPropagation();
      window.open(fixedUrl, "_blank", "noopener,noreferrer");
      return;
    }

    console.log("[Mitto] Handling as external URL");
    e.preventDefault();
    e.stopPropagation();
    openExternalURL(href);
  }
});

// =============================================================================
// Disable WebView Context Menu (macOS app only)
// =============================================================================

// In the native macOS app, disable the default WebView context menu (which shows "Reload")
// to provide a cleaner, more native app experience. This only applies to areas without
// a custom context menu handler (session items have their own context menu).
if (isNativeApp()) {
  document.addEventListener("contextmenu", (e) => {
    // Allow default behavior for text inputs and textareas (for paste, etc.)
    const tagName = e.target.tagName.toLowerCase();
    if (tagName === "input" || tagName === "textarea") {
      return;
    }
    // Allow custom context menu handlers (session items have data-has-context-menu attribute)
    const hasCustomMenu = e.target.closest("[data-has-context-menu]");
    if (hasCustomMenu) {
      return;
    }
    // Prevent the default WebView context menu
    e.preventDefault();
  });
}

// =============================================================================
// Mouse Position Tracker (for native swipe gesture hit-testing)
// =============================================================================

// Track the last known cursor position so native macOS swipe gestures can
// check whether the cursor is over horizontally scrollable content before
// triggering conversation navigation.
let lastMouseX = 0;
let lastMouseY = 0;
document.addEventListener("mousemove", (e) => {
  lastMouseX = e.clientX;
  lastMouseY = e.clientY;
});

/**
 * Returns true if the element currently under the cursor belongs to a
 * horizontally scrollable container (overflow-x: auto/scroll with actual
 * overflow). Used to suppress swipe-navigation when the user is scrolling
 * a table left/right with a two-finger trackpad gesture.
 */
export function isOverHorizontallyScrollable() {
  const el = document.elementFromPoint(lastMouseX, lastMouseY);
  if (!el) return false;
  let node = el;
  while (node) {
    const style = window.getComputedStyle(node);
    const overflowX = style.overflowX;
    if (
      (overflowX === "auto" || overflowX === "scroll") &&
      node.scrollWidth > node.clientWidth
    ) {
      return true;
    }
    node = node.parentElement;
  }
  return false;
}

/**
 * Returns true if a modal dialog overlay is currently open.
 * All modal dialogs use a fixed full-screen z-50 overlay as their backdrop.
 * Used to suppress swipe-navigation when the user is interacting with a dialog.
 */
export function isModalDialogOpen() {
  return !!document.querySelector(".fixed.inset-0.z-50");
}
