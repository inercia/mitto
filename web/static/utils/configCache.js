// Mitto Web Interface - Config Cache
// Caches GET /api/config responses to avoid redundant network fetches during startup.

import { apiUrl } from "./api.js";
import { authFetch } from "./csrf.js";

/** Cache TTL in milliseconds (30 seconds). */
const CONFIG_CACHE_TTL_MS = 30_000;

/**
 * Cache storage: cacheKey → { data, timestamp }
 * The cache key is the acpServer string, or "__default__" when none is supplied.
 * @type {Map<string, { data: object, timestamp: number }>}
 */
const configCache = new Map();

/**
 * Fetch /api/config, returning a cached response when one is still fresh.
 *
 * @param {string|null} acpServer - Optional ACP server to pass as ?acp_server=…
 * @param {boolean} force - When true, bypass the cache and always fetch from network
 * @returns {Promise<object>} Parsed JSON config object
 */
export async function fetchConfig(acpServer = null, force = false) {
  const cacheKey = acpServer || "__default__";
  const cached = configCache.get(cacheKey);

  if (
    !force &&
    cached &&
    Date.now() - cached.timestamp < CONFIG_CACHE_TTL_MS
  ) {
    return cached.data;
  }

  const url = acpServer
    ? apiUrl(`/api/config?acp_server=${encodeURIComponent(acpServer)}`)
    : apiUrl("/api/config");

  const res = await authFetch(url);
  const data = await res.json();
  configCache.set(cacheKey, { data, timestamp: Date.now() });
  return data;
}

/**
 * Invalidate the entire config cache.
 * Call this after saving settings so the next fetch returns fresh data.
 */
export function invalidateConfigCache() {
  configCache.clear();
}

