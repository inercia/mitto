/**
 * Vendor Library Configuration
 *
 * This file defines the versions and URLs for all vendor libraries used by Mitto.
 * It ensures consistency between locally bundled files and CDN-loaded versions.
 *
 * IMPORTANT: When updating a library version:
 * 1. Update the version in VERSIONS below
 * 2. Download the new version from jsdelivr (see README.md)
 * 3. Replace the corresponding file in this directory
 * 4. Test both local and CDN loading modes
 */

// =============================================================================
// Version Configuration
// =============================================================================

/**
 * Library versions - MUST match the locally bundled vendor files.
 * These versions are used to construct CDN URLs for external access.
 */
export const VERSIONS = {
  preact: "10.19.3",
  htm: "3.1.1",
  marked: "11.1.1",
  dompurify: "3.0.8",
  // Mermaid is loaded on-demand, not bundled locally
  mermaid: "11",
};

// =============================================================================
// CDN Configuration
// =============================================================================

/** Base URL for jsdelivr CDN */
const CDN_BASE = "https://cdn.jsdelivr.net/npm";

/**
 * CDN URLs for ES module versions of each library.
 * These are used when accessing Mitto through external connections (Tailscale, etc.)
 */
export const CDN_URLS = {
  preact: `${CDN_BASE}/preact@${VERSIONS.preact}/dist/preact.mjs`,
  preactHooks: `${CDN_BASE}/preact@${VERSIONS.preact}/hooks/dist/hooks.mjs`,
  htm: `${CDN_BASE}/htm@${VERSIONS.htm}/dist/htm.mjs`,
  marked: `${CDN_BASE}/marked@${VERSIONS.marked}/lib/marked.esm.js`,
  dompurify: `${CDN_BASE}/dompurify@${VERSIONS.dompurify}/dist/purify.es.mjs`,
  mermaid: `${CDN_BASE}/mermaid@${VERSIONS.mermaid}/dist/mermaid.min.js`,
};

// =============================================================================
// Local File Configuration
// =============================================================================

/**
 * Local vendor file paths (relative to this config file's directory).
 * These are used for native app and local development.
 */
export const LOCAL_URLS = {
  preact: "./preact.js",
  preactHooks: "./preact-hooks.js",
  htm: "./htm.js",
  marked: "./marked.js",
  dompurify: "./dompurify.js",
  // Mermaid is not bundled locally - always loaded from CDN on demand
};

// =============================================================================
// Download URLs (for updating local files)
// =============================================================================

/**
 * Direct download URLs for updating local vendor files.
 * Use these with curl/wget to download new versions.
 *
 * Example:
 *   curl -o preact.js "https://cdn.jsdelivr.net/npm/preact@10.19.3/dist/preact.mjs"
 */
export const DOWNLOAD_URLS = {
  preact: CDN_URLS.preact,
  preactHooks: CDN_URLS.preactHooks,
  htm: CDN_URLS.htm,
  marked: CDN_URLS.marked,
  dompurify: CDN_URLS.dompurify,
};

