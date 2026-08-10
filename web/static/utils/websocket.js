/**
 * WebSocket utility functions for sequence number tracking, deduplication,
 * and reconnection handling.
 *
 * These functions are extracted from useWebSocket.js for testability.
 */

import { getLastSeenSeq, setLastSeenSeq } from "./storage.js";
import { getSdkClient, redirectToLogin } from "./sdkClient.js";
import { errorStatus } from "./sdkErrors.js";
import { isNativeApp } from "./native.js";

// =============================================================================
// H1: Sequence Number Tracking
// =============================================================================

/**
 * Update lastSeenSeq if the new seq is higher than the current stored value.
 * This ensures we track the highest seq seen during streaming, so reconnection
 * sync requests are up-to-date even if the client disconnects mid-stream.
 *
 * @param {string} sessionId - The session ID
 * @param {number|undefined} seq - The sequence number from the event
 */
export function updateLastSeenSeqIfHigher(sessionId, seq) {
  if (!seq || seq <= 0) return;
  const currentSeq = getLastSeenSeq(sessionId);
  if (seq > currentSeq) {
    setLastSeenSeq(sessionId, seq);
  }
}

// =============================================================================
// M1: Client-Side Deduplication
// =============================================================================

// Maximum number of recent seqs to track per session
// This prevents unbounded memory growth while still catching duplicates
const MAX_RECENT_SEQS = 100;

/**
 * Create a new seq tracker for a session.
 * @returns {{highestSeq: number, recentSeqs: Set<number>}}
 */
export function createSeqTracker() {
  return { highestSeq: 0, recentSeqs: new Set() };
}

/**
 * Check if a sequence number has already been seen for a session.
 * Returns true if this is a duplicate that should be skipped.
 * For coalescing events (same seq as last message), returns false to allow appending.
 *
 * @param {{highestSeq: number, recentSeqs: Set<number>}} tracker - The seq tracker
 * @param {number} seq - The sequence number to check
 * @param {number|undefined} lastMessageSeq - The seq of the last message (for coalescing)
 * @returns {boolean} True if this is a duplicate that should be skipped
 */
export function isSeqDuplicate(tracker, seq, lastMessageSeq) {
  if (!seq || seq <= 0) return false; // No seq = can't deduplicate

  // Allow same seq as last message (coalescing/continuation)
  if (lastMessageSeq && seq === lastMessageSeq) return false;

  // Check if we've seen this seq before
  if (tracker.recentSeqs.has(seq)) {
    return true;
  }

  // Quick check: if seq is much lower than highest, it's likely a duplicate
  if (seq < tracker.highestSeq - MAX_RECENT_SEQS) {
    return true;
  }

  return false;
}

/**
 * Mark a sequence number as seen.
 * Should be called after successfully processing an event.
 *
 * @param {{highestSeq: number, recentSeqs: Set<number>}} tracker - The seq tracker
 * @param {number} seq - The sequence number to mark as seen
 */
export function markSeqSeen(tracker, seq) {
  if (!seq || seq <= 0) return;

  // Add to recent seqs
  tracker.recentSeqs.add(seq);

  // Update highest seq
  if (seq > tracker.highestSeq) {
    tracker.highestSeq = seq;
  }

  // Prune old seqs to prevent unbounded growth
  if (tracker.recentSeqs.size > MAX_RECENT_SEQS) {
    const minSeq = tracker.highestSeq - MAX_RECENT_SEQS;
    for (const s of tracker.recentSeqs) {
      if (s < minSeq) {
        tracker.recentSeqs.delete(s);
      }
    }
  }
}

// =============================================================================
// M2: Exponential Backoff
// =============================================================================

// Exponential backoff configuration for WebSocket reconnection
// Prevents thundering herd when server restarts
const RECONNECT_BASE_DELAY_MS = 1000; // Initial delay: 1 second
const RECONNECT_MAX_DELAY_MS = 30000; // Maximum delay: 30 seconds
const RECONNECT_JITTER_FACTOR = 0.3; // Add up to 30% random jitter

