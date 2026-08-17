# Mitto Developer Documentation

This directory contains technical documentation for developers working on Mitto.

## Table of Contents

### Core Architecture

- **[Architecture Overview](architecture.md)** — High-level system architecture, component breakdown, design decisions, and data flow diagrams

### Component Documentation

- **[Session Management](session-management.md)** — Session recording, playback, state ownership model, and observer pattern

- **[Message Queue](message-queue.md)** — Queue architecture, automatic title generation, REST API, and WebSocket notifications

- **[Prompt Menus & Dispatch](prompts.md)** — How prompts are surfaced across menus (`menus` routing, `enabledWhen` contexts, `requires`), and how they start in existing vs new conversations via named-prompt dispatch

- **[Go Template Rendering in Prompt Bodies](prompt-templates.md)** — Design spec for `text/template` engine, render order, unified context, `cond`/`when` CEL bridge, FuncMap, error policy, `@mitto:` migration table, and corner cases

- **[Web Interface](web-interface.md)** — Browser-based UI architecture, REST API, streaming response handling, responsive design

- **[WebSocket Documentation](websockets/)** — Protocol specification, message types, sequence numbers, synchronization, reconnection handling, and multi-client support (authoritative reference for all real-time communication)

- **[Workspaces](workspaces.md)** — Multi-workspace architecture, CLI usage, REST API, and workspace persistence

- **[Follow-up Suggestions](follow-up-suggestions.md)** — AI-generated response suggestions, persistence, multi-client sync, and lifecycle

- **[Callbacks](callbacks.md)** — HTTP callback endpoints for triggering loop conversations on-demand, token management, security model

- **[Slack Integration Catalog](slack-integration-catalog.md)** — Process-global Slack app/workspace metadata, write-only credential boundary, validation flow, REST/Go SDK contract, channel discovery, and reference-aware deletion seam

- **[JavaScript Client Library](js-client-library.md)** — SDK design decision record: package layout, distribution via `go:embed`, environment-agnostic contract, public/internal boundary, semver policy, and stability promise

- **[JavaScript SDK Reference](../api/README.md)** — Consumer-facing usage docs for `@mitto/sdk`: getting started, client configuration, authentication, REST resource reference, and the realtime guide

- **[Go Client Library](go-client-library.md)** — SDK design decision record: `pkg/api` package layout, `Client`/`Session` object model, options pattern, typed error model, `context.Context` conventions, streaming realtime API, semver policy, and stability promise

- **[API Stability Tiers and Deprecation Policy](api-stability.md)** — Endpoint-level stability tiers (`stable`/`experimental`/`internal`), the `external-stable` marker, deprecation windows, per-surface signalling (REST/WebSocket/JS SDK/Go SDK), and the deprecation register

- **[CLI Conversation Commands](cli-conversation.md)** — Design decision record for `mitto conversation`/`mitto auth`: command tree, global flags and precedence, output contract (table/json/yaml), and exit-code mapping

### Infrastructure

- **[ACP Architecture](acp.md)** — Shared process model, concurrent RPC handling, MultiplexClient routing, auxiliary sessions, content blocks, multi-tier process GC (loop suspend, memory-bloat recycling), and the prompt inactivity watchdog

- **[Restricted Runner Integration](restricted-runners.md)** — Runner system architecture, sandbox types, configuration hierarchy, and ACP subprocess integration

- **[Message Processing Pipeline](processors.md)** — Unified processing pipeline, declarative/command/prompt processors, variable substitution

### Analysis

- **[Session Resume Analysis](session-resume-analysis.md)** — ACP session resume support analysis, UNSTABLE API usage, implementation plan

### Debugging & Tools

- **[MCP Servers](mcp.md)** — Global debug server, per-session MCP servers for ACP agents, advanced settings (feature flags)

## Quick Links

