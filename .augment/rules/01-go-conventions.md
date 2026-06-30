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

Ordering/tracking info first:
```go
OnAgentMessage func(seq int64, html string)
OnToolCall     func(seq int64, id, title, status string)
```

For buffered content with metadata, track in buffer:
```go
type Buffer struct {
    content    strings.Builder
    pendingSeq int64  // Metadata from first write
}
func (b *Buffer) Flush() {
    seq := b.pendingSeq
    b.pendingSeq = 0
    b.onFlush(seq, b.content.String())
}
```

## Interface-Based Decoupling

Define interface where it's USED:
```go
type SeqProvider interface { GetNextSeq() int64 }
type WebClient struct { seqProvider SeqProvider }
```

## Deadlock Prevention

**Rule**: Never call a method that acquires `r.mu` while already holding `r.mu`.

```go
// WRONG — recordEvent also locks r.mu → deadlock
r.mu.Lock(); defer r.mu.Unlock(); r.recordEvent(...)

// RIGHT — call store directly (has its own lock)
r.mu.Lock(); defer r.mu.Unlock(); r.store.AppendEvent(...)
```

## Bounded Deadline Context in Retry Loops

When a retry loop has no parent deadline (deadline-less or very generous context), each per-attempt timeout consumes wall-clock time independently, burning all attempts even on hung transport.

**Pattern** (mitto-8d7): Derive a `budgetCtx` that caps the *entire sequence*:

```go
const totalBudget = 60 * time.Second

budgetCtx := ctx
if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > totalBudget {
    var budgetCancel context.CancelFunc
    budgetCtx, budgetCancel = context.WithTimeout(ctx, totalBudget)
    defer budgetCancel()
}

// Use budgetCtx inside the loop (never extend caller's deadline)
for attempt := 1; attempt <= maxAttempts; attempt++ {
    if budgetCtx.Err() != nil {
        return nil, fmt.Errorf("context cancelled before attempt %d", attempt)
    }
    attemptCtx, cancel := context.WithTimeout(budgetCtx, perAttemptTimeout)
    // ... attempt logic ...
    cancel()
}
```

**Key principle**: Only *tighten* deadlines, never extend. `shouldFailFastCreateAttempt` bails when remaining budget < per-attempt timeout.

## Explicit Lock Management in Retry Loops

`defer mu.Unlock()` does **not** compose safely with manual unlock + retry. If the locked variable is reassigned during retry, defer fires on the wrong object → double-unlock panic.

**Rule**: In retry loops that release and reacquire a lock, use **explicit `mu.Unlock()` on every exit path** instead of `defer`. Extract goroutine+select lock-acquisition into a helper (e.g. `acquireAuxLock`) to keep all retry paths clean. See `internal/web/acp_process_manager.go` for a real example.

### TryLock: Release Before Post-Processing

`defer mu.Unlock()` holds the lock through ALL post-response work. If a response is sent mid-function and the caller immediately sends a follow-up using `TryLock`, the follow-up is silently dropped.

**Rule**: Use explicit `mu.Unlock()` after the core response send; do slow post-processing after unlocking.

```go
// BAD: defer holds lock through slow post-processing; TryLock from follow-up fails
c.loadEventsMu.Lock()
defer c.loadEventsMu.Unlock()
c.handleLoadEvents(req)   // sends response; client immediately sends follow-up → TryLock fails

// GOOD: explicit unlock after response, before post-processing
c.loadEventsMu.Lock()
result := c.handleLoadEvents(req)
c.loadEventsMu.Unlock()         // release here
c.postLoadProcessing(result)    // follow-up can now acquire lock
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

## JSON Marshaling

- **Nil vs empty slices**: `json.Marshal` encodes nil as `null`, empty as `[]`. ACP rejects `null` where array is required. Always initialize: `MCPServers: []MCPServer{}`. Mark with `// Must be empty array, not nil — ACP validates this`.
- **`omitempty` on bool**: Never use `omitempty` on `bool` fields where `false` is meaningful — Go omits `false` as zero value. Example: `PeriodicEnabled bool` must NOT have `omitempty`.
