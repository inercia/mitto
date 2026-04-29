// Mitto Web Interface - Settings Dialog Component
const { useState, useEffect, useMemo, html } = window.preact;

// Import utilities
import {
  secureFetch,
  apiUrl,
  hasNativeFolderPicker,
  pickFolder,
  openExternalURL,
  fetchConfig,
  invalidateConfigCache,
} from "../utils/index.js";
import { setPromptSortMode as savePromptSortMode } from "../utils/storage.js";

// Import shared library functions
import {
  validateUsername,
  validatePassword,
  getWorkspaceVisualInfo,
  getBasename,
} from "../lib.js";

// Import components
import {
  SpinnerIcon,
  CloseIcon,
  SettingsIcon,
  PlusIcon,
  TrashIcon,
  EditIcon,
  ServerEmptyIcon,
  ServerIcon,
  FolderIcon,
  LightningIcon,
  DragHandleIcon,
  LockIcon,
  GlobeIcon,
  SlidersIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  DuplicateIcon,
  ShieldIcon,
  SearchIcon,
} from "./Icons.js";
import { AgentDiscoveryDialog } from "./AgentDiscoveryDialog.js";

// Import constants
import { CYCLING_MODE, CYCLING_MODE_OPTIONS } from "../constants.js";

// Import WorkspaceBadge from app.js - we'll need to pass it as a prop or extract it
// For now, we'll receive it as a prop

/**
 * FolderListEditor — reusable folder list editing component with append/replace modes.
 *
 * In "append" mode: inherited folders are shown dimmed at the top (read-only),
 * then workspace-level folders below with [×] buttons.
 * In "replace" mode: only workspace folders are shown (inherited are hidden).
 */
export function FolderListEditor({
  folders,
  inheritedFolders,
  mode,
  onModeChange,
  onFoldersChange,
  placeholder,
  label,
}) {
  const addFolder = () => onFoldersChange([...(folders || []), ""]);
  const removeFolder = (idx) =>
    onFoldersChange((folders || []).filter((_, i) => i !== idx));
  const updateFolder = (idx, val) => {
    const updated = [...(folders || [])];
    updated[idx] = val;
    onFoldersChange(updated);
  };

  return html`
    <div class="space-y-1">
      <div class="flex items-center gap-2 mb-1">
        <span class="text-sm font-medium text-gray-300 flex-1">${label}</span>
        <select
          value=${mode}
          onChange=${(e) => onModeChange(e.target.value)}
          class="px-2 py-0.5 bg-slate-700 rounded text-xs focus:outline-none focus:ring-2 focus:ring-blue-500"
        >
          <option value="append">Append</option>
          <option value="replace">Replace</option>
        </select>
      </div>

      ${mode === "append" &&
      (inheritedFolders || []).length > 0 &&
      html`
        <div class="space-y-1 opacity-50 pb-1 border-b border-slate-600/40">
          ${(inheritedFolders || []).map(
            (f, idx) => html`
              <div key=${"inh-" + idx} class="flex items-center gap-2">
                <input
                  type="text"
                  value=${f}
                  disabled
                  class="flex-1 px-3 py-1.5 bg-slate-800 rounded text-sm font-mono cursor-not-allowed"
                />
              </div>
            `,
          )}
        </div>
      `}

      ${mode === "replace" &&
      html`
        <p class="text-xs text-amber-400/80 mb-1">
          Replaces all inherited folders
        </p>
      `}

      <div class="space-y-1">
        ${(folders || []).map(
          (f, idx) => html`
            <div key=${idx} class="flex items-center gap-2">
              <input
                type="text"
                value=${f}
                onInput=${(e) => updateFolder(idx, e.target.value)}
                placeholder=${placeholder || "$MITTO_WORKING_DIR"}
                class="flex-1 px-3 py-1.5 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <button
                type="button"
                onClick=${() => removeFolder(idx)}
                class="p-1 text-gray-400 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors"
                title="Remove folder"
              >
                <${TrashIcon} className="w-4 h-4" />
              </button>
            </div>
          `,
        )}
        <button
          type="button"
          onClick=${addFolder}
          class="flex items-center gap-1 px-2 py-1 text-xs text-blue-400 hover:text-blue-300 hover:bg-blue-500/10 rounded transition-colors"
        >
          <${PlusIcon} className="w-3 h-3" />
          Add folder
        </button>
      </div>
    </div>
  `;
}

/**
 * AutoChildrenEditor — edit list of auto-created child conversations.
 * When a new top-level conversation is created in this workspace,
 * these child conversations will be auto-created with it.
 */
export function AutoChildrenEditor({
  children,
  workspaces,
  currentWorkspaceUUID,
  onChange,
  getBasename,
}) {
  const addChild = () =>
    onChange([...(children || []), { title: "", target_workspace_uuid: "" }]);
  const removeChild = (idx) =>
    onChange((children || []).filter((_, i) => i !== idx));
  const updateChild = (idx, field, value) => {
    const updated = [...(children || [])];
    updated[idx] = { ...updated[idx], [field]: value };
    onChange(updated);
  };

  // Filter out current workspace (can't be its own child) and show only workspaces with same working_dir
  const currentWs = workspaces.find((ws) => ws.uuid === currentWorkspaceUUID);
  const targetOptions = workspaces.filter(
    (ws) =>
      ws.uuid !== currentWorkspaceUUID &&
      currentWs &&
      ws.working_dir === currentWs.working_dir,
  );

  const maxChildren = 5;
  const canAdd = (children || []).length < maxChildren;

  return html`
    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <label class="text-sm text-gray-400">Auto-Create Children</label>
        ${canAdd
          ? html`
              <button
                type="button"
                onClick=${addChild}
                class="text-xs px-2 py-1 bg-mitto-input-box hover:bg-blue-600 hover:text-white border border-mitto-border rounded-lg transition-colors"
              >
                + Add Child
              </button>
            `
          : html`
              <span class="text-xs text-gray-500">Max ${maxChildren} children</span>
            `}
      </div>
      <p class="text-xs text-gray-500">
        These conversations are auto-created when a new top-level conversation
        starts. They are deleted when the parent is deleted.
      </p>
      ${(children || []).length === 0
        ? html`
            <div class="text-xs text-gray-500 italic py-2">
              No auto-children configured.
            </div>
          `
        : html`
            <div class="space-y-2">
              ${(children || []).map(
                (child, idx) => html`
                  <div
                    key=${idx}
                    class="flex items-center gap-2 p-2 bg-mitto-input-box rounded-lg border border-mitto-border"
                  >
                    <input
                      type="text"
                      value=${child.title || ""}
                      placeholder="Child title"
                      onInput=${(e) => updateChild(idx, "title", e.target.value)}
                      class="flex-1 bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      style="height: 38px; box-sizing: border-box"
                    />
                    <select
                      value=${child.target_workspace_uuid || ""}
                      onChange=${(e) =>
                        updateChild(
                          idx,
                          "target_workspace_uuid",
                          e.target.value,
                        )}
                      class="bg-mitto-input border border-mitto-border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      style="height: 38px; box-sizing: border-box"
                    >
                      ${targetOptions.map(
                        (ws) => html`
                          <option value=${ws.uuid}>
                            ${ws.name || ws.acp_server}
                            (${getBasename(ws.working_dir)})
                          </option>
                        `,
                      )}
                    </select>
                    <button
                      type="button"
                      onClick=${() => removeChild(idx)}
                      class="p-1.5 text-gray-400 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors"
                      title="Remove child"
                    >
                      <${TrashIcon} className="w-4 h-4" />
                    </button>
                  </div>
                `,
              )}
            </div>
          `}
    </div>
  `;
}

/**
 * RunnerRestrictionsEditor — per-workspace runner restriction overrides.
 * Shown when the workspace runner is not "exec".
 */
export function RunnerRestrictionsEditor({
  runnerType,
  config: runnerConfig,
  effectiveConfig,
  onChange,
}) {
  const [expanded, setExpanded] = useState(false);

  // Networking override
  const overrideNetworking =
    runnerConfig?.restrictions?.allow_networking != null;
  const inheritedNetworking =
    effectiveConfig?.restrictions?.allow_networking !== false; // default true

  // Folder modes — both derive from the single merge_strategy field, so
  // changing one must sync the other to keep UI consistent with the model.
  const derivedMode =
    runnerConfig?.merge_strategy === "replace" ? "replace" : "append";
  const [readMode, setReadMode] = useState(derivedMode);
  const [writeMode, setWriteMode] = useState(derivedMode);

  const updateMergeMode = (mode, setThisMode, setOtherMode) => {
    setThisMode(mode);
    setOtherMode(mode); // keep in sync — single merge_strategy backs both
    const newConfig = {
      ...(runnerConfig || {}),
      merge_strategy: mode === "replace" ? "replace" : "extend",
    };
    onChange(newConfig);
  };

  // Helper: update a restriction field
  const updateRestriction = (field, value) => {
    const newConfig = {
      ...(runnerConfig || {}),
      restrictions: {
        ...(runnerConfig?.restrictions || {}),
        [field]: value,
      },
    };
    onChange(newConfig);
  };

  // Helper: update docker field
  const updateDocker = (field, value) => {
    const newConfig = {
      ...(runnerConfig || {}),
      restrictions: {
        ...(runnerConfig?.restrictions || {}),
        docker: {
          ...(runnerConfig?.restrictions?.docker || {}),
          [field]: value,
        },
      },
    };
    onChange(newConfig);
  };

  const handleNetworkingOverride = (checked) => {
    if (checked) {
      updateRestriction("allow_networking", inheritedNetworking);
    } else {
      // Remove override
      const newConfig = {
        ...(runnerConfig || {}),
        restrictions: { ...(runnerConfig?.restrictions || {}) },
      };
      delete newConfig.restrictions.allow_networking;
      onChange(newConfig);
    }
  };

  const hasConfig =
    runnerConfig &&
    (runnerConfig.restrictions?.allow_networking != null ||
      (runnerConfig.restrictions?.allow_read_folders || []).length > 0 ||
      (runnerConfig.restrictions?.allow_write_folders || []).length > 0 ||
      runnerConfig.restrictions?.docker);

  return html`
    <div class="border border-slate-600/50 rounded-lg overflow-hidden mt-2">
      <button
        type="button"
        onClick=${() => setExpanded(!expanded)}
        class="w-full flex items-center justify-between p-3 bg-slate-700/30 hover:bg-slate-700/50 transition-colors"
      >
        <div class="flex items-center gap-2">
          <${expanded ? ChevronDownIcon : ChevronRightIcon}
            className="w-4 h-4 text-gray-400"
          />
          <span class="text-sm font-medium">Runner Restrictions</span>
        </div>
        ${hasConfig &&
        html`
          <span
            class="px-2 py-0.5 bg-blue-500/20 text-blue-400 rounded text-xs"
          >
            Configured
          </span>
        `}
      </button>

      ${expanded &&
      html`
        <div class="p-4 space-y-4 border-t border-slate-600/50">
          <p class="text-xs text-gray-500">
            Override inherited restrictions from global/agent config.
            ${effectiveConfig
              ? ""
              : " Loading inherited values..."}
          </p>

          <!-- Networking -->
          <div class="space-y-1">
            <div class="flex items-center gap-3">
              <input
                type="checkbox"
                id="override-networking"
                checked=${overrideNetworking}
                onChange=${(e) => handleNetworkingOverride(e.target.checked)}
                class="w-4 h-4 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
              />
              <label for="override-networking" class="text-sm font-medium"
                >Override networking</label
              >
            </div>
            ${overrideNetworking
              ? html`
                  <label class="flex items-center gap-3 ml-6 cursor-pointer">
                    <input
                      type="checkbox"
                      checked=${runnerConfig?.restrictions?.allow_networking !==
                      false}
                      onChange=${(e) =>
                        updateRestriction(
                          "allow_networking",
                          e.target.checked,
                        )}
                      class="w-4 h-4 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                    />
                    <span class="text-sm">Allow networking</span>
                  </label>
                `
              : html`
                  <p class="text-xs text-gray-500 ml-6">
                    Inherited:
                    ${inheritedNetworking ? "allowed" : "blocked"}
                  </p>
                `}
          </div>

          <!-- Read folders -->
          <${FolderListEditor}
            label="Allow read folders"
            folders=${runnerConfig?.restrictions?.allow_read_folders || []}
            inheritedFolders=${effectiveConfig?.restrictions
              ?.allow_read_folders || []}
            mode=${readMode}
            onModeChange=${(m) =>
              updateMergeMode(m, setReadMode, setWriteMode)}
            onFoldersChange=${(folders) =>
              updateRestriction("allow_read_folders", folders)}
            placeholder="$MITTO_WORKING_DIR"
          />

          <!-- Write folders -->
          <${FolderListEditor}
            label="Allow write folders"
            folders=${runnerConfig?.restrictions?.allow_write_folders || []}
            inheritedFolders=${effectiveConfig?.restrictions
              ?.allow_write_folders || []}
            mode=${writeMode}
            onModeChange=${(m) =>
              updateMergeMode(m, setWriteMode, setReadMode)}
            onFoldersChange=${(folders) =>
              updateRestriction("allow_write_folders", folders)}
            placeholder="$MITTO_WORKING_DIR"
          />

          ${runnerType === "docker" &&
          html`
            <div class="space-y-2 pt-2 border-t border-slate-600/50">
              <label class="text-sm font-medium text-gray-300"
                >Docker Settings</label
              >
              <div class="grid grid-cols-3 gap-3">
                <div>
                  <label class="text-xs text-gray-500">Image</label>
                  <input
                    type="text"
                    value=${runnerConfig?.restrictions?.docker?.image || ""}
                    onInput=${(e) => updateDocker("image", e.target.value)}
                    class="w-full px-2 py-1 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                    placeholder="alpine:latest"
                  />
                </div>
                <div>
                  <label class="text-xs text-gray-500">Memory Limit</label>
                  <input
                    type="text"
                    value=${runnerConfig?.restrictions?.docker?.memory_limit ||
                    ""}
                    onInput=${(e) =>
                      updateDocker("memory_limit", e.target.value)}
                    class="w-full px-2 py-1 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                    placeholder="4g"
                  />
                </div>
                <div>
                  <label class="text-xs text-gray-500">CPU Limit</label>
                  <input
                    type="text"
                    value=${runnerConfig?.restrictions?.docker?.cpu_limit || ""}
                    onInput=${(e) => updateDocker("cpu_limit", e.target.value)}
                    class="w-full px-2 py-1 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                    placeholder="2.0"
                  />
                </div>
              </div>
            </div>
          `}

          <!-- Clear button -->
          <div class="flex justify-end pt-2 border-t border-slate-600/50">
            <button
              type="button"
              onClick=${() => onChange(null)}
              class="px-3 py-1.5 text-xs text-gray-400 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors"
            >
              Clear Restrictions
            </button>
          </div>
        </div>
      `}
    </div>
  `;
}

/**
 * Helper component for editing a workspace inline (accordion-style)
 */
