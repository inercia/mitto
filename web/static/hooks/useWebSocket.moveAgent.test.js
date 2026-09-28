/**
 * Execute the real handleGlobalEvent callback with only its state seams
 * stubbed, to test the new "session_agent_moved" case (mitto-f7yo.6) without
 * importing the full 4582-line browser hook. Mirrors the extraction
 * technique in useWebSocket.configOptions.test.js (mitto-6w3): grab the
 * arrow-function source between `const handleGlobalEvent = useCallback(`
 * and its first `}, []);` boundary, then `new Function(...)` it with stubbed
 * bindings. `new Function` only resolves free identifiers lazily, at
 * execution time, so unrelated branches/case blocks referencing bindings we
 * don't supply here (e.g. handleSessionMessageRef, used by other cases) are
 * never touched as long as our test only dispatches "session_agent_moved".
 */
import { readFileSync } from "node:fs";
import { describe, test, expect } from "../utils/testing/testGlobals.js";

const source = readFileSync(
  new URL("./useWebSocket.js", import.meta.url),
  "utf8",
);

function extractHandleGlobalEvent(bindings) {
  const marker = "const handleGlobalEvent = useCallback(";
  const start = source.indexOf(marker);
  expect(start).toBeGreaterThan(-1);
  const end = source.indexOf("}, []);", start);
  expect(end).toBeGreaterThan(start);
  return new Function(
    ...Object.keys(bindings),
    `return ${source.slice(start + marker.length, end + 1)};`,
  )(...Object.values(bindings));
}

function storedSession(id, acpServer) {
  return { session_id: id, name: `Conversation ${id}`, acp_server: acpServer };
}

function activeSession(acpServer) {
  return {
    messages: [],
    isStreaming: false,
    info: { name: "Conversation", acp_server: acpServer },
  };
}

function harness() {
  let storedSessions = [
    storedSession("s1", "agent-a"),
    storedSession("s2", "agent-a"),
  ];
  let sessions = { s1: activeSession("agent-a") };
  const handleGlobalEvent = extractHandleGlobalEvent({
    setStoredSessions: (update) => {
      storedSessions = update(storedSessions);
    },
    setSessions: (update) => {
      sessions = update(sessions);
    },
  });
  return {
    get storedSessions() {
      return storedSessions;
    },
    get sessions() {
      return sessions;
    },
    dispatch: handleGlobalEvent,
  };
}

const moved = (sessionId, acpServer) => ({
  type: "session_agent_moved",
  data: {
    session_id: sessionId,
    acp_server: acpServer,
    previous_agent: "agent-a",
  },
});

describe("session_agent_moved broadcast (mitto-f7yo.6)", () => {
  test("updates acp_server on the matching stored session, leaves others untouched", () => {
    const h = harness();
    h.dispatch(moved("s1", "agent-b"));
    expect(h.storedSessions.find((s) => s.session_id === "s1").acp_server).toBe(
      "agent-b",
    );
    expect(h.storedSessions.find((s) => s.session_id === "s2").acp_server).toBe(
      "agent-a",
    );
  });

  test("updates info.acp_server on the matching active session", () => {
    const h = harness();
    h.dispatch(moved("s1", "agent-b"));
    expect(h.sessions.s1.info.acp_server).toBe("agent-b");
  });

  test("leaves other active-session fields (messages, isStreaming, name) untouched", () => {
    const h = harness();
    const before = h.sessions.s1;
    h.dispatch(moved("s1", "agent-b"));
    expect(h.sessions.s1.messages).toBe(before.messages);
    expect(h.sessions.s1.isStreaming).toBe(before.isStreaming);
    expect(h.sessions.s1.info.name).toBe("Conversation");
  });

  test("no-ops when the session isn't in the active sessions map", () => {
    const h = harness();
    const before = h.sessions;
    h.dispatch(moved("unknown-session", "agent-b"));
    expect(h.sessions).toBe(before);
    // Stored sessions still updates unconditionally (mirrors session_renamed's
    // unconditional storedSessions.map — a session can be in storedSessions
    // without being in the active `sessions` map, e.g. not currently open).
    expect(
      h.storedSessions.find((s) => s.session_id === "unknown-session"),
    ).toBeUndefined();
  });
});
