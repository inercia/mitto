# WebSocket-Only Message Architecture

**Status**: ✅ Implemented (February 2026)

> **Note**: This proposal has been implemented. See [WebSocket Documentation](../websockets/) for the current documentation.

## Problem Statement

The current architecture has multiple sources for loading messages:

1. REST API for initial load and pagination
2. WebSocket streaming for real-time events
3. WebSocket sync for reconnection catch-up
4. Buffer replay for mid-stream connections

This creates complexity:

- Frontend must deduplicate across all sources
- Race conditions between REST and WebSocket
- Different code paths for each scenario
- Complex merge logic in `mergeMessagesWithSync()`

## Solution: Single WebSocket Channel with Server-Side Deduplication

Consolidate all message delivery into a single WebSocket channel with:

1. **Server-side seq tracking** - Each client tracks `lastSentSeq` to prevent duplicates
2. **Unified load/sync mechanism** - Single `load_events` message type for all event loading
3. **Simplified frontend** - No deduplication needed, just append messages

## Detailed Design

### New WebSocket Message Types

#### Client → Server

| Type          | Data                                | Description                 |
| ------------- | ----------------------------------- | --------------------------- |
| `load_events` | `{limit?, before_seq?, after_seq?}` | Load events with pagination |

**Parameters:**

- `limit`: Max events to return (default: 50)
- `before_seq`: Load events with seq < before_seq (for "load more" pagination)
- `after_seq`: Load events with seq > after_seq (for sync after reconnect)

Note: `before_seq` and `after_seq` are mutually exclusive.

#### Server → Client

| Type            | Data                                                   | Description             |
| --------------- | ------------------------------------------------------ | ----------------------- |
| `events_loaded` | `{events, has_more, first_seq, last_seq, total_count}` | Response to load_events |

**Response fields:**

- `events`: Array of event objects
- `has_more`: Boolean, true if more older events exist
- `first_seq`: Lowest seq in returned events (for pagination)
- `last_seq`: Highest seq in returned events (for tracking)
- `total_count`: Total events in session (from metadata)

### Server-Side Seq Tracking

Each `SessionWSClient` tracks what has been sent to prevent duplicates:

```go
type SessionWSClient struct {
    // ... existing fields ...

    // Seq tracking for deduplication
    lastSentSeq   int64      // Highest seq sent to this client
    seqMu         sync.Mutex // Protects lastSentSeq

    // Track if initial load has been done
    initialLoadDone bool
}
```

**Key invariant**: Once a seq is sent to a client, it is never sent again.

### Unified Event Sending

All event sends go through a single method that checks seq:

```go
func (c *SessionWSClient) sendEventIfNew(seq int64, msgType string, data interface{}) bool {
    c.seqMu.Lock()
    defer c.seqMu.Unlock()

    if seq > 0 && seq <= c.lastSentSeq {
        return false // Already sent
    }

    c.sendMessage(msgType, data)

    if seq > c.lastSentSeq {
        c.lastSentSeq = seq
    }
    return true
}
```

