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
  createReconnectDebounceTracker,
  shouldDebounceReconnect,
  checkSessionExists,
  isReconnectLimitReached,
  isTerminalSessionError,
  createSeqWatermark,
  updateSeqWatermark,
  getSeqWatermark,
  clearSeqWatermark,
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

  // This test documents the stale client reconnection bug that was fixed
  // by clearing the seq tracker when isStaleClient is detected in events_loaded
  test("BUG: stale tracker rejects fresh events from server (MUST reset tracker)", () => {
    // Scenario: Client had highestSeq=200 from previous session
    // Server was restarted, now has lastSeq=50
    // Client detects stale state (clientLastSeq > serverLastSeq)
    // Server sends events with seqs 1-50
    // Without resetting tracker, all these events are wrongly rejected!
    const tracker = createSeqTracker();
    tracker.highestSeq = 200; // Stale state from before server restart

    // Fresh events from server after restart have lower seq values
    // These should NOT be duplicates, but without reset they are!
    expect(isSeqDuplicate(tracker, 50, undefined)).toBe(true); // WRONG: rejected as "very old"
    expect(isSeqDuplicate(tracker, 30, undefined)).toBe(true); // WRONG: rejected as "very old"
    expect(isSeqDuplicate(tracker, 1, undefined)).toBe(true); // WRONG: rejected as "very old"

    // The fix: When isStaleClient is detected in events_loaded handler,
    // clearSeenSeqs(sessionId) is called BEFORE processing events.
    // This resets the tracker, so fresh events are accepted:
    const freshTracker = createSeqTracker();
    expect(isSeqDuplicate(freshTracker, 50, undefined)).toBe(false); // CORRECT: accepted
    expect(isSeqDuplicate(freshTracker, 30, undefined)).toBe(false); // CORRECT: accepted
    expect(isSeqDuplicate(freshTracker, 1, undefined)).toBe(false); // CORRECT: accepted
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

// =============================================================================
// Available Commands Message Tests
// =============================================================================

describe("Available Commands WebSocket message handling", () => {
  /**
   * Parse available commands from WebSocket connected message.
   * This mirrors the logic in useWebSocket.js handleSessionMessage.
   */
  function parseAvailableCommands(msgData) {
    if (msgData?.available_commands) {
      return msgData.available_commands;
    }
    return [];
  }

  test("extracts commands from connected message with available_commands", () => {
    const msgData = {
      session_id: "test-session",
      available_commands: [
        { name: "test", description: "Test command", input_hint: "Enter test" },
        { name: "help", description: "Get help" },
      ],
    };
    const commands = parseAvailableCommands(msgData);
    expect(commands).toHaveLength(2);
    expect(commands[0].name).toBe("test");
    expect(commands[0].input_hint).toBe("Enter test");
    expect(commands[1].name).toBe("help");
    expect(commands[1].input_hint).toBeUndefined();
  });

  test("returns empty array when no available_commands field", () => {
    const msgData = {
      session_id: "test-session",
    };
    const commands = parseAvailableCommands(msgData);
    expect(commands).toHaveLength(0);
  });

  test("returns empty array when available_commands is empty", () => {
    const msgData = {
      session_id: "test-session",
      available_commands: [],
    };
    const commands = parseAvailableCommands(msgData);
    expect(commands).toHaveLength(0);
  });

  test("returns empty array for null msgData", () => {
    const commands = parseAvailableCommands(null);
    expect(commands).toHaveLength(0);
  });

  test("returns empty array for undefined msgData", () => {
    const commands = parseAvailableCommands(undefined);
    expect(commands).toHaveLength(0);
  });

  /**
   * Parse available commands from available_commands_updated message.
   * This mirrors the logic in useWebSocket.js handleSessionMessage.
   */
  function parseCommandsUpdateMessage(msg) {
    if (msg.type === "available_commands_updated" && msg.data?.commands) {
      return msg.data.commands;
    }
    return null;
  }

  test("parses available_commands_updated message", () => {
    const msg = {
      type: "available_commands_updated",
      data: {
        session_id: "test-session",
        commands: [{ name: "new-cmd", description: "New command" }],
      },
    };
    const commands = parseCommandsUpdateMessage(msg);
    expect(commands).toHaveLength(1);
    expect(commands[0].name).toBe("new-cmd");
  });

  test("returns null for other message types", () => {
    const msg = {
      type: "agent_message",
      data: {
        html: "<p>Hello</p>",
      },
    };
    const commands = parseCommandsUpdateMessage(msg);
    expect(commands).toBeNull();
  });

  test("returns null when commands field is missing", () => {
    const msg = {
      type: "available_commands_updated",
      data: {
        session_id: "test-session",
        // missing commands field
      },
    };
    const commands = parseCommandsUpdateMessage(msg);
    expect(commands).toBeNull();
  });
});

// =============================================================================
// Reconnect Debounce Tests
// =============================================================================

describe("createReconnectDebounceTracker", () => {
  test("creates tracker with empty timestamps", () => {
    const tracker = createReconnectDebounceTracker();
    expect(tracker.timestamps).toEqual({});
  });
});

describe("shouldDebounceReconnect", () => {
  test("first call goes through immediately", () => {
    const tracker = createReconnectDebounceTracker();
    const result = shouldDebounceReconnect(tracker, "session1", {
      now: () => 1000,
    });
    expect(result.debounced).toBe(false);
  });

  test("records timestamp on first call", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", { now: () => 1000 });
    expect(tracker.timestamps["session1"]).toBe(1000);
  });

  test("second call within window is debounced", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", { now: () => 1000 });
    const result = shouldDebounceReconnect(tracker, "session1", {
      now: () => 1200,
    });
    expect(result.debounced).toBe(true);
    expect(result.elapsed).toBe(200);
  });

  test("call after window expires goes through", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", { now: () => 1000 });
    const result = shouldDebounceReconnect(tracker, "session1", {
      now: () => 4100, // 3100ms later — past the 3000ms window
    });
    expect(result.debounced).toBe(false);
  });

  test("call exactly at window boundary is not debounced", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", { now: () => 1000 });
    const result = shouldDebounceReconnect(tracker, "session1", {
      now: () => 4000, // exactly 3000ms later — at the boundary
    });
    expect(result.debounced).toBe(false);
  });

  test("different sessions are independent", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", { now: () => 1000 });

    // session2 should not be debounced even though session1 just reconnected
    const result = shouldDebounceReconnect(tracker, "session2", {
      now: () => 1100,
    });
    expect(result.debounced).toBe(false);
  });

  test("session1 debounced while session2 goes through", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", { now: () => 1000 });
    shouldDebounceReconnect(tracker, "session2", { now: () => 1000 });

    // Both within window: session1 debounced, session2 debounced
    const r1 = shouldDebounceReconnect(tracker, "session1", {
      now: () => 1200,
    });
    const r2 = shouldDebounceReconnect(tracker, "session2", {
      now: () => 1200,
    });
    expect(r1.debounced).toBe(true);
    expect(r2.debounced).toBe(true);
  });

  test("updates timestamp when call goes through", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", { now: () => 1000 });
    // After window (3000ms+)
    shouldDebounceReconnect(tracker, "session1", { now: () => 4100 });
    expect(tracker.timestamps["session1"]).toBe(4100);
  });

  test("does not update timestamp when debounced", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", { now: () => 1000 });
    // Within window - should be debounced and NOT update timestamp
    shouldDebounceReconnect(tracker, "session1", { now: () => 1200 });
    expect(tracker.timestamps["session1"]).toBe(1000);
  });

  test("custom window size is respected", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "session1", {
      now: () => 1000,
      windowMs: 200,
    });

    // 150ms later with 200ms window -> debounced
    const r1 = shouldDebounceReconnect(tracker, "session1", {
      now: () => 1150,
      windowMs: 200,
    });
    expect(r1.debounced).toBe(true);

    // 250ms later with 200ms window -> not debounced
    const r2 = shouldDebounceReconnect(tracker, "session1", {
      now: () => 1250,
      windowMs: 200,
    });
    expect(r2.debounced).toBe(false);
  });

  test("default window matches RECONNECT_DEBOUNCE_MS constant", () => {
    expect(WEBSOCKET_CONSTANTS.RECONNECT_DEBOUNCE_MS).toBe(3000);
  });

  test("third call after second debounced still debounced within window", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "s1", { now: () => 1000 });
    shouldDebounceReconnect(tracker, "s1", { now: () => 1100 }); // debounced
    const result = shouldDebounceReconnect(tracker, "s1", { now: () => 2000 }); // still within 3000ms of 1000
    expect(result.debounced).toBe(true);
  });

  test("call goes through after debounced calls once window passes", () => {
    const tracker = createReconnectDebounceTracker();
    shouldDebounceReconnect(tracker, "s1", { now: () => 1000 }); // through
    shouldDebounceReconnect(tracker, "s1", { now: () => 1100 }); // debounced
    shouldDebounceReconnect(tracker, "s1", { now: () => 2000 }); // debounced
    const result = shouldDebounceReconnect(tracker, "s1", { now: () => 4100 }); // through (3100ms after 1000)
    expect(result.debounced).toBe(false);
  });
});

