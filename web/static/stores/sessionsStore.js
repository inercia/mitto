// Mitto Web Interface - Subscribable per-session store (mitto-sus.7)
//
// Generalizes the mitto-sus.6 draftStore pattern (module-level map + per-key
// subscription, not a Preact context) from composer-draft text to a slice
// view of the full per-session server state useWebSocket.js owns as one
// monolithic `sessions` React state object.
//
// Problem this addresses: useWebSocket calls setSessions() on every WS chunk
// to ANY session, changing the top-level `sessions` reference on every
// background-session tick. Because App destructures ~40 values out of
// useWebSocket (many derived from `sessions`), that single reference change
// re-renders App and its entire subtree -- SessionList, MessageList,
// ChatInput, dialogs -- even when none of them consume the session that
// actually changed.
//
// This store lets a component subscribe to only the SLICE of a single
// session's data it actually renders (messages / summary / info /
// keepalive), so a background chunk that only changes `sessions[B].messages`
// notifies ONLY the subscribers of session B's messages slice -- not session
// B's summary subscribers, and not any subscriber of a different session id.
//
// Consumers: see hooks/useSessionsStore.js, which wraps these subscribe
// functions with useState + useEffect (Preact does not export
// useSyncExternalStore). See docs/devel/frontend-render-domains.md for the
// full render-domain contract this store is part of.
//
// Scope of this increment (mitto-sus.7): the store is kept in sync via a
// single `replaceAll(sessions)` call from one useEffect in useWebSocket.js
// (mirrors the existing `sessionsRef.current = sessions` effect) rather than
// the finer-grained per-mutation calls (applyChunk/setSessionInfo/...)
// originally sketched in the Plan comment -- replaceAll's internal per-slice
// diffing is functionally equivalent while touching only one call site
// instead of a dozen call sites spread across a 4500+ line hook, which
// keeps this increment's risk bounded. SessionList / MessageList / ChatInput
// do not yet consume these hooks in place of their current props -- that
// migration is deferred to a follow-up increment (see the bead's
// Implementation comment for what remains).

/** @typedef {"messages" | "summary" | "info" | "keepalive"} SessionSlice */

/** @type {Map<string, object>} */
const sessionsById = new Map();

/** @type {Map<string, Map<SessionSlice, Set<(value: any) => void>>>} */
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

/**
 * Extracts the sidebar-visible "summary" slice from a raw session entry, as
 * a narrow explicit field list (rather than the whole `info` object) so
 * unrelated `info` churn (e.g. isReadOnly toggling mid-session) doesn't wake
 * summary subscribers unnecessarily. Field names mirror useWebSocket.js's
 * `session.info.{name,working_dir,archived,pinned,loop_enabled,
 * background_color}` shape (see session_pinned / set-color / loop_updated
 * handlers).
 */
function extractSummary(session) {
  const info = session?.info || {};
  return {
    name: info.name,
    working_dir: info.working_dir,
    archived: info.archived || false,
    pinned: info.pinned || false,
    loop_enabled: info.loop_enabled || false,
    background_color: info.background_color,
    isStreaming: session?.isStreaming || false,
    isWaitingForChildren: session?.isWaitingForChildren || false,
  };
}

/** Extracts the keepalive-relevant slice from a raw session entry. */
function extractKeepalive(session) {
  return {
    lastSeq: session?.lastSeq || 0,
    isRunning: session?.isRunning ?? false,
  };
}

function shallowEqual(a, b) {
  if (a === b) return true;
  if (!a || !b) return false;
  const aKeys = Object.keys(a);
  const bKeys = Object.keys(b);
  if (aKeys.length !== bKeys.length) return false;
  for (const key of aKeys) {
    if (a[key] !== b[key]) return false;
  }
  return true;
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

/** Returns the current messages array for a session (or [] if none). */
export function getMessages(sessionId) {
  return sessionsById.get(sessionId)?.messages || [];
}

/** Returns the current summary slice for a session (or null if none). */
export function getSummary(sessionId) {
  const session = sessionsById.get(sessionId);
  return session ? extractSummary(session) : null;
}

/** Returns the current info object for a session (or null if none). */
export function getInfo(sessionId) {
  return sessionsById.get(sessionId)?.info || null;
}

/** Returns the current keepalive slice for a session (or null if none). */
export function getKeepalive(sessionId) {
  const session = sessionsById.get(sessionId);
  return session ? extractKeepalive(session) : null;
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
/** Subscribes to summary-field changes for a session id. */
export const subscribeSummary = subscribeSlice("summary");
/** Subscribes to full info-object changes for a session id. */
export const subscribeInfo = subscribeSlice("info");
/** Subscribes to keepalive-field changes for a session id. */
export const subscribeKeepalive = subscribeSlice("keepalive");

/**
 * Replaces the entire store contents from a fresh `sessions` map (the same
 * shape as useWebSocket.js's `sessions` React state) and notifies only the
 * slice subscribers whose value actually changed for each touched session
 * id, plus clears any session id removed since the last replaceAll. Safe to
 * call on every `sessions` change -- the diffing happens here, not at the
 * caller, so a background chunk that only touches one session's messages
 * never notifies that session's (or any other session's) summary/info/
 * keepalive subscribers.
 */
export function replaceAll(nextSessionsById) {
  const nextIds = new Set(Object.keys(nextSessionsById || {}));

  // Removed sessions: notify with cleared slice values, then drop.
  for (const existingId of sessionsById.keys()) {
    if (!nextIds.has(existingId)) {
      notify(existingId, "messages", []);
      notify(existingId, "summary", null);
      notify(existingId, "info", null);
      notify(existingId, "keepalive", null);
      sessionsById.delete(existingId);
    }
  }

  for (const id of nextIds) {
    const prevSession = sessionsById.get(id);
    const nextSession = nextSessionsById[id];
    sessionsById.set(id, nextSession);

    if (prevSession?.messages !== nextSession?.messages) {
      notify(id, "messages", nextSession?.messages || []);
    }
    if (prevSession?.info !== nextSession?.info) {
      notify(id, "info", nextSession?.info || null);
    }
    const prevSummary = prevSession ? extractSummary(prevSession) : null;
    const nextSummary = extractSummary(nextSession);
    if (!shallowEqual(prevSummary, nextSummary)) {
      notify(id, "summary", nextSummary);
    }
    const prevKeepalive = prevSession ? extractKeepalive(prevSession) : null;
    const nextKeepalive = extractKeepalive(nextSession);
    if (!shallowEqual(prevKeepalive, nextKeepalive)) {
      notify(id, "keepalive", nextKeepalive);
    }
  }
}

/** Test-only: reset the shared store between test cases. */
export function _resetSessionsStoreForTests() {
  sessionsById.clear();
  listeners.clear();
}
