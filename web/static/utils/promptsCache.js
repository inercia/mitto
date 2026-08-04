// Mitto Web Interface - Workspace Prompts Cache
// Caches GET /api/workspace-prompts responses to collapse the request storm
// that comes from several independent call sites (workspace-prompts dropup,
// per-conversation context menu, per-beads-row context menu, beads list
// prompts button) all hitting the same endpoint in bursts (menu opens, event
// fan-out). Mirrors the three-level pattern already used by configCache.js:
//   1. Completed-response cache (short TTL): avoids refetching within the
//      TTL window when several callers ask for the same params in a burst.
//   2. In-flight Promise cache: concurrent callers for the same params share
//      one HTTP request instead of each firing their own (thundering herd).
//   3. HTTP Last-Modified / If-Modified-Since: after the TTL expires the
//      client revalidates with the server's own conditional-request support
//      (internal/web/handlers/workspace_prompts.go), which is otherwise only
//      used by the workspace-prompts dropup fetcher and unused by the beads
//      and conversation-menu call sites.
//
// Correctness note: the cache key is the FULL param set (working_dir,
// session_id, enabled_context, item_*, include_global, ...), not just
// working_dir. The item_* params make the server evaluate `item.*`-gated
// enabledWhen expressions per beads row (mitto-o0u.1), so two calls that
// differ only in item_id must never share a cache entry.

import { authFetch } from "./csrf.js";
import { endpoints } from "./endpoints.js";

/** Cache TTL in milliseconds. Short enough that toggling a prompt in the
 * Workspaces dialog is visible on the next interaction, long enough to
 * collapse a menu-open burst or a prompts_changed event fan-out into one
 * network round-trip. */
const PROMPTS_CACHE_TTL_MS = 3_000;

/**
 * Completed-response cache: cacheKey → { data, workingDir, lastModified, timestamp }
 * `data` is { prompts, migrated }. `lastModified` is the raw Last-Modified
 * header string (if any), reused as If-Modified-Since on the next call.
 * @type {Map<string, { data: object, workingDir: string, lastModified: string|null, timestamp: number }>}
 */
const promptsCache = new Map();

/**
 * In-flight request deduplication: cacheKey → Promise<object>
 * @type {Map<string, Promise<object>>}
 */
const inflight = new Map();

/**
 * Build a stable cache key from the request params: sorted keys, empty /
 * undefined / null values omitted (so callers that pass `undefined` for an
 * absent item_* field key identically to callers that omit it entirely).
 *
 * @param {Object} params
 * @returns {string}
 */
function cacheKeyFor(params) {
  const entries = Object.entries(params || {})
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  return entries.map(([k, v]) => `${k}=${v}`).join("&");
}

/**
 * Fetch /api/workspace-prompts, deduplicating concurrent/bursty requests for
 * the same params via an in-flight Promise cache and a short TTL
 * completed-response cache, and revalidating with If-Modified-Since once the
 * TTL expires.
 *
 * @param {Object} params - Same shape as passed to endpoints.workspacePrompts.list():
 *   working_dir, session_id, enabled_context, item_kind, item_id,
 *   item_status, item_type, item_priority, item_labels, include_global.
 * @param {Object} [opts]
 * @param {boolean} [opts.force=false] - Bypass both caches and always hit the
 *   network (still populates the caches with the fresh response, so a burst
 *   of forced calls collapses on subsequent non-force callers).
 * @returns {Promise<{ prompts: Array, migrated: Array, lastModified: string|null }>}
 */
export async function fetchWorkspacePromptsCached(params, opts = {}) {
  const { force = false } = opts;
  const workingDir = params?.working_dir;
  if (!workingDir) return { prompts: [], migrated: [], lastModified: null };

  const cacheKey = cacheKeyFor(params);

  if (!force) {
    // 1. Completed-response cache hit
    const cached = promptsCache.get(cacheKey);
    if (cached && Date.now() - cached.timestamp < PROMPTS_CACHE_TTL_MS) {
      return cached.data;
    }

    // 2. In-flight deduplication: join an existing request rather than
    // firing another one for the same params.
    const existing = inflight.get(cacheKey);
    if (existing) {
      return existing;
    }
  }

  const url = endpoints.workspacePrompts.list(params);

  // Attach the stored Last-Modified (if any) as If-Modified-Since so the
  // server can answer 304 when nothing changed (mitto-8x9).
  const cached = promptsCache.get(cacheKey);
  const headers = {};
  if (!force && cached?.lastModified) {
    headers["If-Modified-Since"] = cached.lastModified;
  }

  const promise = authFetch(url, { headers })
    .then(async (res) => {
      if (res.status === 304) {
        if (cached) {
          // Prompts unchanged — keep using the cached data, resetting the
          // TTL window from this revalidation.
          promptsCache.set(cacheKey, { ...cached, timestamp: Date.now() });
          inflight.delete(cacheKey);
          return cached.data;
        }
        // Cache was cleared between the If-Modified-Since send and the 304
        // response — fall through by returning an empty result rather than
        // parsing a body that doesn't exist on a 304.
        inflight.delete(cacheKey);
        return { prompts: [], migrated: [], lastModified: null };
      }
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      const lastModified = res.headers.get("Last-Modified") || null;
      const body = await res.json();
      const data = {
        prompts: body?.prompts || [],
        migrated: body?.migrated || [],
        lastModified,
      };
      promptsCache.set(cacheKey, {
        data,
        workingDir,
        lastModified,
        timestamp: Date.now(),
      });
      inflight.delete(cacheKey);
      return data;
    })
    .catch((err) => {
      // Remove from inflight on error so the next caller retries.
      inflight.delete(cacheKey);
      throw err;
    });

  // Register as in-flight before any await so synchronous callers in the
  // same burst also deduplicate.
  if (!force) {
    inflight.set(cacheKey, promise);
  }

  return promise;
}

/**
 * Invalidate cached workspace-prompts responses.
 *
 * @param {string|null} [workingDir] - When provided, only clears entries for
 *   that working directory (and their matching in-flight entries). When
 *   omitted, clears everything.
 */
export function invalidateWorkspacePromptsCache(workingDir = null) {
  if (!workingDir) {
    promptsCache.clear();
    inflight.clear();
    return;
  }
  for (const [key, entry] of promptsCache) {
    if (entry.workingDir === workingDir) {
      promptsCache.delete(key);
      inflight.delete(key);
    }
  }
}
