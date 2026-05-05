# Session Resume Support Analysis

**Status:** Analysis Document  
**Date:** 2026-04-25  
**Author:** System Analysis  

## Executive Summary

The ACP SDK **does support session resume** via an **UNSTABLE/experimental API** (`session/resume`). Mitto currently uses `session/load` which replays conversation history, but could implement faster session switching using `session/resume` when agents support it. This document analyzes the current state, SDK capabilities, available data, and provides an implementation plan.

---

## 1. ACP SDK Resume Support

### Available Methods

The `github.com/coder/acp-go-sdk` package provides:

| Component | Type | Status |
|-----------|------|--------|
| `UnstableResumeSession()` | Method on `ClientSideConnection` | ⚠️ **UNSTABLE** |
| `UnstableResumeSessionRequest` | Request struct | ⚠️ **UNSTABLE** |
| `UnstableResumeSessionResponse` | Response struct | ⚠️ **UNSTABLE** |
| `SessionResumeCapabilities` | Capability struct | ⚠️ **UNSTABLE** |
| `SessionCapabilities.Resume` | Capability field (`*SessionResumeCapabilities`) | ⚠️ **UNSTABLE** |

### API Signature

```go
// From github.com/coder/acp-go-sdk
func (c *ClientSideConnection) UnstableResumeSession(
    ctx context.Context, 
    params UnstableResumeSessionRequest,
) (UnstableResumeSessionResponse, error)

type UnstableResumeSessionRequest struct {
    Meta       map[string]any `json:"_meta,omitempty"`
    SessionId  SessionId      `json:"sessionId"`      // **REQUIRED**
    Cwd        string         `json:"cwd"`            // **REQUIRED**
    McpServers []McpServer    `json:"mcpServers,omitempty"`
}

type UnstableResumeSessionResponse struct {
    Meta          map[string]any              `json:"_meta,omitempty"`
    ConfigOptions []SessionConfigOption       `json:"configOptions,omitempty"`
    Models        *UnstableSessionModelState  `json:"models,omitempty"`
    Modes         *SessionModeState           `json:"modes,omitempty"`
}
```

### Key Difference: Resume vs Load

| Feature | `session/resume` | `session/load` |
|---------|------------------|----------------|
| **History Replay** | ❌ No — session state already exists | ✅ Yes — replays all previous messages |
| **Speed** | ⚡ Fast (instant) | 🐌 Slow (70+ seconds for long sessions) |
| **Agent State** | Agent must maintain state | Agent rebuilds state from history |
| **Use Case** | Quick session switching | First-time resume or stateless agents |
| **ACP Spec Status** | ⚠️ UNSTABLE | ✅ Stable (in main spec) |

**Resume is faster** because it assumes the agent already has the session in memory. It's ideal for switching between recent sessions that the agent hasn't garbage-collected yet.

---

## 2. Mitto Session Data Storage

Each session is stored in `$MITTO_DIR/sessions/{session-id}/`:

| File | Content | Used by Resume? |
|------|---------|-----------------|
| `metadata.json` | Session metadata (see below) | ✅ **Required** (ACPSessionID, WorkingDir, ACPServer) |
| `events.jsonl` | Full event log (prompts, messages, tool calls) | ❌ Not needed (unlike load) |
| `images/` | Uploaded image files (referenced by UUID) | ❌ Not directly |
| `files/` | Uploaded general files | ❌ Not directly |
| `queue.json` | Queued messages (optional) | ❌ Not directly |
| `action_buttons.json` | Follow-up suggestions (optional) | ❌ Not directly |

### Metadata Structure

From `internal/session/types.go` (lines 208-240):

```go
type Metadata struct {
    SessionID         string          // Mitto's persisted session ID
    Name              string          // User-friendly session name
    ACPServer         string          // **REQUIRED for resume** — which ACP command
    ACPSessionID      string          // **REQUIRED for resume** — ACP protocol session ID
    WorkingDir        string          // **REQUIRED for resume** — working directory
    CreatedAt         time.Time
    UpdatedAt         time.Time
    LastUserMessageAt time.Time
    EventCount        int
    MaxSeq            int64
    Status            SessionStatus   // active, suspended, etc.
    Description       string
    Archived          bool            // If true, don't auto-resume
    ArchivedAt        time.Time
    RunnerType        string          // exec, firejail, docker, etc.
    RunnerRestricted  bool
    CurrentModeID     string          // Current session mode (ask, code, etc.)
    AdvancedSettings  map[string]bool // Per-session feature flags
    ParentSessionID   string          // For child sessions
    ChildOrigin       ChildOrigin     // How child was created
}
```

