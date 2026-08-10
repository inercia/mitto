# Mitto JavaScript SDK

Consumer-facing documentation for `@mitto/sdk` (`web/static/sdk/`), the
official JavaScript client for the Mitto REST API and WebSocket protocol.

This is the **usage** layer. For the SDK's design rationale and stability
rules, see [JS Client Library](../devel/js-client-library.md) (design
decision record) and [API Stability Tiers](../devel/api-stability.md).

## What it is

- **Zero dependencies, plain ESM, no build step.** One file (`index.js`)
  plus its imports, served directly by the Mitto server.
- **Environment-agnostic.** The same code runs in a browser, Node, or Bun —
  `fetch`, `WebSocket`, storage, auth and logging are all injected.
- **Always version-matched with the server it ships with.** The embedded
  copy at `/sdk/index.js` is never out of sync with the API it talks to.

## The public surface

`web/static/sdk/index.js` is the **only** supported import. Everything
under `sdk/core/`, `sdk/resources/`, `sdk/realtime/`, etc. is an internal
deep import and may change in any release without notice — see
[Stability](stability.md).

## Three ways to consume it

1. **Browser, in-tree.** The Mitto UI imports directly from `/sdk/index.js`.
2. **Browser, third party.**
   ```js
   import { createClient } from "https://your-mitto-host/sdk/index.js";
   ```
3. **Node / Bun.** Vendor or fetch the `sdk/` tree — plain ESM, zero runtime
   dependencies, no bundler needed.

See [Getting Started](getting-started.md) for runnable snippets of each.

## Documentation map

| Page                                   | Covers                                                                        |
| -------------------------------------- | ----------------------------------------------------------------------------- |
| [Getting Started](getting-started.md)  | Minimal browser/Node/Bun snippets, first REST call, first stream              |
| [Client Configuration](client.md)      | `createClient()` options, the returned client shape, `ResolvedConfig`         |
| [Authentication](authentication.md)    | Cookie+CSRF, shared bearer token, none, Cloudflare Access                     |
| [REST API Reference](rest.md)          | Every resource namespace, method → endpoint mapping, request options, caching |
| [Realtime Guide](realtime.md)          | `SessionStream`, `EventsStream`, sequence numbers, reconnection, dedup        |
| [Error Model](errors.md)               | The error class taxonomy and the REST error envelope                          |
| [Versioning & Stability](stability.md) | What may/may not change in a minor release                                    |

## Related design documentation

- [JS Client Library](../devel/js-client-library.md) — package layout,
  distribution, environment-agnostic contract, public/internal boundary
- [REST API Conventions](../devel/rest-api-conventions.md) — path naming,
  HTTP methods, error envelope, endpoint mapping
- [WebSocket Protocol](../devel/websockets/) — the authoritative wire
  protocol this SDK's realtime layer implements
- [API Stability Tiers](../devel/api-stability.md) — endpoint-level
  stability tiers and deprecation windows
