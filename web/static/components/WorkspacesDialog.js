// Mitto Web Interface - Workspaces Dialog Component
const { useState, useEffect, useMemo, useCallback, useRef, html } = window.preact;

import {
  secureFetch,
  apiUrl,
  hasNativeFolderPicker,
  pickFolder,
  fetchConfig,
  invalidateConfigCache,
  openExternalURL,
} from "../utils/index.js";

import {
  getWorkspaceVisualInfo,
  getBasename,
} from "../lib.js";

import {
  SpinnerIcon,
  CloseIcon,
  FolderIcon,
  TrashIcon,
  DuplicateIcon,
  ChevronRightIcon,
  ChevronDownIcon,
  ServerIcon,
  EditIcon,
  PlusIcon,
  RefreshIcon,
  RobotIcon,
  GlobeIcon,
} from "./Icons.js";

import { ConfirmDialog } from "./ConfirmDialog.js";
import { Modal } from "./Modal.js";
import { WorkspaceBadge } from "./WorkspaceBadge.js";

import {
  AutoChildrenEditor,
  RunnerRestrictionsEditor,
} from "./SettingsDialog.js";

import { ModelSelection } from "./ModelSelection.js";

// Recommended beads config keys per upstream task system. Shown as context-sensitive
// help under the upstream selector in the Beads tab.
const BEADS_UPSTREAM_HELP = {
  github: {
    label: "GitHub",
    rows: [
      { key: "github.token", desc: "Personal access token" },
      { key: "github.owner", desc: "Repository owner" },
      { key: "github.repo", desc: "Repository name" },
      { key: "github.repository", desc: 'Combined "owner/repo" format' },
      { key: "github.url", desc: "Custom API URL (GitHub Enterprise)" },
    ],
  },
  jira: {
    label: "Jira",
    rows: [
      { key: "jira.url", desc: 'Base URL, e.g. "https://company.atlassian.net"' },
      { key: "jira.project", desc: 'Project key, e.g. "PROJ"' },
      { key: "jira.projects", desc: 'Multiple projects, comma-separated, e.g. "PROJ1,PROJ2"' },
      { key: "jira.api_token", desc: "API token" },
      { key: "jira.username", desc: "Account email (Jira Cloud)" },
      { key: "jira.push_prefix", desc: 'Only push matching issues, e.g. "hippo" or "proj1,proj2"' },
    ],
  },
  gitlab: {
    label: "GitLab",
    rows: [
      { key: "gitlab.url", desc: "GitLab instance URL" },
      { key: "gitlab.token", desc: "Personal access token" },
      { key: "gitlab.project_id", desc: "Project ID or path" },
      { key: "gitlab.group_id", desc: "Group ID for group-level sync" },
      { key: "gitlab.default_project_id", desc: "Project for creating issues in group mode" },
    ],
  },
  linear: {
    label: "Linear",
    rows: [
      { key: "linear.api_key", desc: "API key (for individual developers)" },
      { key: "linear.team_id", desc: "Team ID (UUID)" },
      { key: "linear.team_ids", desc: "Multiple team IDs, comma-separated UUIDs" },
      { key: "linear.project_id", desc: "Optional: sync only this project" },
      { key: "linear.id_mode", desc: 'ID generation: "hash" (default)' },
      { key: "linear.hash_length", desc: "Hash length 3-8 (default: 6)" },
    ],
  },
};