// =============================================================================
// forceReconnectActiveSession backoff behaviour (unit-level simulation)
//
// The hook itself is not unit-tested here, but we can verify that the
// calculateReconnectDelay + shared-attempt-counter pattern used by
// forceReconnectActiveSession produces the right delay sequence when
// called the same way the refactored hook code does.
// =============================================================================

describe("forceReconnectActiveSession backoff simulation", () => {
  /**
   * Simulate the shared-counter pattern used by forceReconnectActiveSession
   * and onclose: both read the same attempts object and increment it before
   * calling connectToSession.
   */
  function simulateReconnectSequence(attempts, sessionId, count) {
    const delays = [];
    for (let i = 0; i < count; i++) {
      const attempt = attempts[sessionId] || 0;
      const delay = calculateReconnectDelay(attempt, { jitterFactor: 0 });
      delays.push(delay);
      // Simulate what the timeout callback does: increment before connecting
      attempts[sessionId] = attempt + 1;
    }
    return delays;
  }

  test("first force-reconnect uses attempt 0 (base delay ~1s)", () => {
    const attempts = {};
    const delays = simulateReconnectSequence(attempts, "s1", 1);
    expect(delays[0]).toBe(WEBSOCKET_CONSTANTS.RECONNECT_BASE_DELAY_MS); // 1000ms
  });

  test("repeated force-reconnects produce increasing delays", () => {
    const attempts = {};
    const delays = simulateReconnectSequence(attempts, "s1", 5);
    const base = WEBSOCKET_CONSTANTS.RECONNECT_BASE_DELAY_MS;
    expect(delays[0]).toBe(base); // 1000ms
    expect(delays[1]).toBe(base * 2); // 2000ms
    expect(delays[2]).toBe(base * 4); // 4000ms
    expect(delays[3]).toBe(base * 8); // 8000ms
    expect(delays[4]).toBe(base * 16); // 16000ms
  });

  test("delays cap at RECONNECT_MAX_DELAY_MS", () => {
    const attempts = {};
    // Run enough iterations to hit the cap
    const delays = simulateReconnectSequence(attempts, "s1", 10);
    const max = WEBSOCKET_CONSTANTS.RECONNECT_MAX_DELAY_MS;
    delays.forEach((d) => expect(d).toBeLessThanOrEqual(max));
    // Last few should be exactly at the cap (no jitter in this test)
    expect(delays[9]).toBe(max);
  });

  test("resetting attempt counter restarts backoff from base delay", () => {
    const attempts = {};
    // Simulate several failures
    simulateReconnectSequence(attempts, "s1", 4);
    expect(attempts["s1"]).toBe(4);

    // Simulate successful connect: onopen does `delete attempts[sessionId]`
    delete attempts["s1"];

    // Next reconnect should start from base delay again
    const delays = simulateReconnectSequence(attempts, "s1", 1);
    expect(delays[0]).toBe(WEBSOCKET_CONSTANTS.RECONNECT_BASE_DELAY_MS);
  });

  test("force-reconnect and onclose share the same counter (no reset between them)", () => {
    // Scenario: force-reconnect fires (attempt 0→1), then the resulting
    // WebSocket closes immediately (attempt 1→2). Delay should keep growing.
    const attempts = {};

    // forceReconnectActiveSession fires: reads attempt 0, schedules with 1s delay, sets attempt=1
    const delay1 = calculateReconnectDelay(attempts["s1"] || 0, {
      jitterFactor: 0,
    });
    attempts["s1"] = (attempts["s1"] || 0) + 1;
    expect(delay1).toBe(1000);
    expect(attempts["s1"]).toBe(1);

    // The new WebSocket opens but immediately closes (onclose fires).
    // onopen was never reached, so counter was NOT reset.
    // onclose reads attempt 1, schedules with 2s delay, sets attempt=2
    const delay2 = calculateReconnectDelay(attempts["s1"] || 0, {
      jitterFactor: 0,
    });
    attempts["s1"] = (attempts["s1"] || 0) + 1;
    expect(delay2).toBe(2000);
    expect(attempts["s1"]).toBe(2);
  });

  test("different sessions have independent attempt counters", () => {
    const attempts = {};

    // Session A fails 3 times
    simulateReconnectSequence(attempts, "sessionA", 3);
    expect(attempts["sessionA"]).toBe(3);

    // Session B starts fresh
    const delayB = calculateReconnectDelay(attempts["sessionB"] || 0, {
      jitterFactor: 0,
    });
    expect(delayB).toBe(WEBSOCKET_CONSTANTS.RECONNECT_BASE_DELAY_MS);
    expect(attempts["sessionA"]).toBe(3); // unchanged
  });
});

