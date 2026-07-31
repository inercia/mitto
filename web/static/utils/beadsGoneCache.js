// mitto-msv — shared negative cache for bead IDs known to return 404.
//
// A stale `beads_issue` metadata field on an (often archived) session drives an
// unbounded polling loop against `GET /api/issues/{id}` — the endpoint 404s for
// every request but the pollers (useLinkedBeadPhase for each session row, the
// header status effect in app.js, and the side-panel effect in SessionPanel.js)
// never give up, costing 500ms-2s of `bd show` subprocess work per hit. Observed:
// ~463 requests over 3 days for a single stale id on one archived session.
//
// This module is the single-source-of-truth negative cache shared by all three
// callers. Once an id 404s in any surface, `markGone(workingDir, id)` records
// the verdict and every subsequent poll (from any caller) short-circuits without
// hitting the network. The verdict deliberately outlives `mitto:beads_changed`
// broadcasts — that event carries only `working_dirs`, not per-id detail
// (see internal/web/server.go OnBeadsChanged), so we cannot safely infer
// resurrection from it. Fallbacks that DO bust the verdict: a full page reload,
// or navigating into the bead in the Beads viewer (which uses BeadsView.js's
// per-instance `goneIdsRef` established for mitto-9vh — unaffected by this
// module).
//
// Keyed per workspace so a bead deleted in workspace A does not shadow a bead
// with the same id that exists in workspace B (bd IDs are workspace-scoped).

// gone: workingDir -> Set<idLower>
const gone = new Map();

function normKey(id) {
  return typeof id === "string" ? id.toLowerCase() : "";
}

/**
 * Report whether `id` in `workingDir` has previously returned 404.
 * @param {string} workingDir
 * @param {string} id
 * @returns {boolean}
 */
export function isGone(workingDir, id) {
  if (!workingDir || !id) return false;
  const bucket = gone.get(workingDir);
  if (!bucket) return false;
  return bucket.has(normKey(id));
}

/**
 * Mark `id` in `workingDir` as gone (404). Subsequent `isGone` calls return
 * true until `clearGone` (or `_reset`) is invoked.
 * @param {string} workingDir
 * @param {string} id
 */
export function markGone(workingDir, id) {
  if (!workingDir || !id) return;
  let bucket = gone.get(workingDir);
  if (!bucket) {
    bucket = new Set();
    gone.set(workingDir, bucket);
  }
  bucket.add(normKey(id));
}

/**
 * Remove the gone verdict for `id` in `workingDir`. Called only when there is
 * positive evidence the bead now exists (today: not wired — the
 * `mitto:beads_changed` broadcast lacks per-id detail; kept as the escape hatch
 * for future per-id broadcast payloads).
 * @param {string} workingDir
 * @param {string} id
 */
export function clearGone(workingDir, id) {
  if (!workingDir || !id) return;
  const bucket = gone.get(workingDir);
  if (!bucket) return;
  bucket.delete(normKey(id));
  if (bucket.size === 0) gone.delete(workingDir);
}

/**
 * Reset all negative-cache state. Test-only helper.
 */
export function _resetBeadsGoneCache() {
  gone.clear();
}
