---
description: MCP server development patterns and tool implementation
globs:
  - "internal/mcpserver/**/*"
keywords:
  - MCP server
  - MCP tool
  - AddTool
  - mcp-go
  - Streamable HTTP
  - STDIO mode
  - session registration
  - session_id parameter
---

# MCP Server Development

The Mitto MCP server (`internal/mcpserver/`) provides a **single global server** with two types of tools:

1. **Global tools** - Always available (list conversations, get config, runtime info)
2. **Session-scoped tools** - Require `session_id` parameter (UI prompts, send prompt, get current session)

## Architecture

All agents connect to the same MCP server at `http://127.0.0.1:5757/mcp`. Session-scoped tools use a `session_id` parameter to identify the target session.

```
┌─────────────────────────────────────────────────────────────┐
│                    Global MCP Server                        │
│                  http://127.0.0.1:5757/mcp                  │
├─────────────────────────────────────────────────────────────┤
│  Global Tools (no session_id):                              │
│  • mitto_conversation_list (always available)               │
│  • mitto_get_config                                         │
│  • mitto_get_runtime_info                                   │
├─────────────────────────────────────────────────────────────┤
│  Session-Scoped Tools (require session_id):                 │
│  • mitto_conversation_get_current                           │
│  • mitto_conversation_send_prompt                           │
│  • mitto_ui_ask_yes_no / _options_buttons / _options_combo  │
├─────────────────────────────────────────────────────────────┤
│  Session Registry: Maps session_id → UIPrompter             │
└─────────────────────────────────────────────────────────────┘
```

## Transport Modes

| Mode               | Use Case                          | Configuration                                   |
| ------------------ | --------------------------------- | ----------------------------------------------- |
| **HTTP** (default) | Remote access, Auggie integration | `TransportModeHTTP`, runs on port 5757          |
| **STDIO**          | Subprocess usage, Claude Desktop  | `TransportModeSTDIO`, reads/writes stdin/stdout |

### HTTP Mode (Streamable HTTP)

```go
config := mcpserver.Config{
    Mode:    mcpserver.TransportModeHTTP,
    Address: "127.0.0.1:5757",
}
server := mcpserver.NewServer(config, sessionsDir, settingsPath)
server.Start(ctx)
```

Client configuration (Auggie/Claude):

```json
{
  "mcpServers": {
    "mitto-debug": {
      "url": "http://127.0.0.1:5757/mcp"
    }
  }
}
```

### STDIO Mode

```go
config := mcpserver.Config{
    Mode: mcpserver.TransportModeSTDIO,
}
server := mcpserver.NewServer(config, sessionsDir, settingsPath)
server.RunSTDIO(ctx)  // Blocks until context cancelled
```

Client configuration:

```json
{
  "mcpServers": {
    "mitto-debug": {
      "command": "/path/to/mitto",
      "args": ["mcp", "--stdio"]
    }
  }
}
```

## Adding New Tools

### Tool Handler Pattern

Tools must return a **struct** (not a slice or primitive) due to MCP SDK requirements:

```go
// ❌ WRONG: Returns slice - will panic
func (s *Server) handleListItems(ctx context.Context, req mcp.CallToolRequest) ([]Item, error) {
    return items, nil
}

// ✅ CORRECT: Returns struct wrapper
type ListItemsOutput struct {
    Items []Item `json:"items"`
}

func (s *Server) handleListItems(ctx context.Context, req mcp.CallToolRequest) (*ListItemsOutput, error) {
    return &ListItemsOutput{Items: items}, nil
}
```

### Registering Tools

```go
func (s *Server) registerTools() {
    // Tool with no input parameters
    mcp.AddTool(s.mcpServer, mcp.Tool{
        Name:        "mitto_get_runtime_info",
        Description: "Get runtime information including OS, architecture, log file paths",
    }, s.handleGetRuntimeInfo)

    // Tool with input parameters
    mcp.AddTool(s.mcpServer, mcp.Tool{
        Name:        "mitto_conversation_get",
        Description: "Get details of a specific conversation",
        InputSchema: mcp.ToolInputSchema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "session_id": {
                    Type:        "string",
                    Description: "The session ID to retrieve",
                },
            },
            Required: []string{"session_id"},
        },
    }, s.handleGetConversation)
}
```

### Input Parameter Handling

```go
type GetConversationInput struct {
    SessionID string `json:"session_id"`
}

func (s *Server) handleGetConversation(ctx context.Context, req mcp.CallToolRequest) (*ConversationOutput, error) {
    var input GetConversationInput
    if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
        return nil, fmt.Errorf("invalid input: %w", err)
    }

    if input.SessionID == "" {
        return nil, fmt.Errorf("session_id is required")
    }

    // ... implementation
}
```

