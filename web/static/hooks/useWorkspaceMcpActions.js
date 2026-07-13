// MCP install/remove + ACP restart action cluster extracted from
// WorkspacesDialog.js. Owns the install form state (open/json/name/scope/
// loading/error/success), the remove state + scope ref, the ephemeral ACP
// restart state (needsRestart/restarting) and 6 useCallback handlers:
//   - handleRestartAcp / handleRestartAcpClick
//   - handleMcpInstall / handleMcpRemove / handleInstallMittoMcp
//   - handleMcpRemoveConfirm
//
// setConfirmDialog + setError are shell-owned and passed in as args.
const { useState, useCallback, useRef } = window.preact;

import {
  secureFetch,
  authFetch,
  endpoints,
  errorMessageFromData,
} from "../utils/index.js";

const { html } = window.preact;

export function useWorkspaceMcpActions({
  selectedWorkspace,
  editAcpServer,
  mcpTools,
  loadMcpTools,
  checkLiveAcpForWorkspace,
  setMcpToolsError,
  setConfirmDialog,
  setError,
}) {
  // Install form state
  const [mcpInstallOpen, setMcpInstallOpen] = useState(false);
  const [mcpInstallJson, setMcpInstallJson] = useState("");
  const [mcpInstallName, setMcpInstallName] = useState("");
  const [mcpInstallScope, setMcpInstallScope] = useState("");
  const [mcpInstallLoading, setMcpInstallLoading] = useState(false);
  const [mcpInstallError, setMcpInstallError] = useState("");
  const [mcpInstallSuccess, setMcpInstallSuccess] = useState("");

  // Remove state + ref
  const [mcpRemoveLoading, setMcpRemoveLoading] = useState(false);
  const mcpRemoveScopeRef = useRef("");

  // Ephemeral restart state — resets when dialog closes (component state)
  const [needsRestart, setNeedsRestart] = useState(false);
  const [restarting, setRestarting] = useState(false);

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
  }, [selectedWorkspace, setError]);

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
  }, [selectedWorkspace, handleRestartAcp, setConfirmDialog]);

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
      setMcpToolsError,
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
    [mcpTools, handleMcpRemove, setConfirmDialog],
  );

  return {
    // Install state + setters
    mcpInstallOpen,
    setMcpInstallOpen,
    mcpInstallJson,
    setMcpInstallJson,
    mcpInstallName,
    setMcpInstallName,
    mcpInstallScope,
    setMcpInstallScope,
    mcpInstallLoading,
    setMcpInstallLoading,
    mcpInstallError,
    setMcpInstallError,
    mcpInstallSuccess,
    setMcpInstallSuccess,
    // Remove state + ref
    mcpRemoveLoading,
    mcpRemoveScopeRef,
    // Restart state + setters
    needsRestart,
    setNeedsRestart,
    restarting,
    setRestarting,
    // Handlers
    handleRestartAcp,
    handleRestartAcpClick,
    handleMcpInstall,
    handleMcpRemove,
    handleInstallMittoMcp,
    handleMcpRemoveConfirm,
  };
}
