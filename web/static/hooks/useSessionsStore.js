// Mitto Web Interface - Per-slice sessionsStore hooks (mitto-sus.7)
//
// Thin useState + useEffect wrappers around stores/sessionsStore.js's
// subscribe functions (Preact does not export useSyncExternalStore). Each
// hook re-subscribes when `sessionId` changes, tearing down the previous
// subscription before the new one activates, so switching the active
// conversation never leaks a listener onto the old session id.
//
// Not yet consumed by SessionList / MessageList / ChatInput in place of
// their current props -- see docs/devel/frontend-render-domains.md and the
// mitto-sus.7 Implementation comment for what remains as follow-up work.

const { useState, useEffect } = window.preact;

import {
  getMessages,
  getSummary,
  getInfo,
  getKeepalive,
  subscribeMessages,
  subscribeSummary,
  subscribeInfo,
  subscribeKeepalive,
} from "../stores/sessionsStore.js";

function useStoreSlice(sessionId, getter, subscribe) {
  const [value, setValue] = useState(() =>
    sessionId ? getter(sessionId) : null,
  );

  useEffect(() => {
    if (!sessionId) {
      setValue(null);
      return undefined;
    }
    setValue(getter(sessionId));
    return subscribe(sessionId, setValue);
  }, [sessionId]);

  return value;
}

/** Subscribes to a single session's messages array. Returns [] when unset. */
export function useActiveSessionMessages(sessionId) {
  return useStoreSlice(sessionId, getMessages, subscribeMessages) || [];
}

/** Subscribes to a single session's sidebar-visible summary fields. */
export function useSessionSummary(sessionId) {
  return useStoreSlice(sessionId, getSummary, subscribeSummary);
}

/** Subscribes to a single session's full info object. */
export function useSessionInfo(sessionId) {
  return useStoreSlice(sessionId, getInfo, subscribeInfo);
}

/** Subscribes to a single session's keepalive-derived fields. */
export function useSessionKeepalive(sessionId) {
  return useStoreSlice(sessionId, getKeepalive, subscribeKeepalive);
}
