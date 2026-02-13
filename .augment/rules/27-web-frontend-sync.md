---
description: Frontend sequence sync, stale client detection, message deduplication, and server authority patterns
globs:
  - "web/static/lib.js"
keywords:
  - stale client detection
  - sequence sync
  - message deduplication
  - server authority
  - lastSeenSeq
  - events_loaded handler
  - mergeMessagesWithSync
  - getMaxSeq
  - seenSeqsRef
  - clearSeenSeqs
  - isSeqDuplicate
  - markSeqSeen
  - M1 fix
  - M1 dedup
  - isStaleClientState
---

# Frontend Sequence Sync and Deduplication

> **📖 Full Protocol Documentation**: See [docs/devel/websockets/](../../docs/devel/websockets/) for complete WebSocket protocol specification.

## Server is Always Right (Sequence Authority)

**Critical Principle**: The server is the single source of truth for sequence numbers. When there's a mismatch between client and server state, **the server always wins**.

### Why This Matters

Mobile clients (especially iPhone) can have stale state due to:

- Phone sleeping while in background
- Network disconnection and reconnection
- Server restart while client was offline
- Browser tab restoration with cached state

### Stale Client Detection

The client detects stale state by comparing sequence numbers:

```javascript
// In events_loaded handler
const clientLastSeq = currentSession?.lastLoadedSeq || 0;
const serverLastSeq = msg.data.last_seq || 0;

// If client thinks it has seen more than server has, client is stale
const isStaleClient =
  clientLastSeq > 0 && serverLastSeq > 0 && clientLastSeq > serverLastSeq;
```

### Recovery Behavior

When stale state is detected (`clientLastSeq > serverLastSeq`):

1. **Full reload**: Discard ALL client messages, use server's data (last 50 messages)
2. **Reset tracking**: Update `lastLoadedSeq` and `firstLoadedSeq` to server's values
3. **Auto-load remaining**: If `hasMore=true`, automatically load all remaining messages

```javascript
// Use isStaleClientState helper from lib.js
const isStaleClient = isStaleClientState(clientLastSeq, serverLastSeq);

if (session.messages.length === 0 || isStaleClient) {
  // Server wins - FULL RELOAD
  messages = newMessages;
  firstLoadedSeq = serverFirstSeq;
  lastLoadedSeq = serverLastSeq;
}
```

## Dynamic Sequence Calculation (No localStorage)

**Important**: The `lastSeenSeq` is calculated dynamically from messages in state, NOT stored in localStorage. This avoids stale localStorage issues, especially in WKWebView.

```javascript
import { getMaxSeq } from "../lib.js";

// Calculate lastSeenSeq from messages in state (not localStorage)
const sessionMessages = sessionsRef.current[sessionId]?.messages || [];
const lastSeq = getMaxSeq(sessionMessages);
```

This approach:

- Eliminates stale localStorage issues in WKWebView
- Always reflects the actual messages being displayed
- Simplifies the sync logic

## Two-Tier Deduplication Strategy

The system uses both server-side and client-side deduplication:

1. **Server-side** (`lastSentSeq` tracking): Prevents duplicates during normal streaming
2. **Client-side** (`mergeMessagesWithSync`): Handles sync after reconnect when messages overlap
3. **M1 fix** (`seenSeqsRef` tracking): Tracks seen seq values to skip duplicates during streaming

### Critical: Reset Seq Tracker on Stale Client Recovery

**When `isStaleClient` is detected, the M1 seq tracker MUST be reset BEFORE processing events.**

Without this reset, the tracker's `highestSeq` from the stale state causes fresh events from the server to be wrongly rejected as "very old" duplicates.

```javascript
// In events_loaded handler
if (isStaleClient) {
  console.log(`[M1 fix] Resetting seq tracker for stale client`);
  clearSeenSeqs(sessionId); // CRITICAL: Reset before processing events
}

// Then process events normally
for (const event of events) {
  if (event.seq) {
    markSeqSeen(sessionId, event.seq);
  }
}
```

**Why this matters:**

- Client had `highestSeq = 200` from previous session
- Server was restarted, now has `lastSeq = 50`
- Server sends events with seqs 1-50
- Without reset: `isSeqDuplicate(50)` returns `true` because `50 < 200 - 100` (highestSeq - MAX_RECENT_SEQS)
- All messages are rejected → UI shows no messages!
- With reset: Fresh tracker accepts all events correctly

### Deduplication in mergeMessagesWithSync

```javascript
export function mergeMessagesWithSync(existingMessages, newMessages) {
  // Create a map of existing messages by seq for fast lookup
  const existingBySeq = new Map();
  const existingHashes = new Set();
  for (const m of existingMessages) {
    if (m.seq) existingBySeq.set(m.seq, m);
    existingHashes.add(getMessageHash(m));
  }

  // Deduplicate by seq (preferred) or content hash (fallback)
  const filteredNewMessages = newMessages.filter((m) => {
    if (m.seq && existingBySeq.has(m.seq)) return false;
    return !existingHashes.has(getMessageHash(m));
  });

  // Combine and sort by seq for correct ordering
  const allMessages = [...existingMessages, ...filteredNewMessages];
  allMessages.sort((a, b) => {
    if (a.seq && b.seq) return a.seq - b.seq;
    return 0;
  });
  return allMessages;
}
```

## events_loaded Handler

```javascript
case "events_loaded": {
  if (isPrepend) {
    // Load more (older events) - no dedup needed
    messages = [...newMessages, ...session.messages];
  } else if (session.messages.length === 0) {
    // Initial load - just use the new messages
    messages = newMessages;
  } else {
    // Sync after reconnect - merge with deduplication
    messages = mergeMessagesWithSync(session.messages, newMessages);
  }
}
```

## Keepalive-Based Stale Detection

Stale state can also be detected via keepalive messages:

```javascript
case "keepalive_ack": {
  const serverMaxSeq = msg.data?.server_max_seq || 0;
  const clientMaxSeq = Math.max(getMaxSeq(sessionMessages), session?.lastLoadedSeq || 0);

  if (isStaleClientState(clientMaxSeq, serverMaxSeq)) {
    // Client has stale state! Trigger full reload
    ws.send(JSON.stringify({
      type: "load_events",
      data: { limit: INITIAL_EVENTS_LIMIT }
    }));
  } else if (serverMaxSeq > clientMaxSeq) {
    // Client is behind - request missing events
    ws.send(JSON.stringify({
      type: "load_events",
      data: { after_seq: clientMaxSeq }
    }));
  }
  break;
}
```

## Key Points Summary

| Scenario                 | Detection Point                    | Client Action                             |
| ------------------------ | ---------------------------------- | ----------------------------------------- |
| `clientSeq > serverSeq`  | `events_loaded` or `keepalive_ack` | Client is stale → full reload             |
| `clientSeq < serverSeq`  | `events_loaded` or `keepalive_ack` | Client is behind → request missing events |
| `clientSeq == serverSeq` | `keepalive_ack`                    | In sync → no action needed                |

**Never** try to "fix" the server based on client state. The server's sequence numbers are authoritative.
