// Sub-hook housing all state / refs / callbacks / effects related to editing
// the current issue's label set (view-mode add/remove/list + workspace-wide
// suggestions datalist). Extracted verbatim from useBeadsDetailPanel as
// mitto-90f.7 PR-12 (mechanical LabelsCluster sub-split).
//
// Tangles handled at the boundary:
//   * fetchDeps (deps cluster in the composer) writes to labels state on
//     issue refresh — the composer reads labels.setLabels from the returned
//     bag to keep that call site working.
//   * Effect 1124 (issue-switch reset) resets labels state — same setter
//     access pattern; the effect stays inline in the composer.
//   * mutateLabel needs to invoke fetchDeps (composer) after add/remove — it
//     is passed via `fetchDepsRef` (a ref the composer sets after fetchDeps
//     is defined). This preserves hook-order in the composer without a
//     forward-reference problem.

const { useState, useEffect, useCallback, useRef } = window.preact;

import { authFetch, secureFetch, endpoints } from "../../../utils/index.js";
import { readBeadsResponse } from "../../../utils/beads.js";

export function useIssueLabels({
  data,
  workingDir,
  showToast,
  fetchDepsRef,
  onUpdated,
  isOpen,
  creating,
}) {
  // Labels shown in view mode. `labels` mirrors the issue's current labels
  // (refreshed via fetchDeps); `labelsBusy` gates add/remove requests;
  // `newLabel` backs the add-label input; `allLabels` holds the workspace-wide
  // label suggestions rendered in the add-label datalist.
  const [labels, setLabels] = useState([]);
  const [labelsBusy, setLabelsBusy] = useState(false);
  const [newLabel, setNewLabel] = useState("");
  const [allLabels, setAllLabels] = useState([]);
  // `addingLabel` toggles the inline add-label input (revealed by the "+"
  // button); `labelInputRef` lets us focus it as soon as it opens.
  const [addingLabel, setAddingLabel] = useState(false);
  const labelInputRef = useRef(null);

  // Fetch the workspace's unique labels to suggest when adding a label. bd
  // returns [{label,count}, ...]; we keep only the names. Refreshed when the
  // panel opens and after a label is added. Non-fatal on failure.
  const fetchAllLabels = useCallback(async () => {
    if (!workingDir) return;
    try {
      const res = await authFetch(
        endpoints.issues.labelsAll({ working_dir: workingDir }),
      );
      const respData = await readBeadsResponse(res);
      if (res.ok && Array.isArray(respData)) {
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

  // Add or remove a label on the current issue, then refresh the issue (so the
  // labels list stays current) and notify the parent list. Mirrors mutateDep.
  const mutateLabel = useCallback(
    async (action, label) => {
      const value = (label || "").trim();
      if (!data || !data.id || !value) return false;
      setLabelsBusy(true);
      try {
        const res = await secureFetch(
          endpoints.issues.labels(data.id, { working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ label: value, action }),
          },
        );
        const respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          showToast &&
            showToast({
              style: "error",
              title: respData.error || `Failed to ${action} label`,
            });
          return false;
        }
        showToast &&
          showToast({
            style: "success",
            title:
              action === "add"
                ? `Added label "${value}"`
                : `Removed label "${value}"`,
          });
        if (fetchDepsRef && fetchDepsRef.current) {
          await fetchDepsRef.current(false);
        }
        if (action === "add") fetchAllLabels();
        onUpdated && onUpdated();
        return true;
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || `Failed to ${action} label`,
          });
        return false;
      } finally {
        setLabelsBusy(false);
      }
    },
    [
      data && data.id,
      workingDir,
      showToast,
      fetchDepsRef,
      fetchAllLabels,
      onUpdated,
    ],
  );

  const handleAddLabel = useCallback(async () => {
    const value = newLabel.trim();
    if (!value || labelsBusy) return;
    const ok = await mutateLabel("add", value);
    if (ok) setNewLabel("");
  }, [newLabel, labelsBusy, mutateLabel]);

  // Focus the add-label input as soon as the "+" reveals it.
  useEffect(() => {
    if (addingLabel && labelInputRef.current) labelInputRef.current.focus();
  }, [addingLabel]);

  return {
    labels,
    setLabels,
    labelsBusy,
    newLabel,
    setNewLabel,
    allLabels,
    addingLabel,
    setAddingLabel,
    labelInputRef,
    mutateLabel,
    handleAddLabel,
  };
}