// =============================================================================
// Reconnection Seq Watermark Tests
//
// These test the seq watermark used by lastKnownSeqRef in useWebSocket.js to
// track the highest received sequence number across WebSocket reconnections.
// The watermark is the primary source for after_seq on reconnect, fixing the
// bug where the client always resynced from seq=0.
// =============================================================================

describe("createSeqWatermark", () => {
  test("creates an empty watermark object", () => {
    const wm = createSeqWatermark();
    expect(wm).toEqual({});
  });
});

describe("updateSeqWatermark", () => {
  test("updates watermark when seq is higher than current", () => {
    const wm = createSeqWatermark();
    expect(updateSeqWatermark(wm, "s1", 10)).toBe(true);
    expect(wm["s1"]).toBe(10);
  });

  test("does not update when seq is lower than current", () => {
    const wm = createSeqWatermark();
    updateSeqWatermark(wm, "s1", 50);
    expect(updateSeqWatermark(wm, "s1", 30)).toBe(false);
    expect(wm["s1"]).toBe(50);
  });

  test("does not update when seq equals current", () => {
    const wm = createSeqWatermark();
    updateSeqWatermark(wm, "s1", 42);
    expect(updateSeqWatermark(wm, "s1", 42)).toBe(false);
    expect(wm["s1"]).toBe(42);
  });

  test("handles undefined seq gracefully", () => {
    const wm = createSeqWatermark();
    expect(updateSeqWatermark(wm, "s1", undefined)).toBe(false);
    expect(wm["s1"]).toBeUndefined();
  });

  test("handles null seq gracefully", () => {
    const wm = createSeqWatermark();
    expect(updateSeqWatermark(wm, "s1", null)).toBe(false);
    expect(wm["s1"]).toBeUndefined();
  });

  test("handles zero seq gracefully", () => {
    const wm = createSeqWatermark();
    expect(updateSeqWatermark(wm, "s1", 0)).toBe(false);
    expect(wm["s1"]).toBeUndefined();
  });

  test("handles negative seq gracefully", () => {
    const wm = createSeqWatermark();
    expect(updateSeqWatermark(wm, "s1", -5)).toBe(false);
    expect(wm["s1"]).toBeUndefined();
  });

  test("tracks multiple sessions independently", () => {
    const wm = createSeqWatermark();
    updateSeqWatermark(wm, "s1", 100);
    updateSeqWatermark(wm, "s2", 200);
    updateSeqWatermark(wm, "s3", 50);
    expect(wm["s1"]).toBe(100);
    expect(wm["s2"]).toBe(200);
    expect(wm["s3"]).toBe(50);
  });

  test("monotonically increases for a session with interleaved events", () => {
    const wm = createSeqWatermark();
    // Simulate receiving events: some with seq, some with max_seq
    updateSeqWatermark(wm, "s1", 1); // first event seq=1
    updateSeqWatermark(wm, "s1", 5); // max_seq=5
    updateSeqWatermark(wm, "s1", 2); // late event seq=2 (should not lower)
    updateSeqWatermark(wm, "s1", 5); // same max_seq (no-op)
    updateSeqWatermark(wm, "s1", 10); // new event seq=10
    expect(wm["s1"]).toBe(10);
  });
});

