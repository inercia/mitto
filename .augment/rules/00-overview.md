---
description: Project overview, package structure, and general guidelines for the Mitto codebase
globs: **/*
alwaysApply: true
---

# Mitto Project Overview

Mitto is a CLI client for the Agent Communication Protocol (ACP). It enables terminal-based interaction with AI coding agents like Auggie and Claude Code.

**Key documentation**: See `docs/architecture.md` for comprehensive architecture details.

## Package Structure

```
cmd/mitto/          → Entry point only (minimal code)
cmd/mitto-app/      → macOS native app entry point (WebView wrapper)
config/             → Embedded default configuration (config.default.yaml)
internal/cmd/       → CLI commands (Cobra-based)
internal/acp/       → ACP protocol client (SDK wrapper)
internal/appdir/    → Platform-native directory management (MITTO_DIR)
internal/auxiliary/ → Hidden auxiliary ACP session for utility tasks (title generation)
internal/config/    → Configuration loading (YAML/JSON) and settings persistence
internal/fileutil/  → Shared JSON file I/O utilities
internal/session/   → Session persistence (Store/Recorder/Player/Lock)
internal/web/       → Web interface server (HTTP, WebSocket, Markdown)
platform/mac/       → macOS app resources (Info.plist, icons, scripts)
web/                → Embedded frontend assets (HTML, JS, CSS)
web/static/         → Frontend source files
  ├── components/   → Preact components (Message.js, etc.)
  ├── hooks/        → Custom Preact hooks
  ├── utils/        → Utility modules (storage.js, etc.)
  ├── app.js        → Main application
  ├── lib.js        → Pure utility functions (testable)
  └── lib.test.js   → Jest tests for lib.js
tests/              → Test suite (integration, UI, mocks, fixtures)
```

## Separation of Concerns

- **Never** import `internal/cmd` from other internal packages
- **Never** import CLI-specific code (readline, cobra) in `internal/acp`, `internal/session`, or `internal/web`
- The `acp` package uses callback functions (`output func(string)`) for UI independence
- The `web` package implements its own `acp.Client` (`WebClient`) with callback-based streaming
- Session package is completely independent of ACP, CLI, and Web

## Utility Packages

### Fileutil Package

The `internal/fileutil` package provides shared JSON file I/O utilities:

| Function | Purpose |
|----------|---------|
| `ReadJSON(path, v)` | Read and unmarshal JSON file |
| `WriteJSON(path, v, perm)` | Write JSON with pretty-printing (simple) |
| `WriteJSONAtomic(path, v, perm)` | Write JSON atomically (temp file + sync + rename) |

```go
// Atomic write (recommended for important data)
if err := fileutil.WriteJSONAtomic(metaPath, &meta, 0644); err != nil {
    return err
}
```

### Appdir Package

The `internal/appdir` package provides platform-native directory management:

**Directory locations (in priority order):**
1. `MITTO_DIR` environment variable (if set)
2. Platform-specific default:
   - **macOS**: `~/Library/Application Support/Mitto`
   - **Linux**: `$XDG_DATA_HOME/mitto` or `~/.local/share/mitto`
   - **Windows**: `%APPDATA%\Mitto`

| Function | Purpose |
|----------|---------|
| `Dir()` | Returns the Mitto data directory path (cached) |
| `EnsureDir()` | Creates the Mitto directory and sessions subdirectory if needed |
| `SettingsPath()` | Returns path to `settings.json` |
| `WorkspacesPath()` | Returns path to `workspaces.json` |
| `SessionsDir()` | Returns path to `sessions/` subdirectory |
| `ResetCache()` | Clears cached directory (for testing) |

### Directory Structure

```
~/Library/Application Support/Mitto/    # (or platform equivalent)
├── settings.json                        # User configuration (JSON)
├── workspaces.json                      # Workspace configuration (JSON, optional)
└── sessions/                            # Session data
    ├── 20260125-143052-a1b2c3d4/
    │   ├── events.jsonl
    │   └── metadata.json
    └── ...
```

## Documentation Standards

### Code Comments

```go
// Package session provides session persistence and management for Mitto.
package session

// Store provides session persistence operations.
// It is safe for concurrent use.
type Store struct { ... }

// TryAcquireLock attempts to acquire a lock on the session.
// Returns ErrSessionLocked if the session is locked by another active process.
func (s *Store) TryAcquireLock(...) (*Lock, error) { ... }
```

### Architecture Updates

When adding new features:
1. Update `docs/architecture.md` with new components
2. Add Mermaid diagrams for complex flows
3. Document design decisions and rationale

