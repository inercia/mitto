# Realtime Guide

This page covers the SDK's two WebSocket wrappers. For the wire protocol
itself (message shapes, envelopes), see
[WebSocket Protocol](../devel/websockets/) — the authoritative reference.

## `SessionStream` — one WebSocket per session

```js
const stream = client.sessionStream(sessionId, options);
// equivalent: import { createSessionStream } from "/sdk/index.js";
```

`options` (all optional, sensible defaults): `wsBaseUrl` (required if
`client.config.baseUrl` is relative), `seqStore`, `pendingPromptStore`,
`dropDuplicates`, `shouldReconnect`, and every tunable in
`SESSION_STREAM_CONSTANTS` (`keepaliveIntervalMs`, `maxReconnectAttempts`,
`reconnectDebounceMs`, etc. — see the export for the full list and
defaults).

### Lifecycle

```js
stream.connect(); // opens; no-op if already connecting/open
stream.on("open", () => {});
stream.on("close", (event) => {});
stream.on("error", (err) => {});
stream.on("reconnecting", ({ attempt, delayMs }) => {});
stream.on("health", ({ healthy }) => {});
stream.on("gone", ({ reason, data }) => {}); // terminal — stops reconnecting
stream.close(); // explicit close, never reconnects
```

States: `idle` → `connecting` → `open` → `closing`/`closed`, plus terminal
`stopped` (explicit close or the reconnect-attempt cap) and terminal `gone`
(session permanently gone — a `session_gone` message, or an error matching
a known terminal pattern; the client stops reconnecting immediately rather
than retrying into a 404 loop).

### Receiving messages

```js
stream.on("message", (msg, { duplicate, seq, maxSeq }) => {
  // msg.type, msg.data — see events.md's EVENTS table below
});
```

Dedup is **non-destructive by default**: a duplicate `seq` is delivered
with `duplicate: true` rather than dropped (dropping unconditionally can
race with `events_loaded` and silently swallow legitimate messages). Pass
`dropDuplicates: true` to opt into the old drop behavior and listen for the
separate `"duplicate"` event instead.

`keepalive_ack` frames are **not** emitted as `"message"** — listen for
`stream.on("keepalive_ack", (data) => {})` instead; they carry host-relevant
state (`queue_length`, `is_running`, `is_prompting`, `status`) but no
seq/stale-detection payload of interest to most callers.

### Sending

```js
stream.send({ type: "cancel" }); // best-effort, returns boolean
await stream.sendWhenConnected({ type: "..." }); // connects first if needed
const result = await stream.sendPrompt({ message: "Hello!" });
// { success: true, promptId } — or throws MittoNetworkError on timeout
stream.cancelPrompt();
stream.forceResetSession();
```

`sendPrompt()` includes delivery verification: an ACK timeout triggers a
forced reconnect to check whether the prompt actually landed
(`lastConfirmedPrompt()`), then retries within a total delivery budget
before giving up. `retryPendingPrompts()` re-sends anything still
outstanding after a reconnect; `resolveAllPendingSends()` resolves every
pending send as successful (e.g. once other evidence proves delivery).

### Sequence numbers & sync

```js
stream.lastSeenSeq(); // highest seq this stream has observed
stream.lastConfirmedPrompt(); // { promptId, seq } | null
stream.resetSync(); // clears dedup state + seq watermark (stale recovery)
stream.isHealthy(); // acked within 2x keepalive interval, no missed acks
```

The stream detects **stale client state** (its last-seen seq exceeds the
server's — e.g. after a server restart) and **gaps** (server has newer
events than the client has seen) from `keepalive_ack` frames and any
message carrying `max_seq`/`server_max_seq`, issuing `load_events`
automatically. See [Sequence Numbers](../devel/websockets/sequence-numbers.md)
and [Synchronization](../devel/websockets/synchronization.md) for the full
algorithm.

### Seq & pending-prompt stores

`SessionStream` uses two injectable stores internally; both default to
in-memory (lost on reload). Persistent variants build on your injected
`storage` (never `localStorage` directly):

```js
import {
  createStorageSeqStore,
  createStoragePendingPromptStore,
} from "/sdk/index.js";

const stream = client.sessionStream(sessionId, {
  seqStore: createStorageSeqStore(client.config.storage),
  pendingPromptStore: createStoragePendingPromptStore(client.config.storage),
});
```

`createMemorySeqStore()`/`createMemoryPendingPromptStore()` are the
corresponding in-memory defaults, exported for hosts that want to construct
one explicitly (e.g. to pass shared instances across multiple streams).

The lower-level primitives these stores and `SessionStream` itself are built
on are also exported, for hosts implementing custom sync/reconnection logic
instead of using `SessionStream` directly:

```js
import {
  createSeqTracker, // -> { highestSeq, recentSeqs: Set }
  isSeqDuplicate, // (tracker, seq, lastMessageSeq) -> boolean
  markSeqSeen, // (tracker, seq) -> void, prunes old entries
  getMaxSeq, // (events) -> highest .seq in an array
  isStaleClientState, // (clientLastSeq, serverLastSeq) -> boolean
  isTerminalSessionError, // (message) -> boolean ("session not found", etc.)
  generatePromptId, // () -> unique prompt ID for delivery tracking
} from "/sdk/index.js";
```

Most hosts never need these directly — `SessionStream` already applies them
internally per-message and exposes the results via `lastSeenSeq()`,
`isHealthy()`, and the `duplicate`/`seq`/`maxSeq` fields on the `"message"`
event.

## `EventsStream` — the global event bus

```js
const events = client.eventsStream(options);
// equivalent: import { createEventsStream } from "/sdk/index.js";
```

Wraps `/api/events`: broadcast-only, seq-less (no keepalive/ACK/seq
machinery — do not expect `SessionStream` semantics here).

```js
events.on("open", ({ isReconnect }) => {});
events.on("connected", (data) => {}); // the server's "connected" payload
events.on("event", ({ type, data }) => {}); // every non-"connected" frame
events.on("message", (msg) => {}); // every raw frame, including "connected"
events.connect();
events.close();
```

## Event & command constants

```js
import {
  EVENTS,
  COMMANDS,
  LEGACY_EVENTS,
  isKnownEventType,
  isCommandType,
} from "/sdk/index.js";

EVENTS.AGENT_MESSAGE; // "agent_message"
COMMANDS.PROMPT; // "prompt"
isKnownEventType("agent_message"); // true (current or legacy)
```

`EVENTS` names every backend→frontend message type; `COMMANDS` names every
frontend→backend request the session socket accepts; `LEGACY_EVENTS` names
deprecated aliases (`permission`→`ui_prompt`, `sync_session`→`load_events`,
etc.) still recognized for compatibility. Every entry has a JSDoc
`@typedef` describing its payload shape — see `sdk/realtime/events.js` for
the full per-event field reference (kept in sync with the Go `WSMsgType*`
constants by a dedicated test).

## Reconnection & backoff

Both streams reconnect with exponential backoff + jitter
(`RECONNECT_BASE_DELAY_MS`…`RECONNECT_MAX_DELAY_MS`, capped at
`MAX_RECONNECT_ATTEMPTS`), debounced (`RECONNECT_DEBOUNCE_MS`) so bursts of
`forceReconnect()` calls collapse into one attempt. Inject `shouldReconnect`
to veto a reconnect (e.g. after detecting the server is shutting down or
the user logged out) — return `true`/`false` or a `Promise` of either.
