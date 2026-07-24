// Mitto Web Interface - Beads Preload Utility
// Best-effort background preloading of `bd show <id>` cache slots for beads
// IDs newly rendered as linkified references in a conversation message. The
// sole purpose is to populate `beads.CachingClient`'s `show:<id>` slot via
// the existing `HandleBeadsShow` handler so the first user click resolves
// against a warm cache instead of a cold `bd show`.
//
// Silent on failure — swallows non-2xx and network errors. Per-workspace
// dedup with a short TTL so a bead mentioned repeatedly triggers one preload
// per TTL window, not one per mention.

import { authFetch } from "./csrf.js";
import { endpoints } from "./endpoints.js";

const TTL_MS = 30_000;

// preloaded: workingDir -> Map<idLower, timestampMs>
const preloaded = new Map();

/**
 * Fire-and-forget preload of `GET /api/issues/{id}?working_dir=…` for each
 * non-deduped id in `ids`. Response is discarded. No-op when `ids` is empty
 * or `workingDir` is falsy.
 * @param {string[]} ids - Lowercased beads IDs to warm.
 * @param {string} workingDir - Workspace working directory (dedup key).
 */
export function preloadBeadsIssues(ids, workingDir) {
  if (!workingDir) return;
  if (!Array.isArray(ids) || ids.length === 0) return;

  const now = Date.now();
  let bucket = preloaded.get(workingDir);
  if (!bucket) {
    bucket = new Map();
    preloaded.set(workingDir, bucket);
  }

  for (const id of ids) {
    if (!id) continue;
    const last = bucket.get(id);
    if (last !== undefined && now - last < TTL_MS) continue;
    bucket.set(id, now);
    // Fire and forget; response body is discarded. authFetch handles auth
    // and unified 401 handling. Failures are swallowed silently — this is a
    // best-effort cache warmer.
    authFetch(endpoints.issues.show(id, { working_dir: workingDir })).catch(
      () => {},
    );
  }
}

/**
 * Reset internal dedup state. Test-only helper.
 */
export function _resetBeadsPreloadCache() {
  preloaded.clear();
}
