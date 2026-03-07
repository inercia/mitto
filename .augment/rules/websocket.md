# WebSocket Protocol Patterns

## Message Types (Backend → Frontend)

| Type | Purpose | Key Fields |
|------|---------|------------|
| `connected` | Session initialized | `session_id`, `agent_supports_images`, `config_options`, `available_commands` |
| `agent_message` | Streaming agent text | `seq`, `max_seq`, `html` |
| `agent_thought` | Agent thinking text | `seq`, `text` |
| `tool_call` | Tool invocation | `seq`, `tool_call_id`, `title`, `status` |
| `tool_call_update` | Tool status change | `tool_call_id`, `status` |
| `prompt_received` | Prompt acknowledged | `prompt_id` |
| `prompt_complete` | Agent finished | `event_count` |
| `error` | Error/warning | `message` |
| `session_gone` | Session deleted | `session_id` |

## Message Types (Frontend → Backend)

| Type | Purpose | Key Fields |
|------|---------|------------|
| `prompt` | Send user message | `message`, `image_ids`, `file_ids` |
| `load_events` | Register as observer | `count`, `from_seq`, `client_seq` |
| `cancel` | Cancel current prompt | — |

## Sequence Numbers
- Assigned at receive-time by `BackgroundSession.getNextSeq()`
- Monotonically increasing per session
- Used for deduplication, gap detection, and replay
- `max_seq` piggybacked on messages for client sync
- Frontend tracks `lastKnownSeq` for reconnection

## WebSocket Connection Flow
1. Client connects to `/api/sessions/{id}/ws`
2. Server sends `connected` message with session metadata
3. Client sends `load_events` to register as observer and get buffered events
4. Client can now send `prompt` messages
5. Server streams `agent_message`, `tool_call`, etc. during processing
6. Server sends `prompt_complete` when agent finishes

## Observer Registration is Required
A WebSocket client that connects but doesn't send `load_events` will NOT receive any streaming events. This is because `load_events` is what triggers `AddObserver()` on the `BackgroundSession`.

## sendSessionConnected Pattern
When adding new session metadata to the frontend:
```go
// In session_ws.go sendSessionConnected():
if bs != nil {
    data["my_new_field"] = bs.MyNewGetter()
}
c.sendMessage(WSMsgTypeConnected, data)
```

## Reconnection
- Exponential backoff with jitter
- On reconnect, client sends `load_events` with last known seq to get missed messages
- Server replays buffered events from the requested seq number
- Force reconnect available for configuration changes

## Error Notifications
Use `OnError` for user-facing messages that don't need persistence or sequence numbers:
```go
bs.notifyObservers(func(o SessionObserver) {
    o.OnError("⚠️ Warning message shown to the user")
})
```
These appear as transient error banners in the frontend, not as persisted chat messages.
