// =============================================================================
// Mitto Web Interface — WebSocket Mobile Resilience sub-hook
// Extracted from useWebSocket.js (mitto-90f.6.1).
//
// Owns the state and effect that keep mobile clients alive across sleep/wake
// cycles (visibility-driven reconnect + auth staleness check). Specifically:
//
//   * lastHiddenTimeRef — timestamp (ms) of when the tab last went hidden;
//     used to detect wake-from-long-sleep in the visibilitychange handler.
//   * staleRecoveryCooldownRef — { [sessionId]: timestamp } cooldown map
//     consulted by useWebSocket's handleSessionMessage / keepalive paths to
//     avoid re-triggering stale-client recovery while React state settles.
//     The composer receives this ref via the returned bag and writes to it
//     directly (identical semantics to the pre-extraction implementation).
//   * isMobileDevice — memoised userAgent sniff used by INITIAL_ACK_TIMEOUT_MS
//     (composer needs the value; sub-hook exposes it on the return bag).
//
// The visibilitychange effect is moved verbatim: same 3-await sequence
// (checkAuthWithRetry → fetchStoredSessions → reconnectAllSessionsStaggered)
// with the nested setTimeout(2000)/setTimeout(300) retry cadence preserved
// byte-for-byte, per mitto-90f.6 scoping report and rule 23 (async ordering).
// =============================================================================

const { useEffect, useRef, useMemo } = window.preact;

import { cleanupExpiredPrompts } from "../lib.js";
import {
  checkAuthWithRetry,
  STALE_THRESHOLD_MS,
} from "../utils/websocket.js";

/**
 * Mobile resilience sub-hook for useWebSocket.
 *
 * @param {object} props
 * @param {() => void} props.reconnectAllSessionsStaggered
 *   Composer callback that force-reconnects all session websockets with
 *   staggering. Called after wake from a long hidden period.
 * @param {() => Promise<Array<{session_id: string}>>} props.fetchStoredSessions
 *   Composer callback that reloads the stored-sessions list.
 * @param {(sessionId: string) => void} props.switchSession
 *   Composer callback that switches the active session.
 * @param {(sessionId: string | null) => void} props.setActiveSessionId
 *   Composer state setter used when the active session was deleted while
 *   the phone was sleeping.
 * @param {{current: string | null}} props.activeSessionIdRef
 *   Ref pointing at the current active session id (kept in sync by the
 *   composer). Read inside the handler to avoid capturing a stale value.
 *
 * @returns {{
 *   isMobileDevice: boolean,
 *   staleRecoveryCooldownRef: {current: Record<string, number>},
 *   lastHiddenTimeRef: {current: number | null},
 * }}
 */
