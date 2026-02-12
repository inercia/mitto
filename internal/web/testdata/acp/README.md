# ACP Event Replay Test Fixtures

This directory contains test fixtures in JSONL format that simulate real ACP event streams.
Each fixture file contains events with timestamps, allowing tests to replay them through
the full WebClient pipeline with realistic timing.

## Format

Each line is a JSON object representing an ACP event:

```json
{"timestamp": "2026-01-25T14:30:57.000Z", "type": "agent_message", "data": {"html": "Hello"}}
{"timestamp": "2026-01-25T14:30:57.050Z", "type": "agent_message", "data": {"html": " world\n"}}
{"timestamp": "2026-01-25T14:30:57.100Z", "type": "tool_call", "data": {"tool_call_id": "tc1", "title": "Read file", "status": "running"}}
```

Note: The `html` field in `agent_message` contains raw markdown text that will be processed
by the MarkdownBuffer. The field name matches the session event format.

## Event Types

- `agent_message`: Text chunk from the agent (data.html contains markdown)
- `tool_call`: Tool invocation (data.tool_call_id, data.title, data.status)
- `tool_update`: Tool status update (data.tool_call_id, data.status)
- `thought`: Agent thought (data.text)
- `plan`: Plan update (no data fields)

## Timing

The test runner calculates the time difference between consecutive events and sleeps
for that duration (optionally scaled by a speed factor). This allows testing:

- Soft timeout behavior (200ms)
- Inactivity timeout behavior (2s)
- Rapid streaming vs slow streaming
- Tool calls arriving mid-content

## Creating Fixtures from Real Sessions

1. Find a session with the problematic behavior in `~/Library/Application Support/Mitto/sessions/`
2. Open the `events.jsonl` file
3. Extract the relevant events
4. Adjust timestamps if needed (use relative times from first event)

## Fixture Files

- `list_with_pause.jsonl` - List with 2.5s pause mid-stream (tests inactivity timeout)
- `code_block_with_tool.jsonl` - Code block with tool call in the middle
- `table_slow_rows.jsonl` - Table with slow row delivery (400ms between rows)

