/**
 * Generic TTL + in-flight-dedup + conditional-revalidation cache decorator
 * (mitto-7gta.10 plan, DECISION 1).
 *
 * Caching is deliberately NEVER baked into `core/transport.js`. Instead this
 * module wraps an arbitrary async function — typically a resource method
 * like `client.prompts.list` or `client.serverConfig.get` — with the same
 * three-level pattern already proven by `utils/promptsCache.js` and
 * `utils/configCache.js`:
 *   1. Completed-response cache (TTL-based): avoids refetching within the
 *      TTL window.
 *   2. In-flight Promise cache: concurrent callers for the same key share
 *      one in-flight request instead of each firing their own (the
 *      "thundering herd" problem).
 *   3. Conditional revalidation (optional): once the TTL expires, replay the
 *      request with a caller-supplied revalidation header derived from the
 *      previous response; if the server answers with the configured
 *      "unchanged" status (e.g. 304), keep serving the cached data and reset
 *      the TTL window instead of re-parsing a body.
 *
 * This is a deep import, not part of the public SDK surface
 * (docs/devel/js-client-library.md §5) — `sdk/index.js` re-exports
 * `createTtlCache` as the supported entrypoint.
 */
/**
 * @typedef {Object} TtlCacheRevalidateOptions
 * @property {(record: *) => (Object|null)} [header] - Given the previously
 *   stored cache record, returns `{ name, value }` for the revalidation
 *   request header (e.g. `{ name: "If-None-Match", value }`), or a falsy
 *   value when there is nothing to revalidate against yet.
 * @property {(response: *) => boolean} [isUnchanged] - Given the raw fetch
 *   response, returns true when the server reports "no change" (e.g.
 *   `response.status === 304`).
 * @property {(response: *, data: *) => *} [extract] - Given the raw response
 *   and its freshly decoded body, returns the opaque record to store (e.g.
 *   `{ payload, etag }`). Only called when `isUnchanged` returns false.
 * @property {(record: *) => *} [value] - Extracts the public value to return
 *   to callers from a stored record (e.g. `record.payload`).
 */
/**
 * @typedef {Object} TtlCacheOptions
 * @property {number} ttlMs - Freshness window in milliseconds.
 * @property {(...args: *[]) => string} keyFor - Derives a stable cache key
 *   from the wrapped function's arguments.
 * @property {TtlCacheRevalidateOptions} [revalidate] - Optional
 *   conditional-revalidation hooks. When omitted, `fn` must resolve with the
 *   plain value to cache and return directly (no `{ response, data }`
 *   wrapping needed).
 */
/**
 * @param {TtlCacheOptions} options
 * @returns {{ wrap: Function, invalidate: (predicate?: (key: string) => boolean) => void }}
 */
export function createTtlCache({ ttlMs, keyFor, revalidate }: TtlCacheOptions): {
    wrap: Function;
    invalidate: (predicate?: (key: string) => boolean) => void;
};
/**
 * Builds a stable cache key from a params object: sorted keys,
 * empty/undefined/null values omitted, so callers that pass `undefined` for
 * an absent field key identically to callers that omit it entirely. Ported
 * verbatim from `utils/promptsCache.js`'s `cacheKeyFor`.
 * @param {Object} [params]
 * @returns {string}
 */
export function keyForParams(params?: any): string;
export type TtlCacheRevalidateOptions = {
    /**
     * - Given the previously
     * stored cache record, returns `{ name, value }` for the revalidation
     * request header (e.g. `{ name: "If-None-Match", value }`), or a falsy
     * value when there is nothing to revalidate against yet.
     */
    header?: (record: any) => (any | null);
    /**
     * - Given the raw fetch
     * response, returns true when the server reports "no change" (e.g.
     * `response.status === 304`).
     */
    isUnchanged?: (response: any) => boolean;
    /**
     * - Given the raw response
     * and its freshly decoded body, returns the opaque record to store (e.g.
     * `{ payload, etag }`). Only called when `isUnchanged` returns false.
     */
    extract?: (response: any, data: any) => any;
    /**
     * - Extracts the public value to return
     * to callers from a stored record (e.g. `record.payload`).
     */
    value?: (record: any) => any;
};
export type TtlCacheOptions = {
    /**
     * - Freshness window in milliseconds.
     */
    ttlMs: number;
    /**
     * - Derives a stable cache key
     * from the wrapped function's arguments.
     */
    keyFor: (...args: any[]) => string;
    /**
     * - Optional
     * conditional-revalidation hooks. When omitted, `fn` must resolve with the
     * plain value to cache and return directly (no `{ response, data }`
     * wrapping needed).
     */
    revalidate?: TtlCacheRevalidateOptions;
};
