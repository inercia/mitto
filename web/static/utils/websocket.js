/**
 * WebSocket utility functions for sequence number tracking, deduplication,
 * and reconnection handling.
 *
 * These functions are extracted from useWebSocket.js for testability.
 */

import { getLastSeenSeq, setLastSeenSeq } from "./storage.js";

// =============================================================================
// H1: Sequence Number Tracking
// =============================================================================

/**
 * Update lastSeenSeq if the new seq is higher than the current stored value.
 * This ensures we track the highest seq seen during streaming, so reconnection
 * sync requests are up-to-date even if the client disconnects mid-stream.
 *
 * @param {string} sessionId - The session ID
 * @param {number|undefined} seq - The sequence number from the event
 */
export function updateLastSeenSeqIfHigher(sessionId, seq) {
  if (!seq || seq <= 0) return;
  const currentSeq = getLastSeenSeq(sessionId);
  if (seq > currentSeq) {
    setLastSeenSeq(sessionId, seq);
  }
}

// =============================================================================
// M1: Client-Side Deduplication
// =============================================================================

// Maximum number of recent seqs to track per session
// This prevents unbounded memory growth while still catching duplicates
const MAX_RECENT_SEQS = 100;

/**
 * Create a new seq tracker for a session.
 * @returns {{highestSeq: number, recentSeqs: Set<number>}}
 */
export function createSeqTracker() {
  return { highestSeq: 0, recentSeqs: new Set() };
}

/**
 * Check if a sequence number has already been seen for a session.
 * Returns true if this is a duplicate that should be skipped.
 * For coalescing events (same seq as last message), returns false to allow appending.
 *
 * @param {{highestSeq: number, recentSeqs: Set<number>}} tracker - The seq tracker
 * @param {number} seq - The sequence number to check
 * @param {number|undefined} lastMessageSeq - The seq of the last message (for coalescing)
 * @returns {boolean} True if this is a duplicate that should be skipped
 */
export function isSeqDuplicate(tracker, seq, lastMessageSeq) {
  if (!seq || seq <= 0) return false; // No seq = can't deduplicate

  // Allow same seq as last message (coalescing/continuation)
  if (lastMessageSeq && seq === lastMessageSeq) return false;

  // Check if we've seen this seq before
  if (tracker.recentSeqs.has(seq)) {
    return true;
  }

  // Quick check: if seq is much lower than highest, it's likely a duplicate
  if (seq < tracker.highestSeq - MAX_RECENT_SEQS) {
    return true;
  }

  return false;
}

/**
 * Mark a sequence number as seen.
 * Should be called after successfully processing an event.
 *
 * @param {{highestSeq: number, recentSeqs: Set<number>}} tracker - The seq tracker
 * @param {number} seq - The sequence number to mark as seen
 */
export function markSeqSeen(tracker, seq) {
  if (!seq || seq <= 0) return;

  // Add to recent seqs
  tracker.recentSeqs.add(seq);

  // Update highest seq
  if (seq > tracker.highestSeq) {
    tracker.highestSeq = seq;
  }

  // Prune old seqs to prevent unbounded growth
  if (tracker.recentSeqs.size > MAX_RECENT_SEQS) {
    const minSeq = tracker.highestSeq - MAX_RECENT_SEQS;
    for (const s of tracker.recentSeqs) {
      if (s < minSeq) {
        tracker.recentSeqs.delete(s);
      }
    }
  }
}

// =============================================================================
// M2: Exponential Backoff
// =============================================================================

// Exponential backoff configuration for WebSocket reconnection
// Prevents thundering herd when server restarts
const RECONNECT_BASE_DELAY_MS = 1000; // Initial delay: 1 second
const RECONNECT_MAX_DELAY_MS = 30000; // Maximum delay: 30 seconds
const RECONNECT_JITTER_FACTOR = 0.3; // Add up to 30% random jitter

