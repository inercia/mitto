# Mock ACP Server

A mock implementation of the Agent Communication Protocol (ACP) server for testing Mitto.

## Purpose

This mock server provides deterministic, repeatable responses for testing without requiring a real AI agent. It:

- Implements the ACP JSON-RPC protocol over stdin/stdout
- Responds based on configurable scenarios
- Simulates streaming responses with configurable delays
- Supports tool calls and permission requests

## Building

```bash
# From project root
make build-mock-acp

# Or directly
go build -o tests/mocks/acp-server/mock-acp-server ./tests/mocks/acp-server
```

## Usage

The mock server is typically started by Mitto as a subprocess, but can be run manually for testing:

```bash
./mock-acp-server [options]
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--scenarios` | auto-detect | Directory containing scenario JSON files |
| `--delay` | 50ms | Default delay between response chunks |
| `--verbose` | false | Enable verbose logging to stderr |

### Example

```bash
./mock-acp-server --scenarios ../fixtures/responses --verbose
```

## Scenarios

Scenarios are JSON files that define how the mock server responds to prompts.

### Scenario Format

```json
{
  "scenario": "scenario-name",
  "description": "Description of this scenario",
  "responses": [
    {
      "trigger": {
        "type": "prompt",
        "pattern": "(?i)hello"
      },
      "actions": [
        {
          "type": "agent_message",
          "delay_ms": 100,
          "chunks": ["Hello! ", "How can I help?"]
        }
      ]
    }
  ]
}
```

### Trigger Types

- `prompt` - Matches against user prompt text using regex

### Action Types

| Type | Description |
|------|-------------|
| `agent_message` | Send agent message chunks |
| `agent_thought` | Send agent thought |
| `tool_call` | Start a tool call |
| `tool_update` | Update tool call status |
| `delay` | Wait for specified time |
| `error` | Simulate an error |

### Example Scenarios

See `tests/fixtures/responses/` for example scenarios:

- `simple-greeting.json` - Basic greeting responses
- `file-list.json` - File listing with tool calls
- `error-response.json` - Error simulation

## Protocol

The mock server implements the ACP protocol:

### Initialization

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0.1.0"}}
```

### New Session

```json
{"jsonrpc":"2.0","id":2,"method":"acp/newSession","params":{"workingDirectory":"/path"}}
```

### Prompt

```json
{"jsonrpc":"2.0","id":3,"method":"acp/prompt","params":{"sessionId":"...","message":"Hello"}}
```

### Session Updates (Notifications)

The server sends notifications for streaming responses:

```json
{"jsonrpc":"2.0","method":"acp/sessionUpdate","params":{"sessionId":"...","update":{"agentMessageChunk":{"content":{"text":{"text":"Hello"}}}}}}
```

## Development

### Adding New Scenarios

1. Create a JSON file in `tests/fixtures/responses/`
2. Define triggers and actions
3. The mock server loads scenarios on startup

### Extending the Mock

- `main.go` - Entry point and server setup
- `types.go` - Protocol type definitions
- `handler.go` - Request handlers and scenario matching
- `sender.go` - Response sending utilities

