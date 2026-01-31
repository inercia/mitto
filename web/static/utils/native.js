// Mitto Web Interface - Native Platform Utilities
// Functions for interacting with the native macOS app (when running in WebView)

// =============================================================================
// External URL Helper
// =============================================================================

/**
 * Opens an external URL in the default browser.
 * In the native macOS app, uses the bound native function.
 * In the web browser, uses window.open.
 * @param {string} url - The URL to open
 */
export function openExternalURL(url) {
  if (typeof window.mittoOpenExternalURL === "function") {
    // Native macOS app - use bound function
    window.mittoOpenExternalURL(url);
  } else {
    // Web browser - open in new tab
    window.open(url, "_blank", "noopener,noreferrer");
  }
}

// =============================================================================
// Folder Picker Helper
// =============================================================================

/**
 * Check if native folder picker is available (macOS app)
 * @returns {boolean}
 */
export function hasNativeFolderPicker() {
  return typeof window.mittoPickFolder === "function";
}

/**
 * Opens a native folder picker dialog and returns the selected path.
 * In the native macOS app, uses the bound native function.
 * Returns a Promise that resolves to the selected path or empty string if cancelled.
 * @returns {Promise<string>}
 */
export async function pickFolder() {
  if (typeof window.mittoPickFolder === "function") {
    // Native macOS app - use bound function
    // The webview binding returns a Promise
    const result = await window.mittoPickFolder();
    return result || "";
  }
  // Web browser - no native folder picker available
  // The caller should use a file input with webkitdirectory as fallback
  return "";
}

// =============================================================================
// Image Picker Helper
// =============================================================================

/**
 * Opens a native image picker dialog and returns the selected file paths.
 * In the native macOS app, uses the bound native function.
 * Returns a Promise that resolves to an array of file paths or empty array if cancelled.
 * @returns {Promise<string[]|null>} Array of paths, or null if native picker unavailable
 */
export async function pickImages() {
  if (typeof window.mittoPickImages === "function") {
    // Native macOS app - use bound function
    // The webview binding returns a Promise
    const result = await window.mittoPickImages();
    return result || [];
  }
  // Web browser - no native image picker available
  // The caller should use a file input as fallback
  return null; // null indicates native picker is not available
}

/**
 * Check if the native image picker is available (running in macOS app)
 * @returns {boolean}
 */
export function hasNativeImagePicker() {
  return typeof window.mittoPickImages === "function";
}

// =============================================================================
// Context Menu Helper
// =============================================================================

/**
 * Check if running in native macOS app (for context menu behavior)
 * @returns {boolean}
 */
export function isNativeApp() {
  return typeof window.mittoPickFolder === "function";
}
