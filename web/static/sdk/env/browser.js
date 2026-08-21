/**
 * Explicit browser environment preset.
 *
 * `sdk/core/` never touches browser-only globals (`localStorage`,
 * `document.cookie`, `location`, ...) implicitly — see
 * docs/devel/js-client-library.md §4. This module is the opt-in seam: a
 * browser host that wants localStorage-backed storage and console-backed
 * logging passes this preset's output into `createClient()` explicitly.
 *
 * This file is the ONLY place under sdk/ allowed to reference `localStorage`
 * / `console` directly, and it is never imported by anything under
 * `sdk/core/`.
 */

function createLocalStorageAdapter(globals) {
  return {
    getItem: (key) => globals.localStorage.getItem(key),
    setItem: (key, value) => globals.localStorage.setItem(key, value),
    removeItem: (key) => globals.localStorage.removeItem(key),
  };
}

function createConsoleLogger(globals) {
  const c = globals.console;
  return {
    debug: (...args) => c.debug(...args),
    info: (...args) => c.info(...args),
    warn: (...args) => c.warn(...args),
    error: (...args) => c.error(...args),
  };
}

/**
 * Builds a partial `createClient()` options object wiring `storage` to
 * `localStorage` and `logger` to `console`, both resolved from the given
 * `globals` (defaults to `globalThis`). Spread the result into
 * `createClient()`'s options, e.g.:
 *
 *   createClient({ ...browserEnv(), baseUrl: "/api" })
 */
export function browserEnv(globals = globalThis) {
  return {
    storage: createLocalStorageAdapter(globals),
    logger: createConsoleLogger(globals),
  };
}

/**
 * Returns a `getCookie(name)` reader backed by `globals.document.cookie` —
 * the injectable seam `sdk/auth/browser-cookie.js`'s `browserCookieAuth`
 * requires so it never touches `document` itself (mitto-7gta.5). This is
 * the only place under `sdk/` (besides this file's `localStorage`/`console`
 * wiring above) allowed to reference `document`.
 *
 * Not bundled into `browserEnv()`'s output: `browserCookieAuth` also needs
 * `fetch` and a `csrfTokenUrl` the preset cannot know, so callers wire it
 * explicitly, e.g.:
 *
 *   import { browserCookieAuth } from "@mitto/sdk/auth/browser-cookie.js";
 *   createClient({
 *     ...browserEnv(),
 *     baseUrl: "/api",
 *     auth: browserCookieAuth({
 *       getCookie: browserCookieReader(),
 *       fetch: window.fetch.bind(window),
 *       csrfTokenUrl: "/api/csrf-token",
 *     }),
 *   })
 */
export function browserCookieReader(globals = globalThis) {
  return function getCookie(name) {
    const match = globals.document.cookie.match(new RegExp("(^| )" + name + "=([^;]+)"));
    return match ? match[2] : null;
  };
}
