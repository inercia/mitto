// Dependencies cluster sub-hook, extracted from useBeadsDetailPanel
// (mitto-90f.7 PR-17). Owns the view-mode dependency edge list + the
// add-dep draft (newDepType / newDepId) + depsLoading/depsBusy gates.
//
// Dependency edits are staged in-memory (addDepLocal / removeDepLocal /
// changeDepTypeLocal) and only written to bd when the panel's Save button
// runs the composer's combined save, which calls persistDeps() to diff the
// working set against the baseline and issue the add/remove calls (a type
// change is a remove + re-add, since bd has no in-place edge update).
// fetchDeps still performs the full-issue refresh on open/switch.
//
// Boundary notes:
//   * fetchDeps fans out to labels/comments/viewEdit state on every issue
//     refresh (populate on success, clear on error/failure). The composer
//     owns those clusters (useIssueLabels / useIssueComments / useViewEdit),
//     so their setters (setLabels, setComments, setNotes, setViewDraft)
//     come in as props off the composer's already-materialised sub-hook bags.
//   * fetchDepsRef bridge: comments (PR-13) call fetchDepsRef.current(false)
//     after posting a comment. The composer creates the ref (useRef(null))
//     and hands the SAME ref instance to comments AND this sub-hook. Here we
//     populate fetchDepsRef.current = fetchDeps synchronously during render so
//     the wiring is live from the first render of an open panel.
//   * The issue-switch reset effect that fires fetchDeps(true) stays in the
//     composer (it also resets labels/comments/notes state and is the one
//     cross-cluster effect the scoping pass flagged). The composer calls
//     deps.fetchDeps(true) from that effect.

const { useState, useCallback } = window.preact;

import { getSdkClient } from "../../../utils/sdkClient.js";
import { errorMessage } from "../../../utils/sdkErrors.js";

// Order-insensitive equality for two dependency-edge arrays, comparing by edge
// id and dependency type. Used to derive depsDirty (working set vs the
// persisted baseline).
function depSetsEqual(a, b) {
  if (a.length !== b.length) return false;
  const mb = new Map(b.map((d) => [d.id, d.dependency_type || "blocks"]));
  for (const d of a) {
    if (mb.get(d.id) !== (d.dependency_type || "blocks")) return false;
  }
  return true;
}

