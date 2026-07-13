// =============================================================================
// Mitto Web Interface — WebSocket ConfigOptions sub-hook
// Extracted from useWebSocket.js (mitto-90f.5). Derives per-session
// configOptions from the active session's info and exposes a setter that
// dispatches a set_config_option message.
// =============================================================================

const { useMemo, useCallback } = window.preact;

export function useWSConfigOptions(
  activeSession,
  activeSessionId,
  sendToSession,
) {
  // Derive configOptions from the active session's info (per-session, not global)
  const configOptions = useMemo(() => {
    if (!activeSessionId) return [];
    return activeSession?.info?.config_options || [];
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

  return { configOptions, setConfigOption };
}
