// =============================================================================
// Mitto Web Interface — WebSocket Connection sub-hook (C1)
// Extracted from useWebSocket.js (mitto-90f.6.2).
//
// Owns the transport primitives for both per-session WebSockets and the global
// events WebSocket: lifecycle (open / onmessage / onclose / onerror),
// exponential-backoff reconnect, keepalive heartbeat + zombie detection,
// staggered background-session reconnect on wake, background-session
// disconnect sweep (lazy-connect hygiene), and the `sendToSession` /
// `waitForSessionConnection` / `isConnectionHealthy` / `forceReconnectActiveSession`
// helpers used by the composer's send path and by upcoming C2
// (useWSDeliveryVerification, mitto-90f.6.3).
//
// The message handler itself (`handleSessionMessage`) stays in the composer
// and is threaded in via a ref (`handleSessionMessageRef`) so its identity is
// stable across reconnects (rule 21-web-frontend-state).
//
// useWSSeqSync outputs used here (only `lastKnownSeqRef`) are received as
// props — the composer keeps the single `useWSSeqSync()` call site
// (rule 24-web-frontend-sync).
//
// Ref-bridges owned by the composer and read by this sub-hook:
//   * retryPendingPromptsRef — populated by C2 (useWSDeliveryVerification)
//     in mitto-90f.6.3; read inside connectToSession's onopen handler.
//   * resolvePendingSendsRef — populated by C2 in mitto-90f.6.3; forwarded to
//     the composer for its handleSessionMessage; not read directly here.
//
// staleRecoveryCooldownRef is owned by useWSMobileResilience (mitto-90f.6.1)
// and passed in as a prop; the session onclose handler clears its entry when
// the connection drops.
//
// connectToEvents (S2, mitto-7gta.18) is backed by the SDK's EventsStream
// (sdk/realtime/events-stream.js) instead of a raw WebSocket: the stream owns
// its own reconnect/backoff loop internally, so this hook only needs to
// create it once (lazily, cached in eventsStreamRef) and call .connect().
// The auth-redirect / server-shutdown reconnect veto is passed in as the
// stream's `shouldReconnect` option — see connectToEvents below.
// =============================================================================

const { useCallback, useEffect, useRef, useState } = window.preact;

import { ROLE_ERROR, INITIAL_EVENTS_LIMIT, limitMessages, getMaxSeq } from "../lib.js";
import { setLastActiveSessionId, getLastActiveSessionId, getLastSeenSeq } from "../utils/storage.js";
import { endpoints } from "../utils/index.js";
import { getSdkClient, getSdkWsBaseUrl } from "../utils/sdkClient.js";
import {
  calculateReconnectDelay,
  createReconnectDebounceTracker,
  shouldDebounceReconnect,
  isReconnectLimitReached,
  checkAuthOrRedirect,
  KEEPALIVE_MAX_MISSED_DEFAULT,
  KEEPALIVE_MAX_MISSED_LARGE_SESSION,
  LARGE_SESSION_SEQ_THRESHOLD,
  STARTUP_STAGGER_MS,
  STAGGERED_RECONNECT_DEBOUNCE_MS,
  BACKGROUND_DISCONNECT_GRACE_MS,
  getKeepaliveInterval,
} from "../utils/websocket.js";

