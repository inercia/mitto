// Mitto Web Interface — Folder Prompts Config Hook
//
// Owns all folder-prompts state, the tab-open load effect, and the four CRUD
// handlers (reload, save, delete, toggle-enabled) for the folder-level Prompts
// tab. Extracted verbatim from WorkspacesDialog.js (mitto-90f.4 Increment D-2)
// so the shell can drop ~200 LOC and pass three grouped objects
// (prompts/promptsSetters/promptsHandlers) instead of 16 individual props
// through WorkspaceFolderEditor down to WorkspaceFolderPromptsTab.
//
// Behavior-preserving: state names, setter names, handler names, effect deps
// and error-banner surfacing all match the original shell code exactly. The
// hook takes selectedFolder, activeTab, groupedWorkspaces, getSelectedFolderDir
// and setError as arguments (getSelectedFolderDir stays owned by the shell
// because other shell handlers still use it; setError is the shell-owned
// shared error banner that togglePromptEnabled/saveWorkspacePrompt/
// deleteWorkspacePrompt surface failures through).

const { useState, useEffect } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";

/**
 * useFolderPromptsConfig — cohesive state/handler bundle for the folder
 * Prompts tab.
 *
 * @param {Object} params
 * @param {string|null} params.selectedFolder — the currently selected folder display name
 * @param {string} params.activeTab — the currently active folder tab id
 * @param {Array} params.groupedWorkspaces — the shell's memoized grouped workspaces (effect dep)
 * @param {() => string|null} params.getSelectedFolderDir — resolver for the folder's working_dir
 * @param {(msg: string) => void} params.setError — shell-owned setter for the shared error banner
 * @returns {{ prompts: Object, promptsSetters: Object, promptsHandlers: Object }}
 */
export function useFolderPromptsConfig({
  selectedFolder,
  activeTab,
  groupedWorkspaces,
  getSelectedFolderDir,
  setError,
}) {
  // Folder prompts state (for the Prompts tab)
  const [folderPrompts, setFolderPrompts] = useState([]);
  const [promptsLoading, setPromptsLoading] = useState(false);
  const [showAddPrompt, setShowAddPrompt] = useState(false);
  const [editingPromptIndex, setEditingPromptIndex] = useState(null);
  const [editPromptName, setEditPromptName] = useState("");
  const [editPromptText, setEditPromptText] = useState("");
  const [editPromptColor, setEditPromptColor] = useState("");
  const [editPromptGroup, setEditPromptGroup] = useState("");
  const [newPromptName, setNewPromptName] = useState("");
  const [newPromptText, setNewPromptText] = useState("");
  const [newPromptColor, setNewPromptColor] = useState("");
  const [newPromptGroup, setNewPromptGroup] = useState("");
  const [promptSaving, setPromptSaving] = useState(false);

  // Load prompts when a folder is selected and the Prompts tab is active
  useEffect(() => {
    if (!selectedFolder || activeTab !== "prompts") return;
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs?.working_dir) return;

    setPromptsLoading(true);
    getSdkClient()
      .prompts.list({
        working_dir: firstWs.working_dir,
        include_global: true,
      })
      .then((data) => {
        setFolderPrompts(data.prompts || []);
      })
      .catch((err) => console.error("Failed to load prompts:", err))
      .finally(() => setPromptsLoading(false));
  }, [selectedFolder, activeTab, groupedWorkspaces]);

  // Load (reload) prompts for the selected folder
  const reloadFolderPrompts = async (workingDir) => {
    const data = await getSdkClient().prompts.list({
      working_dir: workingDir,
      include_global: true,
    });
    setFolderPrompts(data.prompts || []);
  };

  // Create or update a workspace prompt file
  const saveWorkspacePrompt = async (promptData) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    setPromptSaving(true);
    try {
      await getSdkClient().prompts.create({
        working_dir: workingDir,
        ...promptData,
      });
      await reloadFolderPrompts(workingDir);
    } catch (err) {
      setError("Failed to save prompt: " + errorMessage(err, "request failed"));
    } finally {
      setPromptSaving(false);
    }
  };

  // Delete a workspace prompt file by name
  const deleteWorkspacePrompt = async (promptName) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    try {
      await getSdkClient().prompts.remove({
        working_dir: workingDir,
        name: promptName,
      });
      await reloadFolderPrompts(workingDir);
    } catch (err) {
      setError(
        "Failed to delete prompt: " + errorMessage(err, "request failed"),
      );
    }
  };

  // Toggle enabled state for a prompt via PATCH /api/workspace-prompts/{name}?working_dir=.
  // If a .prompt.yaml file exists in .mitto/prompts/, its enabled field is updated in-place.
  // If not, the state is recorded in the workspace .mittorc file.
  const togglePromptEnabled = async (prompt) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    const isCurrentlyEnabled = prompt.enabled !== false;
    try {
      await getSdkClient().prompts.setEnabled(
        prompt.name,
        workingDir,
        !isCurrentlyEnabled,
      );
      await reloadFolderPrompts(workingDir);
    } catch (err) {
      setError(
        "Failed to toggle prompt: " + errorMessage(err, "request failed"),
      );
    }
  };

  const prompts = {
    folderPrompts,
    promptsLoading,
    showAddPrompt,
    editingPromptIndex,
    editPromptName,
    editPromptText,
    editPromptColor,
    editPromptGroup,
    newPromptName,
    newPromptText,
    newPromptColor,
    newPromptGroup,
    promptSaving,
  };
  const promptsSetters = {
    setShowAddPrompt,
    setEditingPromptIndex,
    setEditPromptName,
    setEditPromptText,
    setEditPromptColor,
    setEditPromptGroup,
    setNewPromptName,
    setNewPromptText,
    setNewPromptColor,
    setNewPromptGroup,
  };
  const promptsHandlers = {
    saveWorkspacePrompt,
    deleteWorkspacePrompt,
    togglePromptEnabled,
  };
  return { prompts, promptsSetters, promptsHandlers };
}
