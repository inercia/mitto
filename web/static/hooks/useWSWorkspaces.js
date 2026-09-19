// =============================================================================
// Mitto Web Interface — WebSocket Workspaces sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Owns the REST callbacks (fetch/add/remove); the workspaces/acpServers data
// itself lives in stores/workspacesStore.js (mitto-sus.11) so consumers
// subscribe directly instead of receiving it prop-drilled through
// useWebSocket -> App.
// =============================================================================

const { useEffect, useCallback } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";
import {
  getWorkspaces,
  setWorkspaces,
  setAcpServers,
} from "../stores/workspacesStore.js";

export function useWSWorkspaces() {
  // Fetch workspaces and ACP servers
  const fetchWorkspaces = useCallback(async () => {
    try {
      const data = await getSdkClient().workspaces.list();
      setWorkspaces(data.workspaces || []);
      setAcpServers(data.acp_servers || []);
    } catch (err) {
      console.error("Failed to fetch workspaces:", err);
    }
  }, []);

  // Fetch workspaces on mount
  useEffect(() => {
    fetchWorkspaces();
  }, [fetchWorkspaces]);

  // Add a new workspace
  const addWorkspace = useCallback(
    async (workingDir, acpServer) => {
      try {
        const data = await getSdkClient().workspaces.create({
          working_dir: workingDir,
          acp_server: acpServer,
        });
        // Refresh workspaces list
        await fetchWorkspaces();
        return { workspace: data };
      } catch (err) {
        console.error("Failed to add workspace:", err);
        return { error: errorMessage(err, "Failed to add workspace") };
      }
    },
    [fetchWorkspaces],
  );

  // Remove a workspace. This callback is exposed on the return value but has
  // no live caller in the current UI (WorkspacesLeftPanel's "Delete" button
  // uses useWorkspaceMutations' client-side-staged removeWorkspace instead,
  // keyed by uuid+working_dir). The SDK's workspaces.remove() only accepts a
  // uuid (matching handleRemoveWorkspace's preferred lookup key), not the
  // legacy working_dir query param this callback's signature takes, so the
  // uuid is resolved from the already-fetched workspaces list first.
  const removeWorkspace = useCallback(
    async (workingDir) => {
      try {
        const ws = getWorkspaces().find(
          (w) => w.working_dir === workingDir,
        );
        if (!ws?.uuid) {
          throw new Error("Workspace not found");
        }
        await getSdkClient().workspaces.remove(ws.uuid);
        // Refresh workspaces list
        await fetchWorkspaces();
      } catch (err) {
        console.error("Failed to remove workspace:", err);
        if (err?.details?.conversation_count !== undefined) {
          err.conversationCount = err.details.conversation_count;
        }
        throw err;
      }
    },
    [fetchWorkspaces],
  );

  return {
    fetchWorkspaces,
    addWorkspace,
    removeWorkspace,
  };
}