| Topic                     | Document                                               | Key Sections                                           |
| ------------------------- | ------------------------------------------------------ | ------------------------------------------------------ |
| Package structure         | [Architecture](architecture.md)                        | Component Breakdown                                    |
| Configuration             | [Architecture](architecture.md)                        | `internal/config`                                      |
| ACP architecture          | [ACP Architecture](acp.md)                             | Shared process, multiplexing, concurrency              |
| ACP client                | [ACP Architecture](acp.md)                             | `internal/acp`                                         |
| Process GC tiers          | [ACP Architecture](acp.md)                             | Multi-Tier GC, loop suspend, memory recycle            |
| Memory recycling          | [ACP Architecture](acp.md)                             | Tier 4 — Memory-Bloat Recycling, Configuration         |
| Inactivity watchdog       | [ACP Architecture](acp.md)                             | Prompt Inactivity Watchdog                             |
| Feature flags             | [Architecture](architecture.md)                        | Advanced Settings                                      |
| Event types               | [Session Management](session-management.md)            | Event Types                                            |
| Session settings          | [Session Management](session-management.md)            | Advanced Settings                                      |
| Queue API                 | [Message Queue](message-queue.md)                      | REST API                                               |
| Queue titles              | [Message Queue](message-queue.md)                      | Title Generation                                       |
| Loop multi-trigger        | [Message Queue](message-queue.md)                      | Loop Prompts: Multi-Trigger Architecture               |
| Loop onCompletion         | [Message Queue](message-queue.md)                      | Loop Prompts: On-Completion Delivery                   |
| Loop onTasks              | [Message Queue](message-queue.md)                      | Loop Prompts: On-Tasks Delivery                        |
| Prompt menus              | [Prompt Menus & Dispatch](prompts.md)                  | The `menus` routing key                                |
| Prompt dispatch           | [Prompt Menus & Dispatch](prompts.md)                  | The two start behaviors, deferred resolution           |
| REST endpoints            | [Web Interface](web-interface.md)                      | REST API Endpoints                                     |
| Streaming pipeline        | [Web Interface](web-interface.md)                      | Streaming Response Handling                            |
| WebSocket protocol        | [WebSocket Docs](websockets/protocol-spec.md)          | All message types and formats                          |
| Sequence numbers          | [WebSocket Docs](websockets/sequence-numbers.md)       | Assignment, contract, guarantees                       |
| Reconnection & sync       | [WebSocket Docs](websockets/synchronization.md)        | Gap detection, dedup, circuit breaker                  |
| Communication flows       | [WebSocket Docs](websockets/communication-flows.md)    | Golden path and corner case diagrams                   |
| Mobile support            | [WebSocket Docs](websockets/synchronization.md)        | Mobile Wake Resync, Zombie Detection                   |
| Workspace API             | [Workspaces](workspaces.md)                            | Workspace REST API                                     |
| Action buttons            | [Follow-up Suggestions](follow-up-suggestions.md)      | Persistence, Lifecycle                                 |
| Callback endpoints        | [Callbacks](callbacks.md)                              | Public API, Token Lifecycle, Security                  |
| SDK design (JS)           | [JS Client Library](js-client-library.md)              | Layout, Distribution, Contract, Stability Promise      |
| SDK usage (JS)            | [JavaScript SDK Reference](../api/README.md)           | Getting Started, Client Config, Auth, REST, Realtime   |
| SDK design (Go)           | [Go Client Library](go-client-library.md)              | Layout, Object Model, Error Model, Streaming API       |
| API stability tiers       | [API Stability Tiers](api-stability.md)                | Tiers, `external-stable`, Deprecation Window, Register |
| CLI conversation commands | [CLI Conversation Commands](cli-conversation.md)       | Command tree, Output contract, Exit codes              |
| MCP debugging             | [MCP Servers](mcp.md)                                  | Global Debug Server                                    |
| Session MCP               | [MCP Servers](mcp.md)                                  | Per-Session MCP Servers                                |
| Settings API              | [MCP Servers](mcp.md)                                  | Advanced Settings API                                  |
| Restricted runners        | [Restricted Runner Integration](restricted-runners.md) | Architecture, Runner Types, Config Hierarchy           |
| Message processors        | [Message Processing Pipeline](processors.md)           | Pipeline, Processor Types, Variable Substitution       |
| Session resume            | [Session Resume Analysis](session-resume-analysis.md)  | ACP resume support, UNSTABLE API, implementation plan  |

## Additional Documentation

For user-facing documentation and configuration guides, see the parent [docs/](../) directory:

- [Usage Guide](../usage.md)
- [Configuration](../config/README.md)
- [Development Setup](../development.md)
