// Mitto Web Interface - Composer draft store (mitto-sus.6)
//
// Per-session composer draft text, isolated from App's render tree. Before
// this module, draft text lived in App's `sessionDrafts` state: every
// keystroke set state on App, forcing the entire App subtree (MessageList,
// sidebars, panels, dialogs) to reconcile for what is purely local textarea
// state. This module replaces that with a plain module-level store plus a
// channel-per-session-id subscription, so only the mounted ChatInput
// instance for the affected session re-renders.
//
// Not a Preact/React context: a context provider still re-renders every
// consumer on every write. Subscribing per session id keeps writes O(1) with
// no re-render fan-out to unrelated sessions.

const NO_SESSION_KEY = "__no_session__";

/** @type {Map<string, string>} */
const drafts = new Map();

/** @type {Map<string, Set<(text: string) => void>>} */
const listeners = new Map();

function resolveKey(sessionId) {
  return sessionId ?? NO_SESSION_KEY;
}

/** Returns the current draft text for a session (or "" if none). */
export function getDraft(sessionId) {
  return drafts.get(resolveKey(sessionId)) || "";
}

/**
 * Sets the draft text for a session and notifies any subscribers for that
 * session id. Safe to call for a session other than the one currently
 * mounted (e.g. a background improve-prompt completion for a session the
 * user has since switched away from) — only that session's own subscribers
 * (if any are mounted) are notified.
 */
export function setDraft(sessionId, text) {
  const key = resolveKey(sessionId);
  drafts.set(key, text);
  const subs = listeners.get(key);
  if (!subs) return;
  for (const callback of subs) {
    try {
      callback(text);
    } catch {
      // A misbehaving listener must not break the store or other listeners.
    }
  }
}

/**
 * Subscribes to draft-text changes for a single session id. Returns an
 * unsubscribe function. Does not invoke `callback` with the current value on
 * subscribe — callers that need the current value should read it via
 * `getDraft` first (e.g. on mount / session-id change).
 */
export function subscribe(sessionId, callback) {
  const key = resolveKey(sessionId);
  let subs = listeners.get(key);
  if (!subs) {
    subs = new Set();
    listeners.set(key, subs);
  }
  subs.add(callback);
  return () => {
    subs.delete(callback);
    if (subs.size === 0) listeners.delete(key);
  };
}

/**
 * Returns a plain-object snapshot of all current drafts, keyed the same way
 * the old App-owned `sessionDrafts` map was (null session under
 * "__no_session__"). Intended for cross-session readers that need to see
 * every draft at once rather than subscribing to a single session id.
 */
export function snapshot() {
  return Object.fromEntries(drafts);
}

/** Test-only: reset the shared store between test cases. */
export function _resetDraftStoreForTests() {
  drafts.clear();
  listeners.clear();
}
