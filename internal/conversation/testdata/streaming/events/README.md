# Event-Based Streaming Test Fixtures

This directory contains test fixtures in JSONL format that simulate real streaming scenarios.
Each fixture file contains events with timestamps, allowing tests to replay them with realistic timing.

## Format

Each line is a JSON object with the same structure as session events:

```json
{"seq": 1, "type": "agent_message", "timestamp": "2026-01-25T14:30:57.000Z", "data": {"html": "Hello"}}
{"seq": 2, "type": "agent_message", "timestamp": "2026-01-25T14:30:57.050Z", "data": {"html": " world"}}
{"seq": 3, "type": "tool_call", "timestamp": "2026-01-25T14:30:57.100Z", "data": {"tool_call_id": "tc1", "title": "Read file", "status": "running"}}
```

Note: The field is named `html` for compatibility with the session event format, but in test
fixtures it contains raw markdown that will be processed by the MarkdownBuffer.

## Event Types

- `agent_message`: Text chunk from the agent (data.html contains markdown to be processed)
- `tool_call`: Tool invocation (data.tool_call_id, data.title, data.status)
- `tool_call_update`: Tool status update

## Timing

The test runner calculates the time difference between consecutive events and sleeps
for that duration (optionally scaled by a speed factor). This allows testing:

- Soft timeout behavior (200ms)
- Inactivity timeout behavior (2s)
- Rapid streaming vs slow streaming

## Creating Fixtures

1. Capture real events from a problematic session
2. Extract the relevant portion
3. Adjust timestamps if needed (use relative times from first event)

## Fixture Naming

- `list_split_apostrophe.jsonl` - List split at apostrophe bug
- `code_block_pause.jsonl` - Code block with pause in middle
- `table_slow_rows.jsonl` - Table with slow row delivery

