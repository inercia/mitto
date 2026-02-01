# WebSocket Message Handling Architecture

This document covers the WebSocket message handling system, including how message order is guaranteed, how clients resync after disconnection, and how reconnections are managed.

## Message Ordering

Message ordering is critical for ensuring all clients display conversations correctly. The system uses a **unified event buffer** to preserve streaming order and **sequence numbers** for tracking.

### Unified Event Buffer

All streaming events (agent messages, thoughts, tool calls, file operations) are buffered in a single `EventBuffer` during a prompt. Events are stored in the order they arrive and persisted together when the prompt completes.

```mermaid
sequenceDiagram
    participant Agent as ACP Agent
    participant BS as BackgroundSession
    participant Buffer as EventBuffer
    participant Store as Session Store

    Agent->>BS: AgentMessage("Let me help...")
    BS->>Buffer: AppendAgentMessage()

    Agent->>BS: ToolCall(read file)
    BS->>Buffer: AppendToolCall()

    Agent->>BS: AgentMessage("I found...")
    BS->>Buffer: AppendAgentMessage()

    Agent->>BS: ToolCall(edit file)
    BS->>Buffer: AppendToolCall()

    Agent->>BS: AgentMessage("Done!")
    BS->>Buffer: AppendAgentMessage()

    Agent->>BS: PromptComplete
    BS->>Buffer: Flush()
    Buffer-->>BS: [msg, tool, msg, tool, msg]
    BS->>Store: Persist events in order
```

This ensures events are persisted in the correct streaming order, preserving the interleaving of agent messages and tool calls.

### Sequence Number Assignment

Every event persisted to the session store is assigned a monotonically increasing sequence number (`seq`). The sequence number is assigned at persistence time by `session.Store.AppendEvent()`.

**Key properties:**
- `seq` starts at 1 for each session
- `seq` is assigned at persistence time, not at event creation
- `seq` is never reused or reassigned
- Events are stored in `seq` order in `events.jsonl`

### Frontend Ordering Strategy

The frontend preserves message order using these principles:

1. **Streaming messages** are displayed in the order they arrive via WebSocket
2. **Loaded sessions** use the order from `events.jsonl` (which preserves streaming order)
3. **Sync messages** are appended at the end (they represent events that happened AFTER the last seen event)
4. **Deduplication** prevents the same message from appearing twice

## Message Format

All WebSocket messages use a JSON envelope format with `type` and optional `data` fields.

### Frontend → Backend Messages

| Type | Data | Description |
|------|------|-------------|
| `prompt` | `{message, image_ids?, prompt_id}` | Send user message to agent |
| `cancel` | `{}` | Cancel current agent operation |
| `permission_answer` | `{request_id, approved}` | Respond to permission request |
| `sync_session` | `{after_seq}` | Request events after sequence number |
| `keepalive` | `{client_time}` | Application-level keepalive |
| `rename_session` | `{name}` | Rename the current session |

### Backend → Frontend Messages

| Type | Data | Description |
|------|------|-------------|
| `connected` | `{session_id, client_id, acp_server, is_running}` | Connection established |
| `prompt_received` | `{prompt_id}` | ACK that prompt was received and persisted |
| `user_prompt` | `{sender_id, prompt_id, message, is_mine}` | Broadcast of user prompt to all clients |
| `agent_message` | `{html}` | HTML-rendered agent response chunk |
| `agent_thought` | `{text}` | Agent thinking/reasoning (plain text) |
| `tool_call` | `{id, title, status}` | Tool invocation notification |
| `tool_update` | `{id, status}` | Tool status update |
| `permission` | `{request_id, title, description, options}` | Permission request |
| `prompt_complete` | `{event_count}` | Agent finished responding |
| `session_sync` | `{events, event_count, is_running, is_prompting}` | Response to sync request |
| `error` | `{message, code?}` | Error notification |

## Replay of Missing Content

When a client connects mid-stream (while the agent is actively responding), it needs to catch up on content that has been streamed but not yet persisted.

### The Problem

Agent messages and thoughts are **buffered** during streaming and only **persisted** when the prompt completes. A client connecting mid-stream would miss buffered content.

```mermaid
sequenceDiagram
    participant Agent as ACP Agent
    participant BS as BackgroundSession
    participant Buffer as Message Buffer
    participant Store as Session Store
    participant Client1 as Client 1 (connected)
    participant Client2 as Client 2 (connects later)

    Note over Agent,Client1: Agent starts responding
    Agent->>BS: AgentMessage chunk 1
    BS->>Buffer: Write(chunk1)
    BS->>Client1: OnAgentMessage(chunk1)

    Agent->>BS: AgentMessage chunk 2
    BS->>Buffer: Write(chunk2)
    BS->>Client1: OnAgentMessage(chunk2)

    Note over Client2: Client 2 connects mid-stream
    Client2->>BS: AddObserver(client2)
    BS->>Buffer: Peek() - read without clearing
    Buffer-->>BS: "chunk1 + chunk2"
    BS->>Client2: OnAgentMessage(buffered content)

    Agent->>BS: AgentMessage chunk 3
    BS->>Buffer: Write(chunk3)
    BS->>Client1: OnAgentMessage(chunk3)
    BS->>Client2: OnAgentMessage(chunk3)

    Note over Agent: Agent completes
    Agent->>BS: PromptComplete
    BS->>Buffer: Flush()
    Buffer-->>BS: Full message
    BS->>Store: RecordAgentMessage(full)
```

