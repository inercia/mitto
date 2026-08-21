/**
 * Sequence-number tracking, deduplication, stale-client detection and
 * terminal-error classification — pure, dependency-free ports of the
 * algorithms in web/static/utils/websocket.js and web/static/lib.js
 * (docs/devel/js-client-library.md §4; see .augment/rules/24-web-frontend-sync.md).
 *
 * Never imports window/document/localStorage/console — the storage-backed
 * seq store is built on the injected storage contract from sdk/core/config.js
 * (getItem/setItem/removeItem), not on localStorage directly.
 */

// Maximum number of recent seqs to track per session; prevents unbounded
// memory growth while still catching duplicates.
const MAX_RECENT_SEQS = 100;

export const SEQ_CONSTANTS = { MAX_RECENT_SEQS };

/** Creates a new seq tracker: { highestSeq: number, recentSeqs: Set<number> }. */
export function createSeqTracker() {
  return { highestSeq: 0, recentSeqs: new Set() };
}

/**
 * True if `seq` has already been seen for this tracker and should be
 * treated as a duplicate. Same seq as `lastMessageSeq` is exempted
 * (coalescing/continuation of a streaming message).
 */
export function isSeqDuplicate(tracker, seq, lastMessageSeq) {
  if (!seq || seq <= 0) return false;
  if (lastMessageSeq && seq === lastMessageSeq) return false;
  if (tracker.recentSeqs.has(seq)) return true;
  if (seq < tracker.highestSeq - MAX_RECENT_SEQS) return true;
  return false;
}

/** Marks `seq` as seen; prunes seqs older than MAX_RECENT_SEQS below the new highest. */
export function markSeqSeen(tracker, seq) {
  if (!seq || seq <= 0) return;
  tracker.recentSeqs.add(seq);
  if (seq > tracker.highestSeq) tracker.highestSeq = seq;
  if (tracker.recentSeqs.size > MAX_RECENT_SEQS) {
    const minSeq = tracker.highestSeq - MAX_RECENT_SEQS;
    for (const s of tracker.recentSeqs) {
      if (s < minSeq) tracker.recentSeqs.delete(s);
    }
  }
}

/** Highest seq present in an array of events carrying a `.seq` property. */
export function getMaxSeq(events) {
  if (!events || events.length === 0) return 0;
  return Math.max(...events.map((e) => e.seq || 0));
}

/**
 * True if the client's last-known seq is higher than the server's — the
 * client is holding stale state (server restart, different instance, cached
 * tab) and should discard it in favor of a fresh load from the server.
 */
export function isStaleClientState(clientLastSeq, serverLastSeq) {
  if (!clientLastSeq || clientLastSeq <= 0) return false;
  if (!serverLastSeq || serverLastSeq <= 0) return false;
  return clientLastSeq > serverLastSeq;
}

/**
 * True if a server error message indicates the session is permanently gone
 * and reconnection should stop. Defense-in-depth alongside the explicit
 * `session_gone` message type, for servers that only surface a generic error.
 */
export function isTerminalSessionError(message) {
  if (!message) return false;
  const lower = message.toLowerCase();
  return (
    lower.includes("session not found") ||
    lower.includes("session is closed") ||
    lower.includes("session not running")
  );
}

/**
 * Default in-memory seq watermark store — one per session, monotonic via
 * `set()`, force-clearable via `reset()` (used by stale-state recovery).
 */
export function createMemorySeqStore() {
  const map = new Map();
  return {
    get: (sessionId) => map.get(sessionId) || 0,
    set: (sessionId, seq) => {
      if (seq > (map.get(sessionId) || 0)) map.set(sessionId, seq);
    },
    reset: (sessionId) => {
      map.delete(sessionId);
    },
  };
}

/**
 * Seq watermark store backed by an injected storage adapter (getItem/
 * setItem/removeItem — the same contract as `config.storage`). Hosts derive
 * this from their environment preset, e.g. `browserEnv().storage`; this
 * module never touches `localStorage` directly.
 */
export function createStorageSeqStore(storage, options = {}) {
  const keyPrefix = options.keyPrefix ?? "mitto_seq:";
  const key = (sessionId) => `${keyPrefix}${sessionId}`;

  return {
    get(sessionId) {
      const raw = storage.getItem(key(sessionId));
      const n = raw ? parseInt(raw, 10) : 0;
      return Number.isFinite(n) && n > 0 ? n : 0;
    },
    set(sessionId, seq) {
      if (!seq || seq <= 0) return;
      if (seq > this.get(sessionId)) {
        storage.setItem(key(sessionId), String(seq));
      }
    },
    reset(sessionId) {
      storage.removeItem(key(sessionId));
    },
  };
}
