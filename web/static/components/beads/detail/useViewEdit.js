// View-mode edit cluster sub-hook, extracted from useBeadsDetailPanel
// (mitto-90f.7 PR-16). Owns the panel's view-mode inline editing state and
// flow: per-field edit toggles + refs (title/type/assignee/description/notes),
// the accumulated `viewDraft`, dirty tracking (`viewDirty`), the unified Save
// handler (`handleViewSave`), and the focus/outside-click/seed effects that
// go with those edit modes.
//
// Shared/left-in-composer (view+create):
//   improvingDesc / setImprovingDesc / improveDescriptionText — the magic-wand
//   endpoint is reused for BOTH the create-form description and the view-mode
//   inline edit draft (`improveDescriptionText(text, setText)` accepts any
//   text/setText pair), so they stay in the composer per the PR-14 caveat.
//
// Boundary notes:
//   * `viewDirty` and `savingView` are consumed by usePanelChrome — the
//     composer must call useViewEdit BEFORE usePanelChrome and thread these
//     two values in as props. Hook-order in the composer is preserved by
//     placing the useViewEdit call at the position of the moved state.
//   * `notes` / `setNotes` and `setViewDraft` are used by fetchDeps and the
//     issue-switch reset effect (both stay in the composer), so they are
//     re-exposed on the return bag for the composer to close over.
//   * The former single "leave all edit modes on issue switch" effect that
//     also cleared three comment-edit setters is split: the view-edit part
//     lives here (own effect on data.id); the comment cleanup remains in the
//     composer alongside the deps/labels/notes reset (effect 1124).

const { useState, useEffect, useCallback, useMemo, useRef } = window.preact;

import { secureFetch, endpoints } from "../../../utils/index.js";
import { readBeadsResponse } from "../../../utils/beads.js";
import { renderMarkdown } from "../CommentBody.js";

