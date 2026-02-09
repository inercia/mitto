/**
 * Unit tests for WebSocket utility functions
 *
 * Tests cover:
 * - H1: updateLastSeenSeqIfHigher (sequence number tracking)
 * - M1: isSeqDuplicate, markSeqSeen (client-side deduplication)
 * - M2: calculateReconnectDelay (exponential backoff)
 */

import {
  updateLastSeenSeqIfHigher,
  createSeqTracker,
  isSeqDuplicate,
  markSeqSeen,
  calculateReconnectDelay,
  WEBSOCKET_CONSTANTS,
} from "./websocket.js";

import { getLastSeenSeq, setLastSeenSeq } from "./storage.js";

// Mock localStorage with a simple implementation
const createLocalStorageMock = () => {
  let store = {};
  return {
    getItem: (key) => store[key] || null,
    setItem: (key, value) => {
      store[key] = value.toString();
    },
    removeItem: (key) => {
      delete store[key];
    },
    clear: () => {
      store = {};
    },
  };
};

// Set up localStorage mock
const localStorageMock = createLocalStorageMock();
Object.defineProperty(global, "localStorage", {
  value: localStorageMock,
  writable: true,
});

beforeEach(() => {
  localStorageMock.clear();
});

// =============================================================================
// H1: updateLastSeenSeqIfHigher Tests
// =============================================================================

describe("updateLastSeenSeqIfHigher", () => {
  test("updates seq when current is 0", () => {
    updateLastSeenSeqIfHigher("session1", 5);
    expect(getLastSeenSeq("session1")).toBe(5);
  });

  test("updates seq when new is higher", () => {
    setLastSeenSeq("session1", 5);
    updateLastSeenSeqIfHigher("session1", 10);
    expect(getLastSeenSeq("session1")).toBe(10);
  });

  test("does not update when new is lower", () => {
    setLastSeenSeq("session1", 10);
    updateLastSeenSeqIfHigher("session1", 5);
    expect(getLastSeenSeq("session1")).toBe(10);
  });

  test("does not update when new is equal", () => {
    setLastSeenSeq("session1", 10);
    updateLastSeenSeqIfHigher("session1", 10);
    expect(getLastSeenSeq("session1")).toBe(10);
  });

  test("ignores undefined seq", () => {
    setLastSeenSeq("session1", 5);
    updateLastSeenSeqIfHigher("session1", undefined);
    expect(getLastSeenSeq("session1")).toBe(5);
  });

  test("ignores null seq", () => {
    setLastSeenSeq("session1", 5);
    updateLastSeenSeqIfHigher("session1", null);
    expect(getLastSeenSeq("session1")).toBe(5);
  });

  test("ignores zero seq", () => {
    setLastSeenSeq("session1", 5);
    updateLastSeenSeqIfHigher("session1", 0);
    expect(getLastSeenSeq("session1")).toBe(5);
  });

  test("ignores negative seq", () => {
    setLastSeenSeq("session1", 5);
    updateLastSeenSeqIfHigher("session1", -1);
    expect(getLastSeenSeq("session1")).toBe(5);
  });

  test("handles multiple sessions independently", () => {
    updateLastSeenSeqIfHigher("session1", 10);
    updateLastSeenSeqIfHigher("session2", 20);
    expect(getLastSeenSeq("session1")).toBe(10);
    expect(getLastSeenSeq("session2")).toBe(20);
  });
});

// =============================================================================
// M1: Deduplication Tests
// =============================================================================

describe("createSeqTracker", () => {
  test("creates tracker with initial values", () => {
    const tracker = createSeqTracker();
    expect(tracker.highestSeq).toBe(0);
    expect(tracker.recentSeqs).toBeInstanceOf(Set);
    expect(tracker.recentSeqs.size).toBe(0);
  });
});

describe("isSeqDuplicate", () => {
  test("returns false for first seq", () => {
    const tracker = createSeqTracker();
    expect(isSeqDuplicate(tracker, 1, undefined)).toBe(false);
  });

  test("returns false for undefined seq", () => {
    const tracker = createSeqTracker();
    expect(isSeqDuplicate(tracker, undefined, undefined)).toBe(false);
  });

  test("returns false for null seq", () => {
    const tracker = createSeqTracker();
    expect(isSeqDuplicate(tracker, null, undefined)).toBe(false);
  });

  test("returns false for zero seq", () => {
    const tracker = createSeqTracker();
    expect(isSeqDuplicate(tracker, 0, undefined)).toBe(false);
  });

  test("returns false for negative seq", () => {
    const tracker = createSeqTracker();
    expect(isSeqDuplicate(tracker, -1, undefined)).toBe(false);
  });

  test("returns true for duplicate seq", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, 5);
    expect(isSeqDuplicate(tracker, 5, undefined)).toBe(true);
  });

  test("returns false for same seq as lastMessageSeq (coalescing)", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, 5);
    // Same seq as last message = coalescing, should NOT be duplicate
    expect(isSeqDuplicate(tracker, 5, 5)).toBe(false);
  });

  test("returns true for very old seq", () => {
    const tracker = createSeqTracker();
    // Mark a high seq to set highestSeq
    tracker.highestSeq = 200;
    // Seq that's more than MAX_RECENT_SEQS below highest
    expect(isSeqDuplicate(tracker, 50, undefined)).toBe(true);
  });

  test("returns false for new seq within window", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, 100);
    // New seq within the window
    expect(isSeqDuplicate(tracker, 101, undefined)).toBe(false);
  });
});