/**
 * Calculate reconnection delay with exponential backoff and jitter.
 * @param {number} attempt - The attempt number (0-based)
 * @param {object} options - Optional configuration overrides for testing
 * @param {number} options.baseDelay - Base delay in ms (default: 1000)
 * @param {number} options.maxDelay - Max delay in ms (default: 30000)
 * @param {number} options.jitterFactor - Jitter factor 0-1 (default: 0.3)
 * @param {function} options.random - Random function for testing (default: Math.random)
 * @returns {number} Delay in milliseconds
 */
export function calculateReconnectDelay(attempt, options = {}) {
  const baseDelay = options.baseDelay ?? RECONNECT_BASE_DELAY_MS;
  const maxDelay = options.maxDelay ?? RECONNECT_MAX_DELAY_MS;
  const jitterFactor = options.jitterFactor ?? RECONNECT_JITTER_FACTOR;
  const random = options.random ?? Math.random;

  // Exponential backoff: base * 2^attempt, capped at max
  const exponentialDelay = Math.min(baseDelay * Math.pow(2, attempt), maxDelay);

  // Add jitter to prevent thundering herd
  const jitter = exponentialDelay * jitterFactor * random();

  return Math.floor(exponentialDelay + jitter);
}

// Exponential backoff configuration for session creation failures
// Higher base delay than reconnect (2s vs 1s) since each failed POST blocks an ACP queue slot
const SESSION_CREATION_BASE_DELAY_MS = 2000; // Initial delay: 2 seconds
const SESSION_CREATION_MAX_DELAY_MS = 30000; // Maximum delay: 30 seconds
const SESSION_CREATION_JITTER_FACTOR = 0.3; // Add up to 30% random jitter

/**
 * Calculate session creation retry delay with exponential backoff and jitter.
 * Uses the same algorithm as calculateReconnectDelay but with a higher base delay
 * to account for the heavier cost of each failed session creation request.
 * @param {number} attempt - The attempt number (0-based)
 * @param {object} options - Optional configuration overrides for testing
 * @param {number} options.baseDelay - Base delay in ms (default: 2000)
 * @param {number} options.maxDelay - Max delay in ms (default: 30000)
 * @param {number} options.jitterFactor - Jitter factor 0-1 (default: 0.3)
 * @param {function} options.random - Random function for testing (default: Math.random)
 * @returns {number} Delay in milliseconds
 */
export function calculateSessionCreationDelay(attempt, options = {}) {
  const baseDelay = options.baseDelay ?? SESSION_CREATION_BASE_DELAY_MS;
  const maxDelay = options.maxDelay ?? SESSION_CREATION_MAX_DELAY_MS;
  const jitterFactor = options.jitterFactor ?? SESSION_CREATION_JITTER_FACTOR;
  const random = options.random ?? Math.random;

  // Exponential backoff: base * 2^attempt, capped at max
  const exponentialDelay = Math.min(baseDelay * Math.pow(2, attempt), maxDelay);

  // Add jitter to prevent thundering herd
  const jitter = exponentialDelay * jitterFactor * random();

  return Math.floor(exponentialDelay + jitter);
}

// =============================================================================
// Reconnect Debounce
// =============================================================================

// Default debounce window for force-reconnect (ms)
// 3s window collapses multi-source triggers (visibility change, keepalive miss,
// native app activate) that can fire 1–6s apart into a single reconnect.
const RECONNECT_DEBOUNCE_MS = 3000;

// App-activate resync debounce (ms). macOS fires "App became active" in rapid bursts;
// collapse reactivations within this window into a single resync (bead mitto-c2p8.3).
export const APP_ACTIVATE_RESYNC_DEBOUNCE_MS = 15000;

