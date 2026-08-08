// Mitto Web Interface - CSRF Protection Utilities
// Thin shim over sdk/auth/browser-cookie.js (mitto-7gta.5) preserving this
// module's original call sites (17 importers) during the SDK migration
// (.17/.18). The double-submit-cookie protocol itself — server sets a CSRF
// token in a cookie readable by JavaScript, the frontend echoes it in a
// header, the server verifies header matches cookie — now lives in the SDK
// adapter; this file only supplies the browser globals (document.cookie,
// window.location, fetch) the SDK never touches directly, and the
// redirect-to-login 401 policy the SDK deliberately keeps out of its core.

import { endpoints } from "./endpoints.js";
import { browserCookieAuth } from "../sdk/auth/browser-cookie.js";
import { browserCookieReader } from "../sdk/env/browser.js";

const CSRF_HEADER_NAME = "X-CSRF-Token";

const auth = browserCookieAuth({
  getCookie: browserCookieReader(),
  fetch,
  csrfTokenUrl: endpoints.misc.csrfToken(),
  headerName: CSRF_HEADER_NAME,
});

/**
 * Get a valid CSRF token, fetching from server if no cookie exists.
 * @returns {Promise<string>} The CSRF token
 */
export async function getCSRFToken() {
  const patch = await auth.authorize({ method: "POST" });
  return patch.headers[CSRF_HEADER_NAME];
}

/**
 * Clear the cached CSRF token (e.g., on logout)
 * Note: This doesn't clear the cookie, just any in-memory state
 */
export function clearCSRFToken() {
  auth.onUnauthorized();
}

/**
 * Redirect to the login page.
 * Clears the CSRF token cache before redirecting.
 */
export function redirectToLogin() {
  clearCSRFToken();
  window.location.href = "/auth.html";
}

/**
 * Handle a fetch response, checking for 401 Unauthorized.
 * If 401 is received, redirects to the login page.
 * @param {Response} response - The fetch response
 * @returns {Response} The response (if not 401)
 */
function handleUnauthorized(response) {
  if (response.status === 401) {
    console.warn("Session expired or invalid, redirecting to login...");
    redirectToLogin();
    // Return a never-resolving promise to prevent further processing
    return new Promise(() => {});
  }
  return response;
}

/**
 * Secure fetch wrapper that automatically includes CSRF tokens
 * for state-changing requests (POST, PUT, PATCH, DELETE)
 * Also includes credentials for session cookie handling.
 * Automatically redirects to login on 401 Unauthorized responses.
 *
 * @param {string} url - The URL to fetch
 * @param {RequestInit} options - Fetch options
 * @returns {Promise<Response>} The fetch response
 */
export async function secureFetch(url, options = {}) {
  const method = options.method || "GET";
  const patch = await auth.authorize({ method });

  const headers = new Headers(options.headers || {});
  if (patch.headers) {
    for (const [k, v] of Object.entries(patch.headers)) headers.set(k, v);
  }

  const response = await fetch(url, {
    ...options,
    headers,
    credentials: patch.credentials || "include",
  });

  // Check for 401 and redirect to login if needed
  return handleUnauthorized(response);
}

/**
 * Initialize CSRF protection by ensuring a token cookie exists.
 * If no cookie is present, fetches one from the server.
 * Call this early in app initialization.
 * @returns {Promise<void>}
 */
export async function initCSRF() {
  try {
    // Just ensure we have a token - getCSRFToken will fetch if needed
    await getCSRFToken();
  } catch (err) {
    console.warn("Failed to initialize CSRF token:", err);
    // Don't throw - let individual requests handle failures
  }
}

/**
 * Check a fetch response for 401 Unauthorized and redirect to login if needed.
 * Use this for regular fetch calls that don't use secureFetch.
 * @param {Response} response - The fetch response to check
 * @returns {Response} The response (if not 401)
 */
export function checkAuth(response) {
  return handleUnauthorized(response);
}

/**
 * Wrapper for fetch that includes credentials and handles 401 responses.
 * Use this for GET requests that need auth checking but don't need CSRF.
 * @param {string} url - The URL to fetch
 * @param {RequestInit} options - Fetch options
 * @returns {Promise<Response>} The fetch response
 */
export async function authFetch(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    credentials: "include", // Support cross-origin requests (external access via Tailscale)
  });
  return handleUnauthorized(response);
}
