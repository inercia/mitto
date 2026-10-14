// Loads the workspace's last-seen advertised model catalog for the Models tab.
const { useState, useEffect, useCallback } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";

export function useWorkspaceModels({
  activeTab,
  selectedWorkspace,
  selectedWorkspaceKey,
}) {
  const [modelsData, setModelsData] = useState(null);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState("");

  const loadModels = useCallback(async (uuid) => {
    setModelsError("");
    setModelsData(null);
    if (!uuid) {
      setModelsLoading(false);
      return;
    }
    setModelsLoading(true);
    try {
      const data = await getSdkClient().workspaces.getModels(uuid);
      setModelsData({
        ...data,
        models: Array.isArray(data?.models) ? data.models : [],
      });
    } catch (err) {
      setModelsError(
        `Failed to load models: ${errorMessage(err, "request failed")}`,
      );
      setModelsData({ models: [] });
    } finally {
      setModelsLoading(false);
    }
  }, []);

  useEffect(() => {
    setModelsData(null);
    setModelsError("");
  }, [selectedWorkspaceKey]);

  useEffect(() => {
    if (activeTab === "models" && selectedWorkspace) {
      loadModels(selectedWorkspace.uuid);
    }
  }, [activeTab, selectedWorkspaceKey]);

  return { modelsData, modelsLoading, modelsError, loadModels };
}