// Maximum number of consecutive reconnect attempts before giving up on a session.
// After this many failures, the client assumes the session is permanently gone
// and stops retrying to prevent error storms (see: "Session not found" error storm).
// At 30s max backoff, 15 attempts ≈ ~3.5 minutes of retrying before giving up.
const MAX_SESSION_RECONNECT_ATTEMPTS = 15;

/**
 * Create a per-session reconnect debounce tracker.
 * Returns an object that can be passed to shouldDebounceReconnect.
 * @returns {Object} tracker - { timestamps: {} }
 */
export function createReconnectDebounceTracker() {
  return { timestamps: {} };
}

/**
 * Check whether a force-reconnect for the given session should be debounced
 * (skipped). If the same session was reconnected within `windowMs` ago, returns
 * true (skip). Otherwise records the current time and returns false (proceed).
 *
 * This implements a leading-edge debounce: the first call goes through
 * immediately; subsequent calls within the window are suppressed.
 *
 * @param {Object} tracker - Created by createReconnectDebounceTracker()
 * @param {string} sessionId - The session to check
 * @param {object} [options] - Optional overrides for testing
 * @param {number} [options.windowMs] - Debounce window (default: 500)
 * @param {function} [options.now] - Clock function (default: Date.now)
 * @returns {{ debounced: boolean, elapsed: number }} debounced=true means skip
 */
export function shouldDebounceReconnect(tracker, sessionId, options = {}) {
  const windowMs = options.windowMs ?? RECONNECT_DEBOUNCE_MS;
  const now = (options.now ?? Date.now)();
  const lastTime = tracker.timestamps[sessionId] || 0;
  const elapsed = now - lastTime;

  if (lastTime > 0 && elapsed < windowMs) {
    return { debounced: true, elapsed };
  }

  tracker.timestamps[sessionId] = now;
  return { debounced: false, elapsed };
}

// =============================================================================
// Reconnection Seq Watermark
// =============================================================================

/**
 * Check whether the reconnect attempt count has exceeded the maximum allowed.
 *
 * @param {number} attempt - Current attempt number (0-based)
 * @param {Object} [options]
 * @param {number} [options.maxAttempts] - Override max attempts (default: MAX_SESSION_RECONNECT_ATTEMPTS)
 * @returns {boolean} true if the limit has been reached
 */
export function isReconnectLimitReached(attempt, options = {}) {
  const max = options.maxAttempts ?? MAX_SESSION_RECONNECT_ATTEMPTS;
  return attempt >= max;
}

// =============================================================================

/**
 * Create a per-session seq watermark tracker for reconnection.
 * This tracks the highest received sequence number per session so that
 * ws.onopen can always send the correct after_seq on reconnection,
 * even when React state (messages array) is empty.
 *
 * @returns {Object} tracker - { [sessionId]: number }
 */
export function createSeqWatermark() {
  return {};
}

/**
 * Update the seq watermark for a session if the new seq is higher.
 * Returns true if the watermark was updated.
 *
 * @param {Object} watermark - Created by createSeqWatermark()
 * @param {string} sessionId - The session ID
 * @param {number|undefined|null} seq - The sequence number
 * @returns {boolean} True if watermark was updated
 */
export function updateSeqWatermark(watermark, sessionId, seq) {
  if (!seq || seq <= 0) return false;
  if (seq > (watermark[sessionId] || 0)) {
    watermark[sessionId] = seq;
    return true;
  }
  return false;
}

/**
 * Get the watermark value for a session.
 *
 * @param {Object} watermark - Created by createSeqWatermark()
 * @param {string} sessionId - The session ID
 * @returns {number} The highest known seq, or 0
 */
export function getSeqWatermark(watermark, sessionId) {
  return watermark[sessionId] || 0;
}

/**
 * Clear the watermark for a session (e.g., on deletion or stale client reset).
 *
 * @param {Object} watermark - Created by createSeqWatermark()
 * @param {string} sessionId - The session ID
 */