### The Solution

When a new observer connects to a `BackgroundSession`, the session checks if it's currently prompting. If so, it sends any buffered thought and message content to the new observer using `Peek()` (which reads without clearing the buffer).

**Key methods in `agentMessageBuffer`:**
- `Peek()`: Returns buffer content without clearing it
- `Flush()`: Returns buffer content and clears it (used at prompt completion)

This ensures all clients see the same content, regardless of when they connect.

## Resync Mechanism

The resync mechanism allows clients to catch up on events they missed while disconnected (e.g., phone sleep, network loss).

### Sequence Number Tracking

The frontend tracks the last seen sequence number in localStorage. This is updated when:
- Loading a session (set to highest `seq` from loaded events)
- Receiving `prompt_complete` (updated from `event_count` field)
- Receiving `session_sync` (updated after merge)

### Sync Request Flow

```mermaid
sequenceDiagram
    participant Client as Frontend
    participant WS as Session WebSocket
    participant Handler as SessionWSClient
    participant Store as Session Store

    Note over Client: WebSocket connects
    Client->>Client: Read lastSeenSeq from localStorage
    Client->>WS: sync_session {after_seq: 42}
    WS->>Handler: handleSync(afterSeq=42)
    Handler->>Store: ReadEventsFrom(sessionID, 42)
    Store-->>Handler: Events where seq > 42
    Handler->>Handler: Get session metadata & status
    Handler-->>WS: session_sync {events, event_count, is_running, is_prompting}
    WS-->>Client: Receive sync response
    Client->>Client: mergeMessagesWithSync(existing, new)
    Client->>Client: sortMessagesBySeq(merged)
    Client->>Client: Update lastSeenSeq in localStorage
```

### Merge and Deduplication

When sync events arrive, they're merged with existing messages using `mergeMessagesWithSync()` which:
1. Creates a hash set of existing messages for deduplication
2. Filters out duplicates from new messages
3. Merges both lists and sorts by `seq`

This handles the case where some messages were received via streaming (no `seq`) and the same messages arrive via sync (with `seq`).

## Reconnection Handling

The reconnection system handles WebSocket disconnections gracefully, including the "zombie connection" problem on mobile devices.

### Automatic Reconnection on Close

When a WebSocket closes unexpectedly, the frontend schedules a reconnection after a 2-second delay. The reconnection only occurs if:
- The session is still the active session
- No newer WebSocket has been created for that session

### Pending Prompt Retry

Prompts are saved to localStorage before sending (with a unique `prompt_id`). After reconnection, any prompts that weren't acknowledged are automatically retried. Prompts older than 5 minutes are cleaned up to prevent stale retries.

```mermaid
sequenceDiagram
    participant User
    participant Frontend
    participant Storage as localStorage
    participant WS as WebSocket
    participant Server as Backend

    User->>Frontend: Send message
    Frontend->>Frontend: Generate prompt_id
    Frontend->>Storage: savePendingPrompt(sessionId, promptId, message)
    Frontend->>WS: prompt {message, prompt_id}

    alt Connection Lost Before ACK
        WS-xServer: Connection fails
        Note over Frontend: Prompt still in localStorage

        Note over Frontend: Later: Reconnection
        Frontend->>WS: New WebSocket connection
        WS->>Server: Connection established
        Frontend->>Storage: getPendingPromptsForSession(sessionId)
        Storage-->>Frontend: [{promptId, message}]
        Frontend->>WS: prompt {message, prompt_id} (retry)
        Server-->>WS: prompt_received {prompt_id}
        WS-->>Frontend: ACK received
        Frontend->>Storage: removePendingPrompt(promptId)
    else ACK Received
        Server-->>WS: prompt_received {prompt_id}
        WS-->>Frontend: ACK received
        Frontend->>Storage: removePendingPrompt(promptId)
    end
```

### Multi-Client Prompt Broadcast

When multiple clients are connected to the same session, prompts are broadcast to all clients:

```mermaid
sequenceDiagram
    participant Client1 as Client 1 (sender)
    participant Server as Backend
    participant Client2 as Client 2 (observer)
    participant Client3 as Client 3 (observer)

    Client1->>Server: prompt {message, prompt_id}
    Server->>Server: Persist to session store
    Server-->>Client1: prompt_received {prompt_id}
    Server-->>Client1: user_prompt {is_mine: true, message}
    Server-->>Client2: user_prompt {is_mine: false, message, sender_id}
    Server-->>Client3: user_prompt {is_mine: false, message, sender_id}

    Note over Client2,Client3: Other clients add message to UI
    Client2->>Client2: Check for duplicate (by content hash)
    Client2->>Client2: Add message if not duplicate
```
