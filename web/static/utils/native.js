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

/**
 * Opens a file URL with the system default application.
 * In the native macOS app, uses the bound native function.
 * In the web browser, converts file:// to HTTP and opens in a new tab.
 * @param {string} url - The file:// URL to open
 */
export function openFileURL(url) {
  if (typeof window.mittoOpenFileURL === "function") {
    // Native macOS app - use dedicated file URL function
    window.mittoOpenFileURL(url);
  } else if (typeof window.mittoOpenExternalURL === "function") {
    // Fallback: use external URL opener (works for file:// on macOS)
    window.mittoOpenExternalURL(url);
  } else {
    // Web browser - convert file:// URL to HTTP API endpoint
    const httpUrl = convertFileURLToHTTP(url);
    if (httpUrl) {
      window.open(httpUrl, "_blank", "noopener,noreferrer");
    }
  }
}

/**
 * Converts a file:// URL to an HTTP viewer URL.
 * This is used in web browser mode where file:// URLs are blocked.
 * Opens the file in a syntax-highlighted viewer page.
 * @param {string} fileUrl - The file:// URL to convert
 * @returns {string|null} The HTTP URL for the viewer, or null if conversion failed
 */
export function convertFileURLToHTTP(fileUrl) {
  if (!fileUrl || !fileUrl.startsWith("file://")) {
    return null;
  }

  // Extract the absolute path from the file:// URL
  const absolutePath = decodeURIComponent(fileUrl.slice(7)); // Remove "file://"

  // Get the current workspace from the page state
  // The workspace is stored in the session state
  const workspace = getCurrentWorkspace();
  if (!workspace) {
    console.warn("Cannot convert file URL: no workspace available");
    return null;
  }

  // Calculate the relative path
  let relativePath = absolutePath;
  if (absolutePath.startsWith(workspace)) {
    relativePath = absolutePath.slice(workspace.length);
    if (relativePath.startsWith("/")) {
      relativePath = relativePath.slice(1);
    }
  }

  // Build the HTTP URL for the viewer page (with syntax highlighting)
  // Use workspace UUID for secure file links
  const apiPrefix = getAPIPrefix();
  const workspaceUUID = getCurrentWorkspaceUUID();
  if (!workspaceUUID) {
    console.warn("Cannot convert file URL: no workspace UUID available");
    return null;
  }
  return `${apiPrefix}/viewer.html?ws=${encodeURIComponent(workspaceUUID)}&path=${encodeURIComponent(relativePath)}`;
}

/**
 * Parses an HTTP file API URL and extracts workspace UUID and path parameters.
 * @param {string} httpUrl - The HTTP URL (e.g., /mitto/api/files?ws=...&path=...)
 * @returns {{url: URL, workspaceUUID: string, path: string, apiPrefix: string}|null} Parsed URL info, or null if parsing failed
 */
function parseHTTPFileURL(httpUrl) {
  if (!httpUrl || !httpUrl.includes("/api/files?")) {
    return null;
  }

  try {
    // Parse the URL to extract query parameters
    // Handle both absolute URLs and relative URLs
    let url;
    if (httpUrl.startsWith("http://") || httpUrl.startsWith("https://")) {
      url = new URL(httpUrl);
    } else {
      // Relative URL - use current origin
      url = new URL(httpUrl, window.location.origin);
    }

    // Support both new format (ws=UUID) and legacy format (workspace=path)
    const workspaceUUID = url.searchParams.get("ws");
    const legacyWorkspace = url.searchParams.get("workspace");
    const path = url.searchParams.get("path");

    if (!path || (!workspaceUUID && !legacyWorkspace)) {
      console.warn("Cannot parse HTTP file URL: missing ws/workspace or path");
      return null;
    }

    // Extract API prefix (e.g., /mitto from /mitto/api/files)
    const pathname = url.pathname;
    const apiIndex = pathname.indexOf("/api/files");
    const apiPrefix = apiIndex > 0 ? pathname.slice(0, apiIndex) : "";

    return { url, workspaceUUID, legacyWorkspace, path, apiPrefix };
  } catch (err) {
    console.warn("Failed to parse HTTP file URL:", err);
    return null;
  }
}

/**
 * Converts an HTTP file API URL back to a file:// URL.
 * This is used in the native macOS app to convert HTTP links to file:// URLs
 * that can be opened with the native file opener.
 * @param {string} httpUrl - The HTTP URL (e.g., /mitto/api/files?ws=...&path=...)
 * @returns {string|null} The file:// URL, or null if conversion failed
 */
export function convertHTTPFileURLToFile(httpUrl) {
  const parsed = parseHTTPFileURL(httpUrl);
  if (!parsed) {
    return null;
  }

  // For legacy format, we have the workspace path directly
  // For new format, get workspace path from current session state
  let workspacePath;
  if (parsed.legacyWorkspace) {
    workspacePath = parsed.legacyWorkspace;
  } else {
    workspacePath = getCurrentWorkspace();
    if (!workspacePath) {
      console.warn(
        "Cannot convert HTTP file URL to file://: workspace path not available",
      );
      return null;
    }
  }

  // Build the absolute path
  let absolutePath = workspacePath;
  if (!absolutePath.endsWith("/")) {
    absolutePath += "/";
  }
  absolutePath += parsed.path;

  // Return as file:// URL
  return "file://" + absolutePath;
}

/**
 * Converts an HTTP file API URL to a viewer page URL.
 * This is used in the web browser to open files in the syntax-highlighted viewer.
 * @param {string} httpUrl - The HTTP URL (e.g., /mitto/api/files?ws=...&path=...)
 * @returns {string|null} The viewer URL, or null if conversion failed
 */
