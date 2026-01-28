---
description: Session persistence, lock management, and auxiliary package patterns
globs:
  - "internal/session/**/*"
  - "internal/auxiliary/**/*"
---

# Session Package Patterns

The `internal/session` package handles session persistence and management.

## Component Responsibilities

| Component | Responsibility | Thread-Safe |
|-----------|---------------|-------------|
| `Store` | Low-level file I/O, CRUD operations | Yes (mutex) |
| `Recorder` | High-level recording API, session lifecycle | Yes (mutex) |
| `Player` | Read-only playback, navigation | No (single-user) |
| `Lock` | Session locking, heartbeat, cleanup | Yes (mutex + goroutine) |

## Lock Management

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

## File Formats

- **events.jsonl**: Append-only, one JSON object per line
- **metadata.json**: Pretty-printed JSON, updated on each event
- **.lock**: Pretty-printed JSON, updated every 10 seconds (heartbeat)

## Lock Cleanup

The session package has a global lock registry that handles cleanup on process termination:

```go
// Cleanup functions in cleanup.go
registerLock(lock)      // Called when lock is acquired
unregisterLock(lock)    // Called when lock is released
CleanupAllLocks()       // Called on graceful shutdown
ActiveLockCount()       // Returns number of active locks

// Automatic cleanup on signals (SIGINT, SIGTERM, SIGHUP)
// Started on first lock registration
```

## Auxiliary Package

The `internal/auxiliary` package provides a hidden ACP session for utility tasks like title generation. It runs independently of the main session.

### Components

| Component | File | Purpose |
|-----------|------|---------|
| `Manager` | `manager.go` | Manages lazy-started auxiliary ACP session |
| `auxiliaryClient` | `client.go` | Implements `acp.Client` that collects responses |
| Global functions | `global.go` | Package-level singleton for easy access |

### Usage Pattern

```go
// Initialize once at startup
auxiliary.Initialize(acpCommand, logger)

// Use anywhere in the app
title, err := auxiliary.GenerateTitle(ctx, userMessage)

// Shutdown on exit
auxiliary.Shutdown()
```

### Key Characteristics

- **Lazy initialization**: ACP process starts only on first request
- **Auto-approve all permissions**: Never blocks on permission dialogs
- **File writes denied**: Prevents accidental file modifications
- **Thread-safe**: Multiple goroutines can call `Prompt()` (serialized internally)
- **Response collection**: Buffers `AgentMessageChunk` events into a single response string

