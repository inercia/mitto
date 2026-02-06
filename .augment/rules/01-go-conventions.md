---
description: Go coding conventions, error handling, thread safety, interfaces, constructors, and logging patterns
globs:
  - "**/*.go"
---

# Go Conventions

## Error Handling

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

## Interface Compliance

```go
// Verify interface implementation at compile time
var _ acp.Client = (*Client)(nil)
```

## Interface-Based Decoupling

Use small interfaces to decouple components:

```go
// Define interface where it's USED, not where it's implemented
type SeqProvider interface {
    GetNextSeq() int64
}

// WebClient uses the interface (doesn't know about BackgroundSession)
type WebClient struct {
    seqProvider SeqProvider
}

// BackgroundSession implements it (doesn't know WebClient uses it)
func (bs *BackgroundSession) GetNextSeq() int64 {
    return bs.getNextSeq()
}
```

**Benefits:**
- Testable: Mock `SeqProvider` in tests
- Decoupled: `WebClient` doesn't import `BackgroundSession`
- Flexible: Any type implementing `GetNextSeq()` works

## Constructor Pattern

```go
// Use New* functions that return pointers
func NewStore(baseDir string) (*Store, error) { ... }
func NewRecorder(store *Store) *Recorder { ... }

// Use New*WithX for alternative constructors
func NewRecorderWithID(store *Store, sessionID string) *Recorder { ... }
```

## Thread Safety

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

## Atomic File Writes

```go
// Write to temp file, sync, then rename
tmpPath := path + ".tmp"
os.WriteFile(tmpPath, data, 0644)
f, _ := os.Open(tmpPath)
f.Sync()
f.Close()
os.Rename(tmpPath, path)
```

## Context Propagation

```go
// Always pass context through the call chain
func (c *Connection) Prompt(ctx context.Context, message string) error {
    return c.conn.Prompt(ctx, acp.PromptRequest{...})
}
```

## Callback Patterns

### Consistent Parameter Ordering

When callbacks need ordering/tracking info, put it first:

```go
// Good: seq first, then content
OnAgentMessage func(seq int64, html string)
OnToolCall     func(seq int64, id, title, status string)

// Bad: content first, seq buried
OnAgentMessage func(html string, seq int64)  // Inconsistent
```

### Nil-Safe Callback Invocation

Always check callbacks before calling:

```go
if c.onAgentMessage != nil {
    c.onAgentMessage(seq, html)
}
```

### Passing Data Through Buffers

When buffering content that needs metadata (like seq), track it in the buffer:

```go
type Buffer struct {
    content    strings.Builder
    pendingSeq int64  // Metadata from first write
}

func (b *Buffer) Write(seq int64, data string) {
    if b.content.Len() == 0 {
        b.pendingSeq = seq  // First write's metadata wins
    }
    b.content.WriteString(data)
}

func (b *Buffer) Flush() {
    seq := b.pendingSeq  // Capture before reset
    b.pendingSeq = 0
    b.onFlush(seq, b.content.String())
}
```

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

### PID Checking

```go
// Check if a process is still running (Unix)
func isPIDRunning(pid int) bool {
    process, _ := os.FindProcess(pid)
    return process.Signal(syscall.Signal(0)) == nil
}
```

## Structured Logging

Use the `internal/logging` package for consistent logging with context:

```go
import "github.com/inercia/mitto/internal/logging"

// Get component-specific logger
logger := logging.Web()
logger.Info("Server started", "port", port)

// Create session-scoped logger (includes session_id in all logs)
sessionLogger := logging.WithSession(logger, sessionID)
sessionLogger.Info("Processing prompt")  // Automatically includes session_id

// Create full context logger (includes session_id, working_dir, acp_server)
contextLogger := logging.WithSessionContext(logger, sessionID, workingDir, acpServer)

// Create client-scoped logger for WebSocket handlers
clientLogger := logging.WithClient(logger, clientID, sessionID)
```

**Best practices:**
- Use `logging.WithSession*` to avoid repeating `session_id` in every log call
- Pass loggers down through constructors, not as method parameters
- Use `Debug` for high-frequency events, `Info` for significant state changes

