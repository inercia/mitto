// =============================================================================
// Mitto Web Interface — WebSocket Workspaces sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Owns workspaces + acpServers state, exposes fetch/add/remove callbacks,
// and keeps workspacesRef in sync for potential callback consumers.
// =============================================================================

const { useState, useEffect, useRef, useCallback } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";

export function useWSWorkspaces() {
  // Workspaces state: list of configured workspaces from server
  const [workspaces, setWorkspaces] = useState([]);
  // Available ACP servers from config
  const [acpServers, setAcpServers] = useState([]);

  const workspacesRef = useRef(workspaces); // For accessing workspaces in callbacks

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
        const ws = (workspacesRef.current || []).find(
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

  useEffect(() => {
    workspacesRef.current = workspaces;
  }, [workspaces]);

  return {
    workspaces,
    acpServers,
    workspacesRef,
    fetchWorkspaces,
    addWorkspace,
    removeWorkspace,
  };
}
