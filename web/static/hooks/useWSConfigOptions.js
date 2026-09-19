// =============================================================================
// Mitto Web Interface — WebSocket ConfigOptions sub-hook
// Extracted from useWebSocket.js (mitto-90f.5). Writes the active session's
// config_options into stores/configOptionsStore.js (mitto-sus.11) so
// consumers subscribe directly instead of receiving it prop-drilled through
// useWebSocket -> App, and exposes a setter that dispatches a
// set_config_option message.
// =============================================================================

const { useEffect, useCallback } = window.preact;

import { setConfigOptions } from "../stores/configOptionsStore.js";

export function useWSConfigOptions(
  activeSession,
  activeSessionId,
  sendToSession,
) {
  // Write configOptions into the per-session store (per-session, not global).
  // The store no-ops on a reference-equal value, so an active-session `info`
  // touch that leaves `config_options` untouched (the common case) does not
  // notify subscribers -- only a genuine config_option_changed update does.
  useEffect(() => {
    if (!activeSessionId) return;
    setConfigOptions(activeSessionId, activeSession?.info?.config_options);
  }, [activeSession, activeSessionId]);

  // Change a session config option value
  // For mode changes, use configId "mode" with the desired mode value
  const setConfigOption = useCallback(
    (configId, value) => {
      // Use value == null to allow falsy values like empty strings
      if (!activeSessionId || !configId || value == null) return;
      console.log(`Setting config option: ${configId} = ${value}`);
      sendToSession(activeSessionId, {
        type: "set_config_option",
        data: { config_id: configId, value: value },
      });
    },
    [activeSessionId, sendToSession],
  );

  return { setConfigOption };
}
