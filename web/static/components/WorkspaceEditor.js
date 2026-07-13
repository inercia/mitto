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
import { ModelTagSelect } from "./ModelTagSelect.js";

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
              >ACP Server</label
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
              >ACP Command Override (optional)</label
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
              Custom command line for running the ACP server. Leave empty to use
              the default.
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
                  Using ACP server default: ${srvValue}
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
              MCP servers configured for this workspace's ACP
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
                      class="overflow-x-auto border border-mitto-border rounded-md"
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
                                  <td
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