### Connection Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                     Connection Lifecycle                          │
├──────────────────────────────────────────────────────────────────┤
│                                                                   │
│  1. Client connects to /api/sessions/{id}/ws                     │
│     ↓                                                             │
│  2. Server sends 'connected' with session metadata               │
│     - Sets client.lastSentSeq = 0                                │
│     ↓                                                             │
│  3. Client sends 'load_events' {limit: 50}                       │
│     ↓                                                             │
│  4. Server loads last 50 events from store                       │
│     - Filters: only events with seq > lastSentSeq                │
│     - Updates lastSentSeq to highest loaded seq                  │
│     - Sends 'events_loaded' response                             │
│     ↓                                                             │
│  5. If session is_prompting:                                     │
│     - Server replays buffered events via sendEventIfNew()        │
│     - Only events with seq > lastSentSeq are sent                │
│     ↓                                                             │
│  6. Normal streaming: all events go through sendEventIfNew()     │
│     ↓                                                             │
│  [Disconnect]                                                     │
│     ↓                                                             │
│  7. Client reconnects, sends 'load_events' {after_seq: lastSeq}  │
│     - Server loads events with seq > after_seq                   │
│     - Sets lastSentSeq = after_seq before loading                │
│     - Sends only new events                                      │
│                                                                   │
└──────────────────────────────────────────────────────────────────┘
```

### Edge Cases

#### 1. Client connects mid-stream

**Scenario**: Agent is actively responding when client connects.

**Solution**:

1. Client sends `load_events {limit: 50}`
2. Server loads persisted events, updates `lastSentSeq`
3. Server replays buffered events via `sendEventIfNew()`
4. Buffered events with seq > lastSentSeq are sent
5. New streaming events continue via `sendEventIfNew()`

**No duplicates** because all sends check `lastSentSeq`.

#### 2. Prompt completes during load

**Scenario**: Events are being loaded when prompt completes and events are persisted.

**Solution**:

1. `load_events` handler holds a read lock during load
2. Persistence waits for lock
3. Events are loaded atomically
4. After load, any new persisted events have higher seq
5. Streaming continues with higher seq events

#### 3. Multiple rapid reconnections

**Scenario**: Flaky network causes multiple connect/disconnect cycles.

**Solution**:

1. Each connection starts fresh with `lastSentSeq = 0`
2. Client sends `load_events {after_seq: lastKnownSeq}` from localStorage
3. Server sets `lastSentSeq = after_seq` before loading
4. Only truly new events are sent

#### 4. Very long sessions (pagination)

**Scenario**: Session has 10,000+ events, client wants to load more.

**Solution**:

1. Initial load: `load_events {limit: 50}` → gets last 50 events
2. Load more: `load_events {limit: 50, before_seq: firstLoadedSeq}`
3. Server loads 50 events with seq < before_seq
4. Response includes `has_more: true/false`

**Note**: "Load more" events are historical and don't affect `lastSentSeq` tracking for streaming.

#### 5. Streaming chunks with same seq

**Scenario**: Agent message streams in multiple chunks, all with same seq.

**Solution**:

```go
func (c *SessionWSClient) sendStreamingChunk(seq int64, html string) {
    c.seqMu.Lock()
    defer c.seqMu.Unlock()

    // For streaming, we track if this is a NEW message or continuation
    if seq > c.lastSentSeq {
        // New message - update tracking
        c.lastSentSeq = seq
    } else if seq < c.lastSentSeq {
        // Old message - skip entirely
        return
    }
    // seq == lastSentSeq means continuation of current message - always send

    c.sendMessage("agent_message", map[string]interface{}{
        "seq": seq,
        "html": html,
    })
}
```

#### 6. Client requests events before connecting to session

**Scenario**: Client wants to preview a session without fully connecting.

**Solution**: Keep REST endpoint for read-only access, but mark as "viewer mode".
The WebSocket-only approach is for active session participation.

### Performance Considerations

#### Memory Usage

**Current**: Each client buffers events independently.
**New**: Server tracks only `lastSentSeq` (8 bytes) per client.

**Impact**: Negligible memory increase.

#### CPU Usage

**Current**: Frontend does complex deduplication with hash maps.
**New**: Server does O(1) seq comparison per event.

**Impact**: Slight increase on server, significant decrease on client.

#### Network Usage

**Current**: Events may be sent multiple times (REST + WebSocket + sync).
**New**: Each event sent exactly once per client.

**Impact**: Reduced bandwidth, especially on reconnection.

#### Latency

**Current**: Initial load requires REST call, then WebSocket connect.
**New**: Single WebSocket connection, then load_events message.

**Impact**: Slightly faster initial load (one fewer round trip).

### Frontend Simplification

#### Before (complex deduplication)

```javascript
// Multiple code paths
const restEvents = await fetch(`/api/sessions/${id}/events`);
const messages = convertEventsToMessages(restEvents);

// WebSocket streaming with dedup
case 'agent_message':
  const alreadyExists = messages.some(m => m.seq === msg.seq);
  if (!alreadyExists) { ... }

// Sync with merge
case 'session_sync':
  messages = mergeMessagesWithSync(existing, syncEvents);
```

#### After (simple append)

```javascript
// Single code path - server guarantees no duplicates
case 'events_loaded':
  if (msg.data.prepend) {
    // Load more (older events)
    messages = [...msg.data.events, ...messages];
  } else {
    // Initial load or sync
    messages = msg.data.events;
  }
  break;

case 'agent_message':
case 'tool_call':
case 'user_prompt':
  // Just append - server guarantees this is new
  messages = [...messages, convertEvent(msg)];
  break;
