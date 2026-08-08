/**
 * SessionStream — transport-only WebSocket wrapper for a single session
 * (docs/devel/js-client-library.md §3, realtime/ area). Ported from
 * web/static/hooks/useWSConnection.js + utils/websocket.js, stripped of every
 * Preact/DOM/localStorage/console dependency and re-expressed against the
 * injected `config` from sdk/core/config.js (§4). Never imports window,
 * document, localStorage, native.js or bare console — see the purity test.
 *
 * Scope: connect/reconnect, exponential backoff + jitter, reconnect debounce,
 * keepalive + missed-ack zombie detection, send/sendWhenConnected, seq
 * watermark + non-destructive dedup, stale-client + gap-fill sync, a
 * session-gone/terminal-error circuit breaker, and sendPrompt's delivery
 * verification (ported from useWSSeqSync.js + useWSDeliveryVerification.js,
 * see .14 and .augment/rules/24-web-frontend-sync.md). It emits raw parsed
 * protocol messages and interprets none of their payloads beyond the seq/
 * control-message bookkeeping documented below.
 *
 * Deliberately out of scope (left to later issues / host policy):
 *   - the global /api/events socket (.15)
 *   - typed event name constants (.16)
 *   - multi-session fan-out, staggering, background-disconnect grace,
 *     window.__debug, redirect-to-login on 401 (.17/.18, host/UI concerns)
 */
import { ConfigError, MittoNetworkError } from "../core/errors.js";
import {
  createSeqTracker,
  isSeqDuplicate,
  markSeqSeen,
  isStaleClientState,
  isTerminalSessionError,
  createMemorySeqStore,
} from "./seq.js";
import { generatePromptId, createMemoryPendingPromptStore } from "./pending-prompts.js";

const RECONNECT_BASE_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 30000;
const RECONNECT_JITTER_FACTOR = 0.3;
const RECONNECT_DEBOUNCE_MS = 3000;
const MAX_RECONNECT_ATTEMPTS = 15;
const KEEPALIVE_INTERVAL_MS = 10000;
const KEEPALIVE_MAX_MISSED_DEFAULT = 2;
const KEEPALIVE_MAX_MISSED_LARGE_SESSION = 4;
const LARGE_SESSION_SEQ_THRESHOLD = 500;
const SEND_WHEN_CONNECTED_TIMEOUT_MS = 5000;
// Cooldown after triggering a stale-state recovery, so React-state-less hosts
// don't re-trigger it on every subsequent keepalive_ack while the requested
// load_events response is still in flight.
const STALE_RECOVERY_COOLDOWN_MS = 30000;
// Behind-tolerance applied only while the server reports the session is
// actively streaming (is_prompting); the gap closes naturally as the stream
// completes. Non-streaming sessions get 0 tolerance so session_end and other
// final events sync immediately.
const KEEPALIVE_SYNC_TOLERANCE = 2;
// Debounce window for the immediate (non-keepalive) gap-fill check.
const GAP_FILL_DEBOUNCE_MS = 500;
// Number of most-recent events requested on a full stale-state reload.
const INITIAL_EVENTS_LIMIT = 50;
// Number of events requested per gap-fill load_events (after_seq mode).
const GAP_FILL_LIMIT = 100;
// sendPrompt() delivery-verification budget (see useWSDeliveryVerification.js).
const TOTAL_DELIVERY_BUDGET_MS = 10000;
const INITIAL_ACK_TIMEOUT_MS = 3000;
const RECONNECT_VERIFY_TIMEOUT_MS = 4000;
// Auto-clear window for an in-flight sync (load_events) request. If
// events_loaded never arrives (server error, zombie connection), the flag is
// cleared and a reconnect is forced rather than waiting for keepalive
// miss-counting to eventually notice.
const SYNC_TIMEOUT_MS = 30000;

/** Internal-only marker so sendPrompt() can distinguish an ACK timeout from a send failure. */
class AckTimeoutError extends Error {}

/** Exponential backoff with jitter. Exported for deterministic unit tests. */
export function calculateReconnectDelay(attempt, options = {}) {
  const baseDelay = options.baseDelay ?? RECONNECT_BASE_DELAY_MS;
  const maxDelay = options.maxDelay ?? RECONNECT_MAX_DELAY_MS;
  const jitterFactor = options.jitterFactor ?? RECONNECT_JITTER_FACTOR;
  const random = options.random ?? Math.random;
  const exponentialDelay = Math.min(baseDelay * Math.pow(2, attempt), maxDelay);
  const jitter = exponentialDelay * jitterFactor * random();
  return Math.floor(exponentialDelay + jitter);
}