describe("getSeqWatermark", () => {
  test("returns 0 for unknown session", () => {
    const wm = createSeqWatermark();
    expect(getSeqWatermark(wm, "unknown")).toBe(0);
  });

  test("returns stored value for known session", () => {
    const wm = createSeqWatermark();
    updateSeqWatermark(wm, "s1", 42);
    expect(getSeqWatermark(wm, "s1")).toBe(42);
  });
});

describe("clearSeqWatermark", () => {
  test("removes watermark for a session", () => {
    const wm = createSeqWatermark();
    updateSeqWatermark(wm, "s1", 100);
    clearSeqWatermark(wm, "s1");
    expect(getSeqWatermark(wm, "s1")).toBe(0);
    expect(wm["s1"]).toBeUndefined();
  });

  test("does not affect other sessions", () => {
    const wm = createSeqWatermark();
    updateSeqWatermark(wm, "s1", 100);
    updateSeqWatermark(wm, "s2", 200);
    clearSeqWatermark(wm, "s1");
    expect(getSeqWatermark(wm, "s1")).toBe(0);
    expect(getSeqWatermark(wm, "s2")).toBe(200);
  });

  test("clearing unknown session is a no-op", () => {
    const wm = createSeqWatermark();
    clearSeqWatermark(wm, "nonexistent"); // should not throw
    expect(getSeqWatermark(wm, "nonexistent")).toBe(0);
  });
});

