---
description: Project-specific Go conventions, callback patterns, buffer seq tracking, deadlock prevention, structured logging
globs:
  - "**/*.go"
keywords:
  - Go convention
  - callback
  - deadlock
  - structured logging
---

# Go Conventions

## Callback Patterns

### Consistent Parameter Ordering

When callbacks need ordering/tracking info, put it first:

```go
OnAgentMessage func(seq int64, html string)
OnToolCall     func(seq int64, id, title, status string)
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

## Interface-Based Decoupling

Define interface where it's USED, not where it's implemented:

```go
type SeqProvider interface {
    GetNextSeq() int64
}

type WebClient struct {
    seqProvider SeqProvider
}

func (bs *BackgroundSession) GetNextSeq() int64 {
    return bs.getNextSeq()
}
```

## Deadlock Prevention

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

## PID Checking

```go
func isPIDRunning(pid int) bool {
    process, _ := os.FindProcess(pid)
    return process.Signal(syscall.Signal(0)) == nil
}
```

## Structured Logging

Use the `internal/logging` package for consistent logging with context:

```go
import "github.com/inercia/mitto/internal/logging"

logger := logging.Web()
sessionLogger := logging.WithSession(logger, sessionID)
contextLogger := logging.WithSessionContext(logger, sessionID, workingDir, acpServer)
clientLogger := logging.WithClient(logger, clientID, sessionID)
```

**Best practices:**

- Use `logging.WithSession*` to avoid repeating `session_id` in every log call
- Pass loggers down through constructors, not as method parameters
- Use `Debug` for high-frequency events, `Info` for significant state changes
- Use `Warn` (not `Error`) for expected race conditions during teardown — e.g. streaming goroutines delivering events after `recorder.End()` returns `"session not started"`:

```go
if strings.Contains(err.Error(), "session not started") {
    bs.logger.Warn("Failed to persist tool call", "seq", seq, "error", err)
} else {
    bs.logger.Error("Failed to persist tool call", "seq", seq, "error", err)
}
```

## JSON Marshaling: Nil vs Empty Slices

`json.Marshal` encodes a nil slice as `null` and an empty slice as `[]`. ACP and other APIs that validate with JSON Schema will reject `null` where an array is required.

```go
// BAD — marshals as "mcpServers": null
type SessionParams struct {
    MCPServers []MCPServer `json:"mcpServers"`
}
params := SessionParams{} // MCPServers is nil

// GOOD — marshals as "mcpServers": []
params := SessionParams{
    MCPServers: []MCPServer{}, // or: make([]MCPServer, 0)
}
```

**Rule:** Always initialize slice fields that appear in JSON-serialized API request structs, even when empty. Do not rely on zero values for outbound JSON payloads. Mark intentional empty-slice inits with `// Must be empty array, not nil — ACP validates this`.
