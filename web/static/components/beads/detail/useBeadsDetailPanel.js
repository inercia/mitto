// Custom hook housing all state / refs / memos / callbacks / effects of the
// former BeadsDetailPanel body (mitto-90f.7 PR-11). Extracted verbatim as a
// mechanical code motion from web/static/components/BeadsView.js — no renames,
// no restructuring, no dead-code removal. Every one of the original 107 hooks
// (44 useState + 15 useRef + 7 useMemo + 25 useCallback + 16 useEffect)
// remains in its original declaration order so React's hook-order invariant is
// preserved.
//
// The hook returns a single bag whose shape mirrors the 31 top-level props
// (7 bundles + 24 flat) BeadsDetailPanelBody consumes (see PR-10). The caller
// (BeadsDetailPanel in BeadsView.js) collapses to a ~15-LOC glue that unpacks
// this bag straight into <BeadsDetailPanelBody .../>.
//
// Effect 1124 (issue-switch reset touching deps/labels/comments/notes) stays
// inline here — it is the one cross-cluster effect the scoping pass flagged;
// splitting it belongs to a later sub-split PR (mitto-90f.7+).

const { useState, useEffect, useCallback, useMemo, useRef } = window.preact;

import { authFetch, secureFetch, endpoints } from "../../../utils/index.js";
import { readBeadsResponse } from "../../../utils/beads.js";
import { useIssueLabels } from "./useIssueLabels.js";
import { useIssueComments } from "./useIssueComments.js";
import { useCreateMode } from "./useCreateMode.js";
import { useViewEdit } from "./useViewEdit.js";
import { usePanelChrome } from "./usePanelChrome.js";