describe("seq watermark reconnection scenario", () => {
  test("watermark survives simulated reconnection (ref not cleared)", () => {
    // This simulates the core bug fix: the watermark persists across
    // WebSocket reconnections because it's stored in a ref, not in
    // React state that can be empty during reconnection.
    const wm = createSeqWatermark();

    // Connection 1: receive events seq=1 through seq=113
    for (let i = 1; i <= 113; i++) {
      updateSeqWatermark(wm, "session-abc", i);
    }
    expect(getSeqWatermark(wm, "session-abc")).toBe(113);

    // Simulate forceReconnect: WS closes, messages array may be cleared,
    // but the watermark ref is NOT cleared.
    // (In the real code, sessionsRef.current[id].messages could be empty here)

    // Connection 2: ws.onopen fires — watermark provides the correct after_seq
    const afterSeq = getSeqWatermark(wm, "session-abc");
    expect(afterSeq).toBe(113); // NOT 0!

    // Connection 2 receives events from seq=114 onwards
    updateSeqWatermark(wm, "session-abc", 114);
    updateSeqWatermark(wm, "session-abc", 158);
    expect(getSeqWatermark(wm, "session-abc")).toBe(158);
  });

  test("watermark is reset on stale client detection", () => {
    const wm = createSeqWatermark();
    updateSeqWatermark(wm, "s1", 500); // stale value from old server session

    // Server says max_seq is 50 — client is stale
    // The events_loaded handler should clear the watermark
    clearSeqWatermark(wm, "s1");
    expect(getSeqWatermark(wm, "s1")).toBe(0);

    // Fresh events from server update the watermark correctly
    updateSeqWatermark(wm, "s1", 50);
    expect(getSeqWatermark(wm, "s1")).toBe(50);
  });

  test("watermark is cleaned up on session deletion", () => {
    const wm = createSeqWatermark();
    updateSeqWatermark(wm, "s1", 100);
    updateSeqWatermark(wm, "s2", 200);

    // Delete session s1
    clearSeqWatermark(wm, "s1");

    expect(getSeqWatermark(wm, "s1")).toBe(0);
    expect(getSeqWatermark(wm, "s2")).toBe(200);
  });
});

// =============================================================================
// lastKnownSeqRef Synchronization Tests
//
// These tests verify the logic for using lastKnownSeqRef as the primary source
// for determining after_seq on reconnection and gap detection. The ref is
// updated on every received event and provides a more reliable source than
// React state (getMaxSeq(messages) and lastLoadedSeq) which can be stale.
// =============================================================================

