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
const MAX_CONCURRENT_PRELOADS = 2;

// preloaded: workingDir -> Map<idLower, timestampMs>
const preloaded = new Map();
const pendingPreloads = [];
let activePreloads = 0;
let preloadGeneration = 0;

function drainPreloads() {
  while (
    activePreloads < MAX_CONCURRENT_PRELOADS &&
    pendingPreloads.length > 0
  ) {
    const job = pendingPreloads.shift();
    if (job.generation !== preloadGeneration) continue;
    activePreloads++;
    job.issues
      .show(job.id, { working_dir: job.workingDir })
      .catch(() => {})
      .finally(() => {
        if (job.generation !== preloadGeneration) return;
        activePreloads--;
        drainPreloads();
      });
  }
}

function shouldPreload(workingDir, id) {
  const now = Date.now();
  let bucket = preloaded.get(workingDir);
  if (!bucket) {
    bucket = new Map();
    preloaded.set(workingDir, bucket);
  }
  const last = bucket.get(id);
  if (last !== undefined && now - last < TTL_MS) return false;
  bucket.set(id, now);
  return true;
}

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

  const issues = withIssueCaches(getSdkClient().issues, { isGone });
  for (const id of ids) {
    if (!id || isGone(workingDir, id) || !shouldPreload(workingDir, id))
      continue;
    pendingPreloads.push({
      issues,
      id,
      workingDir,
      generation: preloadGeneration,
    });
  }
  drainPreloads();
}

/**
 * Reset internal dedup state. Test-only helper.
 */
export function _resetBeadsPreloadCache() {
  preloaded.clear();
  pendingPreloads.length = 0;
  activePreloads = 0;
  preloadGeneration++;
}
