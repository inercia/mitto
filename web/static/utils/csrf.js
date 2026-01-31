// Mitto Web Interface - CSRF Protection Utilities
// Handles CSRF token management and secure fetch operations
//
// Uses the double-submit cookie pattern:
// 1. Server sets a CSRF token in a cookie (readable by JavaScript)
// 2. Frontend reads the cookie and sends the same value in a header
// 3. Server verifies header matches cookie
//
// This is stateless - the server doesn't need to store tokens.
// Security comes from the fact that an attacker cannot read the cookie
// value due to same-origin policy, so they cannot set the correct header.

import { getApiPrefix } from "./api.js";

const CSRF_COOKIE_NAME = "mitto_csrf";
const CSRF_HEADER_NAME = "X-CSRF-Token";

// Track if we're currently fetching a token to avoid duplicate requests
let tokenPromise = null;

/**
 * Get the CSRF token from the cookie
 * @returns {string|null} The token or null if not found
 */
function getTokenFromCookie() {
  const match = document.cookie.match(
    new RegExp("(^| )" + CSRF_COOKIE_NAME + "=([^;]+)"),
  );
  return match ? match[2] : null;
}

/**
 * Fetch a new CSRF token from the server (sets the cookie)
 * @returns {Promise<string>} The CSRF token
 */
async function fetchCSRFToken() {
  const prefix = getApiPrefix();
  const response = await fetch(prefix + "/api/csrf-token", {
    credentials: "same-origin", // Include cookies
  });
  if (!response.ok) {
    throw new Error("Failed to fetch CSRF token");
  }
  const data = await response.json();
  return data.token;
}

/**
 * Get a valid CSRF token, fetching from server if no cookie exists.
 * @returns {Promise<string>} The CSRF token
 */
export async function getCSRFToken() {
  // Check if we have a token in the cookie
  const cookieToken = getTokenFromCookie();
  if (cookieToken) {
    return cookieToken;
  }

  // No cookie - need to fetch a token from the server
  // If we're already fetching, wait for that request
  if (tokenPromise) {
    return tokenPromise;
  }

  // Fetch a new token (this will set the cookie)
  tokenPromise = fetchCSRFToken();
  try {
    return await tokenPromise;
  } finally {
    tokenPromise = null;
  }
}

/**
 * Clear the cached CSRF token (e.g., on logout)
 * Note: This doesn't clear the cookie, just any in-memory state
 */
export function clearCSRFToken() {
  tokenPromise = null;
}

/**
 * Check if a request method requires CSRF protection
 * @param {string} method - The HTTP method
 * @returns {boolean} True if CSRF protection is required
 */
function needsCSRFProtection(method) {
  const upperMethod = method?.toUpperCase() || "GET";
  return ["POST", "PUT", "PATCH", "DELETE"].includes(upperMethod);
}

/**
 * Redirect to the login page.
 * Clears the CSRF token cache before redirecting.
 */
function redirectToLogin() {
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

  // Always include credentials for session cookie handling
  const fetchOptions = {
    ...options,
    credentials: "same-origin",
  };

  let response;

  // Only add CSRF token for state-changing methods
  if (needsCSRFProtection(method)) {
    const token = await getCSRFToken();

    // Create a copy of headers to avoid mutating the original
    const headers = new Headers(options.headers || {});
    headers.set(CSRF_HEADER_NAME, token);

    response = await fetch(url, {
      ...fetchOptions,
      headers,
    });
  } else {
    // For safe methods, just use regular fetch with credentials
    response = await fetch(url, fetchOptions);
  }

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
    credentials: "same-origin",
  });
  return handleUnauthorized(response);
}
