// Mitto Web Interface - workspacesStore/configOptionsStore hooks (mitto-sus.11)
//
// Thin useState + useEffect wrappers around stores/workspacesStore.js and
// stores/configOptionsStore.js's subscribe functions (Preact does not
// export useSyncExternalStore), mirroring hooks/useSessionsStore.js and
// hooks/useQueue.js.

const { useState, useEffect } = window.preact;

import {
  getWorkspaces,
  getAcpServers,
  subscribeWorkspaces,
  subscribeAcpServers,
} from "../stores/workspacesStore.js";
import {
  getConfigOptions,
  subscribeConfigOptions,
} from "../stores/configOptionsStore.js";

/** Subscribes to the global workspaces array. */
export function useWorkspaces() {
  const [value, setValue] = useState(() => getWorkspaces());

  useEffect(() => {
    setValue(getWorkspaces());
    return subscribeWorkspaces(setValue);
  }, []);

  return value;
}

/** Subscribes to the global ACP servers array. */
export function useAcpServers() {
  const [value, setValue] = useState(() => getAcpServers());

  useEffect(() => {
    setValue(getAcpServers());
    return subscribeAcpServers(setValue);
  }, []);

  return value;
}

/** Subscribes to a single session's config_options array. Returns [] when unset. */
export function useConfigOptions(sessionId) {
  const [value, setValue] = useState(() =>
    sessionId ? getConfigOptions(sessionId) : [],
  );

  useEffect(() => {
    if (!sessionId) {
      setValue([]);
      return undefined;
    }
    setValue(getConfigOptions(sessionId));
    return subscribeConfigOptions(sessionId, setValue);
  }, [sessionId]);

  return value;
}
