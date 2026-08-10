# list-conversations

Minimal example of the [Mitto Go client](../../../pkg/api): lists every
conversation known to a Mitto server.

```sh
go run ./examples/go-client/list-conversations -url http://localhost:8080
```

Pass `-token` (or set `MITTO_TOKEN`) if the server requires a shared bearer
token. See [docs/api/go-sdk.md](../../../docs/api/go-sdk.md) for the full
Go SDK guide.
