---
description: Session persistence, lock management, and auxiliary package patterns
globs:
  - "internal/session/**/*"
  - "internal/auxiliary/**/*"
---

# Session Package Patterns

**Architecture docs**: See [docs/devel/session-management.md](../docs/devel/session-management.md) and [docs/devel/message-queue.md](../docs/devel/message-queue.md).

## Quick Reference

| Component | Responsibility | Thread-Safe |
|-----------|---------------|-------------|
| `Store` | Low-level file I/O, CRUD operations | Yes (mutex) |
| `Recorder` | High-level recording API, session lifecycle | Yes (mutex) |
| `Player` | Read-only playback, navigation | No (single-user) |
| `Lock` | Session locking, heartbeat, cleanup | Yes (mutex + goroutine) |
| `Queue` | Message queue for busy agent | Yes (mutex) |

## Lock Management

```go
// Update lock status during operations
lock.SetProcessing("Agent thinking...")  // Before prompt
lock.SetIdle()                           // After response
lock.SetWaitingPermission("File write")  // During permission request
```

## Message Queue

**Important**: Queue configuration is **global/workspace-scoped**, NOT per-session.

See [docs/devel/message-queue.md](../docs/devel/message-queue.md) for:
- Configuration options and scope rationale
- REST API endpoints
- WebSocket notifications
- Title auto-generation

## Auxiliary Package

The `internal/auxiliary` package provides a hidden ACP session for utility tasks.

```go
auxiliary.Initialize(acpCommand, logger)  // Once at startup
title, err := auxiliary.GenerateTitle(ctx, userMessage)
auxiliary.Shutdown()  // On exit
```

**Key characteristics**: Lazy init, auto-approve permissions, file writes denied, thread-safe.