function WorkspaceEditForm({
  workspace,
  acpServers,
  allWorkspaces,
  supportedRunners,
  getWorkspaceVisualInfo,
  getBasename,
  onSave,
  onCancel,
}) {
  const [name, setName] = useState(workspace.name || "");
  const [code, setCode] = useState(workspace.code || "");
  const [color, setColor] = useState(
    workspace.color ||
      getWorkspaceVisualInfo(workspace.working_dir).color.backgroundHex ||
      "#808080",
  );
  const [acpServer, setAcpServer] = useState(workspace.acp_server);
  const [auxiliaryAcpServer, setAuxiliaryAcpServer] = useState(
    workspace.auxiliary_acp_server || "",
  );
  const [runner, setRunner] = useState(workspace.restricted_runner || "exec");
  const [autoApprove, setAutoApprove] = useState(
    workspace.auto_approve === true,
  );
  const [autoChildren, setAutoChildren] = useState(
    workspace.auto_children || [],
  );
  // Per-workspace runner restriction overrides (null = no overrides)
  const [runnerConfig, setRunnerConfig] = useState(
    workspace.restricted_runner_config || null,
  );
  // Effective config from global+agent levels (for showing inherited values)
  const [effectiveConfig, setEffectiveConfig] = useState(null);

  // Fetch effective runner config when workspace UUID changes or on mount
  useEffect(() => {
    if (!workspace.uuid) return;
    secureFetch(apiUrl(`/api/workspaces/${workspace.uuid}/effective-runner-config`))
      .then((r) => r.json())
      .then((data) => setEffectiveConfig(data))
      .catch(() => {}); // Ignore errors — effective config is optional for display
  }, [workspace.uuid]);

  // When runner type switches to "exec", clear runner config
  const handleRunnerChange = (newRunner) => {
    setRunner(newRunner);
    if (newRunner === "exec") {
      setRunnerConfig(null);
    } else if (!runnerConfig) {
      // Pre-populate write folders with $MITTO_WORKING_DIR when first enabling restrictions
      setRunnerConfig({
        restrictions: { allow_write_folders: ["$MITTO_WORKING_DIR"] },
      });
    }
  };

  // Sort ACP servers alphabetically by name for display
  const sortedServers = useMemo(
    () => [...acpServers].sort((a, b) => a.name.localeCompare(b.name)),
    [acpServers],
  );

  const handleSave = () => {
    // Ensure code is uppercase and max 3 characters
    const sanitizedCode = (code || "").toUpperCase().slice(0, 3);
    onSave({
      name: name || undefined,
      code: sanitizedCode || undefined,
      color: color || undefined,
      acp_server: acpServer,
      auxiliary_acp_server: auxiliaryAcpServer,
      restricted_runner: runner,
      // Only include runner config for non-exec runners
      restricted_runner_config: runner !== "exec" ? runnerConfig : undefined,
      auto_approve: autoApprove || undefined, // undefined to omit if false
      auto_children: autoChildren.length > 0 ? autoChildren : undefined,
    });
  };

  return html`
    <div class="space-y-3 mt-3 pt-3 border-t border-slate-600/50">
      <!-- Friendly Name -->
      <div>
        <label class="block text-sm text-gray-400 mb-1">Display Name</label>
        <input
          type="text"
          value=${name}
          onInput=${(e) => setName(e.target.value)}
          placeholder=${getBasename(workspace.working_dir)}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
        <p class="text-xs text-gray-500 mt-1">
          Optional friendly name shown in the UI
        </p>
      </div>

      <!-- ACP Server Selection -->
      <div>
        <label class="block text-sm text-gray-400 mb-1">ACP Server</label>
        <select
          value=${acpServer}
          onChange=${(e) => setAcpServer(e.target.value)}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        >
          ${sortedServers.map(
            (srv) => html`
              <option key=${srv.name} value=${srv.name}>${srv.name}</option>
            `,
          )}
        </select>
      </div>

      <!-- Auxiliary ACP Server Selection -->
      <div>
        <label class="block text-sm text-gray-400 mb-1"
          >Auxiliary ACP Server</label
        >
        <select
          value=${auxiliaryAcpServer}
          onChange=${(e) => setAuxiliaryAcpServer(e.target.value)}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        >
          <option value="">Default (shared with main)</option>
          <option value="none">None (disabled)</option>
          ${sortedServers.map(
            (srv) => html`
              <option key=${"aux-" + srv.name} value=${srv.name}>
                ${srv.name}
              </option>
            `,
          )}
        </select>
        <p class="text-xs text-gray-500 mt-1">
          Dedicated server for background tasks (titles, suggestions)
        </p>
      </div>

      <!-- Sandbox Type -->
      <div>
        <label class="block text-sm text-gray-400 mb-1">Sandbox Type</label>
        <select
          value=${runner}
          onChange=${(e) => handleRunnerChange(e.target.value)}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        >
          ${supportedRunners
            .filter((r) => r.supported)
            .map(
              (r) => html`
                <option key=${r.type} value=${r.type}>${r.label}</option>
              `,
            )}
        </select>
        <p class="text-xs text-gray-500 mt-1">
          Controls how the agent is sandboxed
        </p>

        ${runner !== "exec" &&
        html`
          <${RunnerRestrictionsEditor}
            runnerType=${runner}
            config=${runnerConfig}
            effectiveConfig=${effectiveConfig}
            onChange=${setRunnerConfig}
          />
        `}
      </div>

      <!-- Auto-approve Permissions -->
      <label
        class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
      >
        <input
          type="checkbox"
          checked=${autoApprove}
          onChange=${(e) => setAutoApprove(e.target.checked)}
          class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
        />
        <div class="flex-1">
          <div class="font-medium text-sm">Auto-approve Permissions</div>
          <div class="text-xs text-gray-500">
            Automatically approve all permission requests from the agent for
            sessions in this workspace
          </div>
        </div>
      </label>

      <!-- Badge Customization -->
      <div>
        <label class="block text-sm text-gray-400 mb-1"
          >Badge Customization</label
        >
        <div class="flex items-center gap-3">
          <input
            type="text"
            value=${code}
            onInput=${(e) => setCode(e.target.value.toUpperCase().slice(0, 3))}
            placeholder=${getWorkspaceVisualInfo(workspace.working_dir)
              .abbreviation}
            maxlength="3"
            class="w-20 px-3 py-2 bg-slate-700 rounded-lg text-sm text-center uppercase focus:outline-none focus:ring-2 focus:ring-blue-500"
            title="Three-letter code"
          />
          <input
            type="color"
            value=${color}
            onChange=${(e) => setColor(e.target.value)}
            class="w-10 h-10 rounded cursor-pointer border border-slate-600"
            title="Badge color"
          />
          <span class="text-xs text-gray-500">Code and color for badge</span>
        </div>
      </div>

      <!-- Auto-Children Section -->
      <div class="pt-3 border-t border-slate-600/30">
        <${AutoChildrenEditor}
          children=${autoChildren}
          workspaces=${allWorkspaces}
          currentWorkspaceUUID=${workspace.uuid}
          onChange=${setAutoChildren}
          getBasename=${getBasename}
        />
      </div>

      <!-- Actions -->
      <div class="flex justify-end gap-2 pt-2">
        <button
          onClick=${onCancel}
          class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
        >
          Cancel
        </button>
        <button
          onClick=${handleSave}
          class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
        >
          Save
        </button>
      </div>
    </div>
  `;
}

/**
 * Helper component for editing a server inline
 * Server-specific prompts are read-only (managed via prompt files with acps: field)
 */
function ServerEditForm({ server, agentTypes = [], onSave, onCancel }) {
  const [name, setName] = useState(server.name);
  const [command, setCommand] = useState(server.command);
  const [type, setType] = useState(server.type || "");
  const [autoApprove, setAutoApprove] = useState(server.auto_approve === true);
  const [tags, setTags] = useState(
    server.tags ? server.tags.join(", ") : "",
  );
  // Environment variables as array of {key, value} for easier editing
  const [envVars, setEnvVars] = useState(() => {
    const env = server.env || {};
    return Object.entries(env).map(([key, value]) => ({ key, value }));
  });
  // All prompts are now file-based (read-only)
  const filePrompts = server.prompts || [];

  const [typeError, setTypeError] = useState(false);
  const [formError, setFormError] = useState("");

  const handleSave = () => {
    setFormError("");
    if (!name.trim()) {
      setFormError("Server name cannot be empty");
      return;
    }
    if (!command.trim()) {
      setFormError("Server command cannot be empty");
      return;
    }
    if (!type.trim()) {
      setTypeError(true);
      setFormError("Please select an agent type");
      return;
    }
    setTypeError(false);
    // Convert envVars array back to object, filtering out empty keys
    const envObj = {};
    envVars.forEach(({ key, value }) => {
      if (key && key.trim()) {
        envObj[key.trim()] = value || "";
      }
    });
    // Parse tags: split by comma, trim whitespace, filter empty strings
    const parsedTags = tags
      .split(",")
      .map((t) => t.trim())
      .filter((t) => t.length > 0);
    const err = onSave(name, command, type, autoApprove, envObj, parsedTags);
    if (err) {
      setFormError(err);
    }
  };

  const addEnvVar = () => {
    setEnvVars([...envVars, { key: "", value: "" }]);
  };

  const removeEnvVar = (index) => {
    setEnvVars(envVars.filter((_, i) => i !== index));
  };

  const updateEnvVar = (index, field, value) => {
    const updated = [...envVars];
    updated[index] = { ...updated[index], [field]: value };
    setEnvVars(updated);
  };

  return html`
    <div class="space-y-3">
      <div>
        <label class="block text-sm text-gray-400 mb-1">Server Name</label>
        <input
          type="text"
          value=${name}
          onInput=${(e) => setName(e.target.value)}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1">Command</label>
        <input
          type="text"
          value=${command}
          onInput=${(e) => setCommand(e.target.value)}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1"
          >Type
          <span class="text-xs text-red-400 ml-1">*</span></label
        >
        <select
          value=${type}
          onChange=${(e) => {
            setType(e.target.value);
            if (e.target.value) setTypeError(false);
          }}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 ${typeError ? "ring-2 ring-red-500" : "focus:ring-blue-500"}"
        >
          <option value="">-- Select agent type --</option>
          ${agentTypes.map(
            (t) => html`<option key=${t} value=${t}>${t}</option>`,
          )}
        </select>
        ${typeError && html`<p class="text-xs text-red-400 mt-1">
          Agent type is required.
        </p>`}
        <p class="text-xs text-gray-500 mt-1">
          Servers with the same type share prompts and agent configuration.
        </p>
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1"
          >Tags
          <span class="text-xs text-gray-500"
            >(optional, for categorization)</span
          ></label
        >
        <input
          type="text"
          value=${tags}
          onInput=${(e) => setTags(e.target.value)}
          placeholder="e.g., coding, fast-model, production"
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
        <p class="text-xs text-gray-500 mt-1">
          Comma-separated tags for categorization
        </p>
      </div>

      <!-- Auto-approve Permissions -->
      <label
        class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
      >
        <input
          type="checkbox"
          checked=${autoApprove}
          onChange=${(e) => setAutoApprove(e.target.checked)}
          class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
        />
        <div class="flex-1">
          <div class="font-medium text-sm">Auto-approve Permissions</div>
          <div class="text-xs text-gray-500">
            Automatically approve all permission requests from the agent for
            sessions using this server
          </div>
        </div>
      </label>

      <!-- Environment Variables -->
      <div>
        <div class="flex items-center justify-between mb-2">
          <label class="block text-sm text-gray-400"
            >Environment Variables
            <span class="text-xs text-gray-500">(optional)</span>
          </label>
          <button
            type="button"
            onClick=${addEnvVar}
            class="text-xs px-2 py-1 bg-slate-700 hover:bg-slate-600 rounded transition-colors"
          >
            + Add Variable
          </button>
        </div>
        ${envVars.length === 0
          ? html`
              <p class="text-xs text-gray-500 italic">
                No environment variables configured. Click "Add Variable" to add
                one.
              </p>
            `
          : html`
              <div class="space-y-2">
                ${envVars.map(
                  (env, idx) => html`
                    <div key=${idx} class="flex items-center gap-2">
                      <input
                        type="text"
                        value=${env.key}
                        placeholder="NAME"
                        onInput=${(e) => updateEnvVar(idx, "key", e.target.value)}
                        class="flex-1 px-2 py-1.5 bg-slate-700 rounded text-sm focus:outline-none focus:ring-1 focus:ring-blue-500 font-mono"
                      />
                      <span class="text-gray-500">=</span>
                      <input
                        type="text"
                        value=${env.value}
                        placeholder="value"
                        onInput=${(e) =>
                          updateEnvVar(idx, "value", e.target.value)}
                        class="flex-[2] px-2 py-1.5 bg-slate-700 rounded text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
                      />
                      <button
                        type="button"
                        onClick=${() => removeEnvVar(idx)}
                        class="p-1.5 text-red-400 hover:text-red-300 hover:bg-red-500/10 rounded transition-colors"
                        title="Remove variable"
                      >
                        <${TrashIcon} className="w-4 h-4" />
                      </button>
                    </div>
                  `,
                )}
              </div>
            `}
        <p class="text-xs text-gray-500 mt-2">
          These environment variables will be set when starting the ACP server
          process.
        </p>
      </div>

      <!-- Server-specific prompts (read-only, from files with acps: field) -->
      ${filePrompts.length > 0 &&
      html`
        <div>
          <label class="text-sm text-gray-400 mb-2 block"
            >Server-specific prompts
            <span class="text-xs text-gray-500"
              >(from prompt files)</span
            ></label
          >
          <div class="space-y-1">
            ${filePrompts.map(
              (p, idx) => html`
                <div
                  key=${idx}
                  class="flex items-center gap-2 p-2 bg-slate-800/50 rounded text-sm border border-slate-700/50"
                  title="From prompts file with acps: ${server.name}"
                >
                  <div class="flex-1 min-w-0">
                    <div class="font-medium text-xs">${p.name}</div>
                    <div
                      class="text-xs text-gray-500 truncate"
                      title=${p.prompt}
                    >
                      ${p.description || p.prompt}
                    </div>
                  </div>
                </div>
              `,
            )}
          </div>
        </div>
      `}

      ${formError &&
      html`
        <div
          class="p-2 bg-red-500/20 border border-red-500/50 rounded-lg text-red-400 text-sm"
        >
          ⚠️ ${formError}
        </div>
      `}

      <div class="flex justify-end gap-2">
        <button
          onClick=${onCancel}
          class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
        >
          Cancel
        </button>
        <button
          onClick=${handleSave}
          class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
        >
          Save
        </button>
      </div>
    </div>
  `;
}

/**
 * Helper component for editing a prompt inline
 */
function PromptEditForm({ prompt, onSave, onCancel, readOnly = false }) {
  const [name, setName] = useState(prompt.name);
  const [text, setText] = useState(prompt.prompt);
  const [backgroundColor, setBackgroundColor] = useState(
    prompt.backgroundColor || "",
  );
  const [group, setGroup] = useState(prompt.group || "");

  return html`
    <div class="space-y-3">
      <div>
        <label class="block text-sm text-gray-400 mb-1">Button Label</label>
        <input
          type="text"
          value=${name}
          onInput=${(e) => setName(e.target.value)}
          disabled=${readOnly}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${readOnly
            ? "opacity-60 cursor-not-allowed"
            : ""}"
        />
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1">Prompt Text</label>
        <textarea
          value=${text}
          onInput=${(e) => setText(e.target.value)}
          rows="3"
          disabled=${readOnly}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none ${readOnly
            ? "opacity-60 cursor-not-allowed"
            : ""}"
        />
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1"
          >Group (optional)</label
        >
        <input
          type="text"
          value=${group}
          onInput=${(e) => setGroup(e.target.value)}
          placeholder="e.g., Tasks, Code Quality"
          disabled=${readOnly}
          class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${readOnly
            ? "opacity-60 cursor-not-allowed"
            : ""}"
        />
      </div>
      <div>
        <label class="block text-sm text-gray-400 mb-1"
          >Background Color (optional)</label
        >
        <div class="flex items-center gap-2">
          <input
            type="color"
            value=${backgroundColor || "#334155"}
            onInput=${(e) => setBackgroundColor(e.target.value)}
            disabled=${readOnly}
            class="w-10 h-10 rounded cursor-pointer border border-slate-600 ${readOnly
              ? "opacity-60 cursor-not-allowed"
              : ""}"
            title="Choose background color"
          />
          <input
            type="text"
            value=${backgroundColor}
            onInput=${(e) => setBackgroundColor(e.target.value)}
            placeholder="#E8F5E9"
            disabled=${readOnly}
            class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono ${readOnly
              ? "opacity-60 cursor-not-allowed"
              : ""}"
          />
          ${backgroundColor &&
          !readOnly &&
          html`
            <button
              type="button"
              onClick=${() => setBackgroundColor("")}
              class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
              title="Clear color"
            >
              <svg
                class="w-4 h-4 text-gray-400"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M6 18L18 6M6 6l12 12"
                />
              </svg>
            </button>
          `}
        </div>
      </div>
      <div class="flex justify-end gap-2">
        <button
          onClick=${onCancel}
          class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
        >
          ${readOnly ? "Close" : "Cancel"}
        </button>
        ${!readOnly &&
        html`
          <button
            onClick=${() => onSave(name, text, backgroundColor, group)}
            disabled=${!name.trim() || !text.trim()}
            class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50"
          >
            Save
          </button>
        `}
      </div>
    </div>
  `;
}

/**
 * Settings Dialog Component
 * Manages ACP servers, workspaces, prompts, web access, and UI settings.
 */