---

## 3. Data Availability Assessment

### ✅ Required Data — Available

| Field | Source | Status |
|-------|--------|--------|
| `SessionId` | `metadata.ACPSessionID` | ✅ Stored during session creation |
| `Cwd` | `metadata.WorkingDir` | ✅ Stored during session creation |
| `McpServers` | Computed at runtime | ⚠️ **Not persisted** (see Gaps) |

### Current Flow (Load Session)

From `internal/web/session_manager.go` line 1850+:

```go
// Current implementation uses LoadSession:
bs, err := ResumeBackgroundSession(BackgroundSessionConfig{
    PersistedID:      sessionID,
    ACPCommand:       acpCommand,
    ACPCwd:           acpCwd,
    ACPServer:        acpServer,
    ACPSessionID:     acpSessionID,  // ← From metadata
    WorkingDir:       workingDir,     // ← From metadata
    // ... other config
})
```

And in `internal/web/background_session.go` line 2095+:

```go
// resumeSharedACPSession tries LoadSession if ACPSessionID exists:
if acpSessionID != "" && caps.LoadSession {
    handle, err = sharedProcess.LoadSession(ctx, acpSessionID, workingDir, mcpServers)
    // Falls back to NewSession if load fails
}
```

**Conclusion:** We have all **required** data for resume. The missing `McpServers` config is also missing for load, so it's not a blocker.

---

## 4. Implementation Plan

### A. Capability Detection

**Where to check:**
- `SessionCapabilities.Resume != nil` (not `AgentCapabilities.LoadSession`)

**When to check:**
- During ACP initialization in `SharedACPProcess.doStartProcess()` (line 461+)
- After receiving `InitializeResponse` and storing capabilities

**Where to store:**
```go
// Add to SessionHandle (internal/web/shared_acp_process.go):
type SessionHandle struct {
    SessionID    string
    Capabilities acp.AgentCapabilities  // Contains SessionCapabilities
    Modes        *acp.SessionModeState
    Process      *SharedACPProcess
}

// Check capability:
if handle.Capabilities.SessionCapabilities != nil && 
   handle.Capabilities.SessionCapabilities.Resume != nil {
    // Agent supports resume
}
```

**Add logging:**
```go
// In doStartProcess after storing capabilities:
if p.logger != nil {
    p.logger.Debug("Agent session capabilities",
        "acp_server", p.config.ACPServer,
        "resume_supported", initResp.AgentCapabilities.SessionCapabilities != nil && 
                           initResp.AgentCapabilities.SessionCapabilities.Resume != nil,
        "fork_supported", initResp.AgentCapabilities.SessionCapabilities != nil &&
                         initResp.AgentCapabilities.SessionCapabilities.Fork != nil,
        "list_supported", initResp.AgentCapabilities.SessionCapabilities != nil &&
                         initResp.AgentCapabilities.SessionCapabilities.List != nil)
}
```

---

### B. Implementation Steps

#### Step 1: Add ResumeSession Method to SharedACPProcess

In `internal/web/shared_acp_process.go`, add a new method parallel to `LoadSession()`:

```go
// ResumeSession attempts to resume an existing ACP session without replaying history.
// This is faster than LoadSession but requires the agent to support session/resume
// and still have the session in memory.
func (p *SharedACPProcess) ResumeSession(ctx context.Context, acpSessionID, cwd string, mcpServers []acp.McpServer) (*SessionHandle, error) {
    p.activeRPCs.Add(1)
    defer p.activeRPCs.Add(-1)

    totalStart := time.Now()

    p.mu.RLock()
    conn := p.conn
    caps := p.capabilities
    p.mu.RUnlock()

    if conn == nil {
        return nil, fmt.Errorf("shared ACP process is not running")
    }
    
    // Check capability
    if caps == nil || caps.SessionCapabilities == nil || caps.SessionCapabilities.Resume == nil {
        return nil, fmt.Errorf("agent does not support session resume (UNSTABLE API)")
    }

    if cwd == "" {
        cwd = "."
    }

    rpcStart := time.Now()
    resumeResp, err := conn.UnstableResumeSession(ctx, acp.UnstableResumeSessionRequest{
        SessionId:  acp.SessionId(acpSessionID),
        Cwd:        cwd,
        McpServers: mcpServers,
    })
    rpcDuration := time.Since(rpcStart)

    if err != nil {
        if p.logger != nil {
            p.logger.Info("SharedACPProcess.ResumeSession failed (UNSTABLE API)",
                "acp_session_id", acpSessionID,
                "rpc_ms", rpcDuration.Milliseconds(),
                "error", err)
        }
        return nil, fmt.Errorf("failed to resume session: %w", err)
    }

    handle := &SessionHandle{
        SessionID:    acpSessionID,
        Capabilities: *caps,
        Modes:        resumeResp.Modes,
        Process:      p,
    }

    if p.logger != nil {
        p.logger.Info("Resumed ACP session on shared process (UNSTABLE API)",
            "acp_session_id", acpSessionID,
            "total_ms", time.Since(totalStart).Milliseconds(),
            "rpc_resume_session_ms", rpcDuration.Milliseconds())
    }

    return handle, nil
}
```

