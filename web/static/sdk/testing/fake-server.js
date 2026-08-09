/**
 * Shared fake-server fixtures for SDK unit tests (mitto-7gta.23).
 *
 * Consolidates the `fakeResponse`/`mk` pair that used to be re-declared
 * (byte-identical) in every `sdk/resources/*.test.js` file, plus the
 * slightly different variant in `core/transport.test.js`. All fetch
 * behavior stays driven by an injected `config.fetch` stub — never global
 * fetch — preserving the injection discipline documented across the
 * existing SDK tests.
 *
 * Deliberately lives outside `sdk/realtime/`: `session-stream.test.js` runs
 * a no-browser-globals source scan over every non-test `.js` under
 * `realtime/`, and this module has no realtime dependents anyway.
 */
import { resolveConfig } from "../core/config.js";

/**
 * Builds a minimal fake `Response`-like object. Superset of the three
 * shapes previously duplicated across the SDK test suite: a JSON `body`,
 * raw `text`, and/or custom `headers` (any explicit `headers["content-type"]`
 * wins over the JSON-body default).
 * @param {{status?: number, body?: *, headers?: object, text?: string}} [opts]
 */
export function fakeResponse({ status = 200, body, headers = {}, text } = {}) {
  const hasJsonBody = body !== undefined;
  const lowerHeaders = Object.fromEntries(
    Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]),
  );
  const contentType =
    lowerHeaders["content-type"] ?? (hasJsonBody ? "application/json" : null);
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: {
      get: (name) => {
        const key = name.toLowerCase();
        if (key === "content-type") return contentType;
        return lowerHeaders[key] ?? null;
      },
    },
    text: async () => {
      if (hasJsonBody) return JSON.stringify(body);
      if (text !== undefined) return text;
      return "";
    },
  };
}

/**
 * Creates a fake server: a resolved `config` whose `fetch` is a stub that
 * records every call and answers with a swappable responder. Defaults to a
 * bare 204 (matches the prior per-file `mk()` default).
 * @param {object} [extra] - extra `resolveConfig()` options (e.g. apiPrefix)
 * @returns {{config: object, calls: Array, respondWith: Function,
 *   respondOnce: Function, respondTo: Function, lastCall: Function,
 *   reset: Function}}
 */
export function createFakeServer(extra = {}) {
  const calls = [];
  let next = () => fakeResponse({ status: 204 });
  const fetchImpl = async (url, init) => {
    calls.push({ url, init });
    return next(url, init);
  };
  const config = resolveConfig({ fetch: fetchImpl, ...extra }, {});
  return {
    config,
    calls,
    /** Replace the responder for every subsequent call. */
    respondWith: (fn) => {
      next = fn;
    },
    /** Answer the very next call with `fn`, then revert to the prior responder. */
    respondOnce: (fn) => {
      const prev = next;
      next = (...args) => {
        next = prev;
        return fn(...args);
      };
    },
    /**
     * Declarative route table for multi-call tests: keys are `"METHOD url"`
     * (or bare `url`, matched against any method); values are either a
     * plain body (wrapped in `fakeResponse({body})`) or a responder function.
     */
    respondTo: (routes) => {
      next = (url, init) => {
        const method = (init?.method || "GET").toUpperCase();
        const entry = routes[`${method} ${url}`] ?? routes[url];
        if (entry === undefined) {
          throw new Error(`createFakeServer: no route registered for "${method} ${url}"`);
        }
        return typeof entry === "function" ? entry() : fakeResponse({ body: entry });
      };
    },
    lastCall: () => calls[calls.length - 1],
    /** Clears recorded calls and reverts to the default 204 responder. */
    reset: () => {
      calls.length = 0;
      next = () => fakeResponse({ status: 204 });
    },
  };
}

/**
 * Convenience for the common single-resource-module case: builds a fake
 * server and mounts `factory(config)` onto it in one call.
 * Resource modules needing more than `config` (e.g. `misc`'s
 * `(config, serverConfig)`) should call `createFakeServer()` directly and
 * compose the factory call themselves.
 * @param {(config: object) => *} factory
 * @param {object} [extra]
 */
export function mountResource(factory, extra = {}) {
  const server = createFakeServer(extra);
  return { resource: factory(server.config), ...server };
}

/**
 * Builds the per-file `mk(extra)` helper the resource test files use: each
 * call mounts a fresh fake server and spreads the named resources returned
 * by `factory(config)` alongside the server handles. `factory` returns a map
 * so a file composing several resources (e.g. `misc`, which needs `config`'s
 * resource too) needs no fixture of its own.
 * @param {(config: object) => Record<string, *>} factory
 */
export function resourceMounter(factory) {
  return (extra = {}) => {
    const server = createFakeServer(extra);
    return { ...factory(server.config), ...server };
  };
}

/** Responder: fetch itself rejects (DNS/TLS/offline-style failure). */
export function networkFailure(message = "network down") {
  return () => {
    throw new Error(message);
  };
}

/** Responder: a 401 with the canonical nested error envelope. */
export function authFailure({ message = "Authentication required" } = {}) {
  return () => fakeResponse({ status: 401, body: { error: { code: "unauthenticated", message } } });
}

/** Responder: a non-2xx response with the canonical nested error envelope. */
export function apiFailure(status, code, message, extra = {}) {
  return () => fakeResponse({ status, body: { error: { code, message, ...extra } } });
}
