/**
 * Dashboard REST resource module (mitto-7gta.12).
 *
 * `createDashboardResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7/.10 precedent: raw relative paths mirroring
 * `internal/web/routes.go`, never built through `core/endpoints.js`.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.dashboard`.
 */
import { request } from "../core/transport.js";

/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createDashboardResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/dashboard.
     * @param {object} [params] - {limit?} — server default 5, max 50
     * @param {object} [opts] - forwarded to request() (e.g. headers, signal)
     */
    summary: (params, opts) => call("GET", "/api/dashboard", { query: params, ...opts }),

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
    timeseries: (params, opts) => {
      const query = { ...params };
      if (Array.isArray(query.metrics)) {
        query.metrics = query.metrics.join(",");
      }
      return call("GET", "/api/dashboard/timeseries", { query, ...opts });
    },
  };
}