---

#### Step 2: Update resumeSharedACPSession Decision Flow

In `internal/web/background_session.go` line 2095+, update the logic to prefer resume over load:

```go
func (bs *BackgroundSession) resumeSharedACPSession(sharedProcess *SharedACPProcess, workingDir, acpSessionID string) error {
    bs.sharedProcess = sharedProcess

    var caps acp.AgentCapabilities
    if sharedCaps := sharedProcess.Capabilities(); sharedCaps != nil {
        caps = *sharedCaps
    }
    mcpServers := bs.startSessionMcpServer(bs.store, caps)

    bs.acpClient = NewWebClient(bs.buildWebClientConfig())

    var handle *SessionHandle
    var err error

    // Try to resume an existing session if we have an ID.
    // Prefer Resume over Load for speed (no history replay).
    if acpSessionID != "" {
        // Check capabilities
        supportsResume := caps.SessionCapabilities != nil && caps.SessionCapabilities.Resume != nil
        supportsLoad := caps.LoadSession

        // Try Resume first (fast path)
        if supportsResume {
            resumeCtx, resumeCancel := context.WithTimeout(bs.ctx, 10*time.Second)
            handle, err = sharedProcess.ResumeSession(resumeCtx, acpSessionID, workingDir, mcpServers)
            resumeCancel()
            if err != nil {
                logFields := []any{
                    "acp_session_id", acpSessionID,
                    "error", err,
                    "method", "resume",
                }
                if resumeCtx.Err() == context.DeadlineExceeded {
                    logFields = append(logFields, "timeout", true)
                }
                if bs.logger != nil {
                    bs.logger.Info("Resume failed, will try Load or New",
                        logFields...)
                }
                // Fall through to try Load
            } else {
                if bs.logger != nil {
                    bs.logger.Info("Successfully resumed session using UNSTABLE resume API",
                        "acp_session_id", acpSessionID)
                }
            }
        }

        // Fallback to Load (slow path with history replay)
        if handle == nil && supportsLoad {
            loadCtx, loadCancel := context.WithTimeout(bs.ctx, 30*time.Second)
            handle, err = sharedProcess.LoadSession(loadCtx, acpSessionID, workingDir, mcpServers)
            loadCancel()
            if err != nil {
                logFields := []any{
                    "acp_session_id", acpSessionID,
                    "error", err,
                    "method", "load",
                }
                if loadCtx.Err() == context.DeadlineExceeded {
                    logFields = append(logFields, "timeout", true)
                }
                if bs.logger != nil {
                    bs.logger.Info("Load failed, creating new session",
                        logFields...)
                }
            }
        }
    }

    // Final fallback: create new session
    if handle == nil {
        handle, err = sharedProcess.NewSession(bs.ctx, workingDir, mcpServers)
        if err != nil {
            bs.stopSessionMcpServer()
            bs.acpClient.Close()
            bs.acpClient = nil
            bs.sharedProcess = nil
            return fmt.Errorf("failed to create session on shared process: %w", err)
        }
    }

    // ... rest of method unchanged
}
```

---

#### Step 3: Add Metrics and Logging

Track which method was used for session resume:

```go
// Add to BackgroundSession struct:
type BackgroundSession struct {
    // ... existing fields
    resumeMethod string // "resume", "load", or "new"
}

// Log at INFO level when session is successfully resumed:
if bs.logger != nil {
    bs.logger.Info("Resumed ACP session on shared process",
        "session_id", bs.persistedID,
        "acp_session_id", bs.acpID,
        "requested_acp_session_id", acpSessionID,
        "resume_method", bs.resumeMethod,  // NEW
        "supports_images", bs.agentSupportsImages)
}
```

---

### C. Decision Flow Diagram

