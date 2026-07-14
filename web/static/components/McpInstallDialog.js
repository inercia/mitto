// MCP Install dialog extracted from WorkspacesDialog.js. Pure UI: all state and
// handlers are owned by useWorkspaceMcpActions (called in the shell) and passed
// in as props. Wraps ConfirmDialog with a textarea for pasting JSON, an
// optional server-name input (shown only for the single-server "format 3"),
// an optional scope selector (when the agent exposes mcp_scopes), and error/
// success message slots.
const { html } = window.preact;

import { ConfirmDialog } from "./ConfirmDialog.js";

export function McpInstallDialog({
  mcpInstallOpen,
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
  mcpTools,
  handleMcpInstall,
  setMcpInstallOpen,
}) {
  return html`
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
