// Mitto Web Interface - API Configuration
// Provides the API prefix for all API calls and WebSocket connections

/**
 * Get the API prefix for all API endpoints.
 * This is injected by the server into the HTML page.
 * @returns {string} The API prefix (e.g., "/mitto")
 */
export function getApiPrefix() {
  return window.mittoApiPrefix || "";
}

/**
 * Build an API URL with the configured prefix.
 * @param {string} path - The API path (e.g., "/api/sessions")
 * @returns {string} The full URL with prefix (e.g., "/mitto/api/sessions")
 */
export function apiUrl(path) {
  const prefix = getApiPrefix();
  // Ensure path starts with /
  if (!path.startsWith("/")) {
    path = "/" + path;
  }
  return prefix + path;
}

/**
 * Build a WebSocket URL with the configured prefix.
 * @param {string} path - The WebSocket path (e.g., "/ws" or "/api/events")
 * @returns {string} The full WebSocket URL
 */
export function wsUrl(path) {
  const prefix = getApiPrefix();
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  // Ensure path starts with /
  if (!path.startsWith("/")) {
    path = "/" + path;
  }
  return `${protocol}//${window.location.host}${prefix}${path}`;
}

/**
 * Extract a human-readable error message from a parsed API response body.
 * Handles the canonical JSON error envelope ({"error":{"code","message"}}),
 * a legacy flat-string error ({"error":"..."}), a top-level {"message":"..."},
 * and falls back to the provided default.
 * @param {*} data - The parsed response body (object, or anything).
 * @param {string} fallback - Message to use when none can be extracted.
 * @returns {string} The extracted message or the fallback.
 */
export function errorMessageFromData(data, fallback) {
  return (
    data?.error?.message ||
    (typeof data?.error === "string" ? data.error : undefined) ||
    data?.message ||
    fallback
  );
}
