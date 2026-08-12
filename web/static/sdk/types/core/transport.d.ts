/** Build a query string from a params object, omitting null/undefined/""
 *  values. Array values emit repeated `key=v` params (one per element);
 *  empty arrays are "no filter" and omitted entirely. Ported from
 *  utils/endpoints.js's `qs()` so behavior stays byte-identical. Exported
 *  (deep-import only, not re-exported from sdk/index.js) so core/endpoints.js
 *  shares this single implementation instead of duplicating it. */
export function qs(params: any): string;
/**
 * Builds the full request URL from injected config. An absolute `path`
 * (http(s)://) is used as-is (query params are still appended); otherwise
 * `config.baseUrl + config.apiPrefix + path` is used.
 * @param {object} config - resolved config (see core/config.js)
 * @param {string} path
 * @param {object} [query]
 * @returns {string}
 */
export function buildUrl(config: object, path: string, query?: object): string;
/**
 * @typedef {Object} RequestOptions
 * @property {string} [method] - defaults to "GET"
 * @property {string} path - relative (joined with baseUrl+apiPrefix) or absolute
 * @property {object} [query] - query params, see `qs()`
 * @property {*} [body] - JSON-serializable value, or a passthrough body
 *   (FormData/Blob/ArrayBuffer/URLSearchParams/string)
 * @property {object} [headers]
 * @property {AbortSignal} [signal]
 * @property {boolean} [raw] - when true, resolve with the untouched
 *   `Response` instead of a decoded body (for streaming/blob callers)
 * @property {number[]} [allowStatus] - HTTP statuses to exclude from the
 *   error path (in addition to the normal 2xx range), e.g. `[304]` so a
 *   cache decorator can observe a conditional-request "Not Modified" response
 *   itself instead of it always being thrown as a `MittoApiError`. Implies
 *   `raw: true` handling for the allow-listed status is left to the caller —
 *   pass `raw: true` alongside this to get the untouched `Response`.
 * @property {boolean} [retryUnavailable] - retry one canonical 503
 *   `unavailable` response, honoring `Retry-After` up to 30 seconds
 */
/**
 * The single request primitive. Resource modules curry `config` and call
 * this for every HTTP call.
 * @param {object} config - resolved config (see core/config.js)
 * @param {RequestOptions} options
 * @returns {Promise<*>}
 */
export function request(config: object, options: RequestOptions): Promise<any>;
export type RequestOptions = {
    /**
     * - defaults to "GET"
     */
    method?: string;
    /**
     * - relative (joined with baseUrl+apiPrefix) or absolute
     */
    path: string;
    /**
     * - query params, see `qs()`
     */
    query?: object;
    /**
     * - JSON-serializable value, or a passthrough body
     * (FormData/Blob/ArrayBuffer/URLSearchParams/string)
     */
    body?: any;
    headers?: object;
    signal?: AbortSignal;
    /**
     * - when true, resolve with the untouched
     * `Response` instead of a decoded body (for streaming/blob callers)
     */
    raw?: boolean;
    /**
     * - HTTP statuses to exclude from the
     * error path (in addition to the normal 2xx range), e.g. `[304]` so a
     * cache decorator can observe a conditional-request "Not Modified" response
     * itself instead of it always being thrown as a `MittoApiError`. Implies
     * `raw: true` handling for the allow-listed status is left to the caller —
     * pass `raw: true` alongside this to get the untouched `Response`.
     */
    allowStatus?: number[];
    /**
     * - retry one canonical 503
     * `unavailable` response, honoring `Retry-After` up to 30 seconds
     */
    retryUnavailable?: boolean;
};