```

### Key Changes

#### 1. Remove REST API for Events

**Before:**

```javascript
// Initial load via REST
const events = await fetch(`/api/sessions/${id}/events?limit=50`);
// Then connect WebSocket for streaming
connectToSession(sessionId);
```

**After:**

```javascript
// Everything via WebSocket
const ws = connectToSession(sessionId);
ws.send({ type: "load_session", data: { limit: 50 } });
// Response comes as 'session_loaded' message
```

#### 2. Unified Message Handler

**Before:** Multiple code paths

```javascript
// REST load
const messages = convertEventsToMessages(restEvents);
setSessions(prev => ({ ...prev, [id]: { messages } }));

// WebSocket streaming
case 'agent_message':
  // Add to existing messages with dedup check

// WebSocket sync
case 'session_sync':
  // Merge with existing using mergeMessagesWithSync()
```

**After:** Single code path

```javascript
// All messages flow through the same handler
function handleEvents(sessionId, events, options = {}) {
  const { replace = false, prepend = false } = options;

  setSessions((prev) => {
    const session = prev[sessionId] || { messages: [] };
    let messages;

    if (replace) {
      // Initial load or full refresh
      messages = convertEventsToMessages(events);
    } else if (prepend) {
      // Load more (older messages)
      messages = [...convertEventsToMessages(events), ...session.messages];
    } else {
      // Streaming or sync - append with dedup
      messages = appendWithDedup(session.messages, events);
    }

    return { ...prev, [sessionId]: { ...session, messages } };
  });
}
```

#### 3. Server-Side: Single Entry Point

```go
func (c *SessionWSClient) handleMessage(msg WSMessage) {
    switch msg.Type {
    case "load_session":
        // Load events from store, send as session_loaded
        events := c.store.ReadEventsLast(c.sessionID, msg.Limit, msg.BeforeSeq)
        c.send("session_loaded", events)

    case "sync":
        // Same as load but after_seq
        events := c.store.ReadEventsFrom(c.sessionID, msg.AfterSeq)
        c.send("sync_response", events)

    case "prompt":
        // Existing prompt handling
    }
}
```

### Benefits

1. **Single source of truth**: All messages come through WebSocket
2. **No race conditions**: Sequential message handling
3. **Simpler deduplication**: Only need to handle streaming + sync
4. **Easier debugging**: All traffic visible in WebSocket inspector
5. **Better mobile support**: WebSocket handles reconnection naturally
6. **Reduced code**: Remove REST event handlers, merge logic

### Deduplication Strategy

With WebSocket-only, deduplication becomes simpler:

```javascript
function appendWithDedup(existing, newEvents) {
  const seenSeqs = new Set(existing.map((m) => m.seq).filter(Boolean));
  const seenIds = new Set(
    existing.filter((m) => m.role === "tool").map((m) => m.id),
  );

  const filtered = newEvents.filter((e) => {
    if (e.seq && seenSeqs.has(e.seq)) return false;
    if (e.role === "tool" && seenIds.has(e.id)) return false;
    return true;
  });

  return [...existing, ...convertEventsToMessages(filtered)];
}
```

### Connection Lifecycle

```
┌──────────────────────────────────────────────────────────────────┐
│                     Connection Lifecycle                          │
├──────────────────────────────────────────────────────────────────┤
│                                                                   │
│  1. Connect to /api/sessions/{id}/ws                             │
│     ↓                                                             │
│  2. Receive 'connected' with session metadata                     │
│     ↓                                                             │
│  3. Send 'load_session' {limit: 50}                              │
│     ↓                                                             │
│  4. Receive 'session_loaded' with events                         │
│     ↓                                                             │
│  5. If is_prompting: receive buffered events via replay          │
│     ↓                                                             │
│  6. Normal streaming: agent_message, tool_call, etc.             │
│     ↓                                                             │
│  [Disconnect]                                                     │
│     ↓                                                             │
│  7. Reconnect, send 'sync' {after_seq: lastSeenSeq}              │
│     ↓                                                             │
│  8. Receive 'sync_response' with missed events                   │
│                                                                   │
└──────────────────────────────────────────────────────────────────┘
```

### Migration Path

1. **Phase 1**: Add `load_session` WebSocket message type
2. **Phase 2**: Update frontend to use WebSocket for initial load
3. **Phase 3**: Remove REST `/api/sessions/{id}/events` endpoint
4. **Phase 4**: Simplify frontend deduplication logic

### Considerations

#### Pros

- Simpler architecture
- Fewer race conditions
- Easier to reason about
- Better for real-time apps

#### Cons

- WebSocket must be connected before loading messages
- Slightly more complex server-side WebSocket handler
- REST API still needed for session list, metadata, images

#### Keep REST For

- Session list (`GET /api/sessions`)
- Session metadata (`GET /api/sessions/{id}`)
- Session CRUD operations
- Image uploads/downloads
- Queue management

These are truly request-response operations that don't benefit from WebSocket.

## Alternative: Hybrid with Clear Boundaries

If full WebSocket-only is too aggressive, a cleaner hybrid:

1. **REST**: Session list, metadata, CRUD, images, queue
2. **WebSocket**: ALL message/event operations

Clear rule: "If it's about messages/events, use WebSocket. Everything else uses REST."

This still simplifies the frontend significantly by removing the REST events endpoint.

---

## Alternative 2: Server-Side Deduplication

Instead of complex frontend deduplication, track what each client has seen on the server.

### Core Idea

Each WebSocket client tracks its `lastSentSeq`. The server only sends events with `seq > lastSentSeq`.

```go
type SessionWSClient struct {
    // ... existing fields ...
    lastSentSeq int64  // Track what we've sent to this client
    seqMu       sync.Mutex
}

