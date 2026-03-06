# Fix: Lost Prompt Recovery on ACP Disconnect

**Date:** 2026-03-06  
**Issue:** Prompts lost when ACP connection drops between persistence and delivery  
**Status:** Fixed

## Problem

When a user sends a prompt and the ACP connection drops (or was never alive) between the moment the prompt is persisted to `events.jsonl` and the moment it is delivered to the agent, the prompt is silently lost. The agent never processes it.

### Evidence

In session `20260305-183318-b9048528` ("Save Prompt Button"), the `events.jsonl` contains:
- seq 1: `session_start`
- seq 2: `user_prompt` (a detailed feature request sent at 21:30:24)
- No subsequent events (no tool calls, no agent message, no reply)

The session remained `is_running: true` for 12+ hours with no activity. The prompt was written to disk but never delivered.

## Root Cause

The session persistence flow is:
1. User sends prompt via WebSocket
2. Prompt is persisted to `events.jsonl` (via `Recorder.RecordUserPrompt`)
3. Prompt is delivered to ACP agent (via `acpSession.Prompt`)

If the ACP connection is lost between steps 2 and 3, the prompt is persisted but never delivered. On session resume, there was no logic to detect and re-deliver unprocessed prompts.

## Solution

Added automatic detection and re-delivery of unprocessed prompts during session resumption:

### 1. Detection Logic (`internal/session/player.go`)

Added `HasUnprocessedPrompt()` function that:
- Scans events from the end backwards to find the last `user_prompt`
- Checks if there are any subsequent agent response events (`agent_message`, `agent_thought`, or `tool_call`)
- Returns prompt details if unprocessed

```go
type UnprocessedPromptInfo struct {
    Message  string   // The prompt message text
    ImageIDs []string // IDs of images attached to the prompt
    FileIDs  []string // IDs of files attached to the prompt
    Seq      int64    // Sequence number of the prompt
    Found    bool     // Whether an unprocessed prompt was found
}

func HasUnprocessedPrompt(events []Event) UnprocessedPromptInfo
```

### 2. Recovery Logic (`internal/web/background_session.go`)

Added `checkAndRedeliverUnprocessedPrompt()` method that:
- Is called during `ResumeBackgroundSession` after ACP connection is established
- Reads all events from the session
- Calls `HasUnprocessedPrompt()` to detect unprocessed prompts
- Re-delivers the prompt asynchronously with metadata indicating it's a recovery

The re-delivered prompt has:
- `sender_id: "recovery"` to distinguish it from the original
- `prompt_id: "recovery-{seq}"` to track the recovery
- Original message, images, and files

### 3. Logging

Added INFO-level logging for debugging:
- `Detected unprocessed prompt from previous session, re-delivering`
- `Successfully re-delivered unprocessed prompt`
- `Failed to re-deliver unprocessed prompt` (on error)

## Testing

### Unit Tests

Added comprehensive unit tests in `internal/session/player_test.go`:
- `TestHasUnprocessedPrompt` with 9 test cases covering:
  - Empty events
  - No user prompt
  - Processed prompts (with agent_message, agent_thought, tool_call)
  - Unprocessed prompts (last event is user_prompt)
  - Prompts with images and files
  - Multiple conversation turns
  - Prompts followed by non-response events

All tests pass ✅

### Integration Tests

Existing web tests pass ✅

### Manual Testing

See `tests/manual/lost_prompt_recovery.md` for manual test procedure.

## Files Changed

- `internal/session/player.go`: Added `HasUnprocessedPrompt()` and `UnprocessedPromptInfo` type
- `internal/session/player_test.go`: Added unit tests
- `internal/web/background_session.go`: Added `checkAndRedeliverUnprocessedPrompt()` and integration
- `docs/fixes/lost-prompt-recovery.md`: This document
- `tests/manual/lost_prompt_recovery.md`: Manual test procedure

## Deployment Notes

- No database migrations required
- No configuration changes required
- Backward compatible - works with existing sessions
- Recovery happens automatically on session resume (server restart or reconnect)

## Future Improvements

Potential enhancements (not implemented in this fix):
1. Add a UI indicator when a prompt is recovered
2. Track recovery attempts in metadata to prevent infinite loops
3. Add a timeout for recovery attempts
4. Expose recovery status via API for monitoring

