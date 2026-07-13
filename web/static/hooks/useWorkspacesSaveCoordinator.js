// Mitto Web Interface — Workspaces Save Coordinator Hook
//
// Owns the handleSave async orchestration extracted from WorkspacesDialog.js
// (mitto-90f.4 Increment D-8). Pure orchestrator: no local state, no effects;
// composes edits from useFolderGeneralEdits/useWorkspaceEdits/etc, persists via
// existing hook actions (persistMetadata, persistUserDataSchema,
// persistShortcuts) and the /config endpoint, then fires onSave/showToast.
//
// Behavior-preserving: the 125 LOC body was moved byte-equivalent aside from
// dropping the unused `const result =` binding (was never read). All dep-array
// entries mirror closure references so the useCallback identity stays stable
// across the shell's re-renders.

const { useCallback } = window.preact;

import {
  secureFetch,
  endpoints,
  errorMessageFromData,
  fetchConfig,
  invalidateConfigCache,
} from "../utils/index.js";

export function useWorkspacesSaveCoordinator({
  // Selection
  selectedFolder,
  selectedWorkspaceKey,
  selectedWorkspace,
  groupedWorkspaces,
  workspaces,
  // Workspace-level edits
  editIsDefault,
  applyWorkspaceEdits,
  getWorkspaceKey,
  // Folder-level edits
  applyFolderEdits,
  // Metadata persistence (from useFolderMetadataConfig)
  persistMetadata,
  persistUserDataSchema,
  // Shortcuts persistence (from useFolderShortcutsConfig)
  shortcutsLoaded,
  persistShortcuts,
  // Add-folder flow guard
  isNewFolderIncomplete,
  // Shell setters
  setWorkspaces,
  setNewFolderKey,
  setSaving,
  setError,
  // External callbacks
  onSave,
  showToast,
}) {
  const handleSave = useCallback(async () => {
    // Block save if there's an incomplete new folder
    if (isNewFolderIncomplete) {
      setError("Please select a folder for the new workspace before saving");
      return;
    }
    setSaving(true);
    const saveStartTime = Date.now();
    setError("");
    try {
      // Filter out any workspaces with empty working_dir (safety net)
      let updated = workspaces.filter(
        (ws) => ws.working_dir && ws.working_dir.trim() !== "",
      );

      // Apply folder-level edits if a folder is selected
      if (selectedFolder) {
        const folderGroup = groupedWorkspaces.find(
          (g) => g.displayName === selectedFolder,
        );
        const folderWorkingDir = folderGroup?.workspaces[0]?.working_dir;
        if (folderWorkingDir) {
          updated = updated.map((ws) => applyFolderEdits(ws, folderWorkingDir));
        }
      }

      // Apply workspace-level edits if a workspace is selected
      if (selectedWorkspaceKey) {
        updated = updated.map(applyWorkspaceEdits);

        // Enforce a single default workspace per folder: if the selected workspace
        // was marked default, clear is_default on the other workspaces in the same folder.
        if (editIsDefault && selectedWorkspace?.working_dir) {
          updated = updated.map((ws) =>
            ws.working_dir === selectedWorkspace.working_dir &&
            getWorkspaceKey(ws) !== selectedWorkspaceKey
              ? { ...ws, is_default: undefined }
              : ws,
          );
        }
      }

      if (updated.length === 0) {
        setError("At least one workspace is required");
        const elapsed = Date.now() - saveStartTime;
        setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed));
        return;
      }

      const config = await fetchConfig(null, true);
      // The Workspaces dialog must never touch external-access auth/host/port — those
      // belong to the Settings dialog. Omit the `web` section entirely so the backend
      // preserves the existing auth config and never validates a password here.
      const { web: _omitWeb, ...configWithoutWeb } = config;
      const res = await secureFetch(endpoints.config.update(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...configWithoutWeb,
          workspaces: updated,
          prompts: [],
        }),
      });
      if (!res.ok) {
        let errData = null;
        try {
          errData = await res.json();
        } catch (_e) {
          /* non-JSON error body */
        }
        throw new Error(
          errorMessageFromData(errData, "Failed to save configuration"),
        );
      }
      await res.json();
      invalidateConfigCache();

      // Save workspace metadata after config save (workspace must exist first).
      // Both persist actions live in useFolderMetadataConfig; they no-op when
      // no folder is selected or the folder has no resolvable uuid.
      try {
        await persistMetadata();
      } catch (metaErr) {
        setError("Failed to save metadata: " + metaErr.message);
        const elapsed = Date.now() - saveStartTime;
        setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed));
        return;
      }
      try {
        await persistUserDataSchema();
      } catch (schemaErr) {
        setError("Failed to save user data schema: " + schemaErr.message);
        const elapsed = Date.now() - saveStartTime;
        setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed));
        return;
      }

      // Persist folder shortcuts if the Shortcuts tab was opened/edited.
      if (selectedFolder && shortcutsLoaded) {
        try {
          await persistShortcuts();
        } catch (scErr) {
          setError("Failed to save shortcuts: " + scErr.message);
          const elapsed = Date.now() - saveStartTime;
          setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed));
          return;
        }
      }

      setWorkspaces(updated);
      setNewFolderKey(null);
      onSave?.();
      showToast?.({
        style: "success",
        title: "Workspaces saved",
        duration: 2000,
      });
    } catch (err) {
      setError(err.message);
    } finally {
      const elapsed = Date.now() - saveStartTime;
      const remaining = Math.max(0, 1000 - elapsed);
      setTimeout(() => setSaving(false), remaining);
    }
  }, [
    isNewFolderIncomplete,
    selectedFolder,
    selectedWorkspaceKey,
    selectedWorkspace,
    groupedWorkspaces,
    workspaces,
    editIsDefault,
    applyWorkspaceEdits,
    getWorkspaceKey,
    applyFolderEdits,
    persistMetadata,
    persistUserDataSchema,
    shortcutsLoaded,
    persistShortcuts,
    setWorkspaces,
    setNewFolderKey,
    setSaving,
    setError,
    onSave,
    showToast,
  ]);

  return { handleSave };
}
