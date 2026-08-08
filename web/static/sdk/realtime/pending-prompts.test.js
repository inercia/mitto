/**
 * Unit tests for web/static/sdk/realtime/pending-prompts.js — the injectable
 * pending-prompt queue used by SessionStream.sendPrompt()'s delivery
 * verification path.
 */
import {
  PENDING_PROMPTS_CONSTANTS,
  generatePromptId,
  createMemoryPendingPromptStore,
  createStoragePendingPromptStore,
} from "./pending-prompts.js";

describe("generatePromptId", () => {
  test("returns a string prefixed with 'prompt_' and is unique across calls", () => {
    const a = generatePromptId();
    const b = generatePromptId();
    expect(typeof a).toBe("string");
    expect(a.startsWith("prompt_")).toBe(true);
    expect(a).not.toBe(b);
  });
});

describe("createMemoryPendingPromptStore", () => {
  test("save() then getForSession() returns the saved entry with promptId attached", () => {
    const store = createMemoryPendingPromptStore();
    store.save("s1", "p1", "hello", ["img1"], ["file1"]);
    const entries = store.getForSession("s1");
    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({
      promptId: "p1",
      sessionId: "s1",
      message: "hello",
      imageIds: ["img1"],
      fileIds: ["file1"],
    });
  });

  test("getForSession() only returns entries for the requested session", () => {
    const store = createMemoryPendingPromptStore();
    store.save("s1", "p1", "a");
    store.save("s2", "p2", "b");
    expect(store.getForSession("s1").map((e) => e.promptId)).toEqual(["p1"]);
    expect(store.getForSession("s2").map((e) => e.promptId)).toEqual(["p2"]);
  });

  test("getForSession() returns entries ordered oldest-first", () => {
    let t = 1000;
    const store = createMemoryPendingPromptStore({ now: () => t });
    store.save("s1", "p1", "first");
    t += 10;
    store.save("s1", "p2", "second");
    t += 10;
    store.save("s1", "p3", "third");
    expect(store.getForSession("s1").map((e) => e.promptId)).toEqual(["p1", "p2", "p3"]);
  });

  test("remove() deletes an entry so it no longer appears", () => {
    const store = createMemoryPendingPromptStore();
    store.save("s1", "p1", "a");
    store.remove("p1");
    expect(store.getForSession("s1")).toEqual([]);
  });

  test("remove() of a non-existent promptId is a silent no-op", () => {
    const store = createMemoryPendingPromptStore();
    expect(() => store.remove("nope")).not.toThrow();
  });

  test("entries older than the expiry window are excluded", () => {
    let t = 0;
    const store = createMemoryPendingPromptStore({ now: () => t, expiryMs: 1000 });
    store.save("s1", "p1", "a");
    t = 999;
    expect(store.getForSession("s1")).toHaveLength(1);
    t = 1000;
    expect(store.getForSession("s1")).toHaveLength(0);
  });

  test("default expiry matches PENDING_PROMPTS_CONSTANTS.PROMPT_EXPIRY_MS", () => {
    let t = 0;
    const store = createMemoryPendingPromptStore({ now: () => t });
    store.save("s1", "p1", "a");
    t = PENDING_PROMPTS_CONSTANTS.PROMPT_EXPIRY_MS - 1;
    expect(store.getForSession("s1")).toHaveLength(1);
    t = PENDING_PROMPTS_CONSTANTS.PROMPT_EXPIRY_MS;
    expect(store.getForSession("s1")).toHaveLength(0);
  });
});

describe("createStoragePendingPromptStore", () => {
  function fakeStorage() {
    const map = new Map();
    return {
      getItem: (k) => (map.has(k) ? map.get(k) : null),
      setItem: (k, v) => map.set(k, String(v)),
      removeItem: (k) => map.delete(k),
    };
  }

  test("round-trips through the injected storage adapter under the default key", () => {
    const storage = fakeStorage();
    const store = createStoragePendingPromptStore(storage);
    store.save("s1", "p1", "hello", ["img1"]);
    expect(storage.getItem(PENDING_PROMPTS_CONSTANTS.DEFAULT_STORAGE_KEY)).not.toBe(null);
    const entries = store.getForSession("s1");
    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({ promptId: "p1", message: "hello", imageIds: ["img1"] });
  });

  test("honors a custom storageKey option, isolated from the default key", () => {
    const storage = fakeStorage();
    const store = createStoragePendingPromptStore(storage, { storageKey: "custom_key" });
    store.save("s1", "p1", "a");
    expect(storage.getItem("custom_key")).not.toBe(null);
    expect(storage.getItem(PENDING_PROMPTS_CONSTANTS.DEFAULT_STORAGE_KEY)).toBe(null);
  });

  test("remove() deletes only the targeted entry, preserving others", () => {
    const storage = fakeStorage();
    const store = createStoragePendingPromptStore(storage);
    store.save("s1", "p1", "a");
    store.save("s1", "p2", "b");
    store.remove("p1");
    expect(store.getForSession("s1").map((e) => e.promptId)).toEqual(["p2"]);
  });

  test("getForSession() filters by session and excludes expired entries", () => {
    let t = 0;
    const storage = fakeStorage();
    const store = createStoragePendingPromptStore(storage, { now: () => t, expiryMs: 1000 });
    store.save("s1", "p1", "a");
    store.save("s2", "p2", "b");
    t = 1000;
    store.save("s1", "p3", "c");
    expect(store.getForSession("s1").map((e) => e.promptId)).toEqual(["p3"]);
  });

  test("malformed JSON in storage is tolerated and treated as an empty store", () => {
    const storage = fakeStorage();
    storage.setItem(PENDING_PROMPTS_CONSTANTS.DEFAULT_STORAGE_KEY, "{not json");
    const store = createStoragePendingPromptStore(storage);
    expect(store.getForSession("s1")).toEqual([]);
    expect(() => store.save("s1", "p1", "a")).not.toThrow();
  });
});