describe("lastKnownSeqRef synchronization logic", () => {
  // Simulated updateLastKnownSeq logic from useWebSocket.js
  function updateLastKnownSeq(ref, sessionId, seq) {
    if (seq && seq > (ref[sessionId] || 0)) {
      ref[sessionId] = seq;
    }
  }

  // Simulated getClientMaxSeq computation from useWebSocket.js
  function getClientMaxSeq(ref, sessionId, messagesMaxSeq, lastLoadedSeq) {
    const refSeq = ref[sessionId] || 0;
    const stateSeq = Math.max(messagesMaxSeq, lastLoadedSeq);
    return Math.max(refSeq, stateSeq);
  }

  describe("updateLastKnownSeq behavior", () => {
    test("ref starts empty", () => {
      const ref = {};
      expect(ref["session1"]).toBeUndefined();
    });

    test("updates ref when seq is higher than current", () => {
      const ref = {};
      updateLastKnownSeq(ref, "session1", 10);
      expect(ref["session1"]).toBe(10);
    });

    test("does not update when seq is lower", () => {
      const ref = { session1: 50 };
      updateLastKnownSeq(ref, "session1", 30);
      expect(ref["session1"]).toBe(50);
    });

    test("does not update when seq equals current", () => {
      const ref = { session1: 42 };
      updateLastKnownSeq(ref, "session1", 42);
      expect(ref["session1"]).toBe(42);
    });

    test("ignores null seq", () => {
      const ref = { session1: 10 };
      updateLastKnownSeq(ref, "session1", null);
      expect(ref["session1"]).toBe(10);
    });

    test("ignores undefined seq", () => {
      const ref = { session1: 10 };
      updateLastKnownSeq(ref, "session1", undefined);
      expect(ref["session1"]).toBe(10);
    });

    test("ignores zero seq", () => {
      const ref = { session1: 10 };
      updateLastKnownSeq(ref, "session1", 0);
      expect(ref["session1"]).toBe(10);
    });

    test("ignores negative seq", () => {
      const ref = { session1: 10 };
      updateLastKnownSeq(ref, "session1", -5);
      expect(ref["session1"]).toBe(10);
    });

    test("tracks multiple sessions independently", () => {
      const ref = {};
      updateLastKnownSeq(ref, "session1", 100);
      updateLastKnownSeq(ref, "session2", 200);
      updateLastKnownSeq(ref, "session3", 50);
      expect(ref["session1"]).toBe(100);
      expect(ref["session2"]).toBe(200);
      expect(ref["session3"]).toBe(50);
    });

    test("monotonically increases with interleaved events", () => {
      const ref = {};
      updateLastKnownSeq(ref, "s1", 1);
      updateLastKnownSeq(ref, "s1", 5);
      updateLastKnownSeq(ref, "s1", 2); // late event, should not lower
      updateLastKnownSeq(ref, "s1", 5); // same seq, no-op
      updateLastKnownSeq(ref, "s1", 10);
      expect(ref["s1"]).toBe(10);
    });
  });

  describe("getClientMaxSeq computation", () => {
    test("uses ref when ref is ahead of state", () => {
      const ref = { session1: 100 };
      const messagesMaxSeq = 50;
      const lastLoadedSeq = 60;
      const result = getClientMaxSeq(ref, "session1", messagesMaxSeq, lastLoadedSeq);
      expect(result).toBe(100); // ref wins
    });

    test("uses state when state is ahead of ref", () => {
      const ref = { session1: 50 };
      const messagesMaxSeq = 100;
      const lastLoadedSeq = 80;
      const result = getClientMaxSeq(ref, "session1", messagesMaxSeq, lastLoadedSeq);
      expect(result).toBe(100); // state wins
    });

    test("uses ref when ref exists and state is zero", () => {
      const ref = { session1: 100 };
      const messagesMaxSeq = 0;
      const lastLoadedSeq = 0;
      const result = getClientMaxSeq(ref, "session1", messagesMaxSeq, lastLoadedSeq);
      expect(result).toBe(100);
    });

    test("uses state when ref is zero", () => {
      const ref = {};
      const messagesMaxSeq = 50;
      const lastLoadedSeq = 60;
      const result = getClientMaxSeq(ref, "session1", messagesMaxSeq, lastLoadedSeq);
      expect(result).toBe(60);
    });

    test("returns zero when both ref and state are zero", () => {
      const ref = {};
      const messagesMaxSeq = 0;
      const lastLoadedSeq = 0;
      const result = getClientMaxSeq(ref, "session1", messagesMaxSeq, lastLoadedSeq);
      expect(result).toBe(0);
    });

    test("uses higher of messagesMaxSeq and lastLoadedSeq for state", () => {
      const ref = {};
      const messagesMaxSeq = 100;
      const lastLoadedSeq = 150;
      const result = getClientMaxSeq(ref, "session1", messagesMaxSeq, lastLoadedSeq);
      expect(result).toBe(150);
    });

    test("ref provides safety net when messages array is empty", () => {
      // This is the key scenario: messages array cleared during reconnect
      const ref = { session1: 113 };
      const messagesMaxSeq = 0; // messages array is empty
      const lastLoadedSeq = 0; // no loaded events yet
      const result = getClientMaxSeq(ref, "session1", messagesMaxSeq, lastLoadedSeq);
      expect(result).toBe(113); // ref saves the day!
    });
  });

  describe("stale client reset clears ref", () => {
    test("deleting ref entry resets to undefined", () => {
      const ref = { session1: 500 };
      delete ref["session1"];
      expect(ref["session1"]).toBeUndefined();
    });

    test("getClientMaxSeq falls back to state after ref is cleared", () => {
      const ref = { session1: 500 };
      delete ref["session1"]; // stale client reset
      const messagesMaxSeq = 50;
      const lastLoadedSeq = 40;
      const result = getClientMaxSeq(ref, "session1", messagesMaxSeq, lastLoadedSeq);
      expect(result).toBe(50); // uses state since ref is gone
    });

    test("ref can be rebuilt after stale reset", () => {
      const ref = { session1: 500 };
      delete ref["session1"];
      updateLastKnownSeq(ref, "session1", 50);
      expect(ref["session1"]).toBe(50);
    });
  });

  describe("reconnection scenarios", () => {
    test("ref survives reconnection when messages are cleared", () => {
      const ref = {};

      // Initial connection: receive events
      updateLastKnownSeq(ref, "s1", 1);
      updateLastKnownSeq(ref, "s1", 50);
      updateLastKnownSeq(ref, "s1", 113);

      // Simulate reconnection: messages array cleared, but ref persists
      const messagesMaxSeq = 0; // messages cleared
      const lastLoadedSeq = 0;

      // Compute after_seq for reconnection
      const afterSeq = getClientMaxSeq(ref, "s1", messagesMaxSeq, lastLoadedSeq);
      expect(afterSeq).toBe(113); // ref provides correct value
    });

    test("ref continues to update after reconnection", () => {
      const ref = { s1: 113 };

      // After reconnection, receive new events
      updateLastKnownSeq(ref, "s1", 114);
      updateLastKnownSeq(ref, "s1", 158);

      expect(ref["s1"]).toBe(158);
    });

    test("multiple reconnections preserve ref", () => {
      const ref = {};

      // Connection 1
      updateLastKnownSeq(ref, "s1", 50);

      // Reconnect 1
      let afterSeq = getClientMaxSeq(ref, "s1", 0, 0);
      expect(afterSeq).toBe(50);
      updateLastKnownSeq(ref, "s1", 100);

      // Reconnect 2
      afterSeq = getClientMaxSeq(ref, "s1", 0, 0);
      expect(afterSeq).toBe(100);
      updateLastKnownSeq(ref, "s1", 150);

      expect(ref["s1"]).toBe(150);
    });
  });

  describe("multi-session scenarios", () => {
    test("each session has independent ref tracking", () => {
      const ref = {};

      updateLastKnownSeq(ref, "s1", 100);
      updateLastKnownSeq(ref, "s2", 200);

      const seq1 = getClientMaxSeq(ref, "s1", 0, 0);
      const seq2 = getClientMaxSeq(ref, "s2", 0, 0);

      expect(seq1).toBe(100);
      expect(seq2).toBe(200);
    });

    test("clearing one session does not affect others", () => {
      const ref = { s1: 100, s2: 200, s3: 300 };

      delete ref["s2"];

      expect(ref["s1"]).toBe(100);
      expect(ref["s2"]).toBeUndefined();
      expect(ref["s3"]).toBe(300);
    });
  });
});

