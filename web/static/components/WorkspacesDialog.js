// Mitto Web Interface - Workspaces Dialog Component
const { useState, useEffect, useMemo, useCallback, useRef, html } =
  window.preact;

import { authFetch, endpoints } from "../utils/index.js";

import { getBasename } from "../lib.js";

import { useBeadsFolderConfig } from "../hooks/useBeadsFolderConfig.js";
import { useFolderGeneralEdits } from "../hooks/useFolderGeneralEdits.js";
import { useFolderPromptsConfig } from "../hooks/useFolderPromptsConfig.js";
import { useFolderProcessorsConfig } from "../hooks/useFolderProcessorsConfig.js";
import { useFolderShortcutsConfig } from "../hooks/useFolderShortcutsConfig.js";
import { useFolderMetadataConfig } from "../hooks/useFolderMetadataConfig.js";
import { useWorkspaceEdits } from "../hooks/useWorkspaceEdits.js";
import { useWorkspaceMcpActions } from "../hooks/useWorkspaceMcpActions.js";
import { useWorkspaceMcpTools } from "../hooks/useWorkspaceMcpTools.js";
import { useWorkspaceMutations } from "../hooks/useWorkspaceMutations.js";
import { useWorkspacesData } from "../hooks/useWorkspacesData.js";
import { useWorkspacesSaveCoordinator } from "../hooks/useWorkspacesSaveCoordinator.js";

import { SpinnerIcon, CloseIcon, FolderIcon } from "./Icons.js";

import { ConfirmDialog } from "./ConfirmDialog.js";
import { Modal } from "./Modal.js";
import { WorkspaceBadge } from "./WorkspaceBadge.js";

import { McpInstallDialog } from "./McpInstallDialog.js";
import { WorkspaceEditor } from "./WorkspaceEditor.js";
import { WorkspaceFolderEditor } from "./WorkspaceFolderEditor.js";
import { WorkspacesLeftPanel } from "./WorkspacesLeftPanel.js";

// Section descriptors for the folder Shortcuts tab. Section IDs match those
// persisted on the server (folders.json) and used by the render-time toolbars.
const SHORTCUT_SECTIONS = [
  {
    id: "tasksList",
    label: "Tasks list",
    desc: "Buttons shown in the Tasks list toolbar.",
  },
  {
    id: "conversations",
    label: "Conversation",
    desc: "Buttons shown in the conversation toolbar; run in the current conversation.",
  },
  {
    id: "beadsIssue",
    label: "Beads issue",
    desc: "Buttons shown in the beads issue detail toolbar; start a new conversation for the issue.",
  },
];

// When the tree has more folders than this, they start collapsed by default.
// Users can still expand individual folders; that explicit choice is persisted
// and always wins over this count-based default.
const WORKSPACES_EDITOR_COLLAPSE_THRESHOLD = 5;

// Helpers to persist per-folder expansion state for the workspaces editor tree.
function getEditorFolderExpansion(folderName, defaultExpanded = true) {
  try {
    const state = localStorage.getItem(
      `workspaces-editor-folder-${folderName}`,
    );
    return state === null ? defaultExpanded : state === "true";
  } catch (e) {
    return defaultExpanded;
  }
}

function setEditorFolderExpansion(folderName, expanded) {
  try {
    localStorage.setItem(
      `workspaces-editor-folder-${folderName}`,
      String(expanded),
    );
  } catch (e) {
    // Ignore localStorage errors
  }
}

