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
| [follow-up-suggestions.md](../docs/devel/follow-up-suggestions.md) | Action buttons, auxiliary analysis, persistence |

## Rules Files Reference

| File | Triggers When |
|------|---------------|
| `01-go-conventions.md` | Editing Go files (`*.go`) |
| `02-session.md` | Working on `internal/session/` or `internal/auxiliary/` |
| `03-cli.md` | Working on `internal/cmd/` or `cmd/mitto/` |
| `04-acp.md` | Working on `internal/acp/` |
| `05-msghooks.md` | Working on message hooks (`internal/msghooks/`) |
| `06-conversion.md` | Markdown-to-HTML conversion (`internal/conversion/`) |
| `08-config.md` | Working on configuration (`internal/config/`, YAML/JSON) |
| `09-macos-app.md` | Working on macOS app (`cmd/mitto-app/`, `*.m`, `*.h`) |
| **Web Backend (10-12)** | |
| `10-web-backend-core.md` | Server, routing, HTTP handlers |
| `11-web-backend-sequences.md` | Sequence numbers, observers, message ordering, MarkdownBuffer flushing |
| `12-web-backend-actions.md` | Follow-up suggestions, action buttons |
| **Web Frontend (20-26)** | |
| `20-web-frontend-core.md` | Component structure, Preact/HTM basics |
| `21-web-frontend-state.md` | State management, refs, useCallback |
| `22-web-frontend-websocket.md` | WebSocket, message handling, reconnection |
| `23-web-frontend-mobile.md` | Mobile wake resync, visibility change, localStorage |
| `24-web-frontend-lib.md` | lib.js utilities, markdown rendering |
| `25-web-frontend-components.md` | UI components (ChatInput, QueueDropdown, Icons) |
| `26-web-frontend-hooks.md` | Custom hooks (useResizeHandle, useSwipeNavigation) |
| **Testing (30-33)** | |
| `30-testing-unit.md` | Go unit tests (`*_test.go`) |
| `31-testing-integration.md` | Integration tests, mock ACP server |
| `32-testing-playwright.md` | Playwright UI tests |
| `33-testing-js.md` | JavaScript unit tests (lib.test.js) |
| `99-local.md` | Local development notes (not committed) |

## Package Structure

```
cmd/mitto/            → Entry point only (minimal code)
cmd/mitto-app/        → macOS native app entry point
internal/cmd/         → CLI commands (Cobra-based)
internal/acp/         → ACP protocol client (SDK wrapper)
internal/auxiliary/   → Hidden ACP session for utility tasks
internal/client/      → Go client for Mitto REST API + WebSocket (used in tests)
internal/config/      → Configuration loading (YAML/JSON)
internal/conversion/  → Markdown-to-HTML conversion, file link detection
internal/msghooks/    → Message hooks (pre/post processing via external commands)
internal/session/     → Session persistence (Store/Recorder/Player/Lock/Queue)
internal/web/         → Web interface server (HTTP, WebSocket, MarkdownBuffer)
web/static/           → Frontend (Preact/HTM)
  ├── components/     → UI components (ChatInput, QueueDropdown, Message, etc.)
  ├── hooks/          → Custom hooks (useWebSocket, useSwipeNavigation, useResizeHandle)
  └── utils/          → Utilities (api.js, storage.js, native.js, csrf.js)
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

