/**
 * SessionStream — transport-only WebSocket wrapper for a single session
 * (docs/devel/js-client-library.md §3, realtime/ area). Ported from
 * web/static/hooks/useWSConnection.js + utils/websocket.js, stripped of every
 * Preact/DOM/localStorage/console dependency and re-expressed against the
 * injected `config` from sdk/core/config.js (§4). Never imports window,
 * document, localStorage, native.js or bare console — see the purity test.
 *
 * Scope: connect/reconnect, exponential backoff + jitter, reconnect debounce,
 * keepalive + missed-ack zombie detection, send/sendWhenConnected. It emits
 * raw parsed protocol messages and interprets none of their payloads.
 *
 * Deliberately out of scope (left to later issues / host policy):
 *   - seq dedup, stale-client detection, delivery verification (.14)
 *   - the global /api/events socket (.15)
 *   - typed event name constants (.16)
 *   - multi-session fan-out, staggering, background-disconnect grace,
 *     window.__debug, redirect-to-login on 401 (.17/.18, host/UI concerns)
 */
import { ConfigError, MittoNetworkError } from "../core/errors.js";

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

/** Default in-memory seq store; a storage-backed adapter is opt-in (mirrors env/browser.js). */
function createMemorySeqStore() {
  const map = new Map();
  return {
    get: (sessionId) => map.get(sessionId) || 0,
    set: (sessionId, seq) => {
      if (seq > (map.get(sessionId) || 0)) map.set(sessionId, seq);
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
    this._keepaliveIntervalMs = options.keepaliveIntervalMs ?? KEEPALIVE_INTERVAL_MS;
    this._maxReconnectAttempts = options.maxReconnectAttempts ?? MAX_RECONNECT_ATTEMPTS;
    this._reconnectDebounceMs = options.reconnectDebounceMs ?? RECONNECT_DEBOUNCE_MS;
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
    this._reconnectAttempt = 0;
    this._reconnectTimer = null;
    this._lastReconnectAt = 0;
    this._keepaliveTimer = null;
    this._keepalive = { pendingKeepalive: false, missedCount: 0, lastAckTime: 0 };
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
    const ws = new WebSocketImpl(url);
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
      return;
    }
    if (typeof msg.data?.seq === "number") {
      this._seqStore.set(this._sessionId, msg.data.seq);
    }
    this._emitter.emit("message", msg);
  }

  _handleClose(ws, event) {
    if (this._ws !== ws) return;
    this._ws = null;
    this._stopKeepalive();
    this._state = "closed";
    this._emitter.emit("close", event);

    if (this._explicitlyClosed) {
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
        if (this._isSyncInFlight()) {
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

  /** Explicit close: never schedules a reconnect. */
  close() {
    this._explicitlyClosed = true;
    if (this._reconnectTimer) {
      this._clearTimeout(this._reconnectTimer);
      this._reconnectTimer = null;
    }
    this._stopKeepalive();
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
