# Communication Flows

This document shows complete communication flows between the Mitto UI (frontend) and backend, covering both the golden path (happy path) and various corner cases.

## Related Documentation

- [Protocol Specification](./protocol-spec.md) - Message types and formats
- [Sequence Numbers](./sequence-numbers.md) - Ordering and deduplication
- [Synchronization](./synchronization.md) - Reconnection and sync

## Golden Path: Complete Conversation Flow

This diagram shows a complete successful interaction from session connection through agent response:

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant WS as WebSocket
    participant Backend as Mitto Backend
    participant ACP as ACP Agent
    participant Store as Session Store

    Note over UI,Store: 1. Session Connection
    UI->>WS: Connect to /api/sessions/{id}/ws
    WS->>Backend: WebSocket upgrade
    Backend-->>WS: connected {session_id, client_id, is_running}
    WS-->>UI: Connection established

    Note over UI,Store: 2. Initial Event Load
    UI->>WS: load_events {limit: 50}
    Backend->>Store: ReadEventsLast(50)
    Store-->>Backend: Last 50 events
    Backend-->>WS: events_loaded {events, has_more, last_seq}
    WS-->>UI: Display conversation history

    Note over UI,Store: 3. User Sends Message
    UI->>UI: Generate prompt_id, save to localStorage
    UI->>UI: Add user message to UI (optimistic)
    UI->>WS: prompt {message, prompt_id}
    Backend->>Store: Persist user prompt (seq=51)
    Backend-->>WS: user_prompt {seq=51, is_mine=true, prompt_id}
    WS-->>UI: Confirm message (update seq on UI message)
    UI->>UI: Remove from pending prompts

    Note over UI,Store: 4. Agent Response (Streaming)
    Backend->>ACP: Send prompt
    ACP-->>Backend: AgentMessage chunk 1 (seq=52)
    Backend-->>WS: agent_message {seq=52, html, is_prompting=true}
    WS-->>UI: Display streaming response
    UI->>UI: Show "Stop" button

    ACP-->>Backend: ToolCall (seq=53)
    Backend-->>WS: tool_call {seq=53, id, title, status=running}
    WS-->>UI: Display tool indicator

    ACP-->>Backend: ToolUpdate
    Backend-->>WS: tool_update {seq=53, id, status=completed}
    WS-->>UI: Update tool status

    ACP-->>Backend: AgentMessage chunk 2 (seq=54)
    Backend-->>WS: agent_message {seq=54, html}
    WS-->>UI: Append to response

    ACP-->>Backend: PromptComplete
    Backend->>Store: Persist all buffered events
    Backend-->>WS: prompt_complete {event_count=54}
    WS-->>UI: Mark response complete
    UI->>UI: Hide "Stop" button
```

## Golden Path: Permission Request Flow

When the agent needs user permission for an action:

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant WS as WebSocket
    participant Backend as Mitto Backend
    participant ACP as ACP Agent

    Note over UI,ACP: Agent requests permission
    ACP-->>Backend: RequestPermission(title, options)
    Backend-->>WS: permission {request_id, title, description, options}
    WS-->>UI: Display permission dialog

    Note over UI,ACP: User approves
    UI->>WS: permission_answer {option_id, cancel=false}
    Backend->>ACP: PermissionResponse(approved)
    ACP-->>Backend: Continue with action...
    Backend-->>WS: agent_message {html}
    WS-->>UI: Display result
```

## Corner Case: Mobile Phone Sleep/Wake

When the phone sleeps and wakes, the WebSocket may be dead but appear open:

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant OldWS as Old WebSocket
    participant NewWS as New WebSocket
    participant Backend as Mitto Backend
    participant Storage as localStorage

    Note over UI,Backend: Normal operation
    UI->>OldWS: keepalive {client_time}
    Backend-->>OldWS: keepalive_ack {client_time, server_time}
    OldWS-->>UI: Connection healthy

    Note over UI,Backend: Phone goes to sleep
    UI->>UI: visibilitychange → hidden
    UI->>Storage: Save lastHiddenTime

    Note over UI,Backend: Phone wakes up (connection is zombie)
    UI->>UI: visibilitychange → visible
    UI->>Storage: Read lastHiddenTime
    UI->>UI: Calculate hidden duration

    alt Hidden < 1 hour
        UI->>UI: forceReconnectActiveSession()
        UI->>OldWS: close()
        UI->>NewWS: Connect to /api/sessions/{id}/ws
        Backend-->>NewWS: connected {...}
        UI->>NewWS: load_events {after_seq: lastSeenSeq}
        Backend-->>NewWS: events_loaded {events missed while sleeping}
        NewWS-->>UI: Merge with existing messages
    else Hidden > 1 hour (stale session)
        UI->>Backend: GET /api/config (auth check)
        alt Auth valid
            UI->>UI: forceReconnectActiveSession()
        else Auth expired (401)
            UI->>UI: Redirect to login
        end
    end
