// Mitto Web Interface — path helpers.
//
// Pure functions for working with absolute filesystem paths: shortening for
// UI display, and (mitto-q8fx) deciding how to route dropped file paths in
// the native macOS app. No window/document access; safe to import from any
// module (including under jsdom test runs).

/**
 * Replace the home-directory prefix in an absolute path with "~/" for display.
 * Recognizes the macOS pattern "/Users/<name>/" and the Linux pattern
 * "/home/<name>/". Windows ("C:\\Users\\<name>\\") is not currently handled.
 *
 * If the path is exactly the home directory (with or without trailing slash),
 * returns "~". If nothing matches, returns the input unchanged.
 *
 * Pure, no window/document access, safe to call on any string.
 */
export function tildifyPath(path) {
  if (typeof path !== "string" || path === "") return path;
  // Match "/Users/<name>" or "/home/<name>" as a full path segment. The name
  // segment must be non-empty and cannot contain "/".
  const m = path.match(/^(\/Users\/[^/]+|\/home\/[^/]+)(\/.*)?$/);
  if (!m) return path;
  const rest = m[2] || "";
  // Exactly the home dir (e.g. "/Users/alvaro" or "/Users/alvaro/") → "~"
  if (rest === "" || rest === "/") return "~";
  return "~" + rest;
}

/**
 * Check if a file path is inside the workspace directory.
 * @param {string} filePath - Absolute file path.
 * @param {string} workspacePath - Workspace directory path.
 * @returns {string|null} Relative path if inside workspace, null otherwise.
 *
 * Pure, no window/document access, safe to call on any string.
 */
export function getRelativePathIfInWorkspace(filePath, workspacePath) {
  if (!filePath || !workspacePath) return null;
  // Normalize paths (remove trailing slashes)
  const normalizedFile = filePath.replace(/\/+$/, "");
  const normalizedWorkspace = workspacePath.replace(/\/+$/, "");
  // Check if file is inside workspace
  if (normalizedFile.startsWith(normalizedWorkspace + "/")) {
    // Return relative path (without leading slash)
    return normalizedFile.slice(normalizedWorkspace.length + 1);
  }
  return null;
}

/**
 * Decide how to route absolute file paths dropped onto the native macOS app
 * (mitto-q8fx): paths inside the workspace are inserted as relative-path
 * text; paths outside the workspace are uploaded server-side via the
 * "from-path" endpoint instead of the blob-based FormData upload. Reading
 * the file server-side from its absolute path sidesteps WKWebView's
 * promised-file blob-read limitation (some source apps, e.g. VSCode, never
 * actually hand over bytes for a dragged file, only its metadata).
 *
 * @param {string[]} filePaths - Absolute file paths extracted from a drag event.
 * @param {string} workspacePath - Current workspace directory.
 * @returns {{insertAsText: string[], uploadFromPath: string[]}}
 *
 * Pure, no window/document access, safe to call on any string.
 */
export function routeDroppedPaths(filePaths, workspacePath) {
  const insertAsText = [];
  const uploadFromPath = [];
  for (const filePath of filePaths || []) {
    const relative = getRelativePathIfInWorkspace(filePath, workspacePath);
    if (relative !== null) {
      insertAsText.push(relative);
    } else {
      uploadFromPath.push(filePath);
    }
  }
  return { insertAsText, uploadFromPath };
}
