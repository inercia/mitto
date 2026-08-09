// MCP-tools + live-ACP data-loading cluster extracted from WorkspacesDialog.js
// Owns:
//   - mcpTools / mcpToolsLoading / mcpToolsError state
//   - hasLiveAcp state
//   - loadMcpTools(acpServer, uuid) callback
//   - checkLiveAcpForWorkspace(uuid) callback
//   - reset effect on workspace change
//   - MCP tab load effect
const { useState, useEffect, useCallback } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";

// The MCP tools endpoint occasionally answers a non-JSON error body (e.g. a
// plain-text 500 from a proxy); the SDK's MittoApiError still carries that
// raw text on `.body` (sdk/core/errors.js's `errorFromResponse` decodes
// whatever the response actually was), so prefer it over the generic
// SDK-computed `.message` to match the old ct.includes("application/json")
// branching that used to read the raw text via `res.text()`.
function mcpRequestErrorMessage(err, fallback) {
  return typeof err?.body === "string" && err.body
    ? err.body
    : errorMessage(err, fallback);
}

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
      const data = await getSdkClient().workspaces.listMcpTools(
        uuid,
        acpServer,
      );
      if (data.error) {
        setMcpToolsError(data.error);
      }
      setMcpTools(data);
    } catch (err) {
      setMcpToolsError(
        "Failed to load MCP tools: " +
          mcpRequestErrorMessage(err, "request failed"),
      );
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
      const data = await getSdkClient().workspaces.getAcpStatus(workspaceUUID);
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
