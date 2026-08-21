// Mitto Web Interface — Folder Processors Config Hook
//
// Owns all folder-processors state, the tab-open load effect, and the two CRUD
// handlers (toggle-enabled and save-arguments) for the folder-level Processors
// tab. Extracted verbatim from WorkspacesDialog.js (mitto-90f.4 Increment D-3)
// so the shell can drop ~90 LOC and pass three grouped objects
// (processors/processorsSetters/processorsHandlers) instead of 8 individual
// props through WorkspaceFolderEditor.
//
// Behavior-preserving: state names, setter names, handler names, effect deps
// and error-banner surfacing all match the original shell code exactly. The
// hook takes selectedFolder, activeTab, groupedWorkspaces, getSelectedFolderUuid
// and setError as arguments (getSelectedFolderUuid stays owned by the shell
// because other shell handlers still use it; setError is the shell-owned
// shared error banner that toggleProcessorEnabled/saveProcessorArguments
// surface failures through).

const { useState, useEffect } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";

/**
 * useFolderProcessorsConfig — cohesive state/handler bundle for the folder
 * Processors tab.
 *
 * @param {Object} params
 * @param {string|null} params.selectedFolder — the currently selected folder display name
 * @param {string} params.activeTab — the currently active folder tab id
 * @param {Array} params.groupedWorkspaces — the shell's memoized grouped workspaces (effect dep)
 * @param {() => string|null} params.getSelectedFolderUuid — resolver for the folder's workspace uuid
 * @param {(msg: string) => void} params.setError — shell-owned setter for the shared error banner
 * @returns {{ processors: Object, processorsSetters: Object, processorsHandlers: Object }}
 */
export function useFolderProcessorsConfig({
  selectedFolder,
  activeTab,
  groupedWorkspaces,
  getSelectedFolderUuid,
  setError,
}) {
  // Folder processors state (for the Processors tab)
  const [folderProcessors, setFolderProcessors] = useState([]);
  const [processorsLoading, setProcessorsLoading] = useState(false);
  const [expandedProcessor, setExpandedProcessor] = useState(null);
  // Local argument edit state: { [procName]: { [paramName]: value } }
  // Seeded lazily on first edit; cleared after a successful Save.
  const [processorArgEdits, setProcessorArgEdits] = useState({});

  // Load processors when a folder is selected and the Processors tab is active
  useEffect(() => {
    if (!selectedFolder || activeTab !== "processors") return;
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs?.uuid) return;

    setProcessorsLoading(true);
    getSdkClient()
      .processors.list(firstWs.uuid)
      .then((data) => {
        setFolderProcessors(data.processors || []);
      })
      .catch((err) => console.error("Failed to load processors:", err))
      .finally(() => setProcessorsLoading(false));
  }, [selectedFolder, activeTab, groupedWorkspaces]);

  // Reload processors for the selected folder
  const reloadFolderProcessors = async (uuid) => {
    const data = await getSdkClient().processors.list(uuid);
    setFolderProcessors(data.processors || []);
  };

  // Toggle enabled state for a processor via PATCH /api/workspaces/{uuid}/processors/{name}.
  const toggleProcessorEnabled = async (processor) => {
    const uuid = getSelectedFolderUuid();
    if (!uuid) return;
    try {
      await getSdkClient().processors.setEnabled(
        uuid,
        processor.name,
        !processor.enabled,
      );
      await reloadFolderProcessors(uuid);
    } catch (err) {
      setError(
        "Failed to toggle processor: " + errorMessage(err, "request failed"),
      );
    }
  };

  // Save per-workspace argument overrides for a prompt-mode processor via PUT
  // /api/workspaces/{uuid}/processors/{name}/arguments.
  // Sends all declared params (edited value or current effective value).
  // Empty string clears the override for that param (reverts to declared default).
  const saveProcessorArguments = async (proc) => {
    const uuid = getSelectedFolderUuid();
    if (!uuid) return;
    const procEdits = processorArgEdits[proc.name] || {};
    const args = {};
    for (const p of proc.parameters || []) {
      args[p.name] =
        procEdits[p.name] !== undefined ? procEdits[p.name] : p.value;
    }
    try {
      await getSdkClient().processors.setArguments(uuid, proc.name, args);
      await reloadFolderProcessors(uuid);
      // Clear local edits so inputs re-seed from the freshly-loaded effective values.
      setProcessorArgEdits((prev) => {
        const n = { ...prev };
        delete n[proc.name];
        return n;
      });
    } catch (err) {
      setError(
        "Failed to save processor arguments: " +
          errorMessage(err, "request failed"),
      );
    }
  };

  const processors = {
    folderProcessors,
    processorsLoading,
    expandedProcessor,
    processorArgEdits,
  };
  const processorsSetters = {
    setExpandedProcessor,
    setProcessorArgEdits,
  };
  const processorsHandlers = {
    toggleProcessorEnabled,
    saveProcessorArguments,
  };
  return { processors, processorsSetters, processorsHandlers };
}
