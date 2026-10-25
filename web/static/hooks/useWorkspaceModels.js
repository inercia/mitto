// Loads the workspace's last-seen advertised model catalog for the Models tab.
const { useState, useEffect, useCallback, useRef } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";

export function useWorkspaceModels({
  activeTab,
  selectedWorkspace,
  selectedWorkspaceKey,
  editAcpServer,
}) {
  const [modelsData, setModelsData] = useState(null);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState("");

  // Current scope (uuid, server, folder) and mount state, read when a response
  // lands so stale or post-unmount results are dropped.
  const scopeRef = useRef({});
  const mountedRef = useRef(true);
  scopeRef.current = {
    uuid: selectedWorkspace?.uuid,
    server: editAcpServer ?? selectedWorkspace?.acp_server,
    dir: selectedWorkspace?.working_dir,
  };

  const loadModels = useCallback(async (uuid) => {
    const requested = { ...scopeRef.current, uuid };
    const isCurrent = () => {
      const cur = scopeRef.current;
      return (
        mountedRef.current &&
        cur.uuid === requested.uuid &&
        cur.server === requested.server &&
        cur.dir === requested.dir
      );
    };
    setModelsError("");
    setModelsData(null);
    if (!uuid) {
      setModelsLoading(false);
      return;
    }
    setModelsLoading(true);
    try {
      const data = await getSdkClient().workspaces.getModels(uuid);
      if (!isCurrent()) return;
      setModelsData({
        ...data,
        models: Array.isArray(data?.models) ? data.models : [],
      });
    } catch (err) {
      if (!isCurrent()) return;
      setModelsError(
        `Failed to load models: ${errorMessage(err, "request failed")}`,
      );
      setModelsData({ models: [] });
    } finally {
      if (isCurrent()) setModelsLoading(false);
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    setModelsData(null);
    setModelsError("");
  }, [selectedWorkspaceKey, editAcpServer]);

  useEffect(() => {
    if (activeTab === "models" && selectedWorkspace) {
      loadModels(selectedWorkspace.uuid);
    }
  }, [activeTab, selectedWorkspaceKey]);

  return { modelsData, modelsLoading, modelsError, loadModels };
}
