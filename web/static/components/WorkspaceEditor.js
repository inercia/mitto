// Mitto Web Interface - Workspace Editor Component
// Renders the single-workspace (per-ACP-server-in-folder) editor panel: the
// tab bar (General/Runner/MCP) and each tab body. Extracted from
// WorkspacesDialog.js as part of mitto-90f.4. The parent shell retains
// ownership of the `edit*` state and MCP install/remove state and passes them
// down as props so that handleSave/prev-key-flush and the footer Restart-ACP
// button continue to operate unchanged.
const { html } = window.preact;

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
  modelOverridesForAgent,
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
      (() => {
        const agentOverrides = modelOverridesForAgent(
          editModelOverrides,
          editAcpServer,
        );
        // The API returns the saved workspace agent's catalog. Do not offer
        // those models for a different, unsaved agent selection.
        const catalogMatchesAgent = modelsData?.acp_server === editAcpServer;
        const models = catalogMatchesAgent ? modelsData?.models || [] : [];
        const tags = collectModelTags(modelProfiles);
        const updateRows = (rows) =>
          setEditModelOverrides?.((previous) =>
            replaceAgentModelOverrides(previous, editAcpServer, rows),
          );

        return html`
          <div class="space-y-4">
            <div class="flex items-start justify-between gap-3">
              <div>
                <h3 class="text-sm font-medium">
                  Model overrides for ${editAcpServer || "this agent"}
                </h3>
                <p class="text-xs text-mitto-text-muted mt-1">
                  Choose the model to use for each capability tag in this
                  workspace.
                </p>
              </div>
              <div class="flex gap-1 shrink-0">
                ${selectedWorkspace?.uuid &&
                html`<button
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
                <button
                  type="button"
                  class="btn btn-outline btn-sm"
                  data-testid="ws-model-override-add"
                  disabled=${!editAcpServer}
                  onClick=${() =>
                    updateRows([...agentOverrides, { tag: "", model_id: "" }])}
                >
                  <${PlusIcon} className="w-4 h-4" /> Add override
                </button>
              </div>
            </div>

            ${modelsLoading
              ? html`<div
                  class="flex items-center gap-2 text-sm text-mitto-text-muted"
                  role="status"
                >
                  <${SpinnerIcon} className="w-4 h-4" /> Loading available
                  models…
                </div>`
              : modelsError
                ? html`<p class="text-sm text-mitto-warning" role="status">
                    ${modelsError} Saved overrides are preserved.
                  </p>`
                : !catalogMatchesAgent || models.length === 0
                  ? html`<p class="text-sm text-mitto-text-muted" role="status">
                      ${!selectedWorkspace?.uuid
                        ? "Save this workspace before model options can be loaded."
                        : catalogMatchesAgent
                          ? "No model catalog is available yet. Start the agent to discover models; saved overrides are preserved."
                          : "Save the agent selection before its model catalog can be loaded; saved overrides are preserved."}
                    </p>`
                  : html`<p
                      class="text-xs text-mitto-text-muted"
                      data-testid="ws-model-catalog-status"
                    >
                      ${modelsData.live
                        ? "Agent is running"
                        : "Showing last-seen models"}
                      ${modelsData.updated_at
                        ? ` · Updated ${new Date(modelsData.updated_at).toLocaleString()}`
                        : ""}
                    </p>`}
            ${agentOverrides.length === 0
              ? html`<p
                  class="rounded-md border border-dashed border-mitto-border p-6 text-center text-sm text-mitto-text-muted"
                >
                  No model overrides configured for this agent.
                </p>`
              : html`<div class="space-y-2">
                  ${agentOverrides.map((override, index) => {
                    const options = modelCatalogOptions(
                      models,
                      override.model_id,
                    );
                    const rowTags =
                      tags.includes(override.tag) || !override.tag
                        ? tags
                        : [...tags, override.tag].sort((a, b) =>
                            a.localeCompare(b),
                          );
                    return html`<div
                      key=${`${editAcpServer}-${index}`}
                      class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-center gap-2"
                    >
                      <select
                        class="select select-sm w-full"
                        aria-label=${`Capability tag for override ${index + 1}`}
                        data-testid=${`ws-model-override-tag-${index}`}
                        value=${override.tag || ""}
                        onInput=${(event) =>
                          updateRows(
                            agentOverrides.map((row, rowIndex) =>
                              rowIndex === index
                                ? { ...row, tag: event.target.value }
                                : row,
                            ),
                          )}
                      >
                        <option value="">Choose a tag</option>
                        ${rowTags.length === 0 &&
                        html`<option value="" disabled>
                          (no tags defined in model profiles)
                        </option>`}
                        ${rowTags.map(
                          (tag) =>
                            html`<option key=${tag} value=${tag}>
                              ${tag}${!tags.includes(tag)
                                ? " (unavailable)"
                                : ""}
                            </option>`,
                        )}
                      </select>
                      <select
                        class="select select-sm w-full"
                        aria-label=${`Model for override ${index + 1}`}
                        data-testid=${`ws-model-override-model-${index}`}
                        value=${override.model_id || ""}
                        onInput=${(event) =>
                          updateRows(
                            agentOverrides.map((row, rowIndex) =>
                              rowIndex === index
                                ? { ...row, model_id: event.target.value }
                                : row,
                            ),
                          )}
                      >
                        <option value="">
                          ${models.length
                            ? "Choose a model"
                            : "No models available"}
                        </option>
                        ${options.map(
                          (model) =>
                            html`<option
                              key=${model.id}
                              value=${model.id}
                              title=${model.description || model.name}
                            >
                              ${model.name || model.id}
                            </option>`,
                        )}
                      </select>
                      <button
                        type="button"
                        class="btn btn-ghost btn-square btn-sm"
                        aria-label=${`Remove model override ${index + 1}`}
                        data-testid=${`ws-model-override-remove-${index}`}
                        onClick=${() =>
                          updateRows(
                            agentOverrides.filter(
                              (_, rowIndex) => rowIndex !== index,
                            ),
                          )}
                      >
                        <${TrashIcon}
                          className="w-4 h-4 text-mitto-text-muted"
                        />
                      </button>
                    </div>`;
                  })}
                </div>`}
          </div>
        `;
      })()}

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
