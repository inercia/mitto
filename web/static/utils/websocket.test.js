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
