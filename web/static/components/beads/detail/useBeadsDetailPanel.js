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

import { secureFetch, endpoints } from "../../../utils/index.js";
import { useIssueLabels } from "./useIssueLabels.js";
import { useIssueComments } from "./useIssueComments.js";
import { useCreateMode } from "./useCreateMode.js";
import { useViewEdit } from "./useViewEdit.js";
import { usePanelChrome } from "./usePanelChrome.js";
import { useIssueDependencies } from "./useIssueDependencies.js";

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
  // Loading-mode props (mitto-zbfq): let a caller keep a single stable
  // BeadsDetailPanel mount across the null→loaded transition instead of
  // swapping in a separate placeholder Drawer. When isLoading is true and
  // issue/isCreating are absent, isOpen still resolves true so the panel
  // shell renders; BeadsDetailPanelBody renders a small loading/error
  // skeleton inside its Drawer while `data` is null.
  isLoading,
  loadingIssueId,
  loadError,
  onRetry,
  // In-viewer navigation history (mitto-qluh.2). Threaded through from
  // BeadsIssueView so PanelBody can render Back/Forward buttons wired to
  // the viewer's history stack. Undefined when the panel is used outside
  // the single-issue viewer.
  canGoBack,
  canGoForward,
  onGoBack,
  onGoForward,
}) {
  const isOpen = isCreating || !!issue || !!isLoading;
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

  // fetchDepsRef bridges the labels/comments -> deps callback tangle:
  // useIssueLabels.mutateLabel and useIssueComments.handleCommentBlur both
  // call fetchDepsRef.current(false) after a mutation to trigger a full
  // issue refresh. The composer creates the ref once and hands the same
  // instance to labels, comments, AND useIssueDependencies (which populates
  // fetchDepsRef.current = fetchDeps internally). Kept in the composer so
  // one ref is shared across the three sub-hooks.
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
  // successful POST.
  const comments = useIssueComments({
    data,
    workingDir,
    showToast,
    fetchDepsRef,
    onUpdated,
  });
  // Dependencies cluster (deps list + add-dep draft + depsBusy + fetchDeps /
  // mutateDep / handleAddDep / changeDepType) now lives in
  // useIssueDependencies (mitto-90f.7 PR-17). Sub-hook is called AFTER
  // labels/comments/viewEdit so their setters (which fetchDeps fans out to
  // on every issue refresh) are available. It internally populates
  // fetchDepsRef.current = fetchDeps so the labels/comments bridge is live.
  const deps = useIssueDependencies({
    data,
    workingDir,
    showToast,
    fetchDepsRef,
    onUpdated,
    setLabels: labels.setLabels,
    setComments: comments.setComments,
    setNotes: viewEdit.setNotes,
    setViewDraft: viewEdit.setViewDraft,
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

  // fetchDeps, mutateDep, handleAddDep, changeDepType, and the
  // fetchDepsRef.current = fetchDeps bridge assignment now live inside
  // useIssueDependencies (mitto-90f.7 PR-17). The trigger effect below is
  // the ONE cross-cluster site that still touches deps + labels + comments +
  // viewEdit setters together; it stays in the composer and calls
  // deps.fetchDeps(true) for the async refresh.

  // Fetch dependencies, notes, and comments whenever a (non-create) issue is opened or switched.
  // seedDraftNotes=true so the initial open seeds viewDraft.notes from the response.
  useEffect(() => {
    deps.setDeps([]);
    labels.setLabels([]);
    comments.setComments([]);
    viewEdit.setNotes("");
    deps.setNewDepId("");
    deps.setNewDepType("blocks");
    labels.setNewLabel("");
    labels.setAddingLabel(false);
    if (isOpen && !creating && data && data.id) {
      deps.fetchDeps(true);
    }
  }, [isOpen, creating, data && data.id]);

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
    // Loading-mode surface (mitto-zbfq): forwarded to PanelBody so the
    // shared Drawer can show a spinner / error alert while `data` is null.
    isLoading: !!isLoading && !creating && !data,
    loadingIssueId,
    loadError,
    onRetry,
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
    // In-viewer navigation history (mitto-qluh.2) — passed through verbatim
    // to PanelBody so the bottom bar can wire Back/Forward buttons.
    canGoBack,
    canGoForward,
    onGoBack,
    onGoForward,
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
      deps: deps.deps,
      depsLoading: deps.depsLoading,
      depsBusy: deps.depsBusy,
      changeDepType: deps.changeDepType,
      mutateDep: deps.mutateDep,
      newDepType: deps.newDepType,
      setNewDepType: deps.setNewDepType,
      newDepId: deps.newDepId,
      setNewDepId: deps.setNewDepId,
      handleAddDep: deps.handleAddDep,
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