export function clearSeqWatermark(watermark, sessionId) {
  delete watermark[sessionId];
}

// =============================================================================
// Circuit Breaker: Terminal Session Error Detection
// =============================================================================

/**
 * Check if a server error message indicates the session is permanently gone
 * and reconnection should stop immediately.
 *
 * This is used as defense-in-depth alongside the explicit `session_gone`
 * message type. It catches "Session not found" errors from older servers
 * that don't yet send the `session_gone` message.
 *
 * @param {string} message - The error message from the server
 * @returns {boolean} True if this is a terminal "session gone" error
 */
export function isTerminalSessionError(message) {
  if (!message) return false;
  const lower = message.toLowerCase();
  return (
    lower.includes("session not found") ||
    lower.includes("session is closed") ||
    lower.includes("session not running")
  );
}

// =============================================================================
// Singleton Find-or-Route: Reused Conversation Detection
// =============================================================================

/**
 * Whether a POST /api/sessions response indicates the backend routed the
 * request to an EXISTING conversation (singleton find-or-route, mitto-4mb).
 * When true, the client must NOT seed placeholder session state — doing so
 * would clobber the already-loaded conversation and flash "Start chatting
 * with undefined". Only a strict boolean `true` counts. (mitto-4mb.10)
 *
 * @param {object} data - The create-session response body.
 * @returns {boolean}
 */
export function isReusedConversationResponse(data) {
  return data?.reused === true;
}

// Export constants for testing
export const WEBSOCKET_CONSTANTS = {
  MAX_RECENT_SEQS,
  RECONNECT_BASE_DELAY_MS,
  RECONNECT_MAX_DELAY_MS,
  RECONNECT_JITTER_FACTOR,
  RECONNECT_DEBOUNCE_MS,
  MAX_SESSION_RECONNECT_ATTEMPTS,
  SESSION_CREATION_BASE_DELAY_MS,
  SESSION_CREATION_MAX_DELAY_MS,
  SESSION_CREATION_JITTER_FACTOR,
  APP_ACTIVATE_RESYNC_DEBOUNCE_MS,
};

// =============================================================================
// Auth Check Helpers
// =============================================================================

// In-flight Promise for auth-check deduplication.
// When several WebSocket sessions reconnect simultaneously they each call
// checkAuthOrRedirect().  Without deduplication every session fires its own
// raw GET /api/config, creating a mini fetching storm on every reconnect event.
// Sharing a single in-flight Promise collapses N concurrent calls into one
// HTTP round-trip.  The Promise resolves to { status, ok } (plain values, not
// the Response object) so it can safely be awaited by multiple callers.
let _authCheckInflight = null;

/**
 * Quick authentication check before WebSocket reconnection.
 * If auth is invalid (401), redirects to login page and never returns.
 * For network errors or server errors, returns true to allow reconnection to proceed
 * (the WebSocket reconnect will handle retries with exponential backoff).
 *
 * Concurrent callers share a single in-flight HTTP request to avoid a fetch
 * storm when multiple sessions reconnect at the same time.
 *
 * @returns {Promise<boolean>} Always returns true. On 401, redirects and never returns.
 *                             Network/server errors also return true to allow reconnection.
 */
