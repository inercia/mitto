# Mitto Project Rules

## Project Overview

Mitto is a CLI client for the Agent Communication Protocol (ACP). It enables terminal-based interaction with AI coding agents like Auggie and Claude Code.

**Key documentation**: See `docs/architecture.md` for comprehensive architecture details.

## Package Structure

```
cmd/mitto/          → Entry point only (minimal code)
internal/cmd/       → CLI commands (Cobra-based)
internal/acp/       → ACP protocol client (SDK wrapper)
internal/config/    → Configuration loading (YAML)
internal/session/   → Session persistence (Store/Recorder/Player/Lock)
internal/web/       → Web interface server (HTTP, WebSocket, Markdown)
web/                → Embedded frontend assets (HTML, JS, CSS)
```

### Separation of Concerns

- **Never** import `internal/cmd` from other internal packages
- **Never** import CLI-specific code (readline, cobra) in `internal/acp`, `internal/session`, or `internal/web`
- The `acp` package uses callback functions (`output func(string)`) for UI independence
- The `web` package implements its own `acp.Client` (`WebClient`) with callback-based streaming
- Session package is completely independent of ACP, CLI, and Web

## Go Conventions

### Error Handling

```go
// Always wrap errors with context
if err != nil {
    return fmt.Errorf("failed to read config file %s: %w", path, err)
}

// Define package-level sentinel errors for expected conditions
var (
    ErrSessionNotFound = errors.New("session not found")
    ErrSessionLocked   = errors.New("session is locked by another process")
)

// Return sentinel errors, don't wrap them
if !exists {
    return ErrSessionNotFound  // NOT: fmt.Errorf("...: %w", ErrSessionNotFound)
}
```

### Interface Compliance

```go
// Verify interface implementation at compile time
var _ acp.Client = (*Client)(nil)
```

### Constructor Pattern

```go
// Use New* functions that return pointers
func NewStore(baseDir string) (*Store, error) { ... }
func NewRecorder(store *Store) *Recorder { ... }

// Use New*WithX for alternative constructors
func NewRecorderWithID(store *Store, sessionID string) *Recorder { ... }
```

### Thread Safety

```go
type Store struct {
    mu     sync.RWMutex  // Protect shared state
    closed bool
}

func (s *Store) SomeMethod() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.closed {
        return ErrStoreClosed
    }
    // ...
}
```

## Session Package Patterns

### Component Responsibilities

| Component | Responsibility | Thread-Safe |
|-----------|---------------|-------------|
| `Store` | Low-level file I/O, CRUD operations | Yes (mutex) |
| `Recorder` | High-level recording API, session lifecycle | Yes (mutex) |
| `Player` | Read-only playback, navigation | No (single-user) |
| `Lock` | Session locking, heartbeat, cleanup | Yes (mutex + goroutine) |

### Lock Management

```go
// Always register locks for cleanup on exit
lock := &Lock{...}
registerLock(lock)      // In acquireLock()
defer unregisterLock(l) // In Release()

// Update lock status during operations
lock.SetProcessing("Agent thinking...")  // Before prompt
lock.SetIdle()                           // After response
lock.SetWaitingPermission("File write")  // During permission request
```

### File Formats

- **events.jsonl**: Append-only, one JSON object per line
- **metadata.json**: Pretty-printed JSON, updated on each event
- **.lock**: Pretty-printed JSON, updated every 10 seconds (heartbeat)

### Atomic File Writes

```go
// Write to temp file, sync, then rename
tmpPath := path + ".tmp"
os.WriteFile(tmpPath, data, 0644)
f, _ := os.Open(tmpPath)
f.Sync()
f.Close()
os.Rename(tmpPath, path)
```

## CLI UX Patterns

### Cobra Command Structure

```go
var cliCmd = &cobra.Command{
    Use:   "cli",
    Short: "One-line description",
    Long:  `Multi-line description with examples...`,
    RunE:  runCLI,  // Use RunE for error returns
}

func init() {
    rootCmd.AddCommand(cliCmd)
    cliCmd.Flags().StringVar(&flagVar, "flag", "", "Description")
}
```

### User Feedback