/**
 * Calculate reconnection delay with exponential backoff and jitter.
 * @param {number} attempt - The attempt number (0-based)
 * @param {object} options - Optional configuration overrides for testing
 * @param {number} options.baseDelay - Base delay in ms (default: 1000)
 * @param {number} options.maxDelay - Max delay in ms (default: 30000)
 * @param {number} options.jitterFactor - Jitter factor 0-1 (default: 0.3)
 * @param {function} options.random - Random function for testing (default: Math.random)
 * @returns {number} Delay in milliseconds
 */
export function calculateReconnectDelay(attempt, options = {}) {
  const baseDelay = options.baseDelay ?? RECONNECT_BASE_DELAY_MS;
  const maxDelay = options.maxDelay ?? RECONNECT_MAX_DELAY_MS;
  const jitterFactor = options.jitterFactor ?? RECONNECT_JITTER_FACTOR;
  const random = options.random ?? Math.random;

  // Exponential backoff: base * 2^attempt, capped at max
  const exponentialDelay = Math.min(baseDelay * Math.pow(2, attempt), maxDelay);

  // Add jitter to prevent thundering herd
  const jitter = exponentialDelay * jitterFactor * random();

  return Math.floor(exponentialDelay + jitter);
}

// =============================================================================
// Reconnect Debounce
// =============================================================================

// Default debounce window for force-reconnect (ms)
// 3s window collapses multi-source triggers (visibility change, keepalive miss,
// native app activate) that can fire 1–6s apart into a single reconnect.
const RECONNECT_DEBOUNCE_MS = 3000;

// Maximum number of consecutive reconnect attempts before giving up on a session.
// After this many failures, the client assumes the session is permanently gone
// and stops retrying to prevent error storms (see: "Session not found" error storm).
// At 30s max backoff, 15 attempts ≈ ~3.5 minutes of retrying before giving up.
const MAX_SESSION_RECONNECT_ATTEMPTS = 15;

/**
 * Create a per-session reconnect debounce tracker.
 * Returns an object that can be passed to shouldDebounceReconnect.
 * @returns {Object} tracker - { timestamps: {} }
 */
export function createReconnectDebounceTracker() {
  return { timestamps: {} };
}

/**
 * Check whether a force-reconnect for the given session should be debounced
 * (skipped). If the same session was reconnected within `windowMs` ago, returns
 * true (skip). Otherwise records the current time and returns false (proceed).
 *
 * This implements a leading-edge debounce: the first call goes through
 * immediately; subsequent calls within the window are suppressed.
 *
 * @param {Object} tracker - Created by createReconnectDebounceTracker()
 * @param {string} sessionId - The session to check
 * @param {object} [options] - Optional overrides for testing
 * @param {number} [options.windowMs] - Debounce window (default: 500)
 * @param {function} [options.now] - Clock function (default: Date.now)
 * @returns {{ debounced: boolean, elapsed: number }} debounced=true means skip
 */
export function shouldDebounceReconnect(tracker, sessionId, options = {}) {
  const windowMs = options.windowMs ?? RECONNECT_DEBOUNCE_MS;
  const now = (options.now ?? Date.now)();
  const lastTime = tracker.timestamps[sessionId] || 0;
  const elapsed = now - lastTime;

  if (lastTime > 0 && elapsed < windowMs) {
    return { debounced: true, elapsed };
  }

  tracker.timestamps[sessionId] = now;
  return { debounced: false, elapsed };
}

// =============================================================================
// Reconnection Seq Watermark
// =============================================================================

/**
 * Check whether a session still exists on the server via a lightweight REST call.
 * Used by the WebSocket reconnect loop to detect "session not found" (HTTP 404)
 * and stop retrying for permanently gone sessions.
 *
 * The WebSocket API does not expose the HTTP status code when the server rejects
 * the upgrade (e.g., with 404), so we must make a separate REST request to
 * distinguish "session gone" from transient network errors.
 *
 * @param {string} sessionId - The session ID to check
 * @param {function} fetchFn - Fetch function to use (e.g., authFetch for credentials)
 * @param {function} apiUrlFn - Function to build API URLs (e.g., apiUrl)
 * @returns {Promise<{exists: boolean, networkError: boolean}>}
 */
