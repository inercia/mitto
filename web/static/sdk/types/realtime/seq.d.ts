/** Creates a new seq tracker: { highestSeq: number, recentSeqs: Set<number> }. */
export function createSeqTracker(): {
    highestSeq: number;
    recentSeqs: Set<any>;
};
/**
 * True if `seq` has already been seen for this tracker and should be
 * treated as a duplicate. Same seq as `lastMessageSeq` is exempted
 * (coalescing/continuation of a streaming message).
 */
export function isSeqDuplicate(tracker: any, seq: any, lastMessageSeq: any): boolean;
/** Marks `seq` as seen; prunes seqs older than MAX_RECENT_SEQS below the new highest. */
export function markSeqSeen(tracker: any, seq: any): void;
/** Highest seq present in an array of events carrying a `.seq` property. */
export function getMaxSeq(events: any): number;
/**
 * True if the client's last-known seq is higher than the server's — the
 * client is holding stale state (server restart, different instance, cached
 * tab) and should discard it in favor of a fresh load from the server.
 */
export function isStaleClientState(clientLastSeq: any, serverLastSeq: any): boolean;
/**
 * True if a server error message indicates the session is permanently gone
 * and reconnection should stop. Defense-in-depth alongside the explicit
 * `session_gone` message type, for servers that only surface a generic error.
 */
export function isTerminalSessionError(message: any): any;
/**
 * Default in-memory seq watermark store — one per session, monotonic via
 * `set()`, force-clearable via `reset()` (used by stale-state recovery).
 */
export function createMemorySeqStore(): {
    get: (sessionId: any) => any;
    set: (sessionId: any, seq: any) => void;
    reset: (sessionId: any) => void;
};
/**
 * Seq watermark store backed by an injected storage adapter (getItem/
 * setItem/removeItem — the same contract as `config.storage`). Hosts derive
 * this from their environment preset, e.g. `browserEnv().storage`; this
 * module never touches `localStorage` directly.
 */
export function createStorageSeqStore(storage: any, options?: {}): {
    get(sessionId: any): number;
    set(sessionId: any, seq: any): void;
    reset(sessionId: any): void;
};
export namespace SEQ_CONSTANTS {
    export { MAX_RECENT_SEQS };
}
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
declare const MAX_RECENT_SEQS: 100;
export {};
