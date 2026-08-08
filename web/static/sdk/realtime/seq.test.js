/**
 * Unit tests for web/static/sdk/realtime/seq.js — seq tracking, dedup,
 * stale-client detection, terminal-error classification, and the two
 * seq-watermark store variants (memory + storage-backed).
 */
import {
  SEQ_CONSTANTS,
  createSeqTracker,
  isSeqDuplicate,
  markSeqSeen,
  getMaxSeq,
  isStaleClientState,
  isTerminalSessionError,
  createMemorySeqStore,
  createStorageSeqStore,
} from "./seq.js";

describe("createSeqTracker / isSeqDuplicate / markSeqSeen", () => {
  test("a fresh tracker treats any positive seq as not-duplicate", () => {
    const t = createSeqTracker();
    expect(isSeqDuplicate(t, 5)).toBe(false);
  });

  test("marking a seq seen makes a later duplicate check true", () => {
    const t = createSeqTracker();
    markSeqSeen(t, 5);
    expect(isSeqDuplicate(t, 5)).toBe(true);
  });

  test("seq === lastMessageSeq is exempted (coalescing continuation)", () => {
    const t = createSeqTracker();
    markSeqSeen(t, 5);
    expect(isSeqDuplicate(t, 5, 5)).toBe(false);
  });

  test("seq <= 0 or falsy is never a duplicate", () => {
    const t = createSeqTracker();
    expect(isSeqDuplicate(t, 0)).toBe(false);
    expect(isSeqDuplicate(t, -1)).toBe(false);
    expect(isSeqDuplicate(t, undefined)).toBe(false);
  });

  test("a seq far enough below the highest is rejected as duplicate even if never explicitly seen", () => {
    const t = createSeqTracker();
    markSeqSeen(t, SEQ_CONSTANTS.MAX_RECENT_SEQS + 50);
    expect(isSeqDuplicate(t, 1)).toBe(true);
  });

  test("markSeqSeen updates highestSeq only when the new seq is higher", () => {
    const t = createSeqTracker();
    markSeqSeen(t, 10);
    markSeqSeen(t, 3);
    expect(t.highestSeq).toBe(10);
  });

  test("markSeqSeen prunes seqs older than MAX_RECENT_SEQS below the new highest once the set overflows", () => {
    // Pruning only kicks in once recentSeqs.size exceeds MAX_RECENT_SEQS (a
    // single big jump does not retroactively shrink a small set) — feed it
    // enough distinct, monotonically increasing seqs to overflow and confirm
    // the oldest ones fall off while the newest survive.
    const t = createSeqTracker();
    for (let seq = 1; seq <= SEQ_CONSTANTS.MAX_RECENT_SEQS + 50; seq++) {
      markSeqSeen(t, seq);
    }
    expect(t.recentSeqs.has(1)).toBe(false);
    expect(t.recentSeqs.has(SEQ_CONSTANTS.MAX_RECENT_SEQS + 50)).toBe(true);
    expect(t.recentSeqs.size).toBeLessThanOrEqual(SEQ_CONSTANTS.MAX_RECENT_SEQS + 1);
  });

  test("markSeqSeen(0) / markSeqSeen(undefined) is a no-op", () => {
    const t = createSeqTracker();
    markSeqSeen(t, 0);
    markSeqSeen(t, undefined);
    expect(t.recentSeqs.size).toBe(0);
    expect(t.highestSeq).toBe(0);
  });
});

describe("getMaxSeq", () => {
  test("returns 0 for an empty or missing array", () => {
    expect(getMaxSeq([])).toBe(0);
    expect(getMaxSeq(undefined)).toBe(0);
  });

  test("returns the highest .seq across events, treating missing .seq as 0", () => {
    expect(getMaxSeq([{ seq: 3 }, { seq: 9 }, {}, { seq: 1 }])).toBe(9);
  });
});

describe("isStaleClientState", () => {
  test("true only when both sides are positive and client exceeds server", () => {
    expect(isStaleClientState(10, 5)).toBe(true);
  });

  test("false when client <= server", () => {
    expect(isStaleClientState(5, 10)).toBe(false);
    expect(isStaleClientState(5, 5)).toBe(false);
  });

  test("false when either side is zero/missing", () => {
    expect(isStaleClientState(0, 5)).toBe(false);
    expect(isStaleClientState(5, 0)).toBe(false);
    expect(isStaleClientState(undefined, undefined)).toBe(false);
  });
});

describe("isTerminalSessionError", () => {
  test("matches the known terminal phrases case-insensitively", () => {
    expect(isTerminalSessionError("Session not found")).toBe(true);
    expect(isTerminalSessionError("SESSION IS CLOSED")).toBe(true);
    expect(isTerminalSessionError("session not running")).toBe(true);
  });

  test("false for unrelated or missing messages", () => {
    expect(isTerminalSessionError("some other error")).toBe(false);
    expect(isTerminalSessionError(undefined)).toBe(false);
    expect(isTerminalSessionError("")).toBe(false);
  });
});

describe("createMemorySeqStore", () => {
  test("defaults to 0 for an unseen session and is monotonic per session", () => {
    const store = createMemorySeqStore();
    expect(store.get("s1")).toBe(0);
    store.set("s1", 5);
    store.set("s1", 3); // lower value never regresses
    expect(store.get("s1")).toBe(5);
    store.set("s1", 9);
    expect(store.get("s1")).toBe(9);
  });

  test("sessions are isolated from one another", () => {
    const store = createMemorySeqStore();
    store.set("a", 10);
    expect(store.get("b")).toBe(0);
  });

  test("reset() clears the watermark for that session only", () => {
    const store = createMemorySeqStore();
    store.set("a", 10);
    store.set("b", 20);
    store.reset("a");
    expect(store.get("a")).toBe(0);
    expect(store.get("b")).toBe(20);
  });
});

describe("createStorageSeqStore", () => {
  function fakeStorage() {
    const map = new Map();
    return {
      getItem: (k) => (map.has(k) ? map.get(k) : null),
      setItem: (k, v) => map.set(k, String(v)),
      removeItem: (k) => map.delete(k),
    };
  }

  test("round-trips through the injected storage adapter and is monotonic", () => {
    const storage = fakeStorage();
    const store = createStorageSeqStore(storage);
    expect(store.get("s1")).toBe(0);
    store.set("s1", 5);
    expect(storage.getItem("mitto_seq:s1")).toBe("5");
    store.set("s1", 2); // lower value never regresses
    expect(store.get("s1")).toBe(5);
  });

  test("honors a custom keyPrefix", () => {
    const storage = fakeStorage();
    const store = createStorageSeqStore(storage, { keyPrefix: "custom:" });
    store.set("s1", 7);
    expect(storage.getItem("custom:s1")).toBe("7");
  });

  test("get() tolerates malformed/non-numeric stored values", () => {
    const storage = fakeStorage();
    storage.setItem("mitto_seq:s1", "not-a-number");
    const store = createStorageSeqStore(storage);
    expect(store.get("s1")).toBe(0);
  });

  test("set() ignores non-positive values", () => {
    const storage = fakeStorage();
    const store = createStorageSeqStore(storage);
    store.set("s1", 0);
    store.set("s1", -5);
    expect(store.get("s1")).toBe(0);
  });

  test("reset() removes the stored key", () => {
    const storage = fakeStorage();
    const store = createStorageSeqStore(storage);
    store.set("s1", 5);
    store.reset("s1");
    expect(store.get("s1")).toBe(0);
    expect(storage.getItem("mitto_seq:s1")).toBe(null);
  });
});
