// Mitto Web Interface - Subscribable per-session config-options store (mitto-sus.11)
//
// Generalizes the mitto-sus.7 sessionsStore.js pattern (module-level Map +
// per-session-id Set of listeners) to the `config_options` field
// useWSConfigOptions.js used to derive via a `useMemo` on the active
// session's `info` object.
//
// Problem this addresses: the old `useMemo(() => activeSession?.info
// ?.config_options || [], [activeSession, activeSessionId])` recomputed
// (and returned a fresh-looking value through useWebSocket's return object)
// on EVERY change to the active session's `info` object -- not just actual
// config_options changes -- because `info` is rebuilt as a new object on
// almost every WS event for that session (see useWebSocket.js's `connected`/
// `acp_started`/`config_option_changed` handlers). Since useWebSocket
// returns configOptions and App destructures it, this forced a full App
// re-render (and every prop-drilled consumer, e.g. ChatInput/SessionPanel)
// on unrelated active-session activity.
//
// Consumers: see hooks/useWorkspacesStore.js's useConfigOptions(sessionId),
// which wraps subscribeConfigOptions with useState + useEffect (Preact does
// not export useSyncExternalStore).
//
// Writer: useWSConfigOptions.js's effect, watching activeSession.info
// .config_options. Reference-equality diffing here (mirroring
// queueStore.js's setters) means a session `info` touch that leaves
// `config_options` at the same array reference (the common case -- see
// `config_options: msg.data.config_options ?? session.info?.config_options
// ?? []` in useWebSocket.js) is a silent no-op, and only a genuine
// `config_option_changed` update (which rebuilds the array via `.map()`)
// notifies subscribers.

/** @type {Map<string, object[]>} */
const configOptionsBySession = new Map();

/** @type {Map<string, Set<(value: object[]) => void>>} */
const listeners = new Map();

function sliceListeners(sessionId) {
  let subs = listeners.get(sessionId);
  if (!subs) {
    subs = new Set();
    listeners.set(sessionId, subs);
  }
  return subs;
}

function notify(sessionId, value) {
  const subs = listeners.get(sessionId);
  if (!subs || subs.size === 0) return;
  for (const callback of subs) {
    try {
      callback(value);
    } catch {
      // A misbehaving listener must not break the store or other listeners.
    }
  }
}

/** Returns the current config_options array for a session (or [] if none). */
export function getConfigOptions(sessionId) {
  return configOptionsBySession.get(sessionId) || [];
}

/** Subscribes to config_options changes for a session id. */
export function subscribeConfigOptions(sessionId, callback) {
  if (!sessionId) return () => {};
  const subs = sliceListeners(sessionId);
  subs.add(callback);
  return () => {
    subs.delete(callback);
    if (subs.size === 0) listeners.delete(sessionId);
  };
}

/**
 * Sets a session's config_options array. No-ops (does not notify) when the
 * value is reference-equal to the current value.
 */
export function setConfigOptions(sessionId, options) {
  if (!sessionId) return;
  const next = options || [];
  const prev = configOptionsBySession.get(sessionId);
  if (prev === next) return;
  configOptionsBySession.set(sessionId, next);
  notify(sessionId, next);
}

/** Test-only: reset the shared store between test cases. */
export function _resetConfigOptionsStoreForTests() {
  configOptionsBySession.clear();
  listeners.clear();
}
