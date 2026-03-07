# ACP Protocol & SDK Patterns

## SDK: `github.com/coder/acp-go-sdk`

The ACP SDK is the protocol layer between Mitto and AI coding agents (Claude Code, Auggie).

### ContentBlock — Discriminated Union

The SDK's `ContentBlock` uses a struct with pointer fields (NOT a Type() method):

```go
type ContentBlock struct {
    Text         *ContentBlockText
    Image        *ContentBlockImage
    Audio        *ContentBlockAudio
    ResourceLink *ContentBlockResourceLink
    Resource     *ContentBlockResource
}
```

**Checking block type** — use nil checks:
```go
if block.Image != nil {
    // Access: block.Image.Data, block.Image.MimeType
} else if block.Text != nil {
    // Access: block.Text.Text
}
```

**Creating blocks** — use helper functions:
```go
acp.TextBlock("hello")
acp.ImageBlock(base64Data, "image/png")
acp.AudioBlock(base64Data, "audio/wav")
acp.ResourceLinkBlock(name, uri)
```

**Anti-pattern** — these do NOT exist:
```go
// WRONG: block.Type() doesn't exist
// WRONG: acp.ContentBlockTypeImage doesn't exist
// WRONG: acp.ContentBlockTypeText doesn't exist
```

### Agent Capabilities

Advertised during ACP initialization in `AgentCapabilities`:
```go
type AgentCapabilities struct {
    Streaming          bool
    LoadSession        bool
    PromptCapabilities PromptCapabilities
    McpCapabilities    McpCapabilities
}

type PromptCapabilities struct {
    Image           bool
    Audio           bool
    EmbeddedContext bool
}
```

**Critical rule**: Always check capabilities before sending unsupported content blocks. The agent may silently drop them.

### Connection Lifecycle
```
NewConnection(ctx, command, autoApprove, output, logger)
  → Initialize(ctx)         // Returns AgentCapabilities
  → NewSession(ctx, cwd)    // Returns SessionID + Modes
  → Prompt(ctx, blocks)     // Streaming response via callbacks
  → Close()
```

### Image Pipeline
1. Frontend uploads image via HTTP POST `/api/sessions/{id}/images`
2. Image stored on disk in session directory
3. WebSocket prompt message includes `image_ids: ["uuid1", "uuid2"]`
4. `PromptWithMeta` loads images from disk, base64 encodes
5. Creates `acp.ImageBlock(base64, mimeType)` content blocks
6. Sends blocks array to ACP agent via `Prompt()`

### JSON-RPC Transport
ACP uses JSON-RPC 2.0 over stdin/stdout of the agent subprocess:
- Requests: `{"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{...}}`
- Responses: `{"jsonrpc":"2.0","id":1,"result":{...}}`
- Notifications: `{"jsonrpc":"2.0","method":"session/update","params":{...}}`
- Session updates use a tagged union with `"sessionUpdate"` discriminator field
