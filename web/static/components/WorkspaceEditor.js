// Mitto Web Interface - Workspace Editor Component
// Renders the single-workspace (per-ACP-server-in-folder) editor panel: the
// tab bar (General/Runner/MCP) and each tab body. Extracted from
// WorkspacesDialog.js as part of mitto-90f.4. The parent shell retains
// ownership of the `edit*` state and MCP install/remove state and passes them
// down as props so that handleSave/prev-key-flush and the footer Restart-ACP
// button continue to operate unchanged.
const { useState, useEffect, useRef, html } = window.preact;

import { copyToClipboard } from "../lib.js";

import {
  SpinnerIcon,
  TrashIcon,
  PlusIcon,
  RefreshIcon,
  MittoIcon,
  CopyIcon,
} from "./Icons.js";

import { RunnerRestrictionsEditor } from "./SettingsDialog.js";
import { ModelProfileSelect } from "./ModelProfileSelect.js";
import { ModelTagSelect, collectModelTags } from "./ModelTagSelect.js";
import {
  modelCatalogOptions,
  expandModelOverrideGroups,
  groupModelOverrides,
  modelOverridesForAgent,
  modelOverrideTagConflicts,
  replaceAgentModelOverrides,
} from "../utils/workspaceModelOverrides.js";

const buildMcpServerJson = (srv) => {
  const cfg = {};
  if (srv.command) cfg.command = srv.command;
  if (Array.isArray(srv.args) && srv.args.length > 0) cfg.args = srv.args;
  if (srv.url) cfg.url = srv.url;
  if (srv.env && Object.keys(srv.env).length > 0) cfg.env = srv.env;
  return JSON.stringify({ mcpServers: { [srv.name]: cfg } }, null, 2);
};

