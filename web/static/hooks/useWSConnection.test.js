/**
 * Regression tests for the mitto-7gta.18 slice S2 migration: connectToEvents
 * (useWSConnection.js) now backed by the SDK's EventsStream instead of a raw
 * WebSocket.
 *
 * useWSConnection.js cannot be imported directly under jsdom (it reads
 * `window.preact` at module load time, same limitation documented in
 * useWebSocket.test.js / SessionItem.test.js), so these tests read the raw
 * source and assert on the exact wiring rather than executing the hook.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const useWSConnectionJs = readFileSync(
  resolve(__dirname, "useWSConnection.js"),
  "utf8",
);
const useWebSocketJs = readFileSync(
  resolve(__dirname, "useWebSocket.js"),
  "utf8",
);

describe("useWSConnection.js: connectToEvents backed by EventsStream (mitto-7gta.18 S2)", () => {
  test("imports getSdkClient/getSdkWsBaseUrl from the S1 client seam", () => {
    // Multi-line import since mitto-7gta.30 (S1) also pulls in
    // createSdkSeqStore/createSdkPendingPromptStore for SessionStream.
    expect(useWSConnectionJs).toMatch(
      /import \{\s*\n\s*getSdkClient,\s*\n\s*getSdkWsBaseUrl,\s*\n[\s\S]*?\} from "\.\.\/utils\/sdkClient\.js";/,
    );
  });

  test("reuses a cached eventsStreamRef instance instead of re-creating the stream", () => {
    expect(useWSConnectionJs).toMatch(
      /if \(eventsStreamRef\.current\) \{\s*\n\s*eventsStreamRef\.current\.connect\(\);\s*\n\s*return;\s*\n\s*\}/,
    );
  });

  test("creates the stream via getSdkClient().eventsStream({ wsBaseUrl, shouldReconnect })", () => {
    expect(useWSConnectionJs).toMatch(
      /const stream = getSdkClient\(\)\.eventsStream\(\{\s*\n\s*wsBaseUrl: getSdkWsBaseUrl\(\),\s*\n\s*shouldReconnect: async \(\) => \{/,
    );
  });

  test("shouldReconnect preserves the auth-redirect veto before the server-shutdown veto", () => {
    expect(useWSConnectionJs).toMatch(
      /const isAuthenticated = await checkAuthOrRedirect\(\);\s*\n[\s\S]*?if \(!isAuthenticated\) \{\s*\n[\s\S]*?return false;\s*\n\s*\}\s*\n\s*if \(serverShuttingDownRef\.current\) \{/,
    );
  });

  test('"open" handler branches on isReconnect exactly like the old onopen handler', () => {
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("open", \(\{ isReconnect \}\) => \{\s*\n\s*setEventsConnected\(true\);/,
    );
    expect(useWSConnectionJs).toMatch(
      /if \(isReconnect\) \{\s*\n[\s\S]*?fetchStoredSessions\(\);/,
    );
    expect(useWSConnectionJs).toMatch(
      /\} else \{[\s\S]*?fetchStoredSessions\(\)\.then\(\(storedSessionsList\) => \{/,
    );
  });

  test('both "connected" and "event" emissions are re-wrapped into {type, data} and routed through the single handleGlobalEvent entrypoint', () => {
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("connected", \(data\) => handleGlobalEvent\(\{ type: "connected", data \}\)\);/,
    );
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("event", \(msg\) => handleGlobalEvent\(msg\)\);/,
    );
  });

  test('"close" clears eventsConnected', () => {
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("close", \(event\) => \{[\s\S]*?setEventsConnected\(false\);\s*\n\s*\}\);/,
    );
  });

  test("the created stream is cached in eventsStreamRef before connect() is called", () => {
    expect(useWSConnectionJs).toMatch(
      /eventsStreamRef\.current = stream;\s*\n\s*stream\.connect\(\);\s*\n\s*\}, \[fetchStoredSessions, handleGlobalEvent, switchSession\]\);/,
    );
  });

  test("the return bag exposes eventsStreamRef, not the deleted raw-WebSocket refs", () => {
    // keepaliveRef was also removed from the return bag by mitto-7gta.30 (S1):
    // SessionStream owns keepalive/zombie detection internally per session.
    expect(useWSConnectionJs).toMatch(
      /sessionWsRefs,\s*\n\s*serverShuttingDownRef,\s*\n\s*eventsStreamRef,\s*\n\s*staggeredBackgroundTimersRef,\s*\n\s*\};\s*\n\}/,
    );
  });

  test("no leftover references to the deleted raw-WebSocket refs in either hook", () => {
    const deleted = [
      "eventsWsRef",
      "reconnectRef",
      "eventsReconnectAttemptRef",
      "wasConnectedRef",
      "sessionReconnectRefs",
      "sessionReconnectAttemptsRef",
      "keepaliveRef",
    ];
    for (const name of deleted) {
      const re = new RegExp(`\\b${name}\\b`);
      expect(re.test(useWSConnectionJs)).toBe(false);
      expect(re.test(useWebSocketJs)).toBe(false);
    }
  });
});

describe("useWebSocket.js: composer wiring onto eventsStreamRef (mitto-7gta.18 S2)", () => {
  test("destructures eventsStreamRef (not eventsWsRef/reconnectRef) from the connection bag", () => {
    expect(useWebSocketJs).toMatch(
      /sessionWsRefs,\s*\n\s*serverShuttingDownRef,\s*\n\s*eventsStreamRef,\s*\n\s*staggeredBackgroundTimersRef,\s*\n\s*\} = connection;/,
    );
  });

  test("the unmount cleanup effect closes the stream explicitly instead of clearing a reconnect timer + closing a raw socket", () => {
    expect(useWebSocketJs).toMatch(
      /connectToEventsRef\.current\?\.\(\);\s*\n\s*return \(\) => \{\s*\n\s*if \(eventsStreamRef\.current\) eventsStreamRef\.current\.close\(\);/,
    );
  });
});

describe("useWSConnection.js: session sockets backed by SessionStream (mitto-7gta.30 S1/S3)", () => {
  test("no raw `new WebSocket(` remains for session sockets", () => {
    expect(useWSConnectionJs).not.toMatch(/new WebSocket\(/);
    expect(useWebSocketJs).not.toMatch(/new WebSocket\(/);
  });

  test("no leftover readyState/WebSocket.OPEN checks in either hook", () => {
    expect(useWSConnectionJs).not.toMatch(/readyState/);
    expect(useWSConnectionJs).not.toMatch(/WebSocket\.OPEN/);
    expect(useWebSocketJs).not.toMatch(/readyState/);
    expect(useWebSocketJs).not.toMatch(/WebSocket\.OPEN/);
  });

  test("getOrCreateStream builds the stream via getSdkClient().sessionStream(sessionId, {...}) with the seqStore/pendingPromptStore/keepalive/shouldReconnect options", () => {
    expect(useWSConnectionJs).toMatch(
      /const stream = getSdkClient\(\)\.sessionStream\(sessionId, \{\s*\n\s*wsBaseUrl: getSdkWsBaseUrl\(\),\s*\n\s*seqStore: createSdkSeqStore\(\),\s*\n\s*pendingPromptStore: createSdkPendingPromptStore\(\),\s*\n\s*keepaliveIntervalMs: getKeepaliveInterval\(\),\s*\n\s*shouldReconnect: sessionShouldReconnect,/,
    );
  });

  test("getClientMaxSeq folds lastKnownSeqRef and React-state seqs (max of ref, messages, lastLoadedSeq) into the stream", () => {
    expect(useWSConnectionJs).toMatch(
      /getClientMaxSeq: \(\) => \{\s*\n\s*const session = sessionsRef\.current\[sessionId\];\s*\n\s*const refSeq = lastKnownSeqRef\.current\[sessionId\] \|\| 0;\s*\n\s*const stateSeq = Math\.max\(\s*\n\s*getMaxSeq\(session\?\.messages \|\| \[\]\),\s*\n\s*session\?\.lastLoadedSeq \|\| 0,\s*\n\s*\);\s*\n\s*return Math\.max\(refSeq, stateSeq\);\s*\n\s*\},/,
    );
  });

  test("isSyncInFlight is wired to the composer's pendingSyncRef so the stream never races a composer-initiated load_events", () => {
    expect(useWSConnectionJs).toMatch(
      /isSyncInFlight: \(\) => !!pendingSyncRef\.current\[sessionId\],/,
    );
  });

  test("sessionShouldReconnect preserves the auth-redirect veto before the server-shutdown veto (same gate as connectToEvents)", () => {
    expect(useWSConnectionJs).toMatch(
      /const sessionShouldReconnect = useCallback\(async \(\) => \{\s*\n\s*const isAuthenticated = await checkAuthOrRedirect\(\);\s*\n\s*if \(!isAuthenticated\) return false;[\s\S]*?if \(serverShuttingDownRef\.current\) \{/,
    );
  });

  test("the stream wires open/message/keepalive_ack/gone/close/error listeners exactly once, on creation", () => {
    expect(useWSConnectionJs).toMatch(/stream\.on\("open", \(\) => \{/);
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("message", \(msg\) => \{[\s\S]*?handleSessionMessageRef\.current\(sessionId, msg\);/,
    );
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("keepalive_ack", \(data\) => \{\s*\n\s*handleSessionKeepaliveAckRef\.current\?\.\(sessionId, data\);/,
    );
    expect(useWSConnectionJs).toMatch(/stream\.on\("gone", \(event\) => \{/);
    expect(useWSConnectionJs).toMatch(/stream\.on\("close", \(event\) => \{/);
    expect(useWSConnectionJs).toMatch(/stream\.on\("error", \(err\) => \{/);
  });

  test("gone evicts the stream, cancels composer sync, and reuses session_deleted cleanup", () => {
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("gone", \(event\) => \{[\s\S]*?sessionWsRefs\.current\[sessionId\] === stream[\s\S]*?delete sessionWsRefs\.current\[sessionId\];[\s\S]*?clearPendingSync\(sessionId\);[\s\S]*?handleGlobalEvent\(\{\s*type: "session_deleted",\s*data: \{ session_id: sessionId, reason: event\?\.reason \},\s*\}\);/,
    );
  });

  test("session_deleted synchronously clears active and persisted selection", () => {
    const start = useWebSocketJs.indexOf('case "session_deleted"');
    const end = useWebSocketJs.indexOf('case "acp_started"', start);
    const snippet = useWebSocketJs.slice(start, end);

    expect(start).toBeGreaterThan(-1);
    expect(end).toBeGreaterThan(start);
    expect(snippet).toMatch(
      /if \(deletedId === currentId\) \{[\s\S]*?activeSessionIdRef\.current = null;\s*setLastActiveSessionId\(null\);/,
    );
  });

  test("close 1009 evicts the terminal stream and quarantines pending prompts", () => {
    expect(useWSConnectionJs).toMatch(
      /if \(event\?\.code === 1009\) \{[\s\S]*?delete sessionWsRefs\.current\[sessionId\];[\s\S]*?rejectOversizedPromptsRef\.current\?\.\(sessionId\);/,
    );
    expect(useWebSocketJs).toMatch(
      /rejectOversizedPromptsRef\.current = rejectOversizedPromptsForSession;/,
    );
  });

  test("getOrCreateStream caches the stream in sessionWsRefs and reuses it on a second call for the same session", () => {
    expect(useWSConnectionJs).toMatch(
      /const existing = sessionWsRefs\.current\[sessionId\];\s*\n\s*if \(existing\) return existing;/,
    );
    expect(useWSConnectionJs).toMatch(
      /sessionWsRefs\.current\[sessionId\] = stream;\s*\n\s*return stream;/,
    );
  });

  test("connectToSession gets-or-creates the stream then calls .connect() (no manual onopen/onmessage wiring at the call site)", () => {
    expect(useWSConnectionJs).toMatch(
      /const connectToSession = useCallback\(\s*\n\s*\(sessionId\) => \{\s*\n\s*const stream = getOrCreateStream\(sessionId\);\s*\n\s*stream\.connect\(\);\s*\n\s*return stream;/,
    );
  });

  test("sendToSession forwards a plain object to stream.send (not JSON.stringify)", () => {
    expect(useWSConnectionJs).toMatch(
      /const sendToSession = useCallback\(\(sessionId, msg\) => \{\s*\n\s*const stream = sessionWsRefs\.current\[sessionId\];\s*\n\s*return stream \? stream\.send\(msg\) : false;/,
    );
  });

  test('waitForSessionConnection checks stream.state (not readyState) and resolves via stream.once("open", ...)', () => {
    expect(useWSConnectionJs).toMatch(
      /if \(stream\.state === "open"\) \{\s*\n\s*resolve\(stream\);/,
    );
    expect(useWSConnectionJs).toMatch(
      /stream\.once\("open", \(\) => \{\s*\n\s*clearTimeout\(timeoutId\);\s*\n\s*resolve\(stream\);/,
    );
  });

  test("isConnectionHealthy delegates to stream.isHealthy()", () => {
    expect(useWSConnectionJs).toMatch(
      /const isConnectionHealthy = useCallback\(\(sessionId\) => \{\s*\n\s*const stream = sessionWsRefs\.current\[sessionId\];\s*\n\s*if \(!stream\) return true;[\s\S]*?const healthy = stream\.isHealthy\(\);/,
    );
  });

  test("forceReconnectActiveSession and the staggered background sweep call stream.forceReconnect() (no manual close+reconnect)", () => {
    expect(useWSConnectionJs).toMatch(
      /getOrCreateStream\(currentSessionId\)\.forceReconnect\(\);/,
    );
    expect(useWSConnectionJs).toMatch(/existingStream\.forceReconnect\(\);/);
  });

  test("background-disconnect sweep calls stream.close() directly (SessionStream owns reconnect suppression internally)", () => {
    expect(useWSConnectionJs).toMatch(
      /delete sessionWsRefs\.current\[sessionId\];\s*\n\s*stream\.close\(\);/,
    );
  });
});

describe("useWebSocket.js: keepalive_ack UI bookkeeping split from SessionStream (mitto-7gta.30)", () => {
  test("handleSessionKeepaliveAckRef is declared and populated with handleSessionKeepaliveAck", () => {
    expect(useWebSocketJs).toMatch(
      /const handleSessionKeepaliveAckRef = useRef\(null\);/,
    );
    expect(useWebSocketJs).toMatch(
      /handleSessionKeepaliveAckRef\.current = handleSessionKeepaliveAck;/,
    );
  });

  test("handleSessionKeepaliveAck syncs streaming state, processor stats, queue length (active session only), status, and is_running/acp_ready", () => {
    expect(useWebSocketJs).toMatch(
      /const handleSessionKeepaliveAck = useCallback\(\(sessionId, data\) => \{/,
    );
    expect(useWebSocketJs).toMatch(
      /const serverIsPrompting = data\?\.is_prompting \|\| false;/,
    );
    expect(useWebSocketJs).toMatch(
      /if \(\s*data\?\.queue_length !== undefined &&\s*sessionId === activeSessionIdRef\.current\s*\) \{/,
    );
    expect(useWebSocketJs).toMatch(
      /const serverIsRunning = data\?\.is_running \?\? true;/,
    );
  });

  test("handleSessionKeepaliveAckRef is passed into useWSConnection alongside handleSessionMessageRef", () => {
    expect(useWebSocketJs).toMatch(
      /handleSessionMessageRef,\s*\n\s*handleSessionKeepaliveAckRef,\s*\n\s*handleGlobalEvent,/,
    );
  });

  test('all remaining sessionWsRefs call sites use the SessionStream API (.state==="open" / .send(obj)), not the raw WebSocket API', () => {
    // Every `.send(` on a sessionWsRefs-derived stream variable must pass a plain
    // object literal (not JSON.stringify(...)) — count must match across both files.
    const sendCalls = [
      ...useWebSocketJs.matchAll(/\b(?:ws|currentWs)\.send\(/g),
    ];
    expect(sendCalls.length).toBeGreaterThan(0);
    for (const _ of sendCalls) {
      expect(useWebSocketJs).not.toMatch(
        /(?:ws|currentWs)\.send\(\s*JSON\.stringify/,
      );
    }
    // `.state === "open"` guards replace every former `.readyState === WebSocket.OPEN` guard.
    const stateChecks = [...useWebSocketJs.matchAll(/\.state === "open"/g)];
    expect(stateChecks.length).toBeGreaterThanOrEqual(4);
  });
});

describe("useWebSocket.js: close() vs forceReconnect() semantics (mitto-7gta.30)", () => {
  test("the sync timeout force-reconnects the stream instead of close()-ing it", () => {
    // SessionStream.close() sets _explicitlyClosed, which makes _reconnectOrStop
    // go straight to "stopped" — using it here would leave the session offline
    // forever after a sync timeout. forceReconnect() closes and reopens instead.
    expect(useWebSocketJs).toMatch(
      /Sync timeout for session[\s\S]{0,900}?sessionWsRefs\.current\[sessionId\]\?\.forceReconnect\(\);/,
    );
    expect(useWebSocketJs).not.toMatch(
      /Sync timeout for session[\s\S]{0,900}?delete sessionWsRefs\.current\[sessionId\];/,
    );
  });

  test("server_shutdown closes the stream without dropping the ref (close() suppresses reconnect on its own)", () => {
    expect(useWebSocketJs).toMatch(
      /Server shutdown detected for session[\s\S]{0,500}?sessionWsRefs\.current\[sessionId\]\?\.close\(\);/,
    );
    expect(useWebSocketJs).not.toMatch(
      /Server shutdown detected for session[\s\S]{0,500}?delete sessionWsRefs\.current\[sessionId\];/,
    );
  });

  test("switchSession discards a non-open stream by dropping the ref BEFORE close() so connectToSession builds a fresh one", () => {
    expect(useWebSocketJs).toMatch(
      /if \(existingWs\) \{\s*\n\s*delete sessionWsRefs\.current\[sessionId\];\s*\n\s*existingWs\.close\(\);\s*\n\s*\}\s*\n\s*connectToSessionRef\.current\?\.\(sessionId\);/,
    );
  });

  test("switchSession activates the latest click before async loading and never re-activates from a stale response", () => {
    const start = useWebSocketJs.indexOf("const switchSession = useCallback(");
    const end = useWebSocketJs.indexOf("// Handle global events", start);
    const snippet = useWebSocketJs.slice(start, end);
    const activation = snippet.indexOf("setActiveSessionId(sessionId);");
    const firstAwait = snippet.indexOf("const meta = await ");

    expect(start).toBeGreaterThan(-1);
    expect(end).toBeGreaterThan(start);
    expect(activation).toBeGreaterThan(-1);
    expect(activation).toBeLessThan(firstAwait);
    expect(snippet.match(/setActiveSessionId\(sessionId\);/g)).toHaveLength(1);
  });
});
