/**
 * Factory: creates a SessionStream bound to `config` (from resolveConfig())
 * for a single `sessionId`. Does not connect automatically — call
 * `.connect()` (or `.sendWhenConnected()`) explicitly.
 */
export function createSessionStream(config: any, sessionId: any, options?: {}): SessionStream;
export namespace SESSION_STREAM_CONSTANTS {
    export { RECONNECT_BASE_DELAY_MS };
    export { RECONNECT_MAX_DELAY_MS };
    export { RECONNECT_JITTER_FACTOR };
    export { RECONNECT_DEBOUNCE_MS };
    export { MAX_RECONNECT_ATTEMPTS };
    export { KEEPALIVE_INTERVAL_MS };
    export { KEEPALIVE_MAX_MISSED_DEFAULT };
    export { KEEPALIVE_MAX_MISSED_LARGE_SESSION };
    export { LARGE_SESSION_SEQ_THRESHOLD };
    export { SEND_WHEN_CONNECTED_TIMEOUT_MS };
    export { STALE_RECOVERY_COOLDOWN_MS };
    export { KEEPALIVE_SYNC_TOLERANCE };
    export { GAP_FILL_DEBOUNCE_MS };
    export { GAP_FILL_LIMIT };
    export { INITIAL_EVENTS_LIMIT };
    export { TOTAL_DELIVERY_BUDGET_MS };
    export { INITIAL_ACK_TIMEOUT_MS };
    export { RECONNECT_VERIFY_TIMEOUT_MS };
    export { SYNC_TIMEOUT_MS };
}
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
 *   "error", "reconnecting" ({ attempt, delayMs }), "health" ({ healthy }),
 *   "keepalive_ack" (raw `msg.data` of a keepalive_ack frame — see below).
 */