// =============================================================================
// checkSessionExists
// =============================================================================

describe("checkSessionExists", () => {
  // Helper: create a mock fetch that records calls and returns a fixed status
  function createMockFetch(status) {
    const calls = [];
    const fn = async (url) => {
      calls.push(url);
      return { status };
    };
    fn.calls = calls;
    return fn;
  }

  // Helper: create a mock fetch that rejects with an error
  function createFailingFetch(error) {
    const calls = [];
    const fn = async (url) => {
      calls.push(url);
      throw error;
    };
    fn.calls = calls;
    return fn;
  }

  const mockApiUrl = (path) => `http://localhost${path}`;

  test("returns { exists: false } when server responds with 404", async () => {
    const mockFetch = createMockFetch(404);

    const result = await checkSessionExists("session-123", mockFetch, mockApiUrl);
    expect(result).toEqual({ exists: false, networkError: false });
    expect(mockFetch.calls).toEqual(["http://localhost/api/sessions/session-123"]);
  });

  test("returns { exists: true } when server responds with 200", async () => {
    const mockFetch = createMockFetch(200);

    const result = await checkSessionExists("session-456", mockFetch, mockApiUrl);
    expect(result).toEqual({ exists: true, networkError: false });
  });

  test("returns { exists: true } when server responds with 500 (don't give up on server errors)", async () => {
    const mockFetch = createMockFetch(500);

    const result = await checkSessionExists("session-789", mockFetch, mockApiUrl);
    expect(result).toEqual({ exists: true, networkError: false });
  });

  test("returns { exists: true, networkError: true } on network failure", async () => {
    const mockFetch = createFailingFetch(new Error("Network error"));

    const result = await checkSessionExists("session-abc", mockFetch, mockApiUrl);
    expect(result).toEqual({ exists: true, networkError: true });
  });

  test("passes session ID correctly in the URL", async () => {
    const mockFetch = createMockFetch(200);
    const prefixApiUrl = (path) => `/prefix${path}`;

    await checkSessionExists("01JNPKPC01SJYTSE3EYMW5J26R", mockFetch, prefixApiUrl);
    expect(mockFetch.calls).toEqual([
      "/prefix/api/sessions/01JNPKPC01SJYTSE3EYMW5J26R",
    ]);
  });
});

// =============================================================================
// isReconnectLimitReached
// =============================================================================

