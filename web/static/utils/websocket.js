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

// Export constants for testing
export const WEBSOCKET_CONSTANTS = {
  MAX_RECENT_SEQS,
  RECONNECT_BASE_DELAY_MS,
  RECONNECT_MAX_DELAY_MS,
  RECONNECT_JITTER_FACTOR,
};