export class SessionStream {
    constructor(config: any, sessionId: any, options?: {});
    _config: any;
    _sessionId: any;
    _now: any;
    _setTimeout: (...args: any[]) => any;
    _clearTimeout: (...args: any[]) => any;
    _setInterval: (...args: any[]) => any;
    _clearInterval: (...args: any[]) => any;
    _random: any;
    _wsBaseUrl: any;
    _seqStore: any;
    _isSyncInFlight: any;
    _getClientMaxSeq: any;
    _dropDuplicates: any;
    _keepaliveIntervalMs: any;
    _maxReconnectAttempts: any;
    _reconnectDebounceMs: any;
    _staleRecoveryCooldownMs: any;
    _keepaliveSyncTolerance: any;
    _gapFillDebounceMs: any;
    _initialEventsLimit: any;
    _pendingPromptStore: any;
    _totalDeliveryBudgetMs: any;
    _initialAckTimeoutMs: any;
    _reconnectVerifyTimeoutMs: any;
    _syncTimeoutMs: any;
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
    _terminal: boolean;
    _reconnectAttempt: number;
    _reconnectTimer: any;
    _lastReconnectAt: number;
    _keepaliveTimer: any;
    _keepalive: {
        pendingKeepalive: boolean;
        missedCount: number;
        lastAckTime: number;
    };
    _seqTracker: {
        highestSeq: number;
        recentSeqs: Set<any>;
    };
    _syncInFlightInternal: boolean;
    _syncTimeoutTimer: any;
    _lastStaleRecoveryAt: number;
    _gapFillTimer: any;
    _gapFillScheduledAt: number;
    _pendingSends: Map<any, any>;
    _lastConfirmedPrompt: {
        promptId: any;
        seq: any;
    };
    get state(): string;
    on(event: any, handler: any): () => any;
    once(event: any, handler: any): () => any;
    /** Highest seq this stream knows about for its session, per the injected seqStore. */
    lastSeenSeq(): any;
    /** The { promptId, seq } the server last confirmed as delivered, or null. */
    lastConfirmedPrompt(): {
        promptId: any;
        seq: any;
    };
    /**
     * Clears the seq tracker (dedup state) and resets the seq watermark to 0.
     * Call this before requesting a full reload after detecting stale client
     * state (rule 24: the reset pair must happen together, before the
     * load_events response is processed).
     */
    resetSync(): void;
    /**
     * Health check mirroring isConnectionHealthy(): acked within 2x the
     * keepalive interval AND no outstanding misses. Emits no event itself;
     * callers poll this, or listen for the "health" event on transitions.
     */
    isHealthy(): boolean;
    /** Opens the connection if not already open/connecting. No-op otherwise. */
    connect(): void;
    _doConnect(): void;
    _openSocket(WebSocketImpl: any, url: any, wsAuth: any): void;
    _handleOpen(ws: any): void;
    _handleMessage(event: any): void;
    /**
     * Stale-client / behind-client detection on every keepalive_ack, folding in
     * the server's is_prompting flag as the "actively streaming" signal (the
     * same ack already carries it, so no host-injected isStreaming() is
     * needed). Ported from useWebSocket.js's keepalive_ack handler.
     */
    _handleKeepaliveAck(data: any): void;
    /**
     * Immediate (non-keepalive) gap detection: any message carrying max_seq can
     * reveal that the server has events beyond what this client has seen,
     * without waiting for the next keepalive round-trip. Debounced so a burst
     * of messages schedules at most one load_events.
     */
    _checkAndFillGap(maxSeq: any, clientMaxSeq: any): void;
    _setSyncInFlight(): void;
    _clearSyncInFlight(): void;
    /**
     * Circuit breaker: an explicit `session_gone` message, or an error message
     * matching isTerminalSessionError(), means the session is permanently gone
     * — stop reconnecting immediately instead of retrying into a 404 loop.
     */
    _tripCircuitBreaker(reason: any, data: any): void;
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
     * concurrent triggers into a single reconnect, mirroring
     * forceReconnectActiveSession's shouldDebounceReconnect.
     */
    forceReconnect(): void;
    _startKeepalive(ws: any): void;
    _stopKeepalive(): void;
    /** Best-effort send. Returns true iff the socket was open and the send was attempted. */
    send(msg: any): boolean;
    /**
     * Sends once connected, connecting first if needed. Resolves after the
     * send is attempted; rejects with MittoNetworkError on timeout.
     */
    sendWhenConnected(msg: any, options?: {}): Promise<any>;
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
    sendPrompt(payload: {
        message: string;
        promptName?: string;
        imageIds?: string[];
        fileIds?: string[];
    }, options?: {
        initialAckTimeoutMs?: number;
        totalDeliveryBudgetMs?: number;
    }): Promise<{
        success: boolean;
        promptId: string;
        verifiedOnReconnect?: boolean;
        retriedOnReconnect?: boolean;
    }>;
    /** Force-closes and reopens the connection, resolving once "open" fires (or rejecting on timeout). */
    _forceReconnectAndWaitOpen(timeoutMs: any): Promise<any>;
    _resolvePendingSend(promptId: any, result: any): void;
    /** Resolves every currently-pending send as successful (e.g. an agent response proves delivery). */
    resolveAllPendingSends(): void;
    /** Re-sends every unexpired pending prompt for this session (e.g. after a reconnect). Returns the count sent. */
    retryPendingPrompts(): number;
    /** Sends a cancel request for the current prompt/turn. */
    cancelPrompt(): boolean;
    /** Sends a force_reset request for a stuck session. */
    forceResetSession(): boolean;
    /** Explicit close: never schedules a reconnect. */
    close(): void;
}
import { calculateReconnectDelay } from "./ws-transport.js";
import { isReconnectLimitReached } from "./ws-transport.js";
import { RECONNECT_BASE_DELAY_MS } from "./ws-transport.js";
import { RECONNECT_MAX_DELAY_MS } from "./ws-transport.js";
import { RECONNECT_JITTER_FACTOR } from "./ws-transport.js";
import { RECONNECT_DEBOUNCE_MS } from "./ws-transport.js";
import { MAX_RECONNECT_ATTEMPTS } from "./ws-transport.js";
declare const KEEPALIVE_INTERVAL_MS: 10000;
declare const KEEPALIVE_MAX_MISSED_DEFAULT: 2;
declare const KEEPALIVE_MAX_MISSED_LARGE_SESSION: 4;
declare const LARGE_SESSION_SEQ_THRESHOLD: 500;
declare const SEND_WHEN_CONNECTED_TIMEOUT_MS: 5000;
declare const STALE_RECOVERY_COOLDOWN_MS: 30000;
declare const KEEPALIVE_SYNC_TOLERANCE: 2;
declare const GAP_FILL_DEBOUNCE_MS: 500;
declare const GAP_FILL_LIMIT: 100;
declare const INITIAL_EVENTS_LIMIT: 50;
declare const TOTAL_DELIVERY_BUDGET_MS: 10000;
declare const INITIAL_ACK_TIMEOUT_MS: 3000;
declare const RECONNECT_VERIFY_TIMEOUT_MS: 4000;
declare const SYNC_TIMEOUT_MS: 30000;
export { calculateReconnectDelay, isReconnectLimitReached };
