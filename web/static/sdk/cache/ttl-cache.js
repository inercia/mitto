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
export function createTtlCache({ ttlMs, keyFor, revalidate }) {
  /** @type {Map<string, { record: *, timestamp: number }>} */
  const cache = new Map();
  /** @type {Map<string, Promise<*>>} */
  const inflight = new Map();

  const toValue = (record) => (revalidate ? revalidate.value(record) : record);

  /**
   * Wraps `fn` with the three-level cache. Without `revalidate`, `fn(...args)`
   * must resolve with the plain value to cache. With `revalidate`, `fn` is
   * called as `fn(...args, revalidationHeader)` and must resolve with
   * `{ response, data }` (the raw response and the freshly decoded body,
   * only meaningful when the response isn't the "unchanged" status).
   * The wrapped function accepts an extra trailing `{ force }` option to
   * bypass both caches while still repopulating them.
   */
  function wrap(fn) {
    return async function cached(...args) {
      const last = args[args.length - 1];
      const hasOpts = last && typeof last === "object" && "force" in last;
      const force = hasOpts ? !!last.force : false;
      const fnArgs = hasOpts ? args.slice(0, -1) : args;

      const key = keyFor(...fnArgs);

      if (!force) {
        const hit = cache.get(key);
        if (hit && Date.now() - hit.timestamp < ttlMs) {
          return toValue(hit.record);
        }
        const existing = inflight.get(key);
        if (existing) return existing;
      }

      const priorEntry = cache.get(key);
      const revalidationHeader =
        !force && revalidate && priorEntry
          ? revalidate.header(priorEntry.record)
          : null;

      const promise = (async () => {
        try {
          let record;
          if (revalidate) {
            const { response, data: fresh } = await fn(
              ...fnArgs,
              revalidationHeader,
            );
            record =
              priorEntry && revalidate.isUnchanged(response)
                ? priorEntry.record
                : revalidate.extract(response, fresh);
          } else {
            record = await fn(...fnArgs);
          }
          cache.set(key, { record, timestamp: Date.now() });
          return toValue(record);
        } finally {
          inflight.delete(key);
        }
      })();

      if (!force) inflight.set(key, promise);
      return promise;
    };
  }

  /**
   * Invalidates cached entries. With no argument, clears everything
   * (matching `invalidateConfigCache()`'s semantics). With a predicate,
   * clears only keys for which it returns true (matching
   * `invalidateWorkspacePromptsCache(workingDir)`'s selective clear).
   */
  function invalidate(predicate) {
    if (!predicate) {
      cache.clear();
      inflight.clear();
      return;
    }
    for (const key of cache.keys()) {
      if (predicate(key)) {
        cache.delete(key);
        inflight.delete(key);
      }
    }
  }

  return { wrap, invalidate };
}

/**
 * Builds a stable cache key from a params object: sorted keys,
 * empty/undefined/null values omitted, so callers that pass `undefined` for
 * an absent field key identically to callers that omit it entirely. Ported
 * verbatim from `utils/promptsCache.js`'s `cacheKeyFor`.
 * @param {Object} [params]
 * @returns {string}
 */
export function keyForParams(params) {
  const entries = Object.entries(params || {})
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  return entries.map(([k, v]) => `${k}=${v}`).join("&") || "__default__";
}
