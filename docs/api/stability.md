# Versioning & Stability

This page is a short, consumer-facing summary. The authoritative records
are [JS Client Library §5-§7](../devel/js-client-library.md#5-public-vs-internal-boundary)
and [API Stability Tiers and Deprecation Policy](../devel/api-stability.md)
— read those for the full policy and the deprecation register.

## The only supported surface is `index.js`

Everything `web/static/sdk/index.js` re-exports is public. Everything else
(`sdk/core/`, `sdk/resources/*.js`, `sdk/realtime/*.js`, `sdk/auth/*.js`,
etc.) is a deep import and may change in any release without notice —
enforced by the npm `exports` map (deep paths unresolvable at install time)
and lint rules blocking deep `sdk/*` imports from outside `sdk/` itself.

## What must not break in a minor release

- `index.js` export names and signatures
- Error class names and `code` values (`ConfigError` →
  `invalid_config`, `MittoNetworkError` → `network_error`, the
  `MittoApiError`/`MittoAuthError` status→code mapping — see [Errors](errors.md))
- Realtime event names (`EVENTS`, `COMMANDS`, `LEGACY_EVENTS` values)

## What may break in a minor release

- Any internal module or deep-import path
- The shape of an error's `.details` payload
- Anything not re-exported from `index.js`

## `VERSION` and the embedded vs. npm split

```js
import { VERSION } from "/sdk/index.js";
```

Mitto has no compiled-in server version string — releases are git tags
only. The **embedded copy** (served at `/sdk/index.js`) is always
lockstep with the server: its `VERSION` _is_ the release tag the server
ships inside, so there is no compatibility matrix to maintain. A future
**npm-published copy** would carry its own independent semver, cut from a
specific Mitto tag, declaring the minimum server version it requires. Both
forms expose the same `VERSION` constant so a client pairing a mismatched
SDK/server combination can detect it at runtime.

## Endpoint-level stability tiers

Individual REST/WebSocket/SDK surfaces additionally carry a
`stable`/`experimental`/`internal` tier (plus the `external-stable`
marker for paths that cannot even be renamed) — this is a separate,
endpoint-granular concern from the SDK-wide promise above. See
[API Stability Tiers and Deprecation Policy](../devel/api-stability.md)
for the tier definitions, deprecation windows, and the register.