export function WorkspacesDialog({ isOpen, onClose, onSave, initialWorkingDir, initialTab, showToast }) {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const [workspaces, setWorkspaces] = useState([]);
  const [acpServers, setAcpServers] = useState([]);
  const [supportedRunners, setSupportedRunners] = useState([]);
  const [orphanedWorkspaces, setOrphanedWorkspaces] = useState([]);

  const [selectedWorkspaceKey, setSelectedWorkspaceKey] = useState(null);
  const [activeTab, setActiveTab] = useState("general");
  // Pending initial tab to apply after auto-selecting a folder via initialWorkingDir.
  // The folder-population effect (keyed on selectedFolder) otherwise forces "general",
  // so we hand the desired tab off here and consume it there.
  const pendingInitialTabRef = useRef(null);

  // Key of a newly created workspace that doesn't have a valid working_dir yet
  const [newFolderKey, setNewFolderKey] = useState(null);

  const [editName, setEditName] = useState("");
  const [editCode, setEditCode] = useState("");
  const [editColor, setEditColor] = useState("");
  const [editGroup, setEditGroup] = useState("");
  const [editAcpServer, setEditAcpServer] = useState("");
  const [editAuxModelMode, setEditAuxModelMode] = useState("");
  const [editAuxModelPattern, setEditAuxModelPattern] = useState("");
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

  // Track whether a folder group (not a workspace) is selected
  const [selectedFolder, setSelectedFolder] = useState(null);

  // Workspace metadata loaded from .mittorc (description, url)
  const [folderMetadata, setFolderMetadata] = useState(null);
  const [metadataLoading, setMetadataLoading] = useState(false);
  const [editMetaDescription, setEditMetaDescription] = useState("");
  const [editMetaUrl, setEditMetaUrl] = useState("");
  const [editMetaGroup, setEditMetaGroup] = useState("");
  const [editUserDataFields, setEditUserDataFields] = useState([]);

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

  // Folder processors state (for the Processors tab)
  const [folderProcessors, setFolderProcessors] = useState([]);
  const [processorsLoading, setProcessorsLoading] = useState(false);
  const [expandedProcessor, setExpandedProcessor] = useState(null);

  // Folder beads config state (for the Beads Config tab) — UI wrapper over `bd config`.
  // beadsConfig holds the raw {key: value} map last loaded from the server.
  // beadsConfigEntries is the editable list of {key, value} rows for namespaced keys.
  const [beadsConfig, setBeadsConfig] = useState(null);
  const [beadsConfigLoading, setBeadsConfigLoading] = useState(false);
  const [beadsConfigError, setBeadsConfigError] = useState("");
  const [beadsConfigSaving, setBeadsConfigSaving] = useState(false);
  const [newBeadsKey, setNewBeadsKey] = useState("");
  const [newBeadsValue, setNewBeadsValue] = useState("");
  // Folder beads upstream task system ("none"|"jira"|"github"|"gitlab"|"linear"),
  // persisted in folders.json via /api/beads/upstream.
  const [beadsUpstream, setBeadsUpstream] = useState("none");
  const [beadsUpstreamSaving, setBeadsUpstreamSaving] = useState(false);

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

  const handleResizeMouseDown = useCallback((e) => {
    e.preventDefault();
    isDraggingRef.current = true;
    dragStartRef.current = { startX: e.clientX, startWidth: leftPanelWidth };
    document.body.style.userSelect = "none";
    document.body.style.cursor = "col-resize";
  }, [leftPanelWidth]);

  const sortedAcpServers = useMemo(
    () => [...acpServers].sort((a, b) => a.name.localeCompare(b.name)),
    [acpServers],
  );

  const getWorkspaceKey = (ws) => ws.uuid || `${ws.working_dir}|${ws.acp_server}`;

  // Group workspaces by display name, sorted alphabetically, with ACP servers sorted within
  const groupedWorkspaces = useMemo(() => {
    const groups = new Map();
    workspaces.forEach((ws) => {
      const displayName = ws.name || (ws.working_dir ? getBasename(ws.working_dir) : "New Workspace");
      if (!groups.has(displayName)) {
        groups.set(displayName, []);
      }
      groups.get(displayName).push(ws);
    });
    groups.forEach((arr) => arr.sort((a, b) => (a.acp_server || "").localeCompare(b.acp_server || "")));
    return Array.from(groups.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([displayName, wsList]) => ({ displayName, workspaces: wsList }));
  }, [workspaces]);

  const selectedWorkspace = useMemo(
    () => workspaces.find((ws) => getWorkspaceKey(ws) === selectedWorkspaceKey) || null,
    [workspaces, selectedWorkspaceKey],
  );

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
        g.workspaces.some((ws) => ws.working_dir === initialWorkingDir)
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
      const el = container.querySelector(`[data-folder-name="${CSS.escape(selectedFolder)}"]`);
      if (el) {
        el.scrollIntoView({ block: "nearest", behavior: "smooth" });
      }
    });
  }, [isOpen, selectedFolder, loading]);

  // When a workspace child is selected, populate workspace-level edit fields
  useEffect(() => {
    if (!selectedWorkspace) return;
    setEditAcpServer(selectedWorkspace.acp_server || "");
    setEditAuxModelMode(selectedWorkspace.auxiliary_model_selection?.matchMode || "");
    setEditAuxModelPattern(selectedWorkspace.auxiliary_model_selection?.pattern || "");
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
      secureFetch(apiUrl(`/api/workspaces/${selectedWorkspace.uuid}/effective-runner-config`))
        .then((r) => r.json())
        .then((data) => setEffectiveConfig(data))
        .catch(() => {});
    }
  }, [selectedWorkspaceKey]);

  // When a folder is selected, populate folder-level edit fields from the first workspace in the group
  useEffect(() => {
    if (!selectedFolder) return;
    const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
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
    if (firstWs.working_dir) {
      setMetadataLoading(true);
      secureFetch(apiUrl(`/api/workspace-metadata?working_dir=${encodeURIComponent(firstWs.working_dir)}`))
        .then((r) => r.json())
        .then((data) => {
          setFolderMetadata(data || null);
          setEditMetaDescription(data?.description || "");
          setEditMetaUrl(data?.url || "");
          setEditMetaGroup(data?.group || "");
          setEditUserDataFields(
            (data?.user_data_schema?.fields || []).map(f => ({
              name: f.name || '',
              type: f.type || 'string',
              description: f.description || '',
            }))
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
      loadMcpTools(editAcpServer || selectedWorkspace.acp_server, selectedWorkspace.working_dir);
    }
  }, [activeTab, selectedWorkspaceKey, editAcpServer]);

  // Lazily load beads config + upstream when the Beads folder tab is opened.
  useEffect(() => {
    if (activeTab !== "beads" || !selectedFolder) return;
    const workingDir = getSelectedFolderDir();
    if (workingDir) {
      reloadBeadsConfig(workingDir);
      reloadBeadsUpstream(workingDir);
    }
  }, [activeTab, selectedFolder]);

  // Reset beads config state when switching folders.
  useEffect(() => {
    setBeadsConfig(null);
    setBeadsConfigError("");
    setNewBeadsKey("");
    setNewBeadsValue("");
    setBeadsUpstream("none");
  }, [selectedFolder]);

  const loadData = async () => {
    setLoading(true);
    try {
      const [config, runnersRes] = await Promise.all([
        fetchConfig(null, true),
        fetch(apiUrl("/api/supported-runners"), { credentials: "same-origin" }),
      ]);
      const servers = config.acp_servers || [];
      setAcpServers(servers);
      const serverNames = new Set(servers.map((s) => s.name));
      const rawWorkspaces = config.workspaces || [];
      const orphaned = [];
      const valid = rawWorkspaces.filter((ws) => {
        if (!ws.working_dir || ws.working_dir.trim() === "") return false;
        if (!ws.acp_server || !serverNames.has(ws.acp_server)) {
          if (ws.acp_server) orphaned.push({ working_dir: ws.working_dir, missing_server: ws.acp_server });
          return false;
        }
        return true;
      });
      setWorkspaces(valid);
      setOrphanedWorkspaces(orphaned);
      setSelectedFolder(null);
      if (valid.length > 0) {
        setSelectedWorkspaceKey(getWorkspaceKey(valid[0]));
      } else {
        setSelectedWorkspaceKey(null);
      }
      if (runnersRes.ok) {
        setSupportedRunners((await runnersRes.json()) || []);
      } else {
        setSupportedRunners([
          { type: "exec", label: "exec (no restrictions)", supported: true },
          { type: "sandbox-exec", label: "sandbox-exec (macOS)", supported: false },
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

  const loadMcpTools = useCallback(async (acpServer, workingDir) => {
    setMcpToolsLoading(true);
    setMcpToolsError("");
    setMcpTools(null);
    try {
      const params = new URLSearchParams({ acp_server: acpServer });
      if (workingDir) params.set("dir", workingDir);
      const res = await secureFetch(apiUrl(`/api/workspace-mcp-tools?${params}`));
      if (!res.ok) throw new Error(await res.text());
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

  // Check if the given workspace UUID has any active (running) sessions.
  const checkActiveSessionsForWorkspace = useCallback(async (workspaceUUID) => {
    if (!workspaceUUID) return false;
    try {
      const res = await secureFetch(apiUrl("/api/sessions/running"));
      if (!res.ok) return false;
      const data = await res.json();
      return (data.sessions || []).some(s => s.workspace_uuid === workspaceUUID);
    } catch {
      return false;
    }
  }, []);

  // Restart the ACP process for the selected workspace so MCP changes take effect.
  const handleRestartAcp = useCallback(async () => {
    if (!selectedWorkspace?.uuid) return;
    setRestarting(true);
    try {
      const res = await secureFetch(apiUrl(`/api/workspaces/${selectedWorkspace.uuid}/restart-acp`), {
        method: "POST",
      });
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text);
      }
      setNeedsRestart(false);
    } catch (err) {
      setError("Failed to restart ACP: " + err.message);
    } finally {
      setRestarting(false);
    }
  }, [selectedWorkspace]);

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
    if (parsed.mcpServers && typeof parsed.mcpServers === "object" && Object.keys(parsed.mcpServers).length > 0) {
      // Format 1: already has mcpServers wrapper — use as-is
    } else if (typeof parsed.command === "string" || typeof parsed.url === "string") {
      // Format 3: single server definition without a name
      if (!mcpInstallName.trim()) {
        setMcpInstallError("Please enter a server name for the single server definition.");
        return;
      }
      parsed = { mcpServers: { [mcpInstallName.trim()]: parsed } };
    } else {
      // Format 2: bare map of named servers — check all values look like server entries
      const vals = Object.values(parsed);
      if (vals.length > 0 && vals.every(v => v && typeof v === "object" && (typeof v.command === "string" || typeof v.url === "string"))) {
        parsed = { mcpServers: parsed };
      } else {
        setMcpInstallError('Unrecognized JSON format. Paste a "mcpServers" object, a map of named servers, or a single server definition with "command" or "url".');
        return;
      }
    }

    setMcpInstallLoading(true);
    setMcpInstallError("");
    setMcpInstallSuccess("");

    try {
      const acpServer = editAcpServer || selectedWorkspace?.acp_server;
      const res = await secureFetch(apiUrl("/api/workspace-mcp-install"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          acp_server: acpServer,
          dir: selectedWorkspace?.working_dir || "",
          scope: mcpInstallScope,
          definition: parsed,
        }),
      });

      if (!res.ok) {
        const text = await res.text();
        throw new Error(text);
      }

      const data = await res.json();
      const results = data.results || [];
      const failed = results.filter(r => !r.success);

      if (failed.length > 0) {
        setMcpInstallError(failed.map(r => `${r.name}: ${r.message}`).join("\n"));
      } else {
        const names = results.map(r => r.name).join(", ");
        setMcpInstallSuccess(`Successfully installed: ${names}`);
        // Check if active sessions need an ACP restart to pick up the new MCP server
        if (selectedWorkspace?.uuid) {
          checkActiveSessionsForWorkspace(selectedWorkspace.uuid).then(hasActive => {
            if (hasActive) setNeedsRestart(true);
          });
        }
        // Reload MCP tools list after successful install
        setTimeout(() => {
          loadMcpTools(acpServer, selectedWorkspace?.working_dir);
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
  }, [mcpInstallJson, mcpInstallName, mcpInstallScope, editAcpServer, selectedWorkspace, loadMcpTools, checkActiveSessionsForWorkspace]);

  const handleMcpRemove = useCallback(async (serverName, scope) => {
    setMcpRemoveLoading(true);
    try {
      const acpServer = editAcpServer || selectedWorkspace?.acp_server;
      const res = await secureFetch(apiUrl("/api/workspace-mcp-remove"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          acp_server: acpServer,
          dir: selectedWorkspace?.working_dir,
          scope: scope || mcpTools?.mcp_scopes?.[0] || "",
          name: serverName,
        }),
      });
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      if (!data.success) {
        setMcpToolsError(data.message || "Failed to remove MCP server");
      } else {
        // Check if active sessions need an ACP restart to drop the removed MCP server
        if (selectedWorkspace?.uuid) {
          const hasActive = await checkActiveSessionsForWorkspace(selectedWorkspace.uuid);
          if (hasActive) setNeedsRestart(true);
        }
      }
      // Refresh the MCP tools list
      await loadMcpTools(acpServer, selectedWorkspace?.working_dir);
    } catch (err) {
      setMcpToolsError("Failed to remove MCP server: " + err.message);
    } finally {
      setMcpRemoveLoading(false);
    }
  }, [editAcpServer, selectedWorkspace, mcpTools, loadMcpTools, checkActiveSessionsForWorkspace]);

  const handleMcpRemoveConfirm = useCallback((serverName) => {
    const defaultScope = mcpTools?.mcp_scopes?.[0] || "";
    mcpRemoveScopeRef.current = defaultScope;
    setConfirmDialog({
      title: "Remove MCP Server",
      message: `Remove MCP server "${serverName}"?`,
      confirmLabel: "Remove",
      confirmVariant: "danger",
      children: mcpTools?.mcp_scopes?.length > 0 ? html`
        <div class="mt-3">
          <label class="block text-sm text-mitto-text-muted mb-1">Scope</label>
          <select
            value=${defaultScope}
            onInput=${(e) => { mcpRemoveScopeRef.current = e.target.value; }}
            class="select select-sm w-full"
          >
            ${mcpTools.mcp_scopes.map(scope => html`
              <option key=${scope} value=${scope}>${scope}</option>
            `)}
          </select>
        </div>
      ` : null,
      onConfirm: async () => {
        setConfirmDialog(null);
        await handleMcpRemove(serverName, mcpRemoveScopeRef.current || defaultScope);
      },
    });
  }, [mcpTools, handleMcpRemove]);

  // Toggle the "default workspace for this folder" flag. Enforce a single default
  // per folder live: when enabling it, immediately clear is_default on every other
  // workspace that shares this folder so the UI reflects the change before saving.
  const handleToggleIsDefault = (checked) => {
    setEditIsDefault(checked);
    if (checked && selectedWorkspace?.working_dir) {
      setWorkspaces((prev) =>
        prev.map((ws) =>
          ws.working_dir === selectedWorkspace.working_dir && getWorkspaceKey(ws) !== selectedWorkspaceKey
            ? { ...ws, is_default: undefined }
            : ws
        )
      );
    }
  };

  // Apply workspace-level edits (acp_server, runner, auto_approve) to the selected workspace
  const applyWorkspaceEdits = (ws) => {
    if (getWorkspaceKey(ws) !== selectedWorkspaceKey) return ws;
    // Build auxiliary_model_selection object only when both mode and pattern are set
    const auxModelSelection = (editAuxModelMode && editAuxModelPattern)
      ? { matchMode: editAuxModelMode, pattern: editAuxModelPattern }
      : undefined;
    return {
      ...ws,
      acp_server: editAcpServer,
      auxiliary_model_selection: auxModelSelection,
      restricted_runner: editRunner,
      restricted_runner_config: editRunner !== "exec" ? editRunnerConfig : undefined,
      auto_approve: editAutoApprove || undefined,
      is_default: editIsDefault || undefined,
      acp_command_override: editAcpCommandOverride || undefined,
    };
  };

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
      let updated = workspaces.filter((ws) => ws.working_dir && ws.working_dir.trim() !== "");

      // Apply folder-level edits if a folder is selected
      if (selectedFolder) {
        const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
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
            ws.working_dir === selectedWorkspace.working_dir && getWorkspaceKey(ws) !== selectedWorkspaceKey
              ? { ...ws, is_default: undefined }
              : ws
          );
        }
      }

      if (updated.length === 0) { setError("At least one workspace is required"); const elapsed = Date.now() - saveStartTime; setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed)); return; }

      const config = await fetchConfig(null, true);
      const res = await secureFetch(apiUrl("/api/config"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...config, workspaces: updated, prompts: [] }),
      });
      const result = await res.json();
      if (!res.ok) throw new Error(result.error || "Failed to save configuration");
      invalidateConfigCache();

      // Save workspace metadata after config save (workspace must exist first)
      if (selectedFolder && (editMetaDescription || editMetaUrl || editMetaGroup)) {
        const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
        const folderWorkingDir = folderGroup?.workspaces[0]?.working_dir;
        if (folderWorkingDir) {
          try {
            const metaRes = await secureFetch(apiUrl("/api/workspace-metadata"), {
              method: "PUT",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                working_dir: folderWorkingDir,
                description: editMetaDescription,
                url: editMetaUrl,
                group: editMetaGroup,
              }),
            });
            if (!metaRes.ok) {
              const metaErr = await metaRes.json().catch(() => ({}));
              throw new Error(metaErr.error || "Failed to save workspace metadata");
            }
          } catch (metaErr) {
            setError("Failed to save metadata: " + metaErr.message);
            const elapsed = Date.now() - saveStartTime; setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed));
            return;
          }
        }
      }

      // Save user data schema
      if (selectedFolder) {
        const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
        const folderWorkingDir = folderGroup?.workspaces[0]?.working_dir;
        if (folderWorkingDir) {
          // Filter out fields with empty names
          const validFields = editUserDataFields.filter(f => f.name.trim() !== '');
          try {
            const schemaRes = await secureFetch(apiUrl("/api/workspace/user-data-schema"), {
              method: "PUT",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                working_dir: folderWorkingDir,
                fields: validFields,
              }),
            });
            if (!schemaRes.ok) {
              const schemaErr = await schemaRes.json().catch(() => ({}));
              throw new Error(schemaErr.error || "Failed to save user data schema");
            }
          } catch (schemaErr) {
            setError("Failed to save user data schema: " + schemaErr.message);
            const elapsed = Date.now() - saveStartTime; setTimeout(() => setSaving(false), Math.max(0, 1000 - elapsed));
            return;
          }
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
    const used = new Set(workspaces.filter((ws) => ws.working_dir === workingDir).map((ws) => ws.acp_server));
    return acpServers.find((s) => s.name !== currentName && !used.has(s.name))?.name
      || acpServers.find((s) => !used.has(s.name))?.name
      || null;
  };

  // Check if the new (incomplete) folder workspace has a valid working_dir
  const isNewFolderIncomplete = useMemo(() => {
    if (!newFolderKey) return false;
    const ws = workspaces.find((w) => getWorkspaceKey(w) === newFolderKey);
    return ws && (!ws.working_dir || ws.working_dir.trim() === "");
  }, [newFolderKey, workspaces]);

  // Attempt to switch away from an incomplete new folder — warn via dialog and proceed on confirm
  const guardNewFolder = useCallback((onProceed) => {
    if (isNewFolderIncomplete) {
      setConfirmDialog({
        message: "The new workspace has no folder selected. Discard it?",
        confirmLabel: "Discard",
        confirmVariant: "danger",
        onConfirm: () => {
          setWorkspaces((prev) => prev.filter((w) => getWorkspaceKey(w) !== newFolderKey));
          setNewFolderKey(null);
          setConfirmDialog(null);
          onProceed();
        },
      });
      return;
    }
    onProceed();
  }, [isNewFolderIncomplete, newFolderKey]);

  const addWorkspace = () => {
    if (acpServers.length === 0) return;
    // Don't allow creating another while one is incomplete
    if (isNewFolderIncomplete) { setError("Please select a folder for the current new workspace first"); return; }
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
    if (workspaces.length <= 1) { setError("At least one workspace is required"); return; }
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
        const siblings = remaining.filter((w) => w.working_dir === ws.working_dir);
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
    if (!altName) { setError("Cannot duplicate: all ACP servers already used for this folder"); return; }
    const altSrv = acpServers.find((s) => s.name === altName);
    if (!altSrv) { setError("Cannot duplicate: alternative server not found"); return; }
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
    else if (!editRunnerConfig) setEditRunnerConfig({ restrictions: { allow_write_folders: ["$MITTO_WORKING_DIR"] } });
  };

  // Add a new ACP server entry to the selected folder
  const addServerToFolder = () => {
    if (!selectedFolder) return;
    const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs) return;
    const unusedServer = getUnusedServer(firstWs.working_dir, null);
    if (!unusedServer) { setError("All ACP servers are already assigned to this folder"); return; }
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
    const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs) return false;
    return getUnusedServer(firstWs.working_dir, null) !== null;
  }, [selectedFolder, groupedWorkspaces, workspaces, acpServers]);

  // Load prompts when a folder is selected and the Prompts tab is active
  useEffect(() => {
    if (!selectedFolder || activeTab !== "prompts") return;
    const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs?.working_dir) return;

    setPromptsLoading(true);
    secureFetch(apiUrl(`/api/workspace-prompts?dir=${encodeURIComponent(firstWs.working_dir)}&include_global=true`))
      .then((r) => r.json())
      .then((data) => { setFolderPrompts(data.prompts || []); })
      .catch((err) => console.error("Failed to load prompts:", err))
      .finally(() => setPromptsLoading(false));
  }, [selectedFolder, activeTab, groupedWorkspaces]);

  // Helper to get the first workspace dir for the selected folder
  const getSelectedFolderDir = () => {
    const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
    return folderGroup?.workspaces[0]?.working_dir || null;
  };

  // Load (reload) beads config for the selected folder via GET /api/beads/config.
  const reloadBeadsConfig = async (workingDir) => {
    setBeadsConfigLoading(true);
    setBeadsConfigError("");
    try {
      const res = await secureFetch(apiUrl(`/api/beads/config?working_dir=${encodeURIComponent(workingDir)}`));
      const data = await res.json();
      if (data && data.error) {
        // bd missing or not initialized in this folder.
        setBeadsConfig(null);
        setBeadsConfigError(data.error);
      } else {
        setBeadsConfig(data || {});
      }
    } catch (err) {
      setBeadsConfig(null);
      setBeadsConfigError(err.message || "Failed to load beads config");
    } finally {
      setBeadsConfigLoading(false);
    }
  };

  // Set a single beads config key via PUT /api/beads/config, then reload.
  const setBeadsConfigKey = async (key, value) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir || !key) return;
    setBeadsConfigSaving(true);
    setBeadsConfigError("");
    try {
      const res = await secureFetch(apiUrl("/api/beads/config"), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, key, value }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "Failed to set config");
      if (data && data.error) throw new Error(data.stderr || data.error);
      await reloadBeadsConfig(workingDir);
    } catch (err) {
      setBeadsConfigError(err.message || "Failed to set config");
    } finally {
      setBeadsConfigSaving(false);
    }
  };

  // Delete a single beads config key via DELETE /api/beads/config, then reload.
  const unsetBeadsConfigKey = async (key) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir || !key) return;
    setBeadsConfigSaving(true);
    setBeadsConfigError("");
    try {
      const res = await secureFetch(
        apiUrl(`/api/beads/config?working_dir=${encodeURIComponent(workingDir)}&key=${encodeURIComponent(key)}`),
        { method: "DELETE" },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "Failed to delete config");
      if (data && data.error) throw new Error(data.stderr || data.error);
      await reloadBeadsConfig(workingDir);
    } catch (err) {
      setBeadsConfigError(err.message || "Failed to delete config");
    } finally {
      setBeadsConfigSaving(false);
    }
  };

  // Load the folder's upstream task system via GET /api/beads/upstream.
  const reloadBeadsUpstream = async (workingDir) => {
    try {
      const res = await secureFetch(apiUrl(`/api/beads/upstream?working_dir=${encodeURIComponent(workingDir)}`));
      const data = await res.json().catch(() => ({}));
      setBeadsUpstream((data && data.upstream) || "none");
    } catch (_err) {
      setBeadsUpstream("none");
    }
  };

  // Persist the folder's upstream task system via PUT /api/beads/upstream.
  const saveBeadsUpstream = async (upstream) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    const prev = beadsUpstream;
    setBeadsUpstream(upstream); // optimistic
    setBeadsUpstreamSaving(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/upstream"), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, upstream }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "Failed to set upstream");
      if (data && data.error) throw new Error(data.error);
      setBeadsUpstream((data && data.upstream) || upstream);
    } catch (err) {
      setBeadsUpstream(prev); // revert on failure
      setBeadsConfigError(err.message || "Failed to set upstream");
    } finally {
      setBeadsUpstreamSaving(false);
    }
  };

  // Load (reload) prompts for the selected folder
  const reloadFolderPrompts = async (workingDir) => {
    const res = await secureFetch(apiUrl(`/api/workspace-prompts?dir=${encodeURIComponent(workingDir)}&include_global=true`));
    const data = await res.json();
    setFolderPrompts(data.prompts || []);
  };

  // Create or update a workspace prompt file
  const saveWorkspacePrompt = async (promptData) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    setPromptSaving(true);
    try {
      const res = await secureFetch(apiUrl("/api/workspace-prompts"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ dir: workingDir, ...promptData }),
      });
      if (!res.ok) throw new Error(await res.text());
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
        apiUrl(`/api/workspace-prompts?dir=${encodeURIComponent(workingDir)}&name=${encodeURIComponent(promptName)}`),
        { method: "DELETE" }
      );
      if (!res.ok) throw new Error(await res.text());
      await reloadFolderPrompts(workingDir);
    } catch (err) {
      setError("Failed to delete prompt: " + err.message);
    }
  };

  // Load processors when a folder is selected and the Processors tab is active
  useEffect(() => {
    if (!selectedFolder || activeTab !== "processors") return;
    const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs?.working_dir) return;

    setProcessorsLoading(true);
    secureFetch(apiUrl(`/api/workspace-processors?dir=${encodeURIComponent(firstWs.working_dir)}`))
      .then((r) => r.json())
      .then((data) => { setFolderProcessors(data.processors || []); })
      .catch((err) => console.error("Failed to load processors:", err))
      .finally(() => setProcessorsLoading(false));
  }, [selectedFolder, activeTab, groupedWorkspaces]);

  // Reload processors for the selected folder
  const reloadFolderProcessors = async (workingDir) => {
    const res = await secureFetch(apiUrl(`/api/workspace-processors?dir=${encodeURIComponent(workingDir)}`));
    const data = await res.json();
    setFolderProcessors(data.processors || []);
  };

  // Toggle enabled state for a processor via the toggle-enabled endpoint.
  const toggleProcessorEnabled = async (processor) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    try {
      const res = await secureFetch(apiUrl("/api/workspace-processors/toggle-enabled"), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          dir: workingDir,
          name: processor.name,
          enabled: !processor.enabled,
        }),
      });
      if (!res.ok) throw new Error(await res.text());
      await reloadFolderProcessors(workingDir);
    } catch (err) {
      setError("Failed to toggle processor: " + err.message);
    }
  };

  // Toggle enabled state for a prompt using the dedicated toggle-enabled endpoint.
  // If a .md file exists in .mitto/prompts/, its frontmatter is updated in-place.
  // If not, the state is recorded in the workspace .mittorc file.
  const togglePromptEnabled = async (prompt) => {
    const workingDir = getSelectedFolderDir();
    if (!workingDir) return;
    const isCurrentlyEnabled = prompt.enabled !== false;
    try {
      const res = await secureFetch(apiUrl("/api/workspace-prompts/toggle-enabled"), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          dir: workingDir,
          name: prompt.name,
          enabled: !isCurrentlyEnabled,
        }),
      });
      if (!res.ok) throw new Error(await res.text());
      await reloadFolderPrompts(workingDir);
    } catch (err) {
      setError("Failed to toggle prompt: " + err.message);
    }
  };

  if (!isOpen) return null;

  // Different tab sets for folder vs workspace
  const folderTabs = [
    { id: "general", label: "General" },
    { id: "metadata", label: "Metadata" },
    { id: "beads", label: "Tasks" },
    { id: "prompts", label: "Prompts" },
    { id: "processors", label: "Processors" },
    { id: "children", label: "Children" },
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
          setWorkspaces((prev) => prev.filter((w) => getWorkspaceKey(w) !== newFolderKey));
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
        <div class="flex items-center justify-between p-4 border-b border-mitto-border shrink-0">
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
            <div ref=${scrollContainerRef} class="flex-1 overflow-y-auto p-3 space-y-0.5">
              ${loading
                ? html`<div class="flex items-center justify-center py-8"><${SpinnerIcon} className="w-6 h-6 text-mitto-accent" /></div>`
                : workspaces.length === 0
                  ? html`<div class="text-center py-8 text-mitto-text-muted text-sm px-2">
                      <${FolderIcon} className="w-8 h-8 mx-auto mb-2 opacity-40" />
                      <p>No workspaces.</p>
                      <p class="text-xs mt-1">Click the folder icon below to add one.</p>
                    </div>`
                  : groupedWorkspaces.map(({ displayName, workspaces: wsGroup }) => {
                      const isFolderSelected = selectedFolder === displayName && !selectedWorkspaceKey;
                      return html`
                        <div key=${displayName} class="mb-0.5">
                          <!-- Folder header -->
                          <div
                            data-folder-name=${displayName}
                            class="group flex items-center gap-2 px-3 py-1 rounded-sm cursor-pointer transition-colors ${isFolderSelected ? "bg-mitto-accent-500/10" : "hover:bg-base-200/40"}"
                            onClick=${() => guardNewFolder(() => { setSelectedFolder(displayName); setSelectedWorkspaceKey(null); })}
                          >
                            <${ChevronDownIcon} className="w-3.5 h-3.5 text-mitto-text-muted shrink-0" />
                            <${FolderIcon} className="w-4 h-4 text-mitto-text-muted shrink-0" />
                            <span class="text-sm font-medium truncate flex-1" title=${wsGroup[0]?.working_dir || "No folder selected"}>${displayName}</span>
                            <span class="text-xs text-mitto-text-muted">${wsGroup.length}</span>
                          </div>
                          <!-- Workspace children -->
                          <div class="ml-4 pl-3 border-l border-mitto-border mt-0.5">
                            ${wsGroup.map((ws) => {
                              const key = getWorkspaceKey(ws);
                              const isSelected = key === selectedWorkspaceKey;
                              return html`
                                <div
                                  key=${key}
                                  class="group flex items-center gap-2 px-3 py-1 cursor-pointer transition-colors ${isSelected ? "bg-mitto-accent-500/20" : "hover:bg-base-200/40"}"
                                  onClick=${() => guardNewFolder(() => { setSelectedWorkspaceKey(key); setSelectedFolder(null); })}
                                >
                                  <${WorkspaceBadge}
                                    path=${ws.working_dir}
                                    customColor=${ws.color}
                                    customCode=${ws.code}
                                    customName=${ws.name}
                                    size="sm"
                                  />
                                  <span class="text-sm truncate flex-1">${ws.acp_server}</span>
                                </div>
                              `;
                            })}
                          </div>
                        </div>
                      `;
                    })
              }
            </div>

            <!-- Toolbar: Add Folder / Delete / Duplicate / Add Server -->
            <div class="flex items-center justify-end gap-1 px-3 py-2 border-t border-mitto-border">
              <button
                onClick=${addWorkspace}
                aria-disabled=${(acpServers.length === 0 || isNewFolderIncomplete) ? "true" : "false"}
                class="btn btn-ghost btn-square btn-sm ${(acpServers.length === 0 || isNewFolderIncomplete) ? "opacity-40 pointer-events-none" : ""}"
                title="Add folder"
              >
                <${FolderIcon} className="w-4 h-4" />
              </button>
              <button
                onClick=${() => selectedWorkspaceKey && removeWorkspace(selectedWorkspaceKey)}
                aria-disabled=${(!selectedWorkspaceKey || selectedFolder || workspaces.length <= 1) ? "true" : "false"}
                class="btn btn-ghost btn-square btn-sm ${(!selectedWorkspaceKey || selectedFolder || workspaces.length <= 1) ? "opacity-40 pointer-events-none" : ""}"
                title="Delete selected ACP server"
              >
                <${TrashIcon} className="w-4 h-4" />
              </button>
              <button
                onClick=${() => selectedWorkspaceKey && duplicateWorkspace(selectedWorkspaceKey)}
                aria-disabled=${!selectedWorkspaceKey ? "true" : "false"}
                class="btn btn-ghost btn-square btn-sm ${!selectedWorkspaceKey ? "opacity-40 pointer-events-none" : ""}"
                title="Duplicate selected workspace"
              >
                <${DuplicateIcon} className="w-4 h-4" />
              </button>
              <button
                onClick=${addServerToFolder}
                aria-disabled=${(!selectedFolder || !folderCanAddServer) ? "true" : "false"}
                class="btn btn-ghost btn-square btn-sm ${(!selectedFolder || !folderCanAddServer) ? "opacity-40 pointer-events-none" : ""}"
                title="Add ACP server to folder"
              >
                <${ServerIcon} className="w-4 h-4" />
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
              ? (() => {
                  const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
                  const firstWs = folderGroup?.workspaces[0];
                  if (!firstWs) return html`<div class="flex items-center justify-center h-full text-mitto-text-muted text-sm">No workspaces in this folder</div>`;
                  const isNewFolder = newFolderKey && getWorkspaceKey(firstWs) === newFolderKey;
                  const isIncomplete = isNewFolder && (!firstWs.working_dir || firstWs.working_dir.trim() === "");
                  const updateNewFolderPath = (path) => {
                    setWorkspaces((prev) => {
                      // If no other workspace already lives in this folder, this is the
                      // folder's first workspace — mark it as the default for the folder.
                      const isFirstForFolder = !prev.some(
                        (ws) => getWorkspaceKey(ws) !== newFolderKey && ws.working_dir === path
                      );
                      return prev.map((ws) =>
                        getWorkspaceKey(ws) === newFolderKey
                          ? { ...ws, working_dir: path, is_default: isFirstForFolder ? true : undefined }
                          : ws
                      );
                    });
                    // Update the selected folder name to reflect new path
                    const newDisplayName = editName || getBasename(path) || "New Workspace";
                    setSelectedFolder(newDisplayName);
                  };
                  return html`
                    <!-- Folder tab bar (daisyUI radio tabs-border) -->
                    <div role="tablist" class="tabs tabs-border px-4 shrink-0">
                      ${folderTabs.map((tab) => html`
                        <input
                          key=${tab.id}
                          type="radio"
                          name="ws-folder-tabs"
                          role="tab"
                          aria-label=${tab.label}
                          data-testid=${`ws-tab-${tab.id}`}
                          checked=${activeTab === tab.id}
                          onChange=${() => setActiveTab(tab.id)}
                          class="tab ${activeTab === tab.id ? "tab-active text-mitto-accent" : ""}"
                        />
                      `)}
                    </div>

                    <!-- Folder tab content -->
                    <div class="flex-1 overflow-y-auto p-6" data-testid="ws-tab-content">

                      <!-- Folder General tab -->
                      ${activeTab === "general" && html`
                        <div class="space-y-4">
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">Location</legend>
                            <label class="label" for="ws-working-dir">Working Directory</label>
                            ${isNewFolder
                              ? html`
                                  <div class="flex gap-2">
                                    <input
                                      id="ws-working-dir"
                                      type="text"
                                      value=${firstWs.working_dir}
                                      onInput=${(e) => updateNewFolderPath(e.target.value)}
                                      placeholder="/path/to/project"
                                      class="input input-sm flex-1 ${isIncomplete ? "border-error" : ""}"
                                    />
                                    ${hasNativeFolderPicker() && html`
                                      <button
                                        onClick=${async () => { const p = await pickFolder(); if (p) updateNewFolderPath(p); }}
                                        class="btn btn-ghost btn-square btn-sm"
                                        title="Browse"
                                      ><${FolderIcon} className="w-4 h-4" /></button>
                                    `}
                                  </div>
                                  ${isIncomplete && html`<p class="label text-error">Please select a folder for this workspace.</p>`}
                                `
                              : html`
                                  <input
                                    id="ws-working-dir"
                                    type="text"
                                    value=${firstWs.working_dir}
                                    readOnly
                                    class="input input-sm w-full cursor-default"
                                  />
                                `
                            }
                            <label class="label" for="ws-display-name">Display Name</label>
                            <input
                              id="ws-display-name"
                              type="text"
                              value=${editName}
                              onInput=${(e) => setEditName(e.target.value)}
                              placeholder=${getBasename(firstWs.working_dir)}
                              class="input input-sm w-full"
                            />
                            <label class="label" for="ws-folder-group">Group</label>
                            <input
                              id="ws-folder-group"
                              type="text"
                              list="ws-folder-group-options"
                              value=${editGroup}
                              onInput=${(e) => setEditGroup(e.target.value)}
                              placeholder="e.g., development, personal, operations..."
                              class="input input-sm w-full"
                            />
                            <datalist id="ws-folder-group-options">
                              ${folderGroupSuggestions.map(
                                (g) => html`<option value=${g}></option>`,
                              )}
                            </datalist>
                            <p class="text-xs text-mitto-text-muted">
                              Organize folders into groups. Existing groups are suggested as you type.
                            </p>
                          </fieldset>
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">Appearance</legend>
                            <div class="flex gap-4 items-end">
                              <div class="flex-1 min-w-0">
                                <label class="label" for="ws-badge-code">Badge Code</label>
                                <input
                                  id="ws-badge-code"
                                  type="text"
                                  value=${editCode}
                                  onInput=${(e) => setEditCode(e.target.value.toUpperCase().slice(0, 3))}
                                  placeholder="Auto (3 letters max)"
                                  maxlength="3"
                                  class="input input-sm w-full font-mono uppercase"
                                />
                              </div>
                              <div class="shrink-0">
                                <label class="label" for="ws-badge-color">Badge Color</label>
                                <div class="flex items-center gap-2">
                                  <input
                                    id="ws-badge-color"
                                    type="color"
                                    value=${editColor}
                                    onInput=${(e) => setEditColor(e.target.value)}
                                    class="rounded cursor-pointer border border-mitto-border"
                                    style="width: 38px; height: 38px"
                                  />
                                  <span class="text-xs text-mitto-text-muted font-mono">${editColor}</span>
                                </div>
                              </div>
                            </div>
                          </fieldset>
                        </div>
                      `}

                      <!-- Folder Metadata tab -->
                      ${activeTab === "metadata" && html`
                        <div class="space-y-4">
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">Metadata</legend>
                            <label class="label" for="ws-meta-description">Description</label>
                            <textarea
                              id="ws-meta-description"
                              value=${editMetaDescription}
                              onInput=${(e) => setEditMetaDescription(e.target.value)}
                              placeholder="A description of this workspace/project..."
                              rows="3"
                              class="textarea textarea-sm w-full resize-vertical"
                            />
                            <label class="label" for="ws-meta-url">URL</label>
                            <input
                              id="ws-meta-url"
                              type="url"
                              value=${editMetaUrl}
                              onInput=${(e) => setEditMetaUrl(e.target.value)}
                              placeholder="https://github.com/..."
                              class="input input-sm w-full"
                            />
                            <label class="label" for="ws-meta-group">Group</label>
                            <input
                              id="ws-meta-group"
                              type="text"
                              value=${editMetaGroup}
                              onInput=${(e) => setEditMetaGroup(e.target.value)}
                              placeholder="e.g., CGW, Infrastructure, Frontend..."
                              class="input input-sm w-full"
                            />
                          </fieldset>

                          <!-- User Data Schema Editor -->
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">User Data Schema</legend>
                            <div class="flex items-center justify-between mb-2">
                              <p class="label">
                                Define custom data attributes for conversations in this workspace.
                              </p>
                              <button
                                onClick=${() => setEditUserDataFields(prev => [...prev, { name: '', type: 'string', description: '' }])}
                                class="btn btn-ghost btn-xs gap-1"
                                title="Add Field"
                              >
                                <${PlusIcon} className="w-3.5 h-3.5" />
                                Add Field
                              </button>
                            </div>
                            ${editUserDataFields.length === 0 && html`
                              <p class="text-xs text-mitto-text-muted italic py-2">No fields defined. Click "Add Field" to create one.</p>
                            `}
                            ${editUserDataFields.length > 0 && html`
                              <ul class="list">
                                ${editUserDataFields.map((field, i) => html`
                                  <li key=${i} class="list-row items-start gap-2">
                                    <div class="flex-1 min-w-0">
                                      <label class="label" for=${"ws-udf-name-" + i}>Name</label>
                                      <input
                                        id=${"ws-udf-name-" + i}
                                        type="text"
                                        value=${field.name}
                                        onInput=${(e) => setEditUserDataFields(prev => prev.map((f, idx) => idx === i ? { ...f, name: e.target.value } : f))}
                                        placeholder="e.g., JIRA Ticket"
                                        class="input input-sm w-full"
                                        style="height: 28px; box-sizing: border-box"
                                      />
                                    </div>
                                    <div class="w-24 shrink-0">
                                      <label class="label" for=${"ws-udf-type-" + i}>Type</label>
                                      <select
                                        id=${"ws-udf-type-" + i}
                                        value=${field.type}
                                        onChange=${(e) => setEditUserDataFields(prev => prev.map((f, idx) => idx === i ? { ...f, type: e.target.value } : f))}
                                        class="select select-sm w-full"
                                        style="height: 28px; box-sizing: border-box"
                                      >
                                        <option value="string">string</option>
                                        <option value="url">url</option>
                                      </select>
                                    </div>
                                    <div class="flex-1 min-w-0">
                                      <label class="label" for=${"ws-udf-desc-" + i}>Description</label>
                                      <input
                                        id=${"ws-udf-desc-" + i}
                                        type="text"
                                        value=${field.description}
                                        onInput=${(e) => setEditUserDataFields(prev => prev.map((f, idx) => idx === i ? { ...f, description: e.target.value } : f))}
                                        placeholder="Optional description..."
                                        class="input input-sm w-full"
                                        style="height: 28px; box-sizing: border-box"
                                      />
                                    </div>
                                    <div class="shrink-0 pt-4">
                                      <button
                                        onClick=${() => setEditUserDataFields(prev => prev.filter((_, idx) => idx !== i))}
                                        class="btn btn-ghost btn-square btn-xs"
                                        title="Remove field"
                                      >
                                        <${TrashIcon} className="w-3.5 h-3.5" />
                                      </button>
                                    </div>
                                  </li>
                                `)}
                              </ul>
                            `}
                          </fieldset>
                        </div>
                      `}

                      <!-- Folder Beads tab -->
                      ${activeTab === "beads" && html`
                        <div class="space-y-4">
                          <p class="text-sm text-mitto-text-muted">
                            Mitto uses${" "}
                            <a
                              href="https://github.com/steveyegge/beads"
                              onClick=${(e) => {
                                e.preventDefault();
                                openExternalURL("https://github.com/steveyegge/beads");
                              }}
                              class="text-mitto-accent hover:text-mitto-accent-300 underline cursor-pointer"
                              >beads</a
                            >${" "}(the <code>bd</code> tool) for managing tasks.
                          </p>
                          <!-- Upstream task system selector (persisted in folders.json) -->
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">Upstream Tasks</legend>
                            <p class="label">
                              Select the external task system beads syncs with. When set, Pull/Push/Sync
                              actions appear in the Tasks view for this folder.
                            </p>
                            <select
                              value=${beadsUpstream}
                              onInput=${(e) => saveBeadsUpstream(e.target.value)}
                              disabled=${beadsUpstreamSaving}
                              class="select select-sm w-full disabled:opacity-50"
                            >
                              <option value="none">None</option>
                              <option value="jira">Jira</option>
                              <option value="github">GitHub</option>
                              <option value="gitlab">GitLab</option>
                              <option value="linear">Linear</option>
                            </select>
                          </fieldset>

                          ${beadsUpstream !== "none" && BEADS_UPSTREAM_HELP[beadsUpstream] && html`
                            <div class="p-3 bg-mitto-input-box border border-mitto-border rounded-md">
                              <p class="text-xs text-mitto-text-muted mb-2">
                                Recommended ${BEADS_UPSTREAM_HELP[beadsUpstream].label} keys${" "}
                                (click a key to fill the add-key field below):
                              </p>
                              <div class="space-y-1">
                                ${BEADS_UPSTREAM_HELP[beadsUpstream].rows.map((row) => html`
                                  <div key=${row.key} class="flex items-baseline gap-2 text-xs">
                                    <button
                                      type="button"
                                      onClick=${() => setNewBeadsKey(row.key)}
                                      class="font-mono text-mitto-accent hover:text-mitto-accent-300 hover:underline whitespace-nowrap"
                                      title="Use this key in the add-key field below"
                                    >${row.key}</button>
                                    <span class="text-mitto-text-muted">— ${row.desc}</span>
                                  </div>
                                `)}
                              </div>
                            </div>
                          `}

                          <div class="pt-2 border-t border-mitto-border"></div>

                          <p class="text-xs text-mitto-text-muted">
                            Integration settings stored in this folder's beads database via${" "}
                            <span class="font-mono text-mitto-text-muted">bd config</span>. Use namespaced keys such as${" "}
                            <span class="font-mono text-mitto-text-muted">jira.url</span>,${" "}
                            <span class="font-mono text-mitto-text-muted">github.repo</span>, or${" "}
                            <span class="font-mono text-mitto-text-muted">${"custom.<key>"}</span>.
                          </p>

                          ${beadsConfigError && html`
                            <div role="alert" class="alert alert-warning alert-soft text-xs">
                              ${beadsConfigError}
                            </div>
                          `}

                          ${beadsConfigLoading
                            ? html`<div class="flex items-center gap-2 text-sm text-mitto-text-muted"><${SpinnerIcon} className="w-4 h-4 animate-spin" /> Loading…</div>`
                            : (beadsConfig && html`
                              ${(() => {
                                const editable = Object.entries(beadsConfig).filter(([k]) => k.includes("."));
                                const system = Object.entries(beadsConfig).filter(([k]) => !k.includes("."));
                                return html`
                                  <div class="space-y-2">
                                    ${editable.length === 0
                                      ? html`<p class="text-xs text-mitto-text-muted italic">No integration keys set yet.</p>`
                                      : editable.map(([k, v]) => html`
                                        <div key=${k} class="flex gap-2 items-center">
                                          <input
                                            type="text"
                                            value=${k}
                                            readOnly
                                            class="input input-sm font-mono cursor-default"
                                            style="width: 38%; height: 38px; box-sizing: border-box"
                                          />
                                          <input
                                            key=${k + ":" + v}
                                            type="text"
                                            defaultValue=${v}
                                            disabled=${beadsConfigSaving}
                                            onBlur=${(e) => { if (e.target.value !== v) setBeadsConfigKey(k, e.target.value); }}
                                            class="input input-sm flex-1 font-mono"
                                            style="height: 38px; box-sizing: border-box"
                                          />
                                          <button
                                            onClick=${() => { if (beadsConfigSaving) return; unsetBeadsConfigKey(k); }}
                                            aria-disabled=${beadsConfigSaving ? "true" : "false"}
                                            class="btn btn-ghost btn-square btn-sm ${beadsConfigSaving ? "opacity-40 pointer-events-none" : ""}"
                                            title="Delete this key"
                                            style="height: 38px; box-sizing: border-box"
                                          ><${TrashIcon} className="w-4 h-4" /></button>
                                        </div>
                                      `)}

                                    <!-- Add a new key -->
                                    <div class="flex gap-2 items-center">
                                      <input
                                        type="text"
                                        value=${newBeadsKey}
                                        onInput=${(e) => setNewBeadsKey(e.target.value)}
                                        placeholder="jira.url"
                                        class="input input-sm font-mono"
                                        style="width: 38%; height: 38px; box-sizing: border-box"
                                      />
                                      <input
                                        type="text"
                                        value=${newBeadsValue}
                                        onInput=${(e) => setNewBeadsValue(e.target.value)}
                                        placeholder="value"
                                        class="input input-sm flex-1 font-mono"
                                        style="height: 38px; box-sizing: border-box"
                                      />
                                      <button
                                        onClick=${async () => {
                                          const key = newBeadsKey.trim();
                                          if (!key) return;
                                          if (beadsConfigSaving) return;
                                          await setBeadsConfigKey(key, newBeadsValue);
                                          setNewBeadsKey("");
                                          setNewBeadsValue("");
                                        }}
                                        aria-disabled=${(beadsConfigSaving || !newBeadsKey.trim()) ? "true" : "false"}
                                        class="btn btn-ghost btn-square btn-sm ${(beadsConfigSaving || !newBeadsKey.trim()) ? "opacity-40 pointer-events-none" : ""}"
                                        title="Add key"
                                        style="height: 38px; box-sizing: border-box"
                                      ><${PlusIcon} className="w-4 h-4" /></button>
                                    </div>
                                  </div>

                                  ${system.length > 0 && html`
                                    <fieldset class="fieldset pt-2 mt-4">
                                      <legend class="fieldset-legend">System</legend>
                                      <p class="label">Operational beads settings (read-only here; edit via the bd CLI).</p>
                                      <div class="space-y-1">
                                        ${system.map(([k, v]) => html`
                                          <div key=${k} class="flex gap-2 text-xs font-mono text-mitto-text-muted">
                                            <span class="truncate" style="width: 38%">${k}</span>
                                            <span class="flex-1 truncate">${String(v)}</span>
                                          </div>
                                        `)}
                                      </div>
                                    </fieldset>
                                  `}
                                `;
                              })()}
                            `)}
                        </div>
                      `}

                      <!-- Folder Prompts tab -->
                      ${activeTab === "prompts" && html`
                        <div class="space-y-4">
                          <div class="flex items-center justify-between">
                            <p class="text-sm text-mitto-text-muted">
                              Manage prompts for this workspace. Built-in prompts are read-only but can be disabled.
                            </p>
                            <button
                              onClick=${() => setShowAddPrompt(!showAddPrompt)}
                              class="btn btn-ghost btn-square btn-sm ${showAddPrompt ? 'btn-active' : ''}"
                              title="Add Prompt"
                            >
                              <${PlusIcon} className="w-5 h-5" />
                            </button>
                          </div>

                          ${showAddPrompt && html`
                            <fieldset class="fieldset pt-2">
                              <legend class="fieldset-legend">New Prompt</legend>
                              <label class="label" for="new-prompt-name">Button Label</label>
                              <input id="new-prompt-name" type="text" value=${newPromptName} onInput=${(e) => setNewPromptName(e.target.value)}
                                placeholder="e.g., Continue"
                                class="input input-sm w-full"
                              />
                              <label class="label" for="new-prompt-text">Prompt Text</label>
                              <textarea id="new-prompt-text" value=${newPromptText} onInput=${(e) => setNewPromptText(e.target.value)}
                                placeholder="e.g., Please continue with the current task."
                                rows="8"
                                class="textarea textarea-sm w-full resize-y"
                              />
                              <label class="label" for="new-prompt-group">Group (optional)</label>
                              <input id="new-prompt-group" type="text" value=${newPromptGroup} onInput=${(e) => setNewPromptGroup(e.target.value)}
                                placeholder="e.g., Tasks, Code Quality"
                                class="input input-sm w-full"
                              />
                              <label class="label">Background Color (optional)</label>
                              <div class="flex items-center gap-2">
                                <input type="color" value=${newPromptColor || '#334155'} onInput=${(e) => setNewPromptColor(e.target.value)}
                                  class="w-10 h-10 rounded cursor-pointer border border-mitto-border-2"
                                />
                                <input type="text" value=${newPromptColor} onInput=${(e) => setNewPromptColor(e.target.value)}
                                  placeholder="#E8F5E9"
                                  class="input input-sm flex-1 font-mono"
                                />
                              </div>
                              <div class="flex justify-end gap-2 mt-2">
                                <button onClick=${() => { setShowAddPrompt(false); setNewPromptName(""); setNewPromptText(""); setNewPromptColor(""); setNewPromptGroup(""); }}
                                  class="btn btn-ghost btn-sm">Cancel</button>
                                <button onClick=${async () => {
                                    await saveWorkspacePrompt({ name: newPromptName.trim(), prompt: newPromptText.trim(), backgroundColor: newPromptColor || undefined, group: newPromptGroup.trim() || undefined, enabled: true });
                                    setShowAddPrompt(false); setNewPromptName(""); setNewPromptText(""); setNewPromptColor(""); setNewPromptGroup("");
                                  }}
                                  disabled=${!newPromptName.trim() || !newPromptText.trim() || promptSaving}
                                  class="btn btn-primary btn-sm">
                                  ${promptSaving ? 'Saving...' : 'Add Prompt'}
                                </button>
                              </div>
                            </fieldset>
                          `}

                          ${promptsLoading
                            ? html`<div class="flex items-center justify-center p-4"><${SpinnerIcon} className="w-5 h-5 animate-spin" /></div>`
                            : html`
                              <ul class="list">
                                ${folderPrompts.length === 0
                                  ? html`<li class="list-row"><div class="p-4 text-center text-mitto-text-muted text-sm">No prompts found. Click + to add a workspace prompt.</div></li>`
                                  : [...folderPrompts].sort((a, b) => (a.name || "").localeCompare(b.name || "")).map((prompt, idx) => {
                                      const isBuiltin = prompt.source === "builtin" || prompt.source === "file";
                                      const isEnabled = prompt.enabled !== false;
                                      return html`
                                        <li key=${prompt.name}
                                            class="list-row p-0">
                                          <div
                                             class="list-col-grow collapse collapse-plus ${editingPromptIndex === idx ? 'collapse-open' : 'collapse-close'} bg-mitto-surface-3/20 rounded-sm border transition-all ${isEnabled ? 'border-mitto-border-2/50' : 'border-mitto-border-2/30 opacity-60'} w-full">
                                          <div class="collapse-title flex items-center gap-3 p-3 min-h-0 pr-12">
                                            <input type="checkbox" checked=${isEnabled}
                                              onChange=${() => togglePromptEnabled(prompt)}
                                              onClick=${(e) => e.stopPropagation()}
                                              class="checkbox checkbox-sm shrink-0"
                                              title=${isEnabled ? "Disable this prompt" : "Enable this prompt"}
                                            />
                                            ${prompt.backgroundColor && html`
                                              <div class="w-5 h-5 rounded-sm shrink-0 border border-mitto-border-2" style="background-color: ${prompt.backgroundColor}" />
                                            `}
                                            <div class="flex-1 min-w-0">
                                              <div class="flex items-center gap-2">
                                                <span class="text-sm font-medium ${isEnabled ? 'text-mitto-accent' : 'text-mitto-text-muted'}">${prompt.name}</span>
                                                <span class="badge badge-sm ${isBuiltin ? 'bg-mitto-accent-500/20 text-mitto-accent' : 'bg-green-500/20 text-mitto-success'}">
                                                  ${isBuiltin ? 'built-in' : 'workspace'}
                                                </span>
                                              </div>
                                              ${prompt.description && html`<p class="text-xs text-mitto-text-muted mt-0.5 truncate">${prompt.description}</p>`}
                                              ${!prompt.description && prompt.prompt && html`<p class="text-xs text-mitto-text-muted mt-0.5 truncate">${prompt.prompt.slice(0, 80)}${prompt.prompt.length > 80 ? '...' : ''}</p>`}
                                            </div>
                                            <div class="flex items-center gap-1 shrink-0" onClick=${(e) => e.stopPropagation()}>
                                              <button onClick=${() => {
                                                  if (editingPromptIndex === idx) {
                                                    setEditingPromptIndex(null);
                                                  } else {
                                                    setEditPromptName(prompt.name || "");
                                                    setEditPromptText(prompt.prompt || "");
                                                    setEditPromptColor(prompt.backgroundColor || "");
                                                    setEditPromptGroup(prompt.group || "");
                                                    setEditingPromptIndex(idx);
                                                  }
                                                }}
                                                class="btn btn-ghost btn-square btn-xs" title=${isBuiltin ? "View" : "Edit"}>
                                                <${EditIcon} className="w-4 h-4 text-mitto-text-muted" />
                                              </button>
                                              ${!isBuiltin && html`
                                                <button onClick=${() => deleteWorkspacePrompt(prompt.name)}
                                                  class="btn btn-ghost btn-square btn-xs" title="Delete">
                                                  <${TrashIcon} className="w-4 h-4 text-mitto-text-muted hover:text-mitto-danger" />
                                                </button>
                                              `}
                                            </div>
                                          </div>
                                          <div class="collapse-content px-3 pb-3">
                                            <fieldset class="fieldset pt-2">
                                              <legend class="fieldset-legend">${isBuiltin ? 'View Prompt' : 'Edit Prompt'}</legend>
                                              <label class="label" for=${"edit-prompt-name-" + idx}>Button Label</label>
                                              <input id=${"edit-prompt-name-" + idx} type="text" value=${isBuiltin ? prompt.name : editPromptName}
                                                onInput=${(e) => !isBuiltin && setEditPromptName(e.target.value)}
                                                disabled=${isBuiltin}
                                                class="input input-sm w-full ${isBuiltin ? 'opacity-60 cursor-not-allowed' : ''}"
                                              />
                                              <label class="label" for=${"edit-prompt-text-" + idx}>Prompt Text</label>
                                              <textarea id=${"edit-prompt-text-" + idx} rows="8"
                                                value=${isBuiltin ? prompt.prompt : editPromptText}
                                                onInput=${(e) => !isBuiltin && setEditPromptText(e.target.value)}
                                                disabled=${isBuiltin}
                                                class="textarea textarea-sm w-full resize-y ${isBuiltin ? 'opacity-60 cursor-not-allowed' : ''}"
                                              />
                                              <label class="label" for=${"edit-prompt-group-" + idx}>Group (optional)</label>
                                              <input id=${"edit-prompt-group-" + idx} type="text" value=${isBuiltin ? (prompt.group || '') : editPromptGroup}
                                                onInput=${(e) => !isBuiltin && setEditPromptGroup(e.target.value)}
                                                disabled=${isBuiltin}
                                                placeholder="e.g., Tasks, Code Quality"
                                                class="input input-sm w-full ${isBuiltin ? 'opacity-60 cursor-not-allowed' : ''}"
                                              />
                                              ${!isBuiltin && html`
                                                <label class="label">Background Color (optional)</label>
                                                <div class="flex items-center gap-2">
                                                  <input type="color" value=${editPromptColor || '#334155'}
                                                    onInput=${(e) => setEditPromptColor(e.target.value)}
                                                    class="w-8 h-8 rounded cursor-pointer border border-mitto-border-2"
                                                  />
                                                  <input type="text" value=${editPromptColor}
                                                    onInput=${(e) => setEditPromptColor(e.target.value)}
                                                    placeholder="#E8F5E9"
                                                    class="input input-sm flex-1 font-mono"
                                                  />
                                                </div>
                                              `}
                                              <div class="flex justify-end gap-2 mt-2">
                                                <button onClick=${() => setEditingPromptIndex(null)}
                                                  class="btn btn-ghost btn-sm">
                                                  ${isBuiltin ? 'Close' : 'Cancel'}
                                                </button>
                                                ${!isBuiltin && html`
                                                  <button onClick=${async () => {
                                                      await saveWorkspacePrompt({
                                                        name: editPromptName.trim(),
                                                        prompt: editPromptText.trim(),
                                                        backgroundColor: editPromptColor || undefined,
                                                        group: editPromptGroup.trim() || undefined,
                                                        enabled: prompt.enabled !== false,
                                                      });
                                                      setEditingPromptIndex(null);
                                                    }}
                                                    disabled=${!editPromptName.trim() || !editPromptText.trim() || promptSaving}
                                                    class="btn btn-primary btn-sm">
                                                    ${promptSaving ? 'Saving...' : 'Save'}
                                                  </button>
                                                `}
                                              </div>
                                            </fieldset>
                                          </div>
                                        </div>
                                        </li>
                                      `;
                                    })
                                }
                              </ul>
                            `
                          }
                        </div>
                      `}

                      <!-- Folder Processors tab -->
                      ${activeTab === "processors" && html`
                        <div class="space-y-4">
                          <p class="text-sm text-mitto-text-muted">
                            Manage processors for this workspace. Global processors can be disabled per workspace.
                          </p>

                          ${processorsLoading
                            ? html`<div class="flex items-center justify-center p-4"><${SpinnerIcon} className="w-5 h-5 animate-spin" /></div>`
                            : html`
                              <div class="space-y-2">
                                ${folderProcessors.length === 0
                                  ? html`<div class="p-4 text-center text-mitto-text-muted text-sm">No processors found for this workspace.</div>`
                                  : folderProcessors.map((proc) => {
                                      const isWorkspace = proc.source === "workspace";
                                      const isEnabled = proc.enabled !== false;
                                      const isPromptMode = proc.mode === "prompt";
                                      const sourceLabel = isWorkspace ? "workspace" : (proc.source === "builtin" ? "built-in" : "global");
                                      const sourceBadgeClass = isWorkspace
                                        ? "bg-green-500/20 text-mitto-success"
                                        : (proc.source === "builtin" ? "bg-mitto-accent-500/20 text-mitto-accent" : "bg-orange-500/20 text-orange-400");
                                      const borderClass = isPromptMode
                                        ? "border-purple-500/30"
                                        : (isEnabled ? "border-mitto-border-2/50" : "border-mitto-border-2/30 opacity-60");
                                      const isExpanded = expandedProcessor === proc.name;
                                      return html`
                                        <div key=${proc.name}
                                             class="collapse collapse-plus ${isExpanded ? 'collapse-open' : 'collapse-close'} bg-mitto-surface-3/20 rounded-sm border transition-all ${borderClass} ${!isEnabled && !isPromptMode ? 'opacity-60' : ''}">
                                          <div class="collapse-title flex items-center gap-3 p-3 min-h-0 pr-12"
                                               onClick=${() => setExpandedProcessor(isExpanded ? null : proc.name)}>
                                            <input type="checkbox" checked=${isEnabled}
                                              onChange=${() => toggleProcessorEnabled(proc)}
                                              onClick=${(e) => e.stopPropagation()}
                                              class="checkbox checkbox-sm shrink-0"
                                              title=${isEnabled ? "Disable this processor" : "Enable this processor"}
                                            />
                                            <div class="flex-1 min-w-0">
                                              <div class="flex items-center gap-2">
                                                ${isPromptMode && html`<${RobotIcon} className="w-4 h-4 text-purple-400 shrink-0" />`}
                                                <span class="text-sm font-medium font-mono ${isEnabled ? 'text-mitto-accent' : 'text-mitto-text-muted'}">${proc.name}</span>
                                                ${proc.source === "global"
                                                  ? html`<${GlobeIcon} className="w-3.5 h-3.5 text-orange-400 shrink-0" title="Global processor" />`
                                                  : html`<span class="badge badge-sm ${sourceBadgeClass}">${sourceLabel}</span>`
                                                }
                                                ${proc.on && html`<span class="text-xs text-mitto-text-muted">${proc.on}${proc.match ? `:${proc.match}` : ''}</span>`}
                                              </div>
                                              ${proc.description && html`<p class="text-xs text-mitto-text-muted mt-0.5 truncate">${proc.description}</p>`}
                                            </div>
                                          </div>
                                          <div class="collapse-content px-3 pb-3">
                                            <div class="space-y-2 text-sm">
                                              ${proc.description && html`
                                                <div>
                                                  <span class="text-xs text-mitto-text-muted block mb-0.5">Description</span>
                                                  <p class="text-mitto-text">${proc.description}</p>
                                                </div>
                                              `}
                                              ${proc.on && html`
                                                <div>
                                                  <span class="text-xs text-mitto-text-muted block mb-0.5">Trigger</span>
                                                  <p class="font-mono text-xs">${proc.on}${proc.match ? `: ${proc.match}` : ''}</p>
                                                </div>
                                              `}
                                              ${proc.mode && html`
                                                <div>
                                                  <span class="text-xs text-mitto-text-muted block mb-0.5">Mode</span>
                                                  <p class="font-mono text-xs">${proc.mode}</p>
                                                </div>
                                              `}
                                              ${proc.source && html`
                                                <div>
                                                  <span class="text-xs text-mitto-text-muted block mb-0.5">Source</span>
                                                  <p class="font-mono text-xs">${proc.source}</p>
                                                </div>
                                              `}
                                            </div>
                                          </div>
                                        </div>
                                      `;
                                    })
                                }
                              </div>
                            `
                          }
                        </div>
                      `}

                      <!-- Folder Children tab -->
                      ${activeTab === "children" && html`
                        <div class="space-y-5">
                          <p class="text-sm text-mitto-text-muted">Configure automatic child conversations for this folder.</p>
                          <${AutoChildrenEditor}
                            children=${editAutoChildren}
                            workspaces=${workspaces}
                            currentWorkspaceUUID=${firstWs?.uuid}
                            onChange=${setEditAutoChildren}
                            getBasename=${getBasename}
                          />
                        </div>
                      `}
                    </div>
                  `;
                })()
              : !selectedWorkspace
                ? html`<div class="flex flex-col items-center justify-center h-full text-mitto-text-muted text-sm gap-3 px-8 text-center">
                    ${workspaces.length === 0
                      ? html`
                        <${FolderIcon} className="w-10 h-10 opacity-30" />
                        <p class="text-base font-medium text-mitto-text-muted">No workspaces configured</p>
                        <p>Add a workspace to specify a folder where an ACP server will operate.</p>
                        <p class="text-xs">Click the <span class="inline-flex items-center gap-1 text-mitto-text-muted"><${FolderIcon} className="w-3.5 h-3.5" /> folder</span> button below to get started.</p>
                      `
                      : html`<p>Select a workspace to edit</p>`
                    }
                  </div>`
                : html`
                <!-- Workspace tab bar (daisyUI radio tabs-border) -->
                <div role="tablist" class="tabs tabs-border px-4 shrink-0">
                  ${workspaceTabs.map((tab) => html`
                    <input
                      key=${tab.id}
                      type="radio"
                      name="ws-workspace-tabs"
                      role="tab"
                      aria-label=${tab.label}
                      data-testid=${`ws-tab-${tab.id}`}
                      checked=${activeTab === tab.id}
                      onChange=${() => setActiveTab(tab.id)}
                      class="tab ${activeTab === tab.id ? "tab-active text-mitto-accent" : ""}"
                    />
                  `)}
                </div>

                <!-- Workspace tab content -->
                <div class="flex-1 overflow-y-auto p-6" data-testid="ws-tab-content">

                  <!-- Workspace General tab -->
                  ${activeTab === "general" && html`
                    <div class="space-y-4">
                      <div>
                        <label class="block text-sm text-mitto-text-muted mb-1">ACP Server</label>
                        <select
                          value=${editAcpServer}
                          onChange=${(e) => setEditAcpServer(e.target.value)}
                          class="select select-sm w-full"
                          style="height: 38px; box-sizing: border-box"
                        >
                          ${sortedAcpServers.map((s) => html`<option key=${s.name} value=${s.name}>${s.name}</option>`)}
                        </select>
                      </div>
                      <div>
                        <label class="block text-sm text-mitto-text-muted mb-1">ACP Command Override (optional)</label>
                        <input
                          type="text"
                          value=${editAcpCommandOverride}
                          onInput=${(e) => setEditAcpCommandOverride(e.target.value)}
                          placeholder=${(() => { const s = acpServers.find((s) => s.name === editAcpServer); return s ? s.command : ""; })()}
                          class="input input-sm w-full placeholder:text-mitto-text-muted"
                          style="height: 38px; box-sizing: border-box"
                        />
                        <p class="text-xs text-mitto-text-muted mt-1">Custom command line for running the ACP server. Leave empty to use the default.</p>
                      </div>
                      <div>
                        <label class="block text-sm text-mitto-text-muted mb-1">Auxiliary Model Selection (optional)</label>
                        <p class="text-xs text-mitto-text-muted mb-2">
                          Switch auxiliary sessions (titles, suggestions) to a specific model
                        </p>
                        <${ModelSelection}
                          matchMode=${editAuxModelMode}
                          pattern=${editAuxModelPattern}
                          onChange=${(mode, pat) => { setEditAuxModelMode(mode); setEditAuxModelPattern(pat); }}
                        />
                      </div>
                      <label class="flex items-center gap-3 cursor-pointer">
                        <input
                          type="checkbox"
                          checked=${editAutoApprove}
                          onChange=${(e) => setEditAutoApprove(e.target.checked)}
                          class="checkbox checkbox-sm"
                        />
                        <span class="text-sm">Auto-approve tool calls</span>
                      </label>
                      <label class="flex items-center gap-3 cursor-pointer">
                        <input
                          type="checkbox"
                          checked=${editIsDefault}
                          onChange=${(e) => handleToggleIsDefault(e.target.checked)}
                          class="checkbox checkbox-sm"
                        />
                        <span class="text-sm">Default workspace for this folder</span>
                      </label>
                      <p class="text-xs text-mitto-text-muted -mt-2 ml-7">
                        Preferred when this folder has several workspaces and one is launched without a specific agent.
                      </p>
                    </div>
                  `}

                  <!-- Workspace Runner tab -->
                  ${activeTab === "runner" && html`
                    <div class="space-y-5">
                      <div>
                        <label class="block text-sm text-mitto-text-muted mb-3">Runner Type</label>
                        <div class="space-y-2">
                          ${supportedRunners.map((r) => html`
                            <label key=${r.type} class="flex items-center gap-3 cursor-pointer ${!r.supported ? "opacity-50" : ""}">
                              <input
                                type="radio"
                                name="runner-${getWorkspaceKey(selectedWorkspace)}"
                                value=${r.type}
                                checked=${editRunner === r.type}
                                disabled=${!r.supported}
                                onChange=${() => handleRunnerChange(r.type)}
                                class="radio radio-sm"
                              />
                              <span class="text-sm">${r.label}</span>
                            </label>
                          `)}
                        </div>
                      </div>
                      ${editRunner !== "exec" && html`
                        <${RunnerRestrictionsEditor}
                          runnerType=${editRunner}
                          config=${editRunnerConfig}
                          effectiveConfig=${effectiveConfig}
                          onChange=${setEditRunnerConfig}
                        />
                      `}
                    </div>
                  `}

                  <!-- Workspace MCP tab -->
                  ${activeTab === "mcp" && html`
                    <div class="space-y-4">
                      <div class="flex items-center justify-between">
                        <p class="text-sm text-mitto-text-muted">
                          MCP servers configured for this workspace's ACP agent${mcpTools?.agent_name ? ` (${mcpTools.agent_name})` : ""}.
                        </p>
                        <div class="flex items-center gap-0.5">
                          <button
                            onClick=${() => { if (mcpToolsLoading) return; loadMcpTools(editAcpServer || selectedWorkspace?.acp_server, selectedWorkspace?.working_dir); }}
                            aria-disabled=${mcpToolsLoading ? "true" : "false"}
                            class="btn btn-ghost btn-square btn-sm ${mcpToolsLoading ? "opacity-40 pointer-events-none" : ""}"
                            title="Refresh MCP server list"
                          >
                            <${RefreshIcon} className=${`w-4 h-4 ${mcpToolsLoading ? "animate-spin" : ""}`} />
                          </button>
                          ${mcpTools?.has_mcp_install && html`
                            <button
                              onClick=${() => {
                                setMcpInstallOpen(true);
                                setMcpInstallJson("");
                                setMcpInstallName("");
                                setMcpInstallScope(mcpTools?.mcp_scopes?.[0] || "");
                                setMcpInstallError("");
                                setMcpInstallSuccess("");
                              }}
                              class="btn btn-ghost btn-square btn-sm"
                              title="Install MCP servers"
                            >
                              <${PlusIcon} className="w-4 h-4" />
                            </button>
                          `}
                        </div>
                      </div>
                      ${mcpToolsLoading
                        ? html`<div class="flex items-center justify-center p-8"><${SpinnerIcon} className="w-5 h-5 animate-spin" /></div>`
                        : mcpToolsError
                          ? html`<div class="p-4 text-center text-mitto-warning text-sm">${mcpToolsError}</div>`
                          : mcpTools?.servers?.length === 0
                            ? html`<div class="p-4 text-center text-mitto-text-muted text-sm">
                                ${mcpTools?.message || "No MCP servers found for this agent."}
                              </div>`
                            : html`
                              <div class="overflow-x-auto border border-mitto-border rounded-md">
                                <table class="table table-sm" style="table-layout: fixed;">
                                  <colgroup>
                                    <col style="width: 140px;" />
                                    <col />
                                    ${mcpTools?.has_mcp_remove && html`<col style="width: 44px;" />`}
                                  </colgroup>
                                  <thead>
                                    <tr>
                                      <th>Name</th>
                                      <th>Command / URL</th>
                                      ${mcpTools?.has_mcp_remove && html`<th></th>`}
                                    </tr>
                                  </thead>
                                  <tbody>
                                    ${mcpTools?.servers?.map((srv, i) => html`
                                      <tr key=${srv.name || i}>
                                        <td class="font-medium truncate" title=${srv.name}>${srv.name}</td>
                                        <td class="text-mitto-text-muted font-mono text-xs truncate" title=${srv.url || [srv.command, ...(srv.args || [])].join(" ")}>
                                          ${srv.url || [srv.command, ...(srv.args || [])].join(" ")}
                                        </td>
                                        ${mcpTools?.has_mcp_remove && html`
                                          <td class="text-center">
                                            <button
                                              onClick=${() => { if (mcpRemoveLoading) return; handleMcpRemoveConfirm(srv.name); }}
                                              aria-disabled=${mcpRemoveLoading ? "true" : "false"}
                                              class="btn btn-ghost btn-square btn-xs ${mcpRemoveLoading ? "opacity-40 pointer-events-none" : ""}"
                                              title="Remove MCP server"
                                            >
                                              <${TrashIcon} className="w-4 h-4 text-mitto-text-muted hover:text-mitto-danger" />
                                            </button>
                                          </td>
                                        `}
                                      </tr>
                                    `)}
                                  </tbody>
                                </table>
                              </div>
                            `
                      }
                    </div>
                  `}
                </div>
              `}
          </div>
        </div>

        <!-- Footer -->
        <div class="flex items-center justify-between p-4 border-t border-mitto-border shrink-0">
          <div class="flex-1 mr-4">
            ${orphanedWorkspaces.length > 0 && html`
              <p class="text-xs text-mitto-warning">⚠ ${orphanedWorkspaces.length} workspace(s) hidden: missing ACP server</p>
            `}
            ${error && html`<p class="text-xs text-mitto-danger">${error}</p>`}
          </div>
          <div class="flex gap-2">
            ${needsRestart && html`
              <button
                onClick=${handleRestartAcp}
                disabled=${restarting}
                class="btn btn-warning btn-sm gap-2"
                title="Restart ACP to apply MCP changes to active conversations"
              >
                ${restarting
                  ? html`<${SpinnerIcon} className="w-4 h-4" /> Restarting...`
                  : "Restart ACP"}
              </button>
            `}
            <button onClick=${handleClose} data-testid="ws-close" class="btn btn-ghost btn-sm">Close</button>
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
          onInput=${(e) => { setMcpInstallJson(e.target.value); setMcpInstallError(""); setMcpInstallSuccess(""); }}
          placeholder=${'{\n  "mcpServers": {\n    "server-name": {\n      "command": "...",\n      "args": ["..."]\n    }\n  }\n}'}
          class="textarea textarea-sm w-full h-48 font-mono resize-none"
          disabled=${mcpInstallLoading}
          spellcheck="false"
        />
        ${(() => {
          // Detect format 3 (single server def) to show the name input
          try {
            const p = JSON.parse(mcpInstallJson);
            return (typeof p.command === "string" || typeof p.url === "string") && !p.mcpServers;
          } catch { return false; }
        })() && html`
          <div>
            <label class="block text-sm text-mitto-text-muted mb-1">Server name</label>
            <input
              type="text"
              value=${mcpInstallName}
              onInput=${(e) => { setMcpInstallName(e.target.value); setMcpInstallError(""); }}
              placeholder="my-server"
              class="input input-sm w-full"
              disabled=${mcpInstallLoading}
            />
          </div>
        `}
        ${mcpTools?.mcp_scopes?.length > 0 && html`
          <div>
            <label class="block text-sm text-mitto-text-muted mb-1">Scope</label>
            <select
              value=${mcpInstallScope}
              onChange=${(e) => setMcpInstallScope(e.target.value)}
              class="select select-sm w-full"
              disabled=${mcpInstallLoading}
            >
              ${mcpTools.mcp_scopes.map(scope => html`
                <option key=${scope} value=${scope}>${scope}</option>
              `)}
            </select>
          </div>
        `}
        ${mcpInstallError && html`
          <p class="text-sm text-mitto-danger whitespace-pre-wrap">${mcpInstallError}</p>
        `}
        ${mcpInstallSuccess && html`
          <p class="text-sm text-mitto-success">${mcpInstallSuccess}</p>
        `}
      </div>
    <//>
  `;
}
