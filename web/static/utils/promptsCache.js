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

import { getSdkClient } from "./sdkClient.js";
import { createTtlCache, keyForParams } from "../sdk/index.js";

/** Cache TTL in milliseconds. Short enough that toggling a prompt in the
 * Workspaces dialog is visible on the next interaction, long enough to
 * collapse a menu-open burst or a prompts_changed event fan-out into one
 * network round-trip. */
const PROMPTS_CACHE_TTL_MS = 3_000;

/**
 * Side map tracking which working_dir each cache key belongs to, so
 * `invalidateWorkspacePromptsCache(workingDir)` can selectively clear entries
 * without parsing the opaque cache key string. Populated on every call
 * (cache hit or miss) since `working_dir` is always known synchronously from
 * `params`.
 * @type {Map<string, string>}
 */
const workingDirByKey = new Map();

/**
 * Three-level cache (TTL + in-flight dedup + If-Modified-Since revalidation)
 * wrapping `getSdkClient().prompts.list()`, built on the SDK's generic
 * decorator (mitto-7gta.17 plan, decision 5: caching lives at the seam, not
 * baked into the SDK resource itself — see sdk/cache/ttl-cache.js).
 */
const promptsTtlCache = createTtlCache({
  ttlMs: PROMPTS_CACHE_TTL_MS,
  keyFor: (params) => keyForParams(params),
  revalidate: {
    header: (record) =>
      record.lastModified
        ? { name: "If-Modified-Since", value: record.lastModified }
        : null,
    isUnchanged: (response) => response.status === 304,
    extract: (response, data) => ({
      data,
      lastModified: response.headers.get("Last-Modified") || null,
    }),
    value: (record) => record.data,
  },
});

/**
 * `allowStatus: [304]` + `raw: true` let this module observe a conditional
 * "Not Modified" response itself (see core/transport.js's `request()` doc)
 * instead of it always being thrown as a `MittoApiError`.
 */
const fetchWorkspacePromptsRaw = promptsTtlCache.wrap(
  async (params, revalidationHeader) => {
    const headers = {};
    if (revalidationHeader)
      headers[revalidationHeader.name] = revalidationHeader.value;
    const response = await getSdkClient().prompts.list(params, {
      headers,
      raw: true,
      allowStatus: [304],
    });
    if (response.status === 304) {
      return { response, data: undefined };
    }
    const lastModified = response.headers.get("Last-Modified") || null;
    const body = await response.json();
    const data = {
      prompts: body?.prompts || [],
      migrated: body?.migrated || [],
      lastModified,
    };
    return { response, data };
  },
);

/**
 * Fetch /api/workspace-prompts, deduplicating concurrent/bursty requests for
 * the same params via an in-flight Promise cache and a short TTL
 * completed-response cache, and revalidating with If-Modified-Since once the
 * TTL expires.
 *
 * @param {Object} params - Same shape as passed to client.prompts.list():
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

  workingDirByKey.set(keyForParams(params), workingDir);

  return fetchWorkspacePromptsRaw(params, { force });
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
    promptsTtlCache.invalidate();
    workingDirByKey.clear();
    return;
  }
  promptsTtlCache.invalidate((key) => workingDirByKey.get(key) === workingDir);
  for (const [key, wd] of workingDirByKey) {
    if (wd === workingDir) workingDirByKey.delete(key);
  }
}
