// Mitto Web Interface - Subscribable per-session queue store (mitto-sus.11)
//
// Generalizes the mitto-sus.7 sessionsStore.js pattern (module-level Map +
// per-session-id + per-slice Set of listeners) to the three queue-related
// fields useWSQueue.js used to own as App-level useState: queueMessages,
// queueLength, queueConfig.
//
// Problem this addresses: useWSQueue's setQueueLength/setQueueMessages/
// setQueueConfig lived in useWebSocket.js's own state, which useWebSocket
// returns and App destructures -- so every WS-driven queue change (a
// keepalive tick, a "queue_updated" event, an add/delete/move REST
// response) re-rendered App and, through it, every prop-drilled consumer of
// useWebSocket's return value (SessionList, QueueDropdown, ChatInput) even
// when they don't render the queue at all.
//
// Consumers: see hooks/useQueue.js, which wraps these subscribe functions
// with useState + useEffect (Preact does not export useSyncExternalStore).
// See docs/devel/frontend-render-domains.md for the render-domain contract
// this store is part of.
//
// Writers: useWSQueue.js (REST fetch/add/delete/move callbacks) and
// useWebSocket.js's per-session message handler (the "connected" handshake,
// queue_updated, queue_message_titled, queue_reordered, and the
// keepalive_ack sync) write per the session id the message actually
// concerns -- not necessarily the active session -- so a background
// session's queue changes never notify the active session's (or any other
// session's) subscribers.

/** @typedef {"messages" | "length" | "config"} QueueSlice */

/** Default queue configuration, mirrors the pre-migration useWSQueue.js useState initializer. */
export const DEFAULT_QUEUE_CONFIG = {
  enabled: true,
  max_size: 10,
  delay_seconds: 0,
};

/** @type {Map<string, { messages: object[], length: number, config: object }>} */
const queueBySession = new Map();

/** @type {Map<string, Map<QueueSlice, Set<(value: any) => void>>>} */
const listeners = new Map();

function sliceListeners(sessionId, slice) {
  let bySlice = listeners.get(sessionId);
  if (!bySlice) {
    bySlice = new Map();
    listeners.set(sessionId, bySlice);
  }
  let subs = bySlice.get(slice);
  if (!subs) {
    subs = new Set();
    bySlice.set(slice, subs);
  }
  return subs;
}

function notify(sessionId, slice, value) {
  const bySlice = listeners.get(sessionId);
  const subs = bySlice && bySlice.get(slice);
  if (!subs || subs.size === 0) return;
  for (const callback of subs) {
    try {
      callback(value);
    } catch {
      // A misbehaving listener must not break the store or other listeners.
    }
  }
}

function getEntry(sessionId) {
  return (
    queueBySession.get(sessionId) || {
      messages: [],
      length: 0,
      config: DEFAULT_QUEUE_CONFIG,
    }
  );
}

/** Returns the current queue messages array for a session (or [] if none). */
export function getMessages(sessionId) {
  return getEntry(sessionId).messages;
}

/** Returns the current queue length for a session (or 0 if none). */
export function getLength(sessionId) {
  return getEntry(sessionId).length;
}

/** Returns the current queue config for a session (or the default if none). */
export function getConfig(sessionId) {
  return getEntry(sessionId).config;
}

function subscribeSlice(slice) {
  return function subscribe(sessionId, callback) {
    if (!sessionId) return () => {};
    const subs = sliceListeners(sessionId, slice);
    subs.add(callback);
    return () => {
      subs.delete(callback);
      if (subs.size === 0) {
        const bySlice = listeners.get(sessionId);
        bySlice?.delete(slice);
        if (bySlice && bySlice.size === 0) listeners.delete(sessionId);
      }
    };
  };
}

/** Subscribes to messages-array changes for a session id. */
export const subscribeMessages = subscribeSlice("messages");
/** Subscribes to length changes for a session id. */
export const subscribeLength = subscribeSlice("length");
/** Subscribes to config changes for a session id. */
export const subscribeConfig = subscribeSlice("config");

/**
 * Sets a session's queue messages array. Accepts either a value or a
 * React/Preact-style functional updater `(prev) => next`, mirroring the
 * useState setter signature the pre-migration call sites already used, so
 * writers only needed a mechanical setQueueMessages -> setMessages rename.
 * No-ops (does not notify) when the resolved value is reference-equal to
 * the current value.
 */
export function setMessages(sessionId, updater) {
  if (!sessionId) return;
  const prev = getEntry(sessionId);
  const nextMessages =
    typeof updater === "function" ? updater(prev.messages) : updater;
  if (nextMessages === prev.messages) return;
  queueBySession.set(sessionId, { ...prev, messages: nextMessages });
  notify(sessionId, "messages", nextMessages);
}

/** Sets a session's queue length. See setMessages for the updater contract. */
export function setLength(sessionId, updater) {
  if (!sessionId) return;
  const prev = getEntry(sessionId);
  const nextLength =
    typeof updater === "function" ? updater(prev.length) : updater;
  if (nextLength === prev.length) return;
  queueBySession.set(sessionId, { ...prev, length: nextLength });
  notify(sessionId, "length", nextLength);
}

/** Sets a session's queue config. See setMessages for the updater contract. */
export function setConfig(sessionId, updater) {
  if (!sessionId) return;
  const prev = getEntry(sessionId);
  const nextConfig =
    typeof updater === "function" ? updater(prev.config) : updater;
  if (nextConfig === prev.config) return;
  queueBySession.set(sessionId, { ...prev, config: nextConfig });
  notify(sessionId, "config", nextConfig);
}

/**
 * Replaces any subset of a session's queue slices (messages/length/config)
 * at once; only the slices actually present as own-properties of `partial`
 * are considered, and only those whose value changed notify.
 */
export function replaceSession(sessionId, partial = {}) {
  if (!sessionId) return;
  if (Object.prototype.hasOwnProperty.call(partial, "messages")) {
    setMessages(sessionId, partial.messages);
  }
  if (Object.prototype.hasOwnProperty.call(partial, "length")) {
    setLength(sessionId, partial.length);
  }
  if (Object.prototype.hasOwnProperty.call(partial, "config")) {
    setConfig(sessionId, partial.config);
  }
}

/** Test-only: reset the shared store between test cases. */
export function _resetQueueStoreForTests() {
  queueBySession.clear();
  listeners.clear();
}
