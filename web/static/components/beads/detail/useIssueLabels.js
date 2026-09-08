// Sub-hook housing all state / refs / callbacks / effects related to editing
// the current issue's label set (view-mode add/remove/list + workspace-wide
// suggestions datalist). Extracted verbatim from useBeadsDetailPanel as
// mitto-90f.7 PR-12 (mechanical LabelsCluster sub-split).
//
// Tangles handled at the boundary:
//   * fetchDeps (deps cluster in the composer) writes to labels state on
//     issue refresh — the composer reads labels.setLabels from the returned
//     bag to keep that call site working. setLabels resets BOTH the working
//     set and the persisted baseline so a refresh is never seen as dirty.
//   * Effect 1124 (issue-switch reset) resets labels state — same setter
//     access pattern; the effect stays inline in the composer.
//   * Label edits are staged in-memory (addLabelLocal / removeLabelLocal) and
//     only written to bd when the panel's Save button runs the composer's
//     combined save, which calls persistLabels() to diff the working set
//     against the baseline and issue the necessary add/remove calls.

const { useState, useEffect, useCallback, useRef } = window.preact;

import { getSdkClient } from "../../../utils/sdkClient.js";
import { errorMessage } from "../../../utils/sdkErrors.js";

// Order-insensitive equality for two label arrays. Used to derive labelsDirty
// (working set vs the persisted baseline).
function labelSetsEqual(a, b) {
  if (a.length !== b.length) return false;
  const setB = new Set(b);
  for (const x of a) if (!setB.has(x)) return false;
  return true;
}

export function useIssueLabels({
  data,
  workingDir,
  showToast,
  isOpen,
  creating,
}) {
  // Labels shown in view mode. `labels` holds the in-memory working set the
  // user edits; `labelsBaseline` mirrors what is persisted in bd, so the diff
  // between the two drives `labelsDirty` and the persistLabels reconciler.
  // `labelsBusy` gates the Save-time persist; `newLabel` backs the add-label
  // input; `allLabels` holds the workspace-wide label suggestions rendered in
  // the add-label datalist.
  const [labels, setLabelsRaw] = useState([]);
  const [labelsBaseline, setLabelsBaseline] = useState([]);
  const [labelsBusy, setLabelsBusy] = useState(false);
  const [newLabel, setNewLabel] = useState("");
  const [allLabels, setAllLabels] = useState([]);
  // `addingLabel` toggles the inline add-label input (revealed by the "+"
  // button); `labelInputRef` lets us focus it as soon as it opens.
  const [addingLabel, setAddingLabel] = useState(false);
  const labelInputRef = useRef(null);

  // Authoritative setter used by the composer's fetchDeps refresh and the
  // issue-switch reset effect: sets BOTH the working set and the baseline so a
  // server refresh (or a reset) starts clean (labelsDirty === false).
  const setLabels = useCallback((next) => {
    setLabelsRaw(next);
    setLabelsBaseline(next);
  }, []);

  // Stage a label add/remove in memory only. Persisted later by persistLabels
  // when the panel's Save button runs.
  const addLabelLocal = useCallback((label) => {
    const value = (label || "").trim();
    if (!value) return;
    setLabelsRaw((prev) => (prev.includes(value) ? prev : [...prev, value]));
  }, []);
  const removeLabelLocal = useCallback((label) => {
    setLabelsRaw((prev) => prev.filter((l) => l !== label));
  }, []);

  // Dirty when the working set differs from the persisted baseline (never in
  // create mode, where labels are handled separately).
  const labelsDirty = !creating && !labelSetsEqual(labels, labelsBaseline);

  // Fetch the workspace's unique labels to suggest when adding a label. bd
  // returns [{label,count}, ...]; we keep only the names. Refreshed when the
  // panel opens and after a label is added. Non-fatal on failure.
  const fetchAllLabels = useCallback(async () => {
    if (!workingDir) return;
    try {
      const respData = await getSdkClient().issues.labelsAll({
        working_dir: workingDir,
      });
      if (Array.isArray(respData)) {
        setAllLabels(
          respData
            .map((l) => (typeof l === "string" ? l : l && l.label))
            .filter(Boolean),
        );
      }
    } catch (_err) {
      // Non-fatal: label suggestions just won't populate.
    }
  }, [workingDir]);

  useEffect(() => {
    if (isOpen && !creating) fetchAllLabels();
  }, [isOpen, creating, fetchAllLabels]);

  // Reconcile the in-memory working set against the persisted baseline by
  // issuing the necessary bd add/remove calls, then advance the baseline so the
  // panel is no longer dirty. Called by the composer's combined Save handler.
  // Toasts on failure and returns a boolean so the caller can bail before
  // saving the other (view-mode) fields. No-op (returns true) when nothing
  // changed.
  const persistLabels = useCallback(async () => {
    if (!data || !data.id) return true;
    const baseSet = new Set(labelsBaseline);
    const workSet = new Set(labels);
    const toAdd = labels.filter((l) => !baseSet.has(l));
    const toRemove = labelsBaseline.filter((l) => !workSet.has(l));
    if (toAdd.length === 0 && toRemove.length === 0) return true;
    setLabelsBusy(true);
    try {
      for (const label of toRemove) {
        await getSdkClient().issues.labels(
          data.id,
          { working_dir: workingDir },
          { label, action: "remove" },
        );
      }
      for (const label of toAdd) {
        await getSdkClient().issues.labels(
          data.id,
          { working_dir: workingDir },
          { label, action: "add" },
        );
      }
      setLabelsBaseline(labels);
      if (toAdd.length > 0) fetchAllLabels();
      return true;
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to save labels"),
        });
      return false;
    } finally {
      setLabelsBusy(false);
    }
  }, [
    data && data.id,
    workingDir,
    labels,
    labelsBaseline,
    showToast,
    fetchAllLabels,
  ]);

  const handleAddLabel = useCallback(() => {
    const value = newLabel.trim();
    if (!value || labelsBusy) return;
    addLabelLocal(value);
    setNewLabel("");
  }, [newLabel, labelsBusy, addLabelLocal]);

  // Focus the add-label input as soon as the "+" reveals it.
  useEffect(() => {
    if (addingLabel && labelInputRef.current) labelInputRef.current.focus();
  }, [addingLabel]);

  return {
    labels,
    setLabels,
    labelsBusy,
    labelsDirty,
    newLabel,
    setNewLabel,
    allLabels,
    addingLabel,
    setAddingLabel,
    labelInputRef,
    addLabelLocal,
    removeLabelLocal,
    persistLabels,
    handleAddLabel,
  };
}
