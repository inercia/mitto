// Mitto Web Interface — path display helpers.
//
// Pure functions for shortening absolute filesystem paths for UI display.
// No window/document access; safe to import from any module (including under
// jsdom test runs).

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
