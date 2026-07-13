// Dependencies cluster sub-hook, extracted from useBeadsDetailPanel
// (mitto-90f.7 PR-17). Owns the view-mode dependency edge list + the
// add-dep draft (newDepType / newDepId) + depsLoading/depsBusy gates, and
// the four dep operations: fetchDeps (full-issue refresh), mutateDep
// (add/remove), handleAddDep (add-draft submit), and changeDepType (remove
// + re-add with new type).
//
// Boundary notes:
//   * fetchDeps fans out to labels/comments/viewEdit state on every issue
//     refresh (populate on success, clear on error/failure). The composer
//     owns those clusters (useIssueLabels / useIssueComments / useViewEdit),
//     so their setters (setLabels, setComments, setNotes, setViewDraft)
//     come in as props off the composer's already-materialised sub-hook bags.
//   * fetchDepsRef bridge: labels (PR-12) and comments (PR-13) call
//     fetchDepsRef.current(false) after mutations. The composer creates the
//     ref (useRef(null)) and hands the SAME ref instance to labels,
//     comments, AND this sub-hook. Here we populate
//     fetchDepsRef.current = fetchDeps synchronously during render so the
//     wiring is live from the first render of an open panel.
//   * The issue-switch reset effect that fires fetchDeps(true) stays in the
//     composer (it also resets labels/comments/notes state and is the one
//     cross-cluster effect the scoping pass flagged). The composer calls
//     deps.fetchDeps(true) from that effect.

const { useState, useCallback } = window.preact;

import { authFetch, secureFetch, endpoints } from "../../../utils/index.js";
import { readBeadsResponse } from "../../../utils/beads.js";

export function useIssueDependencies({
  data,
  workingDir,
  showToast,
  fetchDepsRef,
  onUpdated,
  setLabels,
  setComments,
  setNotes,
  setViewDraft,
}) {
  // View-mode dependencies. The list rows only carry a dependency_count, so the
  // full edges (id + title + status + dependency_type) are fetched from
  // /api/issues/{id} when an issue is opened. `depsBusy` gates add/remove
  // requests; `newDepType`/`newDepId` back the "add dependency" row.
  const [deps, setDeps] = useState([]);
  const [depsLoading, setDepsLoading] = useState(false);
  const [depsBusy, setDepsBusy] = useState(false);
  const [newDepType, setNewDepType] = useState("blocks");
  const [newDepId, setNewDepId] = useState("");

  // Load the issue's full dependency edges, notes, and comments. The list row
  // only carries counts, so the actual data comes from /api/issues/{id}.
  // seedDraftNotes: when true, also seeds viewDraft.notes from the response so
  // the initial open has a correct draft baseline. Callers that refresh deps
  // after a dep add/remove or comment post must pass false to avoid clobbering
  // an in-progress notes edit.
  const fetchDeps = useCallback(
    async (seedDraftNotes = false) => {
      if (!workingDir || !data || !data.id) return;
      setDepsLoading(true);
      try {
        const res = await authFetch(
          endpoints.issues.show(data.id, { working_dir: workingDir }),
        );
        const respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          setDeps([]);
          setLabels([]);
          setComments([]);
          setNotes("");
          if (seedDraftNotes)
            setViewDraft((prev) => ({ ...prev, notes: "" }));
        } else {
          const issueObj = Array.isArray(respData) ? respData[0] : respData;
          setDeps((issueObj && issueObj.dependencies) || []);
          setLabels((issueObj && issueObj.labels) || []);
          setComments((issueObj && issueObj.comments) || []);
          const fetchedNotes = (issueObj && issueObj.notes) || "";
          setNotes(fetchedNotes);
          if (seedDraftNotes)
            setViewDraft((prev) => ({
              ...prev,
              notes: fetchedNotes,
            }));
        }
      } catch (_err) {
        setDeps([]);
        setLabels([]);
        setComments([]);
        setNotes("");
        if (seedDraftNotes)
          setViewDraft((prev) => ({ ...prev, notes: "" }));
      } finally {
        setDepsLoading(false);
      }
    },
    [workingDir, data && data.id, setLabels, setComments, setNotes, setViewDraft],
  );

  // Wire the fetchDepsRef forward-reference bridge used by useIssueLabels and
  // useIssueComments so mutateLabel / handleCommentBlur can trigger a full
  // issue refresh after add/remove/post.
  fetchDepsRef.current = fetchDeps;

  // Add or remove a dependency edge via /api/issues/{id}/dependencies, then refresh both the
  // dependency list and the parent issue list (so counts stay current).
  const mutateDep = useCallback(
    async (action, dependsOn, depType) => {
      if (!data || !data.id || !dependsOn) return;
      setDepsBusy(true);
      try {
        const body = { depends_on: dependsOn, action };
        if (action === "add") body.type = depType || "blocks";
        const res = await secureFetch(
          endpoints.issues.dependencies(data.id, { working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          },
        );
        const respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          showToast &&
            showToast({
              style: "error",
              title: respData.error || `Failed to ${action} dependency`,
            });
          return false;
        }
        showToast &&
          showToast({
            style: "success",
            title:
              action === "add"
                ? `Added dependency on ${dependsOn}`
                : `Removed dependency on ${dependsOn}`,
          });
        await fetchDeps(false);
        onUpdated && onUpdated();
        return true;
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || `Failed to ${action} dependency`,
          });
        return false;
      } finally {
        setDepsBusy(false);
      }
    },
    [data && data.id, workingDir, showToast, fetchDeps, onUpdated],
  );

  const handleAddDep = useCallback(async () => {
    const target = newDepId.trim();
    if (!target || depsBusy) return;
    const ok = await mutateDep("add", target, newDepType);
    if (ok) setNewDepId("");
  }, [newDepId, newDepType, depsBusy, mutateDep]);

  // Change the kind of an existing edge. bd has no in-place type update, so this
  // removes the edge and re-adds it with the new type. A single combined toast
  // and refresh is issued at the end.
  const changeDepType = useCallback(
    async (dependsOn, nextType) => {
      if (!data || !data.id || !dependsOn || depsBusy) return;
      setDepsBusy(true);
      try {
        const post = (body) =>
          secureFetch(
            endpoints.issues.dependencies(data.id, { working_dir: workingDir }),
            {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(body),
            },
          );
        let res = await post({ depends_on: dependsOn, action: "remove" });
        let respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          showToast &&
            showToast({
              style: "error",
              title: respData.error || "Failed to change dependency type",
            });
          return;
        }
        res = await post({
          depends_on: dependsOn,
          type: nextType,
          action: "add",
        });
        respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          showToast &&
            showToast({
              style: "error",
              title: respData.error || "Failed to change dependency type",
            });
        } else {
          showToast &&
            showToast({
              style: "success",
              title: `Changed ${dependsOn} to ${nextType}`,
            });
        }
        await fetchDeps(false);
        onUpdated && onUpdated();
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || "Failed to change dependency type",
          });
      } finally {
        setDepsBusy(false);
      }
    },
    [data && data.id, workingDir, depsBusy, showToast, fetchDeps, onUpdated],
  );

  return {
    deps,
    setDeps,
    depsLoading,
    depsBusy,
    newDepType,
    setNewDepType,
    newDepId,
    setNewDepId,
    fetchDeps,
    mutateDep,
    handleAddDep,
    changeDepType,
  };
}
