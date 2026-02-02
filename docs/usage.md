# Usage Guide

This document covers the various ways to use Mitto and its command-line options.

## Running Mitto

### CLI Mode

Interactive terminal interface for working with AI coding agents:

```bash
# Start with default ACP server
mitto cli

# Use a specific ACP server
mitto cli --acp claude-code

# With custom working directory
mitto cli --dir /path/to/project

# With debug logging
mitto cli --debug
```

### Web Mode

Browser-based interface with rich Markdown rendering and session management:

```bash
# Start on default port (8080)
mitto web

# Custom port
mitto web --port 3000

# Open browser automatically
mitto web --open

# With debug logging
mitto web --debug
```

### macOS App

Native macOS application with system integration:

```bash
# Open the app
open Mitto.app

# With environment overrides
MITTO_ACP_SERVER=claude-code open Mitto.app
MITTO_WORK_DIR=/path/to/project open Mitto.app
```

## CLI Commands

Available commands during an interactive session:

| Command                | Description              |
| ---------------------- | ------------------------ |
| `/quit`, `/exit`, `/q` | Exit the CLI             |
| `/cancel`              | Cancel current operation |
| `/help`, `/h`, `/?`    | Show available commands  |

## Global Flags

| Flag              | Description                          |
| ----------------- | ------------------------------------ |
| `--acp <name>`    | Select ACP server from configuration |
| `--config <path>` | Use custom configuration file        |
| `--auto-approve`  | Auto-approve all permission requests |
| `--debug`         | Enable debug logging                 |
| `--dir <path>`    | Set working directory                |

## Web Interface Flags

| Flag              | Description                           |
| ----------------- | ------------------------------------- |
| `--port <number>` | HTTP server port (default: 8080)      |
| `--host <addr>`   | HTTP server host (default: 127.0.0.1) |
| `--open`          | Open browser on startup               |
| `--no-sessions`   | Disable session persistence           |

## Session Management

Mitto automatically saves conversation sessions. Sessions are stored in:

- **macOS**: `~/Library/Application Support/Mitto/sessions/`
- **Linux**: `~/.local/share/mitto/sessions/`
- **Windows**: `%APPDATA%\Mitto\sessions\`

Each session is stored as a directory containing:

- `events.jsonl` - Conversation events
- `metadata.json` - Session metadata (title, timestamps, etc.)

## Environment Variables

| Variable           | Description                     |
| ------------------ | ------------------------------- |
| `MITTO_DIR`        | Override default data directory |
| `MITTO_ACP_SERVER` | Default ACP server to use       |
| `MITTO_WORK_DIR`   | Default working directory       |
| `MITTORC`          | Path to configuration file      |

## Examples

### Quick Start

```bash
# Clone and build
git clone https://github.com/inercia/mitto.git
cd mitto && make build

# Start interactive session
./mitto cli
```

### Working with Multiple Projects

```bash
# Start web server for a specific project
mitto web --dir ~/projects/my-app --port 3000

# Start another for a different project
mitto web --dir ~/projects/other-app --port 3001
```

### Using Different AI Agents

```bash
# Use Claude Code
mitto cli --acp claude-code

# Use Auggie
mitto cli --acp auggie
```

## Related Documentation

- [Configuration](config/README.md) - ACP servers and settings
- [Web Interface](config/web.md) - Authentication, hooks, themes
- [macOS App](config/mac.md) - Hotkeys, notifications
- [Development](development.md) - Building and testing
