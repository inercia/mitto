# prompt-stream

Runnable proof that the [Mitto Go client](../../../pkg/api) is usable from an
external Go program: authenticates with a shared bearer token, creates a
conversation, sends one prompt, and streams the agent's response to stdout.

```sh
go run ./examples/go-client/prompt-stream \
    -url http://localhost:8080 -token "$MITTO_TOKEN" \
    -dir /path/to/project -prompt "What does this project do?"
```

Press Ctrl-C to cancel early. The created conversation is deleted on exit.
See [docs/api/go-sdk.md](../../../docs/api/go-sdk.md) for the full Go SDK
guide.
