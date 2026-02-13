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
---

# MCP Server Development

The Mitto MCP server (`internal/mcpserver/`) provides debugging tools via the Model Context Protocol.

## Transport Modes

The server supports two transport modes:

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
        Name:        "get_runtime_info",
        Description: "Get runtime information including OS, architecture, log file paths",
    }, s.handleGetRuntimeInfo)

    // Tool with input parameters
    mcp.AddTool(s.mcpServer, mcp.Tool{
        Name:        "get_conversation",
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
