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
export function browserCookieAuth({ getCookie, fetch: fetchImpl, csrfTokenUrl, cookieName, headerName, }: {
    getCookie: (arg0: string) => (string | null);
    fetch: Function;
    csrfTokenUrl: string;
    cookieName?: string;
    headerName?: string;
}): {
    authorize: (arg0: object) => Promise<object>;
    onUnauthorized: () => void;
};
