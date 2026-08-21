# Client Configuration

## `createClient(options)`

```js
import { createClient } from "/sdk/index.js";

const client = createClient({
  baseUrl, // host origin only, e.g. "" (same origin) or "https://host"
  apiPrefix, // string, rarely needed — see below
  fetch, // typeof fetch
  WebSocket, // typeof WebSocket
  storage, // { getItem, setItem, removeItem }
  auth, // see authentication.md
  logger, // { debug, info, warn, error }
  onUnauthorized, // (error) => void
  wsBaseUrl, // absolute ws(s):// or http(s):// origin
});
```

Every key is optional; an unknown key throws `ConfigError`. Defaults:

| Option           | Default                              | Notes                                                                                                                             |
| ---------------- | ------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------- |
| `baseUrl`        | `""`                                 | Host **origin** only — never include `/api`; resource paths already carry it. Trailing slash trimmed. Relative or absolute.       |
| `apiPrefix`      | `""`                                 | Prepended between `baseUrl` and the path; leading `/` added if missing.                                                           |
| `fetch`          | `globalThis.fetch` bound             | Throws `ConfigError` at request time if neither is available.                                                                     |
| `WebSocket`      | `globalThis.WebSocket`               | Resolved **lazily** — a REST-only caller is never forced to supply one; only realtime calls trigger the `ConfigError` if missing. |
| `storage`        | fresh in-memory `Map`-backed adapter | Never `localStorage` unless you opt in — see [`browserEnv()`](#browserenv-opt-in-browser-defaults).                               |
| `auth`           | `noneAuth()`                         | See [Authentication](authentication.md).                                                                                          |
| `logger`         | no-op `{debug,info,warn,error}`      | Never bare `console.*` internally.                                                                                                |
| `onUnauthorized` | no-op                                | Called with the `MittoAuthError` on every 401, in addition to any `auth.onUnauthorized`.                                          |
| `wsBaseUrl`      | `undefined`                          | Required for realtime features when `baseUrl` is relative (the common same-origin-browser case) — see [Realtime](realtime.md).    |

## `browserEnv()` — opt-in browser defaults

The SDK core never touches `localStorage`/`console`/`document` implicitly
(see [JS Client Library §4](../devel/js-client-library.md#4-environment-agnostic-contract)).
A browser host that wants `localStorage`-backed storage and console-backed
logging opts in explicitly:

```js
import { createClient, browserEnv } from "/sdk/index.js";

const client = createClient({ ...browserEnv() });
```

## The returned client shape

```js
{
  config,          // the resolved internal config (see below) — NOT server config
  endpoints,       // URL registry, see rest.md
  sessions, prompts, processors, shortcuts, issues,
  serverConfig,    // GET/POST /api/config + discovery endpoints (NOT `client.config`)
  files, images,   // `images` is an alias for `sessions.images`
  dashboard, misc, workspaces, acpServers, agents,
  sessionStream(sessionId, options), // -> SessionStream, see realtime.md
  eventsStream(options),             // -> EventsStream, see realtime.md
}
```

**Naming trap:** `client.config` is the _resolved SDK config_ you passed
into `createClient()` (see `ResolvedConfig` below) — it is not the Mitto
server's configuration. The server's `/api/config` resource is
`client.serverConfig`.

`client.images` is not a separate implementation; it is the same object as
`client.sessions.images` (avoids duplicating the session-scoped images
surface).

## `ResolvedConfig`

The frozen object passed to every resource module and stream:

```js
{
  baseUrl, apiPrefix,
  fetch,            // resolved fetch function
  getWebSocket,     // () => WebSocket implementation, throws ConfigError if none
  storage, auth, logger, onUnauthorized,
  wsBaseUrl,        // as supplied, possibly undefined
}
```

## `VERSION`

```js
import { VERSION } from "/sdk/index.js"; // e.g. "0.3.0"
```

The embedded copy's version is always the Mitto release tag it ships
inside — see [Stability](stability.md).
