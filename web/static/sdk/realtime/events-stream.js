/**
 * EventsStream — transport-only WebSocket wrapper for the global /api/events
 * bus (docs/devel/js-client-library.md §3, realtime/ area; mitto-7gta.15).
 * Ported from web/static/hooks/useWSConnection.js's `connectToEvents` +
 * web/static/hooks/useWebSocket.js's `handleGlobalEvent` switch, stripped of
 * every Preact/DOM/localStorage/console dependency.
 *
 * Unlike SessionStream, the global bus (internal/web/events_ws.go) is
 * broadcast-only and seq-less: the server's readPump only reads to detect
 * disconnect, Broadcast() emits bare `{type, data}` frames, and there is no
 * keepalive_ack, session_gone, events_loaded, or prompt ACK on this socket.
 * EventsStream therefore does NOT reuse SessionStream's keepalive/seq/
 * gap-fill/sendPrompt machinery — reusing it verbatim would miss-count
 * keepalives forever and close a healthy socket. It shares only the
 * connect/backoff/emitter primitives via ws-transport.js.
 *
 * Deliberately out of scope (host policy / later issues):
 *   - the initial-vs-reconnect branch (fetch + restore session vs.
 *     refresh-only) — the SDK only reports `isReconnect` via the "open"
 *     event; the host decides what to do with it.
 *   - typed event-name constants (.16) and the 24-case handleGlobalEvent
 *     switch (host-adoption bead) — this class emits raw `{type, data}`.
 */
import { MittoNetworkError } from "../core/errors.js";
import {
  calculateReconnectDelay,
  isReconnectLimitReached,
  wsUrlFor,
  createEmitter,
  MAX_RECONNECT_ATTEMPTS,
  RECONNECT_DEBOUNCE_MS,
} from "./ws-transport.js";

/**
 * Single global-bus WebSocket transport. Receive-only: the server does not
 * act on any client-sent frame, so no send()/sendPrompt() is exposed.
 *
 * States: "idle" -> "connecting" -> "open" -> "closed", plus the terminal
 * "stopped" reached via explicit close() or the reconnect-attempt cap.
 * Explicit close() never schedules a reconnect.
 *
 * Events (via `.on(event, handler) -> unsubscribe`):
 *   "open" ({ isReconnect }), "connected" (server's `connected` payload),
 *   "message" (every raw parsed `{type, data}` frame), "event" ({ type,
 *   data } for every non-"connected" frame), "close", "error",
 *   "reconnecting" ({ attempt, delayMs }).
 */
export class EventsStream {
  constructor(config, options = {}) {
    this._config = config;
    this._now = options.now ?? Date.now;
    this._setTimeout = options.setTimeout ?? setTimeout;
    this._clearTimeout = options.clearTimeout ?? clearTimeout;
    this._random = options.random ?? Math.random;
    this._wsBaseUrl = options.wsBaseUrl;
    this._maxReconnectAttempts = options.maxReconnectAttempts ?? MAX_RECONNECT_ATTEMPTS;
    this._reconnectDebounceMs = options.reconnectDebounceMs ?? RECONNECT_DEBOUNCE_MS;
    // Injectable veto for the browser-specific checkAuthOrRedirect()/
    // "server shutting down" gate before reconnecting. Defaults to always
    // allowing reconnect.
    this._shouldReconnect = options.shouldReconnect ?? (() => true);
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
    this._wasConnected = false;
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

  /** Opens the connection if not already open/connecting. No-op otherwise. */
  connect() {
    if (this._state === "connecting" || this._state === "open") return;
    this._explicitlyClosed = false;
    this._doConnect();
  }

  _doConnect() {
    this._state = "connecting";
    const WebSocketImpl = this._config.getWebSocket();
    const url = wsUrlFor(this._config, "/api/events", { wsBaseUrl: this._wsBaseUrl }, "EventsStream");

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
        this._config.logger.error("EventsStream: authorizeWebSocket failed", err);
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
      this._config.logger.warn("EventsStream: WebSocket error", event);
    };
  }

  _handleOpen(ws) {
    if (this._ws !== ws) return;
    this._state = "open";
    this._reconnectAttempt = 0;
    const isReconnect = this._wasConnected;
    this._wasConnected = true;
    this._emitter.emit("open", { isReconnect });
  }

  _handleMessage(event) {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch (err) {
      this._config.logger.error("EventsStream: failed to parse message", err, event.data);
      return;
    }

    this._emitter.emit("message", msg);

    if (msg.type === "connected") {
      this._emitter.emit("connected", msg.data || {});
      return;
    }

    this._emitter.emit("event", { type: msg.type, data: msg.data });
  }

  _handleClose(ws, event) {
    if (this._ws !== ws) return;
    this._ws = null;
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
    if (this._explicitlyClosed) {
      this._state = "stopped";
      return;
    }
    if (isReconnectLimitReached(this._reconnectAttempt, { maxAttempts: this._maxReconnectAttempts })) {
      this._state = "stopped";
      this._emitter.emit("error", new MittoNetworkError("EventsStream: reconnect attempt limit reached"));
      return;
    }

    // Fast path: a synchronous `true` (the default) schedules the reconnect
    // immediately, matching SessionStream's synchronous reconnect-on-close
    // behavior. Only a genuinely async/false result takes the microtask
    // detour, so hosts that never override shouldReconnect see no timing
    // change from before this hook existed.
    const decision = this._shouldReconnect();
    if (decision === true) {
      this._scheduleReconnect();
      return;
    }
    Promise.resolve(decision).then((allowed) => {
      if (this._state !== "closed") return; // superseded (e.g. reconnected/closed meanwhile)
      if (!allowed) {
        this._state = "stopped";
        return;
      }
      this._scheduleReconnect();
    });
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
   * concurrent triggers into a single reconnect.
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
      ws.close();
    }
    this._doConnect();
  }

  /** Explicit close: never schedules a reconnect. */
  close() {
    this._explicitlyClosed = true;
    if (this._reconnectTimer) {
      this._clearTimeout(this._reconnectTimer);
      this._reconnectTimer = null;
    }
    if (this._ws) {
      this._state = "closing";
      this._ws.close();
    } else {
      this._state = "stopped";
    }
  }
}

/**
 * Factory: creates an EventsStream bound to `config` (from resolveConfig()).
 * Does not connect automatically — call `.connect()` explicitly.
 */
export function createEventsStream(config, options = {}) {
  return new EventsStream(config, options);
}