## Output Types

### Simple Output

```go
type RuntimeInfoOutput struct {
    OS           string            `json:"os"`
    Arch         string            `json:"arch"`
    LogFiles     map[string]string `json:"log_files"`
    SessionsDir  string            `json:"sessions_dir"`
}
```

### List Output

```go
type ListConversationsOutput struct {
    Conversations []ConversationInfo `json:"conversations"`
}

type ConversationInfo struct {
    SessionID     string    `json:"session_id"`
    Title         string    `json:"title"`
    SessionFolder string    `json:"session_folder"`
    MessageCount  int       `json:"message_count"`
    IsRunning     bool      `json:"is_running"`
    IsPrompting   bool      `json:"is_prompting"`
    LastSeq       int       `json:"last_seq"`
}
```

## Error Handling

Return errors with context:

```go
func (s *Server) handleGetConversation(ctx context.Context, req mcp.CallToolRequest) (*ConversationOutput, error) {
    // Validation errors
    if input.SessionID == "" {
        return nil, fmt.Errorf("session_id is required")
    }

    // Operation errors
    metadata, err := s.loadMetadata(input.SessionID)
    if err != nil {
        return nil, fmt.Errorf("failed to load session %s: %w", input.SessionID, err)
    }

    return &ConversationOutput{...}, nil
}
```

## Testing MCP Tools

Use the MCP tools directly to test:

```
# In an AI assistant with MCP configured
Call get_runtime_info_mitto-debug to verify the server is running
Call list_conversations_mitto-debug to see available sessions
```

See `40-mcp-debugging.md` for using MCP tools for debugging.

## Session Registration

Sessions register with the global MCP server to enable session-scoped tools.

### Registering a Session

```go
// In BackgroundSession.startSessionMcpServer()

// Register this session with the global MCP server
if bs.globalMcpServer != nil {
    bs.globalMcpServer.RegisterSession(bs.persistedID, bs, bs.logger)
}
```

### Unregistering a Session

```go
// In BackgroundSession.stopSessionMcpServer()

if bs.globalMcpServer != nil {
    bs.globalMcpServer.UnregisterSession(bs.persistedID)
}
```

### Session-Scoped Tool Pattern

Session-scoped tools require a `session_id` parameter and check permissions at runtime:

```go
// Input type includes session_id
type MyToolInput struct {
    SessionID string `json:"session_id"`
    // ... other parameters
}

func (s *Server) handleMyTool(
    ctx context.Context,
    req *mcp.CallToolRequest,
    input MyToolInput,
) (*mcp.CallToolResult, MyToolOutput, error) {
    // 1. Validate session_id is provided
    if input.SessionID == "" {
        return nil, MyToolOutput{}, fmt.Errorf("session_id is required")
    }

    // 2. Check session is registered (running)
    reg := s.getSession(input.SessionID)
    if reg == nil {
        return nil, MyToolOutput{}, fmt.Errorf("session not found: %s", input.SessionID)
    }

    // 3. Check permissions (if flag required)
    if !s.checkSessionFlag(input.SessionID, session.FlagMyFeature) {
        return nil, MyToolOutput{}, permissionError("my_tool", session.FlagMyFeature, "My Feature")
    }

    // 4. Use reg.uiPrompter for UI prompts
    // 5. Implement the tool logic
    return nil, output, nil
}
```

### Adding New Flags

1. Define the flag in `internal/session/flags.go`:

```go
const FlagNewFeature = "new_feature"

var AvailableFlags = []FlagDefinition{
    // ... existing flags ...
    {
        Name:        FlagNewFeature,
        Label:       "New Feature",
        Description: "Description for the UI",
        Default:     false,
    },
}
```

2. Check the flag in tool handlers:

```go
if !s.checkSessionFlag(input.SessionID, session.FlagNewFeature) {
    return nil, MyToolOutput{}, permissionError("my_tool", session.FlagNewFeature, "New Feature")
}
```

### Session Lifecycle

Sessions are registered/unregistered by `BackgroundSession`:

| Event | Action |
|-------|--------|
| Session start | `registerWithGlobalMCP()` registers session |
| Session archive | `unregisterFromGlobalMCP()` unregisters session |
| Session unarchive | `registerWithGlobalMCP()` re-registers session |
| Session delete | `unregisterFromGlobalMCP()` unregisters session |
| Server shutdown | All sessions automatically unregistered |

### Key Points

- **No per-session MCP servers** - All tools are on the global server
- **`session_id` parameter** - All session-scoped tools require this
- **Permission checks at runtime** - Use `checkSessionFlag()` helper
- **UI prompt routing** - Via registered `UIPrompter`

See `docs/devel/mcp.md` for detailed documentation.