/**
 * Builds the session WebSocket URL from injected config. Unlike
 * utils/api.js's `wsUrl`, this never reads `window.location` — an absolute
 * `config.baseUrl` maps its http(s) scheme to ws(s); a relative/empty
 * `baseUrl` requires an explicit `options.wsBaseUrl` (e.g. "ws://host:1234").
 * TODO(mitto-7gta.6): once endpoints.js is the canonical URL registry, source
 * this from there instead of hand-building it here.
 */
function sessionWsUrl(config, sessionId, options) {
  const base = options.wsBaseUrl ?? config.baseUrl;
  if (!base) {
    throw new ConfigError(
      "SessionStream: cannot derive a WebSocket URL from an empty baseUrl; " +
        "pass options.wsBaseUrl explicitly (e.g. 'ws://host:1234').",
    );
  }
  let wsBase;
  if (/^https:\/\//i.test(base)) {
    wsBase = base.replace(/^https:\/\//i, "wss://");
  } else if (/^http:\/\//i.test(base)) {
    wsBase = base.replace(/^http:\/\//i, "ws://");
  } else if (/^wss?:\/\//i.test(base)) {
    wsBase = base;
  } else {
    throw new ConfigError(
      `SessionStream: unrecognized baseUrl scheme "${base}"; expected an ` +
        "absolute http(s):// or ws(s):// URL.",
    );
  }
  const prefix = config.apiPrefix || "";
  return `${wsBase}${prefix}/api/sessions/${encodeURIComponent(sessionId)}/ws`;
}

/** Minimal zero-dependency emitter. No DOM EventTarget (§4: no DOM). */
function createEmitter() {
  const handlers = new Map();
  return {
    on(event, handler) {
      if (!handlers.has(event)) handlers.set(event, new Set());
      handlers.get(event).add(handler);
      return () => handlers.get(event)?.delete(handler);
    },
    once(event, handler) {
      const off = this.on(event, (...args) => {
        off();
        handler(...args);
      });
      return off;
    },
    emit(event, ...args) {
      for (const handler of handlers.get(event) || []) {
        handler(...args);
      }
    },
  };
}

export const SESSION_STREAM_CONSTANTS = {
  RECONNECT_BASE_DELAY_MS,
  RECONNECT_MAX_DELAY_MS,
  RECONNECT_JITTER_FACTOR,
  RECONNECT_DEBOUNCE_MS,
  MAX_RECONNECT_ATTEMPTS,
  KEEPALIVE_INTERVAL_MS,
  KEEPALIVE_MAX_MISSED_DEFAULT,
  KEEPALIVE_MAX_MISSED_LARGE_SESSION,
  LARGE_SESSION_SEQ_THRESHOLD,
  SEND_WHEN_CONNECTED_TIMEOUT_MS,
  STALE_RECOVERY_COOLDOWN_MS,
  KEEPALIVE_SYNC_TOLERANCE,
  GAP_FILL_DEBOUNCE_MS,
  GAP_FILL_LIMIT,
  INITIAL_EVENTS_LIMIT,
  TOTAL_DELIVERY_BUDGET_MS,
  INITIAL_ACK_TIMEOUT_MS,
  RECONNECT_VERIFY_TIMEOUT_MS,
  SYNC_TIMEOUT_MS,
};

/**
 * Single-session WebSocket transport. One instance per session — multi-
 * session fan-out is host policy, not this class's concern.
 *
 * States: "idle" -> "connecting" -> "open" -> "closing"/"closed", plus the
 * terminal "stopped" reached via explicit close() or the reconnect-attempt
 * cap. Explicit close() never schedules a reconnect.
 *
 * Events (via `.on(event, handler) -> unsubscribe`):
 *   "open", "message" (raw parsed protocol message object), "close",
 *   "error", "reconnecting" ({ attempt, delayMs }), "health" ({ healthy }).
 */
export class SessionStream {
  constructor(config, sessionId, options = {}) {
    this._config = config;
    this._sessionId = sessionId;
    this._now = options.now ?? Date.now;
    this._setTimeout = options.setTimeout ?? setTimeout;
    this._clearTimeout = options.clearTimeout ?? clearTimeout;
    this._setInterval = options.setInterval ?? setInterval;
    this._clearInterval = options.clearInterval ?? clearInterval;
    this._random = options.random ?? Math.random;
    this._wsBaseUrl = options.wsBaseUrl;
    this._seqStore = options.seqStore ?? createMemorySeqStore();
    this._isSyncInFlight = options.isSyncInFlight ?? (() => false);
    this._getClientMaxSeq = options.getClientMaxSeq ?? (() => 0);
    this._dropDuplicates = options.dropDuplicates ?? false;
    this._keepaliveIntervalMs = options.keepaliveIntervalMs ?? KEEPALIVE_INTERVAL_MS;
    this._maxReconnectAttempts = options.maxReconnectAttempts ?? MAX_RECONNECT_ATTEMPTS;
    this._reconnectDebounceMs = options.reconnectDebounceMs ?? RECONNECT_DEBOUNCE_MS;
    this._staleRecoveryCooldownMs = options.staleRecoveryCooldownMs ?? STALE_RECOVERY_COOLDOWN_MS;
    this._keepaliveSyncTolerance = options.keepaliveSyncTolerance ?? KEEPALIVE_SYNC_TOLERANCE;
    this._gapFillDebounceMs = options.gapFillDebounceMs ?? GAP_FILL_DEBOUNCE_MS;
    this._initialEventsLimit = options.initialEventsLimit ?? INITIAL_EVENTS_LIMIT;
    this._pendingPromptStore = options.pendingPromptStore ?? createMemoryPendingPromptStore();
    this._totalDeliveryBudgetMs = options.totalDeliveryBudgetMs ?? TOTAL_DELIVERY_BUDGET_MS;
    this._initialAckTimeoutMs = options.initialAckTimeoutMs ?? INITIAL_ACK_TIMEOUT_MS;
    this._reconnectVerifyTimeoutMs = options.reconnectVerifyTimeoutMs ?? RECONNECT_VERIFY_TIMEOUT_MS;
    this._syncTimeoutMs = options.syncTimeoutMs ?? SYNC_TIMEOUT_MS;
    this._backoffOptions = {
      baseDelay: options.reconnectBaseDelayMs,
      maxDelay: options.reconnectMaxDelayMs,
      jitterFactor: options.reconnectJitterFactor,
      random: this._random,
    };

    this._emitter = createEmitter();
    this._ws = null;
    this._state = "idle";
    this._explicitlyClosed = false;
    this._terminal = false;
    this._reconnectAttempt = 0;
    this._reconnectTimer = null;
    this._lastReconnectAt = 0;
    this._keepaliveTimer = null;
    this._keepalive = { pendingKeepalive: false, missedCount: 0, lastAckTime: 0 };

    this._seqTracker = createSeqTracker();
    this._syncInFlightInternal = false;
    this._syncTimeoutTimer = null;
    this._lastStaleRecoveryAt = 0;
    this._gapFillTimer = null;
    this._gapFillScheduledAt = 0;
    this._pendingSends = new Map();
    this._lastConfirmedPrompt = null;
  }

  get state() {
    return this._state;
  }

  on(event, handler) {
    return this._emitter.on(event, handler);
  }

  once(event, handler) {
    return this._emitter.once(event, handler);
  }

  /** Highest seq this stream knows about for its session, per the injected seqStore. */
  lastSeenSeq() {
    return this._seqStore.get(this._sessionId);
  }

  /** The { promptId, seq } the server last confirmed as delivered, or null. */
  lastConfirmedPrompt() {
    return this._lastConfirmedPrompt;
  }

  /**
   * Clears the seq tracker (dedup state) and resets the seq watermark to 0.
   * Call this before requesting a full reload after detecting stale client
   * state (rule 24: the reset pair must happen together, before the
   * load_events response is processed).
   */
  resetSync() {
    this._seqTracker = createSeqTracker();
    if (typeof this._seqStore.reset === "function") {
      this._seqStore.reset(this._sessionId);
    } else {
      this._config.logger.warn(
        "SessionStream: injected seqStore has no reset(); stale-state watermark could not be cleared",
      );
    }
  }

  /**
   * Health check mirroring isConnectionHealthy(): acked within 2x the
   * keepalive interval AND no outstanding misses. Emits no event itself;
   * callers poll this, or listen for the "health" event on transitions.
   */
  isHealthy() {
    if (this._state !== "open") return false;
    const sinceAck = this._now() - (this._keepalive.lastAckTime || 0);
    return sinceAck < this._keepaliveIntervalMs * 2 && (this._keepalive.missedCount || 0) === 0;
  }

  /** Opens the connection if not already open/connecting. No-op otherwise. */
  connect() {
    if (this._state === "connecting" || this._state === "open") return;
    this._explicitlyClosed = false;
    this._doConnect();
  }

  _doConnect() {
    this._state = "connecting";
    const WebSocketImpl = this._config.getWebSocket();
    const url = sessionWsUrl(this._config, this._sessionId, { wsBaseUrl: this._wsBaseUrl });

    // WebSocket auth (mitto-7gta.5): browsers cannot set custom headers on
    // the handshake, so a browser-facing adapter (noneAuth, browserCookieAuth)
    // simply omits `authorizeWebSocket` and the socket opens synchronously,
    // exactly as before. Non-browser WebSocket implementations (e.g. Node's
    // `ws`, which honours a `{ headers }` third constructor argument) get it
    // via `sharedTokenAuth.authorizeWebSocket()` — the only case that defers
    // socket creation by a microtask. No query-param fallback is used or
    // invented here — see auth/shared-token.js.
    const authorizeWebSocket = this._config.auth.authorizeWebSocket;
    if (typeof authorizeWebSocket !== "function") {
      this._openSocket(WebSocketImpl, url, {});
      return;
    }
    Promise.resolve(authorizeWebSocket.call(this._config.auth, { url })).then(
      (wsAuth) => {
        if (this._state !== "connecting") return; // superseded while awaiting
        this._openSocket(WebSocketImpl, url, wsAuth || {});
      },
      (err) => {
        this._config.logger.error("SessionStream: authorizeWebSocket failed", err);
        if (this._state !== "connecting") return;
        this._emitter.emit("error", err);
        this._handleConnectFailure();
      },
    );
  }

  _openSocket(WebSocketImpl, url, wsAuth) {
    const ws = new WebSocketImpl(url, wsAuth.protocols, wsAuth.options);
    this._ws = ws;

    ws.onopen = () => this._handleOpen(ws);
    ws.onmessage = (event) => this._handleMessage(event);
    ws.onclose = (event) => this._handleClose(ws, event);
    ws.onerror = (event) => {
      this._emitter.emit("error", event);
      this._config.logger.warn("SessionStream: WebSocket error", event);
    };
  }

  _handleOpen(ws) {
    if (this._ws !== ws) return;
    this._state = "open";
    this._reconnectAttempt = 0;
    this._keepalive = { pendingKeepalive: false, missedCount: 0, lastAckTime: this._now() };
    this._startKeepalive(ws);
    this._emitter.emit("open");
    this._emitter.emit("health", { healthy: true });
  }

  _handleMessage(event) {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch (err) {
      this._config.logger.error("SessionStream: failed to parse message", err, event.data);
      return;
    }

    if (msg.type === "keepalive_ack") {
      this._keepalive.pendingKeepalive = false;
      this._keepalive.lastAckTime = this._now();
      this._keepalive.missedCount = 0;
      this._emitter.emit("health", { healthy: true });
      this._handleKeepaliveAck(msg.data || {});
      return;
    }

    if (msg.type === "session_gone") {
      this._tripCircuitBreaker("session_gone", msg.data);
      return;
    }

    if (msg.type === "events_loaded") {
      this._clearSyncInFlight();
    }

    if (msg.type === "connected" && msg.data?.last_user_prompt_id) {
      this._lastConfirmedPrompt = {
        promptId: msg.data.last_user_prompt_id,
        seq: msg.data.last_user_prompt_seq || 0,
      };
    }

    if (msg.type === "prompt_received" && msg.data?.prompt_id) {
      this._resolvePendingSend(msg.data.prompt_id, { success: true, promptId: msg.data.prompt_id });
    }

    if (msg.type === "user_prompt" && msg.data?.is_mine && msg.data?.prompt_id) {
      this._resolvePendingSend(msg.data.prompt_id, { success: true, promptId: msg.data.prompt_id });
    }

    // Gap-fill check uses the watermark BEFORE this message updates it, so a
    // backward gap (client missed several events, this one is just the next
    // in line) is detected the same way a forward jump would be.
    const clientMaxSeqBefore = this.lastSeenSeq();
    const seq = typeof msg.data?.seq === "number" ? msg.data.seq : undefined;
    const maxSeq =
      typeof msg.data?.max_seq === "number"
        ? msg.data.max_seq
        : typeof msg.data?.server_max_seq === "number"
          ? msg.data.server_max_seq
          : undefined;

    if (maxSeq !== undefined) {
      this._checkAndFillGap(maxSeq, clientMaxSeqBefore);
    }

    let duplicate = false;
    if (seq !== undefined) {
      duplicate = isSeqDuplicate(this._seqTracker, seq);
      markSeqSeen(this._seqTracker, seq);
      this._seqStore.set(this._sessionId, seq);
    }
    if (maxSeq !== undefined) {
      this._seqStore.set(this._sessionId, maxSeq);
    }

    // Dedup is non-destructive by default: annotate and still emit "message"
    // so the host decides (rule 24 — dropping unconditionally here previously
    // caused loop user_prompt messages to be silently swallowed by a race
    // with events_loaded). options.dropDuplicates opts into the old behavior.
    if (duplicate && this._dropDuplicates) {
      this._emitter.emit("duplicate", msg, { seq, maxSeq });
    } else {
      this._emitter.emit("message", msg, { duplicate, seq, maxSeq });
    }

    if (msg.type === "error" && isTerminalSessionError(msg.data?.message)) {
      this._tripCircuitBreaker("terminal_error", msg.data);
    }
  }

  /**
   * Stale-client / behind-client detection on every keepalive_ack, folding in
   * the server's is_prompting flag as the "actively streaming" signal (the
   * same ack already carries it, so no host-injected isStreaming() is
   * needed). Ported from useWebSocket.js's keepalive_ack handler.
   */
  _handleKeepaliveAck(data) {
    const serverMaxSeq =
      typeof data.max_seq === "number"
        ? data.max_seq
        : typeof data.server_max_seq === "number"
          ? data.server_max_seq
          : 0;
    if (!serverMaxSeq || serverMaxSeq <= 0) return;

    const clientMaxSeq = Math.max(this.lastSeenSeq(), this._getClientMaxSeq() || 0);

    if (isStaleClientState(clientMaxSeq, serverMaxSeq)) {
      const now = this._now();
      if (this._lastStaleRecoveryAt && now - this._lastStaleRecoveryAt < this._staleRecoveryCooldownMs) {
        return;
      }
      if (this._syncInFlightInternal || this._isSyncInFlight()) return;

      this.resetSync();
      this._setSyncInFlight();
      this._lastStaleRecoveryAt = now;
      this._emitter.emit("stale", { clientMaxSeq, serverMaxSeq });
      this.send({ type: "load_events", data: { limit: this._initialEventsLimit } });
      return;
    }

    const isStreaming = !!data.is_prompting;
    const tolerance = isStreaming ? this._keepaliveSyncTolerance : 0;
    if (serverMaxSeq > clientMaxSeq + tolerance) {
      if (isStreaming) return; // Gap closes naturally as the stream completes.
      if (this._syncInFlightInternal || this._isSyncInFlight()) return;

      this._setSyncInFlight();
      this._emitter.emit("sync", { clientMaxSeq, serverMaxSeq });
      this.send({ type: "load_events", data: { after_seq: clientMaxSeq } });
    }
  }

  /**
   * Immediate (non-keepalive) gap detection: any message carrying max_seq can
   * reveal that the server has events beyond what this client has seen,
   * without waiting for the next keepalive round-trip. Debounced so a burst
   * of messages schedules at most one load_events.
   */
  _checkAndFillGap(maxSeq, clientMaxSeq) {
    if (!maxSeq || maxSeq <= 0) return;
    if (isStaleClientState(clientMaxSeq, maxSeq)) return; // handled by keepalive stale detection

    const gap = maxSeq - (clientMaxSeq + 1);
    if (gap <= 0) return;

    const now = this._now();
    if (this._gapFillTimer && now - this._gapFillScheduledAt < this._gapFillDebounceMs) return;
    if (this._gapFillTimer) this._clearTimeout(this._gapFillTimer);

    this._gapFillScheduledAt = now;
    this._gapFillTimer = this._setTimeout(() => {
      this._gapFillTimer = null;
      if (this._state !== "open") return;
      if (this._syncInFlightInternal || this._isSyncInFlight()) return;
      this._setSyncInFlight();
      this._emitter.emit("sync", { clientMaxSeq, serverMaxSeq: maxSeq, gapFill: true });
      this.send({ type: "load_events", data: { after_seq: clientMaxSeq, limit: GAP_FILL_LIMIT } });
    }, this._gapFillDebounceMs);
  }

  _setSyncInFlight() {
    if (this._syncTimeoutTimer) this._clearTimeout(this._syncTimeoutTimer);
    this._syncInFlightInternal = true;
    this._syncTimeoutTimer = this._setTimeout(() => {
      this._syncTimeoutTimer = null;
      if (!this._syncInFlightInternal) return;
      this._syncInFlightInternal = false;
      this._config.logger.warn("SessionStream: sync timed out waiting for events_loaded; forcing reconnect");
      this.forceReconnect();
    }, this._syncTimeoutMs);
  }

  _clearSyncInFlight() {
    if (this._syncTimeoutTimer) {
      this._clearTimeout(this._syncTimeoutTimer);
      this._syncTimeoutTimer = null;
    }
    this._syncInFlightInternal = false;
  }

  /**
   * Circuit breaker: an explicit `session_gone` message, or an error message
   * matching isTerminalSessionError(), means the session is permanently gone
   * — stop reconnecting immediately instead of retrying into a 404 loop.
   */
  _tripCircuitBreaker(reason, data) {
    if (this._terminal) return;
    this._terminal = true;
    if (this._reconnectTimer) {
      this._clearTimeout(this._reconnectTimer);
      this._reconnectTimer = null;
    }
    this._clearSyncInFlight();
    this._stopKeepalive();
    if (this._ws) {
      const ws = this._ws;
      this._ws = null;
      ws.close();
    }
    this._state = "stopped";
    this._emitter.emit("gone", { reason, data });
  }

  _handleClose(ws, event) {
    if (this._ws !== ws) return;
    this._ws = null;
    this._stopKeepalive();
    this._clearSyncInFlight();
    this._state = "closed";
    this._emitter.emit("close", event);
    this._reconnectOrStop();
  }

  /**
   * Reached when `authorizeWebSocket()` rejects before a socket was ever
   * created (no `ws`/`close` event to key off of) — applies the same
   * explicit-close / reconnect-limit / backoff decision as `_handleClose`.
   */
  _handleConnectFailure() {
    this._state = "closed";
    this._reconnectOrStop();
  }

  _reconnectOrStop() {
    if (this._explicitlyClosed || this._terminal) {
      this._state = "stopped";
      return;
    }
    if (isReconnectLimitReached(this._reconnectAttempt, { maxAttempts: this._maxReconnectAttempts })) {
      this._state = "stopped";
      this._emitter.emit("error", new MittoNetworkError("SessionStream: reconnect attempt limit reached"));
      return;
    }
    this._scheduleReconnect();
  }

  _scheduleReconnect() {
    if (this._reconnectTimer) return;
    const attempt = this._reconnectAttempt;
    const delayMs = calculateReconnectDelay(attempt, this._backoffOptions);
    this._emitter.emit("reconnecting", { attempt: attempt + 1, delayMs });
    this._reconnectTimer = this._setTimeout(() => {
      this._reconnectTimer = null;
      this._reconnectAttempt = attempt + 1;
      this._doConnect();
    }, delayMs);
  }

  /**
   * Force a reconnect, closing any existing connection first. Debounced
   * (leading-edge, `reconnectDebounceMs` window) to collapse bursts of
   * concurrent triggers into a single reconnect, mirroring
   * forceReconnectActiveSession's shouldDebounceReconnect.
   */
  forceReconnect() {
    const now = this._now();
    const elapsed = now - this._lastReconnectAt;
    if (this._lastReconnectAt > 0 && elapsed < this._reconnectDebounceMs) return;
    this._lastReconnectAt = now;

    if (this._reconnectTimer) {
      this._clearTimeout(this._reconnectTimer);
      this._reconnectTimer = null;
    }
    this._explicitlyClosed = false;
    if (this._ws) {
      const ws = this._ws;
      this._ws = null;
      this._stopKeepalive();
      ws.close();
    }
    this._doConnect();
  }

  _startKeepalive(ws) {
    this._stopKeepalive();
    this._keepaliveTimer = this._setInterval(() => {
      if (this._keepalive.pendingKeepalive) {
        if (this._syncInFlightInternal || this._isSyncInFlight()) {
          // Suppress miss-counting while a sync (load_events) is in flight.
        } else {
          this._keepalive.missedCount = (this._keepalive.missedCount || 0) + 1;
          const lastSeq = this.lastSeenSeq();
          const maxMissed =
            lastSeq > LARGE_SESSION_SEQ_THRESHOLD
              ? KEEPALIVE_MAX_MISSED_LARGE_SESSION
              : KEEPALIVE_MAX_MISSED_DEFAULT;
          if (this._keepalive.missedCount >= maxMissed) {
            this._emitter.emit("health", { healthy: false });
            ws.close();
            return;
          }
          this._emitter.emit("health", { healthy: false });
        }
      }
      this._keepalive.pendingKeepalive = true;
      ws.send(
        JSON.stringify({
          type: "keepalive",
          data: { client_time: this._now(), last_seen_seq: this.lastSeenSeq() },
        }),
      );
    }, this._keepaliveIntervalMs);
  }

  _stopKeepalive() {
    if (this._keepaliveTimer) {
      this._clearInterval(this._keepaliveTimer);
      this._keepaliveTimer = null;
    }
  }

  /** Best-effort send. Returns true iff the socket was open and the send was attempted. */
  send(msg) {
    if (this._ws && this._state === "open") {
      this._ws.send(JSON.stringify(msg));
      return true;
    }
    return false;
  }

  /**
   * Sends once connected, connecting first if needed. Resolves after the
   * send is attempted; rejects with MittoNetworkError on timeout.
   */
  sendWhenConnected(msg, options = {}) {
    const timeoutMs = options.timeout ?? SEND_WHEN_CONNECTED_TIMEOUT_MS;
    if (this._state === "open") {
      this.send(msg);
      return Promise.resolve();
    }
    return new Promise((resolve, reject) => {
      const timer = this._setTimeout(() => {
        offOpen();
        reject(new MittoNetworkError("SessionStream: timed out waiting for connection"));
      }, timeoutMs);
      const offOpen = this.once("open", () => {
        this._clearTimeout(timer);
        this.send(msg);
        resolve();
      });
      this.connect();
    });
  }

  /**
   * Sends a user prompt with delivery verification (ported from
   * useWSDeliveryVerification.js's sendPrompt). `payload` is the already-
   * built message content — this method owns none of the UI-side message
   * list, only the wire protocol and the ACK/retry state machine.
   *
   * @param {{message: string, promptName?: string, imageIds?: string[], fileIds?: string[]}} payload
   * @param {{initialAckTimeoutMs?: number, totalDeliveryBudgetMs?: number}} [options]
   * @returns {Promise<{success: boolean, promptId: string, verifiedOnReconnect?: boolean, retriedOnReconnect?: boolean}>}
   */
  async sendPrompt(payload, options = {}) {
    const { message, promptName, imageIds = [], fileIds = [] } = payload || {};
    const startTime = this._now();
    const totalBudgetMs = options.totalDeliveryBudgetMs ?? this._totalDeliveryBudgetMs;
    const promptId = generatePromptId();

    this._pendingPromptStore.save(this._sessionId, promptId, message, imageIds, fileIds);

    const attemptSend = (ackTimeoutMs) =>
      new Promise((resolve, reject) => {
        const timeoutId = this._setTimeout(() => {
          if (!this._pendingSends.has(promptId)) return;
          this._pendingSends.delete(promptId);
          reject(new AckTimeoutError("SessionStream: ACK_TIMEOUT"));
        }, ackTimeoutMs);
        this._pendingSends.set(promptId, { resolve, reject, timeoutId });

        const sent = this.send({
          type: "prompt",
          data: {
            message,
            prompt_name: promptName || undefined,
            image_ids: imageIds,
            file_ids: fileIds,
            prompt_id: promptId,
          },
        });
        if (!sent) {
          this._clearTimeout(timeoutId);
          this._pendingSends.delete(promptId);
          reject(new MittoNetworkError("SessionStream: failed to send prompt (not connected)"));
        }
      });

    const verifyDeliveryAfterReconnect = async (reconnectTimeoutMs) => {
      await this._forceReconnectAndWaitOpen(reconnectTimeoutMs);
      await new Promise((r) => this._setTimeout(r, 100));
      const confirmed = this._lastConfirmedPrompt;
      return !!(confirmed && confirmed.promptId === promptId);
    };

    try {
      const result = await attemptSend(options.initialAckTimeoutMs ?? this._initialAckTimeoutMs);
      this._pendingPromptStore.remove(promptId);
      return result;
    } catch (err) {
      if (!(err instanceof AckTimeoutError)) throw err;

      const remainingBudget = totalBudgetMs - (this._now() - startTime);
      if (remainingBudget <= 0) {
        throw new MittoNetworkError("Message delivery timed out. Please check your connection and try again.");
      }

      try {
        const reconnectTimeoutMs = Math.min(remainingBudget, this._reconnectVerifyTimeoutMs);
        const wasDelivered = await verifyDeliveryAfterReconnect(reconnectTimeoutMs);
        if (wasDelivered) {
          this._pendingPromptStore.remove(promptId);
          return { success: true, promptId, verifiedOnReconnect: true };
        }

        const retryBudget = totalBudgetMs - (this._now() - startTime);
        if (retryBudget <= 500) {
          throw new MittoNetworkError("Message delivery could not be confirmed. Please try again.");
        }

        const result = await attemptSend(retryBudget);
        this._pendingPromptStore.remove(promptId);
        return { ...result, retriedOnReconnect: true };
      } catch (reconnectErr) {
        if (reconnectErr instanceof AckTimeoutError) {
          throw new MittoNetworkError(
            "Message delivery could not be confirmed after retry. Please check your connection.",
          );
        }
        if (reconnectErr instanceof MittoNetworkError) throw reconnectErr;
        throw new MittoNetworkError(
          "Connection lost and could not reconnect. Please check your network and try again.",
          { cause: reconnectErr },
        );
      }
    }
  }

  /** Force-closes and reopens the connection, resolving once "open" fires (or rejecting on timeout). */
  _forceReconnectAndWaitOpen(timeoutMs) {
    return new Promise((resolve, reject) => {
      const timer = this._setTimeout(() => {
        offOpen();
        reject(new MittoNetworkError("SessionStream: timed out waiting for reconnect"));
      }, timeoutMs);
      const offOpen = this.once("open", () => {
        this._clearTimeout(timer);
        resolve();
      });
      this.forceReconnect();
    });
  }

  _resolvePendingSend(promptId, result) {
    const pending = this._pendingSends.get(promptId);
    if (!pending) return;
    this._clearTimeout(pending.timeoutId);
    this._pendingSends.delete(promptId);
    pending.resolve(result);
    this._pendingPromptStore.remove(promptId);
  }

  /** Resolves every currently-pending send as successful (e.g. an agent response proves delivery). */
  resolveAllPendingSends() {
    for (const [promptId, pending] of this._pendingSends) {
      this._clearTimeout(pending.timeoutId);
      pending.resolve({ success: true, promptId });
      this._pendingPromptStore.remove(promptId);
    }
    this._pendingSends.clear();
  }

  /** Re-sends every unexpired pending prompt for this session (e.g. after a reconnect). Returns the count sent. */
  retryPendingPrompts() {
    const pending = this._pendingPromptStore.getForSession(this._sessionId);
    let sent = 0;
    for (const { promptId, message, imageIds } of pending) {
      const ok = this.send({ type: "prompt", data: { message, image_ids: imageIds || [], prompt_id: promptId } });
      if (!ok) break; // Not connected — the rest will retry on the next reconnect.
      sent++;
    }
    return sent;
  }

  /** Sends a cancel request for the current prompt/turn. */
  cancelPrompt() {
    return this.send({ type: "cancel" });
  }

  /** Sends a force_reset request for a stuck session. */
  forceResetSession() {
    return this.send({ type: "force_reset" });
  }

  /** Explicit close: never schedules a reconnect. */
  close() {
    this._explicitlyClosed = true;
    if (this._reconnectTimer) {
      this._clearTimeout(this._reconnectTimer);
      this._reconnectTimer = null;
    }
    this._stopKeepalive();
    this._clearSyncInFlight();
    if (this._ws) {
      this._state = "closing";
      this._ws.close();
    } else {
      this._state = "stopped";
    }
  }
}

/** Whether the reconnect attempt count has exceeded the configured maximum. */
export function isReconnectLimitReached(attempt, options = {}) {
  const max = options.maxAttempts ?? MAX_RECONNECT_ATTEMPTS;
  return attempt >= max;
}

/**
 * Factory: creates a SessionStream bound to `config` (from resolveConfig())
 * for a single `sessionId`. Does not connect automatically — call
 * `.connect()` (or `.sendWhenConnected()`) explicitly.
 */
export function createSessionStream(config, sessionId, options = {}) {
  return new SessionStream(config, sessionId, options);
}
