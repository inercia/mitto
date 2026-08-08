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
