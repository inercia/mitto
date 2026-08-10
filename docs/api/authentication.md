# Authentication

The `auth` option passed to `createClient()` is an adapter implementing up
to three methods:

```js
{
  async authorize({ method, url, headers }) { return { headers?, credentials? }; },
  async authorizeWebSocket({ url }) { return { protocols?, options? }; }, // optional
  onUnauthorized(error) {}, // optional
}
```

`authorize()` runs before every REST request; its return value is merged
into the request's headers/credentials. `authorizeWebSocket()` (optional)
runs before opening a realtime socket. `onUnauthorized()` (optional) is
called on every 401, before the client-level `onUnauthorized` hook.

Three adapters ship in `sdk/auth/`, all re-exported from `index.js`:

## `noneAuth()` — default

Adds nothing. For unauthenticated deployments, or hosts where auth is
handled entirely outside the SDK (e.g. a reverse proxy). This is the
default when `createClient()` receives no `auth` option.

## `browserCookieAuth(options)` — cookie + CSRF

The double-submit-cookie adapter used by the Mitto UI itself: the server
sets a CSRF token in a JS-readable cookie; this adapter echoes it back in
an `X-CSRF-Token` header on state-changing requests (`POST`/`PUT`/`PATCH`/`DELETE`).
Always sends `credentials: "include"` so the session cookie is attached.

```js
import {
  createClient,
  browserCookieAuth,
  browserCookieReader,
} from "/sdk/index.js";

const client = createClient({
  baseUrl: "/api",
  auth: browserCookieAuth({
    getCookie: browserCookieReader(), // reads document.cookie
    fetch: window.fetch.bind(window), // used only to fetch a fresh CSRF token
    csrfTokenUrl: "/api/csrf-token",
    // cookieName: "mitto_csrf" (default)
    // headerName: "X-CSRF-Token" (default)
  }),
});
```

`browserCookieReader()` is the one place in the SDK allowed to touch
`document.cookie`; it is not bundled into `browserEnv()` because
`browserCookieAuth` also needs `fetch` and `csrfTokenUrl`, which the preset
cannot know.

**Never redirects on 401 itself.** That is host policy: wire the
`onUnauthorized` option on `createClient()` to redirect, since the SDK only
raises a typed `MittoAuthError`.

## `sharedTokenAuth(options)` — bearer token

For programmatic clients (CLI tools, scripts, third-party integrations)
authenticating against the deployment-wide shared token. See
[Shared Token (Bearer) Authentication](../config/web/README.md#shared-token-bearer-authentication)
for enabling it server-side.

```js
import { createClient, sharedTokenAuth } from "/sdk/index.js";

const client = createClient({
  baseUrl: "https://mitto.example.com/api",
  auth: sharedTokenAuth({
    getToken: () => process.env.MITTO_TOKEN, // sync or async; lazy, never captured as a literal
  }),
});
```

- Sends `Authorization: Bearer <token>` on REST requests; never sets
  `credentials` and never fetches a CSRF token (bearer-authenticated
  requests need neither).
- `getToken` is a **supplier function, not a string** — source it from an
  env var, keychain, or config file so the token is never captured at
  adapter-construction time. A falsy/empty return sends the request
  unauthenticated, letting the server answer with its normal 401.
- **WebSocket handshake:** header-only, no query-string fallback. Only
  actionable by non-browser `WebSocket` implementations (e.g. Node's `ws`,
  which honours a `{ headers }` constructor option) — browsers cannot set
  custom headers on the handshake, so this is a no-op there. Browser hosts
  needing realtime with a shared token must use `noneAuth` or
  `browserCookieAuth` for the socket instead.
- Rotating the token invalidates every client immediately; there is no
  per-client revocation. Keep it out of shell history and URLs — see the
  server-side notes linked above.

## Cloudflare Access

When Mitto sits behind Cloudflare Access, authentication happens at the
edge before requests reach Mitto — see [External Access](../config/ext-access.md)
and its [Cloudflare Tunnel](../config/ext-access/cloudflare.md) guide. From
the SDK's perspective this is transparent: use `noneAuth()` (or omit `auth`
entirely) unless Mitto's own `simple`/`shared_token` auth is _also_ enabled
behind the tunnel, in which case use `browserCookieAuth`/`sharedTokenAuth`
as above.

## The `onUnauthorized` hook

`createClient({ onUnauthorized })` is called with the `MittoAuthError` on
every 401, in addition to the auth adapter's own optional
`onUnauthorized()`. This is where a browser host wires a redirect-to-login;
the SDK itself never redirects — see
[JS Client Library §4](../devel/js-client-library.md#4-environment-agnostic-contract).
