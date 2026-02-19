# Mitto Developer Documentation

This directory contains technical documentation for developers working on Mitto.

## Table of Contents

### Core Architecture

- **[Architecture Overview](architecture.md)** - High-level system architecture, component breakdown, design decisions, and data flow diagrams

### Component Documentation

- **[Session Management](session-management.md)** - Session recording, playback, state ownership model, and observer pattern

- **[Message Queue](message-queue.md)** - Queue architecture, automatic title generation, REST API, and WebSocket notifications

- **[Web Interface](web-interface.md)** - Browser-based UI architecture, streaming response handling, mobile wake resync

- **[WebSocket Documentation](websockets/)** - Message ordering, synchronization, reconnection handling, and multi-client support

- **[Workspaces](workspaces.md)** - Multi-workspace architecture, CLI usage, REST API, and workspace persistence

- **[Follow-up Suggestions](follow-up-suggestions.md)** - AI-generated response suggestions, persistence, multi-client sync, and lifecycle

### Debugging & Tools

- **[MCP Servers](mcp.md)** - Global debug server, per-session MCP servers for ACP agents, advanced settings (feature flags)

## Quick Links

| Topic              | Document                                          | Key Sections            |
| ------------------ | ------------------------------------------------- | ----------------------- |
| Package structure  | [Architecture](architecture.md)                   | Component Breakdown     |
| Configuration      | [Architecture](architecture.md)                   | `internal/config`       |
| ACP client         | [Architecture](architecture.md)                   | `internal/acp`          |
| Feature flags      | [Architecture](architecture.md)                   | Advanced Settings       |
| Event types        | [Session Management](session-management.md)       | Event Types             |
| Session settings   | [Session Management](session-management.md)       | Advanced Settings       |
| Queue API          | [Message Queue](message-queue.md)                 | REST API                |
| Queue titles       | [Message Queue](message-queue.md)                 | Title Generation        |
| WebSocket protocol | [WebSocket Documentation](websockets/)            | Message Format          |
| Resync mechanism   | [WebSocket Documentation](websockets/)            | Resync Mechanism        |
| Workspace API      | [Workspaces](workspaces.md)                       | Workspace REST API      |
| Mobile support     | [Web Interface](web-interface.md)                 | Mobile Wake Resync      |
| Action buttons     | [Follow-up Suggestions](follow-up-suggestions.md) | Persistence, Lifecycle  |
| MCP debugging      | [MCP Servers](mcp.md)                             | Global Debug Server     |
| Session MCP        | [MCP Servers](mcp.md)                             | Per-Session MCP Servers |
| Settings API       | [MCP Servers](mcp.md)                             | Advanced Settings API   |

## Additional Documentation

For user-facing documentation and configuration guides, see the parent [docs/](../) directory:

- [Usage Guide](../usage.md)
- [Configuration](../config/README.md)
- [Development Setup](../development.md)