```go
// Use emoji prefixes for visual clarity
fmt.Printf("🚀 Starting ACP server: %s\n", server.Name)
fmt.Printf("✅ Connected (protocol v%v)\n", version)
fmt.Printf("🔐 Permission requested: %s\n", title)
fmt.Println("👋 Shutting down...")

// Suppress noise in non-interactive mode
if !isOnceMode || debug {
    fmt.Printf("🚀 Starting...\n")
}
```

### Signal Handling

```go
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
go func() {
    <-sigChan
    cancel()  // Cancel context
}()
```

## ACP Protocol Guidelines

### SDK Usage

- Import: `github.com/coder/acp-go-sdk`
- The `Client` struct implements `acp.Client` interface
- Use `acp.ClientSideConnection` for protocol handling
- Always pass context for cancellation support

### Connection Lifecycle

```go
conn, err := acp.NewConnection(ctx, command, autoApprove, output, logger)
defer conn.Close()

conn.Initialize(ctx)  // Protocol handshake
conn.NewSession(ctx, cwd)  // Create session
conn.Prompt(ctx, message)  // Send prompt
```

### Permission Handling

- Check `autoApprove` flag first
- Prefer "allow" options when auto-approving
- Display numbered options for manual selection
- Loop until valid input received

## Web Interface Patterns

### Architecture

The web interface (`mitto web`) provides a browser-based UI via HTTP and WebSocket:

```
Browser ←→ WebSocket ←→ internal/web ←→ ACP Server (stdin/stdout)
                              ↓
                        MarkdownBuffer → HTML conversion
```

### Key Components

| Component | File | Purpose |
|-----------|------|---------|
| `Server` | `server.go` | HTTP server, routing, static files |
| `WSClient` | `websocket.go` | Per-connection WebSocket handler |
| `WebClient` | `client.go` | Implements `acp.Client` with callbacks |
| `MarkdownBuffer` | `markdown.go` | Streaming Markdown→HTML conversion |

### WebClient Pattern

```go
// WebClient uses callbacks instead of direct output
client := NewWebClient(WebClientConfig{
    AutoApprove: true,
    OnAgentMessage: func(html string) {
        // Send HTML chunk via WebSocket
        sendMessage(WSMsgTypeAgentMessage, map[string]string{"html": html})
    },
    OnToolCall: func(id, title, status string) {
        sendMessage(WSMsgTypeToolCall, ...)
    },
    OnPermission: func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
        // Send to frontend, wait for response
    },
})
```

### Markdown Streaming Buffer

```go
// Buffer accumulates chunks and flushes at semantic boundaries
buffer := NewMarkdownBuffer(func(html string) {
    // Called when HTML is ready to send
})

buffer.Write(chunk)  // Accumulates text
// Auto-flushes on: newline, code block end, paragraph break, timeout (200ms)
buffer.Flush()       // Force flush
buffer.Close()       // Flush and cleanup
```

### WebSocket Message Types

**Frontend → Backend:**
- `new_session` - Create ACP session
- `prompt` - Send user message
- `cancel` - Cancel current operation
- `permission_answer` - Respond to permission request

**Backend → Frontend:**
- `connected` - Session established
- `agent_message` - HTML content (streaming)
- `agent_thought` - Plain text thinking
- `tool_call` / `tool_update` - Tool status
- `permission` - Permission request
- `prompt_complete` - Response finished
- `error` - Error message

### Frontend Technology

- **No build step**: Preact + HTM loaded from CDN (esm.sh)
- **Styling**: Tailwind CSS via Play CDN
- **Embedding**: `go:embed` directive in `web/embed.go`
- **Single binary**: All assets embedded in Go binary

### Frontend Component Structure

```
App
├── SessionList (sidebar, hidden on mobile)
├── Header (connection status, streaming indicator)
├── MessageList
│   └── Message (user/agent/thought/tool/error/system)
└── ChatInput (textarea + send/cancel)
```

### Frontend State Management Patterns

**Use refs for values accessed in callbacks to avoid stale closures:**

