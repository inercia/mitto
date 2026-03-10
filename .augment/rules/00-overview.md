---
description: Mitto project overview, architecture, package structure, key utilities
alwaysApply: true
---

# Mitto Project Overview

Mitto is a CLI client for the Agent Communication Protocol (ACP). It enables terminal-based interaction with AI coding agents like Auggie and Claude Code.

## Architecture Documentation

**Always consult `docs/devel/` for detailed architecture:**

| Document                                                           | Topics                                                                                              |
| ------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| [architecture.md](../docs/devel/architecture.md)                   | System overview, package breakdown                                                                  |
| [session-management.md](../docs/devel/session-management.md)       | Recording, playback, state ownership                                                                |
| [message-queue.md](../docs/devel/message-queue.md)                 | Queue system, title generation, API                                                                 |
| [mcp.md](../docs/devel/mcp.md)                                     | **MCP server** (global tools, session-scoped tools, UI prompts, flags)                              |
| [web-interface.md](../docs/devel/web-interface.md)                 | HTTP server, streaming, mobile support                                                              |
| [websockets/](../docs/devel/websockets/)                           | **WebSocket protocol** (message types, seq numbers, sync, reconnection, delivery verification)      |
| [workspaces.md](../docs/devel/workspaces.md)                       | Multi-workspace, persistence                                                                        |
| [follow-up-suggestions.md](../docs/devel/follow-up-suggestions.md) | Action buttons, auxiliary analysis, persistence                                                     |

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
internal/defense/     → Scanner defense, blocklist, IP metrics (used by web middleware)
internal/mcpserver/   → MCP servers (global debug + per-session)
internal/processors/  → Command processors (pre/post processing via external commands)
internal/runner/      → Restricted runner, sandbox execution (go-restricted-runner)
internal/secrets/     → Secure credential storage (Keychain on macOS)
internal/session/     → Session persistence (Store/Recorder/Player/Lock/Queue/Flags)
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
appdir.LogsDir()       // Logs directory (~/Library/Logs/Mitto on macOS)
```

### Logging

```go
logger := logging.WithSessionContext(base, sessionID, workingDir, acpServer)
logger := logging.WithClient(base, clientID, sessionID)
```

### Log Files

When debugging issues, check log files in `~/Library/Logs/Mitto/`:

| File          | Content                                                                    |
| ------------- | -------------------------------------------------------------------------- |
| `mitto.log`   | Go application logs (server, ACP, sessions). Enable DEBUG for seq numbers. |
| `access.log`  | Security events (auth, unauthorized access)                                |
| `webview.log` | JavaScript console output from WKWebView. Includes message seq numbers.    |

**Tip:** Use sequence numbers (`seq=N`) to track messages across frontend and backend logs. See `40-debugging.md` for detailed debugging instructions.

## Documentation Standards

When adding new features:

1. Update `docs/devel/` (see [README](../docs/devel/README.md) for which file)
2. Add Mermaid diagrams for complex flows
3. Document design decisions and rationale
4. Update relevant `.augment/rules/` files with new patterns