export function useWSMobileResilience({
  reconnectAllSessionsStaggered,
  fetchStoredSessions,
  switchSession,
  setActiveSessionId,
  activeSessionIdRef,
}) {
  // Track when the page was last hidden (for staleness detection on mobile)
  const lastHiddenTimeRef = useRef(null);

  // Cooldown after stale recovery to prevent feedback loops.
  // Maps sessionId → timestamp of last stale recovery.
  // When set, keepalive skips stale detection for this session for STALE_RECOVERY_COOLDOWN_MS.
  const staleRecoveryCooldownRef = useRef({});

  // Initial ACK timeout: short to quickly detect zombie connections
  // Mobile gets slightly longer due to network variability
  const isMobileDevice = useMemo(() => {
    if (typeof navigator === "undefined") return false;
    const ua = navigator.userAgent || "";
    return /iPhone|iPad|iPod|Android|webOS|BlackBerry|IEMobile|Opera Mini/i.test(
      ua,
    );
  }, []);

  // Refresh session list, force reconnect session WebSocket, and retry pending prompts when app becomes visible
  // On mobile, when the phone sleeps, WebSocket connections can become "zombie" connections
  // that appear open but are actually dead. The safest approach is to force a fresh reconnection.
  // Also detect if the session might be stale (phone locked overnight) and verify auth first.
  useEffect(() => {
    const handleVisibilityChange = async () => {
      if (document.visibilityState === "hidden") {
        // Track when the page was hidden so we can detect staleness on wake
        lastHiddenTimeRef.current = Date.now();
        console.log("App hidden, tracking time");
        return;
      }

      if (document.visibilityState === "visible") {
        const now = Date.now();
        const hiddenDuration = lastHiddenTimeRef.current
          ? now - lastHiddenTimeRef.current
          : 0;
        const wasHiddenLong = hiddenDuration > STALE_THRESHOLD_MS;

        console.log(
          `App became visible after ${Math.round(hiddenDuration / 1000)}s` +
            (wasHiddenLong ? " (checking auth first)" : ""),
        );

        // Clean up expired prompts first
        cleanupExpiredPrompts();

        // If the page was hidden for a long time (e.g., phone locked overnight),
        // do an explicit auth check before trying to reconnect.
        // This prevents the user from seeing a stuck/stale state.
        if (wasHiddenLong) {
          console.log("Session may be stale, verifying authentication...");
          const { authenticated, networkError } = await checkAuthWithRetry();

          if (!authenticated) {
            if (networkError) {
              // Network is not available yet - this is common right after phone unlock
              // Wait a bit longer and try again
              console.log(
                "Network not available, will retry auth check in 2s...",
              );
              setTimeout(async () => {
                const retry = await checkAuthWithRetry();
                if (!retry.authenticated && !retry.networkError) {
                  // 401 - session expired
                  return;
                }
                // Either authenticated or still network error - proceed with normal reconnect
                // If still network error, the WebSocket reconnect will handle retries
                const retrySessions = await fetchStoredSessions();

                // Check if the active session still exists
                const retryCurrentSessionId = activeSessionIdRef.current;
                const retrySessionExists =
                  retryCurrentSessionId &&
                  retrySessions.some(
                    (s) => s.session_id === retryCurrentSessionId,
                  );

                if (retryCurrentSessionId && !retrySessionExists) {
                  // Active session was deleted
                  console.log(
                    `Active session ${retryCurrentSessionId} no longer exists, switching...`,
                  );
                  if (retrySessions.length > 0) {
                    switchSession(retrySessions[0].session_id);
                  } else {
                    setActiveSessionId(null);
                  }
                } else {
                  setTimeout(() => {
                    reconnectAllSessionsStaggered();
                  }, 300);
                }
              }, 2000);
              return;
            }
            // Auth check returned 401 - redirectToLogin was already called
            return;
          }
          console.log("Authentication verified, proceeding with reconnect");
        }

        // Fetch stored sessions (updates the session list in sidebar)
        // We await this to ensure we have the latest session list before reconnecting
        const sessions = await fetchStoredSessions();

        // Check if the active session still exists (it may have been deleted while phone was sleeping)
        const currentSessionId = activeSessionIdRef.current;
        const activeSessionExists =
          currentSessionId &&
          sessions.some((s) => s.session_id === currentSessionId);

        if (currentSessionId && !activeSessionExists) {
          // Active session was deleted while phone was sleeping
          console.log(
            `Active session ${currentSessionId} no longer exists, switching...`,
          );
          if (sessions.length > 0) {
            // Switch to the most recent session
            switchSession(sessions[0].session_id);
          } else {
            // No sessions left
            setActiveSessionId(null);
          }
        } else {
          // Reconnect all sessions with staggering to prevent thundering herd.
          // Active session reconnects immediately; background sessions reconnect
          // with increasing delays so their load_events requests don't all hit
          // the server at the same time.
          // Use a small delay to allow the network stack to stabilize after wake.
          setTimeout(() => {
            reconnectAllSessionsStaggered();
          }, 300);
        }
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [fetchStoredSessions, reconnectAllSessionsStaggered, switchSession]);

  return {
    isMobileDevice,
    staleRecoveryCooldownRef,
    lastHiddenTimeRef,
  };
}