func (c *SessionWSClient) sendEventIfNew(seq int64, msgType string, data interface{}) {
    c.seqMu.Lock()
    defer c.seqMu.Unlock()

    if seq <= c.lastSentSeq {
        return // Already sent, skip
    }

    c.sendMessage(msgType, data)
    c.lastSentSeq = seq
}
```

### Benefits

1. **No frontend deduplication needed**: Server guarantees no duplicates
2. **Simpler frontend**: Just append messages as they arrive
3. **Works with existing architecture**: No major refactoring needed

### Implementation

1. When client connects, it sends `lastSeenSeq` (from localStorage)
2. Server sets `lastSentSeq = lastSeenSeq`
3. All event sends go through `sendEventIfNew()`
4. Buffer replay uses same mechanism - only sends if `seq > lastSentSeq`
5. Sync response only includes events with `seq > lastSentSeq`

### Handling Streaming (No Seq Yet)

For streaming chunks that share a seq (agent_message chunks):

- First chunk: assign seq, send, update lastSentSeq
- Subsequent chunks: same seq, always send (they're continuations)

```go
func (c *SessionWSClient) sendStreamingChunk(seq int64, html string, isFirst bool) {
    c.seqMu.Lock()
    defer c.seqMu.Unlock()

    if isFirst {
        if seq <= c.lastSentSeq {
            return // Already sent this message
        }
        c.lastSentSeq = seq
    }
    // Always send if it's a continuation of current message
    c.sendMessage("agent_message", map[string]interface{}{
        "seq": seq,
        "html": html,
    })
}
```

### Comparison

| Approach                 | Frontend Complexity | Server Complexity | Reliability |
| ------------------------ | ------------------- | ----------------- | ----------- |
| Current (frontend dedup) | High                | Low               | Medium      |
| WebSocket-only           | Medium              | Medium            | High        |
| Server-side dedup        | Low                 | Medium            | High        |

### Recommendation

**Server-side deduplication** is the quickest win:

- Minimal frontend changes (remove dedup logic)
- Moderate server changes (add seq tracking per client)
- Guarantees no duplicates at the source

**WebSocket-only** is the cleanest long-term:

- Removes REST/WebSocket race conditions
- Single code path for all message loading
- More work upfront, but simpler maintenance

---

## Quick Fix: Coordinate Replay and Sync

The current duplication happens because:

1. Buffer replay sends in-memory events
2. Sync sends persisted events
3. These can overlap when prompt just completed

**Simple fix**: Don't send sync response for events that were just replayed.

```go
func (c *SessionWSClient) handleSync(afterSeq int64) {
    // If we just replayed buffered events, use the highest replayed seq
    // instead of afterSeq to avoid duplicates
    effectiveAfterSeq := max(afterSeq, c.lastReplayedSeq)

    events, err := c.store.ReadEventsFrom(c.sessionID, effectiveAfterSeq)
    // ...
}
```

This is the minimal change to fix the immediate issue.
