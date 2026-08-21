/**
 * `sharedTokenAuth` — bearer-token auth adapter for programmatic clients
 * (CLI tools, scripts, third-party integrations) authenticating against the
 * deployment-wide shared token (mitto-7gta.26,
 * `internal/web/middleware/auth.go`'s `ValidateBearerRequest`). Sends
 * `Authorization: Bearer <token>` on REST requests and — where the runtime
 * supports it — on the WebSocket handshake. Environment-agnostic per
 * docs/devel/js-client-library.md §4: never touches
 * `window`/`document`/`localStorage`/bare `console.*`.
 *
 * The backend accepts the token ONLY via the `Authorization` header, on
 * both REST and the WS upgrade — no query-parameter fallback exists or is
 * added here (see `extractBearerToken` in auth.go: a query param would only
 * serve browsers, which already have cookies, and would leak the secret
 * into URLs and logs). Browsers cannot set custom headers on a WebSocket
 * handshake, so this adapter's `authorizeWebSocket()` is only actionable by
 * non-browser `WebSocket` implementations (e.g. Node's `ws`, which honours
 * a `{ headers }` third constructor argument) — browser hosts pass `noneAuth`
 * or `browserCookieAuth` for realtime instead.
 *
 * Because a bearer-authenticated request needs no session cookie or CSRF
 * token (server-side: CSRF is bypassed only once the token itself
 * validates), this adapter never sets `credentials` and never fetches a
 * CSRF token.
 */

/**
 * @param {object} options
 * @param {function(): (string|Promise<string>)} options.getToken - Token
 *   supplier, NOT a string: callers source it lazily from an environment
 *   variable, a keychain, or a config file, so the token is never captured
 *   at adapter-construction time. May be sync or async. A falsy/empty
 *   return means "no credential available" — the adapter sends the request
 *   unauthenticated rather than a literal "Bearer undefined", letting the
 *   server answer with its normal 401.
 * @returns {{authorize: function(object): Promise<object>, authorizeWebSocket: function(object): Promise<object>}}
 */
export function sharedTokenAuth({ getToken }) {
  async function resolveToken() {
    const tok = await getToken();
    return tok || null;
  }

  return {
    /** Adds `Authorization: Bearer <token>` when a token is available. */
    async authorize(_request) {
      const tok = await resolveToken();
      if (!tok) return {};
      return { headers: { Authorization: `Bearer ${tok}` } };
    },

    /**
     * Best-effort WebSocket credential: returns constructor `options` a
     * non-browser `WebSocket` implementation can pass an `Authorization`
     * header through. Browser `WebSocket` implementations ignore extra
     * constructor arguments, so this is a no-op there by construction —
     * never degrades to a query-string token.
     */
    async authorizeWebSocket(_request) {
      const tok = await resolveToken();
      if (!tok) return {};
      return { options: { headers: { Authorization: `Bearer ${tok}` } } };
    },
  };
}
