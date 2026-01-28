---
description: Go coding conventions, error handling, thread safety, and common patterns
globs:
  - "**/*.go"
  - "internal/**/*"
  - "cmd/**/*"
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

