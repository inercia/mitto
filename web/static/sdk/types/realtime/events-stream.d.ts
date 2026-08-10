/**
 * Factory: creates an EventsStream bound to `config` (from resolveConfig()).
 * Does not connect automatically — call `.connect()` explicitly.
 */
export function createEventsStream(config: any, options?: {}): EventsStream;
/**
 * Single global-bus WebSocket transport. Receive-only: the server does not
 * act on any client-sent frame, so no send()/sendPrompt() is exposed.
 *
 * States: "idle" -> "connecting" -> "open" -> "closing"/"closed", plus the
 * terminal "stopped" reached via explicit close() or the reconnect-attempt
 * cap. Explicit close() never schedules a reconnect.
 *
 * Events (via `.on(event, handler) -> unsubscribe`):
 *   "open" ({ isReconnect }), "connected" (server's `connected` payload),
 *   "message" (every raw parsed `{type, data}` frame), "event" ({ type,
 *   data } for every non-"connected" frame), "close", "error",
 *   "reconnecting" ({ attempt, delayMs }).
 */
export class EventsStream {
    constructor(config: any, options?: {});
    _config: any;
    _now: any;
    _setTimeout: (...args: any[]) => any;
    _clearTimeout: (...args: any[]) => any;
    _random: any;
    _wsBaseUrl: any;
    _maxReconnectAttempts: any;
    _reconnectDebounceMs: any;
    _shouldReconnect: any;
    _backoffOptions: {
        baseDelay: any;
        maxDelay: any;
        jitterFactor: any;
        random: any;
    };
    _emitter: {
        on(event: any, handler: any): () => any;
        once(event: any, handler: any): () => any;
        emit(event: any, ...args: any[]): void;
    };
    _ws: any;
    _state: string;
    _explicitlyClosed: boolean;
    _reconnectAttempt: number;
    _reconnectTimer: any;
    _lastReconnectAt: number;
    _wasConnected: boolean;
    get state(): string;
    on(event: any, handler: any): () => any;
    once(event: any, handler: any): () => any;
    /** Opens the connection if not already open/connecting. No-op otherwise. */
    connect(): void;
    _doConnect(): void;
    _openSocket(WebSocketImpl: any, url: any, wsAuth: any): void;
    _handleOpen(ws: any): void;
    _handleMessage(event: any): void;
    _handleClose(ws: any, event: any): void;
    /**
     * Reached when `authorizeWebSocket()` rejects before a socket was ever
     * created (no `ws`/`close` event to key off of) — applies the same
     * explicit-close / reconnect-limit / backoff decision as `_handleClose`.
     */
    _handleConnectFailure(): void;
    _reconnectOrStop(): void;
    _scheduleReconnect(): void;
    /**
     * Force a reconnect, closing any existing connection first. Debounced
     * (leading-edge, `reconnectDebounceMs` window) to collapse bursts of
     * concurrent triggers into a single reconnect.
     */
    forceReconnect(): void;
    /** Explicit close: never schedules a reconnect. */
    close(): void;
}
