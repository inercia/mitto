// =============================================================================
// Mitto Web Interface — WebSocket Sequence Sync sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Manages per-session sequence-number deduplication state (seenSeqsRef) and
// exposes the dedup/mark/clear API used across the hook body.
// =============================================================================

const { useRef, useCallback } = window.preact;

import {
  createSeqTracker,
  isSeqDuplicate as isSeqDuplicateUtil,
  markSeqSeen as markSeqSeenUtil,
} from "../utils/websocket.js";

export function useWSSeqSync() {
  // M1 fix: Track seen sequence numbers per session for client-side deduplication
  // { sessionId: { highestSeq: number, recentSeqs: Set<number> } }
  // Uses utility functions from utils/websocket.js for testability
  const seenSeqsRef = useRef({});

  /**
   * Get or create a seq tracker for a session.
   * @param {string} sessionId - The session ID
   * @returns {{highestSeq: number, recentSeqs: Set<number>}}
   */
  const getSeqTracker = useCallback((sessionId) => {
    if (!seenSeqsRef.current[sessionId]) {
      seenSeqsRef.current[sessionId] = createSeqTracker();
    }
    return seenSeqsRef.current[sessionId];
  }, []);

  /**
   * Check if a sequence number has already been seen for a session.
   * Wrapper around utility function that manages per-session state.
   */
  const isSeqDuplicate = useCallback(
    (sessionId, seq, lastMessageSeq) => {
      const tracker = getSeqTracker(sessionId);
      const isDuplicate = isSeqDuplicateUtil(tracker, seq, lastMessageSeq);
      if (isDuplicate) {
        console.log(
          `M1 dedup: Skipping duplicate seq ${seq} for session ${sessionId}`,
        );
      }
      return isDuplicate;
    },
    [getSeqTracker],
  );

  /**
   * Mark a sequence number as seen for a session.
   * Wrapper around utility function that manages per-session state.
   */
  const markSeqSeen = useCallback(
    (sessionId, seq) => {
      const tracker = getSeqTracker(sessionId);
      markSeqSeenUtil(tracker, seq);
    },
    [getSeqTracker],
  );

  /**
   * Clear seen sequences for a session (e.g., when session is deleted or reset).
   */
  const clearSeenSeqs = useCallback((sessionId) => {
    delete seenSeqsRef.current[sessionId];
  }, []);

  return { isSeqDuplicate, markSeqSeen, clearSeenSeqs };
}
