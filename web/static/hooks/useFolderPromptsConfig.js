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

import {
  authFetch,
  secureFetch,
  endpoints,
  errorMessageFromData,
} from "../utils/index.js";

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
    authFetch(
      endpoints.workspacePrompts.list({
        working_dir: firstWs.working_dir,
        include_global: true,
      }),
    )
      .then((r) => r.json())
      .then((data) => {
        setFolderPrompts(data.prompts || []);
      })
      .catch((err) => console.error("Failed to load prompts:", err))
      .finally(() => setPromptsLoading(false));
  }, [selectedFolder, activeTab, groupedWorkspaces]);

  // Load (reload) prompts for the selected folder
  const reloadFolderPrompts = async (workingDir) => {
    const res = await authFetch(
      endpoints.workspacePrompts.list({
        working_dir: workingDir,
        include_global: true,
      }),
    );
    const data = await res.json();
    setFolderPrompts(data.prompts || []);
  };

  // Create or update a workspace prompt file
  const saveWorkspacePrompt = async (promptData) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    setPromptSaving(true);
    try {
      const res = await secureFetch(endpoints.workspacePrompts.create(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, ...promptData }),
      });
      if (!res.ok) {
        const ct = res.headers.get("content-type");
        if (ct && ct.includes("application/json")) {
          const data = await res.json();
          throw new Error(errorMessageFromData(data, "request failed"));
        }
        throw new Error(await res.text());
      }
      await reloadFolderPrompts(workingDir);
    } catch (err) {
      setError("Failed to save prompt: " + err.message);
    } finally {
      setPromptSaving(false);
    }
  };

  // Delete a workspace prompt file by name
  const deleteWorkspacePrompt = async (promptName) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    try {
      const res = await secureFetch(
        endpoints.workspacePrompts.list({
          working_dir: workingDir,
          name: promptName,
        }),
        { method: "DELETE" },
      );
      if (!res.ok) {
        const ct = res.headers.get("content-type");
        if (ct && ct.includes("application/json")) {
          const data = await res.json();
          throw new Error(errorMessageFromData(data, "request failed"));
        }
        throw new Error(await res.text());
      }
      await reloadFolderPrompts(workingDir);
    } catch (err) {
      setError("Failed to delete prompt: " + err.message);
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
      const res = await secureFetch(
        endpoints.workspacePrompts.update(prompt.name, {
          working_dir: workingDir,
        }),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ enabled: !isCurrentlyEnabled }),
        },
      );
      if (!res.ok) {
        const ct = res.headers.get("content-type");
        if (ct && ct.includes("application/json")) {
          const data = await res.json();
          throw new Error(errorMessageFromData(data, "request failed"));
        }
        throw new Error(await res.text());
      }
      await reloadFolderPrompts(workingDir);
    } catch (err) {
      setError("Failed to toggle prompt: " + err.message);
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
