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
