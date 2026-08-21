/**
 * Builds a minimal fake `Response`-like object. Superset of the three
 * shapes previously duplicated across the SDK test suite: a JSON `body`,
 * raw `text`, and/or custom `headers` (any explicit `headers["content-type"]`
 * wins over the JSON-body default).
 * @param {{status?: number, body?: *, headers?: object, text?: string}} [opts]
 */
export function fakeResponse({ status, body, headers, text }?: {
    status?: number;
    body?: any;
    headers?: object;
    text?: string;
}): {
    ok: boolean;
    status: number;
    headers: {
        get: (name: any) => any;
    };
    text: () => Promise<string>;
};
/**
 * Creates a fake server: a resolved `config` whose `fetch` is a stub that
 * records every call and answers with a swappable responder. Defaults to a
 * bare 204 (matches the prior per-file `mk()` default).
 * @param {object} [extra] - extra `resolveConfig()` options (e.g. apiPrefix)
 * @returns {{config: object, calls: Array, respondWith: Function,
 *   respondOnce: Function, respondTo: Function, lastCall: Function,
 *   reset: Function}}
 */
export function createFakeServer(extra?: object): {
    config: object;
    calls: any[];
    respondWith: Function;
    respondOnce: Function;
    respondTo: Function;
    lastCall: Function;
    reset: Function;
};
/**
 * Convenience for the common single-resource-module case: builds a fake
 * server and mounts `factory(config)` onto it in one call.
 * Resource modules needing more than `config` (e.g. `misc`'s
 * `(config, serverConfig)`) should call `createFakeServer()` directly and
 * compose the factory call themselves.
 * @param {(config: object) => *} factory
 * @param {object} [extra]
 */
export function mountResource(factory: (config: object) => any, extra?: object): {
    config: object;
    calls: any[];
    respondWith: Function;
    respondOnce: Function;
    respondTo: Function;
    lastCall: Function;
    reset: Function;
    resource: any;
};
/**
 * Builds the per-file `mk(extra)` helper the resource test files use: each
 * call mounts a fresh fake server and spreads the named resources returned
 * by `factory(config)` alongside the server handles. `factory` returns a map
 * so a file composing several resources (e.g. `misc`, which needs `config`'s
 * resource too) needs no fixture of its own.
 * @param {(config: object) => Record<string, *>} factory
 */
export function resourceMounter(factory: (config: object) => Record<string, any>): (extra?: {}) => {
    config: object;
    calls: any[];
    respondWith: Function;
    respondOnce: Function;
    respondTo: Function;
    lastCall: Function;
    reset: Function;
};
/** Responder: fetch itself rejects (DNS/TLS/offline-style failure). */
export function networkFailure(message?: string): () => never;
/** Responder: a 401 with the canonical nested error envelope. */
export function authFailure({ message }?: {
    message?: string;
}): () => {
    ok: boolean;
    status: number;
    headers: {
        get: (name: any) => any;
    };
    text: () => Promise<string>;
};
/** Responder: a non-2xx response with the canonical nested error envelope. */
export function apiFailure(status: any, code: any, message: any, extra?: {}): () => {
    ok: boolean;
    status: number;
    headers: {
        get: (name: any) => any;
    };
    text: () => Promise<string>;
};
