# Mitto — Claude Code Project Memory

Mitto is a multi-agent interface for AI coding agents (Claude Code, Auggie, Cursor) with CLI, Web UI, and native macOS app. It communicates with agents via the Agent Communication Protocol (ACP).

## Quick Reference

```bash
make build                # Build CLI binary
make build-mock-acp       # Build mock ACP server (required before integration tests)
make build-mac-app        # Build macOS app bundle
make test                 # All unit tests (Go + JS)
make test-go              # Go unit tests only
make test-js              # JavaScript unit tests
make test-integration     # Integration tests (needs mock-acp built first)
make test-ui              # Playwright UI tests
make lint                 # Run golangci-lint
```

## Architecture Overview

- **Entry points**: `cmd/mitto/` (CLI), `cmd/mitto-app/` (macOS native app)
- **Go packages**: All in `internal/` — never import `internal/cmd` from other packages
- **Frontend**: Preact/HTM in `web/static/` (components, hooks, utils)
- **Tests**: `tests/integration/` (Go), `tests/ui/` (Playwright), `tests/mocks/` (mock ACP server)
- **Docs**: `docs/devel/` has detailed architecture docs — consult before major changes
- **Existing AI rules**: `.augment/rules/*.md` has 26 detailed rule files — check before adding patterns

## Core Data Flow

```
Frontend (Preact) ←WebSocket→ BackgroundSession ←JSON-RPC/stdio→ ACP Agent (Claude Code CLI)
```

- `internal/web/background_session.go` — The central hub. Bridges WebSocket clients to ACP agents via the observer pattern.
- `internal/web/session_ws.go` — WebSocket connection handler, sends `connected` message with session metadata.
- `internal/web/observer.go` — `SessionObserver` interface (OnAgentMessage, OnError, OnToolCall, etc.)
- `internal/acp/` — ACP protocol client wrapping `github.com/coder/acp-go-sdk`

## Key Patterns

### Observer Notification Pattern
```go
bs.notifyObservers(func(o SessionObserver) {
    o.OnError("message to user")
})
```

### ACP ContentBlock (Discriminated Union)
The ACP SDK uses nil-pointer checks, NOT a Type() method:
```go
for _, block := range blocks {
    if block.Image != nil { /* image block */ }
    else if block.Text != nil { /* text block */ }
}
```

### Agent Capabilities
Capabilities are advertised during ACP initialization. Always check before using:
```go
caps := resp.AgentCapabilities
bs.agentSupportsImages = caps.PromptCapabilities.Image
// Later in PromptWithMeta:
if len(imageIDs) > 0 && !bs.agentSupportsImages { /* warn but send anyway */ }
```

### Frontend Capability Flow
Backend → WebSocket `connected` message → `useWebSocket.js` stores in session.info → `app.js` passes as prop → Component uses it:
```javascript
// useWebSocket.js: store from connected message
agent_supports_images: msg.data.agent_supports_images ?? false,
// app.js: pass to component
agentSupportsImages=${sessionInfo?.agent_supports_images ?? false}
```

## Testing

### Integration Tests (In-Process)
```bash
# Build mock first, then run
go build -o tests/mocks/acp-server/mock-acp-server ./tests/mocks/acp-server/
go test -v -tags integration ./tests/integration/inprocess/
```

- Tests use `SetupTestServer(t)` which creates an in-process web server with mock ACP
- Mock ACP server communicates via stdin/stdout JSON-RPC
- Scenarios are regex-matched in `tests/fixtures/responses/*.json`
- Build tag: `//go:build integration`

### Test Client (`internal/client/`)
- `CreateSession()`, `Connect()` (WebSocket), `SendPrompt()`, `SendPromptWithImages()`
- `UploadImage()` — multipart POST to `/api/sessions/{id}/images`
- `LoadEvents()` — must be called after Connect to register as observer

### Pre-existing Test Failures
- `TestWSConn_ForceReconnect_AppliesBackoff` may fail from uncommitted working tree changes — verify with `git stash` before blaming your changes

## Common Gotchas

- **SessionManager fields**: The map is `activeSessions` (not `sessions`). Methods use receiver `sm`, but `session_ws.go` uses `s`.
- **Go compiler cascading errors**: An undefined field reference can cause phantom "no field or method" errors on valid fields in the same struct. Fix the root cause first.
- **Image pipeline**: Upload → disk storage → base64 encode on prompt → ACP ContentBlock. Images are NOT stored in the WebSocket message — only `image_ids` are sent, backend loads from disk.
- **Log rotation gaps**: Server logs rotate and can have gaps. When debugging historical issues, check `events.jsonl` in the session directory as the authoritative record.
- **Build the mock ACP server**: Always run `make build-mock-acp` before integration tests. The binary at `tests/mocks/acp-server/mock-acp-server` must exist.

## File Modification Checklist

When adding new agent capabilities:
1. Store capability on `BackgroundSession` during ACP init
2. Add public getter method
3. Check capability before using the feature in `PromptWithMeta`
4. Send user notification via `OnError` if feature unavailable
5. Add to WebSocket `connected` message in `sendSessionConnected()`
6. Store in `useWebSocket.js` session info from `connected` handler
7. Pass as prop through `app.js` to the relevant component
8. Update mock ACP server types and handler for testing
9. Write integration test proving end-to-end flow


<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->