export function useWSConnection({
  // Composer state / refs
  activeSessionId,
  activeSessionIdRef,
  sessionsRef,
  storedSessionsRef,
  setSessions,
  // Message handler (via ref for identity stability — rule 21)
  handleSessionMessageRef,
  handleGlobalEvent,
  // Composer callbacks used by connectToEvents onopen restore path
  fetchStoredSessions,
  switchSession,
  onNoInitialSessionRef,
  // Ref-bridges owned by composer, populated by C2 (mitto-90f.6.3)
  retryPendingPromptsRef,
  // useWSSeqSync outputs (rule 24 — passed as props, NOT re-invoked)
  lastKnownSeqRef,
  // From useWSMobileResilience (mitto-90f.6.1)
  staleRecoveryCooldownRef,
  // Composer sync-tracking primitives (used by keepalive / onopen / onclose)
  pendingSyncRef,
  needsContextLoadRef,
  setPendingSync,
  clearPendingSync,
}) {
  // ---- Owned state ----
  const [eventsConnected, setEventsConnected] = useState(false);

  // Lazily-created SDK EventsStream instance backing connectToEvents (S2,
  // mitto-7gta.18) — the stream owns its own reconnect/backoff loop, so this
  // hook only creates it once and calls .connect()/.close() on it.
  const eventsStreamRef = useRef(null);
  const sessionWsRefs = useRef({});                   // { sessionId: WebSocket }
  const sessionReconnectRefs = useRef({});            // { sessionId: timeoutId }
  const sessionReconnectAttemptsRef = useRef({});
  const reconnectDebounceRef = useRef(createReconnectDebounceTracker());
  const serverShuttingDownRef = useRef(false);
  const staggeredBackgroundTimersRef = useRef({});
  const lastStaggeredReconnectRef = useRef(0);
  const backgroundDisconnectTimerRef = useRef(null);
  const keepaliveRef = useRef({});

  // Connect to per-session WebSocket
  const connectToSession = useCallback(
    (sessionId) => {
      // Clear any pending reconnect timer for this session
      if (sessionReconnectRefs.current[sessionId]) {
        clearTimeout(sessionReconnectRefs.current[sessionId]);
        delete sessionReconnectRefs.current[sessionId];
      }

      // Don't connect if already connected
      if (sessionWsRefs.current[sessionId]) {
        return sessionWsRefs.current[sessionId];
      }

      const ws = new WebSocket(endpoints.sessions.ws(sessionId));
      const wsId = Math.random().toString(36).substring(2, 8); // Debug ID for this connection
      ws._debugId = wsId;

      ws.onopen = () => {
        console.log(`Session WebSocket connected: ${sessionId} (ws: ${wsId})`);

        // M2: Reset reconnection attempt counter on successful connection
        delete sessionReconnectAttemptsRef.current[sessionId];

        // Determine the highest sequence number we have confirmed for this session.
        // Priority (highest wins):
        //   1. lastKnownSeqRef  – updated on every received event, survives WS reconnects
        //   2. localStorage     – written by updateLastKnownSeq, survives app restarts /
        //                         WKWebView page reloads (safe: reads are try/catch-guarded)
        //   3. React state      – messages / lastLoadedSeq
        const session = sessionsRef.current[sessionId];
        const sessionMessages = session?.messages || [];
        const refSeq = lastKnownSeqRef.current[sessionId] || 0;
        // Restore watermark from localStorage on app restart (refSeq is 0 only then).
        const persistedSeq = refSeq === 0 ? getLastSeenSeq(sessionId) : 0;
        if (persistedSeq > 0) {
          // Populate the in-memory ref so all later code sees the restored value.
          lastKnownSeqRef.current[sessionId] = persistedSeq;
        }
        const stateSeq = Math.max(
          getMaxSeq(sessionMessages),
          session?.lastLoadedSeq || 0,
        );
        const lastSeq = Math.max(refSeq, persistedSeq, stateSeq);
        // Add a small random jitter (0–300 ms) before sending the initial load_events.
        // When the app opens several sessions at once (e.g., 5 tabs at startup), they
        // all call onopen within the same JS tick and hammer the server storage layer
        // simultaneously. Spreading them out prevents the burst without any visible
        // latency impact (300 ms is imperceptible to the user).
        const startupJitterMs = Math.floor(Math.random() * 300);
        setTimeout(() => {
          // Guard: if this WebSocket was replaced by a newer one during the jitter
          // window (e.g., a force-reconnect fired), discard this stale send.
          if (sessionWsRefs.current[sessionId] !== ws) return;

          if (lastSeq > 0) {
            console.log(
              `Syncing session ${sessionId} from seq ${lastSeq} (refSeq=${refSeq}, persistedSeq=${persistedSeq}, messages=${sessionMessages.length}, jitter=${startupJitterMs}ms)`,
            );
            // When we have a stored watermark but no messages in memory (app restart /
            // WKWebView reload), flag this session so events_loaded can trigger a
            // context load if the after_seq check returns zero new events.
            if (sessionMessages.length === 0) {
              needsContextLoadRef.current[sessionId] = true;
            }
            setPendingSync(sessionId);
            ws.send(
              JSON.stringify({
                type: "load_events",
                data: { after_seq: lastSeq },
              }),
            );
            // Export to window.__debug for Playwright test observability.
            // Tests assert this is > 0 to verify the localStorage watermark was used.
            if (typeof window !== "undefined" && window.__debug) {
              window.__debug.lastLoadEventsAfterSeq = lastSeq;
              // lastInitialLoadEventsAfterSeq is ONLY set here (watermark restore path)
              // and is NOT overwritten by the fallback context load. This allows tests
              // to reliably assert the watermark was used, even when the fallback fires
              // immediately after and resets lastLoadEventsAfterSeq to 0.
              window.__debug.lastInitialLoadEventsAfterSeq = lastSeq;
              window.__debug.lastLoadEventsSessionId = sessionId;
              window.__debug.lastLoadEventsTimestamp = Date.now();
            }
          } else {
            // No watermark at all — true initial load, fetch the last N events.
            console.log(
              `Loading session ${sessionId} events (initial load, jitter=${startupJitterMs}ms)`,
            );
            setPendingSync(sessionId);
            ws.send(
              JSON.stringify({
                type: "load_events",
                data: { limit: INITIAL_EVENTS_LIMIT },
              }),
            );
          }
        }, startupJitterMs);

        // Retry any pending prompts after a short delay to ensure connection is stable
        setTimeout(() => {
          if (retryPendingPromptsRef.current) {
            retryPendingPromptsRef.current(sessionId);
          }
        }, 500);

        // Start keepalive interval to detect zombie connections
        // Clear any existing keepalive for this session first
        if (keepaliveRef.current[sessionId]?.intervalId) {
          clearInterval(keepaliveRef.current[sessionId].intervalId);
        }

        const intervalId = setInterval(() => {
          const currentWs = sessionWsRefs.current[sessionId];
          if (!currentWs || currentWs.readyState !== WebSocket.OPEN) {
            // WebSocket is not open, clear the interval
            clearInterval(intervalId);
            delete keepaliveRef.current[sessionId];
            return;
          }

          const keepalive = keepaliveRef.current[sessionId];
          if (keepalive?.pendingKeepalive) {
            // If a sync (load_events) is in progress, suppress the miss count.
            // Large syncs (hundreds of events) can block the server's readPump,
            // delaying keepalive_ack responses. This prevents false reconnects.
            if (pendingSyncRef.current[sessionId]) {
              console.log(
                `Keepalive miss suppressed for session ${sessionId} (sync in progress)`,
              );
            } else {
              // Previous keepalive didn't get a response
              keepalive.missedCount = (keepalive.missedCount || 0) + 1;
              console.log(
                `Keepalive missed for session ${sessionId}, count: ${keepalive.missedCount}`,
              );

              const lastSeq = lastKnownSeqRef.current[sessionId] || 0;
              const maxMissed =
                lastSeq > LARGE_SESSION_SEQ_THRESHOLD
                  ? KEEPALIVE_MAX_MISSED_LARGE_SESSION
                  : KEEPALIVE_MAX_MISSED_DEFAULT;
              if (keepalive.missedCount >= maxMissed) {
                // Connection is likely dead, force close to trigger reconnect
                console.log(
                  `Too many missed keepalives for session ${sessionId}, forcing reconnect`,
                );
                clearInterval(intervalId);
                delete keepaliveRef.current[sessionId];
                currentWs.close();
                return;
              }
            }
          }

          // Send keepalive with last_seen_seq
          // This allows the server to tell us if we're behind
          const session = sessionsRef.current[sessionId];
          const sessionMessages = session?.messages || [];
          // Get our last known seq (primary: ref, fallback: React state)
          const refSeq = lastKnownSeqRef.current[sessionId] || 0;
          const stateSeq = Math.max(
            getMaxSeq(sessionMessages),
            session?.lastLoadedSeq || 0,
          );
          const lastSeenSeq = Math.max(refSeq, stateSeq);

          keepaliveRef.current[sessionId] = {
            ...keepaliveRef.current[sessionId],
            intervalId,
            pendingKeepalive: true,
            lastSentTime: Date.now(),
          };

          currentWs.send(
            JSON.stringify({
              type: "keepalive",
              data: { client_time: Date.now(), last_seen_seq: lastSeenSeq },
            }),
          );
        }, getKeepaliveInterval());

        keepaliveRef.current[sessionId] = {
          intervalId,
          lastAckTime: Date.now(),
          missedCount: 0,
          pendingKeepalive: false,
        };
      };

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data);
          if (msg.type !== "keepalive_ack") {
            console.log(
              `[WS ${wsId}] Received:`,
              msg.type,
              msg.data?.html?.substring(0, 50) ||
                msg.data?.message?.substring(0, 50) ||
                "",
            );
          }
          handleSessionMessageRef.current(sessionId, msg);
        } catch (err) {
          console.error(
            "Failed to parse session WebSocket message:",
            err,
            event.data,
          );
        }
      };

      ws.onclose = async (event) => {
        console.log(`Session WebSocket closed: ${sessionId} (ws: ${wsId})`, {
          code: event.code,
          reason: event.reason,
          wasClean: event.wasClean,
        });

        // Clean up keepalive interval for this session
        if (keepaliveRef.current[sessionId]?.intervalId) {
          clearInterval(keepaliveRef.current[sessionId].intervalId);
          delete keepaliveRef.current[sessionId];
        }

        // Clear any pending sync flag and its auto-clear timeout.
        // If a load_events request was in flight when the connection dropped,
        // events_loaded will never arrive — without this, pendingSyncRef stays true
        // indefinitely, suppressing keepalive miss-counting after reconnection.
        clearPendingSync(sessionId);
        // Clear the stale recovery cooldown so the fresh connection gets a clean
        // stale detection check (the cooldown is only meaningful within a session).
        delete staleRecoveryCooldownRef.current[sessionId];

        // Only delete the ref if it still points to this WebSocket (not a newer one)
        if (sessionWsRefs.current[sessionId] === ws) {
          delete sessionWsRefs.current[sessionId];
        } else {
          console.log(
            `WebSocket ${wsId} closed but ref points to different WebSocket - not deleting`,
          );
        }
        // Note: We intentionally do NOT clear isStreaming here.
        // The server may still be processing a prompt even if the WebSocket dropped.
        // On reconnection, the 'connected' message will sync the correct is_prompting state.
        // Setting isStreaming: false here would cause a desync where the user sees
        // the Send button (instead of Stop) but the server rejects with "prompt already in progress".

        // Before reconnecting, check if the close was due to auth failure
        // WebSocket doesn't provide HTTP status codes, so we make a quick auth check
        const isAuthenticated = await checkAuthOrRedirect();
        if (!isAuthenticated) {
          // checkAuthOrRedirect already redirected to login if 401
          return;
        }

        // Don't reconnect if the server is shutting down
        if (serverShuttingDownRef.current) {
          console.log(
            `Server shutdown in progress, not reconnecting session ${sessionId}`,
          );
          return;
        }

        // Reconnect if this session is still active (user hasn't switched away)
        // and no newer WebSocket has been created
        // This handles cases like mobile browser suspension when phone is locked
        if (
          activeSessionIdRef.current === sessionId &&
          !sessionWsRefs.current[sessionId]
        ) {
          // --- Stale-session guard: don't reconnect permanently dead sessions ---
          //
          // Attempt cap (isReconnectLimitReached from utils/websocket.js):
          // if we have repeatedly failed to reconnect (e.g. server is down),
          // stop after the canonical MAX_SESSION_RECONNECT_ATTEMPTS limit so
          // we don't loop forever. The counter resets on the next successful
          // onopen, and is cleared when the user explicitly switches to this
          // session (switchSession).
          //
          // Note: we intentionally do NOT gate on session-ID age here. Sessions
          // are persisted on disk (internal/session/store.go) and resumed on
          // demand, so an old ID says nothing about whether the server can
          // reconnect. A prior age heuristic gave up on live, mid-streaming
          // sessions after the WS dropped (mitto-ale).

          const attempt = sessionReconnectAttemptsRef.current[sessionId] || 0;
          // isReconnectLimitReached is exported from utils/websocket.js and
          // uses the canonical MAX_SESSION_RECONNECT_ATTEMPTS = 15 constant.
          const exceededMaxAttempts = isReconnectLimitReached(attempt);

          if (exceededMaxAttempts) {
            console.warn(
              `[reconnect] Giving up on session ${sessionId}: ` +
                `exceeded ${attempt} consecutive reconnect attempts (limit: isReconnectLimitReached)`,
            );
            setSessions((prev) => {
              const session = prev[sessionId];
              if (!session) return prev;
              const messages = limitMessages([
                ...session.messages,
                {
                  role: ROLE_ERROR,
                  text: "⚠️ Could not reconnect to this session after multiple attempts. Refresh the page to try again.",
                  timestamp: Date.now(),
                },
              ]);
              return {
                ...prev,
                [sessionId]: { ...session, messages, isStreaming: false },
              };
            });
            return;
          }

          // Guard: if forceReconnectActiveSession (or a prior onclose) already scheduled
          // a reconnect timer, don't add a second one. The existing timer will fire with
          // the correct backoff delay.
          if (sessionReconnectRefs.current[sessionId]) {
            console.debug(
              `Reconnect already scheduled for session ${sessionId}, skipping onclose reschedule`,
            );
          } else {
            // M2: Use exponential backoff for reconnection
            const delay = calculateReconnectDelay(attempt);
            console.log(
              `Scheduling reconnect for session ${sessionId} (attempt ${attempt + 1}, delay ${delay}ms)`,
            );

            sessionReconnectRefs.current[sessionId] = setTimeout(() => {
              delete sessionReconnectRefs.current[sessionId];
              // Double-check the session is still active before reconnecting
              if (activeSessionIdRef.current === sessionId) {
                // Increment attempt counter before reconnecting
                sessionReconnectAttemptsRef.current[sessionId] = attempt + 1;
                console.log(`Reconnecting to session: ${sessionId}`);
                connectToSession(sessionId);
              }
            }, delay);
          }
        }
      };

      ws.onerror = (err) => {
        console.error(`Session WebSocket error: ${sessionId}`, {
          type: err.type,
          readyState: ws.readyState,
          url: ws.url,
          bufferedAmount: ws.bufferedAmount,
          timestamp: new Date().toISOString(),
        });
        ws.close();
      };

      sessionWsRefs.current[sessionId] = ws;
      return ws;
    },
    [],
  );

  // Send message to the current session's WebSocket
  const sendToSession = useCallback((sessionId, msg) => {
    const ws = sessionWsRefs.current[sessionId];
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
      return true;
    }
    return false;
  }, []);

  // Connect to the global events bus via the SDK's EventsStream (S2,
  // mitto-7gta.18). The stream is created lazily on first call and reused on
  // subsequent calls (its own internal reconnect loop handles drops — this
  // function only needs to call .connect() once per app lifetime).
  //
  // shouldReconnect mirrors the previous inline onclose gate: an auth check
  // (redirect-to-login on 401) followed by a "server shutting down" veto.
  // EventsStream awaits whatever this returns before scheduling its next
  // reconnect attempt.
  const connectToEvents = useCallback(() => {
    if (eventsStreamRef.current) {
      eventsStreamRef.current.connect();
      return;
    }

    const stream = getSdkClient().eventsStream({
      wsBaseUrl: getSdkWsBaseUrl(),
      shouldReconnect: async () => {
        // Before reconnecting, check if the close was due to auth failure.
        // WebSocket doesn't provide HTTP status codes, so we make a quick
        // auth check first.
        const isAuthenticated = await checkAuthOrRedirect();
        if (!isAuthenticated) {
          // checkAuthOrRedirect already redirected to login if 401.
          return false;
        }
        if (serverShuttingDownRef.current) {
          console.log(
            "Server is shutting down, not reconnecting global events WebSocket",
          );
          return false;
        }
        return true;
      },
    });

    stream.on("open", ({ isReconnect }) => {
      setEventsConnected(true);
      console.log(
        "Global events WebSocket connected",
        isReconnect ? "(reconnect)" : "(initial)",
      );

      if (isReconnect) {
        // On reconnect: refresh the session list to catch any changes
        // that occurred while disconnected (e.g., mobile phone locked)
        // but don't switch sessions - keep the user's current session
        console.log("Refreshing session list after reconnect");
        fetchStoredSessions();
      } else {
        // Initial connection: fetch stored sessions and resume the
        // conversation last viewed *in the persisted filter tab*. The active
        // filter tab is persisted per-device (localStorage); restoring a
        // session from a different tab would cause the effect in app.js to
        // flip the tab away from the user's persisted choice. So the restore
        // is tab-aware: prefer the persisted tab's last session, then fall
        // back to the most recent session within that same tab.
        fetchStoredSessions().then((storedSessionsList) => {
          const sessions = storedSessionsList || [];

          const lastSessionId = getLastActiveSessionId();
          const lastSession =
            lastSessionId &&
            sessions.find((s) => s.session_id === lastSessionId);

          // Lazy-connect: only the active session opens a per-session WebSocket
          // at startup. Background sessions are NOT pre-connected — they connect
          // on demand when the user switches to them (via switchSession). This
          // prevents the "startup storm" where every non-archived session resumes
          // its ACP process simultaneously, spiking CPU and memory. Sidebar status
          // for background sessions stays live via the global events WebSocket.
          if (lastSession) {
            switchSession(lastSession.session_id);
          } else {
            if (lastSessionId) {
              console.log(
                `Last active session ${lastSessionId} no longer exists, clearing`,
              );
              setLastActiveSessionId(null);
            }
            // No valid last-active conversation to restore. When app.js has
            // wired a landing callback (mitto-ce3), let it route the UI (to the
            // global Dashboard) instead of aggressively switching into the
            // most-recent conversation, which would bypass the Dashboard on a
            // cold start. Fall back to the previous behavior when unset.
            if (onNoInitialSessionRef?.current) {
              onNoInitialSessionRef.current();
            } else if (sessions.length > 0) {
              // sessions is sorted by updated_at desc — pick the most recent overall.
              switchSession(sessions[0].session_id);
            }
          }
        });
      }
    });

    // EventsStream splits the raw "connected" frame out into its own
    // "connected" event (payload already unwrapped to `msg.data`); every
    // other frame is re-emitted as "event" ({type, data}). handleGlobalEvent
    // switches on msg.type (including a "connected" case), so both are
    // routed through it re-wrapped into the original {type, data} shape to
    // preserve behaviour identically to the raw-WebSocket implementation.
    stream.on("connected", (data) => handleGlobalEvent({ type: "connected", data }));
    stream.on("event", (msg) => handleGlobalEvent(msg));

    stream.on("close", (event) => {
      console.log("Global events WebSocket closed", {
        code: event?.code,
        reason: event?.reason,
        wasClean: event?.wasClean,
      });
      setEventsConnected(false);
    });

    stream.on("error", (err) => {
      console.error("Global events WebSocket error:", err);
    });

    eventsStreamRef.current = stream;
    stream.connect();
  }, [fetchStoredSessions, handleGlobalEvent, switchSession]);

  // Timeout for waiting for WebSocket to connect (in milliseconds)
  const WS_CONNECT_TIMEOUT = 5000;

  /**
   * Wait for the session WebSocket to be connected.
   * If not connected, triggers a reconnection and waits.
   * @param {string} sessionId - The session ID
   * @param {number} timeout - Timeout in milliseconds
   * @returns {Promise<WebSocket>} The connected WebSocket
   */
  const waitForSessionConnection = useCallback(
    (sessionId, timeout = WS_CONNECT_TIMEOUT) => {
      return new Promise((resolve, reject) => {
        // Check if already connected
        const existingWs = sessionWsRefs.current[sessionId];
        if (existingWs && existingWs.readyState === WebSocket.OPEN) {
          resolve(existingWs);
          return;
        }

        console.log(
          `WebSocket not connected for session ${sessionId}, triggering reconnect`,
        );

        // Clear any pending reconnect timer
        if (sessionReconnectRefs.current[sessionId]) {
          clearTimeout(sessionReconnectRefs.current[sessionId]);
          delete sessionReconnectRefs.current[sessionId];
        }

        // Close existing zombie WebSocket if any
        if (existingWs) {
          delete sessionWsRefs.current[sessionId];
          existingWs.close();
        }

        // Set up timeout
        const timeoutId = setTimeout(() => {
          reject(
            new Error(
              "Connection timed out. Please check your network and try again.",
            ),
          );
        }, timeout);

        // Create new WebSocket connection
        const ws = new WebSocket(endpoints.sessions.ws(sessionId));
        const wsId = Math.random().toString(36).substring(2, 8);
        ws._debugId = wsId;

        ws.onopen = () => {
          clearTimeout(timeoutId);
          console.log(
            `Session WebSocket connected (reconnect): ${sessionId} (ws: ${wsId})`,
          );

          // Store the WebSocket reference
          sessionWsRefs.current[sessionId] = ws;

          // Sync events we may have missed while disconnected
          // Calculate lastSeenSeq dynamically (not localStorage)
          // Use load_events instead of deprecated sync_session
          const session = sessionsRef.current[sessionId];
          const sessionMessages = session?.messages || [];
          // Get our last known seq (primary: ref, fallback: React state)
          const refSeq = lastKnownSeqRef.current[sessionId] || 0;
          const stateSeq = Math.max(
            getMaxSeq(sessionMessages),
            session?.lastLoadedSeq || 0,
          );
          const lastSeq = Math.max(refSeq, stateSeq);
          if (lastSeq > 0) {
            console.log(
              `Syncing session ${sessionId} from seq ${lastSeq} (lastLoadedSeq=${session?.lastLoadedSeq}, messages=${sessionMessages.length})`,
            );
            ws.send(
              JSON.stringify({
                type: "load_events",
                data: { after_seq: lastSeq },
              }),
            );
            // Export to window.__debug for Playwright test observability (reconnect path)
            if (typeof window !== "undefined" && window.__debug) {
              window.__debug.lastLoadEventsAfterSeq = lastSeq;
              window.__debug.lastLoadEventsSessionId = sessionId;
              window.__debug.lastLoadEventsTimestamp = Date.now();
            }
          }

          resolve(ws);
        };

        ws.onerror = (err) => {
          clearTimeout(timeoutId);
          console.error(`Session WebSocket error during reconnect:`, {
            type: err.type,
            readyState: ws.readyState,
            url: ws.url,
            bufferedAmount: ws.bufferedAmount,
            timestamp: new Date().toISOString(),
          });
          reject(new Error("Failed to connect. Please try again."));
        };

        ws.onclose = (event) => {
          console.log(
            `Session WebSocket closed during reconnect: ${sessionId}`,
            {
              code: event.code,
              reason: event.reason,
              wasClean: event.wasClean,
            },
          );
          // If we haven't resolved yet, this is an early close
          clearTimeout(timeoutId);
          if (sessionWsRefs.current[sessionId] === ws) {
            delete sessionWsRefs.current[sessionId];
          }
        };

        // Set up message handler (reuse existing logic)
        ws.onmessage = (event) => {
          try {
            const msg = JSON.parse(event.data);
            handleSessionMessageRef.current(sessionId, msg);
          } catch (err) {
            console.error("Failed to parse session message:", err);
          }
        };
      });
    },
    [],
  );

  /**
   * Check if the WebSocket connection for a session is healthy.
   * A connection is considered healthy if we've received a keepalive_ack recently.
   * @param {string} sessionId - The session ID
   * @returns {boolean} True if connection is healthy
   */
  const isConnectionHealthy = useCallback((sessionId) => {
    const keepalive = keepaliveRef.current[sessionId];
    if (!keepalive) return true; // No keepalive tracking yet, assume healthy

    const timeSinceLastAck = Date.now() - (keepalive.lastAckTime || 0);
    // Consider unhealthy if we haven't received an ACK in 2x the keepalive interval
    // or if we have missed keepalives
    const isHealthy =
      timeSinceLastAck < getKeepaliveInterval() * 2 &&
      (keepalive.missedCount || 0) === 0;

    if (!isHealthy) {
      console.log(
        `Connection unhealthy for session ${sessionId}: timeSinceLastAck=${timeSinceLastAck}ms, missedCount=${keepalive.missedCount}`,
      );
    }
    return isHealthy;
  }, []);

  // Force reconnect active session WebSocket - closes existing connection and schedules a new one
  // Uses the shared exponential backoff counter so repeated failures accumulate delay,
  // and debouncing collapses bursts of concurrent triggers (keepalive miss, visibility
  // change, native app activate) that can fire seconds apart into a single reconnect.
  const forceReconnectActiveSession = useCallback(() => {
    const currentSessionId = activeSessionIdRef.current;
    if (!currentSessionId) return;

    // Debounce: skip if a reconnect was already triggered for this session within the window
    const { debounced, elapsed } = shouldDebounceReconnect(
      reconnectDebounceRef.current,
      currentSessionId,
    );
    if (debounced) {
      console.debug(
        `Skipping duplicate force-reconnect for session ${currentSessionId} (${elapsed}ms since last)`,
      );
      return;
    }

    console.log(`Force reconnecting session ${currentSessionId}`);

    // Clear any pending reconnect timer so we don't double-schedule
    if (sessionReconnectRefs.current[currentSessionId]) {
      clearTimeout(sessionReconnectRefs.current[currentSessionId]);
      delete sessionReconnectRefs.current[currentSessionId];
    }

    // Close existing WebSocket if any.
    // Pre-delete the ref so that the ws.onclose handler sees no active WS ref
    // and skips its own scheduling (it checks sessionReconnectRefs too).
    const existingWs = sessionWsRefs.current[currentSessionId];
    if (existingWs) {
      delete sessionWsRefs.current[currentSessionId];
      existingWs.close();
    }

    // Use the shared exponential backoff counter — the same one used by onclose.
    // This means repeated failures across all paths accumulate delay correctly.
    // On successful connect, onopen resets the counter (via delete), so the
    // next disconnect starts fresh from attempt 0.
    const attempt = sessionReconnectAttemptsRef.current[currentSessionId] || 0;
    const delay = calculateReconnectDelay(attempt);
    console.log(
      `Scheduling force-reconnect for session ${currentSessionId} (attempt ${attempt + 1}, delay ${delay}ms)`,
    );

    sessionReconnectRefs.current[currentSessionId] = setTimeout(() => {
      delete sessionReconnectRefs.current[currentSessionId];
      // Double-check the session is still active before reconnecting
      if (activeSessionIdRef.current === currentSessionId) {
        // Increment shared attempt counter before connecting
        sessionReconnectAttemptsRef.current[currentSessionId] = attempt + 1;
        connectToSession(currentSessionId);
      }
    }, delay);
  }, [connectToSession]);

  // Reconnect all currently-connected sessions with staggering to prevent thundering herd.
  // The active session reconnects immediately via forceReconnectActiveSession (with its existing
  // debounce/backoff logic). Background sessions (those with open WebSockets but not active) are
  // force-closed and reconnected with increasing delays so their load_events requests are spread
  // over time rather than hitting the server simultaneously.
  //
  // Under lazy-connect this operates only on sessions that already have an open
  // WebSocket — typically just the active session plus any background sessions
  // still within their disconnect grace window. It never opens connections for
  // sessions that were not already connected, so it does not re-create a startup
  // storm on wake/visibility events.
  //
  // Debounce: multiple macOS activation events (NSWorkspaceDidWakeNotification,
  // NSWorkspaceScreensDidWakeNotification, applicationDidBecomeActive) can fire
  // 4–10 s apart for the same wake/focus event.  Without a debounce each call
  // schedules a new set of background-session timers; when two sets fire they
  // both close-then-reconnect the same WebSocket, and because server-side teardown
  // is async the old observer is still registered when the new one is added —
  // causing the observer count to climb to 3+ per session.
  //
  // Two-layer protection:
  //   1. Leading-edge debounce: suppress calls within STAGGERED_RECONNECT_DEBOUNCE_MS
  //      (15 s) of the last accepted call.  This coalesces the macOS native
  //      "App became active" and WKWebView "App became visible" pair (which fire
  //      ~6–10 s apart for a single wake) into one reconnect, preventing a
  //      redundant active-session force-reconnect.
  //   2. Timer cancellation: cancel any still-pending background-session timers from
  //      a previous call before scheduling new ones (defense-in-depth for any pair
  //      that still slips past the debounce window before its timers have fired).
  const reconnectAllSessionsStaggered = useCallback(() => {
    // Layer 1: leading-edge debounce.
    const now = Date.now();
    const elapsed = now - lastStaggeredReconnectRef.current;
    if (
      lastStaggeredReconnectRef.current > 0 &&
      elapsed < STAGGERED_RECONNECT_DEBOUNCE_MS
    ) {
      console.debug(
        `[stagger] Skipping duplicate staggered reconnect (${elapsed}ms since last, debounce=${STAGGERED_RECONNECT_DEBOUNCE_MS}ms)`,
      );
      return;
    }
    lastStaggeredReconnectRef.current = now;

    const currentSessionId = activeSessionIdRef.current;

    // Reconnect active session immediately using existing debounce logic
    forceReconnectActiveSession();

    // Layer 2: cancel any still-pending background-session timers from a prior call
    // before scheduling a new batch.  This prevents two sets of timers from both
    // firing and reconnecting the same background session concurrently.
    const pendingCount = Object.keys(
      staggeredBackgroundTimersRef.current,
    ).length;
    if (pendingCount > 0) {
      console.debug(
        `[stagger] Cancelling ${pendingCount} pending background timer(s) from previous call`,
      );
      for (const timerId of Object.values(
        staggeredBackgroundTimersRef.current,
      )) {
        clearTimeout(timerId);
      }
      staggeredBackgroundTimersRef.current = {};
    }

    // Stagger reconnections for background sessions to spread load_events requests
    const backgroundIds = Object.keys(sessionWsRefs.current).filter(
      (id) => id !== currentSessionId,
    );

    if (backgroundIds.length > 0) {
      console.log(
        `[stagger] Scheduling staggered reconnects for ${backgroundIds.length} background session(s)`,
      );
    }

    backgroundIds.forEach((sessionId, index) => {
      const timerId = setTimeout(
        () => {
          // Remove our own entry now that we're executing
          delete staggeredBackgroundTimersRef.current[sessionId];

          const existingWs = sessionWsRefs.current[sessionId];
          if (existingWs) {
            console.log(
              `[stagger] Reconnecting background session ${sessionId} (delay ${(index + 1) * STARTUP_STAGGER_MS}ms)`,
            );
            delete sessionWsRefs.current[sessionId];
            existingWs.close();
            // onclose won't reconnect non-active sessions, so reconnect manually
            connectToSession(sessionId);
          }
        },
        (index + 1) * STARTUP_STAGGER_MS,
      );

      // Track timer so it can be cancelled if another call arrives before it fires
      staggeredBackgroundTimersRef.current[sessionId] = timerId;
    });
  }, [forceReconnectActiveSession, connectToSession]);

  // Lazy-connect hygiene: when the active session changes, schedule a sweep that
  // disconnects per-session WebSockets for sessions that are no longer active.
  // Releasing the WebSocket removes the server-side observer, allowing the backend
  // GC to reclaim idle ACP processes. The timer resets on every active-session
  // change, so only sessions left untouched for the full grace window are dropped.
  // Sessions that are actively streaming, awaiting a user prompt, or waiting on
  // child conversations are kept connected so in-flight work is not interrupted;
  // they are swept on a later switch once they go idle. Disconnected sessions
  // resync from server authority via load_events when the user switches back.
  useEffect(() => {
    if (backgroundDisconnectTimerRef.current) {
      clearTimeout(backgroundDisconnectTimerRef.current);
      backgroundDisconnectTimerRef.current = null;
    }

    backgroundDisconnectTimerRef.current = setTimeout(() => {
      backgroundDisconnectTimerRef.current = null;
      const currentActive = activeSessionIdRef.current;

      // Sessions doing active work are kept connected (driven by global events).
      const keepConnected = new Set(
        (storedSessionsRef.current || [])
          .filter(
            (s) =>
              s.isStreaming ||
              s.isWaitingForUserInput ||
              s.isWaitingForChildren,
          )
          .map((s) => s.session_id),
      );

      for (const sessionId of Object.keys(sessionWsRefs.current)) {
        if (sessionId === currentActive) continue; // never disconnect active
        if (keepConnected.has(sessionId)) continue; // keep busy sessions live

        // Cancel any pending reconnect timer for this background session so it
        // does not get revived after we intentionally disconnect it.
        if (sessionReconnectRefs.current[sessionId]) {
          clearTimeout(sessionReconnectRefs.current[sessionId]);
          delete sessionReconnectRefs.current[sessionId];
        }

        const ws = sessionWsRefs.current[sessionId];
        if (ws) {
          console.log(
            `[lazy] Disconnecting idle background session ${sessionId} (grace elapsed)`,
          );
          // Delete the ref BEFORE closing so onclose treats this as intentional.
          // (The onclose reconnect guard only fires for the active session anyway.)
          delete sessionWsRefs.current[sessionId];
          ws.close();
        }
      }
    }, BACKGROUND_DISCONNECT_GRACE_MS);

    return () => {
      if (backgroundDisconnectTimerRef.current) {
        clearTimeout(backgroundDisconnectTimerRef.current);
        backgroundDisconnectTimerRef.current = null;
      }
    };
  }, [activeSessionId]);

  return {
    connected: eventsConnected,
    forceReconnectActiveSession,
    reconnectAllSessionsStaggered,
    connectToSession,
    connectToEvents,
    isConnectionHealthy,
    waitForSessionConnection,
    sendToSession,
    sessionWsRefs,
    sessionReconnectRefs,
    sessionReconnectAttemptsRef,
    keepaliveRef,
    serverShuttingDownRef,
    eventsStreamRef,
    staggeredBackgroundTimersRef,
  };
}