describe("markSeqSeen", () => {
  test("adds seq to recentSeqs", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, 5);
    expect(tracker.recentSeqs.has(5)).toBe(true);
  });

  test("updates highestSeq", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, 5);
    expect(tracker.highestSeq).toBe(5);
  });

  test("updates highestSeq only when new is higher", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, 10);
    markSeqSeen(tracker, 5);
    expect(tracker.highestSeq).toBe(10);
  });

  test("ignores undefined seq", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, undefined);
    expect(tracker.recentSeqs.size).toBe(0);
    expect(tracker.highestSeq).toBe(0);
  });

  test("ignores zero seq", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, 0);
    expect(tracker.recentSeqs.size).toBe(0);
  });

  test("ignores negative seq", () => {
    const tracker = createSeqTracker();
    markSeqSeen(tracker, -1);
    expect(tracker.recentSeqs.size).toBe(0);
  });

  test("prunes old seqs when exceeding MAX_RECENT_SEQS", () => {
    const tracker = createSeqTracker();
    const maxSeqs = WEBSOCKET_CONSTANTS.MAX_RECENT_SEQS;

    // Add more than MAX_RECENT_SEQS
    for (let i = 1; i <= maxSeqs + 50; i++) {
      markSeqSeen(tracker, i);
    }

    // Old seqs should be pruned
    expect(tracker.recentSeqs.has(1)).toBe(false);
    expect(tracker.recentSeqs.has(10)).toBe(false);

    // Recent seqs should still be there
    expect(tracker.recentSeqs.has(maxSeqs + 50)).toBe(true);
    expect(tracker.recentSeqs.has(maxSeqs + 40)).toBe(true);
  });
});

// =============================================================================
// M2: Exponential Backoff Tests
// =============================================================================

describe("calculateReconnectDelay", () => {
  test("returns base delay for attempt 0", () => {
    const delay = calculateReconnectDelay(0, { jitterFactor: 0 });
    expect(delay).toBe(WEBSOCKET_CONSTANTS.RECONNECT_BASE_DELAY_MS);
  });

  test("doubles delay for each attempt", () => {
    const baseDelay = WEBSOCKET_CONSTANTS.RECONNECT_BASE_DELAY_MS;
    expect(calculateReconnectDelay(0, { jitterFactor: 0 })).toBe(baseDelay);
    expect(calculateReconnectDelay(1, { jitterFactor: 0 })).toBe(baseDelay * 2);
    expect(calculateReconnectDelay(2, { jitterFactor: 0 })).toBe(baseDelay * 4);
    expect(calculateReconnectDelay(3, { jitterFactor: 0 })).toBe(baseDelay * 8);
  });

  test("caps at max delay", () => {
    const maxDelay = WEBSOCKET_CONSTANTS.RECONNECT_MAX_DELAY_MS;
    // Attempt 10 would be 1000 * 2^10 = 1024000, but should cap at 30000
    expect(calculateReconnectDelay(10, { jitterFactor: 0 })).toBe(maxDelay);
    expect(calculateReconnectDelay(20, { jitterFactor: 0 })).toBe(maxDelay);
  });

  test("adds jitter within expected range", () => {
    const baseDelay = WEBSOCKET_CONSTANTS.RECONNECT_BASE_DELAY_MS;
    const jitterFactor = WEBSOCKET_CONSTANTS.RECONNECT_JITTER_FACTOR;

    // With random = 0, should be exactly base delay
    const minDelay = calculateReconnectDelay(0, { random: () => 0 });
    expect(minDelay).toBe(baseDelay);

    // With random = 1, should be base + full jitter
    const maxDelay = calculateReconnectDelay(0, { random: () => 1 });
    expect(maxDelay).toBe(Math.floor(baseDelay * (1 + jitterFactor)));
  });

  test("accepts custom configuration", () => {
    const delay = calculateReconnectDelay(0, {
      baseDelay: 500,
      maxDelay: 5000,
      jitterFactor: 0,
    });
    expect(delay).toBe(500);
  });

  test("respects custom max delay", () => {
    const delay = calculateReconnectDelay(10, {
      baseDelay: 1000,
      maxDelay: 5000,
      jitterFactor: 0,
    });
    expect(delay).toBe(5000);
  });

  test("returns integer values", () => {
    // With jitter, the result should still be an integer
    for (let i = 0; i < 10; i++) {
      const delay = calculateReconnectDelay(i);
      expect(Number.isInteger(delay)).toBe(true);
    }
  });

  test("delay sequence is monotonically increasing (without jitter)", () => {
    let prevDelay = 0;
    for (let i = 0; i < 10; i++) {
      const delay = calculateReconnectDelay(i, { jitterFactor: 0 });
      expect(delay).toBeGreaterThanOrEqual(prevDelay);
      prevDelay = delay;
    }
  });
});

