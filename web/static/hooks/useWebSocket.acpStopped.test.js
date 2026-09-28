/**
 * Reproduction for mitto-wko1: the real `handleSessionMessage` "acp_stopped"
 * classification renders every non-archive/non-gc_suspended reason as a red
 * `role: "error"` message with the raw snake_case reason interpolated and an
 * unconditional "Unarchive to continue." recovery instruction — even for
 * expected, resumable lifecycle transitions (e.g. `agent_moved`, emitted by
 * `SessionManager.MoveSessionToAgent` right before the conversation resumes
 * on the target agent) and for archive-family variants beyond the two REST
 * reasons the classifier special-cases (e.g. `archived_via_mcp`).
 *
 * Uses the same source-extraction technique as
 * useWebSocket.configOptions.test.js / useWebSocket.moveAgent.test.js: pull
 * the real `handleSessionMessage` arrow-function source out of the
 * un-importable 4500+ line browser hook and `new Function(...)` it with
 * stubbed bindings, so the test exercises the actual reducer rather than a
 * re-implementation of it.
 */
import { readFileSync } from "node:fs";
import { describe, test, expect } from "../utils/testing/testGlobals.js";

const source = readFileSync(
  new URL("./useWebSocket.js", import.meta.url),
  "utf8",
);

function extractHandleSessionMessage(bindings) {
  const marker = "const handleSessionMessage = useCallback(";
  const start = source.indexOf(marker);
  expect(start).toBeGreaterThan(-1);
  const end = source.indexOf("}, []);", start);
  expect(end).toBeGreaterThan(start);
  return new Function(
    ...Object.keys(bindings),
    `return ${source.slice(start + marker.length, end + 1)};`,
  )(...Object.values(bindings));
}

function activeSession() {
  return {
    messages: [],
    isRunning: true,
    isStreaming: true,
    activeUIPrompt: { id: "p1" },
    info: { name: "Conversation" },
  };
}

function harness() {
  let sessions = { s1: activeSession() };
  let storedSessions = [{ session_id: "s1", isStreaming: true }];
  const handleSessionMessage = extractHandleSessionMessage({
    setSessions: (update) => {
      sessions = update(sessions);
    },
    setStoredSessions: (update) => {
      storedSessions = update(storedSessions);
    },
    clearUIPromptDraft: () => {},
    queueStore: { setMessages: () => {}, setLength: () => {} },
    sessionUpdateSchedulerRef: { current: { flushSession: () => false } },
    COALESCED_BACKGROUND_MESSAGE_TYPES: new Set(),
    serverShuttingDownRef: { current: false },
    sessionWsRefs: { current: {} },
    sessionsRef: { current: sessions },
    lastKnownSeqRef: { current: {} },
    getMaxSeq: () => 0,
    setPendingSync: () => {},
    setTimeout: () => 0, // don't actually schedule the delayed sync in tests
    console: { log() {} },
  });
  return {
    get sessions() {
      return sessions;
    },
    get storedSessions() {
      return storedSessions;
    },
    dispatch: handleSessionMessage,
  };
}

const stopped = (reason) => ({
  type: "acp_stopped",
  data: { reason },
});

describe("acp_stopped stop-reason classification (mitto-wko1)", () => {
  test("agent_moved is an expected resumable handoff, not a red error telling the user to unarchive", () => {
    const h = harness();
    h.dispatch("s1", stopped("agent_moved"));
    const messages = h.sessions.s1.messages;
    expect(messages.length).toBeGreaterThan(0);
    const last = messages[messages.length - 1];
    // Desired: agent_moved must not be surfaced as a red error, and must not
    // recommend unarchiving (the session is not archived - it resumes on the
    // target agent immediately).
    expect(last.role).not.toBe("error");
    expect(last.text).not.toMatch(/unarchive/i);
    // Desired: the raw wire identifier should never leak into user-facing text.
    expect(last.text).not.toContain("agent_moved");
  });

  test("archived_via_mcp gets the same neutral archive treatment as the REST 'archived' reason", () => {
    const h = harness();
    h.dispatch("s1", stopped("archived_via_mcp"));
    const messages = h.sessions.s1.messages;
    const last = messages[messages.length - 1];
    expect(last.role).toBe("system");
    expect(last.text).toBe("Session archived. Unarchive to continue.");
  });

  test("unknown stop reasons fall back to a safe message without exposing the wire identifier", () => {
    const h = harness();
    h.dispatch("s1", stopped("transport_broken"));
    const messages = h.sessions.s1.messages;
    const last = messages[messages.length - 1];
    expect(last.text).not.toContain("transport_broken");
  });
});
