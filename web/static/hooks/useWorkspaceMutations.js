// Workspace mutation cluster extracted from WorkspacesDialog.js. Owns handlers
// that mutate the workspaces[] array + selection state:
//   - handleToggleIsDefault: enforce single default per folder
//   - getUnusedServer: pick an ACP server not yet used in a folder
//   - guardNewFolder: warn before switching away from an incomplete new folder
//   - addWorkspace / removeWorkspace / duplicateWorkspace: single-workspace CRUD
//   - addServerToFolder: add another ACP server to an existing folder
// Plus the two derived memos:
//   - isNewFolderIncomplete
//   - folderCanAddServer
//
// setConfirmDialog, setError and getWorkspaceKey are shell-owned and passed in.
const { useCallback, useMemo } = window.preact;

import { getBasename } from "../lib.js";

export function useWorkspaceMutations({
  // Data
  workspaces,
  setWorkspaces,
  acpServers,
  sortedAcpServers,
  groupedWorkspaces,
  // Selection
  selectedFolder,
  setSelectedFolder,
  selectedWorkspace,
  selectedWorkspaceKey,
  setSelectedWorkspaceKey,
  // From useWorkspaceEdits
  setEditIsDefault,
  // From shell
  newFolderKey,
  setNewFolderKey,
  setConfirmDialog,
  setError,
  // Utility (inline in shell — pass as arg to keep hook decoupled)
  getWorkspaceKey,
}) {
  // Toggle the "default workspace for this folder" flag. Enforce a single default
  // per folder live: when enabling it, immediately clear is_default on every other
  // workspace that shares this folder so the UI reflects the change before saving.
  const handleToggleIsDefault = useCallback(
    (checked) => {
      setEditIsDefault(checked);
      if (checked && selectedWorkspace?.working_dir) {
        setWorkspaces((prev) =>
          prev.map((ws) =>
            ws.working_dir === selectedWorkspace.working_dir &&
            getWorkspaceKey(ws) !== selectedWorkspaceKey
              ? { ...ws, is_default: undefined }
              : ws,
          ),
        );
      }
    },
    [
      selectedWorkspace,
      selectedWorkspaceKey,
      setEditIsDefault,
      setWorkspaces,
      getWorkspaceKey,
    ],
  );

  const getUnusedServer = useCallback(
    (workingDir, currentName) => {
      const used = new Set(
        workspaces
          .filter((ws) => ws.working_dir === workingDir)
          .map((ws) => ws.acp_server),
      );
      return (
        acpServers.find((s) => s.name !== currentName && !used.has(s.name))
          ?.name ||
        acpServers.find((s) => !used.has(s.name))?.name ||
        null
      );
    },
    [workspaces, acpServers],
  );

  // Check if the new (incomplete) folder workspace has a valid working_dir
  const isNewFolderIncomplete = useMemo(() => {
    if (!newFolderKey) return false;
    const ws = workspaces.find((w) => getWorkspaceKey(w) === newFolderKey);
    return ws && (!ws.working_dir || ws.working_dir.trim() === "");
  }, [newFolderKey, workspaces, getWorkspaceKey]);

  // Attempt to switch away from an incomplete new folder — warn via dialog and proceed on confirm
  const guardNewFolder = useCallback(
    (onProceed) => {
      if (isNewFolderIncomplete) {
        setConfirmDialog({
          message: "The new workspace has no folder selected. Discard it?",
          confirmLabel: "Discard",
          confirmVariant: "danger",
          onConfirm: () => {
            setWorkspaces((prev) =>
              prev.filter((w) => getWorkspaceKey(w) !== newFolderKey),
            );
            setNewFolderKey(null);
            setConfirmDialog(null);
            onProceed();
          },
        });
        return;
      }
      onProceed();
    },
    [
      isNewFolderIncomplete,
      newFolderKey,
      setConfirmDialog,
      setNewFolderKey,
      setWorkspaces,
      getWorkspaceKey,
    ],
  );

  const addWorkspace = useCallback(() => {
    if (acpServers.length === 0) return;
    // Don't allow creating another while one is incomplete
    if (isNewFolderIncomplete) {
      setError("Please select a folder for the current new workspace first");
      return;
    }
    const server = sortedAcpServers[0];
    const newWs = {
      uuid: crypto.randomUUID(),
      working_dir: "",
      acp_server: server.name,
      restricted_runner: "exec",
    };
    const key = getWorkspaceKey(newWs);
    setWorkspaces([...workspaces, newWs]);
    setNewFolderKey(key);
    setSelectedFolder("New Workspace");
    setSelectedWorkspaceKey(null);
    setError("");
  }, [
    acpServers,
    isNewFolderIncomplete,
    sortedAcpServers,
    workspaces,
    setWorkspaces,
    setNewFolderKey,
    setSelectedFolder,
    setSelectedWorkspaceKey,
    setError,
    getWorkspaceKey,
  ]);

  const removeWorkspace = useCallback(
    (key) => {
      if (workspaces.length <= 1) {
        setError("At least one workspace is required");
        return;
      }
      const ws = workspaces.find((w) => getWorkspaceKey(w) === key);
      if (!ws) return;
      const folderName = ws.name || getBasename(ws.working_dir);
      setConfirmDialog({
        message: `Do you want to delete ${ws.acp_server} in workspace ${folderName}?`,
        title: "Delete Workspace",
        confirmLabel: "Delete",
        confirmVariant: "danger",
        onConfirm: () => {
          setConfirmDialog(null);
          const remaining = workspaces.filter(
            (w) => getWorkspaceKey(w) !== key,
          );
          setWorkspaces(remaining);
          const siblings = remaining.filter(
            (w) => w.working_dir === ws.working_dir,
          );
          if (siblings.length > 0) {
            setSelectedFolder(folderName);
            setSelectedWorkspaceKey(null);
          } else if (remaining.length > 0) {
            setSelectedWorkspaceKey(getWorkspaceKey(remaining[0]));
            setSelectedFolder(null);
          } else {
            setSelectedWorkspaceKey(null);
            setSelectedFolder(null);
          }
        },
      });
    },
    [
      workspaces,
      setWorkspaces,
      setError,
      setConfirmDialog,
      setSelectedFolder,
      setSelectedWorkspaceKey,
      getWorkspaceKey,
    ],
  );

  const duplicateWorkspace = useCallback(
    (key) => {
      const ws = workspaces.find((w) => getWorkspaceKey(w) === key);
      if (!ws) return;
      const altName = getUnusedServer(ws.working_dir, ws.acp_server);
      if (!altName) {
        setError(
          "Cannot duplicate: all ACP servers already used for this folder",
        );
        return;
      }
      const altSrv = acpServers.find((s) => s.name === altName);
      if (!altSrv) {
        setError("Cannot duplicate: alternative server not found");
        return;
      }
      const dup = {
        uuid: crypto.randomUUID(),
        working_dir: ws.working_dir,
        acp_server: altName,
        restricted_runner: ws.restricted_runner || "exec",
        ...(ws.name && { name: ws.name }),
        ...(ws.code && { code: ws.code }),
        ...(ws.color && { color: ws.color }),
      };
      const idx = workspaces.findIndex((w) => getWorkspaceKey(w) === key);
      const next = [...workspaces];
      next.splice(idx + 1, 0, dup);
      setWorkspaces(next);
      setSelectedWorkspaceKey(getWorkspaceKey(dup));
    },
    [
      workspaces,
      acpServers,
      getUnusedServer,
      setWorkspaces,
      setSelectedWorkspaceKey,
      setError,
      getWorkspaceKey,
    ],
  );

  // Add a new ACP server entry to the selected folder
  const addServerToFolder = useCallback(() => {
    if (!selectedFolder) return;
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs) return;
    const unusedServer = getUnusedServer(firstWs.working_dir, null);
    if (!unusedServer) {
      setError("All ACP servers are already assigned to this folder");
      return;
    }
    const server = acpServers.find((s) => s.name === unusedServer);
    if (!server) return;
    const newWs = {
      uuid: crypto.randomUUID(),
      working_dir: firstWs.working_dir,
      acp_server: unusedServer,
      restricted_runner: "exec",
      ...(firstWs.name && { name: firstWs.name }),
      ...(firstWs.code && { code: firstWs.code }),
      ...(firstWs.color && { color: firstWs.color }),
      ...(firstWs.group && { group: firstWs.group }),
    };
    setWorkspaces([...workspaces, newWs]);
    setSelectedWorkspaceKey(getWorkspaceKey(newWs));
    setSelectedFolder(null);
  }, [
    selectedFolder,
    groupedWorkspaces,
    acpServers,
    workspaces,
    getUnusedServer,
    setWorkspaces,
    setSelectedWorkspaceKey,
    setSelectedFolder,
    setError,
    getWorkspaceKey,
  ]);

  // Check if folder has unused ACP servers available
  const folderCanAddServer = useMemo(() => {
    if (!selectedFolder) return false;
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs) return false;
    return getUnusedServer(firstWs.working_dir, null) !== null;
  }, [selectedFolder, groupedWorkspaces, getUnusedServer]);

  return {
    // Memos
    isNewFolderIncomplete,
    folderCanAddServer,
    // Handlers
    handleToggleIsDefault,
    getUnusedServer,
    guardNewFolder,
    addWorkspace,
    removeWorkspace,
    duplicateWorkspace,
    addServerToFolder,
  };
}