export function WorkspacesDialog({
  isOpen,
  onClose,
  onSave,
  initialWorkingDir,
  initialTab,
  showToast,
  onOpenPromptParamDialog,
}) {
  const [saving, setSaving] = useState(false);

  const [selectedWorkspaceKey, setSelectedWorkspaceKey] = useState(null);
  const [activeTab, setActiveTab] = useState("general");
  // Pending initial tab to apply after auto-selecting a folder via initialWorkingDir.
  // The folder-population effect (keyed on selectedFolder) otherwise forces "general",
  // so we hand the desired tab off here and consume it there.
  const pendingInitialTabRef = useRef(null);

  // Tracks previously-selected workspace key so useWorkspaceEdits can flush
  // transient edit fields back into the workspaces array on selection change.
  // Owned by the shell (not useWorkspaceEdits) to break the circular dep
  // between useWorkspacesData (which reads it inside loadData) and
  // useWorkspaceEdits (which needs setWorkspaces from useWorkspacesData).
  const prevSelectedWorkspaceKeyRef = useRef(null);

  // prevSelectedWorkspaceKeyRef + workspace-level edit state (editAcpServer,
  // editAuxModel*, editInitialModel*, editRunner*, editAutoApprove, editIsDefault,
  // editAcpCommandOverride) + buildWorkspaceEditsFor/applyWorkspaceEdits helpers
  // + populate-and-flush effect are owned by useWorkspaceEdits (invoked below,
  // after selectedWorkspace + getWorkspaceKey are defined).

  // Key of a newly created workspace that doesn't have a valid working_dir yet
  const [newFolderKey, setNewFolderKey] = useState(null);

  // Per-folder expansion state in the tree, keyed by folder display name. Defaults to expanded.
  const [expandedFolders, setExpandedFolders] = useState({});

  // edit* workspace-level state owned by useWorkspaceEdits (see below).
  const [effectiveConfig, setEffectiveConfig] = useState(null);

  // mcpTools / mcpToolsLoading / mcpToolsError state owned by useWorkspaceMcpTools
  // mcp install/remove state + needsRestart/restarting owned by useWorkspaceMcpActions
  const scrollContainerRef = useRef(null);
  // hasLiveAcp state owned by useWorkspaceMcpTools

  // Track whether a folder group (not a workspace) is selected
  const [selectedFolder, setSelectedFolder] = useState(null);

  // Folder Metadata tab state + handlers moved to useFolderMetadataConfig hook
  // (invoked below, after groupedWorkspaces is defined). Owns the metadata
  // fields (description/url/group) and user-data-schema, the folder-selection
  // reload effect, and the two persist actions used by handleSave.

  // Folder Prompts tab state + handlers moved to useFolderPromptsConfig hook
  // (invoked below, after groupedWorkspaces is defined so its dep array is honored).

  // Folder Processors tab state + handlers moved to useFolderProcessorsConfig
  // hook (invoked below, after groupedWorkspaces is defined so its dep array
  // is honored).

  // Folder Shortcuts tab state + handlers moved to useFolderShortcutsConfig
  // hook (invoked below, alongside the other folder-tab hooks).

  // Confirmation dialog state: { message, title, confirmLabel, confirmVariant, onConfirm }
  const [confirmDialog, setConfirmDialog] = useState(null);

  // Horizontal resize handle for left/right panel split
  const [leftPanelWidth, setLeftPanelWidth] = useState(256);
  const isDraggingRef = useRef(false);
  const dragStartRef = useRef(null);
  const containerRef = useRef(null);

  useEffect(() => {
    const handleMouseMove = (e) => {
      if (!isDraggingRef.current || !dragStartRef.current) return;
      e.preventDefault();
      const containerRect = containerRef.current?.getBoundingClientRect();
      if (!containerRect) return;
      const newWidth = e.clientX - containerRect.left;
      const minLeft = 256; // Never smaller than original w-64
      const minRight = 400; // Enough space for form fields
      const maxLeft = containerRect.width - minRight;
      setLeftPanelWidth(Math.max(minLeft, Math.min(newWidth, maxLeft)));
    };
    const handleMouseUp = () => {
      if (!isDraggingRef.current) return;
      isDraggingRef.current = false;
      dragStartRef.current = null;
      document.body.style.userSelect = "";
      document.body.style.cursor = "";
    };
    document.addEventListener("mousemove", handleMouseMove);
    document.addEventListener("mouseup", handleMouseUp);
    return () => {
      document.removeEventListener("mousemove", handleMouseMove);
      document.removeEventListener("mouseup", handleMouseUp);
    };
  }, []);

  const handleResizeMouseDown = useCallback(
    (e) => {
      e.preventDefault();
      isDraggingRef.current = true;
      dragStartRef.current = { startX: e.clientX, startWidth: leftPanelWidth };
      document.body.style.userSelect = "none";
      document.body.style.cursor = "col-resize";
    },
    [leftPanelWidth],
  );

  const getWorkspaceKey = (ws) =>
    ws.uuid || `${ws.working_dir}|${ws.acp_server}`;

  // Config-loading cluster: owns raw data useStates (workspaces/acpServers/
  // modelProfiles/supportedRunners/orphanedWorkspaces) + loading/error flags
  // + the async loadData function. Called BEFORE any hook, memo, or effect
  // that reads workspaces/acpServers/modelProfiles/loadData.
  const {
    loading,
    error,
    setError,
    workspaces,
    setWorkspaces,
    acpServers,
    modelProfiles,
    supportedRunners,
    orphanedWorkspaces,
    loadData,
  } = useWorkspacesData({
    prevSelectedWorkspaceKeyRef,
    selectedWorkspaceKey,
    setSelectedWorkspaceKey,
    setSelectedFolder,
    getWorkspaceKey,
  });

  const sortedAcpServers = useMemo(
    () => [...acpServers].sort((a, b) => a.name.localeCompare(b.name)),
    [acpServers],
  );

  // Group workspaces by display name, sorted alphabetically, with ACP servers sorted within
  const groupedWorkspaces = useMemo(() => {
    const groups = new Map();
    workspaces.forEach((ws) => {
      const displayName =
        ws.name ||
        (ws.working_dir ? getBasename(ws.working_dir) : "New Workspace");
      if (!groups.has(displayName)) {
        groups.set(displayName, []);
      }
      groups.get(displayName).push(ws);
    });
    groups.forEach((arr) =>
      arr.sort((a, b) =>
        (a.acp_server || "").localeCompare(b.acp_server || ""),
      ),
    );
    return Array.from(groups.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([displayName, wsList]) => ({ displayName, workspaces: wsList }));
  }, [workspaces]);

  // Folder Beads tab state + handlers. Owns beads config/upstream state, the
  // three tab-scoped effects (load/reset), and all API mutators. Kept out of
  // the shell so the shell only forwards the grouped {beads, beadsSetters,
  // beadsHandlers} objects to WorkspaceFolderEditor.
  const { beads, beadsSetters, beadsHandlers } = useBeadsFolderConfig({
    selectedFolder,
    activeTab,
    isOpen,
    getSelectedFolderDir: () => {
      const folderGroup = groupedWorkspaces.find(
        (g) => g.displayName === selectedFolder,
      );
      return folderGroup?.workspaces[0]?.working_dir || null;
    },
  });

  // Folder Prompts tab state + handlers. Owns the 13 prompt state pairs, the
  // tab-open load effect, and the four CRUD handlers (reload/save/delete/toggle).
  // Shell forwards the grouped {prompts, promptsSetters, promptsHandlers} objects
  // to WorkspaceFolderEditor.
  const { prompts, promptsSetters, promptsHandlers } = useFolderPromptsConfig({
    selectedFolder,
    activeTab,
    groupedWorkspaces,
    getSelectedFolderDir: () => {
      const folderGroup = groupedWorkspaces.find(
        (g) => g.displayName === selectedFolder,
      );
      return folderGroup?.workspaces[0]?.working_dir || null;
    },
    setError,
  });

  // Folder Processors tab state + handlers. Owns the 4 processor state pairs,
  // the tab-open load effect, and the two CRUD handlers (toggle/save-arguments).
  // Shell forwards the grouped {processors, processorsSetters, processorsHandlers}
  // objects to WorkspaceFolderEditor.
  const { processors, processorsSetters, processorsHandlers } =
    useFolderProcessorsConfig({
      selectedFolder,
      activeTab,
      groupedWorkspaces,
      getSelectedFolderUuid: () => {
        const folderGroup = groupedWorkspaces.find(
          (g) => g.displayName === selectedFolder,
        );
        return folderGroup?.workspaces[0]?.uuid || null;
      },
      setError,
    });

  // Folder Shortcuts tab state + handlers. Owns the 6 shortcut state pairs,
  // the tab-open load effect and folder-switch reset effect, the four row
  // mutators, the persistShortcuts save action, and the memoized redundant-
  // prompt-names map. Shell forwards the grouped {shortcuts, shortcutsHandlers}
  // objects to WorkspaceFolderEditor; shortcutsLoaded / persistShortcuts are
  // consumed by handleSave.
  const { shortcuts, shortcutsHandlers, shortcutsLoaded, persistShortcuts } =
    useFolderShortcutsConfig({
      selectedFolder,
      activeTab,
      getSelectedFolderDir: () => {
        const folderGroup = groupedWorkspaces.find(
          (g) => g.displayName === selectedFolder,
        );
        return folderGroup?.workspaces[0]?.working_dir || null;
      },
      shortcutSections: SHORTCUT_SECTIONS,
    });

  // Folder Metadata tab state + handlers. Owns 6 metadata state pieces
  // (folderMetadata/metadataLoading + 4 edit fields), the folder-selection
  // reload effect, and the two persist actions (metadata + user-data-schema)
  // consumed by handleSave. Shell forwards the grouped
  // {metadata, metadataSetters} objects to WorkspaceFolderEditor.
  const { metadata, metadataSetters, persistMetadata, persistUserDataSchema } =
    useFolderMetadataConfig({
      selectedFolder,
      groupedWorkspaces,
    });

  // Folder General tab state + helpers. Owns the 5 header edit fields
  // (name/code/color/group/auto-children), the folder-selection populate
  // effect, the group suggestions memo, and applyFolderEdits used by
  // handleSave. Shell forwards grouped {edits, editSetters} to
  // WorkspaceFolderEditor.
  const { edits, editSetters, folderGroupSuggestions, applyFolderEdits } =
    useFolderGeneralEdits({ selectedFolder, groupedWorkspaces, workspaces });

  // Initialize folder expansion state from localStorage when the dialog opens
  // or when the set of folders changes.
  useEffect(() => {
    if (!isOpen) return;
    // With many folders the tree gets long, so default to collapsed; a folder
    // the user has explicitly toggled (stored in localStorage) keeps its state.
    const defaultExpanded =
      groupedWorkspaces.length <= WORKSPACES_EDITOR_COLLAPSE_THRESHOLD;
    const initial = {};
    groupedWorkspaces.forEach(({ displayName }) => {
      initial[displayName] = getEditorFolderExpansion(
        displayName,
        defaultExpanded,
      );
    });
    setExpandedFolders(initial);
  }, [isOpen, groupedWorkspaces]);

  const toggleFolder = useCallback((displayName) => {
    setExpandedFolders((prev) => {
      const next = !(prev[displayName] !== false);
      setEditorFolderExpansion(displayName, next);
      return { ...prev, [displayName]: next };
    });
  }, []);

  const expandAllFolders = useCallback(() => {
    const next = {};
    groupedWorkspaces.forEach(({ displayName }) => {
      next[displayName] = true;
      setEditorFolderExpansion(displayName, true);
    });
    setExpandedFolders(next);
  }, [groupedWorkspaces]);

  const collapseAllFolders = useCallback(() => {
    const next = {};
    groupedWorkspaces.forEach(({ displayName }) => {
      next[displayName] = false;
      setEditorFolderExpansion(displayName, false);
    });
    setExpandedFolders(next);
  }, [groupedWorkspaces]);

  const selectedWorkspace = useMemo(
    () =>
      workspaces.find((ws) => getWorkspaceKey(ws) === selectedWorkspaceKey) ||
      null,
    [workspaces, selectedWorkspaceKey],
  );

  // Workspace-level edit fields (transient scalar state) + populate/flush
  // effect + buildWorkspaceEditsFor/applyWorkspaceEdits helpers. Invoked here
  // so downstream useMemos and hooks (auxLegacyModelLabel,
  // useWorkspaceMcpTools, useWorkspacesSaveCoordinator) can consume the
  // returned edit* values and helpers. prevSelectedWorkspaceKeyRef is owned
  // by the shell and passed in (see top of component).
  const {
    editAcpServer,
    editAuxModelProfile,
    editAuxModelTag,
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
    applyWorkspaceEdits,
  } = useWorkspaceEdits({
    selectedWorkspace,
    selectedWorkspaceKey,
    setWorkspaces,
    getWorkspaceKey,
    prevSelectedWorkspaceKeyRef,
  });

  // Legacy raw matchMode/pattern constraint for the auxiliary model, if any,
  // and whether it's shown as a disabled "Custom (legacy)" dropdown option
  // (only when no profile is selected and it doesn't match a known profile).
  const rawAuxModelConstraint = useMemo(
    () => selectedWorkspace?.auxiliary_model_selection || null,
    [selectedWorkspace],
  );
  const auxLegacyModelLabel = useMemo(() => {
    if (editAuxModelProfile || !rawAuxModelConstraint) return null;
    const matches = modelProfiles.some(
      (p) =>
        p.criteria &&
        p.criteria.matchMode === rawAuxModelConstraint.matchMode &&
        p.criteria.pattern === rawAuxModelConstraint.pattern,
    );
    return matches
      ? null
      : `Custom (legacy): ${rawAuxModelConstraint.matchMode} ${rawAuxModelConstraint.pattern}`;
  }, [editAuxModelProfile, rawAuxModelConstraint, modelProfiles]);

  // MCP-tools + live-ACP data-loading cluster: owns mcpTools/mcpToolsLoading/
  // mcpToolsError/hasLiveAcp state, loadMcpTools + checkLiveAcpForWorkspace
  // callbacks, the mcp-tab load effect, and the workspace-change reset effect.
  const {
    mcpTools,
    mcpToolsLoading,
    mcpToolsError,
    setMcpToolsError,
    hasLiveAcp,
    loadMcpTools,
    checkLiveAcpForWorkspace,
  } = useWorkspaceMcpTools({
    activeTab,
    selectedWorkspace,
    selectedWorkspaceKey,
    selectedFolder,
    editAcpServer,
  });

  // MCP install/remove + ACP restart cluster: owns install form state (open,
  // json, name, scope, loading, error, success), remove state + scope ref,
  // ephemeral restart state (needsRestart/restarting), and 6 handler callbacks
  // (handleRestartAcp, handleRestartAcpClick, handleMcpInstall, handleMcpRemove,
  // handleInstallMittoMcp, handleMcpRemoveConfirm).
  const {
    mcpInstallOpen,
    setMcpInstallOpen,
    mcpInstallJson,
    setMcpInstallJson,
    mcpInstallName,
    setMcpInstallName,
    mcpInstallScope,
    setMcpInstallScope,
    mcpInstallLoading,
    mcpInstallError,
    setMcpInstallError,
    mcpInstallSuccess,
    setMcpInstallSuccess,
    mcpRemoveLoading,
    needsRestart,
    restarting,
    handleRestartAcpClick,
    handleMcpInstall,
    handleInstallMittoMcp,
    handleMcpRemoveConfirm,
  } = useWorkspaceMcpActions({
    selectedWorkspace,
    editAcpServer,
    mcpTools,
    loadMcpTools,
    checkLiveAcpForWorkspace,
    setMcpToolsError,
    setConfirmDialog,
    setError,
  });

  useEffect(() => {
    if (isOpen) {
      setError("");
      setNewFolderKey(null);
      loadData();
    }
  }, [isOpen]);

  // Auto-select the folder matching initialWorkingDir when dialog opens and data is loaded
  useEffect(() => {
    if (isOpen && initialWorkingDir && groupedWorkspaces.length > 0) {
      const matchingGroup = groupedWorkspaces.find((g) =>
        g.workspaces.some((ws) => ws.working_dir === initialWorkingDir),
      );
      if (matchingGroup) {
        // Hand the desired tab to the folder-population effect (keyed on selectedFolder),
        // which would otherwise force "general". Also set it directly for the case where
        // selectedFolder is unchanged (reopening on the same folder) and that effect won't run.
        pendingInitialTabRef.current = initialTab || null;
        setSelectedFolder(matchingGroup.displayName);
        setSelectedWorkspaceKey(null);
        setActiveTab(initialTab || "general");
      }
    }
  }, [isOpen, initialWorkingDir, initialTab, groupedWorkspaces]);

  // Scroll selected folder into view in the tree
  useEffect(() => {
    if (!isOpen || !selectedFolder) return;
    requestAnimationFrame(() => {
      const container = scrollContainerRef.current;
      if (!container) return;
      const el = container.querySelector(
        `[data-folder-name="${CSS.escape(selectedFolder)}"]`,
      );
      if (el) {
        el.scrollIntoView({ block: "nearest", behavior: "smooth" });
      }
    });
  }, [isOpen, selectedFolder, loading]);

  // When a workspace child is selected, reset effectiveConfig and (re)fetch it.
  // The workspace-level edit-field populate and previous-workspace flush are
  // owned by useWorkspaceEdits; the mcp state reset by useWorkspaceMcpTools.
  useEffect(() => {
    if (!selectedWorkspace) return;
    setEffectiveConfig(null);
    setActiveTab("general");
    if (selectedWorkspace.uuid) {
      authFetch(
        endpoints.workspaces.effectiveRunnerConfig(selectedWorkspace.uuid),
      )
        .then((r) => (r.ok ? r.json() : null))
        .then((data) => setEffectiveConfig(data))
        .catch(() => {});
    }
  }, [selectedWorkspaceKey]);

  // When a folder is selected, apply the pending initial tab (from
  // initialWorkingDir auto-select) or default to "general". Header-field
  // populate is owned by useFolderGeneralEdits; metadata reset + reload is
  // owned by useFolderMetadataConfig (both fire on the same [selectedFolder]
  // dep).
  useEffect(() => {
    if (!selectedFolder) return;
    const pendingTab = pendingInitialTabRef.current;
    pendingInitialTabRef.current = null;
    setActiveTab(pendingTab || "general");
  }, [selectedFolder]);

  // MCP tab load effect + mcp state reset moved to useWorkspaceMcpTools hook.

  // Folder Shortcuts tab: load effect + folder-switch reset effect moved to
  // useFolderShortcutsConfig hook (invoked above).

  // loadData moved to useWorkspacesData hook (called above).
  // loadMcpTools + checkLiveAcpForWorkspace moved to useWorkspaceMcpTools hook.
  // handleRestartAcp/handleRestartAcpClick/handleMcpInstall/handleMcpRemove/
  // handleInstallMittoMcp/handleMcpRemoveConfirm moved to useWorkspaceMcpActions.

  // buildWorkspaceEditsFor + applyWorkspaceEdits moved to useWorkspaceEdits hook.
  // Workspace mutation cluster (handleToggleIsDefault, getUnusedServer,
  // isNewFolderIncomplete/folderCanAddServer memos, guardNewFolder,
  // addWorkspace/removeWorkspace/duplicateWorkspace, addServerToFolder) lives
  // in useWorkspaceMutations. Must be called BEFORE useWorkspacesSaveCoordinator
  // (which consumes isNewFolderIncomplete) AND AFTER the memos it depends on
  // (sortedAcpServers/groupedWorkspaces/selectedWorkspace/useWorkspaceEdits).
  const {
    isNewFolderIncomplete,
    folderCanAddServer,
    handleToggleIsDefault,
    guardNewFolder,
    addWorkspace,
    removeWorkspace,
    duplicateWorkspace,
    addServerToFolder,
  } = useWorkspaceMutations({
    workspaces,
    setWorkspaces,
    acpServers,
    sortedAcpServers,
    groupedWorkspaces,
    selectedFolder,
    setSelectedFolder,
    selectedWorkspace,
    selectedWorkspaceKey,
    setSelectedWorkspaceKey,
    setEditIsDefault,
    newFolderKey,
    setNewFolderKey,
    setConfirmDialog,
    setError,
    getWorkspaceKey,
  });

  // handleSave orchestration lives in useWorkspacesSaveCoordinator: composes
  // folder/workspace edits, POSTs /config, then runs the metadata/schema/
  // shortcuts persist chain and fires onSave/showToast. Must be called AFTER
  // applyWorkspaceEdits/applyFolderEdits/isNewFolderIncomplete are defined.
  const { handleSave } = useWorkspacesSaveCoordinator({
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
    isNewFolderIncomplete,
    setWorkspaces,
    setNewFolderKey,
    setSaving,
    setError,
    onSave,
    showToast,
  });

  const handleRunnerChange = (r) => {
    setEditRunner(r);
    if (r === "exec") setEditRunnerConfig(null);
    else if (!editRunnerConfig)
      setEditRunnerConfig({
        restrictions: { allow_write_folders: ["$MITTO_WORKING_DIR"] },
      });
  };

  // Folder Shortcuts tab: load/reset effects, row mutators, persistShortcuts
  // and the redundant-prompt-names memo moved to useFolderShortcutsConfig
  // (called near the top of this component).

  // Folder Processors tab: load effect + reload/toggle/save handlers moved to
  // useFolderProcessorsConfig (called near the top of this component).

  if (!isOpen) return null;

  // Different tab sets for folder vs workspace
  const folderTabs = [
    { id: "general", label: "General", short: "General" },
    { id: "metadata", label: "Metadata", short: "Meta" },
    { id: "beads", label: "Tasks", short: "Tasks" },
    { id: "prompts", label: "Prompts", short: "Prompts" },
    { id: "processors", label: "Processors", short: "Proc" },
    { id: "shortcuts", label: "Shortcuts", short: "Cuts" },
    { id: "children", label: "Children", short: "Children" },
  ];

  const workspaceTabs = [
    { id: "general", label: "General" },
    { id: "runner", label: "Runner" },
    { id: "mcp", label: "MCP" },
  ];

  // Guarded close: warn if there's an incomplete new folder
  const handleClose = () => {
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
          onClose?.();
        },
      });
      return;
    }
    onClose?.();
  };

  return html`
    <${Modal}
      isOpen=${isOpen}
      onClose=${handleClose}
      testid="workspaces-dialog"
      boxClass="workspaces-dialog bg-mitto-sidebar w-[70vw] h-[70vh] max-w-[95vw] max-h-[95vh]"
      bodyClass="flex flex-col flex-1 min-h-0 overflow-hidden"
    >
      <!-- Header -->
      <div
        class="flex items-center justify-between p-4 border-b border-mitto-border shrink-0"
      >
        <h3 class="text-lg font-semibold flex items-center gap-2">
          <${FolderIcon} className="w-5 h-5 opacity-70" />
          Workspaces
        </h3>
        <button onClick=${handleClose} class="btn btn-ghost btn-square btn-sm">
          <${CloseIcon} className="w-4 h-4" />
        </button>
      </div>

      <!-- Body -->
      <div ref=${containerRef} class="flex flex-1 min-h-0 overflow-hidden">
        <!-- Left panel: workspace list -->
        <${WorkspacesLeftPanel}
          scrollContainerRef=${scrollContainerRef}
          loading=${loading}
          workspaces=${workspaces}
          groupedWorkspaces=${groupedWorkspaces}
          expandedFolders=${expandedFolders}
          selectedFolder=${selectedFolder}
          selectedWorkspaceKey=${selectedWorkspaceKey}
          acpServers=${acpServers}
          leftPanelWidth=${leftPanelWidth}
          isNewFolderIncomplete=${isNewFolderIncomplete}
          folderCanAddServer=${folderCanAddServer}
          setSelectedFolder=${setSelectedFolder}
          setSelectedWorkspaceKey=${setSelectedWorkspaceKey}
          guardNewFolder=${guardNewFolder}
          toggleFolder=${toggleFolder}
          getWorkspaceKey=${getWorkspaceKey}
          addWorkspace=${addWorkspace}
          removeWorkspace=${removeWorkspace}
          duplicateWorkspace=${duplicateWorkspace}
          addServerToFolder=${addServerToFolder}
          collapseAllFolders=${collapseAllFolders}
          expandAllFolders=${expandAllFolders}
        />

        <!-- Resize handle -->
        <div
          class="w-1 shrink-0 cursor-col-resize bg-mitto-border hover:bg-mitto-accent-500/50 transition-colors"
          onMouseDown=${handleResizeMouseDown}
        />

        <!-- Right panel: editor -->
        <div class="flex-1 flex flex-col min-w-0 overflow-hidden">
          ${selectedFolder && !selectedWorkspace
            ? html`<${WorkspaceFolderEditor}
                activeTab=${activeTab}
                setActiveTab=${setActiveTab}
                folderTabs=${folderTabs}
                selectedFolder=${selectedFolder}
                setSelectedFolder=${setSelectedFolder}
                groupedWorkspaces=${groupedWorkspaces}
                workspaces=${workspaces}
                setWorkspaces=${setWorkspaces}
                newFolderKey=${newFolderKey}
                getWorkspaceKey=${getWorkspaceKey}
                modelProfiles=${modelProfiles}
                edits=${edits}
                editSetters=${editSetters}
                folderGroupSuggestions=${folderGroupSuggestions}
                metadata=${metadata}
                metadataSetters=${metadataSetters}
                beads=${beads}
                beadsSetters=${beadsSetters}
                beadsHandlers=${beadsHandlers}
                prompts=${prompts}
                promptsSetters=${promptsSetters}
                promptsHandlers=${promptsHandlers}
                processors=${processors}
                processorsSetters=${processorsSetters}
                processorsHandlers=${processorsHandlers}
                shortcuts=${shortcuts}
                shortcutsHandlers=${shortcutsHandlers}
              />`
            : !selectedWorkspace
              ? html`<div
                  class="flex flex-col items-center justify-center h-full text-mitto-text-muted text-sm gap-3 px-8 text-center"
                >
                  ${workspaces.length === 0
                    ? html`
                        <${FolderIcon} className="w-10 h-10 opacity-30" />
                        <p class="text-base font-medium text-mitto-text-muted">
                          No workspaces configured
                        </p>
                        <p>
                          Add a workspace to specify a folder where an ACP
                          server will operate.
                        </p>
                        <p class="text-xs">
                          Click the
                          <span
                            class="inline-flex items-center gap-1 text-mitto-text-muted"
                            ><${FolderIcon} className="w-3.5 h-3.5" />
                            folder</span
                          >
                          button below to get started.
                        </p>
                      `
                    : html`<p>Select a workspace to edit</p>`}
                </div>`
              : html`<${WorkspaceEditor}
                  activeTab=${activeTab}
                  setActiveTab=${setActiveTab}
                  workspaceTabs=${workspaceTabs}
                  selectedWorkspace=${selectedWorkspace}
                  getWorkspaceKey=${getWorkspaceKey}
                  sortedAcpServers=${sortedAcpServers}
                  acpServers=${acpServers}
                  supportedRunners=${supportedRunners}
                  modelProfiles=${modelProfiles}
                  effectiveConfig=${effectiveConfig}
                  editAcpServer=${editAcpServer}
                  setEditAcpServer=${setEditAcpServer}
                  editAcpCommandOverride=${editAcpCommandOverride}
                  setEditAcpCommandOverride=${setEditAcpCommandOverride}
                  editInitialModelProfile=${editInitialModelProfile}
                  setEditInitialModelProfile=${setEditInitialModelProfile}
                  editInitialModelTag=${editInitialModelTag}
                  setEditInitialModelTag=${setEditInitialModelTag}
                  editAuxModelProfile=${editAuxModelProfile}
                  setEditAuxModelProfile=${setEditAuxModelProfile}
                  editAuxModelTag=${editAuxModelTag}
                  setEditAuxModelTag=${setEditAuxModelTag}
                  setEditAuxModelConstraintCleared=${setEditAuxModelConstraintCleared}
                  auxLegacyModelLabel=${auxLegacyModelLabel}
                  rawAuxModelConstraint=${rawAuxModelConstraint}
                  editAutoApprove=${editAutoApprove}
                  setEditAutoApprove=${setEditAutoApprove}
                  editIsDefault=${editIsDefault}
                  handleToggleIsDefault=${handleToggleIsDefault}
                  editRunner=${editRunner}
                  handleRunnerChange=${handleRunnerChange}
                  editRunnerConfig=${editRunnerConfig}
                  setEditRunnerConfig=${setEditRunnerConfig}
                  mcpTools=${mcpTools}
                  mcpToolsLoading=${mcpToolsLoading}
                  mcpToolsError=${mcpToolsError}
                  loadMcpTools=${loadMcpTools}
                  mcpInstallOpen=${mcpInstallOpen}
                  setMcpInstallOpen=${setMcpInstallOpen}
                  setMcpInstallJson=${setMcpInstallJson}
                  setMcpInstallName=${setMcpInstallName}
                  setMcpInstallScope=${setMcpInstallScope}
                  mcpInstallLoading=${mcpInstallLoading}
                  mcpInstallError=${mcpInstallError}
                  setMcpInstallError=${setMcpInstallError}
                  mcpInstallSuccess=${mcpInstallSuccess}
                  setMcpInstallSuccess=${setMcpInstallSuccess}
                  handleInstallMittoMcp=${handleInstallMittoMcp}
                  handleMcpRemoveConfirm=${handleMcpRemoveConfirm}
                  mcpRemoveLoading=${mcpRemoveLoading}
                  showToast=${showToast}
                />`}
        </div>
      </div>

      <!-- Footer -->
      <div
        class="flex items-center justify-between p-4 border-t border-mitto-border shrink-0"
      >
        <div class="flex-1 mr-4">
          ${orphanedWorkspaces.length > 0 &&
          html`
            <p class="text-xs text-mitto-warning">
              ⚠ ${orphanedWorkspaces.length} workspace(s) hidden: missing ACP
              server
            </p>
          `}
          ${error && html`<p class="text-xs text-mitto-danger">${error}</p>`}
        </div>
        <div class="flex gap-2">
          ${activeTab === "mcp" &&
          selectedWorkspace?.uuid &&
          hasLiveAcp &&
          html`
            <button
              onClick=${handleRestartAcpClick}
              disabled=${restarting}
              class="btn btn-sm gap-2 tooltip tooltip-bottom ${needsRestart
                ? "btn-warning"
                : "btn-outline btn-warning"}"
              data-tip=${needsRestart
                ? "Restart ACP to apply MCP changes to active conversations"
                : "Restart the ACP server for this workspace"}
              data-testid="ws-restart-acp"
            >
              ${restarting
                ? html`<${SpinnerIcon} className="w-4 h-4" /> Restarting...`
                : "Restart ACP"}
            </button>
          `}
          <button
            onClick=${handleClose}
            data-testid="ws-close"
            class="btn btn-ghost btn-sm"
          >
            Close
          </button>
          <button
            onClick=${handleSave}
            data-testid="ws-save"
            disabled=${saving || loading}
            class="btn btn-primary btn-sm gap-2"
          >
            ${saving
              ? html`<${SpinnerIcon} className="w-4 h-4" /> Saving...`
              : "Save"}
          </button>
        </div>
      </div>
    <//>

    <${ConfirmDialog}
      isOpen=${!!confirmDialog}
      title=${confirmDialog?.title || "Confirm"}
      message=${confirmDialog?.message || ""}
      confirmLabel=${confirmDialog?.confirmLabel || "Yes"}
      cancelLabel=${confirmDialog?.cancelLabel || "Cancel"}
      confirmVariant=${confirmDialog?.confirmVariant || "primary"}
      onConfirm=${confirmDialog?.onConfirm}
      onCancel=${() => setConfirmDialog(null)}
    >
      ${confirmDialog?.children}
    <//>

    <!-- MCP Install Dialog -->
    <${McpInstallDialog}
      mcpInstallOpen=${mcpInstallOpen}
      mcpInstallJson=${mcpInstallJson}
      setMcpInstallJson=${setMcpInstallJson}
      mcpInstallName=${mcpInstallName}
      setMcpInstallName=${setMcpInstallName}
      mcpInstallScope=${mcpInstallScope}
      setMcpInstallScope=${setMcpInstallScope}
      mcpInstallLoading=${mcpInstallLoading}
      mcpInstallError=${mcpInstallError}
      setMcpInstallError=${setMcpInstallError}
      mcpInstallSuccess=${mcpInstallSuccess}
      setMcpInstallSuccess=${setMcpInstallSuccess}
      mcpTools=${mcpTools}
      handleMcpInstall=${handleMcpInstall}
      setMcpInstallOpen=${setMcpInstallOpen}
    />
  `;
}
