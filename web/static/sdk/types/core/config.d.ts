/**
 * @typedef {Object} CreateClientOptions
 * @property {string} [baseUrl]
 * @property {string} [apiPrefix]
 * @property {typeof fetch} [fetch]
 * @property {typeof WebSocket} [WebSocket]
 * @property {object} [storage] - `Storage`-like: `getItem`/`setItem`/`removeItem`
 * @property {object} [auth] - see `sdk/auth/`
 * @property {object} [logger] - `{debug, info, warn, error}`
 * @property {(error: *) => void} [onUnauthorized]
 * @property {string} [wsBaseUrl]
 */
/**
 * @typedef {Object} ResolvedConfig
 * @property {string} baseUrl
 * @property {string} apiPrefix
 * @property {typeof fetch} fetch
 * @property {() => typeof WebSocket} getWebSocket
 * @property {object} storage
 * @property {object} auth
 * @property {object} logger
 * @property {(error: *) => void} onUnauthorized
 * @property {string} [wsBaseUrl]
 */
/**
 * Normalizes raw `createClient()` options into a stable internal config
 * object. `globals` is injectable so callers/tests can simulate a runtime
 * with no ambient browser globals; real callers should leave it as the
 * default (`globalThis`).
 * @param {CreateClientOptions} [options]
 * @param {Record<string, *>} [globals]
 * @returns {Readonly<ResolvedConfig>}
 */
export function resolveConfig(options?: CreateClientOptions, globals?: Record<string, any>): Readonly<ResolvedConfig>;
export type CreateClientOptions = {
    baseUrl?: string;
    apiPrefix?: string;
    fetch?: typeof fetch;
    WebSocket?: typeof WebSocket;
    /**
     * - `Storage`-like: `getItem`/`setItem`/`removeItem`
     */
    storage?: object;
    /**
     * - see `sdk/auth/`
     */
    auth?: object;
    /**
     * - `{debug, info, warn, error}`
     */
    logger?: object;
    onUnauthorized?: (error: any) => void;
    wsBaseUrl?: string;
};
export type ResolvedConfig = {
    baseUrl: string;
    apiPrefix: string;
    fetch: typeof fetch;
    getWebSocket: () => typeof WebSocket;
    storage: object;
    auth: object;
    logger: object;
    onUnauthorized: (error: any) => void;
    wsBaseUrl?: string;
};
