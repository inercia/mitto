// =============================================================================
// Mitto Web Interface — WebSocket Connection sub-hook (C1)
// Extracted from useWebSocket.js (mitto-90f.6.2).
//
// Owns the transport primitives for both per-session WebSockets and the global
// events WebSocket. Per-session transport (connectToSession,
// waitForSessionConnection, sendToSession, isConnectionHealthy,
// forceReconnectActiveSession, reconnectAllSessionsStaggered,
// background-disconnect sweep) is backed by the SDK's SessionStream
// (sdk/realtime/session-stream.js, mitto-7gta.30) instead of a raw WebSocket:
// the stream owns its own reconnect/backoff, keepalive/zombie detection,
// stale/behind-client sync, and gap-fill internally, so this hook only
// creates one stream per session (lazily, cached in sessionWsRefs) and drives
// it via `.connect()` / `.send()` / `.forceReconnect()` / `.close()`.
//
// The message handler itself (`handleSessionMessage`) stays in the composer
// and is threaded in via a ref (`handleSessionMessageRef`) so its identity is
// stable across reconnects (rule 21-web-frontend-state). keepalive_ack frames
// are NOT part of SessionStream's "message" event (the stream interprets
// their seq/stale-detection payload internally) — the composer's UI-relevant
// keepalive_ack handling (queue length, processor stats, streaming/running
// sync) is threaded through a second ref, `handleSessionKeepaliveAckRef`.
//
// useWSSeqSync outputs (`lastKnownSeqRef`) are received as props and folded
// into the stream's injected `getClientMaxSeq` option so stale-client
// detection keeps seeing React-state-derived seqs (rule 24-web-frontend-sync).
//
// Ref-bridges owned by the composer and read by this sub-hook:
//   * retryPendingPromptsRef — populated by C2 (useWSDeliveryVerification);
//     read inside connectToSession's onopen handler.
//   * rejectOversizedPromptsRef — populated by C2; quarantines pending prompts
//     when the server rejects an unparseable frame with close code 1009.
//   * handleSessionKeepaliveAckRef — populated by the composer; forwards
//     keepalive_ack payloads for UI-only bookkeeping (see above).
//
// staleRecoveryCooldownRef is owned by useWSMobileResilience (mitto-90f.6.1)
// and passed in as a prop; the session close handler clears its entry when
// the connection drops.
//
// connectToEvents (S2, mitto-7gta.18) is backed by the SDK's EventsStream
// (sdk/realtime/events-stream.js) the same way — see connectToEvents below.
// =============================================================================

const { useCallback, useEffect, useRef, useState } = window.preact;

