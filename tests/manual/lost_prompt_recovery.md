# Manual Test: Lost Prompt Recovery

This test verifies that unprocessed prompts are automatically re-delivered when a session is resumed after an ACP connection loss.

## Bug Description

When a user sends a prompt and the ACP connection drops between the moment the prompt is persisted to `events.jsonl` and the moment it is delivered to the agent, the prompt is silently lost. The agent never processes it.

## Fix Description

The fix adds automatic detection and re-delivery of unprocessed prompts during session resumption:

1. `HasUnprocessedPrompt()` function in `internal/session/player.go` detects if the last event is a `user_prompt` with no subsequent agent response
2. `checkAndRedeliverUnprocessedPrompt()` in `internal/web/background_session.go` is called during session resume
3. If an unprocessed prompt is found, it's automatically re-delivered to the agent

## Manual Test Steps

### Setup

1. Build Mitto: `go build -o mitto cmd/mitto/main.go`
2. Start Mitto web interface: `./mitto web`
3. Open browser to http://localhost:8080

### Test Procedure

1. **Create a new session** in the web interface

2. **Manually inject an unprocessed prompt** into the session's events.jsonl:
   ```bash
   # Find the session directory
   SESSION_DIR=~/Library/Application\ Support/Mitto/sessions/<session-id>
   
   # Append an unprocessed user_prompt event
   echo '{"type":"user_prompt","timestamp":"2026-03-06T12:00:00Z","data":{"message":"This is a test prompt that was lost","prompt_id":"test-lost-prompt"}}' >> "$SESSION_DIR/events.jsonl"
   ```

3. **Restart the Mitto server** to trigger session resumption:
   ```bash
   # Stop the server (Ctrl+C)
   # Start it again
   ./mitto web
   ```

4. **Reconnect to the session** in the web interface

5. **Verify the prompt is re-delivered**:
   - Check the server logs for: `Detected unprocessed prompt from previous session, re-delivering`
   - Check the server logs for: `Successfully re-delivered unprocessed prompt`
   - Verify the agent responds to the prompt in the UI
   - Check `events.jsonl` to confirm the prompt was re-delivered and the agent responded

### Expected Results

- Server logs show detection and re-delivery messages
- The agent processes the prompt and responds
- The `events.jsonl` file contains:
  - The original `user_prompt` event
  - A new `user_prompt` event with `sender_id: "recovery"`
  - Agent response events (`agent_message`, `agent_thought`, or `tool_call`)

### Verification

Check the logs in `~/Library/Logs/Mitto/mitto.log`:

```bash
tail -f ~/Library/Logs/Mitto/mitto.log | grep -E "(unprocessed|re-deliver)"
```

Expected log entries:
```
INFO Detected unprocessed prompt from previous session, re-delivering session_id=... prompt_seq=... message_preview=...
INFO Successfully re-delivered unprocessed prompt session_id=... prompt_seq=...
```

## Cleanup

After testing, you can delete the test session:
```bash
rm -rf ~/Library/Application\ Support/Mitto/sessions/<session-id>
```

## Notes

- The recovery happens automatically on session resume
- The re-delivered prompt has `sender_id: "recovery"` to distinguish it from the original
- The recovery is logged at INFO level for debugging
- If the session is already closed when recovery attempts to send, it will fail gracefully

