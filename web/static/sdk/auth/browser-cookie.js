/**
 * `browserCookieAuth` — the double-submit-cookie CSRF adapter, ported from
 * `web/static/utils/csrf.js` behind the adapter interface `core/transport.js`
 * already calls. Environment-agnostic per docs/devel/js-client-library.md
 * §4: `document`/`document.cookie` are NEVER read here — every browser
 * global is injected by the caller (see `env/browser.js`'s
 * `browserCookieReader`, the one file under `sdk/` allowed to touch
 * `document.cookie` directly).
 *
 * Protocol (unchanged from utils/csrf.js): the server sets a CSRF token in
 * a cookie readable by JavaScript; this adapter echoes it back in the
 * `X-CSRF-Token` header on state-changing requests (POST/PUT/PATCH/DELETE);
 * the server verifies the header matches the cookie. Stateless — no
 * server-side token storage.
 *
 * Unlike `utils/csrf.js`, this adapter never redirects on 401 itself: that
 * is host policy (js-client-library.md §4), wired via `core/config.js`'s
 * `onUnauthorized` hook, which `core/transport.js` calls with a typed
 * `MittoAuthError` on every 401.
 */

const DEFAULT_COOKIE_NAME = "mitto_csrf";
const DEFAULT_HEADER_NAME = "X-CSRF-Token";
const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

function needsCSRFProtection(method) {
  return !SAFE_METHODS.has((method || "GET").toUpperCase());
}

/**
 * @param {object} options
 * @param {function(string): (string|null)} options.getCookie - Reads a
 *   named cookie's value (or null if absent). Injected so this file never
 *   touches `document.cookie` directly; see `env/browser.js`.
 * @param {function} options.fetch - Fetch implementation used ONLY to fetch
 *   a fresh CSRF token when no cookie exists yet (typically the same
 *   `fetch` passed to `createClient()`).
 * @param {string} options.csrfTokenUrl - Full URL of the CSRF-token
 *   endpoint (e.g. built from `endpoints.misc.csrfToken()`).
 * @param {string} [options.cookieName] - Defaults to "mitto_csrf".
 * @param {string} [options.headerName] - Defaults to "X-CSRF-Token".
 * @returns {{authorize: function(object): Promise<object>, onUnauthorized: function(): void}}
 */
export function browserCookieAuth({
  getCookie,
  fetch: fetchImpl,
  csrfTokenUrl,
  cookieName = DEFAULT_COOKIE_NAME,
  headerName = DEFAULT_HEADER_NAME,
}) {
  // Single-flight in-flight fetch, scoped to this adapter instance (unlike
  // utils/csrf.js's module-global `tokenPromise`, so multiple clients never
  // share cache state).
  let tokenPromise = null;

  async function fetchCSRFToken() {
    const response = await fetchImpl(csrfTokenUrl, { credentials: "include" });
    if (!response.ok) {
      throw new Error("Failed to fetch CSRF token");
    }
    const data = await response.json();
    return data.token;
  }

  async function getToken() {
    const cookieToken = getCookie(cookieName);
    if (cookieToken) return cookieToken;

    if (tokenPromise) return tokenPromise;

    tokenPromise = fetchCSRFToken();
    try {
      return await tokenPromise;
    } finally {
      tokenPromise = null;
    }
  }

  return {
    /**
     * Always includes credentials (session cookie); adds the CSRF header
     * for state-changing methods only, resolved cookie-first with a
     * single-flight fallback fetch.
     */
    async authorize({ method } = {}) {
      if (!needsCSRFProtection(method)) {
        return { credentials: "include" };
      }
      const token = await getToken();
      return {
        credentials: "include",
        headers: { [headerName]: token },
      };
    },

    /** Clears any in-flight token fetch so the next request refetches. */
    onUnauthorized(_error) {
      tokenPromise = null;
    },
  };
}
