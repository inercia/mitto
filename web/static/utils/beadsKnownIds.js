// Mitto Web Interface - Beads Known IDs Cache
// Module-level cache of known beads issue IDs keyed by working directory.

import { authFetch } from "./csrf.js";
import { endpoints } from "./endpoints.js";

// cache: workingDir -> { ids: Set<string>, meta: Map<string, {title, status}> }
const cache = new Map();

/**
 * Fetch /api/issues for the given working directory, update the module
 * cache, and dispatch a "beads-ids-updated" window event on success.
 * @param {string} workingDir
 */
export async function fetchAndCacheBeadsIds(workingDir) {
  if (!workingDir) return;
  try {
    const res = await authFetch(
      endpoints.issues.list({ working_dir: workingDir }),
    );
    if (!res.ok) return;
    const data = await res.json();
    if (!Array.isArray(data) || data.error) return;
    const ids = new Set();
    const meta = new Map();
    for (const issue of data) {
      if (!issue.id) continue;
      const lower = issue.id.toLowerCase();
      ids.add(lower);
      meta.set(lower, { title: issue.title || "", status: issue.status || "" });
    }
    cache.set(workingDir, { ids, meta });
    window.dispatchEvent(
      new CustomEvent("beads-ids-updated", { detail: { workingDir } }),
    );
  } catch (_err) {
    // ignore fetch errors
  }
}

/**
 * Return the cached IDs/meta for a working directory (sync).
 * Returns {ids: Set, meta: Map} or empty objects if not cached yet.
 * @param {string} workingDir
 * @returns {{ ids: Set<string>, meta: Map<string, {title: string, status: string}> }}
 */
export function getBeadsKnownIds(workingDir) {
  return cache.get(workingDir) || { ids: new Set(), meta: new Map() };
}
