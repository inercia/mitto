// Mitto Web Interface — Folder General Edits Hook
//
// Owns the folder-level general edit fields (name, code, color, group,
// auto-children), the folder-selection populate effect, the group suggestions
// memo, and the applyFolderEdits helper that handleSave uses to project the
// edits onto every workspace sharing the folder's working_dir. Extracted from
// WorkspacesDialog.js (mitto-90f.4 Increment D-7) so the shell can drop the
// 5 useStates + memo + effect + helper and pass grouped {edits, editSetters,
// folderGroupSuggestions, applyFolderEdits} instead of 11 individual props
// through WorkspaceFolderEditor.
//
// Behavior-preserving: state names, setter names, effect deps ([selectedFolder]),
// memo deps ([workspaces, editGroup]), and applyFolderEdits shape match the
// pre-extraction shell code exactly. The activeTab reset that used to share
// this effect stays in the shell (activeTab is orchestrated there).

const { useState, useEffect, useMemo, useCallback } = window.preact;

import { getWorkspaceVisualInfo } from "../lib.js";

export function useFolderGeneralEdits({
  selectedFolder,
  groupedWorkspaces,
  workspaces,
}) {
  const [editName, setEditName] = useState("");
  const [editCode, setEditCode] = useState("");
  const [editColor, setEditColor] = useState("");
  const [editGroup, setEditGroup] = useState("");
  const [editAutoChildren, setEditAutoChildren] = useState([]);

  // When a folder is selected, populate folder-level edit fields from the
  // first workspace in the group.
  useEffect(() => {
    if (!selectedFolder) return;
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs) return;
    setEditName(firstWs.name || "");
    setEditCode(firstWs.code || "");
    setEditGroup(firstWs.group || "");
    setEditColor(
      firstWs.color ||
        getWorkspaceVisualInfo(firstWs.working_dir).color.backgroundHex ||
        "#808080",
    );
    setEditAutoChildren(firstWs.auto_children || []);
  }, [selectedFolder]);

  // Unique folder groups across all workspaces, used to suggest existing groups
  // (so users can unify on the same label). Includes the value currently being
  // edited so a freshly-typed group also appears in the list.
  const folderGroupSuggestions = useMemo(() => {
    const set = new Set();
    workspaces.forEach((ws) => {
      if (ws.group && ws.group.trim()) set.add(ws.group.trim());
    });
    if (editGroup && editGroup.trim()) set.add(editGroup.trim());
    return Array.from(set).sort((a, b) => a.localeCompare(b));
  }, [workspaces, editGroup]);

  // Apply folder-level edits (name, code, color, group, auto-children) to all
  // workspaces sharing folderWorkingDir. Called from handleSave in the shell.
  const applyFolderEdits = useCallback(
    (ws, folderWorkingDir) => {
      if (ws.working_dir !== folderWorkingDir) return ws;
      return {
        ...ws,
        name: editName || undefined,
        code: (editCode || "").toUpperCase().slice(0, 3) || undefined,
        color: editColor || undefined,
        group: editGroup.trim() || undefined,
        auto_children:
          editAutoChildren.length > 0 ? editAutoChildren : undefined,
      };
    },
    [editName, editCode, editColor, editGroup, editAutoChildren],
  );

  return {
    edits: { editName, editCode, editColor, editGroup, editAutoChildren },
    editSetters: {
      setEditName,
      setEditCode,
      setEditColor,
      setEditGroup,
      setEditAutoChildren,
    },
    folderGroupSuggestions,
    applyFolderEdits,
  };
}