export async function checkSessionExists(sessionId, fetchFn, apiUrlFn) {
  try {
    const response = await fetchFn(apiUrlFn(`/api/sessions/${sessionId}`));
    if (response.status === 404) {
      return { exists: false, networkError: false };
    }
    // Any other response (200, 500, etc.) — session may exist, don't give up
    return { exists: true, networkError: false };
  } catch (_err) {
    // Network error — can't determine, treat as transient
    return { exists: true, networkError: true };
  }
}

/**
 * Check whether the reconnect attempt count has exceeded the maximum allowed.
 *
 * @param {number} attempt - Current attempt number (0-based)
 * @param {Object} [options]
 * @param {number} [options.maxAttempts] - Override max attempts (default: MAX_SESSION_RECONNECT_ATTEMPTS)
 * @returns {boolean} true if the limit has been reached
 */
export function isReconnectLimitReached(attempt, options = {}) {
  const max = options.maxAttempts ?? MAX_SESSION_RECONNECT_ATTEMPTS;
  return attempt >= max;
}

// =============================================================================

/**
 * Create a per-session seq watermark tracker for reconnection.
 * This tracks the highest received sequence number per session so that
 * ws.onopen can always send the correct after_seq on reconnection,
 * even when React state (messages array) is empty.
 *
 * @returns {Object} tracker - { [sessionId]: number }
 */
export function createSeqWatermark() {
  return {};
}

/**
 * Update the seq watermark for a session if the new seq is higher.
 * Returns true if the watermark was updated.
 *
 * @param {Object} watermark - Created by createSeqWatermark()
 * @param {string} sessionId - The session ID
 * @param {number|undefined|null} seq - The sequence number
 * @returns {boolean} True if watermark was updated
 */
export function updateSeqWatermark(watermark, sessionId, seq) {
  if (!seq || seq <= 0) return false;
  if (seq > (watermark[sessionId] || 0)) {
    watermark[sessionId] = seq;
    return true;
  }
  return false;
}

/**
 * Get the watermark value for a session.
 *
 * @param {Object} watermark - Created by createSeqWatermark()
 * @param {string} sessionId - The session ID
 * @returns {number} The highest known seq, or 0
 */
export function getSeqWatermark(watermark, sessionId) {
  return watermark[sessionId] || 0;
}

/**
 * Clear the watermark for a session (e.g., on deletion or stale client reset).
 *
 * @param {Object} watermark - Created by createSeqWatermark()
 * @param {string} sessionId - The session ID
 */
export function clearSeqWatermark(watermark, sessionId) {
  delete watermark[sessionId];
}

// =============================================================================
// Circuit Breaker: Terminal Session Error Detection
// =============================================================================

/**
 * Check if a server error message indicates the session is permanently gone
 * and reconnection should stop immediately.
 *
 * This is used as defense-in-depth alongside the explicit `session_gone`
 * message type. It catches "Session not found" errors from older servers
 * that don't yet send the `session_gone` message.
 *
 * @param {string} message - The error message from the server
 * @returns {boolean} True if this is a terminal "session gone" error
 */
export function isTerminalSessionError(message) {
  if (!message) return false;
  const lower = message.toLowerCase();
  return lower.includes("session not found");
}

// Export constants for testing
export const WEBSOCKET_CONSTANTS = {
  MAX_RECENT_SEQS,
  RECONNECT_BASE_DELAY_MS,
  RECONNECT_MAX_DELAY_MS,
  RECONNECT_JITTER_FACTOR,
  RECONNECT_DEBOUNCE_MS,
  MAX_SESSION_RECONNECT_ATTEMPTS,
};
