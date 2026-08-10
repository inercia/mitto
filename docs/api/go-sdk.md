# Mitto Go SDK

Consumer-facing documentation for `github.com/inercia/mitto/pkg/api`
(package `client`), the official Go client for the Mitto REST API and
WebSocket protocol.

This is the **usage** layer. For the SDK's design rationale and stability
rules, see [Go Client Library](../devel/go-client-library.md) (design
decision record).

For the sibling JavaScript SDK, see [docs/api/README.md](README.md).

## Install

```sh
go get github.com/inercia/mitto/pkg/api
```

```go
import client "github.com/inercia/mitto/pkg/api"
```

## First call

```go
c := client.New("http://localhost:8080")
sessions, err := c.ListSessions()
```

## Authentication

By default the client is unauthenticated. For a shared bearer token:

```go
c := client.New(baseURL, client.WithBearerToken(token))
```

See the full list of modes (including cookie login and token rotation via
`WithTokenSupplier`) in the package doc's "# Authentication" section.

## Streaming

```go
sess, err := c.Connect(ctx, sessionID, client.SessionCallbacks{})
sess.SendPrompt("Hello, world!")
for ev, err := range sess.Events(ctx) {
    if ev.Kind == client.EventAgentMessage {
        fmt.Print(ev.HTML)
    }
    if ev.Kind == client.EventPromptComplete {
        break
    }
}
```

## Error model

Non-2xx responses are `*client.APIError`. Branch with `errors.Is` against
the package's sentinels (`ErrNotFound`, `ErrUnauthenticated`, ...) or
`errors.As` for full detail (`Status`, `Code`, `Message`, `Details`).

## Runnable examples

- [`examples/go-client/list-conversations`](../../examples/go-client/list-conversations) —
  minimal program that lists every conversation.
- [`examples/go-client/prompt-stream`](../../examples/go-client/prompt-stream) —
  authenticates, creates a conversation, sends a prompt, and streams the
  response to stdout.

Both are plain `go run`-able programs inside this repository's module, so
they are compiled (and API drift is caught) by the normal build/lint/test.

## Full package documentation

`go doc github.com/inercia/mitto/pkg/api` (or the equivalent godoc page)
covers construction and options, the conversation lifecycle, context
conventions, resilient realtime (reconnect/keepalive/dedup), and thread
safety guarantees in full — see [`pkg/api/doc.go`](../../pkg/api/doc.go).

## Related design documentation

- [Go Client Library](../devel/go-client-library.md) — package layout,
  object model, options pattern, error model, semver/stability policy
- [REST API Conventions](../devel/rest-api-conventions.md) — path naming,
  HTTP methods, error envelope, endpoint mapping
- [WebSocket Protocol](../devel/websockets/) — the authoritative wire
  protocol this SDK's realtime layer implements