export async function checkAuthOrRedirect() {
  // Deduplicate: if an auth check is already in-flight, share that Promise
  // rather than firing a fresh HTTP request for each concurrent caller.
  if (!_authCheckInflight) {
    // getSdkClient() sends credentials via its browser-cookie auth adapter and
    // routes 401s through its onUnauthorized hook → redirectToLogin() (see
    // sdkClient.js). The SDK throws on any non-2xx instead of returning a
    // Response, so the .catch below normalizes back to a {status, ok} shape.
    // A 401 resolves to a never-resolving promise instead of {status: 401}
    // (matching authFetch's old handleUnauthorized, which also never resolved
    // on 401) so the `if (status === 401)` branch below stays unreachable —
    // exactly as unreachable as it was pre-migration. A network error
    // (no HTTP status, e.g. MittoNetworkError) is rethrown so it still
    // rejects `_authCheckInflight`, matching authFetch's fetch()-throws path.
    _authCheckInflight = getSdkClient()
      .serverConfig.get()
      .then(() => ({ status: 200, ok: true }))
      .catch((err) => {
        const status = errorStatus(err);
        if (status === undefined) throw err;
        if (status === 401) return new Promise(() => {});
        return { status, ok: false };
      })
      .finally(() => {
        _authCheckInflight = null;
      });
  }
  try {
    const { status, ok } = await _authCheckInflight;

    if (status === 401) {
      // Session expired — redirect to login and stall forever.
      console.warn("Session expired or invalid, redirecting to login...");
      redirectToLogin();
      return new Promise(() => {});
    }
    // If we got here, auth is valid (200) or there's a server error (5xx).
    // Either way, let reconnection proceed — the WebSocket will retry with backoff.
    if (!ok) {
      console.warn(
        `Auth check returned status ${status}, allowing reconnection to proceed`,
      );
    }
    return true;
  } catch (err) {
    // Network error - let reconnection proceed.
    // The WebSocket reconnection will naturally retry with exponential backoff.
    console.warn(
      "Auth check network error, allowing reconnection to proceed:",
      err.message,
    );
    return true;
  }
}

/**
 * Check authentication with retry logic for network errors.
 * After prolonged phone sleep, the network may take a moment to recover.
 * This function retries a few times before giving up.
 *
 * @param {number} maxRetries - Maximum number of retries (default: 3)
 * @param {number} retryDelay - Delay between retries in ms (default: 500)
 * @returns {Promise<{authenticated: boolean, networkError: boolean}>}
 *   - authenticated: true if the session is valid
 *   - networkError: true if all retries failed due to network errors
 */
export async function checkAuthWithRetry(maxRetries = 3, retryDelay = 500) {
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    try {
      // getSdkClient() sends credentials via its browser-cookie auth adapter
      // and routes 401s through its onUnauthorized hook → redirectToLogin()
      // (see sdkClient.js). Unlike authFetch it throws instead of returning a
      // Response, so success/failure is now branched via try/catch below.
      await getSdkClient().serverConfig.get();
      return { authenticated: true, networkError: false };
    } catch (err) {
      const status = errorStatus(err);

      // authFetch's old handleUnauthorized never resolved on 401 (it
      // returned a never-resolving promise after redirecting), so this
      // branch was unreachable dead code even pre-migration — kept
      // unreachable here too: onUnauthorized already redirected via the
      // sdkClient.js wiring, so stall forever instead of returning.
      if (status === 401) {
        console.log(
          "Auth check: session expired or invalid (401), redirecting to login",
        );
        redirectToLogin();
        return new Promise(() => {});
      }

      if (status !== undefined) {
        // Other error status - treat as auth failure if persistent
        console.warn(`Auth check returned status ${status}`);
      } else {
        // Network error - retry if we have attempts left
        console.warn(
          `Auth check network error (attempt ${attempt + 1}/${maxRetries + 1}):`,
          err.message,
        );
      }
      if (attempt < maxRetries) {
        await new Promise((r) => setTimeout(r, retryDelay));
        continue;
      }
      // All retries exhausted
      return { authenticated: false, networkError: status === undefined };
    }
  }
  // Should not reach here
  return { authenticated: false, networkError: true };
}

// =============================================================================
// Keepalive / Reconnect / Stagger Constants
// =============================================================================

// Time threshold (in ms) for considering the session potentially stale
// If the page has been hidden for longer than this, we do an explicit auth check
// before trying to reconnect. The server session expires after 24 hours.
export const STALE_THRESHOLD_MS = 60 * 60 * 1000; // 1 hour

