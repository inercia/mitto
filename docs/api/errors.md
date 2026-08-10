# Error Model

Every error the SDK itself throws extends `MittoError`, so callers can do a
single `instanceof MittoError` check without enumerating every subtype.
`MittoError` is never thrown directly. Class names and `code` values are
pinned by the SDK's stability promise — see [Stability](stability.md).

```js
import {
  MittoError,
  ConfigError,
  MittoApiError,
  MittoAuthError,
  MittoNetworkError,
} from "/sdk/index.js";
```

## `ConfigError`

Thrown by `resolveConfig()`/`createClient()` for invalid options: an
unknown option key, or a required environment capability (`fetch`, or
`WebSocket` when a realtime feature needs it) that could not be resolved
from injected options or ambient globals. `code: "invalid_config"`.

## `MittoApiError`

Thrown when the server answers with a non-2xx HTTP status. Carries the
parsed error envelope so callers can branch on `code` without
string-matching `message`:

```js
try {
  await client.issues.show(id, { working_dir });
} catch (err) {
  if (err instanceof MittoApiError) {
    console.log(err.status, err.code, err.details, err.body);
  }
}
```

| Field     | Meaning                                          |
| --------- | ------------------------------------------------ |
| `status`  | HTTP status code                                 |
| `code`    | canonical error code (see table below)           |
| `details` | server-supplied structured context, when present |
| `body`    | the parsed (or raw text) response body           |

`code` mirrors the [REST error envelope](../devel/rest-api-conventions.md#4-error-envelope):

```json
{
  "error": {
    "code": "not_found",
    "message": "Session ... not found.",
    "details": {}
  }
}
```

| HTTP status   | `code`               |
| ------------- | -------------------- |
| 400           | `bad_request`        |
| 401           | `unauthenticated`    |
| 403           | `forbidden`          |
| 404           | `not_found`          |
| 405           | `method_not_allowed` |
| 409           | `conflict`           |
| 413           | `too_large`          |
| 429           | `rate_limited`       |
| 503           | `unavailable`        |
| other non-2xx | `server_error`       |

Some legacy endpoints (e.g. `POST /api/callback/{token}`) answer a flat
`{"error": "code", "message": "..."}` shape instead of the nested envelope
— `MittoApiError` normalizes both into the same `.code`/`.body` fields, so
callers never need to know which shape the server used.

**`.details`'s shape is not stable** — it may change per error code without
a major version; only `.status` and `.code` are part of the stability
promise.

## `MittoAuthError`

A specialization of `MittoApiError` for 401/403 responses, so callers that
only care about "the request failed" still catch it via `MittoApiError`,
while auth adapters and the `onUnauthorized` hook can branch on it
precisely. See [Authentication](authentication.md#the-onunauthorized-hook).

## `MittoNetworkError`

Thrown when the request never produced a response: `fetch` rejected, the
request was aborted, or a lower-level network failure (DNS/TLS/offline)
occurred, and also by `SessionStream`/`EventsStream` on connection/delivery
timeouts. `code: "network_error"`; carries the original error as `.cause`
when available.

## What is never thrown

A malformed or empty error response body never masks the real failure — the
SDK falls back to a generic `Request failed with status <n>` message rather
than throwing a JSON-decode error.