function WorkspaceModelOverridesEditor({
  selectedWorkspace,
  getWorkspaceKey,
  editAcpServer,
  editModelOverrides,
  setEditModelOverrides,
  modelProfiles,
  modelsData,
  modelsLoading,
  modelsError,
  loadModels,
  onValidityChange,
}) {
  const rows = modelOverridesForAgent(editModelOverrides, editAcpServer);
  const rowsKey = JSON.stringify(rows.map(({ tag, model_id }) => ({ tag, model_id })));
  const contextKey = `${getWorkspaceKey(selectedWorkspace)}\u0000${editAcpServer}`;
  const [draft, setDraft] = useState(() => ({
    contextKey,
    groups: groupModelOverrides(rows),
  }));
  const [openGroupKey, setOpenGroupKey] = useState(null);
  const dropdownRef = useRef(null);
  const panelRef = useRef(null);
  const triggerRef = useRef(null);
  const expectedRowsKeyRef = useRef(null);
  const groups =
    draft.contextKey === contextKey ? draft.groups : groupModelOverrides(rows);
  const conflicts = modelOverrideTagConflicts(groups);
  const incomplete = groups.some(
    (group) =>
      !group.model_id ||
      !group.tagCounts.some((entry) => entry.tag) ||
      group.tagCounts.some((entry) => !entry.tag),
  );
  const valid = !incomplete && conflicts.length === 0;

  useEffect(() => {
    if (draft.contextKey !== contextKey) {
      setDraft({ contextKey, groups: groupModelOverrides(rows) });
      setOpenGroupKey(null);
      expectedRowsKeyRef.current = null;
      return;
    }
    if (expectedRowsKeyRef.current === rowsKey) {
      expectedRowsKeyRef.current = null;
      return;
    }
    expectedRowsKeyRef.current = null;
    setDraft({ contextKey, groups: groupModelOverrides(rows) });
  }, [contextKey, rowsKey]);

  useEffect(() => {
    onValidityChange?.(valid);
  }, [valid, onValidityChange]);

  useEffect(() => {
    if (!openGroupKey) return;
    // Keep the popover fully visible inside the dialog's scroll area.
    panelRef.current?.scrollIntoView({ block: "nearest" });
  }, [openGroupKey]);

  useEffect(() => {
    if (!openGroupKey) return;
    const onOutsideClick = (event) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target)) {
        setOpenGroupKey(null);
      }
    };
    document.addEventListener("mousedown", onOutsideClick);
    return () => document.removeEventListener("mousedown", onOutsideClick);
  }, [openGroupKey]);

  const publishGroups = (nextGroups) => {
    const nextRows = expandModelOverrideGroups(nextGroups);
    expectedRowsKeyRef.current = JSON.stringify(
      nextRows.map(({ tag, model_id }) => ({ tag, model_id })),
    );
    setDraft({ contextKey, groups: nextGroups });
    setEditModelOverrides?.((previous) =>
      replaceAgentModelOverrides(previous, editAcpServer, nextRows),
    );
  };
  const models =
    modelsData?.acp_server === editAcpServer ? modelsData?.models || [] : [];
  const tags = collectModelTags(modelProfiles);
  const allTags = [...new Set([
    ...tags,
    ...groups.flatMap((group) => group.tagCounts.map((entry) => entry.tag)),
  ].filter(Boolean))].sort((a, b) => a.localeCompare(b));
  const nextOrder =
    groups.reduce(
      (max, group) =>
        Math.max(
          max,
          group.order ?? -1,
          ...group.tagCounts.flatMap((entry) => entry.orders || []),
        ),
      -1,
    ) + 1;

  const toggleTag = (groupKey, tag, checked) =>
    publishGroups(
      groups.map((group) => {
        if (group.key !== groupKey) return group;
        const tagCounts = group.tagCounts.filter((entry) => entry.tag !== tag);
        if (checked) tagCounts.push({ tag, orders: [nextOrder] });
        return { ...group, tagCounts };
      }),
    );

  const resolveConflict = (tag, choice) => {
    const modelID = choice === "__empty__" ? "" : choice;
    publishGroups(
      groups
        .map((group) => {
        const tagEntry = group.tagCounts.find((entry) => entry.tag === tag);
        if (!tagEntry) return group;
        const tagCounts = group.tagCounts.filter((entry) => entry.tag !== tag);
        if ((group.model_id || "") === modelID) tagCounts.push(tagEntry);
        return { ...group, tagCounts };
        })
        .filter((group) =>
          group.tagCounts.length > 0 ||
          !groups.some((original) =>
            original.key === group.key &&
            original.tagCounts.some((entry) => entry.tag === tag),
          ),
        ),
    );
  };

  return html`
    <div class="space-y-4">
      <div class="flex items-start justify-between gap-3">
        <div>
          <h3 class="text-sm font-medium">
            Model overrides for ${editAcpServer || "this agent"}
          </h3>
          <p class="text-xs text-mitto-text-muted mt-1">
            Choose a shared model for one or more capability tags.
          </p>
        </div>
        <div class="flex gap-1 shrink-0">
          ${selectedWorkspace?.uuid && html`<button
            type="button"
            class="btn btn-ghost btn-square btn-sm"
            data-testid="ws-model-catalog-refresh"
            aria-label="Refresh model catalog"
            disabled=${modelsLoading}
            onClick=${() => loadModels?.(selectedWorkspace.uuid)}
          >
            <${RefreshIcon}
              className=${`w-4 h-4 ${modelsLoading ? "animate-spin" : ""}`}
            />
          </button>`}
        </div>
      </div>

      ${modelsLoading
        ? html`<div class="flex items-center gap-2 text-sm text-mitto-text-muted" role="status">
            <${SpinnerIcon} className="w-4 h-4" /> Loading available models…
          </div>`
        : modelsError
          ? html`<p class="text-sm text-mitto-warning" role="status">
              ${modelsError} Saved overrides are preserved.
            </p>`
          : !modelsData || modelsData?.acp_server !== editAcpServer || models.length === 0
            ? html`<p class="text-sm text-mitto-text-muted" role="status">
                ${!selectedWorkspace?.uuid
                  ? "Save this workspace before model options can be loaded."
                  : modelsData?.acp_server === editAcpServer
                    ? "No model catalog is available yet. Start the agent to discover models; saved overrides are preserved."
                    : "Save the agent selection before its model catalog can be loaded; saved overrides are preserved."}
              </p>`
            : html`<p class="text-xs text-mitto-text-muted" data-testid="ws-model-catalog-status">
                ${modelsData.live ? "Agent is running" : "Showing last-seen models"}
                ${modelsData.updated_at
                  ? ` · Updated ${new Date(modelsData.updated_at).toLocaleString()}`
                  : ""}
              </p>`}

      ${conflicts.length > 0 && html`<div class="alert alert-warning" role="alert">
        <div class="w-full space-y-2">
          <p class="font-medium">Some saved tags have conflicting model assignments. Choose which model to keep for each tag before editing or saving.</p>
          ${conflicts.map(({ tag, modelIDs }) => html`
            <label class="flex flex-wrap items-center gap-2 text-sm" key=${tag}>
              <span>${tag}</span>
              <select class="select select-sm" aria-label=${`Choose model to keep for conflicting tag ${tag}`} value="" onChange=${(event) => resolveConflict(tag, event.target.value)}>
                <option value="" disabled>Choose a model to keep</option>
                ${modelIDs.map((id) => html`<option value=${id || "__empty__"} key=${id || "__empty__"}>${id || "(empty model)"}</option>`)}
              </select>
            </label>
          `)}
        </div>
      </div>`}

      ${groups.length === 0
        ? html`<p class="rounded-md border border-dashed border-mitto-border p-6 text-center text-sm text-mitto-text-muted">
            No model overrides configured for this agent.
          </p>`
        : html`<div class="space-y-2">
            ${groups.map((group, index) => {
              const selected = group.tagCounts.filter((entry) => entry.tag);
              const selectedTags = selected.map((entry) => entry.tag);
              const options = modelCatalogOptions(models, group.model_id);
              const open = openGroupKey === group.key;
              const assignedElsewhere = (tag) => groups.some(
                (other) => other.key !== group.key && other.tagCounts.some((entry) => entry.tag === tag),
              );
              const summary = selectedTags.length === 0
                ? "Choose tags"
                : selectedTags.length <= 2
                  ? selectedTags.join(", ")
                  : `${selectedTags.slice(0, 2).join(", ")} and ${selectedTags.length - 2} more`;
              return html`<div class="flex flex-row items-center gap-2" key=${group.key}>
                <div
                  class="relative flex-1 min-w-0"
                  ref=${open ? dropdownRef : null}
                  onKeyDown=${(event) => {
                    if (event.key === "Escape" && open) {
                      // Keep the surrounding Modal's window-level Escape handler from closing the dialog.
                      event.preventDefault();
                      event.stopPropagation();
                      setOpenGroupKey(null);
                      triggerRef.current?.focus();
                    }
                  }}
                >
                  <button
                    type="button"
                    class="btn btn-outline btn-sm w-full justify-between min-w-0"
                    ref=${open ? triggerRef : null}
                    aria-expanded=${open}
                    aria-controls=${`ws-model-override-tags-${index}`}
                    aria-label=${`Capability tags: ${selectedTags.join(", ") || "none selected"}`}
                    data-testid=${`ws-model-override-tag-${index}`}
                    onClick=${() => {
                      setOpenGroupKey(open ? null : group.key);
                    }}
                    onKeyDown=${(event) => {
                      if (event.key === "Escape" && open) {
                        // Keep the surrounding Modal's window-level Escape handler from closing the dialog.
                        event.preventDefault();
                        event.stopPropagation();
                        setOpenGroupKey(null);
                        triggerRef.current?.focus();
                      }
                    }}
                  >
                    <span class="truncate" title=${selectedTags.join(", ")}>${summary}</span>
                    <span aria-hidden="true">▾</span>
                  </button>
                  ${open && html`<div id=${`ws-model-override-tags-${index}`} ref=${panelRef} style="max-height: 15rem" class="absolute left-0 right-0 top-full z-20 mt-1 overflow-y-auto rounded-box border border-base-300 bg-mitto-bg p-2 shadow-lg" role="group" aria-label="Capability tags">
                    ${allTags.length === 0
                      ? html`<p class="p-2 text-xs text-mitto-text-muted">No tags defined in model profiles.</p>`
                      : allTags.map((tag) => {
                          const checked = selectedTags.includes(tag);
                          const assigned = assignedElsewhere(tag);
                          return html`<label class="flex items-center gap-2 rounded px-2 py-1.5 text-sm ${assigned ? "opacity-60" : "cursor-pointer hover:bg-base-200"}" key=${tag} title=${assigned ? "Already assigned to another model group" : tag}>
                            <input type="checkbox" class="checkbox checkbox-sm" checked=${checked} disabled=${assigned || conflicts.some((conflict) => conflict.tag === tag)} onChange=${(event) => toggleTag(group.key, tag, event.target.checked)} />
                            <span>${tag}${!tags.includes(tag) ? " (unavailable)" : ""}</span>
                            ${assigned && html`<span class="sr-only">Already assigned to another model group</span>`}
                          </label>`;
                        })}
                  </div>`}
                </div>
                <select
                  class="select select-sm flex-1 min-w-0"
                  aria-label=${`Model for override group ${index + 1}`}
                  data-testid=${`ws-model-override-model-${index}`}
                  value=${group.model_id || ""}
                  disabled=${conflicts.length > 0}
                  onChange=${(event) => publishGroups(groups.map((row) => row.key === group.key ? { ...row, model_id: event.target.value } : row))}
                >
                  <option value="">${models.length ? "Choose a model" : "No models available"}</option>
                  ${options.map((model) => html`<option key=${model.id} value=${model.id} title=${model.description || model.name}>${model.name || model.id}</option>`)}
                </select>
                <button
                  type="button"
                  class="btn btn-ghost btn-square btn-sm text-mitto-danger"
                  aria-label=${`Remove model override group ${index + 1}`}
                  data-testid=${`ws-model-override-remove-${index}`}
                  onClick=${() => publishGroups(groups.filter((row) => row.key !== group.key))}
                >
                  <${TrashIcon} className="w-4 h-4" />
                </button>
              </div>`;
            })}
          </div>`}
      ${incomplete && html`<p class="text-sm text-mitto-warning" role="status">
        Complete or remove every draft group before saving: each group needs a model and at least one tag.
      </p>`}
      <div class="mt-3 flex items-center gap-2">
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          data-testid="ws-model-override-add"
          disabled=${!editAcpServer || conflicts.length > 0}
          onClick=${() => publishGroups([...groups, { key: `new-${Date.now()}-${groups.length}`, model_id: "", order: nextOrder, tagCounts: [] }])}
        >
          + Add override group
        </button>
      </div>
    </div>
  `;
}