export function SettingsDialog({
  isOpen,
  onClose,
  onSave,
  forceOpen = false,
  WorkspaceBadge,
}) {
  const [activeTab, setActiveTab] = useState("servers");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [warning, setWarning] = useState("");
  // Agent discovery dialog (triggered from Servers tab)
  const [showDiscoverAgents, setShowDiscoverAgents] = useState(false);

  // Configuration state
  const [workspaces, setWorkspaces] = useState([]);
  const [acpServers, setAcpServers] = useState([]);
  // Sorted ACP servers for display (alphabetical by name)
  const sortedAcpServers = useMemo(
    () => [...acpServers].sort((a, b) => a.name.localeCompare(b.name)),
    [acpServers],
  );

  const [authEnabled, setAuthEnabled] = useState(false);
  const [authUsername, setAuthUsername] = useState("");
  const [authPassword, setAuthPassword] = useState("");
  // Track whether the password was loaded from an existing config (sanitized by backend).
  // When true, the user hasn't changed it and we should skip client-side validation.
  const [authPasswordUnchanged, setAuthPasswordUnchanged] = useState(false);
  const [cfEnabled, setCfEnabled] = useState(false);
  const [cfTeamDomain, setCfTeamDomain] = useState("");
  const [cfAudience, setCfAudience] = useState("");
  const [externalPort, setExternalPort] = useState(""); // Empty string = random port
  const [currentExternalPort, setCurrentExternalPort] = useState(null); // Currently running external port
  const [externalEnabled, setExternalEnabled] = useState(false); // Is external listener currently running
  const [hookUpCommand, setHookUpCommand] = useState("");
  const [hookDownCommand, setHookDownCommand] = useState("");
  const [hookExternalAddress, setHookExternalAddress] = useState("");

  // Access log setting (enabled by default)
  const [accessLogEnabled, setAccessLogEnabled] = useState(true);

  // Supported runners (fetched from server based on platform)
  const [supportedRunners, setSupportedRunners] = useState([]);

  // Restricted runners configuration (per runner type)
  const [restrictedRunners, setRestrictedRunners] = useState({});
  const [expandedRunner, setExpandedRunner] = useState(null);
  const [runnerDefaults, setRunnerDefaults] = useState({});

  // Form state for adding new items
  const [showAddServer, setShowAddServer] = useState(false);
  const [newServerName, setNewServerName] = useState("");
  const [newServerCommand, setNewServerCommand] = useState("");
  const [newServerType, setNewServerType] = useState("");
  const [newServerTags, setNewServerTags] = useState("");

  const [editingServer, setEditingServer] = useState(null);

  // Agent types for the type dropdown
  const [agentTypes, setAgentTypes] = useState([]);

  // Track server renames (oldName -> newName) so backend can update sessions
  const [serverRenames, setServerRenames] = useState({});

  // Prompts state
  const [prompts, setPrompts] = useState([]);
  // Sorted prompts for display (alphabetical by name)
  const sortedPrompts = useMemo(
    () => [...prompts].sort((a, b) => (a.name || "").localeCompare(b.name || "")),
    [prompts],
  );
  const [showAddPrompt, setShowAddPrompt] = useState(false);
  const [newPromptName, setNewPromptName] = useState("");
  const [newPromptText, setNewPromptText] = useState("");
  const [newPromptColor, setNewPromptColor] = useState("");
  const [newPromptGroup, setNewPromptGroup] = useState("");
  const [editingPrompt, setEditingPrompt] = useState(null);

  // Prompt drag-and-drop state
  const [draggedPromptIndex, setDraggedPromptIndex] = useState(null);
  const [dragOverPromptIndex, setDragOverPromptIndex] = useState(null);

  // UI settings state (macOS only)
  const [agentCompletedSound, setAgentCompletedSound] = useState(false);
  const [nativeNotifications, setNativeNotifications] = useState(false);
  const [notificationPermissionStatus, setNotificationPermissionStatus] =
    useState(-1); // -1 = unknown, 0 = not determined, 1 = denied, 2 = authorized
  const [showInAllSpaces, setShowInAllSpaces] = useState(false);
  const [startAtLogin, setStartAtLogin] = useState(false);
  const [loginItemSupported, setLoginItemSupported] = useState(false);
  const [badgeClickEnabled, setBadgeClickEnabled] = useState(true);
  const [badgeClickCommand, setBadgeClickCommand] =
    useState("open ${MITTO_WORKING_DIR}");

  // Confirmation settings (all platforms)
  const [confirmDeleteSession, setConfirmDeleteSession] = useState(true);
  // Confirmation settings (macOS only)
  const [confirmQuitWithRunningSessions, setConfirmQuitWithRunningSessions] =
    useState(true);

  // Archive retention period setting
  const [archiveRetentionPeriod, setArchiveRetentionPeriod] = useState("never");

  // Auto-archive inactive period setting
  const [autoArchiveInactiveAfter, setAutoArchiveInactiveAfter] = useState("");

  // Follow-up suggestions settings (advanced) - enabled by default
  const [actionButtonsEnabled, setActionButtonsEnabled] = useState(true);

  // External images settings (advanced) - disabled by default for security
  const [externalImagesEnabled, setExternalImagesEnabled] = useState(false);

  // Max child conversations setting - default 10
  const [maxChildConversations, setMaxChildConversations] = useState(10);

  // Default flags for new conversations
  const [availableFlags, setAvailableFlags] = useState([]);
  const [defaultFlags, setDefaultFlags] = useState({});

  // Input font family setting (web UI)
  const [inputFontFamily, setInputFontFamily] = useState("system");

  // Send key mode setting (web UI) - default: "enter"
  // "enter" = Enter to send, Shift+Enter for new line
  // "ctrl-enter" = Ctrl/Cmd+Enter to send, Enter for new line
  const [sendKeyMode, setSendKeyMode] = useState("enter");

  // Conversation cycling mode setting (web UI) - default: "all"
  const [conversationCyclingMode, setConversationCyclingMode] = useState(
    CYCLING_MODE.ALL,
  );

  // Single expanded group (accordion mode) setting (web UI) - default: false
  const [singleExpandedGroup, setSingleExpandedGroup] = useState(false);

  // Global auto-approve permissions setting - default: true (matches current behavior)
  const [globalAutoApprove, setGlobalAutoApprove] = useState(true);

  // Follow system theme setting (client-side, stored in localStorage)
  const [followSystemTheme, setFollowSystemTheme] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto-follow-system-theme");
      return saved === null ? true : saved === "true";
    }
    return true;
  });

  // Follow system reduced motion setting (client-side, stored in localStorage)
  const [followSystemReducedMotion, setFollowSystemReducedMotion] = useState(
    () => {
      if (typeof localStorage !== "undefined") {
        const saved = localStorage.getItem(
          "mitto-follow-system-reduced-motion",
        );
        return saved === null ? true : saved === "true";
      }
      return true;
    },
  );

  // Reduce animations setting (client-side, stored in localStorage)
  const [reduceAnimations, setReduceAnimationsState] = useState(() => {
    if (typeof localStorage !== "undefined") {
      // If following system, check OS preference
      const followSystem = localStorage.getItem(
        "mitto-follow-system-reduced-motion",
      );
      if (followSystem === null || followSystem === "true") {
        if (typeof window !== "undefined" && window.matchMedia) {
          return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        }
      }
      const saved = localStorage.getItem("mitto-reduce-animations");
      if (saved !== null) return saved === "true";
    }
    return false;
  });

  // Prompt sort mode setting (client-side, stored in localStorage and server)
  const [promptSortMode, setPromptSortMode] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto_prompt_sort_mode");
      return saved === "color" ? "color" : "alphabetical";
    }
    return "alphabetical";
  });

  // Check if running in the native macOS app
  const isMacApp = typeof window.mittoPickFolder === "function";

  // Handle follow system theme toggle
  const handleFollowSystemThemeChange = (enabled) => {
    setFollowSystemTheme(enabled);
    localStorage.setItem("mitto-follow-system-theme", String(enabled));
    // Dispatch a custom event so app.js can react to the change
    window.dispatchEvent(
      new CustomEvent("mitto-follow-system-theme-changed", {
        detail: { enabled },
      }),
    );
  };

  // Handle follow system reduced motion toggle
  const handleFollowSystemReducedMotionChange = (enabled) => {
    setFollowSystemReducedMotion(enabled);
    localStorage.setItem(
      "mitto-follow-system-reduced-motion",
      String(enabled),
    );
    // When enabling, sync with OS preference immediately
    let newReduceAnimations = reduceAnimations;
    if (enabled && typeof window !== "undefined" && window.matchMedia) {
      newReduceAnimations = window.matchMedia(
        "(prefers-reduced-motion: reduce)",
      ).matches;
      setReduceAnimationsState(newReduceAnimations);
    }
    window.dispatchEvent(
      new CustomEvent("mitto-reduce-animations-changed", {
        detail: {
          followSystem: enabled,
          reduceAnimations: newReduceAnimations,
        },
      }),
    );
  };

  // Handle explicit reduce animations toggle
  const handleReduceAnimationsChange = (enabled) => {
    // When user manually toggles, disable follow system
    setFollowSystemReducedMotion(false);
    setReduceAnimationsState(enabled);
    localStorage.setItem("mitto-follow-system-reduced-motion", "false");
    localStorage.setItem("mitto-reduce-animations", String(enabled));
    window.dispatchEvent(
      new CustomEvent("mitto-reduce-animations-changed", {
        detail: { followSystem: false, reduceAnimations: enabled },
      }),
    );
  };

  // Handle prompt sort mode change
  const handlePromptSortModeChange = (mode) => {
    setPromptSortMode(mode);
    savePromptSortMode(mode); // This saves to localStorage and server
  };

  // Load configuration when dialog opens
  useEffect(() => {
    if (isOpen) {
      // Clear any previous messages when dialog opens
      setError("");
      setWarning("");
      setSuccess("");
      loadConfig();
      loadSupportedRunners();
    }
  }, [isOpen]);

  // Fetch available agent types for the type dropdown
  useEffect(() => {
    secureFetch(apiUrl("/api/agent-types"))
      .then((r) => r.json())
      .then((data) => setAgentTypes(data.agent_types || []))
      .catch(() => setAgentTypes([]));
  }, []);

  // Load supported runners from server
  const loadSupportedRunners = async () => {
    try {
      const res = await fetch(apiUrl("/api/supported-runners"), {
        credentials: "same-origin",
      });
      if (res.ok) {
        const runners = await res.json();
        setSupportedRunners(runners || []);
      }
    } catch (err) {
      console.error("Failed to load supported runners:", err);
      // Fallback to all runners if fetch fails
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

    // Also load runner defaults
    try {
      const res = await fetch(apiUrl("/api/runner-defaults"), {
        credentials: "same-origin",
      });
      if (res.ok) {
        const defaults = await res.json();
        setRunnerDefaults(defaults || {});
      }
    } catch (err) {
      console.error("Failed to load runner defaults:", err);
    }
  };

  // Save prompts order to settings.json immediately
  const savePromptsOrder = async (newPrompts) => {
    try {
      // Get current config first (cached read is fine for this read-before-write)
      const config = await fetchConfig();

      // Build the config object with updated prompts
      const webConfig = {
        host: config.web?.host || "127.0.0.1",
        external_port: config.web?.external_port || 0,
        auth: config.web?.auth || null,
        hooks: config.web?.hooks || null,
      };

      // Filter prompts to only save settings-based prompts (not file-based ones)
      const settingsPrompts = newPrompts
        .filter((p) => !p.source || p.source === "settings")
        .map(({ source, ...rest }) => rest); // Remove source field before saving

      const saveConfig = {
        workspaces: config.workspaces || [],
        acp_servers: config.acp_servers || [],
        prompts: settingsPrompts,
        web: webConfig,
        ui: config.ui || {},
      };

      await secureFetch(apiUrl("/api/config"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(saveConfig),
      });
      // Config changed on disk — invalidate cache so next read is fresh.
      invalidateConfigCache();
    } catch (err) {
      console.error("Failed to save prompts order:", err);
    }
  };

  // Prompt drag-and-drop handlers
  const handlePromptDragStart = (e, index) => {
    setDraggedPromptIndex(index);
    e.dataTransfer.effectAllowed = "move";
    // Set drag image data (required for Firefox)
    e.dataTransfer.setData("text/plain", index.toString());
  };

  const handlePromptDragEnd = () => {
    setDraggedPromptIndex(null);
    setDragOverPromptIndex(null);
  };

  const handlePromptDragOver = (e, index) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    if (draggedPromptIndex !== null && index !== draggedPromptIndex) {
      setDragOverPromptIndex(index);
    }
  };

  const handlePromptDragLeave = () => {
    setDragOverPromptIndex(null);
  };

  const handlePromptDrop = (e, dropIndex) => {
    e.preventDefault();
    if (draggedPromptIndex === null || draggedPromptIndex === dropIndex) {
      setDraggedPromptIndex(null);
      setDragOverPromptIndex(null);
      return;
    }

    // Reorder prompts
    const newPrompts = [...prompts];
    const [draggedItem] = newPrompts.splice(draggedPromptIndex, 1);
    newPrompts.splice(dropIndex, 0, draggedItem);

    setPrompts(newPrompts);
    setDraggedPromptIndex(null);
    setDragOverPromptIndex(null);

    // Save immediately
    savePromptsOrder(newPrompts);
  };

  const loadConfig = async () => {
    setLoading(true);
    setError("");
    try {
      // Fetch config and external status in parallel.
      // force=true ensures the settings dialog always shows the latest saved config.
      const [config, externalStatusRes] = await Promise.all([
        fetchConfig(null, /* force */ true),
        fetch(apiUrl("/api/external-status"), { credentials: "same-origin" }),
      ]);

      // Load external status
      if (externalStatusRes.ok) {
        const externalStatus = await externalStatusRes.json();
        setExternalEnabled(externalStatus.enabled);
        setCurrentExternalPort(externalStatus.port || null);
      }

      // Load ACP servers first (needed for workspace validation)
      const servers = config.acp_servers || [];
      setAcpServers(servers);

      // Reset server renames when config is loaded
      setServerRenames({});

      // Filter out invalid workspaces:
      // - Must have a non-empty working_dir
      // - Must reference an existing ACP server
      const serverNames = new Set(servers.map((s) => s.name));
      const rawWorkspaces = config.workspaces || [];
      const validWorkspaces = rawWorkspaces.filter((ws) => {
        // Check for valid working_dir
        if (
          !ws.working_dir ||
          typeof ws.working_dir !== "string" ||
          ws.working_dir.trim() === ""
        ) {
          console.warn("Filtering out workspace with invalid working_dir:", ws);
          return false;
        }
        // Check for valid ACP server reference
        if (!ws.acp_server || !serverNames.has(ws.acp_server)) {
          console.warn(
            "Filtering out workspace with invalid/missing ACP server:",
            ws,
          );
          return false;
        }
        return true;
      });
      setWorkspaces(validWorkspaces);

      // Load auth settings - check if external access is enabled
      // External access is enabled if any auth is configured OR host is 0.0.0.0
      const hasSimpleAuth = config.web?.auth?.simple;
      const hasCfAuth = config.web?.auth?.cloudflare;
      const isExternalHost = config.web?.host === "0.0.0.0";
      if (hasSimpleAuth || hasCfAuth || isExternalHost) {
        setAuthEnabled(true);
      } else {
        setAuthEnabled(false);
      }

      // Simple auth
      const loadedUsername = config.web?.auth?.simple?.username || "";
      const loadedPassword = config.web?.auth?.simple?.password || "";
      setAuthUsername(loadedUsername);
      setAuthPassword(loadedPassword);
      // Backend sanitizes the password (sends empty string) for security.
      // Track this so we can skip validation and preserve the existing password on save.
      setAuthPasswordUnchanged(!!loadedUsername && !loadedPassword);

      // Cloudflare auth
      setCfEnabled(!!hasCfAuth);
      setCfTeamDomain(config.web?.auth?.cloudflare?.team_domain || "");
      setCfAudience(config.web?.auth?.cloudflare?.audience || "");

      // Load external port setting (0 or empty = random)
      const extPort = config.web?.external_port;
      setExternalPort(extPort && extPort > 0 ? String(extPort) : "");

      // Load hook settings
      setHookUpCommand(config.web?.hooks?.up?.command || "");
      setHookDownCommand(config.web?.hooks?.down?.command || "");
      setHookExternalAddress(config.web?.hooks?.external_address || "");

      // Load access log setting (enabled by default)
      setAccessLogEnabled(config.web?.access_log?.enabled !== false);

      // Load prompts from top-level (not under web)
      setPrompts(config.prompts || []);

      // Load UI settings (macOS only)
      setAgentCompletedSound(
        config.ui?.mac?.notifications?.sounds?.agent_completed || false,
      );
      setNativeNotifications(
        config.ui?.mac?.notifications?.native_enabled || false,
      );
      setShowInAllSpaces(config.ui?.mac?.show_in_all_spaces || false);

      // Load badge click action settings (macOS only)
      setBadgeClickEnabled(
        config.ui?.mac?.badge_click_action?.enabled !== false,
      );
      setBadgeClickCommand(
        config.ui?.mac?.badge_click_action?.command || "open ${MITTO_WORKING_DIR}",
      );

      // Load notification permission status (macOS only) - used to show warning if denied
      if (typeof window.mittoGetNotificationPermissionStatus === "function") {
        const status = await window.mittoGetNotificationPermissionStatus();
        setNotificationPermissionStatus(status);
      }

      // Load login item state from native API (macOS 13+ only)
      if (typeof window.mittoIsLoginItemSupported === "function") {
        const supported = await window.mittoIsLoginItemSupported();
        setLoginItemSupported(supported);
        if (supported && typeof window.mittoIsLoginItemEnabled === "function") {
          const enabled = await window.mittoIsLoginItemEnabled();
          setStartAtLogin(enabled);
        }
      }

      // Load confirmation settings (all platforms, default to true)
      setConfirmDeleteSession(
        config.ui?.confirmations?.delete_session !== false,
      );
      // Load confirmation settings (macOS only, default to true)
      setConfirmQuitWithRunningSessions(
        config.ui?.confirmations?.quit_with_running_sessions !== false,
      );

      // Load archive retention period setting (default to "never")
      setArchiveRetentionPeriod(
        config.session?.archive_retention_period || "never",
      );

      // Load auto-archive inactive period setting (default to "" - disabled)
      setAutoArchiveInactiveAfter(
        config.session?.auto_archive_inactive_after || "",
      );

      // Load follow-up suggestions settings (advanced) - enabled by default
      setActionButtonsEnabled(
        config.conversations?.action_buttons?.enabled !== false,
      );

      // Load external images settings (advanced) - disabled by default for security
      setExternalImagesEnabled(
        config.conversations?.external_images?.enabled === true,
      );

      // Load max child conversations setting - default to 10
      setMaxChildConversations(
        config.conversations?.max_child_conversations ?? 10,
      );

      // Load input font family setting (web UI) - default to "system"
      setInputFontFamily(config.ui?.web?.input_font_family || "system");

      // Load send key mode setting (web UI) - default to "enter"
      setSendKeyMode(config.ui?.web?.send_key_mode || "enter");

      // Load single expanded group (accordion mode) setting (web UI) - default to false
      const accordionEnabled = config.ui?.web?.single_expanded_group === true;
      setSingleExpandedGroup(accordionEnabled);

      // Load conversation cycling mode setting (web UI) - default to "all"
      // When accordion mode is enabled, force cycling to "all"
      setConversationCyclingMode(
        accordionEnabled
          ? CYCLING_MODE.ALL
          : config.ui?.web?.conversation_cycling_mode || CYCLING_MODE.ALL,
      );

      // Load restricted runners configuration
      setRestrictedRunners(config.restricted_runners || {});

      // Load global auto-approve permissions setting - default to true (matches current behavior)
      // When permissions config is null/undefined, or auto_approve is null, default is true
      setGlobalAutoApprove(config.permissions?.auto_approve !== false);

      // Load available flags and configured default flags
      try {
        const flagsRes = await fetch(apiUrl("/api/advanced-flags"), {
          credentials: "same-origin",
        });
        if (flagsRes.ok) {
          const flagsData = await flagsRes.json();
          setAvailableFlags(flagsData.flags || []);
          setDefaultFlags(flagsData.configured_defaults || {});
        }
      } catch (err) {
        console.warn("Failed to load advanced flags:", err);
      }

    } catch (err) {
      setError("Failed to load configuration: " + err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    setError("");
    setWarning("");
    setSuccess("");

    // Validation
    if (workspaces.length === 0) {
      setError("At least one workspace is required. Please open the Workspaces dialog to add one.");
      return;
    }

    if (acpServers.length === 0) {
      setError("At least one ACP server is required");
      setActiveTab("servers");
      return;
    }

    if (authEnabled && authUsername.trim()) {
      const usernameError = validateUsername(authUsername);
      if (usernameError) {
        setError(usernameError);
        setActiveTab("web");
        return;
      }
      // Skip password validation if the password hasn't been changed from the
      // sanitized empty value loaded from the backend (existing password is preserved server-side).
      if (!authPasswordUnchanged) {
        const passwordError = validatePassword(authPassword);
        if (passwordError) {
          setError(passwordError);
          setActiveTab("web");
          return;
        }
      }
    }
    if (cfEnabled) {
      if (!cfTeamDomain.trim()) {
        setError("Cloudflare Access: Team domain is required");
        setActiveTab("web");
        return;
      }
      if (cfTeamDomain.includes("://")) {
        setError("Cloudflare Access: Team domain should be a domain name, not a URL");
        setActiveTab("web");
        return;
      }
      if (!cfAudience.trim()) {
        setError("Cloudflare Access: Audience tag is required");
        setActiveTab("web");
        return;
      }
    }
    if (authEnabled && !authUsername.trim() && !cfEnabled) {
      setError("External access requires at least one authentication method (username/password or Cloudflare Access)");
      setActiveTab("web");
      return;
    }

    setSaving(true);
    try {
      // Build web config
      const webConfig = {
        // Set host based on external access setting
        host: authEnabled ? "0.0.0.0" : "127.0.0.1",
        // External port: parse as int, 0 or empty means random
        external_port: externalPort ? parseInt(externalPort, 10) : 0,
        auth: authEnabled
          ? {
              ...(authUsername.trim()
                ? {
                    simple: {
                      username: authUsername.trim(),
                      // When password is unchanged (loaded empty from sanitized config),
                      // send empty string so backend preserves the existing password.
                      password: authPasswordUnchanged
                        ? ""
                        : authPassword.trim(),
                    },
                  }
                : {}),
              ...(cfEnabled
                ? {
                    cloudflare: {
                      team_domain: cfTeamDomain.trim(),
                      audience: cfAudience.trim(),
                    },
                  }
                : {}),
            }
          : null,
      };

      // Add access log setting
      webConfig.access_log = {
        enabled: accessLogEnabled,
      };

      // Add hooks if configured
      if (hookUpCommand.trim() || hookDownCommand.trim() || hookExternalAddress.trim()) {
        webConfig.hooks = {};
        if (hookUpCommand.trim()) {
          webConfig.hooks.up = { command: hookUpCommand.trim() };
        }
        if (hookDownCommand.trim()) {
          webConfig.hooks.down = { command: hookDownCommand.trim() };
        }
        if (hookExternalAddress.trim()) {
          webConfig.hooks.external_address = hookExternalAddress.trim();
        }
      }

      // Build UI config
      const uiConfig = {
        // Confirmations (all platforms)
        confirmations: {
          delete_session: confirmDeleteSession,
        },
        // Web-specific UI settings
        web: {
          input_font_family: inputFontFamily,
          send_key_mode: sendKeyMode,
          conversation_cycling_mode: conversationCyclingMode,
          single_expanded_group: singleExpandedGroup,
        },
      };

      // Add macOS-specific settings
      if (isMacApp) {
        // Add quit confirmation setting (macOS only)
        uiConfig.confirmations.quit_with_running_sessions =
          confirmQuitWithRunningSessions;
        uiConfig.mac = {
          notifications: {
            sounds: {
              agent_completed: agentCompletedSound,
            },
            native_enabled: nativeNotifications,
          },
          show_in_all_spaces: showInAllSpaces,
          start_at_login: startAtLogin,
          badge_click_action: {
            enabled: badgeClickEnabled,
            command: badgeClickCommand,
          },
        };
      }

      // Build conversations config with explicit enabled state and default flags
      const conversationsConfig = {
        action_buttons: {
          enabled: actionButtonsEnabled,
        },
        external_images: {
          enabled: externalImagesEnabled,
        },
        max_child_conversations: maxChildConversations,
        // Only include default_flags if any are set
        ...(Object.keys(defaultFlags).length > 0 && {
          default_flags: defaultFlags,
        }),
      };

      // Build session config with archive retention period and auto-archive inactive period
      const sessionConfig = {
        archive_retention_period: archiveRetentionPeriod,
        auto_archive_inactive_after: autoArchiveInactiveAfter,
      };

      // Filter prompts to only save settings-based prompts (not file-based ones)
      // Prompts with source='settings' or no source (new prompts) should be saved
      // Prompts with source='file' or source='workspace' should not be saved to settings.json
      const settingsPrompts = prompts
        .filter((p) => !p.source || p.source === "settings")
        .map(({ source, ...rest }) => rest); // Remove source field before saving

      // ACP servers are saved with source field so backend can filter out RC file servers
      // (RC file servers are managed in .mittorc, not settings.json)
      const acpServersToSave = acpServers.map((srv) => {
        const saved = {
          name: srv.name,
          command: srv.command,
          source: srv.source || "settings", // Default to settings if not specified
          auto_approve: srv.auto_approve || false, // Include auto-approve setting
          env: srv.env || undefined, // Include env vars if present
          tags: srv.tags && srv.tags.length > 0 ? srv.tags : undefined, // Include tags if present
        };
        // Only include type if specified (otherwise name is used as type)
        if (srv.type) {
          saved.type = srv.type;
        }
        return saved;
      });

      // Only include restricted runners that have configurations
      // Filter out empty runner configs
      const restrictedRunnersToSave = {};
      for (const [runnerType, runnerConfig] of Object.entries(
        restrictedRunners,
      )) {
        if (runnerConfig && runnerConfig.restrictions) {
          restrictedRunnersToSave[runnerType] = runnerConfig;
        }
      }

      // Build permissions config
      const permissionsConfig = {
        auto_approve: globalAutoApprove,
      };

      const config = {
        workspaces: workspaces,
        acp_servers: acpServersToSave,
        prompts: settingsPrompts, // Only settings-based prompts
        web: webConfig,
        ui: uiConfig,
        conversations: conversationsConfig,
        session: sessionConfig,
        permissions: permissionsConfig,
        restricted_runners:
          Object.keys(restrictedRunnersToSave).length > 0
            ? restrictedRunnersToSave
            : undefined,
        // Include server renames so backend can update sessions that reference old names
        server_renames:
          Object.keys(serverRenames).length > 0 ? serverRenames : undefined,
      };

      // DEBUG: Log config being saved
      console.log("DEBUG: Saving config:", JSON.stringify(config.ui, null, 2));
      console.log("DEBUG: nativeNotifications state:", nativeNotifications);

      const res = await secureFetch(apiUrl("/api/config"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });

      const result = await res.json();

      if (!res.ok) {
        throw new Error(result.error || "Failed to save configuration");
      }

      // Config changed on disk — invalidate cache so next read is fresh.
      invalidateConfigCache();

      // Update the global sound and notification setting flags
      if (isMacApp) {
        window.mittoAgentCompletedSoundEnabled = agentCompletedSound;
        window.mittoNativeNotificationsEnabled = nativeNotifications;

        // Update quit confirmation setting via native API
        if (typeof window.mittoSetQuitConfirmEnabled === "function") {
          try {
            window.mittoSetQuitConfirmEnabled(confirmQuitWithRunningSessions);
          } catch (err) {
            console.error("Failed to update quit confirmation setting:", err);
          }
        }
      }

      // Apply login item setting via native API (macOS 13+ only)
      if (
        loginItemSupported &&
        typeof window.mittoSetLoginItemEnabled === "function"
      ) {
        try {
          await window.mittoSetLoginItemEnabled(startAtLogin);
        } catch (err) {
          console.error("Failed to update login item:", err);
          // Don't fail the save, just log the error
        }
      }

      // Fetch updated external status to get the actual running port
      let actualExternalPort = null;
      let externalAccessActive = false;
      try {
        const statusRes = await fetch(apiUrl("/api/external-status"), {
          credentials: "same-origin",
        });
        if (statusRes.ok) {
          const status = await statusRes.json();
          externalAccessActive = status.enabled;
          actualExternalPort = status.port;
          setExternalEnabled(status.enabled);
          setCurrentExternalPort(status.port || null);
        }
      } catch (e) {
        console.error("Failed to fetch external status:", e);
      }

      // Build success message based on what was applied
      let successMsg = "Configuration saved successfully";
      if (externalAccessActive && actualExternalPort) {
        successMsg = `Configuration saved. External access on port ${actualExternalPort}`;
      } else if (result.applied) {
        const details = [];
        if (result.applied.external_access_enabled) {
          details.push("external access enabled");
        }
        if (result.applied.auth_enabled) {
          details.push("authentication active");
        }
        if (details.length > 0) {
          successMsg += ` (${details.join(", ")})`;
        }
      }
      setSuccess(successMsg);

      // Clear server renames after successful save
      setServerRenames({});

      onSave?.();

      // Close dialog after short delay
      setTimeout(() => onClose?.(), 500);
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  };

  const handleClose = () => {
    // Always require at least one ACP server
    if (acpServers.length === 0) {
      setError("At least one ACP server is required");
      setActiveTab("servers");
      return;
    }
    // Always require at least one workspace
    if (workspaces.length === 0) {
      setError("At least one workspace is required. Please open the Workspaces dialog to add one.");
      return;
    }
    onClose?.();
  };



  // ACP Server management
  const addServer = () => {
    if (!newServerName.trim()) {
      setError("Please enter a server name");
      return;
    }
    if (!newServerCommand.trim()) {
      setError("Please enter a server command");
      return;
    }
    if (!newServerType.trim()) {
      setError("Please select an agent type");
      return;
    }
    if (acpServers.some((s) => s.name === newServerName.trim())) {
      setError("A server with this name already exists");
      return;
    }

    const newServer = {
      name: newServerName.trim(),
      command: newServerCommand.trim(),
      source: "settings", // New servers added via UI are saved to settings.json
    };
    // Only include type if specified (otherwise name is used as type)
    if (newServerType.trim()) {
      newServer.type = newServerType.trim();
    }
    // Parse and include tags if specified
    const parsedNewTags = newServerTags
      .split(",")
      .map((t) => t.trim())
      .filter((t) => t.length > 0);
    if (parsedNewTags.length > 0) {
      newServer.tags = parsedNewTags;
    }

    setAcpServers([...acpServers, newServer]);
    setNewServerName("");
    setNewServerCommand("");
    setNewServerType("");
    setNewServerTags("");
    setShowAddServer(false);
    setError("");
  };

  const updateServer = (oldName, newName, newCommand, newType, autoApprove, env, tags) => {
    if (!newName.trim() || !newCommand.trim()) {
      return "Server name and command cannot be empty";
    }
    if (!newType || !newType.trim()) {
      return "Please select an agent type";
    }

    // Check for duplicate name (excluding current)
    if (
      newName !== oldName &&
      acpServers.some((s) => s.name === newName.trim())
    ) {
      return "A server with this name already exists";
    }

    // Update server (prompts are now read-only from files)
    setAcpServers(
      acpServers.map((s) => {
        if (s.name !== oldName) return s;
        const updated = {
          name: newName.trim(),
          command: newCommand.trim(),
          prompts: s.prompts, // Preserve existing prompts (read-only from files)
          source: s.source, // Preserve source (rcfile or settings)
          auto_approve: autoApprove || undefined, // undefined to omit if false
          env: env && Object.keys(env).length > 0 ? env : undefined, // undefined to omit if empty
          tags: tags && tags.length > 0 ? tags : undefined, // undefined to omit if empty
        };
        // Only include type if specified (otherwise name is used as type)
        if (newType && newType.trim()) {
          updated.type = newType.trim();
        }
        return updated;
      }),
    );

    // Update workspaces that reference this server
    if (newName !== oldName) {
      setWorkspaces(
        workspaces.map((ws) =>
          ws.acp_server === oldName
            ? { ...ws, acp_server: newName.trim() }
            : ws,
        ),
      );

      // Track server rename so backend can update sessions
      // If oldName was already a rename target, follow the chain to the original name
      const trimmedNewName = newName.trim();
      const originalName = Object.entries(serverRenames).find(
        ([, target]) => target === oldName,
      )?.[0];
      if (originalName) {
        // Update the existing rename entry
        setServerRenames({
          ...serverRenames,
          [originalName]: trimmedNewName,
        });
      } else {
        // Add a new rename entry
        setServerRenames({
          ...serverRenames,
          [oldName]: trimmedNewName,
        });
      }
    }

    setEditingServer(null);
    setError("");
  };

  const removeServer = (serverName) => {
    // Check if any workspace uses this server
    const usedBy = workspaces.filter((ws) => ws.acp_server === serverName);
    if (usedBy.length > 0) {
      // Build a helpful error message listing the workspaces using this server
      const workspacePaths = usedBy.map((ws) => ws.working_dir).slice(0, 3); // Show up to 3
      const pathList = workspacePaths.join(", ");
      const moreCount = usedBy.length - workspacePaths.length;
      const moreText = moreCount > 0 ? ` and ${moreCount} more` : "";
      setError(
        `Cannot delete "${serverName}": used by workspace(s): ${pathList}${moreText}. Remove or reassign these workspaces first (use the Workspaces dialog).`,
      );
      return;
    }

    if (acpServers.length <= 1) {
      setError("At least one ACP server is required");
      return;
    }

    setAcpServers(acpServers.filter((s) => s.name !== serverName));
    setError(""); // Clear any previous errors
  };

  const duplicateServer = (serverName) => {
    const server = acpServers.find((s) => s.name === serverName);
    if (!server) return;

    // Generate a unique name by appending "(copy)" or "(copy N)"
    let baseName = server.name;
    let copyNum = 1;
    let newName = `${baseName} (copy)`;

    // Check if name already ends with "(copy)" or "(copy N)"
    const copyMatch = baseName.match(/^(.+) \(copy(?: (\d+))?\)$/);
    if (copyMatch) {
      baseName = copyMatch[1];
      copyNum = copyMatch[2] ? parseInt(copyMatch[2], 10) + 1 : 2;
      newName = `${baseName} (copy ${copyNum})`;
    }

    // Find a unique name
    while (acpServers.some((s) => s.name === newName)) {
      copyNum++;
      newName = `${baseName} (copy ${copyNum})`;
    }

    // Create the duplicated server with all copyable properties.
    // Prompts are file-based and not copied; source defaults to "settings".
    const duplicatedServer = {
      name: newName,
      command: server.command,
      source: "settings", // Duplicates are always settings-managed
    };

    // Copy type if present
    if (server.type) {
      duplicatedServer.type = server.type;
    }

    // Copy tags if present
    if (server.tags && server.tags.length > 0) {
      duplicatedServer.tags = [...server.tags];
    }

    // Copy environment variables if present (shallow copy to avoid shared references)
    if (server.env && Object.keys(server.env).length > 0) {
      duplicatedServer.env = { ...server.env };
    }

    // Copy auto_approve if enabled
    if (server.auto_approve) {
      duplicatedServer.auto_approve = server.auto_approve;
    }

    setAcpServers([...acpServers, duplicatedServer]);
    setError("");
  };

  if (!isOpen) return null;

  // Can close if we have both ACP servers and workspaces configured
  const canClose = acpServers.length > 0 && workspaces.length > 0;

  // Define navigation items for sidebar
  const navItems = [
    { id: "servers", label: "ACP Servers", icon: ServerIcon },
    { id: "prompts", label: "Prompts", icon: LightningIcon },
    { id: "runners", label: "Runners", icon: LockIcon },
    { id: "permissions", label: "Conversations", icon: ShieldIcon },
    { id: "web", label: "Web", icon: GlobeIcon },
    { id: "ui", label: "UI", icon: SlidersIcon },
  ];

  return html`
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick=${canClose ? handleClose : null}
    >
      <div
        class="bg-mitto-sidebar rounded-xl w-[70vw] h-[70vh] max-w-[95vw] max-h-[95vh] overflow-hidden shadow-2xl flex flex-col"
        onClick=${(e) => e.stopPropagation()}
      >
        <!-- Header -->
        <div
          class="flex items-center justify-between p-4 border-b border-slate-700"
        >
          <h3 class="text-lg font-semibold flex items-center gap-2">
            <${SettingsIcon} className="w-5 h-5" />
            Settings
          </h3>
          ${canClose &&
          html`
            <button
              onClick=${handleClose}
              class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors"
            >
              <${CloseIcon} className="w-5 h-5" />
            </button>
          `}
        </div>

        <!-- Main content area with sidebar - fills available space -->
        <div class="flex flex-1 min-h-0 overflow-hidden">
          <!-- Sidebar Navigation -->
          <nav
            class="w-44 flex-shrink-0 border-r border-slate-700 py-2 overflow-y-auto"
          >
            ${navItems.map(
              (item) => html`
                <button
                  key=${item.id}
                  onClick=${() => setActiveTab(item.id)}
                  class="w-full flex items-center gap-3 px-4 py-2.5 text-sm font-medium transition-colors ${activeTab ===
                  item.id
                    ? "text-blue-400 bg-blue-500/10 border-l-2 border-blue-400"
                    : "text-gray-400 hover:text-white hover:bg-slate-700/50 border-l-2 border-transparent"}"
                >
                  <${item.icon} className="w-4 h-4 flex-shrink-0" />
                  <span class="truncate">${item.label}</span>
                </button>
              `,
            )}
          </nav>

          <!-- Content Area -->
          <div class="flex-1 overflow-y-auto p-4">
            ${loading
              ? html`
                  <div class="flex items-center justify-center py-12">
                    <${SpinnerIcon} className="w-8 h-8 text-blue-400" />
                  </div>
                `
              : html`
                  <!-- ACP Servers Tab -->
                  ${activeTab === "servers" &&
                  html`
                    <div class="space-y-4">
                      <div class="flex items-center justify-between">
                        <p class="text-gray-400 text-sm">
                          ACP servers are AI coding assistants.${" "}
                          <a
                            href="https://agentclientprotocol.com/overview/agents"
                            onClick=${(e) => {
                              e.preventDefault();
                              openExternalURL(
                                "https://agentclientprotocol.com/overview/agents",
                              );
                            }}
                            class="text-blue-400 hover:text-blue-300 underline cursor-pointer"
                            >Popular examples</a
                          >${" "} include Auggie and Claude Code. You can
                          configure multiple servers and choose which one to use
                          for each workspace.
                        </p>
                        <button
                          onClick=${() => setShowDiscoverAgents(true)}
                          class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors"
                          title="Discover Agents"
                        >
                          <${SearchIcon} className="w-5 h-5" />
                        </button>
                        <button
                          onClick=${() => setShowAddServer(!showAddServer)}
                          class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${showAddServer
                            ? "bg-slate-700"
                            : ""}"
                          title="Add Server"
                        >
                          <${PlusIcon} className="w-5 h-5" />
                        </button>
                      </div>

                      ${showAddServer &&
                      html`
                        <div
                          class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3"
                        >
                          <div>
                            <label class="block text-sm text-gray-400 mb-1"
                              >Server Name</label
                            >
                            <input
                              type="text"
                              value=${newServerName}
                              onInput=${(e) => setNewServerName(e.target.value)}
                              placeholder="e.g., claude-code"
                              class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                            />
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1"
                              >Command</label
                            >
                            <input
                              type="text"
                              value=${newServerCommand}
                              onInput=${(e) =>
                                setNewServerCommand(e.target.value)}
                              placeholder="e.g., npx -y @anthropic/claude-code-acp"
                              class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                            />
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1"
                              >Type
                              <span class="text-xs text-red-400 ml-1">*</span></label
                            >
                            <select
                              value=${newServerType}
                              onChange=${(e) =>
                                setNewServerType(e.target.value)}
                              class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 ${!newServerType ? "ring-2 ring-amber-500/50" : "focus:ring-blue-500"}"
                            >
                              <option value="">-- Select agent type --</option>
                              ${agentTypes.map(
                                (t) => html`<option key=${t} value=${t}>${t}</option>`,
                              )}
                            </select>
                            <p class="text-xs text-gray-500 mt-1">
                              Servers with the same type share prompts and
                              agent configuration.
                            </p>
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1"
                              >Tags
                              <span class="text-xs text-gray-500"
                                >(optional)</span
                              ></label
                            >
                            <input
                              type="text"
                              value=${newServerTags}
                              onInput=${(e) =>
                                setNewServerTags(e.target.value)}
                              placeholder="e.g., coding, fast-model, production"
                              class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                            />
                            <p class="text-xs text-gray-500 mt-1">
                              Comma-separated tags for categorization
                            </p>
                          </div>
                          ${error &&
                          html`
                            <div
                              class="p-2 bg-red-500/20 border border-red-500/50 rounded-lg text-red-400 text-sm"
                            >
                              ⚠️ ${error}
                            </div>
                          `}
                          <div class="flex justify-end gap-2">
                            <button
                              onClick=${() => {
                                setShowAddServer(false);
                                setNewServerName("");
                                setNewServerCommand("");
                                setNewServerType("");
                                setNewServerTags("");
                                setError("");
                              }}
                              class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                            >
                              Cancel
                            </button>
                            <button
                              onClick=${addServer}
                              class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
                            >
                              Add
                            </button>
                          </div>
                        </div>
                      `}
                      ${acpServers.length === 0
                        ? html`
                            <div class="text-center py-8 text-gray-500">
                              <${ServerEmptyIcon}
                                className="w-12 h-12 mx-auto mb-2 opacity-50"
                              />
                              <p>No ACP servers configured.</p>
                              <p class="text-xs mt-1">
                                Click + to add a server.
                              </p>
                            </div>
                          `
                        : html`
                            <div class="space-y-2">
                              ${sortedAcpServers.map((srv) => {
                                // RC file servers are read-only (cannot edit/delete)
                                const isRCFile = srv.source === "rcfile";
                                return html`
                                  <div
                                    key=${srv.name}
                                    class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 ${isRCFile
                                      ? ""
                                      : "hover:bg-slate-700/30"} transition-colors group ${isRCFile
                                      ? "opacity-80"
                                      : ""}"
                                  >
                                    ${editingServer === srv.name && !isRCFile
                                      ? html`
                                          <${ServerEditForm}
                                            server=${srv}
                                            agentTypes=${agentTypes}
                                            onSave=${(name, cmd, type, autoApprove, env, tags) =>
                                              updateServer(
                                                srv.name,
                                                name,
                                                cmd,
                                                type,
                                                autoApprove,
                                                env,
                                                tags,
                                              )}
                                            onCancel=${() =>
                                              setEditingServer(null)}
                                          />
                                        `
                                      : html`
                                          <div class="flex items-center gap-3">
                                            <div class="flex-1 min-w-0">
                                              <div
                                                class="font-medium text-sm flex items-center gap-2"
                                              >
                                                ${srv.name}
                                                ${srv.type &&
                                                html`
                                                  <span
                                                    class="px-1.5 py-0.5 bg-purple-500/20 text-purple-400 rounded text-xs"
                                                    title="Server type for prompt matching"
                                                  >
                                                    ${srv.type}
                                                  </span>
                                                `}
                                                ${srv.tags &&
                                                srv.tags.length > 0 &&
                                                srv.tags.map(
                                                  (tag) => html`
                                                    <span
                                                      key=${tag}
                                                      class="px-1.5 py-0.5 bg-blue-500/20 text-blue-400 rounded text-xs"
                                                      title="Tag"
                                                    >
                                                      ${tag}
                                                    </span>
                                                  `,
                                                )}
                                                ${isRCFile &&
                                                html`
                                                  <span
                                                    class="flex items-center gap-1 text-xs text-amber-400"
                                                    title="This server is defined in .mittorc and cannot be modified here"
                                                  >
                                                    <${LockIcon}
                                                      className="w-3 h-3"
                                                    />
                                                  </span>
                                                `}
                                                ${srv.prompts?.length > 0 &&
                                                html`
                                                  <span
                                                    class="flex items-center gap-1 text-xs text-blue-400"
                                                    title="${srv.prompts
                                                      .length} server-specific prompt(s)"
                                                  >
                                                    <${LightningIcon}
                                                      className="w-3.5 h-3.5"
                                                    />
                                                    ${srv.prompts.length}
                                                  </span>
                                                `}
                                              </div>
                                              <div
                                                class="text-xs text-gray-500 truncate"
                                                title=${srv.command}
                                              >
                                                ${srv.command}
                                                ${isRCFile &&
                                                html`<span
                                                  class="ml-2 text-amber-500/70"
                                                  >(from .mittorc)</span
                                                >`}
                                              </div>
                                            </div>
                                            ${!isRCFile &&
                                            html`
                                              <button
                                                onClick=${() =>
                                                  duplicateServer(srv.name)}
                                                class="p-1.5 text-gray-500 hover:text-green-400 hover:bg-green-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                                title="Duplicate server"
                                              >
                                                <${DuplicateIcon}
                                                  className="w-4 h-4"
                                                />
                                              </button>
                                              <button
                                                onClick=${() =>
                                                  setEditingServer(srv.name)}
                                                class="p-1.5 text-gray-500 hover:text-blue-400 hover:bg-blue-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                                title="Edit server"
                                              >
                                                <${EditIcon}
                                                  className="w-4 h-4"
                                                />
                                              </button>
                                              <button
                                                onClick=${() =>
                                                  removeServer(srv.name)}
                                                class="p-1.5 text-gray-500 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                                title="Remove server"
                                              >
                                                <${TrashIcon}
                                                  className="w-4 h-4"
                                                />
                                              </button>
                                            `}
                                          </div>
                                        `}
                                  </div>
                                `;
                              })}
                            </div>
                          `}
                    </div>
                  `}

                  <!-- Prompts Tab -->
                  ${activeTab === "prompts" &&
                  html`
                    <div class="space-y-4">
                      <div class="flex items-center justify-between">
                        <p class="text-gray-400 text-sm">
                          Predefined prompts appear as quick-access buttons in
                          the chat input.
                        </p>
                        <button
                          onClick=${() => setShowAddPrompt(!showAddPrompt)}
                          class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${showAddPrompt
                            ? "bg-slate-700"
                            : ""}"
                          title="Add Prompt"
                        >
                          <${PlusIcon} className="w-5 h-5" />
                        </button>
                      </div>

                      <!-- Add New Prompt Form -->
                      ${showAddPrompt &&
                      html`
                        <div
                          class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3"
                        >
                          <div>
                            <label class="block text-sm text-gray-400 mb-1"
                              >Button Label</label
                            >
                            <input
                              type="text"
                              value=${newPromptName}
                              onInput=${(e) => setNewPromptName(e.target.value)}
                              placeholder="e.g., Continue"
                              class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                            />
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1"
                              >Prompt Text</label
                            >
                            <textarea
                              value=${newPromptText}
                              onInput=${(e) => setNewPromptText(e.target.value)}
                              placeholder="e.g., Please continue with the current task."
                              rows="3"
                              class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                            />
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1"
                              >Group (optional)</label
                            >
                            <input
                              type="text"
                              value=${newPromptGroup}
                              onInput=${(e) => setNewPromptGroup(e.target.value)}
                              placeholder="e.g., Tasks, Code Quality"
                              class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                            />
                          </div>
                          <div>
                            <label class="block text-sm text-gray-400 mb-1"
                              >Background Color (optional)</label
                            >
                            <div class="flex items-center gap-2">
                              <input
                                type="color"
                                value=${newPromptColor || "#334155"}
                                onInput=${(e) =>
                                  setNewPromptColor(e.target.value)}
                                class="w-10 h-10 rounded cursor-pointer border border-slate-600"
                                title="Choose background color"
                              />
                              <input
                                type="text"
                                value=${newPromptColor}
                                onInput=${(e) =>
                                  setNewPromptColor(e.target.value)}
                                placeholder="#E8F5E9"
                                class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                              />
                              ${newPromptColor &&
                              html`
                                <button
                                  type="button"
                                  onClick=${() => setNewPromptColor("")}
                                  class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                                  title="Clear color"
                                >
                                  <svg
                                    class="w-4 h-4 text-gray-400"
                                    fill="none"
                                    stroke="currentColor"
                                    viewBox="0 0 24 24"
                                  >
                                    <path
                                      stroke-linecap="round"
                                      stroke-linejoin="round"
                                      stroke-width="2"
                                      d="M6 18L18 6M6 6l12 12"
                                    />
                                  </svg>
                                </button>
                              `}
                            </div>
                          </div>
                          <div class="flex justify-end gap-2">
                            <button
                              onClick=${() => {
                                setShowAddPrompt(false);
                                setNewPromptName("");
                                setNewPromptText("");
                                setNewPromptColor("");
                                setNewPromptGroup("");
                              }}
                              class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                            >
                              Cancel
                            </button>
                            <button
                              onClick=${() => {
                                if (
                                  newPromptName.trim() &&
                                  newPromptText.trim()
                                ) {
                                  const newPrompt = {
                                    name: newPromptName.trim(),
                                    prompt: newPromptText.trim(),
                                  };
                                  if (newPromptColor) {
                                    newPrompt.backgroundColor = newPromptColor;
                                  }
                                  if (newPromptGroup.trim()) {
                                    newPrompt.group = newPromptGroup.trim();
                                  }
                                  setPrompts([...prompts, newPrompt]);
                                  setNewPromptName("");
                                  setNewPromptText("");
                                  setNewPromptColor("");
                                  setNewPromptGroup("");
                                  setShowAddPrompt(false);
                                }
                              }}
                              disabled=${!newPromptName.trim() ||
                              !newPromptText.trim()}
                              class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50"
                            >
                              Add Prompt
                            </button>
                          </div>
                        </div>
                      `}

                      <!-- Prompts List -->
                      <div class="space-y-2">
                        ${prompts.length === 0
                          ? html`
                              <div
                                class="p-4 text-center text-gray-500 text-sm"
                              >
                                No prompts configured. Click + to add one.
                              </div>
                            `
                          : sortedPrompts.map((prompt) => {
                              // Find original index in the unsorted prompts array
                              const originalIndex = prompts.findIndex(
                                (p) =>
                                  p.name === prompt.name &&
                                  p.prompt === prompt.prompt,
                              );
                              return html`
                                <div
                                  key=${prompt.name}
                                  class="p-3 bg-slate-700/20 rounded-lg border transition-all border-slate-600/50"
                                >
                                  ${editingPrompt === originalIndex
                                    ? html`
                                        <${PromptEditForm}
                                          prompt=${prompt}
                                          readOnly=${prompt.source === "file" ||
                                          prompt.source === "workspace"}
                                          onSave=${(name, text, bgColor, group) => {
                                            const updated = [...prompts];
                                            updated[originalIndex] = {
                                              ...prompts[originalIndex],
                                              name,
                                              prompt: text,
                                            };
                                            if (bgColor) {
                                              updated[originalIndex]
                                                .backgroundColor = bgColor;
                                            }
                                            if (group && group.trim()) {
                                              updated[originalIndex].group = group.trim();
                                            } else {
                                              delete updated[originalIndex].group;
                                            }
                                            setPrompts(updated);
                                            setEditingPrompt(null);
                                          }}
                                          onCancel=${() =>
                                            setEditingPrompt(null)}
                                        />
                                      `
                                    : html`
                                        <div
                                          class="flex items-start justify-between gap-3"
                                        >
                                          <div
                                            class="flex items-center gap-2 flex-1 min-w-0"
                                          >
                                            ${prompt.backgroundColor &&
                                            html`
                                              <div
                                                class="w-4 h-4 rounded flex-shrink-0 border border-slate-500"
                                                style=${{
                                                  backgroundColor:
                                                    prompt.backgroundColor,
                                                }}
                                                title=${prompt.backgroundColor}
                                              />
                                            `}
                                            <div class="flex-1 min-w-0">
                                              <div
                                                class="font-medium text-sm text-blue-400 flex items-center gap-2"
                                              >
                                                ${prompt.name}
                                                ${prompt.source === "file" &&
                                                html`
                                                  <span
                                                    class="text-xs px-1.5 py-0.5 bg-slate-600 text-gray-300 rounded"
                                                    title="Loaded from file (read-only)"
                                                    >file</span
                                                  >
                                                `}
                                                ${prompt.source ===
                                                  "workspace" &&
                                                html`
                                                  <span
                                                    class="text-xs px-1.5 py-0.5 bg-purple-600/50 text-purple-200 rounded"
                                                    title="Defined in workspace .mittorc (read-only)"
                                                    >workspace</span
                                                  >
                                                `}
                                              </div>
                                              <div
                                                class="text-xs text-gray-500 mt-1 truncate"
                                              >
                                                ${prompt.prompt}
                                              </div>
                                            </div>
                                          </div>
                                          <div class="flex items-center gap-1">
                                            ${prompt.source !== "file" &&
                                            prompt.source !== "workspace"
                                              ? html`
                                                  <button
                                                    onClick=${() =>
                                                      setEditingPrompt(
                                                        originalIndex,
                                                      )}
                                                    class="p-1.5 hover:bg-slate-700 rounded transition-colors"
                                                    title="Edit"
                                                  >
                                                    <${EditIcon}
                                                      className="w-4 h-4 text-gray-400"
                                                    />
                                                  </button>
                                                  <button
                                                    onClick=${() => {
                                                      const updated =
                                                        prompts.filter(
                                                          (_, i) =>
                                                            i !== originalIndex,
                                                        );
                                                      setPrompts(updated);
                                                    }}
                                                    class="p-1.5 hover:bg-red-500/20 rounded transition-colors"
                                                    title="Delete"
                                                  >
                                                    <${TrashIcon}
                                                      className="w-4 h-4 text-gray-400 hover:text-red-400"
                                                    />
                                                  </button>
                                                `
                                              : html`
                                                  <button
                                                    onClick=${() =>
                                                      setEditingPrompt(
                                                        originalIndex,
                                                      )}
                                                    class="p-1.5 hover:bg-slate-700 rounded transition-colors"
                                                    title="View"
                                                  >
                                                    <${EditIcon}
                                                      className="w-4 h-4 text-gray-500"
                                                    />
                                                  </button>
                                                `}
                                          </div>
                                        </div>
                                      `}
                                </div>
                              `;
                            })}
                      </div>
                    </div>
                  `}

                  <!-- Runners Tab -->
                  ${activeTab === "runners" &&
                  html`
                    <div class="space-y-4">
                      <div
                        class="p-3 bg-amber-500/10 rounded-lg border border-amber-500/30"
                      >
                        <p class="text-amber-400 text-sm leading-relaxed">
                          ⚠️ <strong>Advanced feature:</strong> Configure
                          sandboxing restrictions for each runner type. These
                          are global defaults that apply to all workspaces using
                          that runner type. Misconfigured restrictions can break
                          MCP server access.
                        </p>
                      </div>

                      <p class="text-gray-400 text-sm">
                        Configure per-runner-type restrictions. Workspaces using
                        a specific runner type will inherit these settings.
                        <br />
                        <span class="text-gray-500"
                          >Note: .mittorc settings will override these
                          values.</span
                        >
                      </p>

                      <!-- Runner configurations -->
                      <div class="space-y-3">
                        ${supportedRunners
                          .filter((r) => r.type !== "exec" && r.supported)
                          .map(
                            (runner) => html`
                              <div
                                key=${runner.type}
                                class="border border-slate-600/50 rounded-lg overflow-hidden"
                              >
                                <!-- Runner header (collapsible) -->
                                <button
                                  type="button"
                                  onClick=${() =>
                                    setExpandedRunner(
                                      expandedRunner === runner.type
                                        ? null
                                        : runner.type,
                                    )}
                                  class="w-full flex items-center justify-between p-3 bg-slate-700/30 hover:bg-slate-700/50 transition-colors"
                                >
                                  <div class="flex items-center gap-3">
                                    <${expandedRunner === runner.type
                                      ? ChevronDownIcon
                                      : ChevronRightIcon}
                                      className="w-4 h-4 text-gray-400"
                                    />
                                    <div class="text-left">
                                      <div class="font-medium text-sm">
                                        ${runner.label}
                                      </div>
                                      <div class="text-xs text-gray-500">
                                        ${runner.description}
                                      </div>
                                    </div>
                                  </div>
                                  ${restrictedRunners[runner.type] &&
                                  html`
                                    <span
                                      class="px-2 py-0.5 bg-blue-500/20 text-blue-400 rounded text-xs"
                                    >
                                      Configured
                                    </span>
                                  `}
                                </button>

                                <!-- Expanded content -->
                                ${expandedRunner === runner.type &&
                                html`
                                  <div
                                    class="p-4 space-y-4 border-t border-slate-600/50"
                                  >
                                    <!-- Allow networking toggle -->
                                    <label
                                      class="flex items-center gap-3 cursor-pointer"
                                    >
                                      <input
                                        type="checkbox"
                                        checked=${restrictedRunners[runner.type]
                                          ?.restrictions?.allow_networking !==
                                        false}
                                        onChange=${(e) => {
                                          const newConfig = {
                                            ...(restrictedRunners[
                                              runner.type
                                            ] || {}),
                                            restrictions: {
                                              ...(restrictedRunners[runner.type]
                                                ?.restrictions || {}),
                                              allow_networking:
                                                e.target.checked,
                                            },
                                            merge_strategy:
                                              restrictedRunners[runner.type]
                                                ?.merge_strategy || "extend",
                                          };
                                          setRestrictedRunners({
                                            ...restrictedRunners,
                                            [runner.type]: newConfig,
                                          });
                                        }}
                                        class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                                      />
                                      <div>
                                        <div class="font-medium text-sm">
                                          Allow networking
                                        </div>
                                        <div class="text-xs text-gray-500">
                                          Required for network-based MCP servers
                                        </div>
                                      </div>
                                    </label>

                                    <!-- Allow read folders -->
                                    <div class="space-y-2">
                                      <label
                                        class="text-sm font-medium text-gray-300"
                                      >
                                        Allow read folders
                                      </label>
                                      <div class="space-y-1">
                                        ${(
                                          restrictedRunners[runner.type]
                                            ?.restrictions
                                            ?.allow_read_folders || []
                                        ).map(
                                          (folder, idx) => html`
                                            <div
                                              key=${idx}
                                              class="flex items-center gap-2"
                                            >
                                              <input
                                                type="text"
                                                value=${folder}
                                                onInput=${(e) => {
                                                  const folders = [
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ]?.restrictions
                                                      ?.allow_read_folders ||
                                                      []),
                                                  ];
                                                  folders[idx] = e.target.value;
                                                  const newConfig = {
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ] || {}),
                                                    restrictions: {
                                                      ...(restrictedRunners[
                                                        runner.type
                                                      ]?.restrictions || {}),
                                                      allow_read_folders:
                                                        folders,
                                                    },
                                                    merge_strategy:
                                                      restrictedRunners[
                                                        runner.type
                                                      ]?.merge_strategy ||
                                                      "extend",
                                                  };
                                                  setRestrictedRunners({
                                                    ...restrictedRunners,
                                                    [runner.type]: newConfig,
                                                  });
                                                }}
                                                class="flex-1 px-3 py-1.5 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                                                placeholder="$MITTO_WORKING_DIR"
                                              />
                                              <button
                                                type="button"
                                                onClick=${() => {
                                                  const folders = (
                                                    restrictedRunners[
                                                      runner.type
                                                    ]?.restrictions
                                                      ?.allow_read_folders || []
                                                  ).filter((_, i) => i !== idx);
                                                  const newConfig = {
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ] || {}),
                                                    restrictions: {
                                                      ...(restrictedRunners[
                                                        runner.type
                                                      ]?.restrictions || {}),
                                                      allow_read_folders:
                                                        folders,
                                                    },
                                                    merge_strategy:
                                                      restrictedRunners[
                                                        runner.type
                                                      ]?.merge_strategy ||
                                                      "extend",
                                                  };
                                                  setRestrictedRunners({
                                                    ...restrictedRunners,
                                                    [runner.type]: newConfig,
                                                  });
                                                }}
                                                class="p-1 text-gray-400 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors"
                                                title="Remove folder"
                                              >
                                                <${TrashIcon}
                                                  className="w-4 h-4"
                                                />
                                              </button>
                                            </div>
                                          `,
                                        )}
                                        <button
                                          type="button"
                                          onClick=${() => {
                                            const folders = [
                                              ...(restrictedRunners[runner.type]
                                                ?.restrictions
                                                ?.allow_read_folders || []),
                                              "",
                                            ];
                                            const newConfig = {
                                              ...(restrictedRunners[
                                                runner.type
                                              ] || {}),
                                              restrictions: {
                                                ...(restrictedRunners[
                                                  runner.type
                                                ]?.restrictions || {}),
                                                allow_read_folders: folders,
                                              },
                                              merge_strategy:
                                                restrictedRunners[runner.type]
                                                  ?.merge_strategy || "extend",
                                            };
                                            setRestrictedRunners({
                                              ...restrictedRunners,
                                              [runner.type]: newConfig,
                                            });
                                          }}
                                          class="flex items-center gap-1 px-2 py-1 text-xs text-blue-400 hover:text-blue-300 hover:bg-blue-500/10 rounded transition-colors"
                                        >
                                          <${PlusIcon} className="w-3 h-3" />
                                          Add folder
                                        </button>
                                      </div>
                                    </div>

                                    <!-- Allow write folders -->
                                    <div class="space-y-2">
                                      <label
                                        class="text-sm font-medium text-gray-300"
                                      >
                                        Allow write folders
                                      </label>
                                      <div class="space-y-1">
                                        ${(
                                          restrictedRunners[runner.type]
                                            ?.restrictions
                                            ?.allow_write_folders || []
                                        ).map(
                                          (folder, idx) => html`
                                            <div
                                              key=${idx}
                                              class="flex items-center gap-2"
                                            >
                                              <input
                                                type="text"
                                                value=${folder}
                                                onInput=${(e) => {
                                                  const folders = [
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ]?.restrictions
                                                      ?.allow_write_folders ||
                                                      []),
                                                  ];
                                                  folders[idx] = e.target.value;
                                                  const newConfig = {
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ] || {}),
                                                    restrictions: {
                                                      ...(restrictedRunners[
                                                        runner.type
                                                      ]?.restrictions || {}),
                                                      allow_write_folders:
                                                        folders,
                                                    },
                                                    merge_strategy:
                                                      restrictedRunners[
                                                        runner.type
                                                      ]?.merge_strategy ||
                                                      "extend",
                                                  };
                                                  setRestrictedRunners({
                                                    ...restrictedRunners,
                                                    [runner.type]: newConfig,
                                                  });
                                                }}
                                                class="flex-1 px-3 py-1.5 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                                                placeholder="$MITTO_WORKING_DIR"
                                              />
                                              <button
                                                type="button"
                                                onClick=${() => {
                                                  const folders = (
                                                    restrictedRunners[
                                                      runner.type
                                                    ]?.restrictions
                                                      ?.allow_write_folders ||
                                                    []
                                                  ).filter((_, i) => i !== idx);
                                                  const newConfig = {
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ] || {}),
                                                    restrictions: {
                                                      ...(restrictedRunners[
                                                        runner.type
                                                      ]?.restrictions || {}),
                                                      allow_write_folders:
                                                        folders,
                                                    },
                                                    merge_strategy:
                                                      restrictedRunners[
                                                        runner.type
                                                      ]?.merge_strategy ||
                                                      "extend",
                                                  };
                                                  setRestrictedRunners({
                                                    ...restrictedRunners,
                                                    [runner.type]: newConfig,
                                                  });
                                                }}
                                                class="p-1 text-gray-400 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors"
                                                title="Remove folder"
                                              >
                                                <${TrashIcon}
                                                  className="w-4 h-4"
                                                />
                                              </button>
                                            </div>
                                          `,
                                        )}
                                        <button
                                          type="button"
                                          onClick=${() => {
                                            const folders = [
                                              ...(restrictedRunners[runner.type]
                                                ?.restrictions
                                                ?.allow_write_folders || []),
                                              "",
                                            ];
                                            const newConfig = {
                                              ...(restrictedRunners[
                                                runner.type
                                              ] || {}),
                                              restrictions: {
                                                ...(restrictedRunners[
                                                  runner.type
                                                ]?.restrictions || {}),
                                                allow_write_folders: folders,
                                              },
                                              merge_strategy:
                                                restrictedRunners[runner.type]
                                                  ?.merge_strategy || "extend",
                                            };
                                            setRestrictedRunners({
                                              ...restrictedRunners,
                                              [runner.type]: newConfig,
                                            });
                                          }}
                                          class="flex items-center gap-1 px-2 py-1 text-xs text-blue-400 hover:text-blue-300 hover:bg-blue-500/10 rounded transition-colors"
                                        >
                                          <${PlusIcon} className="w-3 h-3" />
                                          Add folder
                                        </button>
                                      </div>
                                    </div>

                                    ${runner.type === "docker" &&
                                    html`
                                      <!-- Docker-specific settings -->
                                      <div
                                        class="space-y-2 pt-2 border-t border-slate-600/50"
                                      >
                                        <label
                                          class="text-sm font-medium text-gray-300"
                                        >
                                          Docker Settings
                                        </label>
                                        <div class="grid grid-cols-3 gap-3">
                                          <div>
                                            <label class="text-xs text-gray-500"
                                              >Image</label
                                            >
                                            <input
                                              type="text"
                                              value=${restrictedRunners[
                                                runner.type
                                              ]?.restrictions?.docker?.image ||
                                              ""}
                                              onInput=${(e) => {
                                                const newConfig = {
                                                  ...(restrictedRunners[
                                                    runner.type
                                                  ] || {}),
                                                  restrictions: {
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ]?.restrictions || {}),
                                                    docker: {
                                                      ...(restrictedRunners[
                                                        runner.type
                                                      ]?.restrictions?.docker ||
                                                        {}),
                                                      image: e.target.value,
                                                    },
                                                  },
                                                  merge_strategy:
                                                    restrictedRunners[
                                                      runner.type
                                                    ]?.merge_strategy ||
                                                    "extend",
                                                };
                                                setRestrictedRunners({
                                                  ...restrictedRunners,
                                                  [runner.type]: newConfig,
                                                });
                                              }}
                                              class="w-full px-2 py-1 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                                              placeholder="alpine:latest"
                                            />
                                          </div>
                                          <div>
                                            <label class="text-xs text-gray-500"
                                              >Memory Limit</label
                                            >
                                            <input
                                              type="text"
                                              value=${restrictedRunners[
                                                runner.type
                                              ]?.restrictions?.docker
                                                ?.memory_limit || ""}
                                              onInput=${(e) => {
                                                const newConfig = {
                                                  ...(restrictedRunners[
                                                    runner.type
                                                  ] || {}),
                                                  restrictions: {
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ]?.restrictions || {}),
                                                    docker: {
                                                      ...(restrictedRunners[
                                                        runner.type
                                                      ]?.restrictions?.docker ||
                                                        {}),
                                                      memory_limit:
                                                        e.target.value,
                                                    },
                                                  },
                                                  merge_strategy:
                                                    restrictedRunners[
                                                      runner.type
                                                    ]?.merge_strategy ||
                                                    "extend",
                                                };
                                                setRestrictedRunners({
                                                  ...restrictedRunners,
                                                  [runner.type]: newConfig,
                                                });
                                              }}
                                              class="w-full px-2 py-1 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                                              placeholder="4g"
                                            />
                                          </div>
                                          <div>
                                            <label class="text-xs text-gray-500"
                                              >CPU Limit</label
                                            >
                                            <input
                                              type="text"
                                              value=${restrictedRunners[
                                                runner.type
                                              ]?.restrictions?.docker
                                                ?.cpu_limit || ""}
                                              onInput=${(e) => {
                                                const newConfig = {
                                                  ...(restrictedRunners[
                                                    runner.type
                                                  ] || {}),
                                                  restrictions: {
                                                    ...(restrictedRunners[
                                                      runner.type
                                                    ]?.restrictions || {}),
                                                    docker: {
                                                      ...(restrictedRunners[
                                                        runner.type
                                                      ]?.restrictions?.docker ||
                                                        {}),
                                                      cpu_limit: e.target.value,
                                                    },
                                                  },
                                                  merge_strategy:
                                                    restrictedRunners[
                                                      runner.type
                                                    ]?.merge_strategy ||
                                                    "extend",
                                                };
                                                setRestrictedRunners({
                                                  ...restrictedRunners,
                                                  [runner.type]: newConfig,
                                                });
                                              }}
                                              class="w-full px-2 py-1 bg-slate-700 rounded text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                                              placeholder="2.0"
                                            />
                                          </div>
                                        </div>
                                      </div>
                                    `}

                                    <!-- Merge strategy -->
                                    <div class="flex items-center gap-3 pt-2">
                                      <label class="text-sm text-gray-400"
                                        >Merge Strategy:</label
                                      >
                                      <select
                                        value=${restrictedRunners[runner.type]
                                          ?.merge_strategy || "extend"}
                                        onChange=${(e) => {
                                          const newConfig = {
                                            ...(restrictedRunners[
                                              runner.type
                                            ] || {}),
                                            merge_strategy: e.target.value,
                                          };
                                          setRestrictedRunners({
                                            ...restrictedRunners,
                                            [runner.type]: newConfig,
                                          });
                                        }}
                                        class="px-2 py-1 bg-slate-700 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                      >
                                        <option value="extend">
                                          extend (merge with workspace config)
                                        </option>
                                        <option value="replace">
                                          replace (ignore workspace config)
                                        </option>
                                      </select>
                                    </div>

                                    <!-- Reset to defaults button -->
                                    <div
                                      class="flex justify-end gap-2 pt-2 border-t border-slate-600/50"
                                    >
                                      <button
                                        type="button"
                                        onClick=${() => {
                                          // Clear config for this runner
                                          const newRunners = {
                                            ...restrictedRunners,
                                          };
                                          delete newRunners[runner.type];
                                          setRestrictedRunners(newRunners);
                                        }}
                                        class="px-3 py-1.5 text-xs text-gray-400 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors"
                                      >
                                        Clear Configuration
                                      </button>
                                      <button
                                        type="button"
                                        onClick=${() => {
                                          // Reset to defaults from server
                                          if (runnerDefaults[runner.type]) {
                                            setRestrictedRunners({
                                              ...restrictedRunners,
                                              [runner.type]:
                                                runnerDefaults[runner.type],
                                            });
                                          }
                                        }}
                                        class="px-3 py-1.5 text-xs text-blue-400 hover:text-blue-300 hover:bg-blue-500/10 rounded transition-colors"
                                      >
                                        Reset to Defaults
                                      </button>
                                    </div>
                                  </div>
                                `}
                              </div>
                            `,
                          )}

                        <!-- Show unsupported runners (disabled) -->
                        ${supportedRunners
                          .filter((r) => r.type !== "exec" && !r.supported)
                          .map(
                            (runner) => html`
                              <div
                                key=${runner.type}
                                class="border border-slate-600/30 rounded-lg overflow-hidden opacity-50"
                              >
                                <div
                                  class="flex items-center justify-between p-3 bg-slate-700/20"
                                >
                                  <div class="flex items-center gap-3">
                                    <${ChevronRightIcon}
                                      className="w-4 h-4 text-gray-500"
                                    />
                                    <div>
                                      <div
                                        class="font-medium text-sm text-gray-500"
                                      >
                                        ${runner.label}
                                      </div>
                                      <div class="text-xs text-gray-600">
                                        ${runner.warning ||
                                        "Not supported on this platform"}
                                      </div>
                                    </div>
                                  </div>
                                </div>
                              </div>
                            `,
                          )}
                      </div>
                    </div>
                  `}

                  <!-- Permissions Tab -->
                  ${activeTab === "permissions" &&
                  html`
                    <div class="space-y-4">
                      <p class="text-gray-400 text-sm">
                        Configure how permission requests from AI agents are
                        handled.
                      </p>

                      <!-- Global Permissions Section -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          Global Settings
                        </h4>

                        <label
                          class="flex items-center gap-3 p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${globalAutoApprove}
                            onChange=${(e) =>
                              setGlobalAutoApprove(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div class="flex-1">
                            <div class="font-medium text-sm">
                              Auto-approve All Permissions
                            </div>
                            <div class="text-xs text-gray-500">
                              Automatically approve all permission requests from
                              AI agents without showing a dialog. This is the
                              default behavior.
                            </div>
                          </div>
                        </label>

                        <div
                          class="p-3 bg-slate-800/50 rounded-lg border border-slate-700"
                        >
                          <p class="text-gray-300 text-sm leading-relaxed">
                            <span class="text-blue-400 font-medium"
                              >Permission hierarchy:</span
                            >${" "}
                            Per-workspace settings can enable auto-approve even
                            when this global setting is off. Configure
                            workspace-specific settings in the Workspaces dialog.
                          </p>
                        </div>

                        ${!globalAutoApprove &&
                        html`
                          <div
                            class="p-3 bg-amber-500/10 rounded-lg border border-amber-500/30"
                          >
                            <p class="text-amber-400 text-sm leading-relaxed">
                              ⚠️ <strong>Note:</strong> When auto-approve is
                              disabled, you will need to manually approve or deny
                              each permission request from the agent. This may
                              interrupt your workflow but provides more control
                              over agent actions.
                            </p>
                          </div>
                        `}
                      </div>

                      <!-- Archive Settings -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          Archive Settings
                        </h4>
                        <div
                          class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Auto-archive inactive conversations
                              </div>
                              <div class="text-xs text-gray-500">
                                Automatically archive conversations after the
                                specified period of inactivity
                              </div>
                            </div>
                            <select
                              value=${autoArchiveInactiveAfter}
                              onChange=${(e) =>
                                setAutoArchiveInactiveAfter(e.target.value)}
                              class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500"
                            >
                              <option value="">Disabled</option>
                              <option value="1d">After 1 day</option>
                              <option value="1w">After 1 week</option>
                              <option value="1m">After 1 month</option>
                              <option value="3m">After 3 months</option>
                            </select>
                          </div>
                        </div>
                        <div
                          class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Auto-delete archived conversations
                              </div>
                              <div class="text-xs text-gray-500">
                                Automatically delete archived conversations
                                after the specified period
                              </div>
                            </div>
                            <select
                              value=${archiveRetentionPeriod}
                              onChange=${(e) =>
                                setArchiveRetentionPeriod(e.target.value)}
                              class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500"
                            >
                              <option value="never">Never</option>
                              <option value="1d">After 1 day</option>
                              <option value="1w">After 1 week</option>
                              <option value="1m">After 1 month</option>
                              <option value="3m">After 3 months</option>
                            </select>
                          </div>
                        </div>
                      </div>

                      <!-- Child Conversations Limit -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          Child Conversations
                        </h4>
                        <div
                          class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Max Child Conversations
                              </div>
                              <div class="text-xs text-gray-500">
                                Maximum number of child conversations an AI agent
                                can spawn via MCP. Auto-created children are not
                                counted. Set to 0 for unlimited.
                              </div>
                            </div>
                            <input
                              type="number"
                              min="0"
                              max="100"
                              value=${maxChildConversations}
                              onChange=${(e) =>
                                setMaxChildConversations(
                                  parseInt(e.target.value, 10) || 0,
                                )}
                              class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm w-20 text-center focus:ring-blue-500 focus:border-blue-500"
                            />
                          </div>
                        </div>
                      </div>

                      <!-- Default Flags for New Conversations -->
                      ${availableFlags.length > 0 &&
                      html`
                        <div class="space-y-3">
                          <h4 class="text-sm font-medium text-gray-300">
                            Default Flags for New Conversations
                          </h4>
                          <p class="text-xs text-gray-500">
                            These flags will be enabled by default when creating
                            new conversations.
                          </p>
                          <div
                            class="bg-slate-700/20 rounded-lg border border-slate-600/50 overflow-hidden"
                          >
                            <table class="w-full text-sm">
                              <tbody>
                                ${availableFlags.map(
                                  (flag) => html`
                                    <tr
                                      key=${flag.name}
                                      class="border-b border-slate-600/30 last:border-b-0 hover:bg-slate-700/30 transition-colors"
                                    >
                                      <td class="p-3 w-10">
                                        <input
                                          type="checkbox"
                                          checked=${defaultFlags[flag.name] ||
                                          false}
                                          onChange=${(e) => {
                                            const newFlags = {
                                              ...defaultFlags,
                                            };
                                            if (e.target.checked) {
                                              newFlags[flag.name] = true;
                                            } else {
                                              delete newFlags[flag.name];
                                            }
                                            setDefaultFlags(newFlags);
                                          }}
                                          class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0 cursor-pointer"
                                        />
                                      </td>
                                      <td class="p-3">
                                        <div class="font-medium">
                                          ${flag.label}
                                        </div>
                                        <div class="text-xs text-gray-500">
                                          ${flag.description}
                                        </div>
                                      </td>
                                    </tr>
                                  `,
                                )}
                              </tbody>
                            </table>
                          </div>
                        </div>
                      `}

                      <!-- Message Display -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          Message Display
                        </h4>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${actionButtonsEnabled}
                            onChange=${(e) =>
                              setActionButtonsEnabled(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div class="flex-1">
                            <div class="font-medium text-sm">
                              Follow-up Suggestions
                            </div>
                            <div class="text-xs text-gray-500">
                              Analyze agent responses to suggest clickable
                              follow-up options (uses auxiliary conversation)
                            </div>
                          </div>
                        </label>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${externalImagesEnabled}
                            onChange=${(e) =>
                              setExternalImagesEnabled(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div class="flex-1">
                            <div class="font-medium text-sm">
                              Allow External Images
                            </div>
                            <div class="text-xs text-gray-500">
                              Load images from external HTTPS sources in
                              messages (requires restart, may expose your IP to
                              external servers)
                            </div>
                          </div>
                        </label>
                      </div>
                    </div>
                  `}

                  <!-- Web Tab -->
                  ${activeTab === "web" &&
                  html`
                    <div class="space-y-4">
                      <p class="text-gray-400 text-sm">
                        Configure external access
                        settings${authEnabled ? " and lifecycle hooks" : ""}.
                      </p>

                      <!-- External Access Section -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          External Access
                        </h4>

                        <label
                          class="flex items-center gap-3 p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${authEnabled}
                            onChange=${(e) => setAuthEnabled(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Allow External Access
                            </div>
                            <div class="text-xs text-gray-500">
                              Listen on all interfaces (0.0.0.0) and require
                              authentication
                            </div>
                          </div>
                        </label>

                        ${authEnabled &&
                        html`
                          <!-- Port and status -->
                          <div
                            class="p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 space-y-3"
                          >
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-gray-400">Port</label>
                              <input
                                type="number"
                                value=${externalPort}
                                onInput=${(e) =>
                                  setExternalPort(e.target.value)}
                                placeholder="random"
                                min="1024"
                                max="65535"
                                class="w-24 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                              />
                              <span class="text-xs text-gray-500"
                                >(leave empty for random)</span
                              >
                            </div>
                            ${externalEnabled &&
                            currentExternalPort &&
                            html`
                              <div class="text-xs text-green-400">
                                ✓ External access active on port${" "}
                                ${currentExternalPort}
                              </div>
                            `}
                          </div>

                          <!-- Authentication Methods -->
                          <div class="space-y-3">
                            <h5 class="text-sm font-medium text-gray-400">
                              Authentication
                            </h5>
                            <p class="text-xs text-gray-500">
                              At least one authentication method is required for
                              external access.
                            </p>

                            <!-- Simple Auth (Username/Password) -->
                            <div
                              class="p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 space-y-3"
                            >
                              <label class="flex items-center gap-3 cursor-pointer">
                                <input
                                  type="checkbox"
                                  checked=${!!authUsername.trim()}
                                  onChange=${(e) => {
                                    if (!e.target.checked) {
                                      setAuthUsername("");
                                      setAuthPassword("");
                                    } else {
                                      setAuthUsername(authUsername || "admin");
                                    }
                                  }}
                                  class="w-4 h-4 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                                />
                                <div>
                                  <div class="font-medium text-sm">
                                    Username / Password
                                  </div>
                                  <div class="text-xs text-gray-500">
                                    Simple credentials for login
                                  </div>
                                </div>
                              </label>
                              ${authUsername.trim() &&
                              html`
                                <div class="flex items-center gap-4 pl-7">
                                  <div class="flex items-center gap-2">
                                    <label class="text-sm text-gray-400"
                                      >Username</label
                                    >
                                    <input
                                      type="text"
                                      value=${authUsername}
                                      onInput=${(e) =>
                                        setAuthUsername(e.target.value)}
                                      placeholder="admin"
                                      class="w-28 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                    />
                                  </div>
                                  <div class="flex items-center gap-2">
                                    <label class="text-sm text-gray-400"
                                      >Password</label
                                    >
                                    <input
                                      type="password"
                                      value=${authPassword}
                                      onInput=${(e) => {
                                        setAuthPassword(e.target.value);
                                        setAuthPasswordUnchanged(false);
                                      }}
                                      placeholder=${authPasswordUnchanged
                                        ? "••••••••"
                                        : "Enter password"}
                                      class="w-28 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                    />
                                  </div>
                                </div>
                              `}
                            </div>

                            <!-- Cloudflare Access Auth -->
                            <div
                              class="p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 space-y-3"
                            >
                              <label class="flex items-center gap-3 cursor-pointer">
                                <input
                                  type="checkbox"
                                  checked=${cfEnabled}
                                  onChange=${(e) =>
                                    setCfEnabled(e.target.checked)}
                                  class="w-4 h-4 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                                />
                                <div>
                                  <div class="font-medium text-sm">
                                    Cloudflare Access
                                  </div>
                                  <div class="text-xs text-gray-500">
                                    SSO/OAuth via Cloudflare Access JWT
                                    validation
                                  </div>
                                </div>
                              </label>
                              ${cfEnabled &&
                              html`
                                <div class="space-y-2 pl-7">
                                  <div class="flex items-center gap-2">
                                    <label
                                      class="text-sm text-gray-400 w-28"
                                      >Team Domain</label
                                    >
                                    <input
                                      type="text"
                                      value=${cfTeamDomain}
                                      onInput=${(e) =>
                                        setCfTeamDomain(e.target.value)}
                                      placeholder="yourteam.cloudflareaccess.com"
                                      class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                    />
                                  </div>
                                  <div class="flex items-center gap-2">
                                    <label
                                      class="text-sm text-gray-400 w-28"
                                      >Audience</label
                                    >
                                    <input
                                      type="text"
                                      value=${cfAudience}
                                      onInput=${(e) =>
                                        setCfAudience(e.target.value)}
                                      placeholder="Application AUD tag"
                                      class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                                    />
                                  </div>
                                </div>
                              `}
                            </div>
                          </div>

                          <!-- Lifecycle Hooks -->
                          <div
                            class="p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 space-y-3"
                          >
                            <h5 class="text-sm font-medium text-gray-300">
                              Lifecycle Hooks
                            </h5>
                            <p class="text-xs text-gray-500">
                              Commands to run when external access starts/stops
                              (e.g., for tunneling).${" "}
                              <button
                                type="button"
                                onClick=${() =>
                                  openExternalURL(
                                    "https://github.com/inercia/mitto/blob/main/docs/config/ext-access.md",
                                  )}
                                class="text-blue-400 hover:text-blue-300 underline cursor-pointer"
                              >
                                Learn more
                              </button>
                            </p>
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-gray-400 w-12"
                                >Up</label
                              >
                              <input
                                type="text"
                                value=${hookUpCommand}
                                onInput=${(e) =>
                                  setHookUpCommand(e.target.value)}
                                placeholder="e.g., cloudflared tunnel --url http://localhost:$PORT"
                                class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                              />
                            </div>
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-gray-400 w-12"
                                >Down</label
                              >
                              <input
                                type="text"
                                value=${hookDownCommand}
                                onInput=${(e) =>
                                  setHookDownCommand(e.target.value)}
                                placeholder="e.g., pkill cloudflared"
                                class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                              />
                            </div>
                            <div class="flex items-center gap-2 mt-2">
                              <label class="text-sm text-gray-400 w-12"
                                >URL</label
                              >
                              <input
                                type="text"
                                value=${hookExternalAddress}
                                onInput=${(e) =>
                                  setHookExternalAddress(e.target.value)}
                                placeholder="e.g., https://mitto.example.com"
                                class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                              />
                            </div>
                            <p class="text-xs text-gray-600 mt-1">
                              If set, Mitto monitors this URL and restarts
                              hooks if unreachable.
                            </p>
                          </div>
                        `}
                      </div>

                      <!-- Access Log Section -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          Access Log
                        </h4>

                        <label
                          class="flex items-center gap-3 p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${accessLogEnabled}
                            onChange=${(e) =>
                              setAccessLogEnabled(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Enable Access Log
                            </div>
                            <div class="text-xs text-gray-500">
                              Log security-relevant events (login attempts,
                              unauthorized access, external requests) to a
                              rotating log file
                            </div>
                          </div>
                        </label>
                      </div>
                    </div>
                  `}

                  <!-- UI Tab -->
                  ${activeTab === "ui" &&
                  html`
                    <div class="space-y-4">
                      <!-- Appearance Settings (all platforms) -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          Appearance
                        </h4>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${followSystemTheme}
                            onChange=${(e) =>
                              handleFollowSystemThemeChange(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Follow system theme
                            </div>
                            <div class="text-xs text-gray-500">
                              Automatically switch between light and dark mode
                              based on your system preferences
                            </div>
                          </div>
                        </label>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${followSystemReducedMotion}
                            onChange=${(e) =>
                              handleFollowSystemReducedMotionChange(
                                e.target.checked,
                              )}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Follow system reduced motion
                            </div>
                            <div class="text-xs text-gray-500">
                              Automatically reduce animations based on your
                              system accessibility preferences
                            </div>
                          </div>
                        </label>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors ${followSystemReducedMotion
                            ? "opacity-50"
                            : ""}"
                        >
                          <input
                            type="checkbox"
                            checked=${reduceAnimations}
                            onChange=${(e) =>
                              handleReduceAnimationsChange(e.target.checked)}
                            disabled=${followSystemReducedMotion}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0 ${followSystemReducedMotion
                              ? "cursor-not-allowed"
                              : ""}"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Reduce animations
                            </div>
                            <div class="text-xs text-gray-500">
                              ${followSystemReducedMotion
                                ? "Controlled by system preference"
                                : "Replace pulsing and blinking animations with static indicators"}
                            </div>
                          </div>
                        </label>
                        <div
                          class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Prompt sorting
                              </div>
                              <div class="text-xs text-gray-500">
                                How to sort prompts in the dropdown menu
                              </div>
                            </div>
                            <select
                              value=${promptSortMode}
                              onChange=${(e) =>
                                handlePromptSortModeChange(e.target.value)}
                              class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500"
                            >
                              <option value="alphabetical">Alphabetical</option>
                              <option value="color">By Color</option>
                            </select>
                          </div>
                        </div>
                        <div
                          class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Input box font
                              </div>
                              <div class="text-xs text-gray-500">
                                Font family for the message compose area
                              </div>
                            </div>
                            <select
                              value=${inputFontFamily}
                              onChange=${(e) =>
                                setInputFontFamily(e.target.value)}
                              class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500"
                            >
                              <option value="system">System Default</option>
                              <option value="sans-serif">Sans-Serif</option>
                              <option value="serif">Serif</option>
                              <option value="monospace">Monospace</option>
                              <option value="menlo">Menlo</option>
                              <option value="monaco">Monaco</option>
                              <option value="consolas">Consolas</option>
                              <option value="courier-new">Courier New</option>
                              <option value="jetbrains-mono">
                                JetBrains Mono
                              </option>
                              <option value="sf-mono">SF Mono</option>
                              <option value="cascadia-code">
                                Cascadia Code
                              </option>
                            </select>
                          </div>
                        </div>
                        <div
                          class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Send message shortcut
                              </div>
                              <div class="text-xs text-gray-500">
                                Key combination to send messages
                              </div>
                            </div>
                            <select
                              value=${sendKeyMode}
                              onChange=${(e) => setSendKeyMode(e.target.value)}
                              class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500"
                            >
                              <option value="enter">
                                Enter to send (${navigator.platform?.includes("Mac")
                                  ? "⌘"
                                  : "Ctrl"}+Enter to queue)
                              </option>
                              <option value="ctrl-enter">
                                ${navigator.platform?.includes("Mac")
                                  ? "⌘"
                                  : "Ctrl"}+Enter to send (${navigator.platform?.includes("Mac")
                                  ? "⌘⇧"
                                  : "Ctrl+Shift"}+Enter to queue)
                              </option>
                            </select>
                          </div>
                        </div>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${singleExpandedGroup}
                            onChange=${(e) => {
                              const checked = e.target.checked;
                              setSingleExpandedGroup(checked);
                              // When accordion mode is enabled, force cycling to "all"
                              if (checked) {
                                setConversationCyclingMode(CYCLING_MODE.ALL);
                              }
                            }}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Accordion mode for groups
                            </div>
                            <div class="text-xs text-gray-500">
                              When grouping is enabled, only one group can be
                              expanded at a time
                            </div>
                          </div>
                        </label>
                        <div
                          class="p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 ${singleExpandedGroup ? "opacity-50" : ""}"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Conversation cycling
                              </div>
                              <div class="text-xs text-gray-500">
                                ${singleExpandedGroup
                                  ? "Requires accordion mode to be disabled"
                                  : "Which conversations to include when using keyboard/swipe navigation"}
                              </div>
                            </div>
                            <select
                              value=${singleExpandedGroup ? CYCLING_MODE.ALL : conversationCyclingMode}
                              onChange=${(e) =>
                                setConversationCyclingMode(e.target.value)}
                              disabled=${singleExpandedGroup}
                              class="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:ring-blue-500 focus:border-blue-500 ${singleExpandedGroup ? "cursor-not-allowed" : ""}"
                            >
                              ${CYCLING_MODE_OPTIONS.map(
                                (opt) => html`
                                  <option key=${opt.value} value=${opt.value}>
                                    ${opt.label}
                                  </option>
                                `,
                              )}
                            </select>
                          </div>
                        </div>
                      </div>

                      <!-- Confirmation Settings (all platforms) -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-gray-300">
                          Confirmations
                        </h4>
                        <label
                          class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${confirmDeleteSession}
                            onChange=${(e) =>
                              setConfirmDeleteSession(e.target.checked)}
                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Confirm before deleting conversations
                            </div>
                            <div class="text-xs text-gray-500">
                              Show a confirmation dialog when deleting a
                              conversation
                            </div>
                          </div>
                        </label>
                        ${isMacApp &&
                        html`
                          <label
                            class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${confirmQuitWithRunningSessions}
                              onChange=${(e) =>
                                setConfirmQuitWithRunningSessions(
                                  e.target.checked,
                                )}
                              class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Confirm before quitting with active
                                conversations
                              </div>
                              <div class="text-xs text-gray-500">
                                Show a confirmation dialog when quitting while
                                an agent is responding
                              </div>
                            </div>
                          </label>
                        `}
                      </div>

                      <!-- macOS-specific settings -->
                      ${isMacApp &&
                      html`
                        <div class="space-y-3">
                          <h4 class="text-sm font-medium text-gray-300">
                            macOS Settings
                          </h4>
                          <label
                            class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${agentCompletedSound}
                              onChange=${(e) =>
                                setAgentCompletedSound(e.target.checked)}
                              class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Play sound when agent completes
                              </div>
                              <div class="text-xs text-gray-500">
                                Play a notification sound when the AI finishes
                                responding
                              </div>
                            </div>
                          </label>
                          <label
                            class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${nativeNotifications}
                              onChange=${(e) => {
                                // Simply save the preference - permission will be requested on app restart
                                setNativeNotifications(e.target.checked);
                              }}
                              class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Native notifications
                              </div>
                              <div class="text-xs text-gray-500">
                                Show notifications in macOS Notification Center
                                (requires restart)
                                ${notificationPermissionStatus === 1
                                  ? html`<span class="text-yellow-500 ml-1"
                                      >(permission denied in System
                                      Settings)</span
                                    >`
                                  : ""}
                              </div>
                            </div>
                          </label>
                          <label
                            class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${showInAllSpaces}
                              onChange=${(e) =>
                                setShowInAllSpaces(e.target.checked)}
                              class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Show in all Spaces
                              </div>
                              <div class="text-xs text-gray-500">
                                Make the window visible in all macOS Spaces
                                (requires restart)
                              </div>
                            </div>
                          </label>
                          ${loginItemSupported &&
                          html`
                            <label
                              class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                            >
                              <input
                                type="checkbox"
                                checked=${startAtLogin}
                                onChange=${(e) =>
                                  setStartAtLogin(e.target.checked)}
                                class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                              />
                              <div>
                                <div class="font-medium text-sm">
                                  Start at Login
                                </div>
                                <div class="text-xs text-gray-500">
                                  Launch Mitto automatically when you log in
                                </div>
                              </div>
                            </label>
                          `}

                          <!-- Badge Click Action -->
                          <label
                            class="flex items-center gap-3 p-3 bg-slate-700/20 rounded-lg border border-slate-600/50 cursor-pointer hover:bg-slate-700/30 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${badgeClickEnabled}
                              onChange=${(e) =>
                                setBadgeClickEnabled(e.target.checked)}
                              class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                            />
                            <div class="flex-1">
                              <div class="font-medium text-sm">
                                Workspace badge click action
                              </div>
                              <div class="text-xs text-gray-500">
                                Click workspace badge in conversation list to
                                run a command
                              </div>
                            </div>
                          </label>
                          ${badgeClickEnabled &&
                          html`
                            <div
                              class="p-4 bg-slate-700/20 rounded-lg border border-slate-600/50 space-y-2"
                            >
                              <div class="flex items-center gap-2">
                                <label class="text-sm text-gray-400 w-20"
                                  >Command</label
                                >
                                <input
                                  type="text"
                                  value=${badgeClickCommand}
                                  onInput=${(e) =>
                                    setBadgeClickCommand(e.target.value)}
                                  placeholder="open \${MITTO_WORKING_DIR}"
                                  class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                                />
                              </div>
                              <p class="text-xs text-gray-500">
                                Use${" "}
                                <code class="bg-slate-600 px-1 rounded"
                                  >\${MITTO_WORKING_DIR}</code
                                >${" "} as placeholder for the workspace path
                              </p>
                            </div>
                          `}
                        </div>
                      `}

                    </div>
                  `}
                `}
          </div>
        </div>

        <!-- Footer -->
        <div class="p-4 border-t border-slate-700">
          ${error &&
          html`
            <div
              class="mb-3 p-3 bg-red-500/20 border border-red-500/50 rounded-lg text-red-400 text-sm"
            >
              ${error}
            </div>
          `}
          ${warning &&
          html`
            <div
              class="mb-3 p-3 bg-yellow-500/20 border border-yellow-500/50 rounded-lg text-yellow-400 text-sm"
            >
              ${warning}
            </div>
          `}
          ${success &&
          html`
            <div
              class="mb-3 p-3 bg-green-500/20 border border-green-500/50 rounded-lg text-green-400 text-sm"
            >
              ${success}
            </div>
          `}
          <div class="flex justify-end gap-3">
            ${canClose &&
            html`
              <button
                onClick=${handleClose}
                class="px-4 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors"
              >
                Cancel
              </button>
            `}
            <button
              onClick=${handleSave}
              disabled=${saving}
              class="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50 flex items-center gap-2"
            >
              ${saving
                ? html`
                    <${SpinnerIcon} className="w-4 h-4" />
                    Saving...
                  `
                : "Save Changes"}
            </button>
          </div>
        </div>
      </div>

      <!-- Agent Discovery Dialog (settings mode - returns agents to state without saving) -->
      <${AgentDiscoveryDialog}
        isOpen=${showDiscoverAgents}
        mode="settings"
        existingServers=${acpServers}
        onClose=${() => setShowDiscoverAgents(false)}
        onAgentsSelected=${(newAgents) => {
          // Deduplicate by case-insensitive name before adding to state
          const existingNames = new Set(acpServers.map((s) => s.name.toLowerCase()));
          const toAdd = newAgents.filter((a) => !existingNames.has(a.name.toLowerCase()));
          if (toAdd.length > 0) {
            setAcpServers([...acpServers, ...toAdd]);
          }
          setShowDiscoverAgents(false);
        }}
      />
    </div>
  `;
}
