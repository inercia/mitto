// mitto-66r — useLinkedBeadPhase
//
// Given a session's beads_issue id + working_dir, resolve the linked bead's
// (issue_type, labels) and return the derived phase state via
// utils/phaseState.derivePhaseState.
//
// Rationale for a shared hook (Option 2 in the bead brief): SessionItem is
// rendered per visible row, so a naive per-instance fetch would fire N
// concurrent requests each time the sidebar renders. A module-level cache keyed
// by `${workingDir}|${issueId}` lets every SessionItem hit the same in-flight
// promise / cached result and dedupe automatically. We refresh opportunistically
// on the existing `mitto:beads_changed` broadcast (no new backend event).

const { useEffect, useState } = window.preact;
import { authFetch } from "../utils/csrf.js";
import { endpoints } from "../utils/endpoints.js";
import { derivePhaseState } from "../utils/phaseState.js";

// Module-level cache: key -> { promise, value: {issue_type, labels}|null }
// Only ONE entry per (workingDir,issueId); refresh replaces it in place.
const cache = new Map();

function cacheKey(workingDir, issueId) {
  return `${workingDir || ""}|${issueId || ""}`;
}

async function fetchIssue(workingDir, issueId) {
  const res = await authFetch(
    endpoints.issues.show(issueId, { working_dir: workingDir }),
  );
  if (!res.ok) return null;
  const data = await res.json();
  const issueObj = Array.isArray(data) ? data[0] : data;
  if (!issueObj || issueObj.error) return null;
  return {
    issue_type: issueObj.issue_type,
    labels: Array.isArray(issueObj.labels) ? issueObj.labels : [],
  };
}

// Return a promise for the (issue_type, labels) tuple. Multiple callers with
// the same key share the same in-flight promise. Once resolved the result is
// cached; use invalidate() (or a `mitto:beads_changed` broadcast) to refresh.
function getOrFetch(workingDir, issueId) {
  const key = cacheKey(workingDir, issueId);
  const existing = cache.get(key);
  if (existing) return existing.promise;
  const promise = fetchIssue(workingDir, issueId)
    .then((value) => {
      const cur = cache.get(key);
      if (cur && cur.promise === promise) cur.value = value;
      return value;
    })
    .catch(() => {
      // On network / parse failure surface null so callers can gracefully
      // hide the pill. Do NOT keep a failed promise in the cache so a later
      // render / beads_changed refresh can retry.
      cache.delete(key);
      return null;
    });
  cache.set(key, { promise, value: undefined });
  return promise;
}

function invalidateAll() {
  cache.clear();
}

/**
 * Resolve the phase state (see utils/phaseState.derivePhaseState) for a
 * session's linked bead, or null when the bead is not a feature/bug (or no
 * bead is linked, or the fetch failed).
 *
 * @param {string|undefined} issueId
 * @param {string|undefined} workingDir
 * @returns {object|null} phase state or null
 */
export function useLinkedBeadPhase(issueId, workingDir) {
  const [state, setState] = useState(null);

  useEffect(() => {
    if (!issueId || !workingDir) {
      setState(null);
      return;
    }
    let cancelled = false;
    // Kick off fetch (dedup via getOrFetch); apply the derived phase state
    // when it resolves. If the row is unmounted before it resolves we drop
    // the update.
    getOrFetch(workingDir, issueId).then((val) => {
      if (cancelled) return;
      if (!val) {
        setState(null);
        return;
      }
      setState(derivePhaseState(val.issue_type, val.labels));
    });

    // Refresh on the workspace-wide beads_changed broadcast. The event fires
    // for any beads mutation in the workspace so we invalidate the whole
    // cache and re-fetch for the currently mounted issueId; the cost is
    // bounded by the number of visible session rows.
    function onBeadsChanged() {
      invalidateAll();
      getOrFetch(workingDir, issueId).then((val) => {
        if (cancelled) return;
        if (!val) {
          setState(null);
          return;
        }
        setState(derivePhaseState(val.issue_type, val.labels));
      });
    }
    window.addEventListener("mitto:beads_changed", onBeadsChanged);

    return () => {
      cancelled = true;
      window.removeEventListener("mitto:beads_changed", onBeadsChanged);
    };
  }, [issueId, workingDir]);

  return state;
}
