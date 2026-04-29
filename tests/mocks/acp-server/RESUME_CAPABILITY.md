# Mock ACP Server: Session Resume Support

## Overview

The mock ACP server now supports the ACP v0.12.0 session resume capability (`session/unstableResumeSession`). This allows Mitto to resume existing sessions without replaying the full history.

## Implementation

### 1. Types (`types.go`)

Added the following types to support session resume:

```go
// Session capabilities
type SessionCapabilities struct {
    Resume *SessionResumeCapabilities `json:"resume,omitempty"`
    Fork   *SessionForkCapabilities   `json:"fork,omitempty"`
    List   *SessionListCapabilities   `json:"list,omitempty"`
}

type SessionResumeCapabilities struct {
    Meta map[string]any `json:"_meta,omitempty"`
}

// Session resume request/response
type UnstableResumeSessionRequest struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    SessionID  SessionID      `json:"sessionId"`
    Cwd        string         `json:"cwd"`
    McpServers []McpServer    `json:"mcpServers,omitempty"`
}

type UnstableResumeSessionResponse struct {
    Meta          map[string]any        `json:"_meta,omitempty"`
    ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
    Models        *any                  `json:"models,omitempty"`
    Modes         *SessionModeState     `json:"modes,omitempty"`
}
```

### 2. Server State (`main.go`)

Added session state storage:

```go
type SessionState struct {
    SessionID     string
    Modes         *SessionModeState
    ConfigOptions []SessionConfigOption
}

type MockACPServer struct {
    mu           sync.Mutex
    sessions     map[string]*SessionState  // NEW: Session storage
    // ... other fields
}
```

### 3. Handlers (`handler.go`)

#### Initialize Handler
Now advertises session resume capability:

```go
result.AgentCapabilities.SessionCapabilities = &SessionCapabilities{
    Resume: &SessionResumeCapabilities{},
}
```

#### New Session Handler
Stores session state for later resume:

```go
s.sessions[s.sessionID] = &SessionState{
    SessionID:     s.sessionID,
    Modes:         modes,
    ConfigOptions: []SessionConfigOption{},
}
```

#### Resume Handler
Restores session state:

```go
func (s *MockACPServer) handleUnstableResumeSession(req JSONRPCRequest) error {
    // Parse request
    // Look up session in s.sessions map
    // Return error if not found
    // Restore session state (s.sessionID, s.currentMode)
    // Return session state (modes, configOptions)
}
```

### 4. Method Registration
Added `session/unstableResumeSession` to the method dispatch:

```go
case "session/unstableResumeSession":
    return s.handleUnstableResumeSession(req)
```

## Testing

### Unit Test (Python)

Run `tests/mocks/acp-server/test_resume.py` to verify:

1. ✓ Resume capability is advertised in `initialize`
2. ✓ Session can be created and stored
3. ✓ Session can be resumed successfully
4. ✓ Non-existent sessions return proper error

```bash
python3 tests/mocks/acp-server/test_resume.py
```

Expected output:
```
✓ Resume capability advertised
✓ Session created: mock-session-XXX
✓ Session resumed successfully
✓ Correctly rejected non-existent session
✓ ALL TESTS PASSED
```

### Integration Tests

Integration tests are in `tests/integration/inprocess/session_resume_test.go`:

- `TestSessionResume_PreferResumeOverLoad` - Verifies resume flow through archive/unarchive
- `TestSessionResume_SessionNotFound` - Tests error handling for missing sessions
- `TestSessionResume_ModePreservation` - Verifies mode state is preserved

**Note:** Integration tests require the main Mitto codebase to support resume (Tasks 1-3).
They will compile but may not fully execute until the ACP client wrapper is updated.

## Protocol Flow

### Successful Resume

```
Client → Server: initialize
Server → Client: { agentCapabilities: { sessionCapabilities: { resume: {} } } }

Client → Server: session/new { cwd: "/path" }
Server → Client: { sessionId: "xxx", modes: {...} }

[Session is used, then archived]

Client → Server: session/unstableResumeSession { sessionId: "xxx", cwd: "/path" }
Server → Client: { modes: {...}, configOptions: [] }
```

### Failed Resume (Session Not Found)

```
Client → Server: session/unstableResumeSession { sessionId: "unknown" }
Server → Client: { error: { code: -32000, message: "session not found: unknown (may have been garbage collected)" } }
```

## Fallback Chain

When Mitto unarchives a session, it should attempt (in order):

1. **Resume** (if capability advertised) - Fast, no history replay
2. **Load** (if resume fails) - Replays history from events.jsonl
3. **New** (if both fail) - Creates fresh session, loses history

The mock server simulates step 1. Sessions persist in memory until the mock process terminates.

## Future Enhancements

1. **Session expiration** - Automatically remove sessions after timeout
2. **Config options** - Store and return actual config options per session
3. **Mode changes** - Update session state when mode is changed via `session/setMode`
4. **Load handler** - Add `session/load` handler for fallback testing
5. **Metrics** - Track resume vs load vs new session creation

## References

- ACP SDK v0.12.0: https://github.com/coder/acp-go-sdk
- Session Resume Analysis: `docs/devel/session-resume-analysis.md`
- Parent Task: Session 20260425-223905-8498c510
