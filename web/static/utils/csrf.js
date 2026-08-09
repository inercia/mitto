// Mitto Web Interface - CSRF Protection Utilities
// Thin shim over sdk/auth/browser-cookie.js (mitto-7gta.5). All REST call
// sites have moved onto the SDK client (mitto-7gta.17 S0-S7), which owns the
// double-submit-cookie protocol itself via getSdkClient()'s onUnauthorized
// hook (wired to redirectToLogin() below, see utils/sdkClient.js). This
// module now owns only what the SDK deliberately keeps out of its core: the
// browser globals (document.cookie, window.location) needed to read/clear
// the CSRF token cookie and to perform the redirect-to-login 401 policy.
// authFetch/secureFetch/checkAuth were deleted in slice S8 once their last
// importers (utils/prompts.js, hooks/useConversationSeeding.js) migrated.

import { endpoints } from "./endpoints.js";
import { browserCookieAuth } from "../sdk/auth/browser-cookie.js";
import { browserCookieReader } from "../sdk/env/browser.js";

const CSRF_HEADER_NAME = "X-CSRF-Token";

const auth = browserCookieAuth({
  getCookie: browserCookieReader(),
  // Late-bound on purpose: the original module called the global `fetch`
  // inside fetchCSRFToken(), so a host (or test) replacing globalThis.fetch
  // after import still applied. Passing the bare identifier would snapshot
  // the binding at module-load time and silently change that.
  fetch: (...args) => fetch(...args),
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
 * Clear the cached CSRF token (e.g., on logout).
 * Note: This doesn't clear the cookie, just any in-memory state.
 * Private: only used by redirectToLogin() below; no external importers
 * remain after slice S8 (mitto-7gta.17).
 */
function clearCSRFToken() {
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