export function convertHTTPFileURLToViewer(httpUrl) {
  const parsed = parseHTTPFileURL(httpUrl);
  if (!parsed) {
    return null;
  }

  // Build the viewer URL using the same origin and API prefix
  // Static files are served at the same prefix as the API (e.g., /mitto/viewer.html)
  //
  // IMPORTANT: Old recordings may have links without the API prefix (e.g., /api/files?...)
  // When the parsed apiPrefix is empty but we know the current page has a prefix,
  // we should use the current prefix to ensure the viewer URL works correctly.
  const viewerUrl = new URL(parsed.url.origin);
  const apiPrefix = parsed.apiPrefix || getAPIPrefix();
  viewerUrl.pathname = apiPrefix + "/viewer.html";

  // Use UUID if available, otherwise fall back to legacy workspace path
  if (parsed.workspaceUUID) {
    viewerUrl.searchParams.set("ws", parsed.workspaceUUID);
  } else if (parsed.legacyWorkspace) {
    viewerUrl.searchParams.set("workspace", parsed.legacyWorkspace);
  }
  viewerUrl.searchParams.set("path", parsed.path);

  return viewerUrl.toString();
}

/**
 * Gets the current workspace directory from the active session.
 * @returns {string|null} The workspace directory, or null if not available
 */
function getCurrentWorkspace() {
  // Try to get from global state (set by the app)
  if (window.mittoCurrentWorkspace) {
    return window.mittoCurrentWorkspace;
  }
  // Fallback: try to get from session storage
  const stored = sessionStorage.getItem("mittoCurrentWorkspace");
  if (stored) {
    return stored;
  }
  return null;
}

/**
 * Gets the current workspace UUID from the active session.
 * @returns {string|null} The workspace UUID, or null if not available
 */
function getCurrentWorkspaceUUID() {
  // Try to get from global state (set by the app)
  if (window.mittoCurrentWorkspaceUUID) {
    return window.mittoCurrentWorkspaceUUID;
  }
  // Fallback: try to get from session storage
  const stored = sessionStorage.getItem("mittoCurrentWorkspaceUUID");
  if (stored) {
    return stored;
  }
  return null;
}

/**
 * Gets the API prefix for the current page.
 * This uses the server-injected window.mittoApiPrefix value if available,
 * which is the authoritative source for the API prefix.
 * @returns {string} The API prefix (e.g., "/mitto" or "")
 */
export function getAPIPrefix() {
  // First, check for the server-injected API prefix (most reliable)
  // This is set in index.html by the server at runtime
  if (window.mittoApiPrefix !== undefined) {
    return window.mittoApiPrefix;
  }

  // Fallback: try to detect from the URL path
  // The API prefix is typically the path before /api/
  // For example, if we're at /mitto/index.html, the prefix is /mitto
  const path = window.location.pathname;
  const apiIndex = path.indexOf("/api/");
  if (apiIndex > 0) {
    return path.slice(0, apiIndex);
  }
  // Check if we're at a known prefix
  if (path.startsWith("/mitto")) {
    return "/mitto";
  }
  return "";
}

/**
 * Sets the current workspace for file URL conversion.
 * This should be called when a session is selected.
 * @param {string} workspace - The workspace directory path
 * @param {string} [uuid] - The workspace UUID (optional, for secure file links)
 */
export function setCurrentWorkspace(workspace, uuid) {
  window.mittoCurrentWorkspace = workspace;
  sessionStorage.setItem("mittoCurrentWorkspace", workspace);
  if (uuid) {
    window.mittoCurrentWorkspaceUUID = uuid;
    sessionStorage.setItem("mittoCurrentWorkspaceUUID", uuid);
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
// File Picker Helper
// =============================================================================

/**
 * Opens a native file picker dialog and returns the selected file paths.
 * In the native macOS app, uses the bound native function.
 * Returns a Promise that resolves to an array of file paths or empty array if cancelled.
 * @returns {Promise<string[]|null>} Array of paths, or null if native picker unavailable
 */
export async function pickFiles() {
  if (typeof window.mittoPickFiles === "function") {
    // Native macOS app - use bound function
    // The webview binding returns a Promise
    const result = await window.mittoPickFiles();
    return result || [];
  }
  // Web browser - no native file picker available
  // The caller should use a file input as fallback
  return null; // null indicates native picker is not available
}

/**
 * Check if the native file picker is available (running in macOS app)
 * @returns {boolean}
 */
export function hasNativeFilePicker() {
  return typeof window.mittoPickFiles === "function";
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

// =============================================================================
// URL Fix Helper for Backwards Compatibility
// =============================================================================

/**
 * Fixes old viewer URLs that are missing the API prefix.
 * Old recordings may have URLs like /viewer.html?... that need to be
 * converted to /mitto/viewer.html?... when accessed through a reverse proxy.
 *
 * @param {string} url - The URL to check and potentially fix
 * @returns {string|null} The fixed URL, or null if no fix was needed
 */
export function fixViewerURLIfNeeded(url) {
  try {
    const parsed = new URL(url);
    const pathname = parsed.pathname;

    // Check if this is a viewer.html URL without the API prefix
    if (pathname === "/viewer.html") {
      const apiPrefix = getAPIPrefix();
      if (apiPrefix && apiPrefix !== "") {
        // Fix the URL by adding the API prefix
        parsed.pathname = apiPrefix + "/viewer.html";
        console.log(
          "[Mitto] Fixed old viewer URL:",
          url,
          "->",
          parsed.toString(),
        );
        return parsed.toString();
      }
    }
    return null;
  } catch (e) {
    console.error("[Mitto] Error fixing viewer URL:", e);
    return null;
  }
}
