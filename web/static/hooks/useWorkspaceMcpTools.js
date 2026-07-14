// MCP-tools + live-ACP data-loading cluster extracted from WorkspacesDialog.js
// Owns:
//   - mcpTools / mcpToolsLoading / mcpToolsError state
//   - hasLiveAcp state
//   - loadMcpTools(acpServer, uuid) callback
//   - checkLiveAcpForWorkspace(uuid) callback
//   - reset effect on workspace change
//   - MCP tab load effect
const { useState, useEffect, useCallback } = window.preact;

import { authFetch, endpoints, errorMessageFromData } from "../utils/index.js";

export function useWorkspaceMcpTools({
  activeTab,
  selectedWorkspace,
  selectedWorkspaceKey,
  selectedFolder,
  editAcpServer,
}) {
  const [mcpTools, setMcpTools] = useState(null);
  const [mcpToolsLoading, setMcpToolsLoading] = useState(false);
  const [mcpToolsError, setMcpToolsError] = useState("");
  const [hasLiveAcp, setHasLiveAcp] = useState(false);

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

  // Reset mcp state on workspace change (previously inline in shell's
  // per-workspace populate effect, keyed on [selectedWorkspaceKey]).
  useEffect(() => {
    setMcpTools(null);
    setMcpToolsError("");
  }, [selectedWorkspaceKey]);

  // Load MCP tools + live-ACP status when the MCP tab is active for a workspace.
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
    // loadMcpTools/checkLiveAcpForWorkspace are stable useCallbacks with []
    // deps — safe to omit from deps.
  }, [activeTab, selectedWorkspaceKey, editAcpServer]);

  return {
    mcpTools,
    mcpToolsLoading,
    mcpToolsError,
    setMcpToolsError,
    hasLiveAcp,
    setHasLiveAcp,
    loadMcpTools,
    checkLiveAcpForWorkspace,
  };
}
