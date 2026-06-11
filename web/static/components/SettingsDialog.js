// Mitto Web Interface - Settings Dialog Component
const { useState, useEffect, useMemo, useRef, html } = window.preact;

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
import { ModelSelection } from "./ModelSelection.js";

// Import constants
import { CYCLING_MODE, CYCLING_MODE_OPTIONS } from "../constants.js";

// Import the curated theme list (single source of truth) for the theme picker.
import { NAMED_THEMES } from "../hooks/useTheme.js";

// Human-friendly labels for the named daisyUI themes (l6a). Order follows
// NAMED_THEMES insertion order; any theme without a label falls back to its key.
const THEME_LABELS = {
  mitto: "Mitto (default)",
  // Light themes
  light: "Light",
  cupcake: "Cupcake",
  bumblebee: "Bumblebee",
  emerald: "Emerald",
  corporate: "Corporate",
  retro: "Retro",
  cyberpunk: "Cyberpunk",
  valentine: "Valentine",
  garden: "Garden",
  lofi: "Lofi",
  pastel: "Pastel",
  fantasy: "Fantasy",
  wireframe: "Wireframe",
  cmyk: "CMYK",
  autumn: "Autumn",
  acid: "Acid",
  lemonade: "Lemonade",
  winter: "Winter",
  nord: "Nord",
  caramellatte: "Caramellatte",
  silk: "Silk",
  // Dark themes
  dark: "Dark",
  synthwave: "Synthwave",
  halloween: "Halloween",
  forest: "Forest",
  aqua: "Aqua",
  black: "Black",
  luxury: "Luxury",
  dracula: "Dracula",
  business: "Business",
  night: "Night",
  coffee: "Coffee",
  dim: "Dim",
  sunset: "Sunset",
  abyss: "Abyss",
};