export function useIssueDependencies({
  data,
  allIssues,
  creating,
  workingDir,
  showToast,
  fetchDepsRef,
  setLabels,
  setComments,
  setNotes,
  setViewDraft,
}) {
  // View-mode dependencies. The list rows only carry a dependency_count, so the
  // full edges (id + title + status + dependency_type) are fetched from
  // /api/issues/{id} when an issue is opened. `deps` holds the in-memory
  // working set the user edits; `depsBaseline` mirrors what is persisted in bd,
  // so the diff between the two drives `depsDirty` and the persistDeps
  // reconciler. `depsBusy` gates the Save-time persist; `newDepType`/`newDepId`
  // back the "add dependency" row.
  const [deps, setDepsRaw] = useState([]);
  const [depsBaseline, setDepsBaseline] = useState([]);
  const [depsLoading, setDepsLoading] = useState(false);
  const [depsBusy, setDepsBusy] = useState(false);
  const [newDepType, setNewDepType] = useState("blocks");
  const [newDepId, setNewDepId] = useState("");

  // Authoritative setter used by the composer's fetchDeps refresh and the
  // issue-switch reset effect: sets BOTH the working set and the baseline so a
  // server refresh (or a reset) starts clean (depsDirty === false).
  const setDeps = useCallback((next) => {
    setDepsRaw(next);
    setDepsBaseline(next);
  }, []);

  // Stage a dependency add/remove/type-change in memory only. Persisted later
  // by persistDeps when the panel's Save button runs. A newly-added edge
  // borrows its title/status from allIssues so the row renders immediately.
  const addDepLocal = useCallback(
    (id, type) => {
      const value = (id || "").trim();
      if (!value) return;
      setDepsRaw((prev) => {
        if (prev.some((d) => d.id === value)) return prev;
        const match = (allIssues || []).find((i) => i.id === value);
        return [
          ...prev,
          {
            id: value,
            title: (match && match.title) || "",
            status: match && match.status,
            dependency_type: type || "blocks",
          },
        ];
      });
    },
    [allIssues],
  );
  const removeDepLocal = useCallback((id) => {
    setDepsRaw((prev) => prev.filter((d) => d.id !== id));
  }, []);
  const changeDepTypeLocal = useCallback((id, nextType) => {
    setDepsRaw((prev) =>
      prev.map((d) => (d.id === id ? { ...d, dependency_type: nextType } : d)),
    );
  }, []);

  // Dirty when the working set differs from the persisted baseline by edge id
  // or dependency type (never in create mode, where deps are handled separately
  // by useCreateMode).
  const depsDirty = !creating && !depSetsEqual(deps, depsBaseline);

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
        const respData = await getSdkClient().issues.show(data.id, {
          working_dir: workingDir,
        });
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
      } catch (_err) {
        setDeps([]);
        setLabels([]);
        setComments([]);
        setNotes("");
        if (seedDraftNotes) setViewDraft((prev) => ({ ...prev, notes: "" }));
      } finally {
        setDepsLoading(false);
      }
    },
    [
      workingDir,
      data && data.id,
      setLabels,
      setComments,
      setNotes,
      setViewDraft,
    ],
  );

  // Wire the fetchDepsRef forward-reference bridge used by useIssueComments so
  // handleCommentBlur can trigger a full issue refresh after posting a comment.
  fetchDepsRef.current = fetchDeps;

  // Reconcile the in-memory working set against the persisted baseline by
  // issuing the necessary bd add/remove calls, then advance the baseline so the
  // panel is no longer dirty. Called by the composer's combined Save handler. A
  // type change is a remove + re-add (bd has no in-place edge update). Toasts on
  // failure and returns a boolean so the caller can bail before saving the
  // other view-mode fields. No-op (returns true) when nothing changed.
  const persistDeps = useCallback(async () => {
    if (!data || !data.id) return true;
    const baseById = new Map(depsBaseline.map((d) => [d.id, d]));
    const workIds = new Set(deps.map((d) => d.id));
    const toRemove = depsBaseline
      .filter((d) => !workIds.has(d.id))
      .map((d) => d.id);
    const toAdd = [];
    const toReAdd = [];
    for (const d of deps) {
      const type = d.dependency_type || "blocks";
      const base = baseById.get(d.id);
      if (!base) {
        toAdd.push({ id: d.id, type });
      } else if ((base.dependency_type || "blocks") !== type) {
        toReAdd.push({ id: d.id, type });
      }
    }
    if (toRemove.length === 0 && toAdd.length === 0 && toReAdd.length === 0)
      return true;
    setDepsBusy(true);
    try {
      const post = (body) =>
        getSdkClient().issues.dependencies(
          data.id,
          { working_dir: workingDir },
          body,
        );
      for (const id of toRemove) await post({ depends_on: id, action: "remove" });
      for (const { id, type } of toReAdd) {
        await post({ depends_on: id, action: "remove" });
        await post({ depends_on: id, type, action: "add" });
      }
      for (const { id, type } of toAdd)
        await post({ depends_on: id, type, action: "add" });
      setDepsBaseline(deps);
      return true;
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to save dependencies"),
        });
      return false;
    } finally {
      setDepsBusy(false);
    }
  }, [data && data.id, workingDir, deps, depsBaseline, showToast]);

  const handleAddDep = useCallback(() => {
    const target = newDepId.trim();
    if (!target || depsBusy) return;
    addDepLocal(target, newDepType);
    setNewDepId("");
  }, [newDepId, newDepType, depsBusy, addDepLocal]);

  return {
    deps,
    setDeps,
    depsLoading,
    depsBusy,
    depsDirty,
    newDepType,
    setNewDepType,
    newDepId,
    setNewDepId,
    fetchDeps,
    addDepLocal,
    removeDepLocal,
    changeDepTypeLocal,
    persistDeps,
    handleAddDep,
  };
}
