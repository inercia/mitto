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
    expect(useWSConnectionJs).toMatch(
      /import \{ getSdkClient, getSdkWsBaseUrl \} from "\.\.\/utils\/sdkClient\.js";/,
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

  test("\"open\" handler branches on isReconnect exactly like the old onopen handler", () => {
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("open", \(\{ isReconnect \}\) => \{\s*\n\s*setEventsConnected\(true\);/,
    );
    expect(useWSConnectionJs).toMatch(/if \(isReconnect\) \{\s*\n[\s\S]*?fetchStoredSessions\(\);/);
    expect(useWSConnectionJs).toMatch(
      /\} else \{[\s\S]*?fetchStoredSessions\(\)\.then\(\(storedSessionsList\) => \{/,
    );
  });

  test("both \"connected\" and \"event\" emissions are re-wrapped into {type, data} and routed through the single handleGlobalEvent entrypoint", () => {
    expect(useWSConnectionJs).toMatch(
      /stream\.on\("connected", \(data\) => handleGlobalEvent\(\{ type: "connected", data \}\)\);/,
    );
    expect(useWSConnectionJs).toMatch(/stream\.on\("event", \(msg\) => handleGlobalEvent\(msg\)\);/);
  });

  test("\"close\" clears eventsConnected", () => {
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
    expect(useWSConnectionJs).toMatch(/keepaliveRef,\s*\n\s*serverShuttingDownRef,\s*\n\s*eventsStreamRef,\s*\n\s*staggeredBackgroundTimersRef,\s*\n\s*\};\s*\n\}/);
  });

  test("no leftover references to the deleted raw-WebSocket refs in either hook", () => {
    const deleted = ["eventsWsRef", "reconnectRef", "eventsReconnectAttemptRef", "wasConnectedRef"];
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
      /keepaliveRef,\s*\n\s*serverShuttingDownRef,\s*\n\s*eventsStreamRef,\s*\n\s*staggeredBackgroundTimersRef,\s*\n\s*\} = connection;/,
    );
  });

  test("the unmount cleanup effect closes the stream explicitly instead of clearing a reconnect timer + closing a raw socket", () => {
    expect(useWebSocketJs).toMatch(
      /connectToEventsRef\.current\?\.\(\);\s*\n\s*return \(\) => \{\s*\n\s*if \(eventsStreamRef\.current\) eventsStreamRef\.current\.close\(\);/,
    );
  });
});
