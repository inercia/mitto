/** Exponential backoff with jitter. Exported for deterministic unit tests. */
export function calculateReconnectDelay(attempt: any, options?: {}): number;
/** Whether the reconnect attempt count has exceeded the configured maximum. */
export function isReconnectLimitReached(attempt: any, options?: {}): boolean;
/**
 * Builds an absolute WebSocket URL from injected config plus a path suffix
 * (e.g. "/api/sessions/{id}/ws" or "/api/events"). Never reads
 * `window.location` — an absolute `config.baseUrl` maps its http(s) scheme
 * to ws(s); a relative/empty `baseUrl` requires an explicit
 * `options.wsBaseUrl` (e.g. "ws://host:1234"). `label` prefixes thrown
 * ConfigError messages so each caller's errors stay distinguishable (e.g.
 * "SessionStream: ..." vs "EventsStream: ...").
 * This stays the low-level primitive (mitto-7gta.6): core/endpoints.js's
 * `sessions.ws()`/`events.ws()` builders are the named registry on top of
 * it, delegating here rather than hand-building WebSocket URLs themselves.
 */
export function wsUrlFor(config: any, path: any, options?: {}, label?: string): string;
/** Minimal zero-dependency emitter. No DOM EventTarget (§4: no DOM). */
export function createEmitter(): {
    on(event: any, handler: any): () => any;
    once(event: any, handler: any): () => any;
    emit(event: any, ...args: any[]): void;
};
export const RECONNECT_BASE_DELAY_MS: 1000;
export const RECONNECT_MAX_DELAY_MS: 30000;
export const RECONNECT_JITTER_FACTOR: 0.3;
export const RECONNECT_DEBOUNCE_MS: 3000;
export const MAX_RECONNECT_ATTEMPTS: 15;
