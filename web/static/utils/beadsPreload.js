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

import { getSdkClient } from "./sdkClient.js";
import { withIssueCaches } from "../sdk/index.js";
import { isGone } from "./beadsGoneCache.js";

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

  // withIssueCaches' preload() already fires-and-forgets each show() call and
  // swallows errors (best-effort cache warmer); isGone/shouldPreload here
  // reproduce this module's original TTL-dedup + negative-cache skip exactly.
  const issues = withIssueCaches(getSdkClient().issues, {
    isGone,
    shouldPreload: (wd, id) => {
      const now = Date.now();
      let bucket = preloaded.get(wd);
      if (!bucket) {
        bucket = new Map();
        preloaded.set(wd, bucket);
      }
      const last = bucket.get(id);
      if (last !== undefined && now - last < TTL_MS) return false;
      bucket.set(id, now);
      return true;
    },
  });
  issues.preload(ids, { working_dir: workingDir });
}

/**
 * Reset internal dedup state. Test-only helper.
 */
export function _resetBeadsPreloadCache() {
  preloaded.clear();
}
