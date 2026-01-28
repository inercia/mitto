---
description: ACP protocol client, SDK usage, connection lifecycle, and permission handling
globs:
  - "internal/acp/**/*"
---

# ACP Protocol Guidelines

## SDK Usage

- Import: `github.com/coder/acp-go-sdk`
- The `Client` struct implements `acp.Client` interface
- Use `acp.ClientSideConnection` for protocol handling
- Always pass context for cancellation support

## Connection Lifecycle

```go
conn, err := acp.NewConnection(ctx, command, autoApprove, output, logger)
defer conn.Close()

conn.Initialize(ctx)  // Protocol handshake
conn.NewSession(ctx, cwd)  // Create session
conn.Prompt(ctx, message)  // Send prompt
```

## Permission Handling

- Check `autoApprove` flag first
- Prefer "allow" options when auto-approving
- Display numbered options for manual selection
- Loop until valid input received

## Permission Helper Functions

The `permission.go` file provides helper functions for permission handling:

```go
// AutoApprovePermission selects the best "allow" option automatically
// Priority: AllowOnce > AllowAlways > first option
// Returns Cancelled if no options available
resp := AutoApprovePermission(options)

// SelectPermissionOption selects a specific option by index
// Returns Cancelled if index is out of bounds
resp := SelectPermissionOption(options, selectedIndex)

// CancelledPermissionResponse returns a cancelled response
resp := CancelledPermissionResponse()
```

## ACP Connection Testing

The `Connection` struct in `connection.go` manages the ACP process lifecycle. Key testable behaviors:

| Method | Test Scenarios |
|--------|----------------|
| `NewConnection()` | Empty command, invalid command, valid command with mock ACP |
| `Initialize()` | Successful handshake, output callback receives "Connected" message |
| `NewSession()` | Session creation, session ID populated |
| `Prompt()` | With session, without session (error), response handling |
| `Cancel()` | With/without active session |
| `Close()` | Normal close, double close, nil cmd edge case |
| `Done()` | Returns valid channel, not closed while active |
| `HasImageSupport()` | Before/after Initialize |