```javascript
// Problem: activeSessionId in useCallback captures stale value
const handleMessage = useCallback((msg) => {
    // activeSessionId here is stale - it was captured when callback was created
    if (!activeSessionId) return;  // BUG: always null on first messages!
}, [activeSessionId]);

// Solution: Use a ref that's always current
const activeSessionIdRef = useRef(activeSessionId);
useEffect(() => {
    activeSessionIdRef.current = activeSessionId;
}, [activeSessionId]);

const handleMessage = useCallback((msg) => {
    const currentSessionId = activeSessionIdRef.current;  // Always current!
    if (!currentSessionId) return;
}, []);  // No dependency on activeSessionId
```

**Race condition pattern in WebSocket handlers:**
- WebSocket messages can arrive before React state updates complete
- Session switching: `session_switched` sets `activeSessionId`, but `agent_message` may arrive first
- Always use refs for state that callbacks need to read during async operations

**Function definition order in hooks:**
- `useCallback` functions must be defined before they're used in dependency arrays
- If function A uses function B, define B before A
- Circular dependencies require refs to break the cycle

### CDN Selection for Frontend Libraries

**Recommended CDN for ES modules**: Skypack (`cdn.skypack.dev`)
- Handles internal module resolution correctly
- Works with Preact hooks imports

**Avoid for ES modules**:
- `unpkg.com` and `jsdelivr.net` - May fail with "Failed to resolve module specifier" errors
  when libraries have internal imports without full paths
- `esm.sh` - Generally works but may have availability issues

```html
<!-- Recommended -->
<script type="module">
    import { h, render } from 'https://cdn.skypack.dev/preact@10.19.3';
    import { useState, useEffect } from 'https://cdn.skypack.dev/preact@10.19.3/hooks';
    import htm from 'https://cdn.skypack.dev/htm@3.1.1';
</script>
```

## Testing Requirements

### Unit Test Coverage

- All public functions should have tests
- Test both success and error paths
- Use `t.TempDir()` for file-based tests
- Use table-driven tests for multiple scenarios

```go
func TestSomething(t *testing.T) {
    tmpDir := t.TempDir()
    store, err := NewStore(tmpDir)
    if err != nil {
        t.Fatalf("NewStore failed: %v", err)
    }
    defer store.Close()
    // ...
}
```

### Test Naming

```go
func TestStore_CreateAndGet(t *testing.T) { ... }
func TestLock_ForceAcquireIdle(t *testing.T) { ... }
func TestLockInfo_StealabilityReason(t *testing.T) { ... }
```

### Integration Tests

- Located in `tests/integration/`
- Bash scripts with `#!/bin/bash` and `set -e`
- Check prerequisites before running
- Use timeouts to detect hangs
- Print clear PASS/FAIL messages

## Documentation Standards

### Code Comments

```go
// Package session provides session persistence and management for Mitto.
package session

// Store provides session persistence operations.
// It is safe for concurrent use.
type Store struct { ... }

// TryAcquireLock attempts to acquire a lock on the session.
// Returns ErrSessionLocked if the session is locked by another active process.
func (s *Store) TryAcquireLock(...) (*Lock, error) { ... }
```

### Architecture Updates

When adding new features:
1. Update `docs/architecture.md` with new components
2. Add Mermaid diagrams for complex flows
3. Document design decisions and rationale

## Common Pitfalls

### Deadlocks

```go
// WRONG: Calling method that acquires lock while holding lock
func (r *Recorder) Start() error {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.recordEvent(...)  // recordEvent also locks r.mu!
}

// RIGHT: Call store directly or release lock first
func (r *Recorder) Start() error {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.store.AppendEvent(...)  // Store has its own lock
}
```

### Resource Cleanup

```go
// Always use defer for cleanup
lock, err := store.TryAcquireLock(sessionID, "cli")
if err != nil {
    return err
}
defer lock.Release()

// Register for signal-based cleanup
registerLock(lock)  // Automatic cleanup on SIGINT/SIGTERM
```

### Context Propagation

```go
// Always pass context through the call chain
func (c *Connection) Prompt(ctx context.Context, message string) error {
    return c.conn.Prompt(ctx, acp.PromptRequest{...})
}
```

