# Mitto Developer Documentation

This directory contains technical documentation for developers working on Mitto.

## Table of Contents

### Core Architecture

- **[Architecture Overview](architecture.md)** — High-level system architecture, component breakdown, design decisions, and data flow diagrams

### Component Documentation

- **[Session Management](session-management.md)** — Session recording, playback, state ownership model, and observer pattern

- **[Message Queue](message-queue.md)** — Queue architecture, automatic title generation, REST API, and WebSocket notifications

- **[Web Interface](web-interface.md)** — Browser-based UI architecture, REST API, streaming response handling, responsive design

- **[WebSocket Documentation](websockets/)** — Protocol specification, message types, sequence numbers, synchronization, reconnection handling, and multi-client support (authoritative reference for all real-time communication)

- **[Workspaces](workspaces.md)** — Multi-workspace architecture, CLI usage, REST API, and workspace persistence

- **[Follow-up Suggestions](follow-up-suggestions.md)** — AI-generated response suggestions, persistence, multi-client sync, and lifecycle

- **[Callbacks](callbacks.md)** — HTTP callback endpoints for triggering periodic conversations on-demand, token management, security model

### Infrastructure

- **[ACP Architecture](acp.md)** — Shared process model, concurrent RPC handling, MultiplexClient routing, auxiliary sessions, content blocks, and process GC

- **[Restricted Runner Integration](restricted-runner-integration.md)** — Runner system architecture, sandbox types, configuration hierarchy, and ACP subprocess integration

### Debugging & Tools

- **[MCP Servers](mcp.md)** — Global debug server, per-session MCP servers for ACP agents, advanced settings (feature flags)

## Quick Links

| Topic               | Document                                                          | Key Sections                                 |
| ------------------- | ----------------------------------------------------------------- | -------------------------------------------- |
| Package structure   | [Architecture](architecture.md)                                   | Component Breakdown                          |
| Configuration       | [Architecture](architecture.md)                                   | `internal/config`                            |
| ACP architecture    | [ACP Architecture](acp.md)                                        | Shared process, multiplexing, concurrency    |
| ACP client          | [ACP Architecture](acp.md)                                        | `internal/acp`                               |
| Feature flags       | [Architecture](architecture.md)                                   | Advanced Settings                            |
| Event types         | [Session Management](session-management.md)                       | Event Types                                  |
| Session settings    | [Session Management](session-management.md)                       | Advanced Settings                            |
| Queue API           | [Message Queue](message-queue.md)                                 | REST API                                     |
| Queue titles        | [Message Queue](message-queue.md)                                 | Title Generation                             |
| REST endpoints      | [Web Interface](web-interface.md)                                 | REST API Endpoints                           |
| Streaming pipeline  | [Web Interface](web-interface.md)                                 | Streaming Response Handling                  |
| WebSocket protocol  | [WebSocket Docs](websockets/protocol-spec.md)                     | All message types and formats                |
| Sequence numbers    | [WebSocket Docs](websockets/sequence-numbers.md)                  | Assignment, contract, guarantees             |
| Reconnection & sync | [WebSocket Docs](websockets/synchronization.md)                   | Gap detection, dedup, circuit breaker        |
| Communication flows | [WebSocket Docs](websockets/communication-flows.md)               | Golden path and corner case diagrams         |
| Mobile support      | [WebSocket Docs](websockets/synchronization.md)                   | Mobile Wake Resync, Zombie Detection         |
| Workspace API       | [Workspaces](workspaces.md)                                       | Workspace REST API                           |
| Action buttons      | [Follow-up Suggestions](follow-up-suggestions.md)                 | Persistence, Lifecycle                       |
| Callback endpoints  | [Callbacks](callbacks.md)                                         | Public API, Token Lifecycle, Security        |
| MCP debugging       | [MCP Servers](mcp.md)                                             | Global Debug Server                          |
| Session MCP         | [MCP Servers](mcp.md)                                             | Per-Session MCP Servers                      |
| Settings API        | [MCP Servers](mcp.md)                                             | Advanced Settings API                        |
| Restricted runners  | [Restricted Runner Integration](restricted-runner-integration.md) | Architecture, Runner Types, Config Hierarchy |

## Additional Documentation

For user-facing documentation and configuration guides, see the parent [docs/](../) directory:

- [Usage Guide](../usage.md)
- [Configuration](../config/README.md)
- [Development Setup](../development.md)
