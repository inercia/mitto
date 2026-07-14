// Create-mode cluster sub-hook, extracted from useBeadsDetailPanel
// (mitto-90f.7 PR-14). Owns the new-issue creation form state (title, type,
// priority, description, assignee, notes, deps draft + submitting flag) and
// the three create-mode handlers (handleSave submit, addCreateDep,
// removeCreateDep). Also owns createEditorApiRef (the imperative handle for
// the create-form description CodeMirror editor) and the reset-on-enter
// effect gated by isCreating.
//
// Shared/left-in-composer (view+create):
//   improvingDesc / setImprovingDesc / improveDescriptionText — the magic-wand
//   endpoint is reused for the view-mode description edit draft as well
//   (`improveDescriptionText(text, setText)` accepts any text/setText pair),
//   so these stay in the composer per the PR-14 view/create-shared caveat.
//
// PanelBody consumption is preserved by:
//   1. Returning the exact 18 fields that used to live in the `create` slot
//      of the return bag so the composer can re-expose them as
//      `create: { ...create-fields }` (composer collapses to bare `create,`).
//   2. Additionally returning `description`, `setDescription`, `submitting`,
//      `createEditorApiRef`, and `handleSave` so the composer can re-expose
//      them as flat props / into the `handlers` bundle (PanelBody reads them
//      as top-level props today).

const { useState, useEffect, useCallback, useRef } = window.preact;

import { secureFetch } from "../../../utils/csrf.js";
import { endpoints } from "../../../utils/endpoints.js";
import { readBeadsResponse } from "../../../utils/beads.js";

export function useCreateMode({
  isCreating,
  createParentId,
  workingDir,
  showToast,
  onCreated,
  onClose,
}) {
  // Create-mode form state.
  const [title, setTitle] = useState("");
  const [type, setType] = useState("task");
  const [priority, setPriority] = useState(2); // 2 = Medium
  const [description, setDescription] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [createDeps, setCreateDeps] = useState([]);
  const [createNewDepType, setCreateNewDepType] = useState("blocks");
  const [createNewDepId, setCreateNewDepId] = useState("");
  const [createAssignee, setCreateAssignee] = useState("");
  const [createNotes, setCreateNotes] = useState("");

  // Imperative handle for the create-form's description CodeMirror editor.
  const createEditorApiRef = useRef(null);

  // Reset the form whenever create mode is (re)entered.
  useEffect(() => {
    if (isCreating) {
      setTitle("");
      setType("task");
      setPriority(2);
      setDescription("");
      setSubmitting(false);
      setCreateDeps([]);
      setCreateNewDepType("blocks");
      setCreateNewDepId("");
      setCreateAssignee("");
      setCreateNotes("");
    }
  }, [isCreating]);

  const handleSave = useCallback(async () => {
    if (!description.trim()) return;
    setSubmitting(true);
    try {
      const body = { type, priority, description: description.trim() };
      if (title.trim()) body.title = title.trim();
      if (createParentId) body.parent = createParentId;
      if (createAssignee.trim()) body.assignee = createAssignee.trim();
      if (createNotes.trim()) body.notes = createNotes.trim();
      if (createDeps.length)
        body.dependencies = createDeps.map((d) => ({
          id: d.id,
          type: d.type || "blocks",
        }));
      const res = await secureFetch(
        endpoints.issues.create({ working_dir: workingDir }),
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
            title: respData.error || "Failed to create issue",
          });
      } else {
        showToast && showToast({ style: "success", title: "Issue created" });
        onCreated && onCreated();
        onClose && onClose();
      }
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: err.message || "Failed to create issue",
        });
    } finally {
      setSubmitting(false);
    }
  }, [
    workingDir,
    title,
    type,
    priority,
    description,
    createParentId,
    createAssignee,
    createNotes,
    createDeps,
    showToast,
    onCreated,
    onClose,
  ]);

  const addCreateDep = useCallback(() => {
    const id = createNewDepId.trim();
    if (!id) return;
    if (createDeps.some((d) => d.id === id)) return;
    setCreateDeps((prev) => [...prev, { id, type: createNewDepType }]);
    setCreateNewDepId("");
  }, [createNewDepId, createNewDepType, createDeps]);

  const removeCreateDep = useCallback((id) => {
    setCreateDeps((prev) => prev.filter((d) => d.id !== id));
  }, []);

  return {
    title,
    setTitle,
    type,
    setType,
    priority,
    setPriority,
    description,
    setDescription,
    submitting,
    createDeps,
    setCreateDeps,
    createNewDepType,
    setCreateNewDepType,
    createNewDepId,
    setCreateNewDepId,
    createAssignee,
    setCreateAssignee,
    createNotes,
    setCreateNotes,
    createEditorApiRef,
    addCreateDep,
    removeCreateDep,
    handleSave,
  };
}
