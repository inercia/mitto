# Mitto Developer Documentation

This directory contains technical documentation for developers working on Mitto.

## Table of Contents

### Core Architecture

- **[Architecture Overview](architecture.md)** - High-level system architecture, component breakdown, design decisions, and data flow diagrams

### Component Documentation

- **[Session Management](session-management.md)** - Session recording, playback, state ownership model, and the message queue system

- **[Web Interface](web-interface.md)** - Browser-based UI architecture, streaming response handling, mobile wake resync

- **[WebSocket Messaging](websocket-messaging.md)** - Message ordering, synchronization, reconnection handling, and multi-client support

- **[Workspaces](workspaces.md)** - Multi-workspace architecture, CLI usage, REST API, and workspace persistence

## Quick Links

| Topic | Document | Key Sections |
|-------|----------|--------------|
| Package structure | [Architecture](architecture.md) | Component Breakdown |
| Configuration | [Architecture](architecture.md) | `internal/config` |
| ACP client | [Architecture](architecture.md) | `internal/acp` |
| Event types | [Session Management](session-management.md) | Event Types |
| Queue API | [Session Management](session-management.md) | Queue REST API Endpoints |
| WebSocket protocol | [WebSocket Messaging](websocket-messaging.md) | Message Format |
| Resync mechanism | [WebSocket Messaging](websocket-messaging.md) | Resync Mechanism |
| Workspace API | [Workspaces](workspaces.md) | Workspace REST API |
| Mobile support | [Web Interface](web-interface.md) | Mobile Wake Resync |

## Additional Documentation

For user-facing documentation and configuration guides, see the parent [docs/](../) directory:

- [Usage Guide](../usage.md)
- [Configuration](../config/README.md)
- [Development Setup](../development.md)
