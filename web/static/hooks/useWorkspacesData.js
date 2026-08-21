// Config-loading cluster extracted from WorkspacesDialog.js. Owns the raw
// data useStates (workspaces, ACP servers, model profiles, supported runners,
// orphaned workspaces) plus loading/error flags, and the single async loadData
// function that populates them from /config + /runners/supported.
//
// Selection state (selectedWorkspaceKey/selectedFolder) stays in the shell:
// loadData receives its getter/setters as props and mutates them to preserve
// the previously-selected workspace across a reload/reopen.
const { useState, useCallback } = window.preact;

import { fetchConfig } from "../utils/index.js";
import { getSdkClient } from "../utils/sdkClient.js";
import { errorStatus } from "../utils/sdkErrors.js";
import { getBasename } from "../lib.js";

export function useWorkspacesData({
  prevSelectedWorkspaceKeyRef,
  selectedWorkspaceKey,
  setSelectedWorkspaceKey,
  setSelectedFolder,
  getWorkspaceKey,
}) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [workspaces, setWorkspaces] = useState([]);
  const [acpServers, setAcpServers] = useState([]);
  const [modelProfiles, setModelProfiles] = useState([]);
  const [supportedRunners, setSupportedRunners] = useState([]);
  const [orphanedWorkspaces, setOrphanedWorkspaces] = useState([]);

  const loadData = useCallback(async () => {
    // Reset the flush tracker so stale edit-field values from a previous dialog
    // session are not flushed onto a workspace after a reload/reopen.
    prevSelectedWorkspaceKeyRef.current = null;
    setLoading(true);
    try {
      const [config, runnersResult] = await Promise.all([
        fetchConfig(null, true),
        // A non-2xx status falls back to the default runner list below
        // (mirrors the old `if (runnersRes.ok)` guard); only a
        // network-level failure propagates to the outer catch, same as the
        // old raw `fetch()`'s rejection.
        getSdkClient()
          .serverConfig.supportedRunners()
          .then((data) => ({ ok: true, data }))
          .catch((err) => {
            if (errorStatus(err) === undefined) throw err;
            return { ok: false };
          }),
      ]);
      const servers = config.acp_servers || [];
      setAcpServers(servers);
      setModelProfiles(Array.isArray(config.models) ? config.models : []);
      const serverNames = new Set(servers.map((s) => s.name));
      const rawWorkspaces = config.workspaces || [];
      const orphaned = [];
      const valid = rawWorkspaces.filter((ws) => {
        if (!ws.working_dir || ws.working_dir.trim() === "") return false;
        if (!ws.acp_server || !serverNames.has(ws.acp_server)) {
          if (ws.acp_server)
            orphaned.push({
              working_dir: ws.working_dir,
              missing_server: ws.acp_server,
            });
          return false;
        }
        return true;
      });
      setWorkspaces(valid);
      setOrphanedWorkspaces(orphaned);
      setSelectedFolder(null);
      if (valid.length > 0) {
        // Preserve the previously-selected workspace across a reload/reopen when it
        // still exists. Otherwise the selection resets to valid[0], whose order is
        // not stable (it reflects the backend's map-iteration order, not the sorted
        // tree). That made a just-saved edit appear "lost": the dialog reopened on a
        // different workspace that legitimately still showed its own value. When no
        // prior selection matches, fall back to a deterministic first entry (sorted
        // by display name, then ACP server) so the initial selection is predictable.
        const prevKey = selectedWorkspaceKey;
        const preserved =
          prevKey && valid.some((ws) => getWorkspaceKey(ws) === prevKey);
        if (preserved) {
          setSelectedWorkspaceKey(prevKey);
        } else {
          const firstByName = [...valid].sort((a, b) => {
            const an = a.name || getBasename(a.working_dir) || "";
            const bn = b.name || getBasename(b.working_dir) || "";
            return (
              an.localeCompare(bn) ||
              (a.acp_server || "").localeCompare(b.acp_server || "")
            );
          })[0];
          setSelectedWorkspaceKey(getWorkspaceKey(firstByName));
        }
      } else {
        setSelectedWorkspaceKey(null);
      }
      if (runnersResult.ok) {
        setSupportedRunners(runnersResult.data || []);
      } else {
        setSupportedRunners([
          { type: "exec", label: "exec (no restrictions)", supported: true },
          {
            type: "sandbox-exec",
            label: "sandbox-exec (macOS)",
            supported: false,
          },
          { type: "firejail", label: "firejail (Linux)", supported: false },
          { type: "docker", label: "docker (all platforms)", supported: true },
        ]);
      }
    } catch (err) {
      setError("Failed to load configuration: " + err.message);
    } finally {
      setLoading(false);
    }
  }, [
    prevSelectedWorkspaceKeyRef,
    selectedWorkspaceKey,
    setSelectedWorkspaceKey,
    setSelectedFolder,
    getWorkspaceKey,
  ]);

  return {
    loading,
    setLoading,
    error,
    setError,
    workspaces,
    setWorkspaces,
    acpServers,
    setAcpServers,
    modelProfiles,
    supportedRunners,
    orphanedWorkspaces,
    loadData,
  };
}
