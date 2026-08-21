# prompt-stream

Runnable proof that the [Mitto JS SDK](../../../web/static/sdk) is genuinely
environment-agnostic and that programmatic auth works end to end:
authenticates with a shared bearer token, lists conversations, creates one,
sends a prompt, and streams the agent's response to stdout. No
dependencies, no build step.

```sh
bun run examples/js-client/prompt-stream/main.js \
    --url http://localhost:8080 --token "$MITTO_TOKEN" \
    --dir /path/to/project --prompt "What does this project do?"
```

Also runs under Node, with one caveat: Node's built-in `WebSocket` cannot
set the `Authorization` header the realtime WS upgrade needs, so Node needs
the optional [`ws`](https://www.npmjs.com/package/ws) package installed
(`npm install ws`) — Bun needs nothing extra, since its native `WebSocket`
already supports it (via a small header-placement adapter in `main.js`).
The example detects the runtime automatically and fails with an actionable
message if `ws` is missing under Node.

Press Ctrl-C to cancel early. The created conversation is deleted on exit.
See [docs/api/getting-started.md](../../../docs/api/getting-started.md) and
[docs/api/realtime.md](../../../docs/api/realtime.md) for the full SDK
guide, and
[examples/go-client/prompt-stream](../../go-client/prompt-stream) for the
equivalent Go example.
