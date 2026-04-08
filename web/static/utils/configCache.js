// Mitto Web Interface - Config Cache
// Caches GET /api/config responses to avoid redundant network fetches during startup.
// Uses three-level deduplication / bandwidth optimisation:
//   1. Completed-response cache (TTL-based): avoids refetching within the TTL window.
//   2. In-flight Promise cache: if a request for the same key is already in flight,
//      subsequent callers share that Promise instead of issuing a duplicate HTTP request.
//      This prevents the "thundering herd" where many concurrent callers all see a cache
//      miss (response not yet stored) and each fire their own HTTP request.
//   3. HTTP ETag / If-None-Match: after the TTL expires the client sends the stored
//      ETag and the server returns 304 Not Modified when the payload is unchanged.
//      This cuts the ~35 KB body transfer to a ~300-byte round-trip for unchanged config.

import { apiUrl } from "./api.js";
import { authFetch } from "./csrf.js";

/** Cache TTL in milliseconds (30 seconds). */
const CONFIG_CACHE_TTL_MS = 30_000;

/**
 * Completed-response cache: cacheKey → { data, etag, timestamp }
 * The cache key is the acpServer string, or "__default__" when none is supplied.
 * @type {Map<string, { data: object, etag: string|null, timestamp: number }>}
 */
const configCache = new Map();

/**
 * In-flight request deduplication: cacheKey → Promise<object>
 * Populated when a fetch is started; removed when it settles (resolved or rejected).
 * Callers that arrive while a request is in flight receive the same Promise.
 * @type {Map<string, Promise<object>>}
 */
const inflight = new Map();

/**
 * Fetch /api/config, returning a cached response when one is still fresh.
 *
 * Concurrent calls with the same cache key that arrive while a request is already
 * in flight will share that request's Promise, so only one HTTP round-trip is made.
 *
 * @param {string|null} acpServer - Optional ACP server to pass as ?acp_server=…
 * @param {boolean} force - When true, bypass both caches and always fetch from network
 * @returns {Promise<object>} Parsed JSON config object
 */
export async function fetchConfig(acpServer = null, force = false) {
  const cacheKey = acpServer || "__default__";

  // 1. Completed-response cache hit
  if (!force) {
    const cached = configCache.get(cacheKey);
    if (cached && Date.now() - cached.timestamp < CONFIG_CACHE_TTL_MS) {
      return cached.data;
    }

    // 2. In-flight deduplication: join an existing request rather than firing another
    const existing = inflight.get(cacheKey);
    if (existing) {
      return existing;
    }
  }

  const url = acpServer
    ? apiUrl(`/api/config?acp_server=${encodeURIComponent(acpServer)}`)
    : apiUrl("/api/config");

  // Attach the stored ETag (if any) so the server can return 304 Not Modified
  // when the config has not changed since the last successful fetch.
  const cached = configCache.get(cacheKey);
  const fetchHeaders = {};
  if (!force && cached?.etag) {
    fetchHeaders["If-None-Match"] = cached.etag;
  }

  const promise = authFetch(url, { headers: fetchHeaders })
    .then((res) => {
      if (res.status === 304) {
        if (cached) {
          // Config unchanged — keep using the cached data without parsing the body.
          // Update the timestamp so the TTL window resets from this revalidation.
          configCache.set(cacheKey, {
            ...cached,
            timestamp: Date.now(),
          });
          inflight.delete(cacheKey);
          return cached.data;
        }
        // Cache was cleared between ETag send and 304 response — fall through to parse.
      }
      const etag = res.headers.get("ETag") || null;
      return res.json().then((data) => {
        configCache.set(cacheKey, { data, etag, timestamp: Date.now() });
        inflight.delete(cacheKey);
        return data;
      });
    })
    .catch((err) => {
      // Remove from inflight on error so the next caller retries
      inflight.delete(cacheKey);
      throw err;
    });

  // Register as in-flight before any await so synchronous callers also deduplicate
  if (!force) {
    inflight.set(cacheKey, promise);
  }

  return promise;
}

/**
 * Invalidate the entire config cache.
 * Call this after saving settings so the next fetch returns fresh data.
 * Both the completed-response cache and the in-flight map are cleared so that
 * concurrent fetches in progress do not repopulate the cache with stale data.
 */
export function invalidateConfigCache() {
  configCache.clear();
  inflight.clear();
}
