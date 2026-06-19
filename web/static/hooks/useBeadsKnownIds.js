// Mitto Web Interface - useBeadsKnownIds Hook
// Fetches known beads issue IDs on mount / workingDir change and refreshes
// every 60 seconds. Updates the module-level cache in beadsKnownIds.js and
// dispatches "beads-ids-updated" so already-rendered messages re-linkify.

const { useEffect } = window.preact;
import { fetchAndCacheBeadsIds } from "../utils/beadsKnownIds.js";

const REFRESH_INTERVAL_MS = 60_000;

/**
 * Call once from app.js with the active session's working directory.
 * @param {string} workingDir
 */
export function useBeadsKnownIds(workingDir) {
  useEffect(() => {
    if (!workingDir) return;
    fetchAndCacheBeadsIds(workingDir);
    const interval = setInterval(
      () => fetchAndCacheBeadsIds(workingDir),
      REFRESH_INTERVAL_MS,
    );
    return () => clearInterval(interval);
  }, [workingDir]);
}
