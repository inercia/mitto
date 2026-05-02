// Mitto Web Interface - Workspaces Dialog Component
const { useState, useEffect, useMemo, useCallback, useRef, html } = window.preact;

import {
  secureFetch,
  apiUrl,
  hasNativeFolderPicker,
  pickFolder,
  fetchConfig,
  invalidateConfigCache,
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
  RobotIcon,
} from "./Icons.js";

import { ConfirmDialog } from "./ConfirmDialog.js";

import {
  AutoChildrenEditor,
  RunnerRestrictionsEditor,
} from "./SettingsDialog.js";

export function WorkspacesDialog({ isOpen, onClose, onSave, WorkspaceBadge }) {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const [workspaces, setWorkspaces] = useState([]);
  const [acpServers, setAcpServers] = useState([]);
  const [supportedRunners, setSupportedRunners] = useState([]);
  const [orphanedWorkspaces, setOrphanedWorkspaces] = useState([]);

  const [selectedWorkspaceKey, setSelectedWorkspaceKey] = useState(null);
  const [activeTab, setActiveTab] = useState("general");

  // Key of a newly created workspace that doesn't have a valid working_dir yet
  const [newFolderKey, setNewFolderKey] = useState(null);

  const [editName, setEditName] = useState("");
  const [editCode, setEditCode] = useState("");
  const [editColor, setEditColor] = useState("");
  const [editAcpServer, setEditAcpServer] = useState("");
  const [editAuxAcpServer, setEditAuxAcpServer] = useState("");
  const [editRunner, setEditRunner] = useState("exec");
  const [editRunnerConfig, setEditRunnerConfig] = useState(null);
  const [editAutoApprove, setEditAutoApprove] = useState(false);
  const [editAutoChildren, setEditAutoChildren] = useState([]);
  const [effectiveConfig, setEffectiveConfig] = useState(null);

  // Track whether a folder group (not a workspace) is selected
  const [selectedFolder, setSelectedFolder] = useState(null);

  // Workspace metadata loaded from .mittorc (description, url)
  const [folderMetadata, setFolderMetadata] = useState(null);
  const [metadataLoading, setMetadataLoading] = useState(false);
  const [editMetaDescription, setEditMetaDescription] = useState("");
  const [editMetaUrl, setEditMetaUrl] = useState("");
  const [editMetaGroup, setEditMetaGroup] = useState("");

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

  useEffect(() => {
    if (isOpen) {
      setError("");
      setNewFolderKey(null);
      loadData();
    }
  }, [isOpen]);

  // When a workspace child is selected, populate workspace-level edit fields
  useEffect(() => {
    if (!selectedWorkspace) return;
    setEditAcpServer(selectedWorkspace.acp_server || "");
    setEditAuxAcpServer(selectedWorkspace.auxiliary_acp_server || "");
    setEditRunner(selectedWorkspace.restricted_runner || "exec");
    setEditRunnerConfig(selectedWorkspace.restricted_runner_config || null);
    setEditAutoApprove(selectedWorkspace.auto_approve === true);
    setEffectiveConfig(null);
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
    setEditColor(
      firstWs.color ||
        getWorkspaceVisualInfo(firstWs.working_dir).color.backgroundHex ||
        "#808080",
    );
    setEditAutoChildren(firstWs.auto_children || []);
    setActiveTab("general");

    // Load workspace metadata from .mittorc
    setFolderMetadata(null);
    setEditMetaDescription("");
    setEditMetaUrl("");
    setEditMetaGroup("");
    if (firstWs.working_dir) {
      setMetadataLoading(true);
      secureFetch(apiUrl(`/api/workspace-metadata?working_dir=${encodeURIComponent(firstWs.working_dir)}`))
        .then((r) => r.json())
        .then((data) => {
          setFolderMetadata(data || null);
          setEditMetaDescription(data?.description || "");
          setEditMetaUrl(data?.url || "");
          setEditMetaGroup(data?.group || "");
        })
        .catch(() => {
          setFolderMetadata(null);
          setEditMetaDescription("");
          setEditMetaUrl("");
          setEditMetaGroup("");
        })
        .finally(() => {
          setMetadataLoading(false);
        });
    }
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
      auto_children: editAutoChildren.length > 0 ? editAutoChildren : undefined,
    };
  };

  // Apply workspace-level edits (acp_server, runner, auto_approve) to the selected workspace
  const applyWorkspaceEdits = (ws) => {
    if (getWorkspaceKey(ws) !== selectedWorkspaceKey) return ws;
    const server = acpServers.find((s) => s.name === editAcpServer);
    return {
      ...ws,
      acp_server: editAcpServer,
      acp_command: server ? server.command : ws.acp_command,
      auxiliary_acp_server: editAuxAcpServer || undefined,
      restricted_runner: editRunner,
      restricted_runner_config: editRunner !== "exec" ? editRunnerConfig : undefined,
      auto_approve: editAutoApprove || undefined,
    };
  };

  const handleSave = async () => {
    // Block save if there's an incomplete new folder
    if (isNewFolderIncomplete) {
      setError("Please select a folder for the new workspace before saving");
      return;
    }
    setSaving(true);
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
      }

      if (updated.length === 0) { setError("At least one workspace is required"); setSaving(false); return; }

      const config = await fetchConfig(null, true);
      const res = await secureFetch(apiUrl("/api/config"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...config, workspaces: updated }),
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
            setSaving(false);
            return;
          }
        }
      }

      setNewFolderKey(null);
      onSave?.();
      onClose?.();
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
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
      acp_command: server.command,
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
      acp_command: altSrv.command,
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
      acp_command: server.command,
      restricted_runner: "exec",
      ...(firstWs.name && { name: firstWs.name }),
      ...(firstWs.code && { code: firstWs.code }),
      ...(firstWs.color && { color: firstWs.color }),
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
    { id: "prompts", label: "Prompts" },
    { id: "processors", label: "Processors" },
    { id: "children", label: "Children" },
  ];

  const workspaceTabs = [
    { id: "general", label: "General" },
    { id: "runner", label: "Runner" },
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
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick=${handleClose}>
      <div class="workspaces-dialog bg-mitto-sidebar rounded-xl w-[70vw] h-[70vh] max-w-[95vw] max-h-[95vh] overflow-hidden shadow-2xl flex flex-col" onClick=${(e) => e.stopPropagation()}>

        <!-- Header -->
        <div class="flex items-center justify-between p-4 border-b border-mitto-border flex-shrink-0">
          <h3 class="text-lg font-semibold flex items-center gap-2">
            <${FolderIcon} className="w-5 h-5 opacity-70" />
            Workspaces
          </h3>
          <button onClick=${handleClose} class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors">
            <${CloseIcon} className="w-4 h-4" />
          </button>
        </div>

        <!-- Body -->
        <div ref=${containerRef} class="flex flex-1 min-h-0 overflow-hidden">

          <!-- Left panel: workspace list -->
          <div class="flex-shrink-0 flex flex-col" style="width: ${leftPanelWidth}px">
            <div class="flex-1 overflow-y-auto p-3 space-y-1.5">
              ${loading
                ? html`<div class="flex items-center justify-center py-8"><${SpinnerIcon} className="w-6 h-6 text-blue-400" /></div>`
                : workspaces.length === 0
                  ? html`<div class="text-center py-8 text-gray-500 text-sm px-2">
                      <${FolderIcon} className="w-8 h-8 mx-auto mb-2 opacity-40" />
                      <p>No workspaces.</p>
                      <p class="text-xs mt-1">Click the folder icon below to add one.</p>
                    </div>`
                  : groupedWorkspaces.map(({ displayName, workspaces: wsGroup }) => {
                      const isFolderSelected = selectedFolder === displayName && !selectedWorkspaceKey;
                      return html`
                        <div key=${displayName} class="mb-1.5">
                          <!-- Folder header -->
                          <div
                            class="group flex items-center gap-2 px-3 py-2 rounded-lg cursor-pointer transition-colors ${isFolderSelected ? "bg-blue-500/10" : "hover:bg-slate-700/30"}"
                            onClick=${() => guardNewFolder(() => { setSelectedFolder(displayName); setSelectedWorkspaceKey(null); })}
                          >
                            <${ChevronDownIcon} className="w-3.5 h-3.5 text-gray-500 flex-shrink-0" />
                            <${FolderIcon} className="w-4 h-4 text-gray-400 flex-shrink-0" />
                            <span class="text-sm font-medium truncate flex-1" title=${wsGroup[0]?.working_dir || "No folder selected"}>${displayName}</span>
                            <span class="text-xs text-gray-600">${wsGroup.length}</span>
                          </div>
                          <!-- Workspace children -->
                          <div class="ml-4 pl-3 border-l border-mitto-border mt-1">
                            ${wsGroup.map((ws) => {
                              const key = getWorkspaceKey(ws);
                              const isSelected = key === selectedWorkspaceKey;
                              return html`
                                <div
                                  key=${key}
                                  class="group flex items-center gap-2 px-3 py-2 cursor-pointer transition-colors border-b border-mitto-border ${isSelected ? "bg-blue-500/20" : "hover:bg-slate-700/30"}"
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
                disabled=${acpServers.length === 0 || isNewFolderIncomplete}
                class="p-1.5 rounded-lg transition-colors hover:bg-blue-600 hover:text-white text-gray-400 disabled:opacity-30 disabled:cursor-not-allowed"
                title="Add folder"
              >
                <${FolderIcon} className="w-4 h-4" />
              </button>
              <button
                onClick=${() => selectedWorkspaceKey && removeWorkspace(selectedWorkspaceKey)}
                disabled=${!selectedWorkspaceKey || selectedFolder || workspaces.length <= 1}
                class="p-1.5 rounded-lg transition-colors hover:bg-red-600 hover:text-white text-gray-400 disabled:opacity-30 disabled:cursor-not-allowed"
                title="Delete selected ACP server"
              >
                <${TrashIcon} className="w-4 h-4" />
              </button>
              <button
                onClick=${() => selectedWorkspaceKey && duplicateWorkspace(selectedWorkspaceKey)}
                disabled=${!selectedWorkspaceKey}
                class="p-1.5 rounded-lg transition-colors hover:bg-blue-600 hover:text-white text-gray-400 disabled:opacity-30 disabled:cursor-not-allowed"
                title="Duplicate selected workspace"
              >
                <${DuplicateIcon} className="w-4 h-4" />
              </button>
              <button
                onClick=${addServerToFolder}
                disabled=${!selectedFolder || !folderCanAddServer}
                class="p-1.5 rounded-lg transition-colors hover:bg-blue-600 hover:text-white text-gray-400 disabled:opacity-30 disabled:cursor-not-allowed"
                title="Add ACP server to folder"
              >
                <${ServerIcon} className="w-4 h-4" />
              </button>
            </div>
          </div>

          <!-- Resize handle -->
          <div
            class="w-1 flex-shrink-0 cursor-col-resize bg-mitto-border hover:bg-blue-500/50 transition-colors"
            onMouseDown=${handleResizeMouseDown}
          />

          <!-- Right panel: editor -->
          <div class="flex-1 flex flex-col min-w-0 overflow-hidden">
            ${selectedFolder && !selectedWorkspace
              ? (() => {
                  const folderGroup = groupedWorkspaces.find((g) => g.displayName === selectedFolder);
                  const firstWs = folderGroup?.workspaces[0];
                  if (!firstWs) return html`<div class="flex items-center justify-center h-full text-gray-500 text-sm">No workspaces in this folder</div>`;
                  const isNewFolder = newFolderKey && getWorkspaceKey(firstWs) === newFolderKey;
                  const isIncomplete = isNewFolder && (!firstWs.working_dir || firstWs.working_dir.trim() === "");
                  const updateNewFolderPath = (path) => {
                    setWorkspaces((prev) => prev.map((ws) =>
                      getWorkspaceKey(ws) === newFolderKey ? { ...ws, working_dir: path } : ws
                    ));
                    // Update the selected folder name to reflect new path
                    const newDisplayName = editName || getBasename(path) || "New Workspace";
                    setSelectedFolder(newDisplayName);
                  };
                  return html`
                    <!-- Folder tab bar -->
                    <div class="flex border-b border-mitto-border px-4 flex-shrink-0">
                      ${folderTabs.map((tab) => html`
                        <button
                          key=${tab.id}
                          onClick=${() => setActiveTab(tab.id)}
                          class="px-4 py-2.5 text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${activeTab === tab.id ? "border-blue-500 text-blue-400" : "border-transparent text-gray-500 hover:text-gray-300"}"
                          style="margin-bottom: -1px"
                        >${tab.label}</button>
                      `)}
                    </div>

                    <!-- Folder tab content -->
                    <div class="flex-1 overflow-y-auto p-6">

                      <!-- Folder General tab -->
                      ${activeTab === "general" && html`
                        <div class="space-y-4">
                          <div>
                            <label class="block text-sm text-gray-400 mb-1">Working Directory</label>
                            ${isNewFolder
                              ? html`
                                  <div class="flex gap-2">
                                    <input
                                      type="text"
                                      value=${firstWs.working_dir}
                                      onInput=${(e) => updateNewFolderPath(e.target.value)}
                                      placeholder="/path/to/project"
                                      class="flex-1 bg-mitto-input border ${isIncomplete ? "border-red-400" : "border-mitto-border"} rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                      style="height: 38px; box-sizing: border-box"
                                    />
                                    ${hasNativeFolderPicker() && html`
                                      <button
                                        onClick=${async () => { const p = await pickFolder(); if (p) updateNewFolderPath(p); }}
                                        class="px-2 py-1.5 bg-mitto-input border border-mitto-border rounded-lg text-gray-400 hover:text-white transition-colors"
                                        title="Browse"
                                        style="height: 38px; box-sizing: border-box"
                                      ><${FolderIcon} className="w-4 h-4" /></button>
                                    `}
                                  </div>
                                  ${isIncomplete && html`<p class="text-xs text-red-400 mt-1">Please select a folder for this workspace.</p>`}
                                `
                              : html`
                                  <input
                                    type="text"
                                    value=${firstWs.working_dir}
                                    readOnly
                                    class="w-full bg-mitto-input-box border border-mitto-border rounded-lg px-3 py-2 text-sm text-gray-500 cursor-default"
                                    style="height: 38px; box-sizing: border-box"
                                  />
                                `
                            }
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1">Display Name</label>
                            <input
                              type="text"
                              value=${editName}
                              onInput=${(e) => setEditName(e.target.value)}
                              placeholder=${getBasename(firstWs.working_dir)}
                              class="w-full bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                              style="height: 38px; box-sizing: border-box"
                            />
                          </div>
                          <div class="flex gap-4 items-end">
                            <div class="flex-1 min-w-0">
                              <label class="block text-sm text-gray-400 mb-1">Badge Code</label>
                              <input
                                type="text"
                                value=${editCode}
                                onInput=${(e) => setEditCode(e.target.value.toUpperCase().slice(0, 3))}
                                placeholder="Auto (3 letters max)"
                                maxlength="3"
                                class="w-full bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono uppercase"
                                style="height: 38px; box-sizing: border-box"
                              />
                            </div>
                            <div class="flex-shrink-0">
                              <label class="block text-sm text-gray-400 mb-1">Badge Color</label>
                              <div class="flex items-center gap-2">
                                <input
                                  type="color"
                                  value=${editColor}
                                  onInput=${(e) => setEditColor(e.target.value)}
                                  class="rounded cursor-pointer border border-mitto-border"
                                  style="width: 38px; height: 38px"
                                />
                                <span class="text-xs text-gray-500 font-mono">${editColor}</span>
                              </div>
                            </div>
                          </div>
                        </div>
                      `}

                      <!-- Folder Metadata tab -->
                      ${activeTab === "metadata" && html`
                        <div class="space-y-4">
                          <div>
                            <label class="block text-sm text-gray-400 mb-1">Description</label>
                            <textarea
                              value=${editMetaDescription}
                              onInput=${(e) => setEditMetaDescription(e.target.value)}
                              placeholder="A description of this workspace/project..."
                              rows="3"
                              class="w-full bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-vertical"
                            />
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1">URL</label>
                            <input
                              type="url"
                              value=${editMetaUrl}
                              onInput=${(e) => setEditMetaUrl(e.target.value)}
                              placeholder="https://github.com/..."
                              class="w-full bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                              style="height: 38px; box-sizing: border-box"
                            />
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1">Group</label>
                            <input
                              type="text"
                              value=${editMetaGroup}
                              onInput=${(e) => setEditMetaGroup(e.target.value)}
                              placeholder="e.g., CGW, Infrastructure, Frontend..."
                              class="w-full bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                              style="height: 38px; box-sizing: border-box"
                            />
                          </div>

                          <!-- User Data Schema -->
                          ${folderMetadata?.user_data_schema?.fields?.length > 0 && html`
                            <div class="mt-6 pt-4 border-t border-mitto-border">
                              <h3 class="text-sm font-medium text-gray-300 mb-3">User Data</h3>
                              <p class="text-xs text-gray-500 mb-3">
                                Custom data attributes defined in the workspace <code class="text-gray-400">.mittorc</code> file. These can be set on individual conversations.
                              </p>
                              <div class="border border-mitto-border rounded-lg overflow-hidden">
                                <table class="w-full text-sm">
                                  <thead>
                                    <tr class="bg-slate-800/50">
                                      <th class="text-left px-3 py-2 text-gray-400 font-medium border-b border-mitto-border">Name</th>
                                      <th class="text-left px-3 py-2 text-gray-400 font-medium border-b border-mitto-border">Data Type</th>
                                      <th class="text-left px-3 py-2 text-gray-400 font-medium border-b border-mitto-border">Description</th>
                                    </tr>
                                  </thead>
                                  <tbody>
                                    ${folderMetadata.user_data_schema.fields.map((field, i) => html`
                                      <tr key=${field.name} class="${i % 2 === 0 ? 'bg-slate-800/20' : ''}">
                                        <td class="px-3 py-2 text-gray-300 font-mono text-xs">${field.name}</td>
                                        <td class="px-3 py-2 text-gray-400">
                                          <span class="inline-block px-1.5 py-0.5 bg-slate-700 rounded text-xs font-mono">${field.type || 'string'}</span>
                                        </td>
                                        <td class="px-3 py-2 text-gray-500 text-xs">${field.description || '—'}</td>
                                      </tr>
                                    `)}
                                  </tbody>
                                </table>
                              </div>
                            </div>
                          `}
                        </div>
                      `}

                      <!-- Folder Prompts tab -->
                      ${activeTab === "prompts" && html`
                        <div class="space-y-4">
                          <div class="flex items-center justify-between">
                            <p class="text-sm text-gray-400">
                              Manage prompts for this workspace. Built-in prompts are read-only but can be disabled.
                            </p>
                            <button
                              onClick=${() => setShowAddPrompt(!showAddPrompt)}
                              class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${showAddPrompt ? 'bg-slate-700' : ''}"
                              title="Add Prompt"
                            >
                              <${PlusIcon} className="w-5 h-5" />
                            </button>
                          </div>

                          ${showAddPrompt && html`
                            <div class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3">
                              <div>
                                <label class="block text-sm text-gray-400 mb-1">Button Label</label>
                                <input type="text" value=${newPromptName} onInput=${(e) => setNewPromptName(e.target.value)}
                                  placeholder="e.g., Continue"
                                  class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                />
                              </div>
                              <div>
                                <label class="block text-sm text-gray-400 mb-1">Prompt Text</label>
                                <textarea value=${newPromptText} onInput=${(e) => setNewPromptText(e.target.value)}
                                  placeholder="e.g., Please continue with the current task."
                                  rows="8"
                                  class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-y"
                                />
                              </div>
                              <div>
                                <label class="block text-sm text-gray-400 mb-1">Group (optional)</label>
                                <input type="text" value=${newPromptGroup} onInput=${(e) => setNewPromptGroup(e.target.value)}
                                  placeholder="e.g., Tasks, Code Quality"
                                  class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                />
                              </div>
                              <div>
                                <label class="block text-sm text-gray-400 mb-1">Background Color (optional)</label>
                                <div class="flex items-center gap-2">
                                  <input type="color" value=${newPromptColor || '#334155'} onInput=${(e) => setNewPromptColor(e.target.value)}
                                    class="w-10 h-10 rounded cursor-pointer border border-slate-600"
                                  />
                                  <input type="text" value=${newPromptColor} onInput=${(e) => setNewPromptColor(e.target.value)}
                                    placeholder="#E8F5E9"
                                    class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                                  />
                                </div>
                              </div>
                              <div class="flex justify-end gap-2">
                                <button onClick=${() => { setShowAddPrompt(false); setNewPromptName(""); setNewPromptText(""); setNewPromptColor(""); setNewPromptGroup(""); }}
                                  class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors">Cancel</button>
                                <button onClick=${async () => {
                                    await saveWorkspacePrompt({ name: newPromptName.trim(), prompt: newPromptText.trim(), backgroundColor: newPromptColor || undefined, group: newPromptGroup.trim() || undefined, enabled: true });
                                    setShowAddPrompt(false); setNewPromptName(""); setNewPromptText(""); setNewPromptColor(""); setNewPromptGroup("");
                                  }}
                                  disabled=${!newPromptName.trim() || !newPromptText.trim() || promptSaving}
                                  class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50">
                                  ${promptSaving ? 'Saving...' : 'Add Prompt'}
                                </button>
                              </div>
                            </div>
                          `}

                          ${promptsLoading
                            ? html`<div class="flex items-center justify-center p-4"><${SpinnerIcon} className="w-5 h-5 animate-spin" /></div>`
                            : html`
                              <div class="space-y-2">
                                ${folderPrompts.length === 0
                                  ? html`<div class="p-4 text-center text-gray-500 text-sm">No prompts found. Click + to add a workspace prompt.</div>`
                                  : [...folderPrompts].sort((a, b) => (a.name || "").localeCompare(b.name || "")).map((prompt, idx) => {
                                      const isBuiltin = prompt.source === "builtin" || prompt.source === "file";
                                      const isEnabled = prompt.enabled !== false;
                                      return html`
                                        <div key=${prompt.name} class="p-3 bg-slate-700/20 rounded-lg border transition-all ${isEnabled ? 'border-slate-600/50' : 'border-slate-600/30 opacity-60'}">
                                          ${editingPromptIndex === idx
                                            ? html`
                                              <div class="space-y-3">
                                                <div>
                                                  <label class="block text-xs text-gray-400 mb-1">Button Label</label>
                                                  <input type="text" value=${isBuiltin ? prompt.name : editPromptName}
                                                    onInput=${(e) => !isBuiltin && setEditPromptName(e.target.value)}
                                                    disabled=${isBuiltin}
                                                    class="w-full px-2 py-1.5 bg-slate-700 rounded text-sm ${isBuiltin ? 'opacity-60 cursor-not-allowed' : 'focus:outline-none focus:ring-2 focus:ring-blue-500'}"
                                                  />
                                                </div>
                                                <div>
                                                  <label class="block text-xs text-gray-400 mb-1">Prompt Text</label>
                                                  <textarea rows="8"
                                                    value=${isBuiltin ? prompt.prompt : editPromptText}
                                                    onInput=${(e) => !isBuiltin && setEditPromptText(e.target.value)}
                                                    disabled=${isBuiltin}
                                                    class="w-full px-2 py-1.5 bg-slate-700 rounded text-sm resize-y ${isBuiltin ? 'opacity-60 cursor-not-allowed' : 'focus:outline-none focus:ring-2 focus:ring-blue-500'}"
                                                  />
                                                </div>
                                                <div>
                                                  <label class="block text-xs text-gray-400 mb-1">Group (optional)</label>
                                                  <input type="text" value=${isBuiltin ? (prompt.group || '') : editPromptGroup}
                                                    onInput=${(e) => !isBuiltin && setEditPromptGroup(e.target.value)}
                                                    disabled=${isBuiltin}
                                                    placeholder="e.g., Tasks, Code Quality"
                                                    class="w-full px-2 py-1.5 bg-slate-700 rounded text-sm ${isBuiltin ? 'opacity-60 cursor-not-allowed' : 'focus:outline-none focus:ring-2 focus:ring-blue-500'}"
                                                  />
                                                </div>
                                                ${!isBuiltin && html`
                                                  <div>
                                                    <label class="block text-xs text-gray-400 mb-1">Background Color (optional)</label>
                                                    <div class="flex items-center gap-2">
                                                      <input type="color" value=${editPromptColor || '#334155'}
                                                        onInput=${(e) => setEditPromptColor(e.target.value)}
                                                        class="w-8 h-8 rounded cursor-pointer border border-slate-600"
                                                      />
                                                      <input type="text" value=${editPromptColor}
                                                        onInput=${(e) => setEditPromptColor(e.target.value)}
                                                        placeholder="#E8F5E9"
                                                        class="flex-1 px-2 py-1.5 bg-slate-700 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                                                      />
                                                    </div>
                                                  </div>
                                                `}
                                                <div class="flex justify-end gap-2">
                                                  <button onClick=${() => setEditingPromptIndex(null)}
                                                    class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors">
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
                                                      class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50">
                                                      ${promptSaving ? 'Saving...' : 'Save'}
                                                    </button>
                                                  `}
                                                </div>
                                              </div>
                                            `
                                            : html`
                                              <div class="flex items-center gap-3">
                                                <input type="checkbox" checked=${isEnabled}
                                                  onChange=${() => togglePromptEnabled(prompt)}
                                                  class="rounded border-slate-600 text-blue-500 focus:ring-blue-500 flex-shrink-0"
                                                  title=${isEnabled ? "Disable this prompt" : "Enable this prompt"}
                                                />
                                                ${prompt.backgroundColor && html`
                                                  <div class="w-5 h-5 rounded-sm flex-shrink-0 border border-slate-600" style="background-color: ${prompt.backgroundColor}" />
                                                `}
                                                <div class="flex-1 min-w-0">
                                                  <div class="flex items-center gap-2">
                                                    <span class="text-sm font-medium ${isEnabled ? 'text-blue-400' : 'text-gray-500'}">${prompt.name}</span>
                                                    <span class="text-xs px-1.5 py-0.5 rounded ${isBuiltin ? 'bg-blue-500/20 text-blue-400' : 'bg-green-500/20 text-green-400'}">
                                                      ${isBuiltin ? 'built-in' : 'workspace'}
                                                    </span>
                                                  </div>
                                                  ${prompt.description && html`<p class="text-xs text-gray-500 mt-0.5 truncate">${prompt.description}</p>`}
                                                  ${!prompt.description && prompt.prompt && html`<p class="text-xs text-gray-500 mt-0.5 truncate">${prompt.prompt.slice(0, 80)}${prompt.prompt.length > 80 ? '...' : ''}</p>`}
                                                </div>
                                                <div class="flex items-center gap-1 flex-shrink-0">
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
                                                    class="p-1.5 hover:bg-slate-700 rounded transition-colors" title=${isBuiltin ? "View" : "Edit"}>
                                                    <${EditIcon} className="w-4 h-4 ${isBuiltin ? 'text-gray-500' : 'text-gray-400'}" />
                                                  </button>
                                                  ${!isBuiltin && html`
                                                    <button onClick=${() => deleteWorkspacePrompt(prompt.name)}
                                                      class="p-1.5 hover:bg-red-500/20 rounded transition-colors" title="Delete">
                                                      <${TrashIcon} className="w-4 h-4 text-gray-400 hover:text-red-400" />
                                                    </button>
                                                  `}
                                                </div>
                                              </div>
                                            `}
                                        </div>
                                      `;
                                    })
                                }
                              </div>
                            `
                          }
                        </div>
                      `}

                      <!-- Folder Processors tab -->
                      ${activeTab === "processors" && html`
                        <div class="space-y-4">
                          <p class="text-sm text-gray-400">
                            Manage processors for this workspace. Global processors can be disabled per workspace.
                          </p>

                          ${processorsLoading
                            ? html`<div class="flex items-center justify-center p-4"><${SpinnerIcon} className="w-5 h-5 animate-spin" /></div>`
                            : html`
                              <div class="space-y-2">
                                ${folderProcessors.length === 0
                                  ? html`<div class="p-4 text-center text-gray-500 text-sm">No processors found for this workspace.</div>`
                                  : folderProcessors.map((proc) => {
                                      const isWorkspace = proc.source === "workspace";
                                      const isEnabled = proc.enabled !== false;
                                      const isPromptMode = proc.mode === "prompt";
                                      const sourceLabel = isWorkspace ? "workspace" : (proc.source === "builtin" ? "built-in" : "global");
                                      const sourceBadgeClass = isWorkspace
                                        ? "bg-green-500/20 text-green-400"
                                        : (proc.source === "builtin" ? "bg-blue-500/20 text-blue-400" : "bg-orange-500/20 text-orange-400");
                                      const borderClass = isPromptMode
                                        ? "border-purple-500/30"
                                        : (isEnabled ? "border-slate-600/50" : "border-slate-600/30 opacity-60");
                                      return html`
                                        <div key=${proc.name} class="p-3 bg-slate-700/20 rounded-lg border transition-all ${borderClass} ${!isEnabled && !isPromptMode ? 'opacity-60' : ''}">
                                          <div class="flex items-center gap-3">
                                            <input type="checkbox" checked=${isEnabled}
                                              onChange=${() => toggleProcessorEnabled(proc)}
                                              class="rounded border-slate-600 text-blue-500 focus:ring-blue-500 flex-shrink-0"
                                              title=${isEnabled ? "Disable this processor" : "Enable this processor"}
                                            />
                                            <div class="flex-1 min-w-0">
                                              <div class="flex items-center gap-2">
                                                ${isPromptMode && html`<${RobotIcon} className="w-4 h-4 text-purple-400 flex-shrink-0" />`}
                                                <span class="text-sm font-medium font-mono ${isEnabled ? 'text-blue-400' : 'text-gray-500'}">${proc.name}</span>
                                                <span class="text-xs px-1.5 py-0.5 rounded ${sourceBadgeClass}">${sourceLabel}</span>
                                                ${isPromptMode && html`<span class="text-xs px-1.5 py-0.5 rounded bg-purple-500/20 text-purple-400">prompt</span>`}
                                                ${proc.when && html`<span class="text-xs text-gray-500">when: ${proc.when}</span>`}
                                              </div>
                                              ${proc.description && html`<p class="text-xs text-gray-500 mt-0.5 truncate">${proc.description}</p>`}
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
                          <p class="text-sm text-gray-400">Configure automatic child conversations for this folder.</p>
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
                ? html`<div class="flex flex-col items-center justify-center h-full text-gray-500 text-sm gap-3 px-8 text-center">
                    ${workspaces.length === 0
                      ? html`
                        <${FolderIcon} className="w-10 h-10 opacity-30" />
                        <p class="text-base font-medium text-gray-400">No workspaces configured</p>
                        <p>Add a workspace to specify a folder where an ACP server will operate.</p>
                        <p class="text-xs">Click the <span class="inline-flex items-center gap-1 text-gray-400"><${FolderIcon} className="w-3.5 h-3.5" /> folder</span> button below to get started.</p>
                      `
                      : html`<p>Select a workspace to edit</p>`
                    }
                  </div>`
                : html`
                <!-- Workspace tab bar -->
                <div class="flex border-b border-mitto-border px-4 flex-shrink-0">
                  ${workspaceTabs.map((tab) => html`
                    <button
                      key=${tab.id}
                      onClick=${() => setActiveTab(tab.id)}
                      class="px-4 py-2.5 text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${activeTab === tab.id ? "border-blue-500 text-blue-400" : "border-transparent text-gray-500 hover:text-gray-300"}"
                      style="margin-bottom: -1px"
                    >${tab.label}</button>
                  `)}
                </div>

                <!-- Workspace tab content -->
                <div class="flex-1 overflow-y-auto p-6">

                  <!-- Workspace General tab -->
                  ${activeTab === "general" && html`
                    <div class="space-y-4">
                      <div>
                        <label class="block text-sm text-gray-400 mb-1">ACP Server</label>
                        <select
                          value=${editAcpServer}
                          onChange=${(e) => setEditAcpServer(e.target.value)}
                          class="w-full bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                          style="height: 38px; box-sizing: border-box"
                        >
                          ${sortedAcpServers.map((s) => html`<option key=${s.name} value=${s.name}>${s.name}</option>`)}
                        </select>
                      </div>
                      <div>
                        <label class="block text-sm text-gray-400 mb-1">Auxiliary ACP Server (optional)</label>
                        <select
                          value=${editAuxAcpServer}
                          onChange=${(e) => setEditAuxAcpServer(e.target.value)}
                          class="w-full bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                          style="height: 38px; box-sizing: border-box"
                        >
                          <option value="">None</option>
                          ${sortedAcpServers.map((s) => html`<option key=${s.name} value=${s.name}>${s.name}</option>`)}
                        </select>
                      </div>
                      <label class="flex items-center gap-3 cursor-pointer">
                        <input
                          type="checkbox"
                          checked=${editAutoApprove}
                          onChange=${(e) => setEditAutoApprove(e.target.checked)}
                          class="rounded border-mitto-border text-blue-500 focus:ring-blue-500"
                        />
                        <span class="text-sm">Auto-approve tool calls</span>
                      </label>
                    </div>
                  `}

                  <!-- Workspace Runner tab -->
                  ${activeTab === "runner" && html`
                    <div class="space-y-5">
                      <div>
                        <label class="block text-sm text-gray-400 mb-3">Runner Type</label>
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
                                class="text-blue-500"
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
                </div>
              `}
          </div>
        </div>

        <!-- Footer -->
        <div class="flex items-center justify-between p-4 border-t border-mitto-border flex-shrink-0">
          <div class="flex-1 mr-4">
            ${orphanedWorkspaces.length > 0 && html`
              <p class="text-xs text-yellow-400">⚠ ${orphanedWorkspaces.length} workspace(s) hidden: missing ACP server</p>
            `}
            ${error && html`<p class="text-xs text-red-400">${error}</p>`}
          </div>
          <div class="flex gap-2">
            <button onClick=${handleClose} class="px-4 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors">Cancel</button>
            <button
              onClick=${handleSave}
              disabled=${saving || loading}
              class="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg transition-colors flex items-center gap-2"
            >
              ${saving && html`<${SpinnerIcon} className="w-4 h-4" />`}
              Save
            </button>
          </div>
        </div>
      </div>
    </div>

    <${ConfirmDialog}
      isOpen=${!!confirmDialog}
      title=${confirmDialog?.title || "Confirm"}
      message=${confirmDialog?.message || ""}
      confirmLabel=${confirmDialog?.confirmLabel || "Yes"}
      cancelLabel=${confirmDialog?.cancelLabel || "Cancel"}
      confirmVariant=${confirmDialog?.confirmVariant || "primary"}
      onConfirm=${confirmDialog?.onConfirm}
      onCancel=${() => setConfirmDialog(null)}
    />
  `;
}
