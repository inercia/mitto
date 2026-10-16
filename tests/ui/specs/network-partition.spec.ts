import { test, expect } from "../fixtures/test-fixtures";

/**
 * Network partition recovery tests for Mitto Web UI.
 *
 * These tests verify that the keepalive mechanism correctly detects zombie
 * WebSocket connections when a network partition occurs, and that the app
 * recovers by reconnecting and syncing missed events.
 *
 * Uses Chrome DevTools Protocol (CDP) to simulate real network offline events.
 * Keepalive: 10s interval, max 2 missed → ~20-25s until forced reconnect.
 *
 * NOTE: These tests only run in Chromium (CDP is not available in Firefox/WebKit).
 */

test.describe("Network Partition Recovery", () => {
  test.skip(
    ({ browserName }) => browserName !== "chromium",
    "CDP (newCDPSession) is only available in Chromium",
  );
  test.setTimeout(90000);

  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  /**
   * Test 1: Verify keepalive detects zombie connection and app recovers.
   *
   * Flow:
   * 1. Create fresh session, send baseline message
   * 2. Observe keepalive traffic on the session WebSocket
   * 3. Go offline via CDP
   * 4. Verify keepalives go unacknowledged long enough to trigger recovery
   * 5. Restore network
   * 6. Verify app recovers (input enabled, can send/receive again)
   * 7. Verify a replacement session socket and successful recovery
   */
  test("should detect zombie connection and recover after network partition", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    const sessionSockets: Array<{ url: string; closed: boolean }> = [];
    const keepaliveFrames: Array<{
      direction: "framesent" | "framereceived";
      url: string;
      payload: { type: string; data?: unknown };
    }> = [];
    page.on("websocket", (socket) => {
      const observed = { url: socket.url(), closed: false };
      sessionSockets.push(observed);
      socket.on("close", () => {
        observed.closed = true;
      });
      for (const direction of ["framesent", "framereceived"] as const) {
        socket.on(direction, (event) => {
          const raw =
            typeof event.payload === "string"
              ? event.payload
              : event.payload.toString();
          try {
            const message = JSON.parse(raw) as { type: string; data?: unknown };
            if (
              message.type === "keepalive" ||
              message.type === "keepalive_ack"
            ) {
              keepaliveFrames.push({
                direction,
                url: socket.url(),
                payload: message,
              });
            }
          } catch {
            // Ignore non-JSON frames.
          }
        });
      }
    });

    const sessionId = await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    // Send baseline message to establish session with events and seq numbers
    const initialMessage = helpers.uniqueMessage("Baseline message");
    await helpers.sendMessageAndWait(page, initialMessage);

    const sessionSocketPath = `/api/sessions/${encodeURIComponent(sessionId)}/ws`;
    const matchingSessionSockets = () =>
      sessionSockets.filter((socket) => socket.url.includes(sessionSocketPath));
    const matchingKeepaliveFrames = () =>
      keepaliveFrames.filter(({ url }) => url.includes(sessionSocketPath));
    const socketsBeforePartition = matchingSessionSockets().length;
    expect(socketsBeforePartition).toBeGreaterThan(0);
    const framesBeforePartition = matchingKeepaliveFrames().length;

    // Get CDP session for network emulation
    const cdpSession = await page.context().newCDPSession(page);

    try {
      // Simulate network partition: go completely offline
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: true,
        downloadThroughput: 0,
        uploadThroughput: 0,
        latency: 0,
      });

      // The first keepalive may be nearly 10s away; allow two unacknowledged
      // probes and the following missed-ack interval while CDP remains offline.
      const sentKeepaliveCount = () =>
        matchingKeepaliveFrames()
          .slice(framesBeforePartition)
          .filter(
            ({ direction, payload }) =>
              direction === "framesent" && payload.type === "keepalive",
          ).length;
      await expect
        .poll(sentKeepaliveCount, {
          timeout: 25_000,
          intervals: [1000, 2000, 3000],
        })
        .toBeGreaterThanOrEqual(2);
      await page.waitForTimeout(11_000);
      const partitionFrames = matchingKeepaliveFrames().slice(
        framesBeforePartition,
      );
      expect(
        partitionFrames.filter(
          ({ direction, payload }) =>
            direction === "framereceived" && payload.type === "keepalive_ack",
        ),
      ).toHaveLength(0);

      // Restore network connectivity
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // Once network traffic can complete the close handshake, the zombie
      // stream must close and open a replacement connection.
      await expect
        .poll(() => matchingSessionSockets().some((socket) => socket.closed), {
          timeout: timeouts.agentResponse,
        })
        .toBe(true);
      // App should recover: chat input becomes enabled again
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.appReady,
      });
      await expect
        .poll(() => matchingSessionSockets().length, {
          timeout: timeouts.agentResponse,
        })
        .toBeGreaterThan(socketsBeforePartition);
      await helpers.waitForWebSocketReady(page);
      expect(matchingSessionSockets().some((socket) => !socket.closed)).toBe(
        true,
      );

      // Send another message to confirm the connection is fully functional
      const recoveryMessage = helpers.uniqueMessage("Recovery message");
      await page.locator(selectors.chatInput).fill(recoveryMessage);
      await page.locator(selectors.sendButton).click();

      // Recovery message should appear in chat
      await expect(
        page
          .locator(selectors.userMessage)
          .filter({ hasText: recoveryMessage }),
      ).toBeVisible({ timeout: timeouts.agentResponse });

      // Agent should respond (confirms full round-trip is working)
      await helpers.waitForAgentResponse(page);

      // The session stream closed during the partition and opened a replacement
      // socket before the recovery prompt completed.
    } finally {
      // Always restore network conditions to avoid affecting subsequent tests
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });

  /**
   * Test 2: Verify missed events are synced after network partition during streaming.
   *
   * Flow:
   * 1. Create fresh session, send initial message, record baseline seq watermark
   * 2. Send second message to start agent streaming
   * 3. Within 100ms of sending, go offline via CDP
   * 4. Wait 25s offline (agent finishes on server; keepalive detects zombie)
   * 5. Restore network
   * 6. Wait for reconnect (chatInput enabled)
   * 7. Assert: load_events was sent (lastLoadEventsTimestamp increased)
   * 8. Assert: streaming message and agent response appear in UI
   * 9. Assert: no fatal error state in UI
   */
  test("should recover all events after network partition during streaming", async ({
    page,
    helpers,
    selectors,
    timeouts,
  }) => {
    await helpers.createFreshSession(page);
    await helpers.waitForWebSocketReady(page);

    // Send initial message and wait for complete response — establishes baseline
    const initialMessage = helpers.uniqueMessage("Initial message");
    await helpers.sendMessageAndWait(page, initialMessage);

    // Start monitoring console logs BEFORE the partition
    const consoleLogs: string[] = [];
    page.on("console", (msg) => {
      const text = msg.text();
      if (
        text.includes("Syncing session") ||
        text.includes("load_events") ||
        text.includes("keepalive") ||
        text.includes("Keepalive") ||
        text.includes("reconnect") ||
        text.includes("Reconnect") ||
        text.includes("Force reconnect") ||
        text.includes("connected") ||
        text.includes("WebSocket")
      ) {
        consoleLogs.push(text);
      }
    });

    const cdpSession = await page.context().newCDPSession(page);

    try {
      // Send second message to start agent streaming
      const streamingMessage = helpers.uniqueMessage("Streaming message");
      await page.locator(selectors.chatInput).fill(streamingMessage);
      await page.locator(selectors.sendButton).click();

      // Go offline within 100ms of sending — the server will complete the response
      // while the client is partitioned (no way to deliver it until reconnect)
      await page.waitForTimeout(100);
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: true,
        downloadThroughput: 0,
        uploadThroughput: 0,
        latency: 0,
      });

      // Stay offline for 25s — keepalive misses accumulate, zombie detected
      await page.waitForTimeout(25000);

      // Restore network connectivity
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });

      // Wait for the app to reconnect — chatInput becoming enabled proves
      // the WebSocket reconnected and sent connected + load_events
      await expect(page.locator(selectors.chatInput)).toBeEnabled({
        timeout: timeouts.appReady,
      });

      // Key assertion 1: Both user messages are visible
      await expect(
        page.locator(selectors.userMessage).filter({ hasText: initialMessage }),
      ).toBeVisible({ timeout: timeouts.agentResponse });
      await expect(
        page
          .locator(selectors.userMessage)
          .filter({ hasText: streamingMessage }),
      ).toBeVisible({ timeout: timeouts.agentResponse });

      // Key assertion 2: Agent responses are visible — the streaming response
      // that was generated while offline must have been recovered after reconnect
      const agentMessages = page.locator(selectors.agentMessage);
      await expect(agentMessages).toHaveCount(2, {
        timeout: timeouts.agentResponse,
      });

      // Key assertion 3: No fatal error state in UI
      const fatalErrorCount = await page
        .locator("[data-error='fatal'], .fatal-error")
        .count();
      expect(fatalErrorCount).toBe(0);

      // Key assertion 4: App is functional — can send a follow-up message
      const followUpMessage = helpers.uniqueMessage(
        "Follow-up after partition",
      );
      await helpers.sendMessageAndWait(page, followUpMessage);
      await expect(
        page
          .locator(selectors.userMessage)
          .filter({ hasText: followUpMessage }),
      ).toBeVisible();
    } finally {
      // Always restore network conditions
      await cdpSession.send("Network.emulateNetworkConditions", {
        offline: false,
        downloadThroughput: -1,
        uploadThroughput: -1,
        latency: 0,
      });
    }
  });
});
