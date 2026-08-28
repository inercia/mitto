// Mitto Web Interface — Folder Task Label Colors Config Hook
//
// Owns all folder-scoped task-label-color state for the folder editor's Tasks
// tab (mitto-m5f.3): the tab-open load effect, the folder-switch reset
// effect, the four row-mutation handlers (add/update/remove/move — reused
// from utils/taskLabelColors.js, the same transforms backing the global
// Settings editor), and the persist-on-save function. Mirrors
// useFolderShortcutsConfig.js's shape/lifecycle exactly.
//
// The shell needs taskLabelColorsLoaded (to know whether to call
// persistTaskLabelColors from handleSave) and persistTaskLabelColors itself;
// both are returned as top-level fields alongside the grouped
// {taskLabelColors, taskLabelColorsHandlers} objects.

const { useState, useEffect } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";
import {
  addTaskLabelColor,
  moveTaskLabelColor,
  removeTaskLabelColor,
  updateTaskLabelColor,
} from "../utils/taskLabelColors.js";

/**
 * useFolderTaskLabelColorsConfig — cohesive state/handler bundle for the
 * folder Tasks tab's task-label-color editor.
 *
 * @param {Object} params
 * @param {string|null} params.selectedFolder — the currently selected folder display name
 * @param {string} params.activeTab — the currently active folder tab id
 * @param {() => string|null} params.getSelectedFolderDir — resolver for the folder's working_dir
 * @returns {{
 *   taskLabelColors: Object,
 *   taskLabelColorsHandlers: Object,
 *   taskLabelColorsLoaded: boolean,
 *   persistTaskLabelColors: () => Promise<void>,
 * }}
 */
export function useFolderTaskLabelColorsConfig({
  selectedFolder,
  activeTab,
  getSelectedFolderDir,
}) {
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");

  // Lazily load folder task-label colors when the Tasks folder tab is opened.
  useEffect(() => {
    if (activeTab !== "beads" || !selectedFolder) return;
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    setLoading(true);
    setError("");
    getSdkClient()
      .taskLabelColors.getFolder({ working_dir: workingDir })
      .then((data) => {
        setEntries(data.entries || []);
        setLoaded(true);
      })
      .catch((err) =>
        setError("Failed to load task label colors: " + err.message),
      )
      .finally(() => setLoading(false));
  }, [activeTab, selectedFolder]);

  // Reset state when switching folders.
  useEffect(() => {
    setEntries([]);
    setError("");
    setLoaded(false);
  }, [selectedFolder]);

  const onAdd = () => setEntries((prev) => addTaskLabelColor(prev));
  const onUpdate = (idx, patch) =>
    setEntries((prev) => updateTaskLabelColor(prev, idx, patch));
  const onRemove = (idx) =>
    setEntries((prev) => removeTaskLabelColor(prev, idx));
  const onMove = (idx, dir) =>
    setEntries((prev) => moveTaskLabelColor(prev, idx, dir));

  // Persist the entries via PUT /api/folders/task-label-colors. Throws on
  // failure; updates local state and notifies open Tasks views on success.
  const persistTaskLabelColors = async () => {
    if (!loaded) return;
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    const trimmed = entries.map((entry) => ({
      label: (entry.label || "").trim(),
      color: (entry.color || "").trim(),
    }));
    if (trimmed.some((entry) => !entry.label)) {
      throw new Error("Task labels must not be empty");
    }
    if (trimmed.some((entry) => !/^#[0-9a-fA-F]{6}$/.test(entry.color))) {
      throw new Error("Task label colors must be six-digit hex values");
    }
    let data;
    try {
      data = await getSdkClient().taskLabelColors.setFolder(workingDir, {
        entries: trimmed,
      });
    } catch (err) {
      throw new Error(errorMessage(err, "Failed to save task label colors"));
    }
    setEntries(data.entries || []);
    // Notify any open Tasks list (BeadsView) so its title colors refresh
    // immediately, without requiring a full page reload.
    window.dispatchEvent(
      new CustomEvent("mitto:folder_task_label_colors_updated", {
        detail: { working_dir: workingDir },
      }),
    );
  };

  return {
    taskLabelColors: { entries, loading, error },
    taskLabelColorsHandlers: { onAdd, onUpdate, onRemove, onMove },
    taskLabelColorsLoaded: loaded,
    persistTaskLabelColors,
  };
}
