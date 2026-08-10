/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createDashboardResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/dashboard.
     * @param {object} [params] - {limit?} — server default 5, max 50
     * @param {object} [opts] - forwarded to request() (e.g. headers, signal)
     */
    summary: (params?: object, opts?: object) => Promise<any>;
    /**
     * GET /api/dashboard/timeseries.
     *
     * @param {object} [params] - {range?: "24h"|"7d"|"30d", bucket?:
     *   "hour"|"day", metrics?: string|string[], workspace?, groupBy?:
     *   "model"}. The server (handlers/dashboard_timeseries.go) parses
     *   `metrics` with `strings.Split(raw, ",")`, but transport's `qs()`
     *   emits array values as repeated `key=v` params — so an array-valued
     *   `metrics` is comma-joined here before being handed to `call()`; a
     *   string value is passed through untouched.
     * @param {object} [opts] - forwarded to request() (e.g. headers, signal)
     */
    timeseries: (params?: object, opts?: object) => Promise<any>;
};