```
┌─────────────────────────────────────┐
│ User switches to old conversation   │
└────────────────┬────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────┐
│ Have ACPSessionID in metadata?      │
└────────┬─────────────────┬──────────┘
         │ NO              │ YES
         ▼                 ▼
    ┌────────┐   ┌──────────────────────┐
    │ Create │   │ Check Agent Caps     │
    │  New   │   └──────┬───────────────┘
    │Session │          │
    └────────┘          ▼
                 ┌──────────────────────┐
                 │ Supports Resume?     │
                 └──┬──────────────┬────┘
                    │ YES          │ NO
                    ▼              ▼
         ┌──────────────────┐  ┌────────────────┐
         │ Try Resume (fast)│  │ Supports Load? │
         └────┬─────────┬───┘  └───┬────────┬───┘
              │ Success │ Fail     │ YES    │ NO
              ▼         ▼          ▼        ▼
         ✅ Done   ┌────────────────┐   ┌────────┐
                   │ Try Load (slow)│   │ Create │
                   └────┬───────┬───┘   │  New   │
                        │Success│Fail   └────────┘
                        ▼       ▼
                    ✅ Done  ┌────────┐
                             │ Create │
                             │  New   │
                             └────────┘
```

---

### D. Testing Requirements

#### 1. Mock ACP Server Updates

Update `tests/mocks/acp-server/types.go`:

```go
// Add to AgentCapabilities:
type AgentCapabilities struct {
    // ... existing fields
    SessionCapabilities *SessionCapabilities `json:"sessionCapabilities,omitempty"`
}

type SessionCapabilities struct {
    Resume *SessionResumeCapabilities `json:"resume,omitempty"`
    Fork   *SessionForkCapabilities   `json:"fork,omitempty"`
    List   *SessionListCapabilities   `json:"list,omitempty"`
}

type SessionResumeCapabilities struct {
    Meta map[string]any `json:"_meta,omitempty"`
}
```

Update `tests/mocks/acp-server/handler.go`:

```go
// Add handler for session/resume:
func (s *Server) handleUnstableResumeSession(params json.RawMessage) (any, error) {
    var req UnstableResumeSessionRequest
    if err := json.Unmarshal(params, &req); err != nil {
        return nil, err
    }
    
    // Check if we have this session
    s.mu.Lock()
    sess, exists := s.sessions[string(req.SessionID)]
    s.mu.Unlock()
    
    if !exists {
        return nil, fmt.Errorf("session not found: %s", req.SessionID)
    }
    
    // Return session state without replaying history
    return UnstableResumeSessionResponse{
        Modes: sess.Modes,
        ConfigOptions: sess.ConfigOptions,
    }, nil
}
```

#### 2. Integration Tests

Add test in `tests/integration/inprocess/`:

```go
func TestSessionResume_UsingUnstableResumeAPI(t *testing.T) {
    // Test that resume is preferred over load when supported
    // Test fallback to load when resume fails
    // Test fallback to new when both fail
}
```

#### 3. Manual Testing

Test with real agents:
- Claude Code (check if it supports resume)
- Auggie (check if it supports resume)
- Log which method is actually used

---

## 5. Gaps and Considerations

### ⚠️ Critical Gaps

#### 1. MCP Server Configuration Not Persisted

**Problem:**
- `LoadSession` and `ResumeSession` both require `mcpServers []acp.McpServer`
- Currently computed at runtime from workspace config
- If workspace config changes, we may pass different servers than original session

**Impact:**
- Medium — sessions work but may have different tools available

**Solution:**
- Add `MCPServers []acp.McpServer` to `session.Metadata`
- Persist during session creation
- Use persisted config during resume

**Example:**
```go
type Metadata struct {
    // ... existing fields
    MCPServers []acp.McpServer `json:"mcp_servers,omitempty"` // NEW
}
```

---

#### 2. UNSTABLE API Warning

**Problem:**
- Resume API is marked UNSTABLE and **may change or be removed**
- No guarantees about backward compatibility

**Risks:**
- SDK update could break resume functionality
- Agents may implement it differently

**Mitigation:**
- Always have fallback to Load
- Log when UNSTABLE APIs are used
- Monitor SDK release notes
- Add integration tests to catch breaking changes

**Implementation:**
```go
// Wrap all resume calls with clear unstable warnings:
if bs.logger != nil {
    bs.logger.Warn("Using UNSTABLE session/resume API - may change in future SDK versions",
        "acp_session_id", acpSessionID)
}
```

---

#### 3. Agent Session Garbage Collection

