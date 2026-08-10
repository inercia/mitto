/**
 * Builds the endpoint registry for a given resolved config.
 * @param {object} config - resolved config (see core/config.js)
 * @param {object} [options]
 * @param {string} [options.wsBaseUrl] - absolute ws(s):// or http(s):// origin
 *   used to derive WebSocket URLs when `config.baseUrl` is relative/empty.
 * @returns {object} the endpoint registry
 */
export function createEndpoints(config: object, options?: {
    wsBaseUrl?: string;
}): object;
