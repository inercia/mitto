# Getting Started

## Browser, in-tree (same origin as the Mitto UI)

`fetch` and `WebSocket` are ambient browser globals, so an empty config
works — the SDK resolves them from `globalThis`:

```html
<script type="module">
  import { createClient } from "/sdk/index.js";

  const client = createClient({ baseUrl: "/api" });
  const sessions = await client.sessions.list();
  console.log(sessions);
</script>
```

## Browser, third party (different origin)

Requires the target's [CORS allowlist](../config/web/README.md#cors-and-cross-origin-access)
to include your origin, and a
[shared bearer token](authentication.md#sharedtokenauth-bearer-token) since
cookies are never sent cross-origin:

```js
import {
  createClient,
  sharedTokenAuth,
} from "https://mitto.example.com/sdk/index.js";

const client = createClient({
  baseUrl: "https://mitto.example.com/api",
  auth: sharedTokenAuth({ getToken: () => "your-shared-secret" }),
});

const sessions = await client.sessions.list();
```

## Node / Bun

Node ≥ 18 and Bun both ship global `fetch`; only `WebSocket` needs an
explicit implementation if you plan to use realtime streams (e.g. the `ws`
package, or Bun's built-in `WebSocket`). Vendor or fetch the `sdk/` tree —
it is plain ESM with zero runtime dependencies:

```js
import { createClient, sharedTokenAuth } from "./sdk/index.js";
import WebSocket from "ws";

const client = createClient({
  baseUrl: "http://localhost:8080/api",
  wsBaseUrl: "ws://localhost:8080",
  WebSocket,
  auth: sharedTokenAuth({ getToken: () => process.env.MITTO_TOKEN }),
});

const sessions = await client.sessions.list();
```

See [Authentication](authentication.md) for enabling the shared token on the
server side, and [Client Configuration](client.md) for every `createClient()`
option.

## First REST call

Every resource method is a thin wrapper returning a `Promise` of the
decoded JSON body (or throwing a typed error — see [Errors](errors.md)):

```js
const running = await client.sessions.running();
const issue = await client.issues.show("mitto-123", {
  working_dir: "/path/to/repo",
});
```

See the [REST API Reference](rest.md) for the full resource list.

## First realtime stream

```js
const stream = client.sessionStream("20260101-120000-abc123");
stream.on("message", (msg, meta) => console.log(msg.type, meta));
stream.on("open", () => console.log("connected"));
stream.connect();

// Later:
await stream.sendPrompt({ message: "Hello!" });
```

See the [Realtime Guide](realtime.md) for the full event/session-stream API,
sequence numbers, and reconnection behavior.

## Runnable examples

- [`examples/js-client/browser-snippet`](../../examples/js-client/browser-snippet) —
  standalone HTML page: lists conversations and streams one prompt's
  response.
- [`examples/js-client/prompt-stream`](../../examples/js-client/prompt-stream) —
  Bun/Node CLI: authenticates with a shared bearer token, creates a
  conversation, sends a prompt, and streams the response to stdout. Proves
  the SDK is genuinely environment-agnostic.

See [Go SDK](go-sdk.md#runnable-examples) for the equivalent Go examples.
