/**
 * Shared WebSocket transport primitives for sdk/realtime/** (mitto-7gta.15).
 *
 * Extracted out of session-stream.js so SessionStream (per-session,
 * seq/keepalive-aware) and EventsStream (global bus, broadcast-only) can
 * both build on the same backoff math, URL derivation, and zero-dependency
 * emitter without either forking the other's protocol-specific state
 * machine. Same purity rules as the rest of sdk/realtime/**: no window,
 * document, console, localStorage, navigator, location, or native.js — see
 * the scan test in session-stream.test.js (covers this whole directory).
 */
import { ConfigError } from "../core/errors.js";

export const RECONNECT_BASE_DELAY_MS = 1000;
export const RECONNECT_MAX_DELAY_MS = 30000;
export const RECONNECT_JITTER_FACTOR = 0.3;
export const RECONNECT_DEBOUNCE_MS = 3000;
export const MAX_RECONNECT_ATTEMPTS = 15;

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

/** Whether the reconnect attempt count has exceeded the configured maximum. */
export function isReconnectLimitReached(attempt, options = {}) {
  const max = options.maxAttempts ?? MAX_RECONNECT_ATTEMPTS;
  return attempt >= max;
}

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
export function wsUrlFor(config, path, options = {}, label = "MittoWsTransport") {
  const base = options.wsBaseUrl ?? config.baseUrl;
  if (!base) {
    throw new ConfigError(
      `${label}: cannot derive a WebSocket URL from an empty baseUrl; ` +
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
      `${label}: unrecognized baseUrl scheme "${base}"; expected an ` +
        "absolute http(s):// or ws(s):// URL.",
    );
  }
  const prefix = config.apiPrefix || "";
  return `${wsBase}${prefix}${path}`;
}

/** Minimal zero-dependency emitter. No DOM EventTarget (§4: no DOM). */
export function createEmitter() {
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