export function WorkspaceEditor({
  // Tab state
  activeTab,
  setActiveTab,
  workspaceTabs,
  // Workspace + servers
  selectedWorkspace,
  getWorkspaceKey,
  sortedAcpServers,
  acpServers,
  supportedRunners,
  modelProfiles,
  effectiveConfig,
  // Edit fields (shell-owned)
  editAcpServer,
  setEditAcpServer,
  editAcpCommandOverride,
  setEditAcpCommandOverride,
  editInitialModelProfile,
  setEditInitialModelProfile,
  editInitialModelTag,
  setEditInitialModelTag,
  editModelOverrides = [],
  setEditModelOverrides,
  onModelOverridesValidityChange,
  modelsData,
  modelsLoading,
  modelsError,
  loadModels,
  editAuxModelProfile,
  setEditAuxModelProfile,
  editAuxModelTag,
  setEditAuxModelTag,
  setEditAuxModelConstraintCleared,
  auxLegacyModelLabel,
  rawAuxModelConstraint,
  editAutoApprove,
  setEditAutoApprove,
  editIsDefault,
  handleToggleIsDefault,
  editRunner,
  handleRunnerChange,
  editRunnerConfig,
  setEditRunnerConfig,
  // MCP subsection
  mcpTools,
  mcpToolsLoading,
  mcpToolsError,
  loadMcpTools,
  mcpInstallOpen,
  setMcpInstallOpen,
  setMcpInstallJson,
  setMcpInstallName,
  setMcpInstallScope,
  mcpInstallLoading,
  mcpInstallError,
  setMcpInstallError,
  mcpInstallSuccess,
  setMcpInstallSuccess,
  handleInstallMittoMcp,
  handleMcpRemoveConfirm,
  mcpRemoveLoading,
  // Notifications
  showToast,
}) {
  return html`
    <!-- Workspace tab bar (daisyUI radio tabs-border) -->
    <div role="tablist" class="tabs tabs-border px-4 shrink-0">
      ${workspaceTabs.map(
        (tab) => html`
          <input
            key=${tab.id}
            type="radio"
            name="ws-workspace-tabs"
            role="tab"
            aria-label=${tab.label}
            data-testid=${`ws-tab-${tab.id}`}
            checked=${activeTab === tab.id}
            onChange=${() => setActiveTab(tab.id)}
            class="tab ${activeTab === tab.id
              ? "tab-active text-mitto-accent"
              : ""}"
          />
        `,
      )}
    </div>

    <!-- Workspace tab content -->
    <div class="flex-1 overflow-y-auto p-6" data-testid="ws-tab-content">
      <!-- Workspace General tab -->
      ${activeTab === "general" &&
      html`
        <div class="space-y-4">
          <div>
            <label class="block text-sm text-mitto-text-muted mb-1"
              >Agent</label
            >
            <select
              value=${editAcpServer}
              onChange=${(e) => setEditAcpServer(e.target.value)}
              class="select select-sm w-full"
              style="height: 38px; box-sizing: border-box"
            >
              ${sortedAcpServers.map(
                (s) =>
                  html`<option key=${s.name} value=${s.name}>
                    ${s.name}
                  </option>`,
              )}
            </select>
          </div>
          <div>
            <label class="block text-sm text-mitto-text-muted mb-1"
              >Agent Command Override (optional)</label
            >
            <input
              type="text"
              value=${editAcpCommandOverride}
              onInput=${(e) => setEditAcpCommandOverride(e.target.value)}
              placeholder=${(() => {
                const s = acpServers.find((s) => s.name === editAcpServer);
                return s ? s.command : "";
              })()}
              class="input input-sm w-full placeholder:text-mitto-text-muted"
              style="height: 38px; box-sizing: border-box"
            />
            <p class="text-xs text-mitto-text-muted mt-1">
              Custom command line for running the agent. Leave empty to use the
              default.
            </p>
          </div>
          <div>
            <label class="block text-sm text-mitto-text-muted mb-1"
              >Initial Model (optional)</label
            >
            <p class="text-xs text-mitto-text-muted mb-2">
              Apply this model as the baseline for every new conversation
              created in this workspace
            </p>
            <div class="flex items-center gap-2">
              <div class="flex-1 min-w-0">
                <${ModelProfileSelect}
                  value=${editInitialModelProfile}
                  profiles=${modelProfiles}
                  className="w-full"
                  onChange=${(name) => {
                    setEditInitialModelProfile(name);
                    if (name) {
                      setEditInitialModelTag("");
                    }
                  }}
                />
              </div>
              <span class="text-xs text-mitto-text-muted shrink-0"
                >or by tag</span
              >
              <div class="flex-1 min-w-0">
                <${ModelTagSelect}
                  value=${editInitialModelTag}
                  profiles=${modelProfiles}
                  className="w-full"
                  onChange=${(tag) => {
                    setEditInitialModelTag(tag);
                    if (tag) {
                      setEditInitialModelProfile("");
                    }
                  }}
                />
              </div>
            </div>
            ${(() => {
              // Precedence hint: when this workspace has no
              // initial-model preference of its own but its ACP
              // server does, surface which value will be used.
              if (editInitialModelProfile || editInitialModelTag) return null;
              const srv = acpServers.find((s) => s.name === editAcpServer);
              const srvValue =
                srv && (srv.initial_model_profile || srv.initial_model_tag);
              if (!srvValue) return null;
              return html`
                <p class="text-xs text-mitto-text-muted mt-1">
                  Using agent default: ${srvValue}
                </p>
              `;
            })()}
          </div>
          <div>
            <label class="block text-sm text-mitto-text-muted mb-1"
              >Auxiliary Model Selection (optional)</label
            >
            <p class="text-xs text-mitto-text-muted mb-2">
              Switch auxiliary sessions (titles, suggestions) to a specific
              model
            </p>
            <div class="flex items-center gap-2">
              <div class="flex-1 min-w-0">
                <${ModelProfileSelect}
                  value=${editAuxModelProfile}
                  profiles=${modelProfiles}
                  legacyLabel=${auxLegacyModelLabel}
                  className="w-full"
                  onChange=${(name) => {
                    setEditAuxModelProfile(name);
                    if (name) {
                      setEditAuxModelTag("");
                    }
                    if (!name && rawAuxModelConstraint) {
                      setEditAuxModelConstraintCleared(true);
                    }
                  }}
                />
              </div>
              <span class="text-xs text-mitto-text-muted shrink-0"
                >or by tag</span
              >
              <div class="flex-1 min-w-0">
                <${ModelTagSelect}
                  value=${editAuxModelTag}
                  profiles=${modelProfiles}
                  className="w-full"
                  onChange=${(tag) => {
                    setEditAuxModelTag(tag);
                    if (tag) {
                      setEditAuxModelProfile("");
                    }
                    if (!tag && rawAuxModelConstraint) {
                      setEditAuxModelConstraintCleared(true);
                    }
                  }}
                />
              </div>
            </div>
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
            Preferred when this folder has several workspaces and one is
            launched without a specific agent.
          </p>
        </div>
      `}

      <!-- Workspace Models tab -->
      ${activeTab === "models" &&
      html`<${WorkspaceModelOverridesEditor}
        selectedWorkspace=${selectedWorkspace}
        getWorkspaceKey=${getWorkspaceKey}
        editAcpServer=${editAcpServer}
        editModelOverrides=${editModelOverrides}
        setEditModelOverrides=${setEditModelOverrides}
        modelProfiles=${modelProfiles}
        modelsData=${modelsData}
        modelsLoading=${modelsLoading}
        modelsError=${modelsError}
        loadModels=${loadModels}
        onValidityChange=${onModelOverridesValidityChange}
      />`}

      <!-- Workspace Runner tab -->
      ${activeTab === "runner" &&
      html`
        <div class="space-y-5">
          <div>
            <label class="block text-sm text-mitto-text-muted mb-3"
              >Runner Type</label
            >
            <div class="space-y-2">
              ${supportedRunners.map(
                (r) => html`
                  <label
                    key=${r.type}
                    class="flex items-center gap-3 cursor-pointer ${!r.supported
                      ? "opacity-50"
                      : ""}"
                  >
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
                `,
              )}
            </div>
          </div>
          ${editRunner !== "exec" &&
          html`
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
      ${activeTab === "mcp" &&
      html`
        <div class="space-y-4">
          <div class="flex items-center justify-between">
            <p class="text-sm text-mitto-text-muted">
              MCP servers configured for this workspace's
              agent${mcpTools?.agent_name ? ` (${mcpTools.agent_name})` : ""}.
            </p>
            <div class="flex items-center gap-0.5">
              <button
                onClick=${() => {
                  if (mcpToolsLoading) return;
                  loadMcpTools(
                    editAcpServer || selectedWorkspace?.acp_server,
                    selectedWorkspace?.uuid,
                  );
                }}
                aria-disabled=${mcpToolsLoading ? "true" : "false"}
                class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${mcpToolsLoading
                  ? "opacity-40 pointer-events-none"
                  : ""}"
                data-tip="Refresh MCP server list"
                aria-label="Refresh MCP server list"
              >
                <${RefreshIcon}
                  className=${`w-4 h-4 ${mcpToolsLoading ? "animate-spin" : ""}`}
                />
              </button>
              ${mcpTools?.has_mcp_install &&
              html`
                <button
                  onClick=${() => {
                    if (mcpInstallLoading) return;
                    handleInstallMittoMcp();
                  }}
                  aria-disabled=${mcpInstallLoading ? "true" : "false"}
                  class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${mcpInstallLoading
                    ? "opacity-40 pointer-events-none"
                    : ""}"
                  data-tip="Install Mitto's MCP server"
                  aria-label="Install Mitto's MCP server"
                >
                  <${MittoIcon} className="w-4 h-4" />
                </button>
                <button
                  onClick=${() => {
                    setMcpInstallOpen(true);
                    setMcpInstallJson("");
                    setMcpInstallName("");
                    setMcpInstallScope(mcpTools?.mcp_scopes?.[0] || "");
                    setMcpInstallError("");
                    setMcpInstallSuccess("");
                  }}
                  class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom"
                  data-tip="Install MCP servers"
                  aria-label="Install MCP servers"
                >
                  <${PlusIcon} className="w-4 h-4" />
                </button>
              `}
            </div>
          </div>
          ${!mcpInstallOpen &&
          mcpInstallError &&
          html`
            <p class="text-sm text-mitto-danger whitespace-pre-wrap">
              ${mcpInstallError}
            </p>
          `}
          ${!mcpInstallOpen &&
          mcpInstallSuccess &&
          html`
            <p class="text-sm text-mitto-success">${mcpInstallSuccess}</p>
          `}
          ${mcpToolsLoading
            ? html`<div class="flex items-center justify-center p-8">
                <${SpinnerIcon} className="w-5 h-5 animate-spin" />
              </div>`
            : mcpToolsError
              ? html`<div class="p-4 text-center text-mitto-warning text-sm">
                  ${mcpToolsError}
                </div>`
              : mcpTools?.servers?.length === 0
                ? html`<div
                    class="p-4 text-center text-mitto-text-muted text-sm"
                  >
                    ${mcpTools?.message ||
                    "No MCP servers found for this agent."}
                  </div>`
                : html`
                    <div
                      class="overflow-hidden border border-mitto-border rounded-md"
                    >
                      <table
                        class="table table-sm"
                        style="table-layout: fixed;"
                      >
                        <colgroup>
                          <col style="width: 140px;" />
                          <col />
                          ${mcpTools?.has_mcp_remove &&
                          html`<col style="width: 72px;" />`}
                        </colgroup>
                        <thead>
                          <tr>
                            <th>Name</th>
                            <th>Command / URL</th>
                            ${mcpTools?.has_mcp_remove && html`<th></th>`}
                          </tr>
                        </thead>
                        <tbody>
                          ${mcpTools?.servers?.map(
                            (srv, i) => html`
                              <tr key=${srv.name || i}>
                                <td
                                  class="font-medium truncate"
                                  title=${srv.name}
                                >
                                  ${srv.name}
                                </td>
                                <td
                                  class="text-mitto-text-muted font-mono text-xs truncate"
                                  title=${srv.url ||
                                  [srv.command, ...(srv.args || [])].join(" ")}
                                >
                                  ${srv.url ||
                                  [srv.command, ...(srv.args || [])].join(" ")}
                                </td>
                                ${mcpTools?.has_mcp_remove &&
                                html`
                                  <td class="text-center">
                                    <div
                                      class="flex items-center justify-center gap-1"
                                    >
                                      <button
                                        onClick=${async () => {
                                          const ok = await copyToClipboard(
                                            buildMcpServerJson(srv),
                                          );
                                          showToast?.({
                                            style: ok ? "success" : "error",
                                            title: ok
                                              ? `Copied ${srv.name}`
                                              : "Copy failed",
                                            duration: 2000,
                                          });
                                        }}
                                        class="btn btn-ghost btn-square btn-xs tooltip tooltip-bottom"
                                        data-tip="Copy server config as JSON"
                                        aria-label="Copy MCP server config"
                                      >
                                        <${CopyIcon}
                                          className="w-4 h-4 text-mitto-text-muted"
                                        />
                                      </button>
                                      <button
                                        onClick=${() => {
                                          if (mcpRemoveLoading) return;
                                          handleMcpRemoveConfirm(srv.name);
                                        }}
                                        aria-disabled=${mcpRemoveLoading
                                          ? "true"
                                          : "false"}
                                        class="btn btn-ghost btn-square btn-xs tooltip tooltip-bottom ${mcpRemoveLoading
                                          ? "opacity-40 pointer-events-none"
                                          : ""}"
                                        data-tip="Remove MCP server"
                                        aria-label="Remove MCP server"
                                      >
                                        <${TrashIcon}
                                          className="w-4 h-4 text-mitto-text-muted hover:text-mitto-danger"
                                        />
                                      </button>
                                    </div>
                                  </td>
                                `}
                              </tr>
                            `,
                          )}
                        </tbody>
                      </table>
                    </div>
                  `}
        </div>
      `}
    </div>
  `;
}