// WorkspaceBadge is now a standalone component module (not prop-drilled from app.js)

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
        <span class="text-sm font-medium text-mitto-text-secondary flex-1">${label}</span>
        <select
          value=${mode}
          onChange=${(e) => onModeChange(e.target.value)}
          class="select select-sm"
        >
          <option value="append">Append</option>
          <option value="replace">Replace</option>
        </select>
      </div>

      ${mode === "append" &&
      (inheritedFolders || []).length > 0 &&
      html`
        <div class="space-y-1 opacity-50 pb-1 border-b border-mitto-border-2/40">
          ${(inheritedFolders || []).map(
            (f, idx) => html`
              <div key=${"inh-" + idx} class="flex items-center gap-2">
                <input
                  type="text"
                  value=${f}
                  disabled
                  class="input input-sm flex-1 font-mono cursor-not-allowed"
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
                class="input input-sm flex-1 font-mono"
              />
              <button
                type="button"
                onClick=${() => removeFolder(idx)}
                class="btn btn-ghost btn-square btn-xs"
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
          class="btn btn-ghost btn-xs"
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
    <fieldset class="fieldset pt-2">
      <legend class="fieldset-legend">Auto-Create Children</legend>
      <p class="label">
        These conversations are auto-created when a new top-level conversation
        starts. They are deleted when the parent is deleted.
      </p>
      <div class="flex justify-end mb-2">
        ${canAdd
          ? html`
              <button
                type="button"
                onClick=${addChild}
                class="btn btn-ghost btn-xs"
              >
                + Add Child
              </button>
            `
          : html`
              <span class="text-xs text-mitto-text-muted">Max ${maxChildren} children</span>
            `}
      </div>
      ${(children || []).length === 0
        ? html`
            <div class="text-xs text-mitto-text-muted italic py-2">
              No auto-children configured.
            </div>
          `
        : html`
            <div class="space-y-2">
              ${(children || []).map(
                (child, idx) => html`
                  <div
                    key=${idx}
                    class="flex items-center gap-2 p-2 bg-mitto-input-box rounded-md border border-mitto-border"
                  >
                    <input
                      type="text"
                      value=${child.title || ""}
                      placeholder="Child title"
                      onInput=${(e) => updateChild(idx, "title", e.target.value)}
                      class="input input-sm flex-1"
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
                      class="select select-sm"
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
                      class="btn btn-ghost btn-square btn-xs"
                      title="Remove child"
                    >
                      <${TrashIcon} className="w-4 h-4" />
                    </button>
                  </div>
                `,
              )}
            </div>
          `}
    </fieldset>
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
    <div class="border border-mitto-border-2/50 rounded-md overflow-hidden mt-2">
      <button
        type="button"
        onClick=${() => setExpanded(!expanded)}
        class="w-full flex items-center justify-between p-3 bg-mitto-surface-3/30 hover:bg-mitto-surface-3/50 transition-colors"
      >
        <div class="flex items-center gap-2">
          <${expanded ? ChevronDownIcon : ChevronRightIcon}
            className="w-4 h-4 text-mitto-text-muted"
          />
          <span class="text-sm font-medium">Runner Restrictions</span>
        </div>
        ${hasConfig &&
        html`
          <span
            class="badge badge-sm bg-mitto-accent-500/20 text-mitto-accent"
          >
            Configured
          </span>
        `}
      </button>

      ${expanded &&
      html`
        <div class="p-4 space-y-4 border-t border-mitto-border-2/50">
          <fieldset class="fieldset pt-2">
            <legend class="fieldset-legend">Networking</legend>
            <p class="label">
              Override inherited restrictions from global/agent config.
              ${effectiveConfig
                ? ""
                : " Loading inherited values..."}
            </p>
            <div class="space-y-1">
              <div class="flex items-center gap-3">
                <input
                  type="checkbox"
                  id="override-networking"
                  checked=${overrideNetworking}
                  onChange=${(e) => handleNetworkingOverride(e.target.checked)}
                  class="checkbox checkbox-sm checkbox-primary"
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
                        class="checkbox checkbox-sm checkbox-primary"
                      />
                      <span class="text-sm">Allow networking</span>
                    </label>
                  `
                : html`
                    <p class="text-xs text-mitto-text-muted ml-6">
                      Inherited:
                      ${inheritedNetworking ? "allowed" : "blocked"}
                    </p>
                  `}
            </div>
          </fieldset>

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
            <fieldset class="fieldset pt-2 mt-2">
              <legend class="fieldset-legend">Docker Settings</legend>
              <div class="grid grid-cols-3 gap-3">
                <div>
                  <label class="label" for="docker-image">Image</label>
                  <input
                    id="docker-image"
                    type="text"
                    value=${runnerConfig?.restrictions?.docker?.image || ""}
                    onInput=${(e) => updateDocker("image", e.target.value)}
                    class="input input-sm w-full font-mono"
                    placeholder="alpine:latest"
                  />
                </div>
                <div>
                  <label class="label" for="docker-memory">Memory Limit</label>
                  <input
                    id="docker-memory"
                    type="text"
                    value=${runnerConfig?.restrictions?.docker?.memory_limit ||
                    ""}
                    onInput=${(e) =>
                      updateDocker("memory_limit", e.target.value)}
                    class="input input-sm w-full font-mono"
                    placeholder="4g"
                  />
                </div>
                <div>
                  <label class="label" for="docker-cpu">CPU Limit</label>
                  <input
                    id="docker-cpu"
                    type="text"
                    value=${runnerConfig?.restrictions?.docker?.cpu_limit || ""}
                    onInput=${(e) => updateDocker("cpu_limit", e.target.value)}
                    class="input input-sm w-full font-mono"
                    placeholder="2.0"
                  />
                </div>
              </div>
            </fieldset>
          `}

          <!-- Clear button -->
          <div class="flex justify-end pt-2 border-t border-mitto-border-2/50">
            <button
              type="button"
              onClick=${() => onChange(null)}
              class="btn btn-ghost btn-xs"
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
 * Helper component for editing a server inline
 * Server-specific prompts are read-only (managed via prompt files with acps: field)
 */
function ServerEditForm({ server, agentTypes = [], onChange }) {
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

  // Model constraint state
  const [constraintModelMode, setConstraintModelMode] = useState(
    server.constraints?.model?.matchMode || "",
  );
  const [constraintModelPattern, setConstraintModelPattern] = useState(
    server.constraints?.model?.pattern || "",
  );

  // Build the current server state and notify the parent
  const emitChange = (overrides = {}) => {
    const currentState = {
      name: overrides.name !== undefined ? overrides.name : name,
      command: overrides.command !== undefined ? overrides.command : command,
      type: overrides.type !== undefined ? overrides.type : type,
      autoApprove: overrides.autoApprove !== undefined ? overrides.autoApprove : autoApprove,
      tags: overrides.tags !== undefined ? overrides.tags : tags,
      envVars: overrides.envVars !== undefined ? overrides.envVars : envVars,
      constraintModelMode: overrides.constraintModelMode !== undefined ? overrides.constraintModelMode : constraintModelMode,
      constraintModelPattern: overrides.constraintModelPattern !== undefined ? overrides.constraintModelPattern : constraintModelPattern,
    };

    // Convert envVars array to object, filtering out empty keys
    const envObj = {};
    currentState.envVars.forEach(({ key, value }) => {
      if (key && key.trim()) {
        envObj[key.trim()] = value || "";
      }
    });

    // Parse tags
    const parsedTags = currentState.tags
      .split(",")
      .map((t) => t.trim())
      .filter((t) => t.length > 0);

    // Build constraints
    const constraints = {};
    if (currentState.constraintModelMode && currentState.constraintModelPattern) {
      constraints.model = {
        matchMode: currentState.constraintModelMode,
        pattern: currentState.constraintModelPattern,
      };
    }

    onChange(
      currentState.name,
      currentState.command,
      currentState.type,
      currentState.autoApprove,
      envObj,
      parsedTags,
      Object.keys(constraints).length > 0 ? constraints : undefined,
    );
  };

  const addEnvVar = () => {
    setEnvVars([...envVars, { key: "", value: "" }]);
  };

  const removeEnvVar = (index) => {
    const newVars = envVars.filter((_, i) => i !== index);
    setEnvVars(newVars);
    emitChange({ envVars: newVars });
  };

  const updateEnvVar = (index, field, value) => {
    const updated = [...envVars];
    updated[index] = { ...updated[index], [field]: value };
    setEnvVars(updated);
    emitChange({ envVars: updated });
  };

  return html`
    <fieldset class="fieldset pt-2 space-y-3">
      <legend class="fieldset-legend">Server Configuration</legend>
      <div>
        <label class="label" for="acp-server-name">Server Name</label>
        <input
          id="acp-server-name"
          type="text"
          value=${name}
          onInput=${(e) => { setName(e.target.value); emitChange({ name: e.target.value }); }}
          class="input input-sm w-full"
        />
      </div>
      <div>
        <label class="label" for="acp-server-command">Command</label>
        <input
          id="acp-server-command"
          type="text"
          value=${command}
          onInput=${(e) => { setCommand(e.target.value); emitChange({ command: e.target.value }); }}
          class="input input-sm w-full"
        />
      </div>
      <!-- Model Selection -->
      <div>
        <label class="label">Model Selection</label>
        <p class="label">
          Switch to a model based on some selection criteria
        </p>
        <${ModelSelection}
          matchMode=${constraintModelMode}
          pattern=${constraintModelPattern}
          onChange=${(mode, pat) => {
            setConstraintModelMode(mode);
            setConstraintModelPattern(pat);
            emitChange({ constraintModelMode: mode, constraintModelPattern: pat });
          }}
        />
      </div>
      <div>
        <label class="label" for="acp-server-type"
          >Type
          <span class="text-xs text-mitto-danger ml-1">*</span></label
        >
        <select
          id="acp-server-type"
          value=${type}
          onChange=${(e) => { setType(e.target.value); emitChange({ type: e.target.value }); }}
          class="select select-sm w-full"
        >
          <option value="">-- Select agent type --</option>
          ${agentTypes.map(
            (t) => html`<option key=${t} value=${t}>${t}</option>`,
          )}
        </select>
        <p class="label">
          Servers with the same type share prompts and agent configuration.
        </p>
      </div>
      <div>
        <label class="label" for="acp-server-tags"
          >Tags
          <span class="text-xs text-mitto-text-muted"
            >(optional, for categorization)</span
          ></label
        >
        <input
          id="acp-server-tags"
          type="text"
          value=${tags}
          onInput=${(e) => { setTags(e.target.value); emitChange({ tags: e.target.value }); }}
          placeholder="e.g., coding, fast-model, production"
          class="input input-sm w-full"
        />
        <p class="label">
          Comma-separated tags for categorization
        </p>
      </div>

      <!-- Auto-approve Permissions -->
      <label
        class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
      >
        <input
          type="checkbox"
          checked=${autoApprove}
          onChange=${(e) => { setAutoApprove(e.target.checked); emitChange({ autoApprove: e.target.checked }); }}
          class="checkbox checkbox-sm checkbox-primary"
        />
        <div class="flex-1">
          <div class="font-medium text-sm">Auto-approve Permissions</div>
          <div class="text-xs text-mitto-text-muted">
            Automatically approve all permission requests from the agent for
            sessions using this server
          </div>
        </div>
      </label>

      <!-- Environment Variables -->
      <div>
        <div class="flex items-center justify-between mb-2">
          <label class="label"
            >Environment Variables
            <span class="text-xs text-mitto-text-muted">(optional)</span>
          </label>
          <button
            type="button"
            onClick=${addEnvVar}
            class="btn btn-ghost btn-xs"
          >
            + Add Variable
          </button>
        </div>
        ${envVars.length === 0
          ? html`
              <p class="text-xs text-mitto-text-muted italic">
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
                        class="input input-sm flex-1 font-mono"
                      />
                      <span class="text-mitto-text-muted">=</span>
                      <input
                        type="text"
                        value=${env.value}
                        placeholder="value"
                        onInput=${(e) =>
                          updateEnvVar(idx, "value", e.target.value)}
                        class="input input-sm"
                      />
                      <button
                        type="button"
                        onClick=${() => removeEnvVar(idx)}
                        class="btn btn-ghost btn-square btn-xs"
                        title="Remove variable"
                      >
                        <${TrashIcon} className="w-4 h-4" />
                      </button>
                    </div>
                  `,
                )}
              </div>
            `}
        <p class="text-xs text-mitto-text-muted mt-2">
          These environment variables will be set when starting the ACP server
          process.
        </p>
      </div>

      <!-- Server-specific prompts (read-only, from files with acps: field) -->
      ${filePrompts.length > 0 &&
      html`
        <div>
          <label class="label"
            >Server-specific prompts
            <span class="text-xs text-mitto-text-muted"
              >(from prompt files)</span
            ></label
          >
          <div class="space-y-1">
            ${filePrompts.map(
              (p, idx) => html`
                <div
                  key=${idx}
                  class="flex items-center gap-2 p-2 bg-mitto-surface-2/50 rounded text-sm border border-mitto-border-1/50"
                  title="From prompts file with acps: ${server.name}"
                >
                  <div class="flex-1 min-w-0">
                    <div class="font-medium text-xs">${p.name}</div>
                    <div
                      class="text-xs text-mitto-text-muted truncate"
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

    </fieldset>
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
    <fieldset class="fieldset pt-2 space-y-3">
      <legend class="fieldset-legend">Action Button</legend>
      <div>
        <label class="label" for="action-btn-label">Button Label</label>
        <input
          id="action-btn-label"
          type="text"
          value=${name}
          onInput=${(e) => setName(e.target.value)}
          disabled=${readOnly}
          class="input input-sm w-full ${readOnly
            ? "opacity-60 cursor-not-allowed"
            : ""}"
        />
      </div>
      <div>
        <label class="label" for="action-btn-text">Prompt Text</label>
        <textarea
          id="action-btn-text"
          value=${text}
          onInput=${(e) => setText(e.target.value)}
          rows="8"
          disabled=${readOnly}
          class="textarea textarea-sm w-full resize-y ${readOnly
            ? "opacity-60 cursor-not-allowed"
            : ""}"
        />
      </div>
      <div>
        <label class="label" for="action-btn-group"
          >Group (optional)</label
        >
        <input
          id="action-btn-group"
          type="text"
          value=${group}
          onInput=${(e) => setGroup(e.target.value)}
          placeholder="e.g., Tasks, Code Quality"
          disabled=${readOnly}
          class="input input-sm w-full ${readOnly
            ? "opacity-60 cursor-not-allowed"
            : ""}"
        />
      </div>
      <div>
        <label class="label"
          >Background Color (optional)</label
        >
        <div class="flex items-center gap-2">
          <input
            type="color"
            value=${backgroundColor || "#334155"}
            onInput=${(e) => setBackgroundColor(e.target.value)}
            disabled=${readOnly}
            class="w-10 h-10 rounded cursor-pointer border border-mitto-border-2 ${readOnly
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
            class="input input-sm flex-1 font-mono ${readOnly
              ? "opacity-60 cursor-not-allowed"
              : ""}"
          />
          ${backgroundColor &&
          !readOnly &&
          html`
            <button
              type="button"
              onClick=${() => setBackgroundColor("")}
              class="btn btn-ghost btn-square btn-xs"
              title="Clear color"
            >
              <svg
                class="w-4 h-4 text-mitto-text-muted"
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
          type="button"
          onClick=${onCancel}
          class="btn btn-ghost btn-sm"
        >
          ${readOnly ? "Close" : "Cancel"}
        </button>
        ${!readOnly &&
        html`
          <button
            type="button"
            onClick=${() => onSave(name, text, backgroundColor, group)}
            disabled=${!name.trim() || !text.trim()}
            class="btn btn-primary btn-sm"
          >
            Save
          </button>
        `}
      </div>
    </fieldset>
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
  showToast,
}) {
  const [activeTab, setActiveTab] = useState("servers");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [warning, setWarning] = useState("");
  // Agent discovery dialog (triggered from Servers tab)
  const [showDiscoverAgents, setShowDiscoverAgents] = useState(false);

  // Configuration state
  const [workspaces, setWorkspaces] = useState([]);
  const [acpServers, setAcpServers] = useState([]);
  // Stable key counter for ACP servers — survives renames without losing focus
  const stableKeyRef = useRef(0);
  const assignStableKey = (srv) => {
    if (srv._key == null) {
      srv._key = ++stableKeyRef.current;
    }
    return srv;
  };
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
  // True when the backend reported that a password already exists (in keychain or settings).
  // Used to distinguish "user left field empty" from "no password was ever set".
  const [hasExistingPassword, setHasExistingPassword] = useState(false);
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



  // UI settings state (macOS only)
  const [agentCompletedSound, setAgentCompletedSound] = useState(false);
  const [nativeNotifications, setNativeNotifications] = useState(false);
  const [notificationPermissionStatus, setNotificationPermissionStatus] =
    useState(-1); // -1 = unknown, 0 = not determined, 1 = denied, 2 = authorized
  const [showInAllSpaces, setShowInAllSpaces] = useState(false);
  const [startAtLogin, setStartAtLogin] = useState(false);
  const [loginItemSupported, setLoginItemSupported] = useState(false);
  const [badgeClickCommand, setBadgeClickCommand] =
    useState("open ${MITTO_WORKING_DIR}");
  const [terminalActionCommand, setTerminalActionCommand] = useState(
    "open -a Terminal ${MITTO_WORKING_DIR}",
  );

  // Confirmation settings (all platforms)
  const [confirmDeleteSession, setConfirmDeleteSession] = useState(true);
  // Confirmation settings (macOS only)
  const [confirmQuitWithRunningSessions, setConfirmQuitWithRunningSessions] =
    useState(true);

  // Archive retention period setting
  const [archiveRetentionPeriod, setArchiveRetentionPeriod] = useState("never");

  // Auto-archive inactive period setting
  const [autoArchiveInactiveAfter, setAutoArchiveInactiveAfter] = useState("");
  const [maxMessagesPerSession, setMaxMessagesPerSession] = useState(2000);

  // Periodic suspend timeout setting (default "" = 30 minutes)
  const [periodicSuspendTimeout, setPeriodicSuspendTimeout] = useState("");

  // Memory recycle threshold setting (default "" = disabled, opt-in)
  const [memoryRecycleThreshold, setMemoryRecycleThreshold] = useState("");

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

  // Input font size setting (web UI)
  const [inputFontSize, setInputFontSize] = useState("default");

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

  // Two-slot theme setting (l6a). Mirrors useTheme.js two-slot state;
  // changes are broadcast via mitto-theme-light-changed / mitto-theme-dark-changed.
  const [lightThemeName, setLightThemeName] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto-theme-light");
      if (saved && Object.prototype.hasOwnProperty.call(NAMED_THEMES, saved)) {
        return saved;
      }
      // Migration: seed from old single-slot key if it was a light-bucket theme
      const legacy = localStorage.getItem("mitto-theme-name");
      if (legacy && Object.prototype.hasOwnProperty.call(NAMED_THEMES, legacy)) {
        if (NAMED_THEMES[legacy] === "light" || legacy === "mitto") {
          return legacy;
        }
      }
    }
    return "mitto";
  });

  const [darkThemeName, setDarkThemeName] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto-theme-dark");
      if (saved && Object.prototype.hasOwnProperty.call(NAMED_THEMES, saved)) {
        return saved;
      }
      // Migration: seed from old single-slot key if it was a dark-bucket theme
      const legacy = localStorage.getItem("mitto-theme-name");
      if (legacy && Object.prototype.hasOwnProperty.call(NAMED_THEMES, legacy)) {
        if (NAMED_THEMES[legacy] === "dark") {
          return legacy;
        }
      }
    }
    return "mitto";
  });

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

  // Handle per-slot theme changes — persist and broadcast to useTheme.js (l6a).
  const handleLightThemeChange = (name) => {
    setLightThemeName(name);
    localStorage.setItem("mitto-theme-light", name);
    window.dispatchEvent(
      new CustomEvent("mitto-theme-light-changed", { detail: { name } }),
    );
  };

  const handleDarkThemeChange = (name) => {
    setDarkThemeName(name);
    localStorage.setItem("mitto-theme-dark", name);
    window.dispatchEvent(
      new CustomEvent("mitto-theme-dark-changed", { detail: { name } }),
    );
  };

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
      servers.forEach(assignStableKey);
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
      // Track whether a password already exists in the keychain/settings.
      setHasExistingPassword(!!config.has_auth_password);

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

      // Load UI settings (macOS only)
      setAgentCompletedSound(
        config.ui?.mac?.notifications?.sounds?.agent_completed || false,
      );
      setNativeNotifications(
        config.ui?.mac?.notifications?.native_enabled || false,
      );
      setShowInAllSpaces(config.ui?.mac?.show_in_all_spaces || false);

      // Load badge click action settings (macOS only)
      setBadgeClickCommand(
        config.ui?.mac?.badge_click_action?.command || "open ${MITTO_WORKING_DIR}",
      );

      // Load terminal action settings (macOS only)
      setTerminalActionCommand(
        config.ui?.mac?.terminal_action?.command ||
          "open -a Terminal ${MITTO_WORKING_DIR}",
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

      // Load max messages per session (default 2000).
      // Backend: 0 = not configured (use default), negative = disabled.
      // UI: 0 = unlimited (no pruning), positive = limit.
      const rawMaxMessages = config.session?.max_messages_per_session;
      if (rawMaxMessages != null && rawMaxMessages < 0) {
        setMaxMessagesPerSession(0); // Disabled → show as "unlimited" (0)
      } else {
        setMaxMessagesPerSession(rawMaxMessages || 2000);
      }

      // Load periodic suspend timeout (default "" = 30 minutes)
      setPeriodicSuspendTimeout(
        config.session?.periodic_suspend_timeout || "",
      );

      // Load memory recycle threshold (default "" = disabled)
      setMemoryRecycleThreshold(
        config.session?.memory_recycle_threshold || "",
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

      // Load input font size setting (web UI) - default to "default"
      setInputFontSize(config.ui?.web?.input_font_size || "default");

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

    // Validate all ACP servers have required fields
    for (const srv of acpServers) {
      if (!srv.name || !srv.name.trim()) {
        setError("All ACP servers must have a name");
        setActiveTab("servers");
        return;
      }
      if (!srv.command || !srv.command.trim()) {
        setError(`ACP server "${srv.name}" must have a command`);
        setActiveTab("servers");
        return;
      }
    }
    // Check for duplicate server names
    const serverNames = acpServers.map((s) => s.name.trim());
    const duplicates = serverNames.filter((n, i) => serverNames.indexOf(n) !== i);
    if (duplicates.length > 0) {
      setError(`Duplicate ACP server name: "${duplicates[0]}"`);
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
        // If the field is empty but a password already exists in the keychain,
        // treat this as "keep existing" — no validation needed.
        if (authPassword === "" && hasExistingPassword) {
          // No-op: server will preserve the existing password.
        } else {
          const passwordError = validatePassword(authPassword);
          if (passwordError) {
            setError(passwordError);
            setActiveTab("web");
            return;
          }
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
    const saveStartTime = Date.now();
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
          input_font_size: inputFontSize,
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
            enabled: badgeClickCommand.trim() !== "",
            command: badgeClickCommand,
          },
          terminal_action: {
            enabled: terminalActionCommand.trim() !== "",
            command: terminalActionCommand,
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

      // Build session config with archive retention period, auto-archive inactive period,
      // and max messages per session (auto-pruning).
      // UI: 0 = unlimited (no pruning) → backend: -1 (disabled).
      // UI: positive = limit → backend: same value.
      const sessionConfig = {
        archive_retention_period: archiveRetentionPeriod,
        auto_archive_inactive_after: autoArchiveInactiveAfter,
        max_messages_per_session:
          maxMessagesPerSession === 0 ? -1 : maxMessagesPerSession,
        periodic_suspend_timeout: periodicSuspendTimeout,
        memory_recycle_threshold: memoryRecycleThreshold,
      };

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
          constraints: srv.constraints || undefined, // Include constraints if present
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
        prompts: [], // Prompts are now managed per-workspace, not in global settings
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

      // Fetch updated external status to refresh the displayed port/state
      try {
        const statusRes = await fetch(apiUrl("/api/external-status"), {
          credentials: "same-origin",
        });
        if (statusRes.ok) {
          const status = await statusRes.json();
          setExternalEnabled(status.enabled);
          setCurrentExternalPort(status.port || null);
        }
      } catch (e) {
        console.error("Failed to fetch external status:", e);
      }

      // Notify success via the app-wide auto-dismissing toast
      const appliedDetails = [];
      if (result.applied) {
        if (result.applied.external_access_enabled) {
          appliedDetails.push("external access enabled");
        }
        if (result.applied.auth_enabled) {
          appliedDetails.push("authentication active");
        }
      }
      showToast?.({
        style: "success",
        title: "Configuration saved",
        message: appliedDetails.join(", "),
        duration: 2000,
      });

      // Clear server renames after successful save
      setServerRenames({});

      onSave?.();
    } catch (err) {
      setError(err.message);
    } finally {
      const elapsed = Date.now() - saveStartTime;
      const remaining = Math.max(0, 1000 - elapsed);
      setTimeout(() => setSaving(false), remaining);
    }
  };

  const handleClose = () => {
    // Closing the dialog is always allowed; configuration validation
    // (at least one ACP server, at least one workspace) is enforced on Save.
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

    assignStableKey(newServer);
    setAcpServers([...acpServers, newServer]);
    setNewServerName("");
    setNewServerCommand("");
    setNewServerType("");
    setNewServerTags("");
    setShowAddServer(false);
    setError("");
  };

  const updateServer = (oldName, newName, newCommand, newType, autoApprove, env, tags, constraints) => {
    // Update server in-memory (prompts are now read-only from files)
    setAcpServers(
      acpServers.map((s) => {
        if (s.name !== oldName) return s;
        const updated = {
          _key: s._key, // Preserve stable key across renames
          name: (newName || "").trim() || oldName, // Fall back to old name if empty
          command: (newCommand || "").trim() || s.command, // Fall back to old command if empty
          prompts: s.prompts, // Preserve existing prompts (read-only from files)
          source: s.source, // Preserve source (rcfile or settings)
          auto_approve: autoApprove || undefined, // undefined to omit if false
          env: env && Object.keys(env).length > 0 ? env : undefined, // undefined to omit if empty
          tags: tags && tags.length > 0 ? tags : undefined, // undefined to omit if empty
          constraints: constraints || undefined, // undefined to omit if empty
        };
        // Only include type if specified (otherwise name is used as type)
        if (newType && newType.trim()) {
          updated.type = newType.trim();
        }
        return updated;
      }),
    );

    // Update workspace references for renames (editingServer uses _key, no update needed)
    const trimmedNewName = (newName || "").trim();
    if (trimmedNewName && trimmedNewName !== oldName) {
      setWorkspaces(
        workspaces.map((ws) => {
          const updated = { ...ws };
          if (updated.acp_server === oldName) {
            updated.acp_server = trimmedNewName;
          }
          return updated;
        }),
      );
      // Track server rename
      const originalName = Object.entries(serverRenames).find(
        ([, target]) => target === oldName,
      )?.[0];
      if (originalName) {
        setServerRenames({ ...serverRenames, [originalName]: trimmedNewName });
      } else {
        setServerRenames({ ...serverRenames, [oldName]: trimmedNewName });
      }
    }
  };

  const removeServer = (serverName) => {
    // Check if any workspace uses this server as its primary ACP server
    const usedBy = workspaces.filter(
      (ws) => ws.acp_server === serverName,
    );
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

    assignStableKey(duplicatedServer);
    setAcpServers([...acpServers, duplicatedServer]);
    setError("");
  };

  if (!isOpen) return null;

  // Can close if we have both ACP servers and workspaces configured
  const canClose = acpServers.length > 0 && workspaces.length > 0;

  // Define navigation items for sidebar
  const navItems = [
    { id: "servers", label: "ACP Servers", icon: ServerIcon },
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
        class="settings-dialog bg-mitto-sidebar rounded-lg w-[70vw] h-[70vh] max-w-[95vw] max-h-[95vh] overflow-hidden shadow-2xl flex flex-col"
        data-testid="settings-dialog"
        onClick=${(e) => e.stopPropagation()}
      >
        <!-- Header -->
        <div
          class="flex items-center justify-between p-4 border-b border-mitto-border-1"
        >
          <h3 class="text-lg font-semibold flex items-center gap-2">
            <${SettingsIcon} className="w-5 h-5" />
            Settings
          </h3>
          ${canClose &&
          html`
            <button
              onClick=${handleClose}
              class="btn btn-ghost btn-square btn-sm"
            >
              <${CloseIcon} className="w-5 h-5" />
            </button>
          `}
        </div>

        <!-- Main content area with sidebar - fills available space -->
        <div class="flex flex-1 min-h-0 overflow-hidden">
          <!-- Sidebar Navigation -->
          <ul
            class="menu flex-nowrap w-44 shrink-0 border-r border-mitto-border-1 overflow-y-auto"
          >
            ${navItems.map(
              (item) => html`
                <li key=${item.id}>
                  <button
                    data-testid=${`settings-nav-${item.id}`}
                    onClick=${() => setActiveTab(item.id)}
                    class="font-medium ${activeTab === item.id
                      ? "menu-active"
                      : "text-mitto-text-muted"}"
                  >
                    <${item.icon} className="w-4 h-4 shrink-0" />
                    <span class="truncate">${item.label}</span>
                  </button>
                </li>
              `,
            )}
          </ul>

          <!-- Content Area -->
          <div class="flex-1 overflow-y-auto p-4" data-testid="settings-content">
            ${loading
              ? html`
                  <div class="flex items-center justify-center py-12">
                    <${SpinnerIcon} className="w-8 h-8 text-mitto-accent" />
                  </div>
                `
              : html`
                  <!-- ACP Servers Tab -->
                  ${activeTab === "servers" &&
                  html`
                    <div class="space-y-4">
                      <div class="flex items-center justify-between">
                        <p class="text-mitto-text-muted text-sm">
                          ACP servers are AI coding assistants.${" "}
                          <a
                            href="https://agentclientprotocol.com/overview/agents"
                            onClick=${(e) => {
                              e.preventDefault();
                              openExternalURL(
                                "https://agentclientprotocol.com/overview/agents",
                              );
                            }}
                            class="text-mitto-accent hover:text-mitto-accent-300 underline cursor-pointer"
                            >Popular examples</a
                          >${" "} include Auggie and Claude Code. You can
                          configure multiple servers and choose which one to use
                          for each workspace.
                        </p>
                        <button
                          type="button"
                          onClick=${() => setShowDiscoverAgents(true)}
                          class="btn btn-ghost btn-square btn-sm"
                          title="Discover Agents"
                        >
                          <${SearchIcon} className="w-5 h-5" />
                        </button>
                        <button
                          type="button"
                          onClick=${() => setShowAddServer(!showAddServer)}
                          class="btn btn-ghost btn-square btn-sm ${showAddServer ? "btn-active" : ""}"
                          title="Add Server"
                        >
                          <${PlusIcon} className="w-5 h-5" />
                        </button>
                      </div>

                      ${showAddServer &&
                      html`
                        <fieldset
                          class="fieldset pt-2 space-y-3"
                        >
                          <legend class="fieldset-legend">Add Server</legend>
                          <div>
                            <label class="label" for="new-server-name"
                              >Server Name</label
                            >
                            <input
                              id="new-server-name"
                              type="text"
                              value=${newServerName}
                              onInput=${(e) => setNewServerName(e.target.value)}
                              placeholder="e.g., claude-code"
                              class="input input-sm w-full"
                            />
                          </div>
                          <div>
                            <label class="label" for="new-server-command"
                              >Command</label
                            >
                            <input
                              id="new-server-command"
                              type="text"
                              value=${newServerCommand}
                              onInput=${(e) =>
                                setNewServerCommand(e.target.value)}
                              placeholder="e.g., npx -y @anthropic/claude-code-acp"
                              class="input input-sm w-full"
                            />
                          </div>
                          <div>
                            <label class="label" for="new-server-type"
                              >Type
                              <span class="text-xs text-mitto-danger ml-1">*</span></label
                            >
                            <select
                              id="new-server-type"
                              value=${newServerType}
                              onChange=${(e) =>
                                setNewServerType(e.target.value)}
                              class="select select-sm w-full ${!newServerType ? "ring-2 ring-amber-500/50" : ""}"
                            >
                              <option value="">-- Select agent type --</option>
                              ${agentTypes.map(
                                (t) => html`<option key=${t} value=${t}>${t}</option>`,
                              )}
                            </select>
                            <p class="label">
                              Servers with the same type share prompts and
                              agent configuration.
                            </p>
                          </div>
                          <div>
                            <label class="label" for="new-server-tags"
                              >Tags
                              <span class="text-xs text-mitto-text-muted"
                                >(optional)</span
                              ></label
                            >
                            <input
                              id="new-server-tags"
                              type="text"
                              value=${newServerTags}
                              onInput=${(e) =>
                                setNewServerTags(e.target.value)}
                              placeholder="e.g., coding, fast-model, production"
                              class="input input-sm w-full"
                            />
                            <p class="label">
                              Comma-separated tags for categorization
                            </p>
                          </div>
                          ${error &&
                          html`
                            <div
                              role="alert"
                              class="alert alert-error alert-soft text-sm"
                            >
                              ⚠️ ${error}
                            </div>
                          `}
                          <div class="flex justify-end gap-2">
                            <button
                              type="button"
                              onClick=${() => {
                                setShowAddServer(false);
                                setNewServerName("");
                                setNewServerCommand("");
                                setNewServerType("");
                                setNewServerTags("");
                                setError("");
                              }}
                              class="btn btn-ghost btn-sm"
                            >
                              Cancel
                            </button>
                            <button
                              type="button"
                              onClick=${addServer}
                              class="btn btn-primary btn-sm"
                            >
                              Add
                            </button>
                          </div>
                        </fieldset>
                      `}
                      <fieldset class="fieldset pt-2">
                        <legend class="fieldset-legend">ACP Servers</legend>
                      ${acpServers.length === 0
                        ? html`
                            <div class="text-center py-8 text-mitto-text-muted">
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
                            <ul class="list">
                              ${sortedAcpServers.map((srv) => {
                                // RC file servers are read-only (cannot edit/delete)
                                const isRCFile = srv.source === "rcfile";
                                const isExpanded = editingServer === srv._key && !isRCFile;
                                return html`
                                  <li
                                    key=${srv._key}
                                    class="list-row p-0"
                                  >
                                  <div
                                    class="collapse ${isExpanded ? "collapse-open" : "collapse-close"} bg-mitto-surface-3/20 rounded-sm border border-mitto-border-2/50 ${isRCFile ? "opacity-80" : ""} group w-full"
                                  >
                                    <!-- Collapsed header row — click to expand/collapse -->
                                    <div
                                      class="collapse-title flex items-center gap-3 py-3 px-3 min-h-0 ${!isRCFile ? "cursor-pointer hover:bg-mitto-surface-3/30" : ""} transition-colors"
                                      onClick=${!isRCFile ? () => setEditingServer(isExpanded ? null : srv._key) : null}
                                    >
                                      ${!isRCFile && html`
                                        <${isExpanded ? ChevronDownIcon : ChevronRightIcon}
                                          className="w-4 h-4 text-mitto-text-muted shrink-0"
                                        />
                                      `}
                                      <div class="flex-1 min-w-0">
                                        <div class="font-medium text-sm flex items-center gap-2">
                                          ${srv.name}
                                          ${srv.type && html`
                                            <span
                                              class="badge badge-sm bg-purple-500/20 text-purple-400"
                                              title="Server type for prompt matching"
                                            >
                                              ${srv.type}
                                            </span>
                                          `}
                                          ${srv.tags && srv.tags.length > 0 && srv.tags.map(
                                            (tag) => html`
                                              <span
                                                key=${tag}
                                                class="badge badge-sm bg-mitto-accent-500/20 text-mitto-accent"
                                                title="Tag"
                                              >
                                                ${tag}
                                              </span>
                                            `,
                                          )}
                                          ${isRCFile && html`
                                            <span
                                              class="flex items-center gap-1 text-xs text-amber-400"
                                              title="This server is defined in .mittorc and cannot be modified here"
                                            >
                                              <${LockIcon} className="w-3 h-3" />
                                            </span>
                                          `}
                                          ${srv.prompts?.length > 0 && html`
                                            <span
                                              class="flex items-center gap-1 text-xs text-mitto-accent"
                                              title="${srv.prompts.length} server-specific prompt(s)"
                                            >
                                              <${LightningIcon} className="w-3.5 h-3.5" />
                                              ${srv.prompts.length}
                                            </span>
                                          `}
                                        </div>
                                        <div
                                          class="text-xs text-mitto-text-muted truncate"
                                          title=${srv.command}
                                        >
                                          ${srv.command}
                                          ${isRCFile && html`<span class="ml-2 text-amber-500/70">(from .mittorc)</span>`}
                                        </div>
                                      </div>
                                      ${!isRCFile && html`
                                        <button
                                          type="button"
                                          onClick=${(e) => {
                                            e.stopPropagation();
                                            duplicateServer(srv.name);
                                          }}
                                          class="btn btn-ghost btn-square btn-sm opacity-0 group-hover:opacity-100"
                                          title="Duplicate server"
                                        >
                                          <${DuplicateIcon} className="w-4 h-4" />
                                        </button>
                                        <button
                                          type="button"
                                          onClick=${(e) => {
                                            e.stopPropagation();
                                            removeServer(srv.name);
                                          }}
                                          class="btn btn-ghost btn-square btn-sm opacity-0 group-hover:opacity-100"
                                          title="Remove server"
                                        >
                                          <${TrashIcon} className="w-4 h-4" />
                                        </button>
                                      `}
                                    </div>
                                    <!-- Expanded edit form -->
                                    <div class="collapse-content px-3 pb-3">
                                      ${isExpanded && html`
                                        <${ServerEditForm}
                                          server=${srv}
                                          agentTypes=${agentTypes}
                                          onChange=${(name, cmd, type, autoApprove, env, tags, constraints) =>
                                            updateServer(
                                              srv.name,
                                              name,
                                              cmd,
                                              type,
                                              autoApprove,
                                              env,
                                              tags,
                                              constraints,
                                            )}
                                        />
                                      `}
                                    </div>
                                  </div>
                                  </li>
                                `;
                              })}
                            </ul>
                          `}
                      </fieldset>
                    </div>
                  `}

                  <!-- (Prompts are managed per-workspace in WorkspacesDialog) -->

                  <!-- Runners Tab -->
                  ${activeTab === "runners" &&
                  html`
                    <div class="space-y-4">
                      <div
                        role="alert"
                        class="alert alert-warning alert-soft"
                      >
                        <p class="text-sm leading-relaxed">
                          ⚠️ <strong>Advanced feature:</strong> Configure
                          sandboxing restrictions for each runner type. These
                          are global defaults that apply to all workspaces using
                          that runner type. Misconfigured restrictions can break
                          MCP server access.
                        </p>
                      </div>

                      <p class="text-mitto-text-muted text-sm">
                        Configure per-runner-type restrictions. Workspaces using
                        a specific runner type will inherit these settings.
                        <br />
                        <span class="text-mitto-text-muted"
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
                                class="border border-mitto-border-2/50 rounded-md overflow-hidden"
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
                                  class="w-full flex items-center justify-between p-3 bg-mitto-surface-3/30 hover:bg-mitto-surface-3/50 transition-colors"
                                >
                                  <div class="flex items-center gap-3">
                                    <${expandedRunner === runner.type
                                      ? ChevronDownIcon
                                      : ChevronRightIcon}
                                      className="w-4 h-4 text-mitto-text-muted"
                                    />
                                    <div class="text-left">
                                      <div class="font-medium text-sm">
                                        ${runner.label}
                                      </div>
                                      <div class="text-xs text-mitto-text-muted">
                                        ${runner.description}
                                      </div>
                                    </div>
                                  </div>
                                  ${restrictedRunners[runner.type] &&
                                  html`
                                    <span
                                      class="badge badge-sm bg-mitto-accent-500/20 text-mitto-accent"
                                    >
                                      Configured
                                    </span>
                                  `}
                                </button>

                                <!-- Expanded content -->
                                ${expandedRunner === runner.type &&
                                html`
                                  <div
                                    class="p-4 space-y-4 border-t border-mitto-border-2/50"
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
                                        class="checkbox checkbox-sm checkbox-primary"
                                      />
                                      <div>
                                        <div class="font-medium text-sm">
                                          Allow networking
                                        </div>
                                        <div class="text-xs text-mitto-text-muted">
                                          Required for network-based MCP servers
                                        </div>
                                      </div>
                                    </label>

                                    <!-- Allow read folders -->
                                    <div class="space-y-2">
                                      <label
                                        class="text-sm font-medium text-mitto-text-secondary"
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
                                                class="input input-sm flex-1 font-mono"
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
                                                class="btn btn-ghost btn-square btn-xs"
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
                                          class="btn btn-ghost btn-xs"
                                        >
                                          <${PlusIcon} className="w-3 h-3" />
                                          Add folder
                                        </button>
                                      </div>
                                    </div>

                                    <!-- Allow write folders -->
                                    <div class="space-y-2">
                                      <label
                                        class="text-sm font-medium text-mitto-text-secondary"
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
                                                class="input input-sm flex-1 font-mono"
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
                                                class="btn btn-ghost btn-square btn-xs"
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
                                          class="btn btn-ghost btn-xs"
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
                                        class="space-y-2 pt-2 border-t border-mitto-border-2/50"
                                      >
                                        <label
                                          class="text-sm font-medium text-mitto-text-secondary"
                                        >
                                          Docker Settings
                                        </label>
                                        <div class="grid grid-cols-3 gap-3">
                                          <div>
                                            <label class="text-xs text-mitto-text-muted"
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
                                              class="input input-sm w-full font-mono"
                                              placeholder="alpine:latest"
                                            />
                                          </div>
                                          <div>
                                            <label class="text-xs text-mitto-text-muted"
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
                                              class="input input-sm w-full font-mono"
                                              placeholder="4g"
                                            />
                                          </div>
                                          <div>
                                            <label class="text-xs text-mitto-text-muted"
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
                                              class="input input-sm w-full font-mono"
                                              placeholder="2.0"
                                            />
                                          </div>
                                        </div>
                                      </div>
                                    `}

                                    <!-- Merge strategy -->
                                    <div class="flex items-center gap-3 pt-2">
                                      <label class="text-sm text-mitto-text-muted"
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
                                        class="select select-sm"
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
                                      class="flex justify-end gap-2 pt-2 border-t border-mitto-border-2/50"
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
                                        class="btn btn-ghost btn-xs"
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
                                        class="btn btn-ghost btn-xs"
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
                                class="border border-mitto-border-2/30 rounded-md overflow-hidden opacity-50"
                              >
                                <div
                                  class="flex items-center justify-between p-3 bg-mitto-surface-3/20"
                                >
                                  <div class="flex items-center gap-3">
                                    <${ChevronRightIcon}
                                      className="w-4 h-4 text-mitto-text-muted"
                                    />
                                    <div>
                                      <div
                                        class="font-medium text-sm text-mitto-text-muted"
                                      >
                                        ${runner.label}
                                      </div>
                                      <div class="text-xs text-mitto-text-muted">
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
                      <p class="text-mitto-text-muted text-sm">
                        Configure how permission requests from AI agents are
                        handled.
                      </p>

                      <!-- Global Permissions Section -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Global Settings
                        </h4>

                        <label
                          class="flex items-center gap-3 p-4 cursor-pointer hover:bg-base-200/40 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${globalAutoApprove}
                            onChange=${(e) =>
                              setGlobalAutoApprove(e.target.checked)}
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div class="flex-1">
                            <div class="font-medium text-sm">
                              Auto-approve All Permissions
                            </div>
                            <div class="text-xs text-mitto-text-muted">
                              Automatically approve all permission requests from
                              AI agents without showing a dialog. This is the
                              default behavior.
                            </div>
                          </div>
                        </label>

                        <div
                          class="p-3 bg-mitto-surface-2/50 rounded-md border border-mitto-border-1"
                        >
                          <p class="text-mitto-text-secondary text-sm leading-relaxed">
                            <span class="text-mitto-accent font-medium"
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
                            role="alert"
                            class="alert alert-warning alert-soft"
                          >
                            <p class="text-sm leading-relaxed">
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
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Archive Settings
                        </h4>
                        <div
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Auto-archive inactive conversations
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Automatically archive conversations after the
                                specified period of inactivity
                              </div>
                            </div>
                            <select
                              value=${autoArchiveInactiveAfter}
                              onChange=${(e) =>
                                setAutoArchiveInactiveAfter(e.target.value)}
                              class="select select-sm"
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
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Auto-delete archived conversations
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Automatically delete archived conversations
                                after the specified period
                              </div>
                            </div>
                            <select
                              value=${archiveRetentionPeriod}
                              onChange=${(e) =>
                                setArchiveRetentionPeriod(e.target.value)}
                              class="select select-sm"
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

                      <!-- Suspend Settings -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Suspend Settings
                        </h4>
                        <div
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Suspend periodic conversations
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Automatically suspend idle periodic conversations
                                when their next run is farther away than this
                                timeout. Saves memory by stopping ACP and MCP
                                processes. Conversations resume transparently
                                when focused.
                              </div>
                            </div>
                            <select
                              value=${periodicSuspendTimeout}
                              onInput=${(e) =>
                                setPeriodicSuspendTimeout(e.target.value)}
                              class="select select-sm"
                            >
                              <option value="">After 30 minutes</option>
                              <option value="15m">After 15 minutes</option>
                              <option value="30m">After 30 minutes</option>
                              <option value="1h">After 1 hour</option>
                              <option value="2h">After 2 hours</option>
                              <option value="disabled">Disabled</option>
                            </select>
                          </div>
                        </div>
                      </div>

                      <!-- Memory Recycling -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Memory Recycling
                        </h4>
                        <div
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Recycle bloated idle conversations
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Recycle an idle agent process when its memory
                                usage grows beyond this size, reclaiming memory
                                from bloated conversations. Only fully-idle
                                conversations are affected and they resume
                                transparently when focused.
                              </div>
                            </div>
                            <select
                              value=${memoryRecycleThreshold}
                              onInput=${(e) =>
                                setMemoryRecycleThreshold(e.target.value)}
                              class="select select-sm"
                            >
                              <option value="">Disabled</option>
                              <option value="3g">Above 3 GB</option>
                              <option value="4g">Above 4 GB</option>
                              <option value="6g">Above 6 GB</option>
                              <option value="8g">Above 8 GB</option>
                            </select>
                          </div>
                        </div>
                      </div>

                      <!-- Conversation History Limits -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Conversation History
                        </h4>
                        <div
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Max messages per conversation
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Automatically prune oldest messages when a
                                conversation exceeds this limit. Prevents
                                excessive memory usage in long-running
                                conversations. Set to 0 for unlimited.
                              </div>
                            </div>
                            <input
                              type="number"
                              min="0"
                              max="100000"
                              step="100"
                              value=${maxMessagesPerSession}
                              onChange=${(e) =>
                                setMaxMessagesPerSession(
                                  parseInt(e.target.value, 10) || 0,
                                )}
                              class="input input-sm w-24 text-center"
                            />
                          </div>
                        </div>
                      </div>

                      <!-- Child Conversations Limit -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Child Conversations
                        </h4>
                        <div
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Max Child Conversations
                              </div>
                              <div class="text-xs text-mitto-text-muted">
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
                              class="input input-sm w-20 text-center"
                            />
                          </div>
                        </div>
                      </div>

                      <!-- Default Flags for New Conversations -->
                      ${availableFlags.length > 0 &&
                      html`
                        <div class="space-y-3">
                          <h4 class="text-sm font-medium text-mitto-text-secondary">
                            Default Flags for New Conversations
                          </h4>
                          <p class="text-xs text-mitto-text-muted">
                            These flags will be enabled by default when creating
                            new conversations.
                          </p>
                          <div
                            class="overflow-x-auto"
                          >
                            <table class="table table-sm">
                              <tbody>
                                ${availableFlags.map(
                                  (flag) => html`
                                    <tr key=${flag.name}>
                                      <td class="w-10">
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
                                          class="checkbox checkbox-sm checkbox-primary cursor-pointer"
                                        />
                                      </td>
                                      <td>
                                        <div class="font-medium">
                                          ${flag.label}
                                        </div>
                                        <div class="text-xs text-mitto-text-muted">
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
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Message Display
                        </h4>
                        <label
                          class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${actionButtonsEnabled}
                            onChange=${(e) =>
                              setActionButtonsEnabled(e.target.checked)}
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div class="flex-1">
                            <div class="font-medium text-sm">
                              Follow-up Suggestions
                            </div>
                            <div class="text-xs text-mitto-text-muted">
                              Analyze agent responses to suggest clickable
                              follow-up options (uses auxiliary conversation)
                            </div>
                          </div>
                        </label>
                        <label
                          class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${externalImagesEnabled}
                            onChange=${(e) =>
                              setExternalImagesEnabled(e.target.checked)}
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div class="flex-1">
                            <div class="font-medium text-sm">
                              Allow External Images
                            </div>
                            <div class="text-xs text-mitto-text-muted">
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
                      <p class="text-mitto-text-muted text-sm">
                        Configure external access
                        settings${authEnabled ? " and lifecycle hooks" : ""}.
                      </p>

                      <!-- External Access Section -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          External Access
                        </h4>

                        <label
                          class="flex items-center gap-3 p-4 cursor-pointer hover:bg-base-200/40 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${authEnabled}
                            onChange=${(e) => setAuthEnabled(e.target.checked)}
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Allow External Access
                            </div>
                            <div class="text-xs text-mitto-text-muted">
                              Listen on all interfaces (0.0.0.0) and require
                              authentication
                            </div>
                          </div>
                        </label>

                        ${authEnabled &&
                        html`
                          <!-- Port and status -->
                          <div
                            class="p-4 space-y-3"
                          >
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-mitto-text-muted">Port</label>
                              <input
                                type="number"
                                value=${externalPort}
                                onInput=${(e) =>
                                  setExternalPort(e.target.value)}
                                placeholder="random"
                                min="1024"
                                max="65535"
                                class="input input-sm w-24"
                              />
                              <span class="text-xs text-mitto-text-muted"
                                >(leave empty for random)</span
                              >
                            </div>
                            ${externalEnabled &&
                            currentExternalPort &&
                            html`
                              <div class="text-xs text-mitto-success">
                                ✓ External access active on port${" "}
                                ${currentExternalPort}
                              </div>
                            `}
                          </div>

                          <!-- Authentication Methods -->
                          <div class="space-y-3">
                            <h5 class="text-sm font-medium text-mitto-text-muted">
                              Authentication
                            </h5>
                            <p class="text-xs text-mitto-text-muted">
                              At least one authentication method is required for
                              external access.
                            </p>

                            <!-- Simple Auth (Username/Password) -->
                            <div
                              class="p-4 space-y-3"
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
                                  class="checkbox checkbox-sm checkbox-primary"
                                />
                                <div>
                                  <div class="font-medium text-sm">
                                    Username / Password
                                  </div>
                                  <div class="text-xs text-mitto-text-muted">
                                    Simple credentials for login
                                  </div>
                                </div>
                              </label>
                              ${authUsername.trim() &&
                              html`
                                <div class="flex items-center gap-4 pl-7">
                                  <div class="flex items-center gap-2">
                                    <label class="text-sm text-mitto-text-muted"
                                      >Username</label
                                    >
                                    <input
                                      type="text"
                                      value=${authUsername}
                                      onInput=${(e) =>
                                        setAuthUsername(e.target.value)}
                                      placeholder="admin"
                                      class="input input-sm w-28"
                                    />
                                  </div>
                                  <div class="flex items-center gap-2">
                                    <label class="text-sm text-mitto-text-muted"
                                      >Password</label
                                    >
                                    <input
                                      type="password"
                                      value=${authPassword}
                                      onInput=${(e) => {
                                        setAuthPassword(e.target.value);
                                        if (e.target.value === "" && hasExistingPassword) {
                                          // User cleared the field while a keychain password exists
                                          // → revert to "keep existing" mode
                                          setAuthPasswordUnchanged(true);
                                        } else if (e.target.value !== "") {
                                          // User typed a new password → mark as changed
                                          setAuthPasswordUnchanged(false);
                                        }
                                      }}
                                      placeholder=${authPasswordUnchanged
                                        ? "••••••••"
                                        : "Enter password"}
                                      class="input input-sm w-28"
                                    />
                                  </div>
                                </div>
                              `}
                            </div>

                            <!-- Cloudflare Access Auth -->
                            <div
                              class="p-4 space-y-3"
                            >
                              <label class="flex items-center gap-3 cursor-pointer">
                                <input
                                  type="checkbox"
                                  checked=${cfEnabled}
                                  onChange=${(e) =>
                                    setCfEnabled(e.target.checked)}
                                  class="checkbox checkbox-sm checkbox-primary"
                                />
                                <div>
                                  <div class="font-medium text-sm">
                                    Cloudflare Access
                                  </div>
                                  <div class="text-xs text-mitto-text-muted">
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
                                      class="text-sm text-mitto-text-muted w-28"
                                      >Team Domain</label
                                    >
                                    <input
                                      type="text"
                                      value=${cfTeamDomain}
                                      onInput=${(e) =>
                                        setCfTeamDomain(e.target.value)}
                                      placeholder="yourteam.cloudflareaccess.com"
                                      class="input input-sm flex-1"
                                    />
                                  </div>
                                  <div class="flex items-center gap-2">
                                    <label
                                      class="text-sm text-mitto-text-muted w-28"
                                      >Audience</label
                                    >
                                    <input
                                      type="text"
                                      value=${cfAudience}
                                      onInput=${(e) =>
                                        setCfAudience(e.target.value)}
                                      placeholder="Application AUD tag"
                                      class="input input-sm flex-1 font-mono"
                                    />
                                  </div>
                                </div>
                              `}
                            </div>
                          </div>

                          <!-- Lifecycle Hooks -->
                          <div
                            class="p-4 space-y-3"
                          >
                            <h5 class="text-sm font-medium text-mitto-text-secondary">
                              Lifecycle Hooks
                            </h5>
                            <p class="text-xs text-mitto-text-muted">
                              Commands to run when external access starts/stops
                              (e.g., for tunneling).${" "}
                              <button
                                type="button"
                                onClick=${() =>
                                  openExternalURL(
                                    "https://github.com/inercia/mitto/blob/main/docs/config/ext-access.md",
                                  )}
                                class="text-mitto-accent hover:text-mitto-accent-300 underline cursor-pointer"
                              >
                                Learn more
                              </button>
                            </p>
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-mitto-text-muted w-12"
                                >Up</label
                              >
                              <input
                                type="text"
                                value=${hookUpCommand}
                                onInput=${(e) =>
                                  setHookUpCommand(e.target.value)}
                                placeholder="e.g., cloudflared tunnel --url http://localhost:$PORT"
                                class="input input-sm flex-1 font-mono"
                              />
                            </div>
                            <div class="flex items-center gap-2">
                              <label class="text-sm text-mitto-text-muted w-12"
                                >Down</label
                              >
                              <input
                                type="text"
                                value=${hookDownCommand}
                                onInput=${(e) =>
                                  setHookDownCommand(e.target.value)}
                                placeholder="e.g., pkill cloudflared"
                                class="input input-sm flex-1 font-mono"
                              />
                            </div>
                            <div class="flex items-center gap-2 mt-2">
                              <label class="text-sm text-mitto-text-muted w-12"
                                >URL</label
                              >
                              <input
                                type="text"
                                value=${hookExternalAddress}
                                onInput=${(e) =>
                                  setHookExternalAddress(e.target.value)}
                                placeholder="e.g., https://mitto.example.com"
                                class="input input-sm flex-1 font-mono"
                              />
                            </div>
                            <p class="text-xs text-mitto-text-muted mt-1">
                              If set, Mitto monitors this URL and restarts
                              hooks if unreachable.
                            </p>
                          </div>
                        `}
                      </div>

                      <!-- Access Log Section -->
                      <div class="space-y-3">
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Access Log
                        </h4>

                        <label
                          class="flex items-center gap-3 p-4 cursor-pointer hover:bg-base-200/40 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${accessLogEnabled}
                            onChange=${(e) =>
                              setAccessLogEnabled(e.target.checked)}
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Enable Access Log
                            </div>
                            <div class="text-xs text-mitto-text-muted">
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
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Appearance
                        </h4>
                        <div class="p-3 space-y-3">
                          <div class="text-xs text-mitto-text-muted">
                            Choose a daisyUI color theme for each mode. "Mitto
                            (default)" uses the built-in Mitto palette.
                          </div>
                          <div class="flex items-center justify-between gap-3">
                            <div class="font-medium text-sm">Light theme</div>
                            <select
                              value=${lightThemeName}
                              onInput=${(e) =>
                                handleLightThemeChange(e.target.value)}
                              class="select select-sm"
                            >
                              <option value="mitto">${THEME_LABELS.mitto}</option>
                              ${Object.entries(NAMED_THEMES)
                                .filter(([, bucket]) => bucket === "light")
                                .map(([name]) =>
                                  html`<option value=${name}>
                                    ${THEME_LABELS[name] || name}
                                  </option>`,
                                )}
                            </select>
                          </div>
                          <div class="flex items-center justify-between gap-3">
                            <div class="font-medium text-sm">Default dark theme</div>
                            <select
                              value=${darkThemeName}
                              onInput=${(e) =>
                                handleDarkThemeChange(e.target.value)}
                              class="select select-sm"
                            >
                              <option value="mitto">${THEME_LABELS.mitto}</option>
                              ${Object.entries(NAMED_THEMES)
                                .filter(([, bucket]) => bucket === "dark")
                                .map(([name]) =>
                                  html`<option value=${name}>
                                    ${THEME_LABELS[name] || name}
                                  </option>`,
                                )}
                            </select>
                          </div>
                        </div>
                        <label
                          class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${followSystemTheme}
                            onChange=${(e) =>
                              handleFollowSystemThemeChange(e.target.checked)}
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Follow system theme
                            </div>
                            <div class="text-xs text-mitto-text-muted">
                              Automatically switch between light and dark mode
                              based on your system preferences
                            </div>
                          </div>
                        </label>
                        <label
                          class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${followSystemReducedMotion}
                            onChange=${(e) =>
                              handleFollowSystemReducedMotionChange(
                                e.target.checked,
                              )}
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Follow system reduced motion
                            </div>
                            <div class="text-xs text-mitto-text-muted">
                              Automatically reduce animations based on your
                              system accessibility preferences
                            </div>
                          </div>
                        </label>
                        <label
                          class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors ${followSystemReducedMotion
                            ? "opacity-50"
                            : ""}"
                        >
                          <input
                            type="checkbox"
                            checked=${reduceAnimations}
                            onChange=${(e) =>
                              handleReduceAnimationsChange(e.target.checked)}
                            disabled=${followSystemReducedMotion}
                            class="checkbox checkbox-sm checkbox-primary ${followSystemReducedMotion
                              ? "cursor-not-allowed"
                              : ""}"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Reduce animations
                            </div>
                            <div class="text-xs text-mitto-text-muted">
                              ${followSystemReducedMotion
                                ? "Controlled by system preference"
                                : "Replace pulsing and blinking animations with static indicators"}
                            </div>
                          </div>
                        </label>
                        <div
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Prompt sorting
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                How to sort prompts in the dropdown menu
                              </div>
                            </div>
                            <select
                              value=${promptSortMode}
                              onChange=${(e) =>
                                handlePromptSortModeChange(e.target.value)}
                              class="select select-sm"
                            >
                              <option value="alphabetical">Alphabetical</option>
                              <option value="color">By Color</option>
                            </select>
                          </div>
                        </div>
                        <div
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Input box font
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Font family and size for the message compose area
                              </div>
                            </div>
                            <div class="flex items-center gap-2">
                              <select
                                value=${inputFontFamily}
                                onChange=${(e) =>
                                  setInputFontFamily(e.target.value)}
                                class="select select-sm"
                              >
                                <option value="system">System Default</option>
                                <option value="sans-serif">Sans-Serif</option>
                                <option value="serif">Serif</option>
                                <option value="monospace">Monospace</option>
                                <option value="menlo">Menlo</option>
                                <option value="monaco">Monaco</option>
                                <option value="consolas">Consolas</option>
                                <option value="courier-new">Courier New</option>
                                <option value="jetbrains-mono">JetBrains Mono</option>
                                <option value="sf-mono">SF Mono</option>
                                <option value="cascadia-code">Cascadia Code</option>
                              </select>
                              <select
                                value=${inputFontSize}
                                onChange=${(e) =>
                                  setInputFontSize(e.target.value)}
                                class="select select-sm"
                              >
                                <option value="small">Small</option>
                                <option value="default">Default</option>
                                <option value="medium">Medium</option>
                                <option value="large">Large</option>
                                <option value="xl">Extra Large</option>
                              </select>
                            </div>
                          </div>
                        </div>
                        <div
                          class="p-3"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Send message shortcut
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Key combination to send messages
                              </div>
                            </div>
                            <select
                              value=${sendKeyMode}
                              onChange=${(e) => setSendKeyMode(e.target.value)}
                              class="select select-sm"
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
                          class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
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
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Accordion mode for groups
                            </div>
                            <div class="text-xs text-mitto-text-muted">
                              When grouping is enabled, only one group can be
                              expanded at a time
                            </div>
                          </div>
                        </label>
                        <div
                          class="p-3 ${singleExpandedGroup ? "opacity-50" : ""}"
                        >
                          <div class="flex items-center justify-between">
                            <div>
                              <div class="font-medium text-sm">
                                Conversation cycling
                              </div>
                              <div class="text-xs text-mitto-text-muted">
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
                              class="select select-sm ${singleExpandedGroup ? "cursor-not-allowed" : ""}"
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
                        <h4 class="text-sm font-medium text-mitto-text-secondary">
                          Confirmations
                        </h4>
                        <label
                          class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked=${confirmDeleteSession}
                            onChange=${(e) =>
                              setConfirmDeleteSession(e.target.checked)}
                            class="checkbox checkbox-sm checkbox-primary"
                          />
                          <div>
                            <div class="font-medium text-sm">
                              Confirm before deleting conversations
                            </div>
                            <div class="text-xs text-mitto-text-muted">
                              Show a confirmation dialog when deleting a
                              conversation
                            </div>
                          </div>
                        </label>
                        ${isMacApp &&
                        html`
                          <label
                            class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${confirmQuitWithRunningSessions}
                              onChange=${(e) =>
                                setConfirmQuitWithRunningSessions(
                                  e.target.checked,
                                )}
                              class="checkbox checkbox-sm checkbox-primary"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Confirm before quitting with active
                                conversations
                              </div>
                              <div class="text-xs text-mitto-text-muted">
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
                          <h4 class="text-sm font-medium text-mitto-text-secondary">
                            macOS Settings
                          </h4>
                          <label
                            class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${agentCompletedSound}
                              onChange=${(e) =>
                                setAgentCompletedSound(e.target.checked)}
                              class="checkbox checkbox-sm checkbox-primary"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Play sound when agent completes
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Play a notification sound when the AI finishes
                                responding
                              </div>
                            </div>
                          </label>
                          <label
                            class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${nativeNotifications}
                              onChange=${(e) => {
                                // Simply save the preference - permission will be requested on app restart
                                setNativeNotifications(e.target.checked);
                              }}
                              class="checkbox checkbox-sm checkbox-primary"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Native notifications
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Show notifications in macOS Notification Center
                                (requires restart)
                                ${notificationPermissionStatus === 1
                                  ? html`<span class="text-mitto-warning ml-1"
                                      >(permission denied in System
                                      Settings)</span
                                    >`
                                  : ""}
                              </div>
                            </div>
                          </label>
                          <label
                            class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                          >
                            <input
                              type="checkbox"
                              checked=${showInAllSpaces}
                              onChange=${(e) =>
                                setShowInAllSpaces(e.target.checked)}
                              class="checkbox checkbox-sm checkbox-primary"
                            />
                            <div>
                              <div class="font-medium text-sm">
                                Show in all Spaces
                              </div>
                              <div class="text-xs text-mitto-text-muted">
                                Make the window visible in all macOS Spaces
                                (requires restart)
                              </div>
                            </div>
                          </label>
                          ${loginItemSupported &&
                          html`
                            <label
                              class="flex items-center gap-3 p-3 cursor-pointer hover:bg-base-200/40 transition-colors"
                            >
                              <input
                                type="checkbox"
                                checked=${startAtLogin}
                                onChange=${(e) =>
                                  setStartAtLogin(e.target.checked)}
                                class="checkbox checkbox-sm checkbox-primary"
                              />
                              <div>
                                <div class="font-medium text-sm">
                                  Start at Login
                                </div>
                                <div class="text-xs text-mitto-text-muted">
                                  Launch Mitto automatically when you log in
                                </div>
                              </div>
                            </label>
                          `}

                          <!-- Open Folder Action -->
                          <div
                            class="p-4 space-y-2"
                          >
                            <div class="font-medium text-sm">
                              Open folder command
                            </div>
                            <div class="text-xs text-mitto-text-muted mb-2">
                              Command to open workspace folder from badges and group header buttons. Leave empty to disable.
                            </div>
                            <div class="flex items-center gap-2">
                              <input
                                type="text"
                                value=${badgeClickCommand}
                                onInput=${(e) =>
                                  setBadgeClickCommand(e.target.value)}
                                placeholder="open \${MITTO_WORKING_DIR}"
                                class="input input-sm flex-1 font-mono"
                              />
                            </div>
                            <p class="text-xs text-mitto-text-muted">
                              Use${" "}
                              <code class="bg-mitto-surface-4 px-1 rounded"
                                >\${MITTO_WORKING_DIR}</code
                              >${" "} as placeholder for the workspace path
                            </p>
                          </div>

                          <!-- Terminal Action -->
                          <div
                            class="p-4 space-y-2"
                          >
                            <div class="font-medium text-sm">
                              Open terminal command
                            </div>
                            <div class="text-xs text-mitto-text-muted mb-2">
                              Command to open a terminal at the workspace folder from group header buttons. Leave empty to disable.
                            </div>
                            <div class="flex items-center gap-2">
                              <input
                                type="text"
                                value=${terminalActionCommand}
                                onInput=${(e) =>
                                  setTerminalActionCommand(e.target.value)}
                                placeholder="open -a Terminal \${MITTO_WORKING_DIR}"
                                class="input input-sm flex-1 font-mono"
                              />
                            </div>
                            <p class="text-xs text-mitto-text-muted">
                              Use${" "}
                              <code class="bg-mitto-surface-4 px-1 rounded"
                                >\${MITTO_WORKING_DIR}</code
                              >${" "} as placeholder for the workspace path
                            </p>
                          </div>
                        </div>
                      `}

                    </div>
                  `}
                `}
          </div>
        </div>

        <!-- Footer -->
        <div class="p-4 border-t border-mitto-border-1">
          ${error &&
          html`
            <div
              role="alert"
              class="alert alert-error alert-soft text-sm mb-3"
            >
              ${error}
            </div>
          `}
          ${warning &&
          html`
            <div
              role="alert"
              class="alert alert-warning alert-soft text-sm mb-3"
            >
              ${warning}
            </div>
          `}
          <div class="flex justify-end gap-3">
            ${canClose &&
            html`
              <button
                onClick=${handleClose}
                data-testid="settings-close"
                class="btn btn-ghost btn-sm"
              >
                Close
              </button>
            `}
            <button
              onClick=${handleSave}
              data-testid="settings-save"
              disabled=${saving}
              class="btn btn-primary btn-sm gap-2"
            >
              ${saving
                ? html`
                    <${SpinnerIcon} className="w-4 h-4" />
                    Saving...
                  `
                : "Save"}
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
            toAdd.forEach(assignStableKey);
            setAcpServers([...acpServers, ...toAdd]);
          }
          setShowDiscoverAgents(false);
        }}
      />
    </div>
  `;
}
