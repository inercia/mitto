// Mitto Web Interface - useBeadsKnownIds Hook
// Fetches known beads issue IDs on mount / workingDir change, refreshes
// every 60 seconds as a safety net, and re-fetches immediately when the
// backend broadcasts "mitto:beads_changed" for the current workingDir.
// Updates the module-level cache in beadsKnownIds.js and dispatches
// "beads-ids-updated" so already-rendered messages re-linkify.

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
    const onChanged = (e) => {
      const dirs = e?.detail?.working_dirs;
      if (!Array.isArray(dirs) || dirs.includes(workingDir)) {
        fetchAndCacheBeadsIds(workingDir);
      }
    };
    window.addEventListener("mitto:beads_changed", onChanged);
    return () => {
      clearInterval(interval);
      window.removeEventListener("mitto:beads_changed", onChanged);
    };
  }, [workingDir]);
}