**Problem:**
- Resume assumes agent still has session in memory
- Agents may garbage-collect old sessions
- No way to know if session is still available without trying

**Impact:**
- Resume will fail for old sessions
- Need fallback to Load (already planned)

**Mitigation:**
- Already handled by fallback logic
- Consider time-based heuristic: only try resume for sessions accessed < 1 hour ago

---

### 📊 Performance Considerations

#### Resume vs Load Performance

Based on comments in code (line 102: "LoadSession can take 70+ seconds"):

| Method | Typical Time | Use Case |
|--------|--------------|----------|
| Resume | < 1 second | Recent sessions still in agent memory |
| Load | 10-70+ seconds | Old sessions or first-time resume |
| New | 1-5 seconds | Fresh conversation |

**Expected improvement:**
- 10-70x faster for recent sessions
- No network/disk overhead from history replay

---

### 🔐 Security Considerations

**No additional security risks:**
- Resume uses same authentication as Load
- No new attack surface
- Same permission model

---

## 6. Recommended Next Steps

### Phase 1: Observability (High Priority) ✅

**Goal:** Understand current landscape before implementing resume

**Tasks:**
1. ✅ **Add capability logging** (already implemented in this session)
   - Log `SessionCapabilities.Resume` support
   - Log `SessionCapabilities.Fork` support
   - Log `SessionCapabilities.List` support

2. ✅ **Add Meta field logging** (already implemented in this session)
   - Log `InitializeResponse.Meta`
   - Log `AgentCapabilities.Meta`
   - Log `AgentInfo.Meta`

3. **Monitor production logs:**
   - Track which agents support resume
   - Track how often we use Load vs New
   - Identify performance bottlenecks

**Deliverables:**
- Log analysis showing agent capabilities
- Performance metrics for Load operations

---

### Phase 2: Resume Implementation (Medium Priority)

**Goal:** Implement resume with proper fallbacks

**Tasks:**
1. Add `ResumeSession()` method to `SharedACPProcess`
2. Update `resumeSharedACPSession()` decision logic
3. Add resume metrics logging
4. Update mock ACP server to support resume
5. Write integration tests

**Deliverables:**
- Working resume implementation
- Fallback to Load when resume fails
- Integration test coverage

**Estimated Effort:** 1-2 days

---

### Phase 3: MCP Server Persistence (Low Priority)

**Goal:** Persist MCP server config for consistent resume

**Tasks:**
1. Add `MCPServers` field to `session.Metadata`
2. Persist during session creation
3. Load during resume
4. Handle migration for existing sessions

**Deliverables:**
- Consistent MCP server config across resume
- Backward-compatible metadata format

**Estimated Effort:** 0.5-1 day

---

### Phase 4: Production Monitoring (Ongoing)

**Goal:** Track resume success/failure in production

**Metrics to track:**
- Resume attempts vs successes
- Resume failures with fallback to Load
- Resume performance (time to ready)
- Agent capability adoption (% supporting resume)

**Deliverables:**
- Dashboard showing resume usage
- Alerts for high resume failure rates

---

## 7. Conclusion

**Can we implement resume?** ✅ **Yes, with caveats:**

| Aspect | Status | Notes |
|--------|--------|-------|
| SDK Support | ✅ Available | UNSTABLE API |
| Data Availability | ✅ Sufficient | ACPSessionID, WorkingDir, ACPServer all stored |
| Fallback Strategy | ✅ Planned | Resume → Load → New |
| Testing | ⚠️ Needs work | Mock server needs updates |
| Production Ready | ⚠️ Not yet | Need Phase 2 implementation |

**Key Takeaways:**

1. **Resume is faster** (instant vs 70+ seconds for long conversations)
2. **Already have required data** (ACPSessionID stored in metadata)
3. **UNSTABLE API** requires careful handling and fallbacks
4. **Current Load implementation** provides good fallback path
5. **Low risk** due to multi-level fallback strategy

**Recommended Action:**
- ✅ Phase 1 already completed (capability logging)
- 🚀 Proceed with Phase 2 implementation
- 📊 Monitor agent adoption of resume capability
- 🔄 Iterate based on production metrics

---

## References

- ACP Protocol Docs: https://agentclientprotocol.com/protocol/initialization
- SDK Source: `github.com/coder/acp-go-sdk`
- Current Load Implementation: `internal/web/shared_acp_process.go:606`
- Current Resume Flow: `internal/web/background_session.go:2095`
- Metadata Storage: `internal/session/types.go:208`