```

## Corner Case: Send Message During Zombie Connection

When user tries to send a message but the connection is actually dead:

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant ZombieWS as Zombie WebSocket
    participant NewWS as New WebSocket
    participant Backend as Mitto Backend
    participant Storage as localStorage

    Note over UI,Backend: Connection appears open but is dead
    UI->>UI: User types message, clicks Send
    UI->>UI: isConnectionHealthy() → false (missed keepalives)

    Note over UI,Backend: Force reconnect before sending
    UI->>ZombieWS: close()
    UI->>UI: waitForSessionConnection()
    UI->>NewWS: Connect to /api/sessions/{id}/ws
    Backend-->>NewWS: connected {...}

    Note over UI,Backend: Now send on fresh connection
    UI->>Storage: savePendingPrompt(promptId, message)
    UI->>NewWS: prompt {message, prompt_id}
    Backend-->>NewWS: user_prompt {seq, is_mine=true, prompt_id}
    NewWS-->>UI: Message confirmed
    UI->>Storage: removePendingPrompt(promptId)
```

## Corner Case: Send Timeout with Automatic Retry

When the ACK doesn't arrive within the initial timeout period, the frontend automatically reconnects and retries delivery.

**Timing budget (10 seconds total):**

- Initial ACK timeout: **3 seconds** (desktop) / **4 seconds** (mobile)
- Reconnect + delivery verification: up to 4 seconds
- Retry delivery + second ACK: up to 3 seconds

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant ZombieWS as Zombie WebSocket
    participant NewWS as New WebSocket
    participant Backend as Mitto Backend
    participant Storage as localStorage

    UI->>Storage: savePendingPrompt(promptId, message)
    UI->>UI: Add message to UI (optimistic)
    UI->>ZombieWS: prompt {message, prompt_id}
    UI->>UI: Start ACK timeout (3s desktop, 4s mobile)

    Note over ZombieWS,Backend: Connection may be zombie

    Note over UI: ACK timeout expires...
    UI->>UI: ACK timeout fires
    UI->>ZombieWS: close() (force close potentially zombie connection)

    Note over UI,Backend: Reconnect to verify delivery status
    UI->>NewWS: Connect to /api/sessions/{id}/ws
    Backend-->>NewWS: connected {last_user_prompt_id, last_user_prompt_seq, ...}
    NewWS-->>UI: Store last_user_prompt_id in ref

    UI->>UI: Compare: pending promptId == last_user_prompt_id?

    alt Prompt was delivered (IDs match)
        UI->>UI: Resolve send as SUCCESS
        UI->>Storage: removePendingPrompt(promptId)
        Note over UI: No error shown - message was delivered! ✓
    else Prompt was NOT delivered (IDs don't match)
        Note over UI,Backend: Retry delivery on fresh connection
        UI->>NewWS: prompt {message, prompt_id} (retry)
        Backend-->>NewWS: prompt_received {prompt_id}
        UI->>UI: Resolve send as SUCCESS
        UI->>Storage: removePendingPrompt(promptId)
    else Reconnection failed or retry timeout
        UI->>UI: Reject with error
        UI->>UI: Show error: "Message delivery could not be confirmed"
    end
```

## Corner Case: Client Connects Mid-Stream

When a client connects while the agent is actively responding:

```mermaid
sequenceDiagram
    participant Client1 as Client 1 (original)
    participant Client2 as Client 2 (joins mid-stream)
    participant Backend as Mitto Backend
    participant Buffer as Message Buffer
    participant ACP as ACP Agent

    Note over Client1,ACP: Agent is responding to Client 1
    ACP-->>Backend: AgentMessage chunk 1
    Backend->>Buffer: Write(chunk1)
    Backend-->>Client1: agent_message {seq=10, html}

    ACP-->>Backend: AgentMessage chunk 2
    Backend->>Buffer: Write(chunk2)
    Backend-->>Client1: agent_message {seq=10, html}

    Note over Client2: Client 2 connects mid-stream
    Client2->>Backend: WebSocket connect
    Backend-->>Client2: connected {is_running=true, is_prompting=true}

    Note over Backend: Replay buffered content to new client
    Backend->>Buffer: Peek() (read without clearing)
    Buffer-->>Backend: "chunk1 + chunk2"
    Backend-->>Client2: agent_message {seq=10, html="chunk1+chunk2"}

    Note over Client1,Client2: Both clients now in sync
    ACP-->>Backend: AgentMessage chunk 3
    Backend->>Buffer: Write(chunk3)
    Backend-->>Client1: agent_message {seq=10, html}
    Backend-->>Client2: agent_message {seq=10, html}
```

## Corner Case: Multiple Clients, One Sends Prompt

When multiple clients are connected and one sends a message:

```mermaid
sequenceDiagram
    participant Desktop as Desktop Client
    participant Mobile as Mobile Client
    participant Backend as Mitto Backend
    participant ACP as ACP Agent

    Note over Desktop,Mobile: Both clients connected to same session

    Note over Desktop: Desktop user sends message
    Desktop->>Backend: prompt {message, prompt_id="abc123"}
    Backend->>Backend: Persist user prompt (seq=20)

    Note over Backend: Broadcast to all clients
    Backend-->>Desktop: user_prompt {seq=20, is_mine=true, prompt_id="abc123"}
    Backend-->>Mobile: user_prompt {seq=20, is_mine=false, prompt_id="abc123", sender_id}

    Desktop->>Desktop: Update existing message with seq
    Mobile->>Mobile: Add new user message to UI

    Note over Desktop,Mobile: Agent responds - both see it
    ACP-->>Backend: AgentMessage
    Backend-->>Desktop: agent_message {seq=21, html}
    Backend-->>Mobile: agent_message {seq=21, html}
```

## Corner Case: Reconnect During Active Streaming

When WebSocket disconnects while agent is responding:

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant OldWS as Old WebSocket
    participant NewWS as New WebSocket
    participant Backend as Mitto Backend
    participant ACP as ACP Agent

    Note over UI,ACP: Agent is streaming response
    ACP-->>Backend: AgentMessage (seq=30)
    Backend-->>OldWS: agent_message {seq=30, html}
    OldWS-->>UI: Display chunk

    Note over OldWS: Connection drops
    OldWS-xBackend: Connection lost
    UI->>UI: onclose triggered

    Note over ACP,Backend: Agent continues (server-side)
    ACP-->>Backend: AgentMessage (seq=31)
    ACP-->>Backend: ToolCall (seq=32)
    ACP-->>Backend: PromptComplete
    Backend->>Backend: Persist all events

    Note over UI: Reconnect after 2 seconds
    UI->>NewWS: Connect to /api/sessions/{id}/ws
    Backend-->>NewWS: connected {is_running=true, is_prompting=false}

    UI->>NewWS: load_events {after_seq: 29}
    Backend-->>NewWS: events_loaded {events 30-32}

    Note over UI: Merge with deduplication
    UI->>UI: mergeMessagesWithSync()
    Note over UI: seq=30 already displayed → skip
    Note over UI: seq=31, 32 are new → add
```

## Corner Case: Load More (Pagination)

When user scrolls up to load older messages:

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant WS as WebSocket
    participant Backend as Mitto Backend
    participant Store as Session Store

    Note over UI: User has messages seq 50-100 displayed
    Note over UI: firstLoadedSeq = 50

    Note over UI: User scrolls to top
    UI->>UI: Trigger "Load More"
    UI->>WS: load_events {limit: 50, before_seq: 50}

    Backend->>Store: ReadEventsLast(50, beforeSeq=50)
    Store-->>Backend: Events seq 1-49
    Backend-->>WS: events_loaded {events, has_more=false, prepend=true}

    WS-->>UI: Receive older events
    UI->>UI: Prepend to message list
    UI->>UI: Update firstLoadedSeq = 1
    UI->>UI: Maintain scroll position
```

## Corner Case: Session Deleted While Phone Sleeping

When the active session is deleted by another client while the phone is asleep:

```mermaid
sequenceDiagram
    participant Mobile as Mobile Client
    participant Desktop as Desktop Client
    participant Backend as Mitto Backend
    participant Storage as localStorage

    Note over Mobile: Phone goes to sleep
    Mobile->>Storage: Save activeSessionId = "session-123"

    Note over Desktop: User deletes session on desktop
    Desktop->>Backend: DELETE /api/sessions/session-123
    Backend->>Backend: Delete session

    Note over Mobile: Phone wakes up
    Mobile->>Mobile: visibilitychange → visible
    Mobile->>Backend: GET /api/sessions (fetch session list)
    Backend-->>Mobile: Sessions list (session-123 not included)

    Mobile->>Mobile: Check if activeSessionId exists in list
    Mobile->>Mobile: Session "session-123" not found!

    alt Other sessions exist
        Mobile->>Mobile: Switch to most recent session
        Mobile->>Storage: Update activeSessionId
    else No sessions
        Mobile->>Mobile: Clear activeSessionId
        Mobile->>Mobile: Show "New conversation" prompt
    end
```

## Agent Response as Implicit ACK

As a fallback, if the agent starts responding (with `agent_message` or `agent_thought`), any pending sends for that session are automatically resolved:

```mermaid
sequenceDiagram
    participant UI as Mitto UI
    participant WS as WebSocket
    participant Backend as Mitto Backend

    UI->>WS: prompt {message, prompt_id}
    UI->>UI: Start timeout, waiting for ACK

    Note over Backend: Prompt received, persisted
    Backend-->>WS: prompt_received {prompt_id}
    Note over WS: ACK lost (network hiccup)

    Note over Backend: Agent starts responding
    Backend-->>WS: agent_message {seq, html}
    WS-->>UI: Agent response received

    UI->>UI: Agent responding → resolve ALL pending sends
    UI->>UI: Clear timeout, mark send as successful
    Note over UI: No error shown to user ✓
```
