/**
 * SDK core config resolution — environment-agnostic per
 * docs/devel/js-client-library.md §4.
 *
 * Forbidden in this file (and everywhere under sdk/core/): `window`,
 * `document`, `document.cookie`, `localStorage`, `location`, `native.js`,
 * bare `console.*`. Everything environment-specific is injected via
 * `options` or the `globals` parameter — never read implicitly from the
 * ambient global scope beyond the explicit `globals` object passed in
 * (which defaults to `globalThis` for real callers; tests inject a bare
 * object to simulate a runtime with no ambient browser globals).
 */
import { ConfigError } from "./errors.js";

const ALLOWED_KEYS = new Set([
  "baseUrl",
  "apiPrefix",
  "fetch",
  "WebSocket",
  "storage",
  "auth",
  "logger",
  "onUnauthorized",
]);

/** Trim exactly one trailing slash (but never reduce "/" to ""). */
function trimTrailingSlash(s) {
  return s.length > 1 && s.endsWith("/") ? s.slice(0, -1) : s;
}

function normalizeBaseUrl(baseUrl) {
  if (baseUrl === undefined || baseUrl === null || baseUrl === "") return "";
  return trimTrailingSlash(String(baseUrl));
}

function normalizeApiPrefix(apiPrefix) {
  if (apiPrefix === undefined || apiPrefix === null || apiPrefix === "") {
    return "";
  }
  let p = String(apiPrefix);
  if (!p.startsWith("/")) p = "/" + p;
  return trimTrailingSlash(p);
}

/** Fresh, per-client in-memory storage adapter. Never localStorage implicitly. */
function createMemoryStorage() {
  const map = new Map();
  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => {
      map.set(key, String(value));
    },
    removeItem: (key) => {
      map.delete(key);
    },
  };
}

/** Silent no-op logger. Never bare console.*. */
function createNoopLogger() {
  const noop = () => {};
  return { debug: noop, info: noop, warn: noop, error: noop };
}

/** No-op passthrough auth adapter; concrete adapters are `.5`'s scope. */
function createNoopAuth() {
  return {
    async authorize(_request) {
      return {};
    },
  };
}

function resolveFetch(injected, globals) {
  if (injected) return injected;
  if (typeof globals.fetch === "function") {
    return globals.fetch.bind(globals);
  }
  return null;
}

/**
 * Returns a thunk that resolves the WebSocket implementation lazily: it
 * must NOT throw at createClient()/resolveConfig() time (a REST-only
 * caller should never be forced to supply one), only when a realtime
 * feature actually calls it.
 */
function resolveWebSocketThunk(injected, globals) {
  const impl = injected || globals.WebSocket || null;
  return () => {
    if (!impl) {
      throw new ConfigError(
        "No WebSocket implementation available: pass `WebSocket` to " +
          "createClient(), or use an environment where it is a global " +
          "(e.g. the browser preset), before using realtime features.",
      );
    }
    return impl;
  };
}

/**
 * Normalizes raw `createClient()` options into a stable internal config
 * object. `globals` is injectable so callers/tests can simulate a runtime
 * with no ambient browser globals; real callers should leave it as the
 * default (`globalThis`).
 */
export function resolveConfig(options = {}, globals = globalThis) {
  for (const key of Object.keys(options)) {
    if (!ALLOWED_KEYS.has(key)) {
      throw new ConfigError(`Unknown config option: "${key}"`);
    }
  }

  const fetchImpl = resolveFetch(options.fetch, globals);
  if (!fetchImpl) {
    throw new ConfigError(
      "No fetch implementation available: pass `fetch` to createClient(), " +
        "or use an environment where it is a global (e.g. the browser preset).",
    );
  }

  const config = {
    baseUrl: normalizeBaseUrl(options.baseUrl),
    apiPrefix: normalizeApiPrefix(options.apiPrefix),
    fetch: fetchImpl,
    getWebSocket: resolveWebSocketThunk(options.WebSocket, globals),
    storage: options.storage || createMemoryStorage(),
    auth: options.auth || createNoopAuth(),
    logger: options.logger || createNoopLogger(),
    onUnauthorized: options.onUnauthorized || (() => {}),
  };

  return Object.freeze(config);
}
