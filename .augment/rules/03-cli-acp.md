---
description: CLI UX patterns and ACP protocol client, connection lifecycle, permission handling
globs:
  - "internal/cmd/**/*"
  - "cmd/mitto/**/*"
  - "internal/acp/**/*"
keywords:
  - CLI command
  - Cobra
  - ACP connection
  - ACP protocol
  - acp-go-sdk
  - permission handling
  - AutoApprovePermission
---

# CLI and ACP Protocol

## CLI Patterns

### Cobra Command Structure

```go
var cliCmd = &cobra.Command{
    Use:   "cli",
    Short: "One-line description",
    RunE:  runCLI,
}
```

### User Feedback

```go
fmt.Printf("🚀 Starting ACP server: %s\n", server.Name)
fmt.Printf("✅ Connected (protocol v%v)\n", version)
```

### Multi-Workspace CLI Usage

```bash
mitto web --dir /path/to/project1 --dir /path/to/project2
mitto web --dir auggie:/path/to/project1 --dir claude-code:/path/to/project2
```

## ACP Protocol

### SDK Usage

- Import: `github.com/coder/acp-go-sdk`
- The `Client` struct implements `acp.Client` interface
- Use `acp.ClientSideConnection` for protocol handling

### Connection Lifecycle

```go
conn, err := acp.NewConnection(ctx, command, autoApprove, output, logger)
defer conn.Close()

conn.Initialize(ctx)       // Protocol handshake
conn.NewSession(ctx, cwd)  // Create session
conn.Prompt(ctx, message)  // Send prompt
```

### Permission Handling

```go
// AutoApprovePermission selects the best "allow" option automatically
// Priority: AllowOnce > AllowAlways > first option
resp := AutoApprovePermission(options)

// SelectPermissionOption selects a specific option by index
resp := SelectPermissionOption(options, selectedIndex)

// CancelledPermissionResponse returns a cancelled response
resp := CancelledPermissionResponse()
```

### ACP Connection Testing

| Method              | Test Scenarios                                                     |
| ------------------- | ------------------------------------------------------------------ |
| `NewConnection()`   | Empty command, invalid command, valid command with mock ACP        |
| `Initialize()`      | Successful handshake, output callback receives "Connected" message |
| `NewSession()`      | Session creation, session ID populated                             |
| `Prompt()`          | With session, without session (error), response handling           |
| `Cancel()`          | With/without active session                                        |
| `Close()`           | Normal close, double close, nil cmd edge case                      |
| `Done()`            | Returns valid channel, not closed while active                     |
| `HasImageSupport()` | Before/after Initialize                                            |
