// Mitto Web Interface - Workspaces Dialog Component
const { useState, useEffect, useMemo, useCallback, useRef, html } =
  window.preact;

import {
  secureFetch,
  authFetch,
  apiUrl,
  endpoints,
  errorMessageFromData,
  fetchConfig,
  invalidateConfigCache,
  openExternalURL,
} from "../utils/index.js";

import { getWorkspaceVisualInfo, getBasename } from "../lib.js";

import { useBeadsFolderConfig } from "../hooks/useBeadsFolderConfig.js";
import { useFolderPromptsConfig } from "../hooks/useFolderPromptsConfig.js";

import {
  SpinnerIcon,
  CloseIcon,
  FolderIcon,
  TrashIcon,
  DuplicateIcon,
  ChevronRightIcon,
  ChevronDownIcon,
  ExpandIcon,
  CollapseIcon,
  ServerIcon,
} from "./Icons.js";

import { ConfirmDialog } from "./ConfirmDialog.js";
import { Modal } from "./Modal.js";
import { WorkspaceBadge } from "./WorkspaceBadge.js";

import { WorkspaceEditor } from "./WorkspaceEditor.js";
import { WorkspaceFolderEditor } from "./WorkspaceFolderEditor.js";
import { promptMenuIncludes } from "../utils/prompts.js";

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
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const [workspaces, setWorkspaces] = useState([]);
  const [acpServers, setAcpServers] = useState([]);
  // Named Model profiles (config.models), used for the auxiliary model dropdown
  const [modelProfiles, setModelProfiles] = useState([]);
  const [supportedRunners, setSupportedRunners] = useState([]);
  const [orphanedWorkspaces, setOrphanedWorkspaces] = useState([]);

  const [selectedWorkspaceKey, setSelectedWorkspaceKey] = useState(null);
  const [activeTab, setActiveTab] = useState("general");
  // Pending initial tab to apply after auto-selecting a folder via initialWorkingDir.
  // The folder-population effect (keyed on selectedFolder) otherwise forces "general",
  // so we hand the desired tab off here and consume it there.
  const pendingInitialTabRef = useRef(null);

  // Tracks the workspace whose transient edit fields are currently loaded, so we
  // can flush those edits back into the `workspaces` array before switching to a
  // different workspace. Without this, edits to multiple workspaces in a single
  // dialog session would be lost (only the last-selected workspace would save).
  const prevSelectedWorkspaceKeyRef = useRef(null);

  // Key of a newly created workspace that doesn't have a valid working_dir yet
  const [newFolderKey, setNewFolderKey] = useState(null);

  // Per-folder expansion state in the tree, keyed by folder display name. Defaults to expanded.
  const [expandedFolders, setExpandedFolders] = useState({});

  const [editName, setEditName] = useState("");
  const [editCode, setEditCode] = useState("");
  const [editColor, setEditColor] = useState("");
  const [editGroup, setEditGroup] = useState("");
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
  const [editAutoChildren, setEditAutoChildren] = useState([]);
  const [effectiveConfig, setEffectiveConfig] = useState(null);

  const [mcpTools, setMcpTools] = useState(null);
  const [mcpToolsLoading, setMcpToolsLoading] = useState(false);
  const [mcpToolsError, setMcpToolsError] = useState("");

  const [mcpInstallOpen, setMcpInstallOpen] = useState(false);
  const [mcpInstallJson, setMcpInstallJson] = useState("");
  const [mcpInstallName, setMcpInstallName] = useState("");
  const [mcpInstallScope, setMcpInstallScope] = useState("");
  const [mcpInstallLoading, setMcpInstallLoading] = useState(false);
  const [mcpInstallError, setMcpInstallError] = useState("");
  const [mcpInstallSuccess, setMcpInstallSuccess] = useState("");

  const [mcpRemoveLoading, setMcpRemoveLoading] = useState(false);
  const mcpRemoveScopeRef = useRef("");
  const scrollContainerRef = useRef(null);

  // Ephemeral restart state — resets when dialog closes (component state)
  const [needsRestart, setNeedsRestart] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [hasLiveAcp, setHasLiveAcp] = useState(false);

  // Track whether a folder group (not a workspace) is selected
  const [selectedFolder, setSelectedFolder] = useState(null);

  // Workspace metadata loaded from .mittorc (description, url)
  const [folderMetadata, setFolderMetadata] = useState(null);
  const [metadataLoading, setMetadataLoading] = useState(false);
  const [editMetaDescription, setEditMetaDescription] = useState("");
  const [editMetaUrl, setEditMetaUrl] = useState("");
  const [editMetaGroup, setEditMetaGroup] = useState("");
  const [editUserDataFields, setEditUserDataFields] = useState([]);

  // Folder Prompts tab state + handlers moved to useFolderPromptsConfig hook
  // (invoked below, after groupedWorkspaces is defined so its dep array is honored).

  // Folder processors state (for the Processors tab)
  const [folderProcessors, setFolderProcessors] = useState([]);
  const [processorsLoading, setProcessorsLoading] = useState(false);
  const [expandedProcessor, setExpandedProcessor] = useState(null);
  // Local argument edit state: { [procName]: { [paramName]: value } }
  // Seeded lazily on first edit; cleared after a successful Save.
  const [processorArgEdits, setProcessorArgEdits] = useState({});

  // Folder shortcuts state (for the Shortcuts tab)
  const [shortcutsSections, setShortcutsSections] = useState({});
  const [shortcutsLoading, setShortcutsLoading] = useState(false);
  const [shortcutsLoaded, setShortcutsLoaded] = useState(false);
  const [shortcutsError, setShortcutsError] = useState("");
  // Per-section prompt lists, filtered by the section's prompt menu tag and
  // sorted by name. Section ids match those persisted on the server side.
  const [sectionPrompts, setSectionPrompts] = useState({
    tasksList: [],
    conversations: [],
    beadsIssue: [],
  });
  // Global shortcut sections (from settings.json). Used only to derive which
  // prompts are already configured globally so they can be excluded from the
  // folder-level dropdowns and any duplicate folder rows greyed out.
  const [globalShortcutsSections, setGlobalShortcutsSections] = useState({});

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

  const sortedAcpServers = useMemo(
    () => [...acpServers].sort((a, b) => a.name.localeCompare(b.name)),
    [acpServers],
  );

  const getWorkspaceKey = (ws) =>
    ws.uuid || `${ws.working_dir}|${ws.acp_server}`;

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

  // When a workspace child is selected, populate workspace-level edit fields
  useEffect(() => {
    // Flush the previously-selected workspace's transient edits into the
    // workspaces array before repopulating the fields for the new selection.
    // The scalar edit state (editAcpServer, editAuxModelProfile, etc.) still
    // holds the previous workspace's values at this point, so applying them
    // against prevKey commits those edits. This also runs when navigating to a
    // folder (selectedWorkspaceKey becomes null) so edits are not lost there.
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
    setEffectiveConfig(null);
    setMcpTools(null);
    setMcpToolsError("");
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

  // When a folder is selected, populate folder-level edit fields from the first workspace in the group
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
    // Apply a pending initial tab (from initialWorkingDir auto-select), else default to general.
    const pendingTab = pendingInitialTabRef.current;
    pendingInitialTabRef.current = null;
    setActiveTab(pendingTab || "general");

    // Load workspace metadata from .mittorc
    setFolderMetadata(null);
    setEditMetaDescription("");
    setEditMetaUrl("");
    setEditMetaGroup("");
    setEditUserDataFields([]);
    if (firstWs.uuid) {
      setMetadataLoading(true);
      authFetch(endpoints.workspaces.metadata(firstWs.uuid))
        .then((r) => r.json())
        .then((data) => {
          setFolderMetadata(data || null);
          setEditMetaDescription(data?.description || "");
          setEditMetaUrl(data?.url || "");
          setEditMetaGroup(data?.group || "");
          setEditUserDataFields(
            (data?.user_data_schema?.fields || []).map((f) => ({
              name: f.name || "",
              type: f.type || "string",
              description: f.description || "",
            })),
          );
        })
        .catch(() => {
          setFolderMetadata(null);
          setEditMetaDescription("");
          setEditMetaUrl("");
          setEditMetaGroup("");
          setEditUserDataFields([]);
        })
        .finally(() => {
          setMetadataLoading(false);
        });
    }
  }, [selectedFolder]);

  useEffect(() => {
    if (activeTab === "mcp" && selectedWorkspace && !selectedFolder) {
      loadMcpTools(
        editAcpServer || selectedWorkspace.acp_server,
        selectedWorkspace.uuid,
      );
      checkLiveAcpForWorkspace(selectedWorkspace.uuid).then(setHasLiveAcp);
    } else {
      setHasLiveAcp(false);
    }
    // checkLiveAcpForWorkspace/loadMcpTools are stable useCallbacks defined
    // later in the component; referencing them here would trigger a TDZ
    // ReferenceError since the deps array is evaluated during render.
  }, [activeTab, selectedWorkspaceKey, editAcpServer]);

  // Lazily load shortcuts when the Shortcuts folder tab is opened.
  useEffect(() => {
    if (activeTab !== "shortcuts" || !selectedFolder) return;
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    setShortcutsLoading(true);
    setShortcutsError("");
    Promise.all([
      authFetch(endpoints.folders.shortcuts({ working_dir: workingDir }))
        .then((r) => r.json())
        .then((data) => setShortcutsSections(data.sections || {})),
      // Global shortcuts: prompts already configured here are excluded from the
      // folder dropdowns (and any duplicate folder rows are greyed out).
      authFetch(endpoints.global.shortcuts())
        .then((r) => r.json())
        .then((data) => setGlobalShortcutsSections(data.sections || {}))
        .catch(() => setGlobalShortcutsSections({})),
      authFetch(
        endpoints.workspacePrompts.list({
          working_dir: workingDir,
          include_global: true,
        }),
      )
        .then((r) => r.json())
        .then((data) => {
          const all = data.prompts || [];
          const byMenu = (menu) =>
            all
              .filter((p) => promptMenuIncludes(p, menu))
              .sort((a, b) => a.name.localeCompare(b.name));
          setSectionPrompts({
            tasksList: byMenu("beadsList"),
            conversations: byMenu("prompts"),
            beadsIssue: byMenu("beadsIssues"),
          });
        }),
    ])
      .then(() => setShortcutsLoaded(true))
      .catch((err) =>
        setShortcutsError("Failed to load shortcuts: " + err.message),
      )
      .finally(() => setShortcutsLoading(false));
  }, [activeTab, selectedFolder]);

  // Reset shortcuts state when switching folders.
  useEffect(() => {
    setShortcutsSections({});
    setSectionPrompts({ tasksList: [], conversations: [], beadsIssue: [] });
    setShortcutsError("");
    setShortcutsLoaded(false);
  }, [selectedFolder]);

  const loadData = async () => {
    // Reset the flush tracker so stale edit-field values from a previous dialog
    // session are not flushed onto a workspace after a reload/reopen.
    prevSelectedWorkspaceKeyRef.current = null;
    setLoading(true);
    try {
      const [config, runnersRes] = await Promise.all([
        fetchConfig(null, true),
        fetch(endpoints.runners.supported(), { credentials: "same-origin" }),
      ]);
      const servers = config.acp_servers || [];
      setAcpServers(servers);
      setModelProfiles(Array.isArray(config.models) ? config.models : []);
      const serverNames = new Set(servers.map((s) => s.name));
      const rawWorkspaces = config.workspaces || [];
      const orphaned = [];
      const valid = rawWorkspaces.filter((ws) => {
        if (!ws.working_dir || ws.working_dir.trim() === "") return false;
        if (!ws.acp_server || !serverNames.has(ws.acp_server)) {
          if (ws.acp_server)
            orphaned.push({
              working_dir: ws.working_dir,
              missing_server: ws.acp_server,
            });
          return false;
        }
        return true;
      });
      setWorkspaces(valid);
      setOrphanedWorkspaces(orphaned);
      setSelectedFolder(null);
      if (valid.length > 0) {
        // Preserve the previously-selected workspace across a reload/reopen when it
        // still exists. Otherwise the selection resets to valid[0], whose order is
        // not stable (it reflects the backend's map-iteration order, not the sorted
        // tree). That made a just-saved edit appear "lost": the dialog reopened on a
        // different workspace that legitimately still showed its own value. When no
        // prior selection matches, fall back to a deterministic first entry (sorted
        // by display name, then ACP server) so the initial selection is predictable.
        const prevKey = selectedWorkspaceKey;
        const preserved =
          prevKey && valid.some((ws) => getWorkspaceKey(ws) === prevKey);
        if (preserved) {
          setSelectedWorkspaceKey(prevKey);
        } else {
          const firstByName = [...valid].sort((a, b) => {
            const an = a.name || getBasename(a.working_dir) || "";
            const bn = b.name || getBasename(b.working_dir) || "";
            return (
              an.localeCompare(bn) ||
              (a.acp_server || "").localeCompare(b.acp_server || "")
            );
          })[0];
          setSelectedWorkspaceKey(getWorkspaceKey(firstByName));
        }
      } else {
        setSelectedWorkspaceKey(null);
      }
      if (runnersRes.ok) {
        setSupportedRunners((await runnersRes.json()) || []);
      } else {
        setSupportedRunners([
          { type: "exec", label: "exec (no restrictions)", supported: true },
          {
            type: "sandbox-exec",
            label: "sandbox-exec (macOS)",
            supported: false,
          },
          { type: "firejail", label: "firejail (Linux)", supported: false },
          { type: "docker", label: "docker (all platforms)", supported: true },
        ]);
      }
    } catch (err) {
      setError("Failed to load configuration: " + err.message);
    } finally {
      setLoading(false);
    }
  };

  // Apply folder-level edits (name, code, color, children) to all workspaces in the same folder
  const applyFolderEdits = (ws, folderWorkingDir) => {
    if (ws.working_dir !== folderWorkingDir) return ws;
    return {
      ...ws,
      name: editName || undefined,
      code: (editCode || "").toUpperCase().slice(0, 3) || undefined,
      color: editColor || undefined,
      group: editGroup.trim() || undefined,
      auto_children: editAutoChildren.length > 0 ? editAutoChildren : undefined,
    };
  };

  const loadMcpTools = useCallback(async (acpServer, uuid) => {
    setMcpToolsLoading(true);
    setMcpToolsError("");
    setMcpTools(null);
    if (!uuid) {
      setMcpToolsError("No workspace selected");
      setMcpTools({ servers: [], agent_name: "" });
      setMcpToolsLoading(false);
      return;
    }
    try {
      const res = await authFetch(
        endpoints.workspaces.mcpTools(uuid, { acp_server: acpServer }),
      );
      if (!res.ok) {
        const ct = res.headers.get("content-type");
        if (ct && ct.includes("application/json")) {
          const ed = await res.json();
          throw new Error(errorMessageFromData(ed, "request failed"));
        }
        throw new Error(await res.text());
      }
      const data = await res.json();
      if (data.error) {
        setMcpToolsError(data.error);
      }
      setMcpTools(data);
    } catch (err) {
      setMcpToolsError("Failed to load MCP tools: " + err.message);
      setMcpTools({ servers: [], agent_name: "" });
    } finally {
      setMcpToolsLoading(false);
    }
  }, []);

  // Check if the given workspace UUID has a live shared ACP process. The Restart
  // ACP button must be offered whenever this is true (even with 0 conversations),
  // because the live process loaded the old MCP config at startup.
  const checkLiveAcpForWorkspace = useCallback(async (workspaceUUID) => {
    if (!workspaceUUID) return false;
    try {
      const res = await authFetch(
        endpoints.workspaces.acpStatus(workspaceUUID),
      );
      if (!res.ok) return false;
      const data = await res.json();
      return !!data.alive;
    } catch {
      return false;
    }
  }, []);

  // Restart the ACP process for the selected workspace so MCP changes take effect.
  const handleRestartAcp = useCallback(async () => {
    if (!selectedWorkspace?.uuid) return;
    setRestarting(true);
    try {
      const res = await secureFetch(
        endpoints.workspaces.restartAcp(selectedWorkspace.uuid),
        {
          method: "POST",
        },
      );
      if (!res.ok) {
        let msg = "Failed to restart ACP";
        try {
          const data = await res.json();
          msg = errorMessageFromData(data, msg);
        } catch (_) {
          /* keep default */
        }
        throw new Error(msg);
      }
      setNeedsRestart(false);
    } catch (err) {
      setError("Failed to restart ACP: " + err.message);
    } finally {
      setRestarting(false);
    }
  }, [selectedWorkspace]);

  // Restart ACP with a warning if any conversation in this workspace is currently
  // prompting (its response would be interrupted by the restart).
  const handleRestartAcpClick = useCallback(async () => {
    const uuid = selectedWorkspace?.uuid;
    if (!uuid) return;
    let affected = 0;
    try {
      const res = await authFetch(endpoints.sessions.running());
      if (res.ok) {
        const data = await res.json();
        const list = Array.isArray(data?.sessions) ? data.sessions : [];
        affected = list.filter(
          (s) => s.workspace_uuid === uuid && s.is_prompting,
        ).length;
      }
    } catch {
      // Best-effort detection: on error, fall through to a direct restart.
    }
    if (affected > 0) {
      const plural = affected === 1 ? "" : "s";
      const verb = affected === 1 ? "is" : "are";
      setConfirmDialog({
        title: "Restart ACP?",
        message:
          `There ${verb} ${affected} conversation${plural} with an agent actively ` +
          `responding in this workspace. Restarting the ACP server now will ` +
          `interrupt the response${plural} and may lose unsaved work.`,
        confirmLabel: "Restart",
        confirmVariant: "danger",
        onConfirm: () => {
          setConfirmDialog(null);
          handleRestartAcp();
        },
      });
      return;
    }
    handleRestartAcp();
  }, [selectedWorkspace, handleRestartAcp]);

  const handleMcpInstall = useCallback(async () => {
    // Client-side JSON validation
    let parsed;
    try {
      parsed = JSON.parse(mcpInstallJson);
    } catch (e) {
      setMcpInstallError("Invalid JSON: " + e.message);
      return;
    }

    // Normalize to { mcpServers: { ... } } — detect format automatically
    if (
      parsed.mcpServers &&
      typeof parsed.mcpServers === "object" &&
      Object.keys(parsed.mcpServers).length > 0
    ) {
      // Format 1: already has mcpServers wrapper — use as-is
    } else if (
      typeof parsed.command === "string" ||
      typeof parsed.url === "string"
    ) {
      // Format 3: single server definition without a name
      if (!mcpInstallName.trim()) {
        setMcpInstallError(
          "Please enter a server name for the single server definition.",
        );
        return;
      }
      parsed = { mcpServers: { [mcpInstallName.trim()]: parsed } };
    } else {
      // Format 2: bare map of named servers — check all values look like server entries
      const vals = Object.values(parsed);
      if (
        vals.length > 0 &&
        vals.every(
          (v) =>
            v &&
            typeof v === "object" &&
            (typeof v.command === "string" || typeof v.url === "string"),
        )
      ) {
        parsed = { mcpServers: parsed };
      } else {
        setMcpInstallError(
          'Unrecognized JSON format. Paste a "mcpServers" object, a map of named servers, or a single server definition with "command" or "url".',
        );
        return;
      }
    }

    if (!selectedWorkspace?.uuid) {
      setMcpInstallError("No workspace selected");
      return;
    }
    setMcpInstallLoading(true);
    setMcpInstallError("");
    setMcpInstallSuccess("");

    try {
      const acpServer = editAcpServer || selectedWorkspace?.acp_server;
      const res = await secureFetch(
        endpoints.workspaces.mcpToolsInstall(selectedWorkspace.uuid),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            acp_server: acpServer,
            scope: mcpInstallScope,
            definition: parsed,
          }),
        },
      );

      if (!res.ok) {
        const ct = res.headers.get("content-type");
        if (ct && ct.includes("application/json")) {
          const ed = await res.json();
          throw new Error(errorMessageFromData(ed, "request failed"));
        }
        throw new Error(await res.text());
      }

      const data = await res.json();
      const results = data.results || [];
      const failed = results.filter((r) => !r.success);

      if (failed.length > 0) {
        setMcpInstallError(
          failed.map((r) => `${r.name}: ${r.message}`).join("\n"),
        );
      } else {
        const names = results.map((r) => r.name).join(", ");
        setMcpInstallSuccess(`Successfully installed: ${names}`);
        // Check if a live ACP process needs restarting to pick up the new MCP server
        if (selectedWorkspace?.uuid) {
          checkLiveAcpForWorkspace(selectedWorkspace.uuid).then((hasActive) => {
            if (hasActive) setNeedsRestart(true);
          });
        }
        // Reload MCP tools list after successful install
        setTimeout(() => {
          loadMcpTools(acpServer, selectedWorkspace?.uuid);
          setMcpInstallOpen(false);
          setMcpInstallJson("");
          setMcpInstallName("");
          setMcpInstallSuccess("");
          setMcpInstallError("");
        }, 1500);
      }
    } catch (err) {
      setMcpInstallError("Installation failed: " + err.message);
    } finally {
      setMcpInstallLoading(false);
    }
  }, [
    mcpInstallJson,
    mcpInstallName,
    mcpInstallScope,
    editAcpServer,
    selectedWorkspace,
    loadMcpTools,
    checkLiveAcpForWorkspace,
  ]);

  const handleMcpRemove = useCallback(
    async (serverName, scope) => {
      setMcpRemoveLoading(true);
      try {
        const acpServer = editAcpServer || selectedWorkspace?.acp_server;
        const res = await secureFetch(
          endpoints.workspaces.mcpToolsRemove(selectedWorkspace.uuid),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              acp_server: acpServer,
              scope: scope || mcpTools?.mcp_scopes?.[0] || "",
              name: serverName,
            }),
          },
        );
        if (!res.ok) {
          const ct = res.headers.get("content-type");
          if (ct && ct.includes("application/json")) {
            const ed = await res.json();
            throw new Error(errorMessageFromData(ed, "request failed"));
          }
          throw new Error(await res.text());
        }
        const data = await res.json();
        if (!data.success) {
          setMcpToolsError(data.message || "Failed to remove MCP server");
        } else {
          // Check if a live ACP process needs restarting to drop the removed MCP server
          if (selectedWorkspace?.uuid) {
            const hasActive = await checkLiveAcpForWorkspace(
              selectedWorkspace.uuid,
            );
            if (hasActive) setNeedsRestart(true);
          }
        }
        // Refresh the MCP tools list
        await loadMcpTools(acpServer, selectedWorkspace?.uuid);
      } catch (err) {
        setMcpToolsError("Failed to remove MCP server: " + err.message);
      } finally {
        setMcpRemoveLoading(false);
      }
    },
    [
      editAcpServer,
      selectedWorkspace,
      mcpTools,
      loadMcpTools,
      checkLiveAcpForWorkspace,
    ],
  );

  // One-click install of Mitto's own MCP server. Reuses the manual install
  // endpoint/handling but skips the JSON dialog, building the definition from the
  // live MCP URL reported by the backend (falling back to the default port).
  const handleInstallMittoMcp = useCallback(async () => {
    const mcpUrl = mcpTools?.mcp_url || "http://127.0.0.1:5757/mcp";
    const scope = mcpTools?.mcp_scopes?.[0] || "";
    setMcpInstallLoading(true);
    setMcpInstallError("");
    setMcpInstallSuccess("");
    if (!selectedWorkspace?.uuid) {
      setMcpInstallError("No workspace selected");
      return;
    }
    try {
      const acpServer = editAcpServer || selectedWorkspace?.acp_server;
      const res = await secureFetch(
        endpoints.workspaces.mcpToolsInstall(selectedWorkspace.uuid),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            acp_server: acpServer,
            scope,
            definition: { mcpServers: { mitto: { url: mcpUrl } } },
          }),
        },
      );
      if (!res.ok) {
        const ct = res.headers.get("content-type");
        if (ct && ct.includes("application/json")) {
          const ed = await res.json();
          throw new Error(errorMessageFromData(ed, "request failed"));
        }
        throw new Error(await res.text());
      }
      const data = await res.json();
      const results = data.results || [];
      const failed = results.filter((r) => !r.success);
      if (failed.length > 0) {
        setMcpInstallError(
          failed.map((r) => `${r.name}: ${r.message}`).join("\n"),
        );
      } else {
        setMcpInstallSuccess("Installed Mitto MCP server.");
        if (selectedWorkspace?.uuid) {
          checkLiveAcpForWorkspace(selectedWorkspace.uuid).then((hasActive) => {
            if (hasActive) setNeedsRestart(true);
          });
        }
        await loadMcpTools(acpServer, selectedWorkspace?.uuid);
      }
    } catch (err) {
      setMcpInstallError("Installation failed: " + err.message);
    } finally {
      setMcpInstallLoading(false);
    }
  }, [
    mcpTools,
    editAcpServer,
    selectedWorkspace,
    loadMcpTools,
    checkLiveAcpForWorkspace,
  ]);

  const handleMcpRemoveConfirm = useCallback(
    (serverName) => {
      const defaultScope = mcpTools?.mcp_scopes?.[0] || "";
      mcpRemoveScopeRef.current = defaultScope;
      setConfirmDialog({
        title: "Remove MCP Server",
        message: `Remove MCP server "${serverName}"?`,
        confirmLabel: "Remove",
        confirmVariant: "danger",
        children:
          mcpTools?.mcp_scopes?.length > 0
            ? html`
                <div class="mt-3">
                  <label class="block text-sm text-mitto-text-muted mb-1"
                    >Scope</label
                  >
                  <select
                    value=${defaultScope}
                    onInput=${(e) => {
                      mcpRemoveScopeRef.current = e.target.value;
                    }}
                    class="select select-sm w-full"
                  >
                    ${mcpTools.mcp_scopes.map(
                      (scope) => html`
                        <option key=${scope} value=${scope}>${scope}</option>
                      `,
                    )}
                  </select>
                </div>
              `
            : null,
        onConfirm: async () => {
          setConfirmDialog(null);
          await handleMcpRemove(
            serverName,
            mcpRemoveScopeRef.current || defaultScope,
          );
        },
      });
    },
    [mcpTools, handleMcpRemove],
  );

  // Toggle the "default workspace for this folder" flag. Enforce a single default
  // per folder live: when enabling it, immediately clear is_default on every other
  // workspace that shares this folder so the UI reflects the change before saving.
  const handleToggleIsDefault = (checked) => {
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
  };

  // Build a workspace object with the current transient edit fields applied,
  // but only for the workspace matching targetKey; all others pass through
  // unchanged. This is used both to flush edits on selection change and to
  // commit the currently-selected workspace at save time.
  const buildWorkspaceEditsFor = (ws, targetKey) => {
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
  };

  // Apply workspace-level edits (acp_server, runner, auto_approve) to the selected workspace
  const applyWorkspaceEdits = (ws) =>
    buildWorkspaceEditsFor(ws, selectedWorkspaceKey);

  const handleSave = async () => {
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
      const result = await res.json();
      invalidateConfigCache();

      // Save workspace metadata after config save (workspace must exist first)
      if (
        selectedFolder &&
        (editMetaDescription || editMetaUrl || editMetaGroup)
      ) {
        const folderGroup = groupedWorkspaces.find(
          (g) => g.displayName === selectedFolder,
        );
        const folderWsUuid = folderGroup?.workspaces[0]?.uuid;
        if (folderWsUuid) {
          try {
            const metaRes = await secureFetch(
              endpoints.workspaces.metadata(folderWsUuid),
              {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                  description: editMetaDescription,
                  url: editMetaUrl,
                  group: editMetaGroup,
                }),
              },
            );
            if (!metaRes.ok) {
              const metaErr = await metaRes.json().catch(() => ({}));
              throw new Error(
                errorMessageFromData(
                  metaErr,
                  "Failed to save workspace metadata",
                ),
              );
            }
          } catch (metaErr) {
            setError("Failed to save metadata: " + metaErr.message);
            const elapsed = Date.now() - saveStartTime;
            setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed));
            return;
          }
        }
      }

      // Save user data schema
      if (selectedFolder) {
        const folderGroup = groupedWorkspaces.find(
          (g) => g.displayName === selectedFolder,
        );
        const folderWsUuid = folderGroup?.workspaces[0]?.uuid;
        if (folderWsUuid) {
          // Filter out fields with empty names
          const validFields = editUserDataFields.filter(
            (f) => f.name.trim() !== "",
          );
          try {
            const schemaRes = await secureFetch(
              endpoints.workspaces.userDataSchema(folderWsUuid),
              {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                  fields: validFields,
                }),
              },
            );
            if (!schemaRes.ok) {
              const schemaErr = await schemaRes.json().catch(() => ({}));
              throw new Error(
                errorMessageFromData(
                  schemaErr,
                  "Failed to save user data schema",
                ),
              );
            }
          } catch (schemaErr) {
            setError("Failed to save user data schema: " + schemaErr.message);
            const elapsed = Date.now() - saveStartTime;
            setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed));
            return;
          }
        }
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
  };

  const getUnusedServer = (workingDir, currentName) => {
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
  };

  // Check if the new (incomplete) folder workspace has a valid working_dir
  const isNewFolderIncomplete = useMemo(() => {
    if (!newFolderKey) return false;
    const ws = workspaces.find((w) => getWorkspaceKey(w) === newFolderKey);
    return ws && (!ws.working_dir || ws.working_dir.trim() === "");
  }, [newFolderKey, workspaces]);

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
    [isNewFolderIncomplete, newFolderKey],
  );

  const addWorkspace = () => {
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
  };

  const removeWorkspace = (key) => {
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
        const remaining = workspaces.filter((w) => getWorkspaceKey(w) !== key);
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
  };

  const duplicateWorkspace = (key) => {
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
  };

  const handleRunnerChange = (r) => {
    setEditRunner(r);
    if (r === "exec") setEditRunnerConfig(null);
    else if (!editRunnerConfig)
      setEditRunnerConfig({
        restrictions: { allow_write_folders: ["$MITTO_WORKING_DIR"] },
      });
  };

  // Add a new ACP server entry to the selected folder
  const addServerToFolder = () => {
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
  };

  // Check if folder has unused ACP servers available
  const folderCanAddServer = useMemo(() => {
    if (!selectedFolder) return false;
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs) return false;
    return getUnusedServer(firstWs.working_dir, null) !== null;
  }, [selectedFolder, groupedWorkspaces, workspaces, acpServers]);

  // Helper to get the first workspace dir for the selected folder
  const getSelectedFolderDir = () => {
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    return folderGroup?.workspaces[0]?.working_dir || null;
  };

  const getSelectedFolderUuid = () => {
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    return folderGroup?.workspaces[0]?.uuid || null;
  };

  // ------ Shortcuts tab helpers -----------------------------------------------

  // Immutably update a row in the given section.
  const updateShortcutRow = (section, idx, patch) => {
    setShortcutsSections((prev) => {
      const list = [...(prev[section] || [])];
      list[idx] = { ...list[idx], ...patch };
      return { ...prev, [section]: list };
    });
  };

  // Remove a row from the given section.
  const removeShortcutRow = (section, idx) => {
    setShortcutsSections((prev) => {
      const list = [...(prev[section] || [])];
      list.splice(idx, 1);
      return { ...prev, [section]: list };
    });
  };

  // Move a row up (dir=-1) or down (dir=1) within the given section.
  const moveShortcutRow = (section, idx, dir) => {
    setShortcutsSections((prev) => {
      const list = [...(prev[section] || [])];
      const target = idx + dir;
      if (target < 0 || target >= list.length) return prev;
      [list[idx], list[target]] = [list[target], list[idx]];
      return { ...prev, [section]: list };
    });
  };

  // Append a new row to the given section, seeded with sensible defaults so it
  // renders as a complete, usable shortcut right away.
  const addShortcutRow = (section) => {
    // Default the prompt to the first available prompt for this section (if any)
    // so the row is immediately editable rather than showing an empty selector.
    const available = sectionPrompts[section] || [];
    const defaultPrompt = available.length > 0 ? available[0].name : "";
    setShortcutsSections((prev) => {
      const list = [...(prev[section] || [])];
      if (list.length >= 10) return prev;
      // Empty icon → fall back to the linked prompt's own icon at render time.
      list.push({ icon: "", prompt: defaultPrompt });
      return { ...prev, [section]: list };
    });
  };

  // Persist the shortcuts sections via PUT /api/folders/shortcuts.
  // Throws on failure; updates local state on success. Invoked by the
  // dialog footer Save (handleSave).
  const persistShortcuts = async () => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    // Build all sections: drop rows with empty prompt, cap to 10 per section.
    const sections = {};
    for (const id of ["tasksList", "conversations", "beadsIssue"]) {
      sections[id] = (shortcutsSections[id] || [])
        .filter((r) => r.prompt)
        .slice(0, 10);
    }
    const res = await secureFetch(
      endpoints.folders.shortcuts({ working_dir: workingDir }),
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sections }),
      },
    );
    const data = await res.json().catch(() => ({}));
    if (!res.ok)
      throw new Error(errorMessageFromData(data, "Failed to save shortcuts"));
    setShortcutsSections(data.sections || {});
    // Notify any open Tasks list (BeadsView) so its shortcut buttons refresh
    // immediately, without requiring a full page reload.
    window.dispatchEvent(
      new CustomEvent("mitto:folder_shortcuts_updated", {
        detail: { working_dir: workingDir },
      }),
    );
  };

  // Per-section set of prompt names configured at the GLOBAL level. Folder rows
  // referencing these are greyed out and the prompts are excluded from the
  // folder-level dropdowns (they are already shown via the global shortcuts).
  const shortcutRedundantPromptNames = useMemo(() => {
    const out = {};
    for (const { id } of SHORTCUT_SECTIONS) {
      out[id] = new Set(
        (globalShortcutsSections[id] || [])
          .map((r) => r.prompt)
          .filter(Boolean),
      );
    }
    return out;
  }, [globalShortcutsSections]);

  // ---------------------------------------------------------------------------

  // Load processors when a folder is selected and the Processors tab is active
  useEffect(() => {
    if (!selectedFolder || activeTab !== "processors") return;
    const folderGroup = groupedWorkspaces.find(
      (g) => g.displayName === selectedFolder,
    );
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs?.uuid) return;

    setProcessorsLoading(true);
    authFetch(endpoints.workspaces.processors(firstWs.uuid))
      .then((r) => r.json())
      .then((data) => {
        setFolderProcessors(data.processors || []);
      })
      .catch((err) => console.error("Failed to load processors:", err))
      .finally(() => setProcessorsLoading(false));
  }, [selectedFolder, activeTab, groupedWorkspaces]);

  // Reload processors for the selected folder
  const reloadFolderProcessors = async (uuid) => {
    const res = await authFetch(endpoints.workspaces.processors(uuid));
    const data = await res.json();
    setFolderProcessors(data.processors || []);
  };

  // Toggle enabled state for a processor via PATCH /api/workspaces/{uuid}/processors/{name}.
  const toggleProcessorEnabled = async (processor) => {
    const uuid = getSelectedFolderUuid();
    if (!uuid) return;
    try {
      const res = await secureFetch(
        endpoints.workspaces.processor(uuid, processor.name),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ enabled: !processor.enabled }),
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
      await reloadFolderProcessors(uuid);
    } catch (err) {
      setError("Failed to toggle processor: " + err.message);
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
      const res = await secureFetch(
        endpoints.workspaces.processorArguments(uuid, proc.name),
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ arguments: args }),
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
      await reloadFolderProcessors(uuid);
      // Clear local edits so inputs re-seed from the freshly-loaded effective values.
      setProcessorArgEdits((prev) => {
        const n = { ...prev };
        delete n[proc.name];
        return n;
      });
    } catch (err) {
      setError("Failed to save processor arguments: " + err.message);
    }
  };

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
        <div class="shrink-0 flex flex-col" style="width: ${leftPanelWidth}px">
          <div
            ref=${scrollContainerRef}
            class="flex-1 overflow-y-auto p-3 space-y-0.5"
          >
            ${loading
              ? html`<div class="flex items-center justify-center py-8">
                  <${SpinnerIcon} className="w-6 h-6 text-mitto-accent" />
                </div>`
              : workspaces.length === 0
                ? html`<div
                    class="text-center py-8 text-mitto-text-muted text-sm px-2"
                  >
                    <${FolderIcon}
                      className="w-8 h-8 mx-auto mb-2 opacity-40"
                    />
                    <p>No workspaces.</p>
                    <p class="text-xs mt-1">
                      Click the folder icon below to add one.
                    </p>
                  </div>`
                : groupedWorkspaces.map(
                    ({ displayName, workspaces: wsGroup }) => {
                      const isFolderSelected =
                        selectedFolder === displayName && !selectedWorkspaceKey;
                      const isExpanded = expandedFolders[displayName] !== false;
                      return html`
                        <div key=${displayName} class="mb-0.5">
                          <!-- Folder header -->
                          <div
                            data-folder-name=${displayName}
                            class="group flex items-center gap-2 px-3 py-1 rounded-sm cursor-pointer transition-colors ${isFolderSelected
                              ? "bg-mitto-accent-500/10"
                              : "hover:bg-base-200/40"}"
                            onClick=${() =>
                              guardNewFolder(() => {
                                setSelectedFolder(displayName);
                                setSelectedWorkspaceKey(null);
                              })}
                          >
                            <span
                              class="shrink-0 flex items-center cursor-pointer"
                              role="button"
                              aria-label=${isExpanded
                                ? "Collapse folder"
                                : "Expand folder"}
                              onClick=${(e) => {
                                e.stopPropagation();
                                toggleFolder(displayName);
                              }}
                            >
                              ${isExpanded
                                ? html`<${ChevronDownIcon}
                                    className="w-3.5 h-3.5 text-mitto-text-muted"
                                  />`
                                : html`<${ChevronRightIcon}
                                    className="w-3.5 h-3.5 text-mitto-text-muted"
                                  />`}
                            </span>
                            <${FolderIcon}
                              className="w-4 h-4 text-mitto-text-muted shrink-0"
                            />
                            <span
                              class="text-sm font-medium truncate flex-1"
                              title=${wsGroup[0]?.working_dir ||
                              "No folder selected"}
                              >${displayName}</span
                            >
                            <span class="text-xs text-mitto-text-muted"
                              >${wsGroup.length}</span
                            >
                          </div>
                          <!-- Workspace children -->
                          ${isExpanded
                            ? html`
                                <div
                                  class="ml-4 pl-3 border-l border-mitto-border mt-0.5"
                                >
                                  ${wsGroup.map((ws) => {
                                    const key = getWorkspaceKey(ws);
                                    const isSelected =
                                      key === selectedWorkspaceKey;
                                    return html`
                                      <div
                                        key=${key}
                                        class="group flex items-center gap-2 px-3 py-1 cursor-pointer transition-colors ${isSelected
                                          ? "bg-mitto-accent-500/20"
                                          : "hover:bg-base-200/40"}"
                                        onClick=${() =>
                                          guardNewFolder(() => {
                                            setSelectedWorkspaceKey(key);
                                            setSelectedFolder(null);
                                          })}
                                      >
                                        <${WorkspaceBadge}
                                          path=${ws.working_dir}
                                          customColor=${ws.color}
                                          customCode=${ws.code}
                                          customName=${ws.name}
                                          size="sm"
                                        />
                                        <span class="text-sm truncate flex-1"
                                          >${ws.acp_server}</span
                                        >
                                      </div>
                                    `;
                                  })}
                                </div>
                              `
                            : ""}
                        </div>
                      `;
                    },
                  )}
          </div>

          <!-- Toolbar: Add Folder / Delete / Duplicate / Add Server -->
          <div
            class="flex items-center justify-end gap-1 px-3 py-2 border-t border-mitto-border"
          >
            <button
              onClick=${addWorkspace}
              aria-disabled=${acpServers.length === 0 || isNewFolderIncomplete
                ? "true"
                : "false"}
              class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${acpServers.length ===
                0 || isNewFolderIncomplete
                ? "opacity-40 pointer-events-none"
                : ""}"
              data-tip="Add folder"
              aria-label="Add folder"
            >
              <${FolderIcon} className="w-4 h-4" />
            </button>
            <button
              onClick=${() =>
                selectedWorkspaceKey && removeWorkspace(selectedWorkspaceKey)}
              aria-disabled=${!selectedWorkspaceKey ||
              selectedFolder ||
              workspaces.length <= 1
                ? "true"
                : "false"}
              class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${!selectedWorkspaceKey ||
              selectedFolder ||
              workspaces.length <= 1
                ? "opacity-40 pointer-events-none"
                : ""}"
              data-tip="Delete selected ACP server"
              aria-label="Delete selected ACP server"
            >
              <${TrashIcon} className="w-4 h-4" />
            </button>
            <button
              onClick=${() =>
                selectedWorkspaceKey &&
                duplicateWorkspace(selectedWorkspaceKey)}
              aria-disabled=${!selectedWorkspaceKey ? "true" : "false"}
              class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${!selectedWorkspaceKey
                ? "opacity-40 pointer-events-none"
                : ""}"
              data-tip="Duplicate selected workspace"
              aria-label="Duplicate selected workspace"
            >
              <${DuplicateIcon} className="w-4 h-4" />
            </button>
            <button
              onClick=${addServerToFolder}
              aria-disabled=${!selectedFolder || !folderCanAddServer
                ? "true"
                : "false"}
              class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${!selectedFolder ||
              !folderCanAddServer
                ? "opacity-40 pointer-events-none"
                : ""}"
              data-tip="Add ACP server to folder"
              aria-label="Add ACP server to folder"
            >
              <${ServerIcon} className="w-4 h-4" />
            </button>
            <div
              class="h-5 border-l border-mitto-border mx-1"
              aria-hidden="true"
            ></div>
            <button
              onClick=${collapseAllFolders}
              aria-disabled=${groupedWorkspaces.length === 0 ? "true" : "false"}
              class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${groupedWorkspaces.length ===
              0
                ? "opacity-40 pointer-events-none"
                : ""}"
              data-tip="Collapse all"
              aria-label="Collapse all folders"
            >
              <${CollapseIcon} className="w-4 h-4" />
            </button>
            <button
              onClick=${expandAllFolders}
              aria-disabled=${groupedWorkspaces.length === 0 ? "true" : "false"}
              class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${groupedWorkspaces.length ===
              0
                ? "opacity-40 pointer-events-none"
                : ""}"
              data-tip="Expand all"
              aria-label="Expand all folders"
            >
              <${ExpandIcon} className="w-4 h-4" />
            </button>
          </div>
        </div>

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
                editName=${editName}
                setEditName=${setEditName}
                editCode=${editCode}
                setEditCode=${setEditCode}
                editColor=${editColor}
                setEditColor=${setEditColor}
                editGroup=${editGroup}
                setEditGroup=${setEditGroup}
                editAutoChildren=${editAutoChildren}
                setEditAutoChildren=${setEditAutoChildren}
                folderGroupSuggestions=${folderGroupSuggestions}
                editMetaDescription=${editMetaDescription}
                setEditMetaDescription=${setEditMetaDescription}
                editMetaUrl=${editMetaUrl}
                setEditMetaUrl=${setEditMetaUrl}
                editMetaGroup=${editMetaGroup}
                setEditMetaGroup=${setEditMetaGroup}
                editUserDataFields=${editUserDataFields}
                setEditUserDataFields=${setEditUserDataFields}
                beads=${beads}
                beadsSetters=${beadsSetters}
                beadsHandlers=${beadsHandlers}
                prompts=${prompts}
                promptsSetters=${promptsSetters}
                promptsHandlers=${promptsHandlers}
                folderProcessors=${folderProcessors}
                processorsLoading=${processorsLoading}
                expandedProcessor=${expandedProcessor}
                setExpandedProcessor=${setExpandedProcessor}
                processorArgEdits=${processorArgEdits}
                setProcessorArgEdits=${setProcessorArgEdits}
                toggleProcessorEnabled=${toggleProcessorEnabled}
                saveProcessorArguments=${saveProcessorArguments}
                shortcutsSections=${shortcutsSections}
                sectionPrompts=${sectionPrompts}
                shortcutsLoading=${shortcutsLoading}
                shortcutsError=${shortcutsError}
                shortcutRedundantPromptNames=${shortcutRedundantPromptNames}
                addShortcutRow=${addShortcutRow}
                updateShortcutRow=${updateShortcutRow}
                removeShortcutRow=${removeShortcutRow}
                moveShortcutRow=${moveShortcutRow}
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
    <${ConfirmDialog}
      isOpen=${mcpInstallOpen}
      title="Install MCP Servers"
      confirmLabel="Install"
      cancelLabel="Cancel"
      isLoading=${mcpInstallLoading}
      onConfirm=${handleMcpInstall}
      onCancel=${() => {
        if (!mcpInstallLoading) {
          setMcpInstallOpen(false);
          setMcpInstallName("");
          setMcpInstallError("");
          setMcpInstallSuccess("");
        }
      }}
    >
      <div class="space-y-4 mt-3">
        <p class="text-sm text-mitto-text-muted">
          Paste one or more MCP server definitions as JSON.
        </p>
        <textarea
          value=${mcpInstallJson}
          onInput=${(e) => {
            setMcpInstallJson(e.target.value);
            setMcpInstallError("");
            setMcpInstallSuccess("");
          }}
          placeholder=${'{\n  "mcpServers": {\n    "server-name": {\n      "command": "...",\n      "args": ["..."]\n    }\n  }\n}'}
          class="textarea textarea-sm w-full h-48 font-mono resize-none"
          disabled=${mcpInstallLoading}
          spellcheck="false"
        />
        ${(() => {
          // Detect format 3 (single server def) to show the name input
          try {
            const p = JSON.parse(mcpInstallJson);
            return (
              (typeof p.command === "string" || typeof p.url === "string") &&
              !p.mcpServers
            );
          } catch {
            return false;
          }
        })() &&
        html`
          <div>
            <label class="block text-sm text-mitto-text-muted mb-1"
              >Server name</label
            >
            <input
              type="text"
              value=${mcpInstallName}
              onInput=${(e) => {
                setMcpInstallName(e.target.value);
                setMcpInstallError("");
              }}
              placeholder="my-server"
              class="input input-sm w-full"
              disabled=${mcpInstallLoading}
            />
          </div>
        `}
        ${mcpTools?.mcp_scopes?.length > 0 &&
        html`
          <div>
            <label class="block text-sm text-mitto-text-muted mb-1"
              >Scope</label
            >
            <select
              value=${mcpInstallScope}
              onChange=${(e) => setMcpInstallScope(e.target.value)}
              class="select select-sm w-full"
              disabled=${mcpInstallLoading}
            >
              ${mcpTools.mcp_scopes.map(
                (scope) => html`
                  <option key=${scope} value=${scope}>${scope}</option>
                `,
              )}
            </select>
          </div>
        `}
        ${mcpInstallError &&
        html`
          <p class="text-sm text-mitto-danger whitespace-pre-wrap">
            ${mcpInstallError}
          </p>
        `}
        ${mcpInstallSuccess &&
        html` <p class="text-sm text-mitto-success">${mcpInstallSuccess}</p> `}
      </div>
    <//>
  `;
}
