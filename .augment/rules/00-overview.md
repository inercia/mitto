---
description: Project overview, package structure, and general guidelines for the Mitto codebase
globs: **/*
alwaysApply: true
---

# Mitto Project Overview

Mitto is a CLI client for the Agent Communication Protocol (ACP). It enables terminal-based interaction with AI coding agents like Auggie and Claude Code.

## Architecture Documentation

**Always consult `docs/devel/` for detailed architecture:**

| Document | Topics |
|----------|--------|
| [architecture.md](../docs/devel/architecture.md) | System overview, package breakdown |
| [session-management.md](../docs/devel/session-management.md) | Recording, playback, state ownership |
| [message-queue.md](../docs/devel/message-queue.md) | Queue system, title generation, API |
| [web-interface.md](../docs/devel/web-interface.md) | HTTP server, streaming, mobile support |
| [websocket-messaging.md](../docs/devel/websocket-messaging.md) | Message types, sync, reconnection |
| [workspaces.md](../docs/devel/workspaces.md) | Multi-workspace, persistence |

## Package Structure

```
cmd/mitto/          → Entry point only (minimal code)
cmd/mitto-app/      → macOS native app entry point
internal/cmd/       → CLI commands (Cobra-based)
internal/acp/       → ACP protocol client (SDK wrapper)
internal/auxiliary/ → Hidden ACP session for utility tasks
internal/client/    → Go client for Mitto REST API + WebSocket (used in tests)
internal/config/    → Configuration loading (YAML/JSON)
internal/msghooks/  → Message hooks (pre/post processing)
internal/session/   → Session persistence (Store/Recorder/Player/Lock/Queue)
internal/web/       → Web interface server (HTTP, WebSocket)
web/static/         → Frontend (Preact/HTM components)
```

## Separation of Concerns

- **Never** import `internal/cmd` from other internal packages
- **Never** import CLI-specific code in `internal/acp`, `internal/session`, or `internal/web`
- Session package is completely independent of ACP, CLI, and Web

## Key Utility Packages

### Fileutil

```go
fileutil.WriteJSONAtomic(path, &data, 0644)  // Atomic write (recommended)
fileutil.ReadJSON(path, &data)                // Read JSON
```

### Appdir

Platform-native directories: `MITTO_DIR` env var, or `~/Library/Application Support/Mitto` (macOS).

```go
appdir.Dir()           // Data directory path
appdir.SessionsDir()   // sessions/ subdirectory
appdir.SettingsPath()  // settings.json path
```

### Logging

```go
// Session-scoped (auto-includes session_id, working_dir, acp_server)
logger := logging.WithSessionContext(base, sessionID, workingDir, acpServer)

// Client-scoped (auto-includes client_id, session_id)
logger := logging.WithClient(base, clientID, sessionID)
```

## Documentation Standards

When adding new features:
1. Update `docs/devel/` (see [README](../docs/devel/README.md) for which file)
2. Add Mermaid diagrams for complex flows
3. Document design decisions and rationale

