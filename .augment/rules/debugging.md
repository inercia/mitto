# Debugging & Investigation Patterns

## Log Files
Located in `~/Library/Logs/Mitto/` (macOS):
- `mitto.log` — Main application log (server, ACP, sessions)
- `access.log` — Security events (auth, unauthorized access)
- `webview.log` — JavaScript console from WKWebView

## Session Data
Each session stores data in `~/Library/Application Support/Mitto/sessions/{session-id}/`:
- `events.jsonl` — Authoritative event record (user prompts, agent messages, tool calls)
- `images/` — Uploaded image files (referenced by UUID)
- `files/` — Uploaded general files

## Investigating Issues

### Check events.jsonl for the Session
```bash
cat ~/Library/Application\ Support/Mitto/sessions/{id}/events.jsonl | jq .
```
Each line is a JSON event with `seq`, `type`, `timestamp`, `data`. Look for:
- `user_prompt` events with `image_ids` to verify images were attached
- `agent_message` events to see what the agent actually received/said
- Gaps in sequence numbers indicating missed events

### Check Server Logs
```bash
grep "session_id.*{id}" ~/Library/Logs/Mitto/mitto.log | tail -50
```
Key log patterns:
- `"Sending prompt to ACP agent"` — Shows block counts (image_blocks, text_blocks)
- `"Agent capabilities"` — Shows what the agent advertised (prompt_image, prompt_audio)
- `"Agent does not support image prompts"` — Capability check triggered
- `"Failed to get image path"` — Image loading issue

### Log Rotation Gaps
Server logs rotate and can have gaps of 30+ minutes. If critical events are missing from logs, check `events.jsonl` instead — it's append-only and doesn't rotate.

## Common Investigation Steps

1. **"Agent didn't see my image"**
   - Check `events.jsonl` for `user_prompt` with `image_ids`
   - Check server log for `"Sending prompt to ACP agent"` — does it show `image_blocks > 0`?
   - Check server log for `"Agent does not support image prompts"` warning
   - Check `"Agent capabilities"` log line — is `prompt_image=true`?

2. **"WebSocket not receiving messages"**
   - Verify client sent `load_events` after connecting
   - Check observer count: `"Notifying multiple observers"` log entries
   - Check for `session_gone` message (session was deleted)

3. **"Agent process crashed"**
   - Look for `"ACP connection closed during prompt"` in logs
   - Check if auto-restart kicked in: `"The AI agent process stopped unexpectedly. Restarting (attempt N of 3)..."`

## MCP Debug Tools
The MCP server exposes debug tools accessible from the AI agent:
- `mitto_conversation_list` — List all sessions
- `mitto_get_config` — Current configuration
- `mitto_get_runtime_info` — Runtime state and health
