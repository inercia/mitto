# Testing Patterns & Lessons Learned

## Mock ACP Server (`tests/mocks/acp-server/`)

### Structure
- `main.go` — Entry point, scenario loading, stdin/stdout read loop
- `types.go` — All protocol types (JSON-RPC, ACP messages, content blocks, scenarios)
- `handler.go` — Request handlers (initialize, new_session, prompt, set_mode, cancel, shutdown)
- `sender.go` — Thread-safe JSON-RPC message sending

### Content Blocks
The mock server's `ContentBlock` supports both text and image:
```go
type ContentBlock struct {
    Type     string `json:"type,omitempty"`
    Text     string `json:"text,omitempty"`
    Data     string `json:"data,omitempty"`     // base64 image data
    MimeType string `json:"mimeType,omitempty"`
}
func (c *ContentBlock) IsImage() bool { return c.Type == "image" && c.Data != "" }
```

### Agent Capabilities
The mock advertises capabilities in `handleInitialize()`:
```go
result.AgentCapabilities.Streaming = true
result.AgentCapabilities.PromptCapabilities.Image = true
```

### Scenario Matching
Prompt text is matched against regex patterns in `tests/fixtures/responses/*.json`. If images are detected, the mock responds with an acknowledgment before checking scenarios.

### Rebuilding After Changes
Always rebuild after modifying mock server code:
```bash
go build -o tests/mocks/acp-server/mock-acp-server ./tests/mocks/acp-server/
```

## Integration Test Patterns

### Test Setup
```go
//go:build integration

func TestMyFeature(t *testing.T) {
    ts := SetupTestServer(t)  // Creates in-process server with mock ACP
    session, _ := ts.Client.CreateSession(client.CreateSessionRequest{})
    defer ts.Client.DeleteSession(session.SessionID)
    // ... test logic
}
```

### WebSocket Test Flow
1. `CreateSession()` — REST API
2. `UploadImage()` (if needed) — multipart POST
3. `Connect(ctx, sessionID, callbacks)` — WebSocket
4. `ws.LoadEvents(50, 0, 0)` — **Required** to register as observer
5. `ws.SendPrompt("message")` or `ws.SendPromptWithImages("msg", imageIDs)`
6. `waitFor(t, timeout, condition, "description")` — poll until prompt_complete
7. Assert on collected callback data

### Minimal Test PNG
For image tests, use a minimal 1x1 transparent PNG (67 bytes):
```go
minimalPNG := []byte{
    0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
    // ... IHDR, IDAT, IEND chunks
}
```

## Test Commands
```bash
go test ./internal/web/...                                    # Web package unit tests
go test ./internal/client/...                                 # Client package tests
go test -v -tags integration ./tests/integration/inprocess/   # All integration tests
go test -v -tags integration -run TestName ./tests/integration/inprocess/  # Single test
```

## Lessons Learned
- **Always build mock ACP** before running integration tests
- **`LoadEvents` is required** — without it, the WebSocket client isn't registered as an observer and receives no streaming events
- **Pre-existing failures**: Check `git stash && test && git stash pop` to verify failures aren't from other uncommitted changes
- **Edit string matching**: When using surgical edits on large files, read the exact lines first to get the precise string — whitespace and comments must match exactly
- **Cascading compiler errors**: In Go, one undefined field can cause phantom errors on valid fields in the same struct. Fix the root cause first.
