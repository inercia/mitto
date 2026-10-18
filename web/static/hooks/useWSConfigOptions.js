// =============================================================================
// Mitto Web Interface — WebSocket ConfigOptions sub-hook
// Extracted from useWebSocket.js (mitto-90f.5). Writes the active session's
// config_options into stores/configOptionsStore.js (mitto-sus.11) so
// consumers subscribe directly instead of receiving it prop-drilled through
// useWebSocket -> App, and exposes a setter that dispatches a
// set_config_option message.
// =============================================================================

const { useEffect, useCallback, useRef } = window.preact;

import { setConfigOptions } from "../stores/configOptionsStore.js";
import { createModelTagRequests } from "../utils/modelTagRequests.js";

export function useWSConfigOptions(
  activeSession,
  activeSessionId,
  sendToSession,
) {
  const sendRef = useRef(sendToSession);
  sendRef.current = sendToSession;
  const modelTagRequestsRef = useRef(null);
  const createRequests = () => createModelTagRequests({
    sendToSession: (sessionId, message) => sendRef.current(sessionId, message),
  });
  // Be ready for a key event before passive effects run. Setup replay below
  // still replaces a disposed controller rather than reviving it.
  if (modelTagRequestsRef.current === null) {
    modelTagRequestsRef.current = createRequests();
  }
  useEffect(() => {
    const requests = modelTagRequestsRef.current ?? createRequests();
    modelTagRequestsRef.current = requests;
    return () => {
      requests.dispose();
      modelTagRequestsRef.current = null;
    };
  }, []);
  const applyModelTag = useCallback(
    (targetSessionId, modelTag) => modelTagRequestsRef.current?.applyModelTag(targetSessionId, modelTag)
      ?? Promise.resolve({ status: "cancelled" }),
    [],
  );
  const handleModelTagApplied = useCallback(
    (socketSessionId, data) => modelTagRequestsRef.current?.handleModelTagApplied(socketSessionId, data),
    [],
  );

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

  return { setConfigOption, applyModelTag, handleModelTagApplied };
}
