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

import { getSdkClient } from "./sdkClient.js";
import { createTtlCache, keyForParams } from "../sdk/index.js";

/** Cache TTL in milliseconds (30 seconds). */
const CONFIG_CACHE_TTL_MS = 30_000;

/**
 * Three-level cache (TTL + in-flight dedup + ETag revalidation) wrapping
 * `getSdkClient().serverConfig.get()`, built on the SDK's generic decorator
 * (mitto-7gta.17 plan, decision 5: caching lives at the seam, not baked into
 * the SDK resource itself — see sdk/cache/ttl-cache.js).
 */
const configTtlCache = createTtlCache({
  ttlMs: CONFIG_CACHE_TTL_MS,
  keyFor: (acpServer, sessionId) =>
    keyForParams({ acp_server: acpServer, session_id: sessionId }),
  revalidate: {
    header: (record) =>
      record.etag ? { name: "If-None-Match", value: record.etag } : null,
    isUnchanged: (response) => response.status === 304,
    extract: (response, data) => ({
      data,
      etag: response.headers.get("ETag") || null,
    }),
    value: (record) => record.data,
  },
});

/**
 * `allowStatus: [304]` + `raw: true` let this module observe a conditional
 * "Not Modified" response itself (see core/transport.js's `request()` doc)
 * instead of it always being thrown as a `MittoApiError`.
 */
const fetchConfigCached = configTtlCache.wrap(
  async (acpServer, sessionId, revalidationHeader) => {
    const headers = {};
    if (revalidationHeader)
      headers[revalidationHeader.name] = revalidationHeader.value;
    const response = await getSdkClient().serverConfig.get(
      { acp_server: acpServer, session_id: sessionId },
      { headers, raw: true, allowStatus: [304] },
    );
    const data = response.status === 304 ? undefined : await response.json();
    return { response, data };
  },
);

/**
 * Fetch /api/config, returning a cached response when one is still fresh.
 *
 * Concurrent calls with the same cache key that arrive while a request is already
 * in flight will share that request's Promise, so only one HTTP round-trip is made.
 *
 * @param {string|null} acpServer - Optional ACP server to pass as ?acp_server=…
 * @param {boolean} force - When true, bypass both caches and always fetch from network
 * @param {string|null} sessionId - Optional session ID to pass as ?session_id=… for server-side filtering
 * @returns {Promise<object>} Parsed JSON config object
 */
export async function fetchConfig(
  acpServer = null,
  force = false,
  sessionId = null,
) {
  return fetchConfigCached(acpServer, sessionId, { force });
}

/**
 * Invalidate the entire config cache.
 * Call this after saving settings so the next fetch returns fresh data.
 * Both the completed-response cache and the in-flight map are cleared so that
 * concurrent fetches in progress do not repopulate the cache with stale data.
 */
export function invalidateConfigCache() {
  configTtlCache.invalidate();
}
