// Mitto Web Interface - Per-slice queueStore hooks (mitto-sus.11)
//
// Thin useState + useEffect wrappers around stores/queueStore.js's subscribe
// functions (Preact does not export useSyncExternalStore), mirroring
// hooks/useSessionsStore.js. Each hook re-subscribes when `sessionId`
// changes, tearing down the previous subscription before the new one
// activates, so switching the active conversation never leaks a listener
// onto the old session id.

const { useState, useEffect } = window.preact;

import {
  getMessages,
  getLength,
  getConfig,
  subscribeMessages,
  subscribeLength,
  subscribeConfig,
  DEFAULT_QUEUE_CONFIG,
} from "../stores/queueStore.js";

function useQueueSlice(sessionId, getter, subscribe) {
  const [value, setValue] = useState(() =>
    sessionId ? getter(sessionId) : undefined,
  );

  useEffect(() => {
    if (!sessionId) {
      setValue(undefined);
      return undefined;
    }
    setValue(getter(sessionId));
    return subscribe(sessionId, setValue);
  }, [sessionId]);

  return value;
}

/** Subscribes to a single session's queued-messages array. Returns [] when unset. */
export function useQueueMessages(sessionId) {
  return useQueueSlice(sessionId, getMessages, subscribeMessages) || [];
}

/** Subscribes to a single session's queue length. Returns 0 when unset. */
export function useQueueLength(sessionId) {
  return useQueueSlice(sessionId, getLength, subscribeLength) || 0;
}

/** Subscribes to a single session's queue config. Returns the default when unset. */
export function useQueueConfig(sessionId) {
  return (
    useQueueSlice(sessionId, getConfig, subscribeConfig) ||
    DEFAULT_QUEUE_CONFIG
  );
}
