// Workspace-level edit fields (transient in-dialog scalar state) extracted
// from WorkspacesDialog.js. Owns:
//   - 11 useStates for edit* fields (acp server, aux/initial model
//     profile+tag+cleared flag, runner+config, auto_approve, is_default,
//     acp_command_override)
//   - prevSelectedWorkspaceKeyRef (tracks previously-selected key so edits
//     can be flushed back into the workspaces array on selection change)
//   - buildWorkspaceEditsFor(ws, targetKey) helper (used to flush on change
//     and to commit the selected workspace at save time)
//   - applyWorkspaceEdits(ws) — buildWorkspaceEditsFor bound to the current
//     selectedWorkspaceKey
//   - populate + flush effect keyed on [selectedWorkspaceKey]
const { useState, useEffect, useRef, useCallback } = window.preact;

export function useWorkspaceEdits({
  selectedWorkspace,
  selectedWorkspaceKey,
  setWorkspaces,
  getWorkspaceKey,
}) {
  const [editAcpServer, setEditAcpServer] = useState("");
  const [editAuxModelProfile, setEditAuxModelProfile] = useState("");
  const [editAuxModelTag, setEditAuxModelTag] = useState("");
  // Whether the user has explicitly cleared a legacy raw auxiliary model
  // constraint by picking "-- None --" (vs. never having touched the control).
  const [editAuxModelConstraintCleared, setEditAuxModelConstraintCleared] =
    useState(false);
  // Per-workspace initial-model preference applied as the baseline model of
  // every new conversation created in this workspace. Mutually exclusive:
  // profile wins server-side when both are set.
  const [editInitialModelProfile, setEditInitialModelProfile] = useState("");
  const [editInitialModelTag, setEditInitialModelTag] = useState("");
  const [editRunner, setEditRunner] = useState("exec");
  const [editRunnerConfig, setEditRunnerConfig] = useState(null);
  const [editAutoApprove, setEditAutoApprove] = useState(false);
  const [editIsDefault, setEditIsDefault] = useState(false);
  const [editAcpCommandOverride, setEditAcpCommandOverride] = useState("");

  const prevSelectedWorkspaceKeyRef = useRef(null);

  // Build a workspace object with the current transient edit fields applied,
  // but only for the workspace matching targetKey; all others pass through
  // unchanged. Used both to flush edits on selection change and to commit the
  // currently-selected workspace at save time.
  const buildWorkspaceEditsFor = useCallback(
    (ws, targetKey) => {
      if (getWorkspaceKey(ws) !== targetKey) return ws;
      // A selected profile (or an explicit "-- None --") always wins over any
      // legacy raw matchMode/pattern constraint. Otherwise, an untouched
      // legacy raw constraint is preserved as-is.
      const rawAuxModelConstraint = ws.auxiliary_model_selection || null;
      const auxModelSelection =
        !editAuxModelProfile &&
        rawAuxModelConstraint &&
        !editAuxModelConstraintCleared
          ? rawAuxModelConstraint
          : undefined;
      return {
        ...ws,
        acp_server: editAcpServer,
        auxiliary_model_profile: editAuxModelProfile || undefined,
        auxiliary_model_tag: editAuxModelTag || undefined,
        auxiliary_model_selection: auxModelSelection,
        initial_model_profile: editInitialModelProfile || undefined,
        initial_model_tag: editInitialModelTag || undefined,
        restricted_runner: editRunner,
        restricted_runner_config:
          editRunner !== "exec" ? editRunnerConfig : undefined,
        auto_approve: editAutoApprove || undefined,
        is_default: editIsDefault || undefined,
        acp_command_override: editAcpCommandOverride || undefined,
      };
    },
    [
      getWorkspaceKey,
      editAcpServer,
      editAuxModelProfile,
      editAuxModelTag,
      editAuxModelConstraintCleared,
      editInitialModelProfile,
      editInitialModelTag,
      editRunner,
      editRunnerConfig,
      editAutoApprove,
      editIsDefault,
      editAcpCommandOverride,
    ],
  );

  // Apply workspace-level edits to the selected workspace.
  const applyWorkspaceEdits = useCallback(
    (ws) => buildWorkspaceEditsFor(ws, selectedWorkspaceKey),
    [buildWorkspaceEditsFor, selectedWorkspaceKey],
  );

  // When a workspace child is selected: flush the previously-selected
  // workspace's transient edits into the workspaces array, then populate the
  // scalar edit fields for the new selection. Keyed on [selectedWorkspaceKey]
  // to match the shell's original effect.
  useEffect(() => {
    // The scalar edit state still holds the previous workspace's values at
    // this point, so applying them against prevKey commits those edits. This
    // also runs when navigating to a folder (selectedWorkspaceKey becomes
    // null) so edits are not lost there.
    const prevKey = prevSelectedWorkspaceKeyRef.current;
    if (prevKey && prevKey !== selectedWorkspaceKey) {
      setWorkspaces((prev) =>
        prev.map((ws) => buildWorkspaceEditsFor(ws, prevKey)),
      );
    }
    prevSelectedWorkspaceKeyRef.current = selectedWorkspaceKey;

    if (!selectedWorkspace) return;
    setEditAcpServer(selectedWorkspace.acp_server || "");
    setEditAuxModelProfile(selectedWorkspace.auxiliary_model_profile || "");
    setEditAuxModelTag(selectedWorkspace.auxiliary_model_tag || "");
    setEditAuxModelConstraintCleared(false);
    setEditInitialModelProfile(selectedWorkspace.initial_model_profile || "");
    setEditInitialModelTag(selectedWorkspace.initial_model_tag || "");
    setEditAcpCommandOverride(selectedWorkspace.acp_command_override || "");
    setEditRunner(selectedWorkspace.restricted_runner || "exec");
    setEditRunnerConfig(selectedWorkspace.restricted_runner_config || null);
    setEditAutoApprove(selectedWorkspace.auto_approve === true);
    setEditIsDefault(selectedWorkspace.is_default === true);
    // NOTE: buildWorkspaceEditsFor intentionally omitted from deps to match
    // the shell's original effect (keyed only on [selectedWorkspaceKey]).
    // Adding it would re-fire the flush every render.
  }, [selectedWorkspaceKey]);

  return {
    editAcpServer,
    editAuxModelProfile,
    editAuxModelTag,
    editAuxModelConstraintCleared,
    editInitialModelProfile,
    editInitialModelTag,
    editRunner,
    editRunnerConfig,
    editAutoApprove,
    editIsDefault,
    editAcpCommandOverride,
    setEditAcpServer,
    setEditAuxModelProfile,
    setEditAuxModelTag,
    setEditAuxModelConstraintCleared,
    setEditInitialModelProfile,
    setEditInitialModelTag,
    setEditRunner,
    setEditRunnerConfig,
    setEditAutoApprove,
    setEditIsDefault,
    setEditAcpCommandOverride,
    buildWorkspaceEditsFor,
    applyWorkspaceEdits,
    prevSelectedWorkspaceKeyRef,
  };
}