export function useBeadsDetailPanel({
  issue,
  allIssues,
  isCreating,
  workingDir,
  initialFullscreen,
  onClose,
  onCreated,
  onUpdated,
  showToast,
  onFetchPrompts,
  onRunPrompt,
  onDelete,
  onToggleStatus,
  onToggleDefer,
  statusBusy,
  onSelectIssue,
  createParentId,
}) {
  const isOpen = isCreating || !!issue;
  const lastIssueRef = useRef(issue);
  const lastCreatingRef = useRef(isCreating);
  if (issue) lastIssueRef.current = issue;
  if (isOpen) lastCreatingRef.current = isCreating;

  // While closing, keep rendering whichever mode was last open.
  const creating = isOpen ? isCreating : lastCreatingRef.current;
  const data = issue || lastIssueRef.current;

  // Create-mode form state + submit handler now live in useCreateMode
  // (mitto-90f.7 PR-14). Sub-hook call placed at the original position of
  // the create-state block to preserve hook-order. The composer re-exposes
  // the sub-hook return in the `create` bundle and flattens description /
  // submitting / createEditorApiRef / handleSave to satisfy PanelBody's
  // existing top-level prop consumption.
  const create = useCreateMode({
    isCreating,
    createParentId,
    workingDir,
    showToast,
    onCreated,
    onClose,
  });

  // Magic-wand "Improve description" state. Mirrors ChatInput's improve-prompt
  // flow but targets the create-form description. `improvingDesc` gates the
  // in-flight request and drives the spinner. Kept in the composer because
  // improveDescriptionText is shared by view-mode inline edits and create-mode
  // (mitto-90f.7 PR-14 view/create-shared caveat, retained through PR-16).
  const [improvingDesc, setImprovingDesc] = useState(false);

  // View-mode edit cluster (mitto-90f.7 PR-16): per-field edit state + refs
  // (title/type/assignee/description/notes), viewDraft/savingView/savedBaseline,
  // notes, the derived md/viewOriginal/viewDirty memos, the save/discard
  // handlers, and the associated seed/focus/edit-mode-reset effects. Called
  // BEFORE usePanelChrome so viewDirty/savingView are available for the close
  // gate. The composer re-exposes viewEdit fields in the `view` bundle and
  // as flat props (md, descMinHeight, descViewRef, detailEditorApiRef,
  // viewDirty, savingView) so PanelBody's prop shape is preserved.
  const viewEdit = useViewEdit({
    data,
    creating,
    workingDir,
    showToast,
    onUpdated,
  });

  // View-mode dependencies. The list rows only carry a dependency_count, so the
  // full edges (id + title + status + dependency_type) are fetched from
  // /api/issues/{id} when an issue is opened. `depsBusy` gates add/remove
  // requests; `newDepType`/`newDepId` back the "add dependency" row.
  const [deps, setDeps] = useState([]);
  const [depsLoading, setDepsLoading] = useState(false);
  const [depsBusy, setDepsBusy] = useState(false);
  const [newDepType, setNewDepType] = useState("blocks");
  const [newDepId, setNewDepId] = useState("");
  // Labels editing state + handlers now live in useIssueLabels (mitto-90f.7
  // PR-12). fetchDepsRef bridges the labels->deps callback tangle: fetchDeps
  // is defined later in this hook, so we hand the sub-hook a ref that we
  // populate below (see `fetchDepsRef.current = fetchDeps`). The composer
  // continues to read labels state via `labels.*` (see fetchDeps refresh and
  // the issue-switch reset effect below).
  const fetchDepsRef = useRef(null);
  const labels = useIssueLabels({
    data,
    workingDir,
    showToast,
    fetchDepsRef,
    onUpdated,
    isOpen,
    creating,
  });
  // Comments cluster (list state + add-comment editor + handlers) now lives
  // in useIssueComments (mitto-90f.7 PR-13). Reuses the fetchDepsRef bridge
  // from PR-12: handleCommentBlur calls fetchDepsRef.current(false) after a
  // successful POST. The composer continues to write comments.setComments in
  // fetchDeps and the issue-switch reset effects below.
  const comments = useIssueComments({
    data,
    workingDir,
    showToast,
    fetchDepsRef,
    onUpdated,
  });

  // handleSave (create submit), addCreateDep, and removeCreateDep now live in
  // useCreateMode (mitto-90f.7 PR-14). Access via `create.handleSave` /
  // `create.addCreateDep` / `create.removeCreateDep`.

  // AI-enhance a description text field via the same auxiliary endpoint the chat
  // input's magic wand uses (/api/aux/improve-prompt). Works on any
  // text/setText pair so it serves both the create-form description and the
  // view-mode inline edit draft. Replaces the text with the improved version on
  // success; surfaces errors as a toast. No-op when empty or already running.
  const improveDescriptionText = useCallback(
    async (text, setText) => {
      if (improvingDesc || !text || !text.trim()) return;
      setImprovingDesc(true);
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 65000); // 65s timeout
      try {
        const response = await secureFetch(endpoints.aux.improvePrompt(), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            prompt: text,
            workspace_uuid:
              (typeof window !== "undefined" &&
                window.mittoCurrentWorkspaceUUID) ||
              (typeof sessionStorage !== "undefined" &&
                sessionStorage.getItem("mittoCurrentWorkspaceUUID")) ||
              "",
          }),
          signal: controller.signal,
        });
        clearTimeout(timeoutId);
        if (!response.ok) {
          const errData = await response.json().catch(() => ({}));
          throw new Error(
            errData?.error?.message ||
              errData?.message ||
              "Failed to improve description",
          );
        }
        const respData = await response.json();
        if (respData.improved_prompt) {
          setText(respData.improved_prompt);
        }
      } catch (err) {
        clearTimeout(timeoutId);
        const msg =
          err.name === "AbortError"
            ? "Request timed out. Please try again."
            : err.message || "Failed to improve description";
        showToast && showToast({ style: "error", title: msg });
      } finally {
        setImprovingDesc(false);
      }
    },
    [improvingDesc, showToast],
  );

  const subtasks = useMemo(
    () =>
      !creating && data ? allIssues.filter((i) => i.parent === data.id) : [],
    [creating, allIssues, data && data.id],
  );

  // Panel chrome/shell cluster (mitto-90f.7 PR-15): open/close fade, outside-
  // click detection, confirm-close dialog, kebab context menu + panelMenuItems,
  // headerToolbarItems, per-folder shortcut buttons, isMobile / fullscreen /
  // shouldRender / isClosing. Called here (after viewEdit.viewDirty is
  // computed) so handleClose and the deferred-close effect can gate the dirty
  // check and the save-in-flight defer. lastIssueRef / lastCreatingRef stay
  // in the composer so `data` and `creating` remain sticky during the fade-out.
  const {
    shouldRender,
    isClosing,
    fullscreen,
    setFullscreen,
    isMobile,
    panelMenu,
    setPanelMenu,
    panelMenuItems,
    headerToolbarItems,
    confirmDiscard,
    setConfirmDiscard,
    handleClose,
    handleDiscardAndClose,
  } = usePanelChrome({
    isOpen,
    data,
    creating,
    viewDirty: viewEdit.viewDirty,
    savingView: viewEdit.savingView,
    initialFullscreen,
    workingDir,
    statusBusy,
    onClose,
    onDelete,
    onToggleStatus,
    onToggleDefer,
    onRunPrompt,
    onFetchPrompts,
  });

  // Sibling comment-edit cleanup on issue switch. The view-edit reset that
  // used to share this effect body now lives inside useViewEdit; this small
  // effect handles the three comment-editor setters on the same data.id
  // dependency so both fire on the same transition.
  useEffect(() => {
    comments.setAddingComment(false);
    comments.setSavingComment(false);
    comments.setCommentDraft("");
  }, [data && data.id]);

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
          labels.setLabels([]);
          comments.setComments([]);
          viewEdit.setNotes("");
          if (seedDraftNotes)
            viewEdit.setViewDraft((prev) => ({ ...prev, notes: "" }));
        } else {
          const issueObj = Array.isArray(respData) ? respData[0] : respData;
          setDeps((issueObj && issueObj.dependencies) || []);
          labels.setLabels((issueObj && issueObj.labels) || []);
          comments.setComments((issueObj && issueObj.comments) || []);
          const fetchedNotes = (issueObj && issueObj.notes) || "";
          viewEdit.setNotes(fetchedNotes);
          if (seedDraftNotes)
            viewEdit.setViewDraft((prev) => ({
              ...prev,
              notes: fetchedNotes,
            }));
        }
      } catch (_err) {
        setDeps([]);
        labels.setLabels([]);
        comments.setComments([]);
        viewEdit.setNotes("");
        if (seedDraftNotes)
          viewEdit.setViewDraft((prev) => ({ ...prev, notes: "" }));
      } finally {
        setDepsLoading(false);
      }
    },
    [
      workingDir,
      data && data.id,
      labels.setLabels,
      comments.setComments,
      viewEdit.setNotes,
      viewEdit.setViewDraft,
    ],
  );
  // Wire the fetchDepsRef forward-reference bridge used by useIssueLabels so
  // mutateLabel can trigger a full issue refresh after add/remove.
  fetchDepsRef.current = fetchDeps;

  // startAddComment + handleCommentBlur now live in useIssueComments
  // (mitto-90f.7 PR-13); handleCommentBlur uses fetchDepsRef.current to reach
  // fetchDeps defined above.

  // Fetch dependencies, notes, and comments whenever a (non-create) issue is opened or switched.
  // seedDraftNotes=true so the initial open seeds viewDraft.notes from the response.
  useEffect(() => {
    setDeps([]);
    labels.setLabels([]);
    comments.setComments([]);
    viewEdit.setNotes("");
    setNewDepId("");
    setNewDepType("blocks");
    labels.setNewLabel("");
    labels.setAddingLabel(false);
    if (isOpen && !creating && data && data.id) {
      fetchDeps(true);
    }
  }, [isOpen, creating, data && data.id]);

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

  // NOTE: the two early-return-null gates that historically lived here
  // (`if (!shouldRender) return null;` and `if (!creating && !data) return null;`)
  // were rendering guards for the JSX return that used to follow. Now that
  // the hook returns a data bag instead of JSX, the guards move to the caller
  // (BeadsDetailPanel in BeadsView.js), which checks h.shouldRender /
  // h.creating / h.data on the returned bag before rendering.

  return {
    // Early-return gates (caller uses these before rendering)
    shouldRender,
    creating,
    data,
    // Flat props (24)
    isClosing,
    isMobile,
    fullscreen,
    setFullscreen,
    createParentId,
    submitting: create.submitting,
    viewDirty: viewEdit.viewDirty,
    savingView: viewEdit.savingView,
    description: create.description,
    setDescription: create.setDescription,
    createEditorApiRef: create.createEditorApiRef,
    detailEditorApiRef: viewEdit.detailEditorApiRef,
    descMinHeight: viewEdit.descMinHeight,
    descViewRef: viewEdit.descViewRef,
    md: viewEdit.md,
    workingDir,
    improvingDesc,
    improveDescriptionText,
    allIssues,
    subtasks,
    onSelectIssue,
    showToast,
    // Bundles (7)
    create,
    view: {
      viewDraft: viewEdit.viewDraft,
      setViewDraft: viewEdit.setViewDraft,
      editingType: viewEdit.editingType,
      setEditingType: viewEdit.setEditingType,
      typeRef: viewEdit.typeRef,
      editingAssignee: viewEdit.editingAssignee,
      setEditingAssignee: viewEdit.setEditingAssignee,
      assigneeRef: viewEdit.assigneeRef,
      editingTitle: viewEdit.editingTitle,
      setEditingTitle: viewEdit.setEditingTitle,
      titleRef: viewEdit.titleRef,
      editingDesc: viewEdit.editingDesc,
      setEditingDesc: viewEdit.setEditingDesc,
      editingNotes: viewEdit.editingNotes,
      setEditingNotes: viewEdit.setEditingNotes,
      notesRef: viewEdit.notesRef,
      notesViewRef: viewEdit.notesViewRef,
      notesMinHeight: viewEdit.notesMinHeight,
    },
    deps: {
      deps,
      depsLoading,
      depsBusy,
      changeDepType,
      mutateDep,
      newDepType,
      setNewDepType,
      newDepId,
      setNewDepId,
      handleAddDep,
    },
    labels,
    comments,
    handlers: {
      handleClose,
      handleSave: create.handleSave,
      handleViewSave: viewEdit.handleViewSave,
      handleDiscardAndClose,
      handleTitleKeyDown: viewEdit.handleTitleKeyDown,
      handleAssigneeKeyDown: viewEdit.handleAssigneeKeyDown,
      startEditTitle: viewEdit.startEditTitle,
      startEditAssignee: viewEdit.startEditAssignee,
      startEditDesc: viewEdit.startEditDesc,
      startEditNotes: viewEdit.startEditNotes,
    },
    chrome: {
      headerToolbarItems,
      panelMenu,
      setPanelMenu,
      panelMenuItems,
      confirmDiscard,
      setConfirmDiscard,
    },
  };
}