export function useViewEdit({
  data,
  creating,
  workingDir,
  showToast,
  onUpdated,
}) {
  // View-mode inline description editing. editingDesc switches the rendered
  // description to a CodeMirror editor. Edits accumulate in viewDraft and are
  // persisted by the unified Save button. descMinHeight keeps the editor at
  // least as tall as the content it replaces.
  const [editingDesc, setEditingDesc] = useState(false);
  const [descMinHeight, setDescMinHeight] = useState(0);
  const detailEditorApiRef = useRef(null);
  const descViewRef = useRef(null);

  // View-mode inline title editing.
  const [editingTitle, setEditingTitle] = useState(false);
  const titleRef = useRef(null);
  // Snapshot of viewDraft.title captured on startEditTitle so Escape can revert.
  const titleEditStartRef = useRef("");

  // View-mode inline type editing.
  const [editingType, setEditingType] = useState(false);
  const typeRef = useRef(null);

  // View-mode inline assignee editing.
  const [editingAssignee, setEditingAssignee] = useState(false);
  const assigneeRef = useRef(null);
  // Snapshot of viewDraft.assignee captured on startEditAssignee so Escape can revert.
  const assigneeEditStartRef = useRef("");

  // Draft / dirty / save state for view mode. All six editable fields
  // accumulate into viewDraft; a single Save posts them together.
  const [viewDraft, setViewDraft] = useState({
    title: "",
    type: "task",
    priority: 2,
    description: "",
    assignee: "",
    notes: "",
  });
  const [savingView, setSavingView] = useState(false);
  // After a successful Save, holds the just-persisted field values so the dirty
  // check clears immediately — without waiting for the async onUpdated() refresh
  // to flow updated `data` back down. Reset to null when a different issue opens
  // (the seed effect below). When set, it takes precedence over viewOriginal.
  const [savedBaseline, setSavedBaseline] = useState(null);

  const [notes, setNotes] = useState("");

  // View-mode inline notes editing.
  const [editingNotes, setEditingNotes] = useState(false);
  const [notesMinHeight, setNotesMinHeight] = useState(0);
  const notesRef = useRef(null);
  const notesViewRef = useRef(null);

  // Close the type dropdown on outside click while it is open.
  useEffect(() => {
    if (!editingType) return undefined;
    const onDocClick = (e) => {
      if (typeRef.current && !typeRef.current.contains(e.target)) {
        setEditingType(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [editingType]);

  // md renders the draft description so the read-only view reflects in-progress edits.
  const md = useMemo(
    () => renderMarkdown(!creating && viewDraft && viewDraft.description),
    [creating, viewDraft && viewDraft.description],
  );

  // The "original" values used to compute dirtiness. Notes come from async
  // fetchDeps, so they are sourced from the `notes` state rather than data.
  const viewOriginal = useMemo(
    () => ({
      title: (data && data.title) || "",
      type: (data && data.issue_type) || "task",
      priority: data && typeof data.priority === "number" ? data.priority : 2,
      description: (data && data.description) || "",
      assignee: (data && data.assignee) || "",
      notes: notes || "",
    }),
    [
      data && data.id,
      data && data.title,
      data && data.issue_type,
      data && data.priority,
      data && data.description,
      data && data.assignee,
      notes,
    ],
  );

  const viewDirty = useMemo(() => {
    if (creating) return false;
    // A successful save records its persisted values in savedBaseline; compare
    // against those so the panel is no longer "dirty" the instant Save resolves.
    const base = savedBaseline || viewOriginal;
    const t = viewDraft.title.trim();
    return (
      (t !== "" && t !== base.title) ||
      viewDraft.type !== base.type ||
      viewDraft.priority !== base.priority ||
      viewDraft.description !== base.description ||
      viewDraft.assignee.trim() !== base.assignee ||
      viewDraft.notes !== base.notes
    );
  }, [creating, viewDraft, viewOriginal, savedBaseline]);

  // Seed non-notes fields whenever a different issue opens (notes come from
  // fetchDeps in the composer, which calls setViewDraft when seedDraftNotes is true).
  useEffect(() => {
    if (creating || !data || !data.id) return;
    setSavedBaseline(null);
    setViewDraft({
      title: data.title || "",
      type: data.issue_type || "task",
      priority: typeof data.priority === "number" ? data.priority : 2,
      description: data.description || "",
      assignee: data.assignee || "",
      notes: "",
    });
  }, [creating, data && data.id]);

  // Leave all view edit modes whenever the viewed issue changes. The composer
  // handles the sibling comment-edit cleanup in its own effect on the same
  // dep so behavior matches the previous single-effect version.
  useEffect(() => {
    setEditingDesc(false);
    setEditingTitle(false);
    setEditingType(false);
    setEditingAssignee(false);
    setEditingNotes(false);
  }, [data && data.id]);

  // The description CodeMirror editor auto-focuses on mount (autoFocus prop)
  // so no separate useEffect is needed here.

  // Focus the notes textarea (cursor at end) when entering notes-edit mode.
  useEffect(() => {
    if (editingNotes && notesRef.current) {
      const el = notesRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingNotes]);

  // Focus the title input (cursor at end) when entering edit mode.
  useEffect(() => {
    if (editingTitle && titleRef.current) {
      const el = titleRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingTitle]);

  // Focus the assignee input (cursor at end) when entering edit mode.
  useEffect(() => {
    if (editingAssignee && assigneeRef.current) {
      const el = assigneeRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingAssignee]);

  const startEditDesc = useCallback(() => {
    if (descViewRef.current) setDescMinHeight(descViewRef.current.offsetHeight);
    setEditingDesc(true);
  }, []);

  const startEditNotes = useCallback(() => {
    if (notesViewRef.current)
      setNotesMinHeight(notesViewRef.current.offsetHeight);
    setEditingNotes(true);
  }, []);

  const startEditTitle = useCallback(() => {
    titleEditStartRef.current = viewDraft.title;
    setEditingTitle(true);
  }, [viewDraft.title]);

  const startEditAssignee = useCallback(() => {
    assigneeEditStartRef.current = viewDraft.assignee;
    setEditingAssignee(true);
  }, [viewDraft.assignee]);

  // Enter saves (via blur); Escape reverts to snapshot and blurs.
  const handleTitleKeyDown = useCallback((e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      e.target.blur();
    } else if (e.key === "Escape") {
      e.preventDefault();
      setViewDraft((p) => ({ ...p, title: titleEditStartRef.current }));
      e.target.blur();
    }
  }, []);

  const handleAssigneeKeyDown = useCallback((e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      e.target.blur();
    } else if (e.key === "Escape") {
      e.preventDefault();
      setViewDraft((p) => ({ ...p, assignee: assigneeEditStartRef.current }));
      e.target.blur();
    }
  }, []);

  // Unified Save: patches all dirty fields in one PATCH /api/issues/{id} call.
  const handleViewSave = useCallback(async () => {
    if (!data || !data.id || savingView) return;
    const body = {};
    const t = viewDraft.title.trim();
    if (t !== "" && t !== viewOriginal.title) body.title = t;
    if (viewDraft.type !== viewOriginal.type) body.type = viewDraft.type;
    if (viewDraft.priority !== viewOriginal.priority)
      body.priority = viewDraft.priority;
    if (viewDraft.description !== viewOriginal.description)
      body.description = viewDraft.description;
    if (viewDraft.assignee.trim() !== viewOriginal.assignee)
      body.assignee = viewDraft.assignee.trim();
    if (viewDraft.notes !== viewOriginal.notes) body.notes = viewDraft.notes;
    if (Object.keys(body).length === 0) return;
    setSavingView(true);
    try {
      const res = await secureFetch(
        endpoints.issues.update(data.id, { working_dir: workingDir }),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast &&
          showToast({
            style: "error",
            title: respData.error || "Failed to save changes",
          });
      } else {
        if ("notes" in body) setNotes(viewDraft.notes);
        // Record what we just persisted so viewDirty clears immediately (the
        // normalized values mirror how the dirty check reads the draft), instead
        // of staying dirty until the async onUpdated() refresh re-seeds `data`.
        setSavedBaseline({
          title: viewDraft.title.trim(),
          type: viewDraft.type,
          priority: viewDraft.priority,
          description: viewDraft.description,
          assignee: viewDraft.assignee.trim(),
          notes: viewDraft.notes,
        });
        setEditingTitle(false);
        setEditingType(false);
        setEditingDesc(false);
        setEditingNotes(false);
        setEditingAssignee(false);
        showToast && showToast({ style: "success", title: "Changes saved" });
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: err.message || "Failed to save changes",
        });
    } finally {
      setSavingView(false);
    }
  }, [
    viewDraft,
    viewOriginal,
    data && data.id,
    workingDir,
    savingView,
    showToast,
    onUpdated,
  ]);

  return {
    // Draft + save state
    viewDraft,
    setViewDraft,
    savingView,
    // notes (composer's fetchDeps + reset effect need setNotes)
    notes,
    setNotes,
    // Edit-mode flags + refs
    editingDesc,
    setEditingDesc,
    descMinHeight,
    descViewRef,
    detailEditorApiRef,
    editingTitle,
    setEditingTitle,
    titleRef,
    editingType,
    setEditingType,
    typeRef,
    editingAssignee,
    setEditingAssignee,
    assigneeRef,
    editingNotes,
    setEditingNotes,
    notesMinHeight,
    notesRef,
    notesViewRef,
    // Memos
    md,
    viewDirty,
    // Handlers
    startEditDesc,
    startEditNotes,
    startEditTitle,
    startEditAssignee,
    handleTitleKeyDown,
    handleAssigneeKeyDown,
    handleViewSave,
  };
}