// Keepalive configuration for detecting zombie WebSocket connections and sequence sync
// On mobile, connections can appear open but be dead (zombie connections)
// Keepalive also piggybacks sequence numbers to detect out-of-sync situations
// Native macOS app uses shorter interval (5s) since it's local with no network latency
// Browser uses longer interval (10s) to reduce network overhead
export const KEEPALIVE_INTERVAL_NATIVE_MS = 5000; // Send keepalive every 5 seconds in native app
export const KEEPALIVE_INTERVAL_BROWSER_MS = 10000; // Send keepalive every 10 seconds in browser
export const KEEPALIVE_TIMEOUT_MS = 10000; // Consider connection unhealthy if no response in 10 seconds
// Cooldown period after stale client recovery. During this window, keepalive
// will not re-trigger stale detection for the session, giving React state
// and the auto-load prepend time to settle.
export const STALE_RECOVERY_COOLDOWN_MS = 30000; // 30 seconds
export const KEEPALIVE_MAX_MISSED_DEFAULT = 2; // Force reconnect after 2 missed keepalives
export const KEEPALIVE_MAX_MISSED_LARGE_SESSION = 4; // For sessions with 500+ events
export const LARGE_SESSION_SEQ_THRESHOLD = 500;

// Sync tolerance: only request sync if client is more than N sequences behind server.
// This avoids excessive sync requests during normal streaming where the markdown buffer
// may hold content briefly before flushing to the UI. A tolerance of 2 prevents
// sync requests when client is just 1-2 behind due to normal buffering delays.
// NOTE: This tolerance is only applied during active streaming. For non-streaming sessions,
// tolerance is 0 to ensure immediate sync of final events like session_end.
export const KEEPALIVE_SYNC_TOLERANCE = 2;

// Startup stagger: delay between each background session's WebSocket connection at startup/wake.
// Prevents thundering herd where all sessions send load_events simultaneously,
// overwhelming the server with concurrent large event replays.
// Active session always connects first with no delay; background sessions stagger by this amount.
export const STARTUP_STAGGER_MS = 300;

// Debounce window for reconnectAllSessionsStaggered (ms).
// Multiple macOS activation sources (NSWorkspaceDidWakeNotification,
// NSWorkspaceScreensDidWakeNotification, applicationDidBecomeActive) can fire
// 4–10 seconds apart for the same wake/focus event.  In addition, the native
// "App became active" callback and the WKWebView visibilitychange "App became
// visible" event are distinct triggers that both funnel here ~6 s apart for a
// single wake.  Collapsing these into a single staggered reconnect prevents a
// redundant active-session force-reconnect (which tears down a freshly opened
// WebSocket) and avoids duplicate background-session timers accumulating
// observers on BackgroundSession.  Matches APP_ACTIVATE_RESYNC_DEBOUNCE_MS
// (one resync per wake) so the active+visible pair coalesces into one reconnect.
export const STAGGERED_RECONNECT_DEBOUNCE_MS = 15000;

// Grace period (ms) before a background session's per-session WebSocket is
// disconnected after it stops being the active session. Lazy-connect keeps only
// the active session connected; releasing a background WebSocket removes its
// server-side observer so the backend GC can reclaim the idle ACP process.
// The grace window keeps rapid back-and-forth switching cheap (the connection is
// reused if the user returns within the window). The disconnect timer resets on
// every active-session change, so only sessions left untouched for the full
// window are dropped.
export const BACKGROUND_DISCONNECT_GRACE_MS = 30000;

/**
 * Get the appropriate keepalive interval based on the runtime environment.
 * Native macOS app uses a shorter interval for faster sync detection.
 * @returns {number} Keepalive interval in milliseconds
 */
export function getKeepaliveInterval() {
  return isNativeApp()
    ? KEEPALIVE_INTERVAL_NATIVE_MS
    : KEEPALIVE_INTERVAL_BROWSER_MS;
}