import { ROLE_ERROR, INITIAL_EVENTS_LIMIT, limitMessages, getMaxSeq } from "../lib.js";
import { setLastActiveSessionId, getLastActiveSessionId } from "../utils/storage.js";
import {
  getSdkClient,
  getSdkWsBaseUrl,
  createSdkSeqStore,
  createSdkPendingPromptStore,
} from "../utils/sdkClient.js";
import { MittoNetworkError } from "../sdk/index.js";
import {
  checkAuthOrRedirect,
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
  // keepalive_ack UI bookkeeping (queue length, processor stats, streaming
  // sync) — SessionStream excludes keepalive_ack from its "message" event,
  // so the composer's handling is threaded through this second ref instead.
  handleSessionKeepaliveAckRef,
  handleGlobalEvent,
  // Composer callbacks used by connectToEvents onopen restore path
  fetchStoredSessions,
  switchSession,
  onNoInitialSessionRef,
  // Ref-bridges owned by composer, populated by C2 (mitto-90f.6.3)
  retryPendingPromptsRef,
  rejectOversizedPromptsRef,
  // useWSSeqSync outputs (rule 24 — passed as props, NOT re-invoked)
  lastKnownSeqRef,
  // From useWSMobileResilience (mitto-90f.6.1)
  staleRecoveryCooldownRef,
  // Composer sync-tracking primitives. pendingSyncRef also tracks the
  // composer's OWN manual syncs (acp_stopped delayed sync, load-more-messages
  // before_seq, fallback context load) — injected into the stream as
  // `isSyncInFlight` so the stream's internal stale/behind-detection never
  // races a composer-initiated load_events with its own.
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
  // { sessionId: SessionStream } — one lazily-created stream per session
  // (mitto-7gta.30 S1). Kept as `sessionWsRefs` for one commit to keep this
  // diff reviewable; renamed to `sessionStreamsRef` in a follow-up cleanup.
  const sessionWsRefs = useRef({});
  const serverShuttingDownRef = useRef(false);
  const staggeredBackgroundTimersRef = useRef({});
  const lastStaggeredReconnectRef = useRef(0);
  const backgroundDisconnectTimerRef = useRef(null);

  // shouldReconnect veto shared by every per-session SessionStream: an auth
  // check (redirect-to-login on 401) followed by a "server shutting down"
  // veto — identical gate to connectToEvents' EventsStream below.
  const sessionShouldReconnect = useCallback(async () => {
    const isAuthenticated = await checkAuthOrRedirect();
    if (!isAuthenticated) return false; // already redirected to login on 401
    if (serverShuttingDownRef.current) {
      console.log("Server is shutting down, not reconnecting session WebSocket");
      return false;
    }
    return true;
  }, []);

  // Get-or-create the SessionStream for a session, wiring its event
  // listeners exactly once (on creation). Does NOT call .connect() — callers
  // decide when to open the connection.
  const getOrCreateStream = useCallback(
    (sessionId) => {
      const existing = sessionWsRefs.current[sessionId];
      if (existing) return existing;

      const stream = getSdkClient().sessionStream(sessionId, {
        wsBaseUrl: getSdkWsBaseUrl(),
        seqStore: createSdkSeqStore(),
        pendingPromptStore: createSdkPendingPromptStore(),
        keepaliveIntervalMs: getKeepaliveInterval(),
        shouldReconnect: sessionShouldReconnect,
        // Folds React-state-derived seqs into the stream's stale/behind-client
        // detection, matching the pre-migration keepalive_ack computation.
        getClientMaxSeq: () => {
          const session = sessionsRef.current[sessionId];
          const refSeq = lastKnownSeqRef.current[sessionId] || 0;
          const stateSeq = Math.max(
            getMaxSeq(session?.messages || []),
            session?.lastLoadedSeq || 0,
          );
          return Math.max(refSeq, stateSeq);
        },
        // See pendingSyncRef comment above the destructured props.
        isSyncInFlight: () => !!pendingSyncRef.current[sessionId],
      });

      stream.on("open", () => {
        console.log(`Session stream connected: ${sessionId}`);

        const session = sessionsRef.current[sessionId];
        const sessionMessages = session?.messages || [];
        const refSeq = lastKnownSeqRef.current[sessionId] || 0;
        // Restore watermark from the stream's seqStore on app restart
        // (refSeq is 0 only then — the seqStore mirrors localStorage).
        const persistedSeq = refSeq === 0 ? stream.lastSeenSeq() : 0;
        if (persistedSeq > 0) {
          lastKnownSeqRef.current[sessionId] = persistedSeq;
        }
        const stateSeq = Math.max(
          getMaxSeq(sessionMessages),
          session?.lastLoadedSeq || 0,
        );
        const lastSeq = Math.max(refSeq, persistedSeq, stateSeq);
        // Add a small random jitter (0–300 ms) before sending the initial load_events.
        // When the app opens several sessions at once (e.g., 5 tabs at startup), they
        // all open within the same JS tick and hammer the server storage layer
        // simultaneously. Spreading them out prevents the burst without any visible
        // latency impact (300 ms is imperceptible to the user).
        const startupJitterMs = Math.floor(Math.random() * 300);
        setTimeout(() => {
          // Guard: if this stream was replaced/torn down during the jitter
          // window, discard this stale send.
          if (sessionWsRefs.current[sessionId] !== stream) return;

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
            stream.send({ type: "load_events", data: { after_seq: lastSeq } });
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
            stream.send({
              type: "load_events",
              data: { limit: INITIAL_EVENTS_LIMIT },
            });
          }
        }, startupJitterMs);

        // Retry any pending prompts after a short delay to ensure connection is stable
        setTimeout(() => {
          if (retryPendingPromptsRef.current) {
            retryPendingPromptsRef.current(sessionId);
          }
        }, 500);
      });

      stream.on("message", (msg) => {
        console.log(
          `[stream ${sessionId}] Received:`,
          msg.type,
          msg.data?.html?.substring(0, 50) ||
            msg.data?.message?.substring(0, 50) ||
            "",
        );
        handleSessionMessageRef.current(sessionId, msg);
      });

      // keepalive_ack carries UI-relevant state (queue_length, processor
      // stats, is_running/is_prompting/status) that the stream itself does
      // not interpret beyond seq/stale-detection — forward it separately.
      stream.on("keepalive_ack", (data) => {
        handleSessionKeepaliveAckRef.current?.(sessionId, data);
      });

      stream.on("close", (event) => {
        if (event?.code === 1009) {
          if (sessionWsRefs.current[sessionId] === stream) {
            delete sessionWsRefs.current[sessionId];
          }
          rejectOversizedPromptsRef.current?.(sessionId);
        }
        // Clear any pending sync flag and its auto-clear timeout. If a
        // composer-initiated load_events (acp_stopped delayed sync,
        // load-more-messages, fallback context load) was in flight when the
        // connection dropped, events_loaded will never arrive — without
        // this, pendingSyncRef stays true indefinitely, suppressing the
        // stream's own stale/behind-detection after reconnection.
        clearPendingSync(sessionId);
        // Clear the stale recovery cooldown so the fresh connection gets a
        // clean stale detection check (cooldown is only meaningful within a
        // session's lifetime).
        delete staleRecoveryCooldownRef.current[sessionId];
      });

      stream.on("error", (err) => {
        // MittoNetworkError with this shape means the stream gave up after
        // exhausting its reconnect-attempt budget (see SessionStream's
        // _reconnectOrStop). Surface the same user-facing message the
        // pre-migration raw-WebSocket path showed.
        if (
          err instanceof MittoNetworkError &&
          /reconnect attempt limit reached/.test(err.message || "")
        ) {
          console.warn(
            `[reconnect] Giving up on session ${sessionId}: exceeded reconnect attempt limit`,
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
        console.error(`Session stream error: ${sessionId}`, err);
      });

      sessionWsRefs.current[sessionId] = stream;
      return stream;
    },
    [sessionShouldReconnect],
  );

  // Connect to (or reuse) the per-session SessionStream.
  const connectToSession = useCallback(
    (sessionId) => {
      const stream = getOrCreateStream(sessionId);
      stream.connect();
      return stream;
    },
    [getOrCreateStream],
  );

  // Send message to the current session's stream.
  const sendToSession = useCallback((sessionId, msg) => {
    const stream = sessionWsRefs.current[sessionId];
    return stream ? stream.send(msg) : false;
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

  // Timeout for waiting for a session stream to connect (in milliseconds)
  const WS_CONNECT_TIMEOUT = 5000;

  /**
   * Wait for the session's stream to be connected. If not connected,
   * triggers a connect and waits. The stream's own "open" handler (wired in
   * getOrCreateStream) performs the sync/load_events send — this function
   * only waits for that "open" event, it does not duplicate the send.
   * @param {string} sessionId - The session ID
   * @param {number} timeout - Timeout in milliseconds
   * @returns {Promise<import("../sdk/index.js").SessionStream>} The connected stream
   */
  const waitForSessionConnection = useCallback(
    (sessionId, timeout = WS_CONNECT_TIMEOUT) => {
      return new Promise((resolve, reject) => {
        const stream = getOrCreateStream(sessionId);
        if (stream.state === "open") {
          resolve(stream);
          return;
        }

        console.log(
          `Session stream not connected for session ${sessionId}, triggering reconnect`,
        );

        const timeoutId = setTimeout(() => {
          reject(
            new Error(
              "Connection timed out. Please check your network and try again.",
            ),
          );
        }, timeout);

        stream.once("open", () => {
          clearTimeout(timeoutId);
          resolve(stream);
        });

        // No-op if already connecting; otherwise (re)opens the socket —
        // handles both a cold start and a suspected zombie connection.
        stream.connect();
      });
    },
    [getOrCreateStream],
  );

  /**
   * Check if the stream for a session is healthy (acked within 2x the
   * keepalive interval and no outstanding missed keepalives).
   * @param {string} sessionId - The session ID
   * @returns {boolean} True if connection is healthy
   */
  const isConnectionHealthy = useCallback((sessionId) => {
    const stream = sessionWsRefs.current[sessionId];
    if (!stream) return true; // No stream yet, assume healthy
    const healthy = stream.isHealthy();
    if (!healthy) {
      console.log(`Connection unhealthy for session ${sessionId}`);
    }
    return healthy;
  }, []);

  // Force reconnect the active session's stream. SessionStream.forceReconnect()
  // owns its own leading-edge debounce and exponential-backoff reconnect
  // scheduling internally, so this is now a thin pass-through.
  const forceReconnectActiveSession = useCallback(() => {
    const currentSessionId = activeSessionIdRef.current;
    if (!currentSessionId) return;
    console.log(`Force reconnecting session ${currentSessionId}`);
    getOrCreateStream(currentSessionId).forceReconnect();
  }, [getOrCreateStream]);

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

          const existingStream = sessionWsRefs.current[sessionId];
          if (existingStream) {
            console.log(
              `[stagger] Reconnecting background session ${sessionId} (delay ${(index + 1) * STARTUP_STAGGER_MS}ms)`,
            );
            // SessionStream.forceReconnect() closes-then-reopens internally
            // and owns its own reconnect scheduling — no manual close +
            // connectToSession round trip needed.
            existingStream.forceReconnect();
          }
        },
        (index + 1) * STARTUP_STAGGER_MS,
      );

      // Track timer so it can be cancelled if another call arrives before it fires
      staggeredBackgroundTimersRef.current[sessionId] = timerId;
    });
  }, [forceReconnectActiveSession]);

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

        const stream = sessionWsRefs.current[sessionId];
        if (stream) {
          console.log(
            `[lazy] Disconnecting idle background session ${sessionId} (grace elapsed)`,
          );
          // stream.close() is an explicit close — SessionStream never
          // schedules a reconnect after it, so no extra timer cancellation
          // (the reconnect-attempt bookkeeping lives inside the stream now).
          delete sessionWsRefs.current[sessionId];
          stream.close();
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
    // The per-session reconnect-timer map, reconnect-attempt counter map, and
    // keepalive-state map that used to live here are gone (mitto-7gta.30):
    // SessionStream owns reconnect scheduling, attempt counting, and
    // keepalive/zombie detection internally per session.
    sessionWsRefs,
    serverShuttingDownRef,
    eventsStreamRef,
    staggeredBackgroundTimersRef,
  };
}
