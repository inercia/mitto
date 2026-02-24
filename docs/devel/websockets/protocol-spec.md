# WebSocket Protocol Specification

This document defines the WebSocket message types and formats used for real-time communication between the Mitto frontend and backend.

## Related Documentation

- [Sequence Numbers](./sequence-numbers.md) - Ordering and deduplication
- [Synchronization](./synchronization.md) - Reconnection and sync
- [Communication Flows](./communication-flows.md) - Complete interaction flows

## Message Envelope

All WebSocket messages use a JSON envelope format:

```json
{
  "type": "message_type",
  "data": { ... }
}
```

## Frontend → Backend Messages

| Type                | Data                                | Description                                      |
| ------------------- | ----------------------------------- | ------------------------------------------------ |
| `prompt`            | `{message, image_ids?, prompt_id}`  | Send user message to agent                       |
| `cancel`            | `{}`                                | Cancel current agent operation                   |
| `permission_answer` | `{request_id, approved}`            | Respond to permission request                    |
| `load_events`       | `{limit?, before_seq?, after_seq?}` | Load events (initial, pagination, or sync)       |
| `keepalive`         | `{client_time}`                     | Application-level keepalive for zombie detection |
| `rename_session`    | `{name}`                            | Rename the current session                       |

## Backend → Frontend Messages

| Type              | Data                                                                                           | Description                                                                  |
| ----------------- | ---------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| `connected`       | `{session_id, client_id, acp_server, is_running, last_user_prompt_id?, last_user_prompt_seq?}` | Connection established (includes last prompt info for delivery verification) |
| `prompt_received` | `{prompt_id}`                                                                                  | ACK that prompt was received and persisted                                   |
| `user_prompt`     | `{seq, max_seq, sender_id, prompt_id, message, is_mine}`                                       | Broadcast of user prompt to all clients                                      |
| `agent_message`   | `{seq, max_seq, html, is_prompting}`                                                           | HTML-rendered agent response chunk                                           |
| `agent_thought`   | `{seq, max_seq, text, is_prompting}`                                                           | Agent thinking/reasoning (plain text)                                        |
| `tool_call`       | `{seq, max_seq, id, title, status, is_prompting}`                                              | Tool invocation notification                                                 |
| `tool_update`     | `{seq, max_seq, id, status, is_prompting}`                                                     | Tool status update                                                           |
| `permission`      | `{request_id, title, description, options}`                                                    | Permission request                                                           |
| `prompt_complete` | `{event_count, max_seq}`                                                                       | Agent finished responding                                                    |
| `events_loaded`   | `{events, has_more, first_seq, last_seq, max_seq, total_count, prepend, is_prompting}`         | Response to load_events request                                              |
| `keepalive_ack`   | `{client_time, server_time, max_seq, is_prompting, is_running, queue_length, status}`          | Response to keepalive (for zombie detection and state sync)                  |
| `error`           | `{message, code?}`                                                                             | Error notification                                                           |

## Key Field Descriptions

### Sequence Number Fields

- **`seq`**: Monotonically increasing sequence number assigned when event is received from ACP. See [Sequence Numbers](./sequence-numbers.md) for details.
- **`max_seq`**: Highest sequence number the server has for this session. Enables immediate gap detection.

### Delivery Verification Fields

The `connected` message includes delivery verification fields:

- **`last_user_prompt_id`**: ID of the last user prompt in the session
- **`last_user_prompt_seq`**: Sequence number of the last user prompt

These enable the frontend to verify delivery of pending prompts after reconnecting from a zombie connection.

### Keepalive Fields

The `keepalive_ack` includes session state for multi-tab sync:

| Field          | Type   | Description                                                        |
| -------------- | ------ | ------------------------------------------------------------------ |
| `is_prompting` | bool   | Whether agent is currently responding                              |
| `is_running`   | bool   | Whether background session is active (ACP connected)               |
| `queue_length` | int    | Number of messages waiting in queue (enables multi-tab queue sync) |
| `status`       | string | Session status (`active`, `completed`, `error`)                    |

## Event Types in load_events

The `events_loaded` response contains an array of events. Each event has a `type` field:

| Event Type      | Description              |
| --------------- | ------------------------ |
| `user_prompt`   | User message             |
| `agent_message` | Agent response (HTML)    |
| `agent_thought` | Agent thinking/reasoning |
| `tool_call`     | Tool invocation          |
| `tool_update`   | Tool status update       |
| `file_read`     | File read operation      |
| `file_write`    | File write operation     |
| `plan`          | Agent plan               |

## load_events Parameters

| Parameter    | Type  | Description                                      |
| ------------ | ----- | ------------------------------------------------ |
| `limit`      | int   | Maximum events to return (default: 50, max: 500) |
| `before_seq` | int64 | Load events with seq < before_seq (pagination)   |
| `after_seq`  | int64 | Load events with seq > after_seq (sync)          |

**Note:** `before_seq` and `after_seq` are mutually exclusive.
