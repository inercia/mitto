// =============================================================================
// Mitto Web Interface — WebSocket Workspaces sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Owns workspaces + acpServers state, exposes fetch/add/remove callbacks,
// and keeps workspacesRef in sync for potential callback consumers.
// =============================================================================

const { useState, useEffect, useRef, useCallback } = window.preact;

import { secureFetch, authFetch } from "../utils/csrf.js";
import { endpoints } from "../utils/index.js";

export function useWSWorkspaces() {
  // Workspaces state: list of configured workspaces from server
  const [workspaces, setWorkspaces] = useState([]);
  // Available ACP servers from config
  const [acpServers, setAcpServers] = useState([]);

  const workspacesRef = useRef(workspaces); // For accessing workspaces in callbacks

  // Fetch workspaces and ACP servers
  const fetchWorkspaces = useCallback(async () => {
    try {
      const response = await authFetch(endpoints.workspaces.list());
      if (response.ok) {
        const data = await response.json();
        setWorkspaces(data.workspaces || []);
        setAcpServers(data.acp_servers || []);
      }
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
        const response = await secureFetch(endpoints.workspaces.create(), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            working_dir: workingDir,
            acp_server: acpServer,
          }),
        });

        if (!response.ok) {
          let msg = "Failed to add workspace";
          try {
            const data = await response.json();
            msg = data.error?.message || msg;
          } catch (_e) {
            /* keep default */
          }
          return { error: msg };
        }

        const data = await response.json();
        // Refresh workspaces list
        await fetchWorkspaces();
        return { workspace: data };
      } catch (err) {
        console.error("Failed to add workspace:", err);
        return { error: err.message || "Failed to add workspace" };
      }
    },
    [fetchWorkspaces],
  );

  // Remove a workspace
  const removeWorkspace = useCallback(
    async (workingDir) => {
      try {
        const response = await secureFetch(
          endpoints.workspaces.list({ working_dir: workingDir }),
          {
            method: "DELETE",
          },
        );

        if (!response.ok) {
          // Try to parse as JSON for structured errors
          const contentType = response.headers.get("content-type");
          if (contentType && contentType.includes("application/json")) {
            const errorData = await response.json();
            const error = new Error(
              errorData.error?.message || "Failed to remove workspace",
            );
            error.code = errorData.error?.code;
            error.conversationCount =
              errorData.error?.details?.conversation_count;
            throw error;
          }
          const errorText = await response.text();
          throw new Error(errorText);
        }

        // Refresh workspaces list
        await fetchWorkspaces();
      } catch (err) {
        console.error("Failed to remove workspace:", err);
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