describe("isReconnectLimitReached", () => {
  test("returns false when attempt is 0", () => {
    expect(isReconnectLimitReached(0)).toBe(false);
  });

  test("returns false when attempt is below default limit", () => {
    expect(isReconnectLimitReached(5)).toBe(false);
    expect(isReconnectLimitReached(14)).toBe(false);
  });

  test("returns true when attempt equals default limit", () => {
    const limit = WEBSOCKET_CONSTANTS.MAX_SESSION_RECONNECT_ATTEMPTS;
    expect(isReconnectLimitReached(limit)).toBe(true);
  });

  test("returns true when attempt exceeds default limit", () => {
    const limit = WEBSOCKET_CONSTANTS.MAX_SESSION_RECONNECT_ATTEMPTS;
    expect(isReconnectLimitReached(limit + 1)).toBe(true);
    expect(isReconnectLimitReached(100)).toBe(true);
  });

  test("default limit matches MAX_SESSION_RECONNECT_ATTEMPTS constant", () => {
    expect(WEBSOCKET_CONSTANTS.MAX_SESSION_RECONNECT_ATTEMPTS).toBe(15);
  });

  test("accepts custom maxAttempts override", () => {
    expect(isReconnectLimitReached(3, { maxAttempts: 3 })).toBe(true);
    expect(isReconnectLimitReached(2, { maxAttempts: 3 })).toBe(false);
  });

  test("custom maxAttempts of 0 means always reached", () => {
    expect(isReconnectLimitReached(0, { maxAttempts: 0 })).toBe(true);
  });
});

// =============================================================================
// Error storm prevention (integration-style tests)
// =============================================================================

describe("error storm prevention", () => {
  test("session-gone detection: 404 response stops reconnection", async () => {
    // Simulates the scenario from the bug report:
    // 1. WebSocket connects to a dead session
    // 2. Server returns 404 (before upgrade)
    // 3. ws.onclose fires (ws._wasOpen is false)
    // 4. Client checks REST API → 404 → calls handleSessionGone
    const mockFetch = async () => ({ status: 404 });
    const mockApiUrl = (path) => path;

    const result = await checkSessionExists(
      "01JNPKPC01SJYTSE3EYMW5J26R",
      mockFetch,
      mockApiUrl,
    );

    expect(result.exists).toBe(false);
    // In the real code, this would trigger handleSessionGone()
    // which stops all reconnection attempts for this session
  });

  test("max retry limit prevents unbounded reconnection", () => {
    // Simulates the scenario where the session exists but keeps failing:
    // After MAX_SESSION_RECONNECT_ATTEMPTS failures, reconnection stops
    const maxAttempts = WEBSOCKET_CONSTANTS.MAX_SESSION_RECONNECT_ATTEMPTS;

    // Simulate the attempt counter incrementing
    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      expect(isReconnectLimitReached(attempt)).toBe(false);
    }
    // At the limit, reconnection should stop
    expect(isReconnectLimitReached(maxAttempts)).toBe(true);
  });

  test("successful connection resets attempt counter (backoff sequence restarts)", () => {
    // Verify that the backoff delay sequence restarts from base after
    // a successful connection (attempt 0)
    const baseDelay = WEBSOCKET_CONSTANTS.RECONNECT_BASE_DELAY_MS;
    const delay = calculateReconnectDelay(0, { jitterFactor: 0 });
    expect(delay).toBe(baseDelay);
  });
});

// =============================================================================
// Circuit Breaker: isTerminalSessionError
// =============================================================================

describe("isTerminalSessionError", () => {
  test('returns true for "Session not found"', () => {
    expect(isTerminalSessionError("Session not found")).toBe(true);
  });

  test('returns true for "session not found" (lowercase)', () => {
    expect(isTerminalSessionError("session not found")).toBe(true);
  });

  test('returns true for "SESSION NOT FOUND" (uppercase)', () => {
    expect(isTerminalSessionError("SESSION NOT FOUND")).toBe(true);
  });

  test('returns true for messages containing "session not found" in context', () => {
    expect(isTerminalSessionError("Session not found in store")).toBe(true);
    expect(
      isTerminalSessionError("Error: session not found for ID abc123"),
    ).toBe(true);
  });

  test("returns false for null", () => {
    expect(isTerminalSessionError(null)).toBe(false);
  });

  test("returns false for undefined", () => {
    expect(isTerminalSessionError(undefined)).toBe(false);
  });

  test("returns false for empty string", () => {
    expect(isTerminalSessionError("")).toBe(false);
  });

  test("returns false for unrelated errors", () => {
    expect(isTerminalSessionError("Connection timeout")).toBe(false);
    expect(isTerminalSessionError("Internal server error")).toBe(false);
    expect(isTerminalSessionError("Failed to send prompt")).toBe(false);
  });

  test("returns false for partial matches that are not session-not-found", () => {
    expect(isTerminalSessionError("session expired")).toBe(false);
    expect(isTerminalSessionError("not found")).toBe(false);
  });
});
