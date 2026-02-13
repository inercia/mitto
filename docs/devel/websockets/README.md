# WebSocket Documentation

This directory contains the WebSocket messaging architecture documentation for Mitto. The documentation is split into focused files for easier navigation and maintenance.

## Table of Contents

| Document                                        | Description                                               |
| ----------------------------------------------- | --------------------------------------------------------- |
| [Protocol Specification](./protocol-spec.md)    | Message types, formats, and event types                   |
| [Sequence Numbers](./sequence-numbers.md)       | Ordering, assignment, contract, and guarantees            |
| [Synchronization](./synchronization.md)         | Reconnection, event loading, gap detection, deduplication |
| [Communication Flows](./communication-flows.md) | Golden path flows and corner cases with diagrams          |

## Overview

The WebSocket messaging system provides real-time bidirectional communication between the Mitto frontend and backend. Key features include:

- **Message ordering**: Guaranteed ordering using sequence numbers assigned at ACP receive time
- **Reliable delivery**: Prompt acknowledgment with retry on failure
- **Reconnection support**: Automatic reconnection with sync to catch up on missed events
- **Zombie detection**: Keepalive mechanism to detect and recover from dead connections
- **Multi-client support**: Multiple clients can connect to the same session

## Reading Order

For a complete understanding of the WebSocket system:

1. **[Protocol Specification](./protocol-spec.md)** - Start here to understand the message types and formats
2. **[Sequence Numbers](./sequence-numbers.md)** - Learn how messages are ordered and deduplicated
3. **[Synchronization](./synchronization.md)** - Understand reconnection, sync, and gap filling
4. **[Communication Flows](./communication-flows.md)** - See complete interaction flows with diagrams

## Quick Reference

### Frontend → Backend Messages

| Type                | Purpose                                 |
| ------------------- | --------------------------------------- |
| `prompt`            | Send user message                       |
| `load_events`       | Load events (initial, pagination, sync) |
| `keepalive`         | Connection health check                 |
| `cancel`            | Cancel agent operation                  |
| `permission_answer` | Respond to permission request           |

### Backend → Frontend Messages

| Type                        | Purpose                         |
| --------------------------- | ------------------------------- |
| `connected`                 | Connection established          |
| `prompt_received`           | Prompt ACK                      |
| `agent_message`             | Streaming agent response (HTML) |
| `tool_call` / `tool_update` | Tool invocations                |
| `events_loaded`             | Response to load_events         |
| `keepalive_ack`             | Connection health + state sync  |
| `prompt_complete`           | Agent finished responding       |

### Key Concepts

- **`seq`**: Monotonically increasing sequence number for ordering
- **`max_seq`**: Highest seq on server, enables immediate gap detection
- **`lastSentSeq`**: Server-side tracking to prevent duplicate events
- **Three-tier deduplication**: Server-side + client-side seq + content hash

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        Frontend (Mitto UI)                       │
├─────────────────────────────────────────────────────────────────┤
│  useWebSocket.js                                                │
│  ├── Connection management                                       │
│  ├── Keepalive (5s native, 10s browser)                         │
│  ├── Gap detection (max_seq piggybacking)                       │
│  └── Sequence tracking (seenSeqsRef)                            │
└────────────────────────────┬────────────────────────────────────┘
                             │ WebSocket
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                        Backend (session_ws.go)                   │
├─────────────────────────────────────────────────────────────────┤
│  SessionWSClient                                                │
│  ├── lastSentSeq tracking (server-side dedup)                   │
│  ├── Observer pattern (receives events from BackgroundSession)  │
│  └── Event loading (handleLoadEvents)                           │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                     BackgroundSession                            │
├─────────────────────────────────────────────────────────────────┤
│  ├── SeqProvider (GetNextSeq)                                   │
│  ├── EventBuffer (unified event ordering)                       │
│  ├── MarkdownBuffer (seq preservation for agent messages)       │
│  └── Observer notifications                                     │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                        ACP Agent                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Related Rules Files

The following `.augment/rules/` files contain implementation patterns:

- **[11-web-backend-sequences.md](../../../.augment/rules/11-web-backend-sequences.md)** - Backend implementation patterns
- **[22-web-frontend-websocket.md](../../../.augment/rules/22-web-frontend-websocket.md)** - Frontend implementation patterns
- **[27-web-frontend-sync.md](../../../.augment/rules/27-web-frontend-sync.md)** - Sequence sync and deduplication patterns

## Testing

Key tests for the WebSocket system:

- `TestEventBuffer_OutOfOrderSeqPreserved`
- `TestEventBuffer_CoalescingPreservesFirstSeq`
- `TestReconnectDuringAgentStreaming`
- `TestStaleSeqSync`
- `TestMultipleClientsSeeSameEvents`

See `internal/web/*_test.go` for the complete test suite.
