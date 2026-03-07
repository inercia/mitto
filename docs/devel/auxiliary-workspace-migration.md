# Auxiliary Workspace Migration Plan

## Overview

This document outlines the plan to migrate auxiliary conversations from a separate global ACP process to workspace-scoped sessions within each workspace's singleton `SharedACPProcess`.

## Current Architecture

**Global Auxiliary Process:**

- `auxiliary.Manager`: Owns a separate ACP process (`exec.Cmd`)
- Creates ONE session per Manager instance
- Global singleton via `Initialize()` and `GetManager()`
- Initialized with first workspace's ACP command
- Used for: title generation, prompt improvement, follow-up analysis

**Problem:**

- "First workspace wins" - auxiliary uses first workspace's ACP server
- Incorrect when user has multiple workspaces with different ACP servers
- Wastes resources (separate process for auxiliary tasks)
- Cannot support multiple concurrent auxiliary tasks efficiently

## Target Architecture

**Workspace-Scoped Auxiliary Sessions:**

- Each workspace has its own pool of auxiliary sessions
- Auxiliary sessions are regular ACP sessions within the workspace's `SharedACPProcess`
- Support multiple auxiliary sessions per workspace (e.g., title-gen, follow-up, improve-prompt)
- Sessions created on-demand and reused
- Proper workspace isolation

## Design

### 1. ProcessProvider Interface (Dependency Inversion)

**File:** `internal/auxiliary/provider.go`

```go
// ProcessProvider creates and manages auxiliary ACP sessions within workspace processes.
type ProcessProvider interface {
    // PromptAuxiliary sends a prompt to an auxiliary session for the given workspace and purpose.
    // The provider manages session creation and reuse internally.
    // purpose identifies the session type (e.g., "title-gen", "follow-up", "improve-prompt")
    PromptAuxiliary(ctx context.Context, workspaceUUID, purpose, message string) (string, error)

    // CloseWorkspaceAuxiliary closes all auxiliary sessions for a workspace.
    CloseWorkspaceAuxiliary(workspaceUUID string) error
}
```

**Rationale:** Dependency inversion keeps `internal/auxiliary` independent of `internal/web`.

### 2. WorkspaceAuxiliaryManager

**File:** `internal/auxiliary/workspace_manager.go`

```go
type WorkspaceAuxiliaryManager struct {
    mu       sync.RWMutex
    provider ProcessProvider // Injected dependency
    logger   *slog.Logger
}

// Workspace-scoped API
func (m *WorkspaceAuxiliaryManager) GenerateTitle(ctx context.Context, workspaceUUID, message string) (string, error)
func (m *WorkspaceAuxiliaryManager) ImprovePrompt(ctx context.Context, workspaceUUID, userPrompt string) (string, error)
func (m *WorkspaceAuxiliaryManager) AnalyzeFollowUpQuestions(ctx context.Context, workspaceUUID, userPrompt, agentMsg string) ([]FollowUpSuggestion, error)
```

### 3. ACPProcessManager Implements ProcessProvider

**File:** `internal/web/acp_process_manager.go`

```go
type ACPProcessManager struct {
    // ... existing fields ...

    // Auxiliary session tracking
    auxMu       sync.Mutex
    auxSessions map[auxSessionKey]*auxiliarySessionState
}

type auxSessionKey struct {
    workspaceUUID string
    purpose       string // "title-gen", "follow-up", "improve-prompt"
}

type auxiliarySessionState struct {
    mu        sync.Mutex // Serializes requests to this session
    sessionID string
    client    *auxiliaryClient // Collects responses
    lastUsed  time.Time
}
```

**Implementation:**

- `PromptAuxiliary`: Get or create auxiliary session, send prompt, collect response
- `CloseWorkspaceAuxiliary`: Close all auxiliary sessions for a workspace
- Session creation: Use `SharedACPProcess.NewSession()` with empty MCP servers
- Session cleanup: When workspace is closed or removed

### 4. Backward Compatibility

**File:** `internal/auxiliary/global.go`

Keep existing global API working (deprecated):

```go
// Deprecated: Use WorkspaceAuxiliaryManager.GenerateTitle instead
func GenerateTitle(ctx context.Context, message string) (string, error)
```

Global functions delegate to first workspace for backward compatibility.

## Migration Strategy

### Phase 1: Add New Infrastructure (Low Risk)

- [ ] Add `ProcessProvider` interface
- [ ] Add `WorkspaceAuxiliaryManager` type
- [ ] Add workspace-scoped API methods
- [ ] Keep global API intact

**Estimated effort:** 1-2 days

### Phase 2: Implement ProcessProvider (Medium Risk)

- [ ] Extend `ACPProcessManager` with auxiliary session tracking
- [ ] Implement `PromptAuxiliary` method
- [ ] Implement `CloseWorkspaceAuxiliary` method
- [ ] Add `auxiliaryClient` integration
- [ ] Handle concurrency (mutex per session)

**Estimated effort:** 2-3 days

### Phase 3: Update Call Sites (High Risk)

- [ ] Update `Server` to hold `WorkspaceAuxiliaryManager`
- [ ] Update `BackgroundSession.analyzeFollowUpQuestions` (has `workspaceUUID`)
- [ ] Update `GenerateAndSetTitle` (add workspace lookup)
- [ ] Update `QueueTitleWorker` (add workspace lookup)
- [ ] Update `handleImprovePrompt` (add workspace context to API)

**Estimated effort:** 3-4 days

### Phase 4: Testing (Medium Risk)

- [ ] Unit tests for `WorkspaceAuxiliaryManager`
- [ ] Unit tests for `ACPProcessManager` auxiliary methods
- [ ] Integration tests with mock ACP server
- [ ] Test concurrent requests to same auxiliary session
- [ ] Test multiple auxiliary sessions per workspace

**Estimated effort:** 1-2 days

### Phase 5: Documentation (Low Risk)

- [ ] Update `.augment/rules/02-session.md`
- [ ] Update `docs/devel/architecture.md`
- [ ] Add migration guide
- [ ] Update API documentation

**Estimated effort:** 1 day

**Total Estimated Effort:** 8-12 days

## Implementation Details

### Session Lifecycle

1. **Creation**: On-demand when first prompt is sent for a (workspace, purpose) pair
2. **Reuse**: Subsequent prompts to same (workspace, purpose) reuse the session
3. **Cleanup**: When workspace's `SharedACPProcess` is closed or workspace is removed

### Concurrency

- Each auxiliary session handles one request at a time (serialize with mutex)
- Different purposes can run in parallel (title-gen and follow-up simultaneously)
- Same purpose across different workspaces can run in parallel

### Error Handling

- **Workspace has no SharedACPProcess**: Create it on-demand
- **SharedACPProcess dies/restarts**: Auxiliary sessions lost, recreated on next request
- **Session creation fails**: Return error, don't cache, retry on next request
- **Workspace deleted during request**: Context cancellation stops request

## Files to Modify

### New Files

- `internal/auxiliary/provider.go`
- `internal/auxiliary/workspace_manager.go`
- `internal/auxiliary/workspace_manager_test.go`

### Modified Files

- `internal/auxiliary/global.go` - Deprecate, add backward compatibility
- `internal/web/acp_process_manager.go` - Implement ProcessProvider
- `internal/web/server.go` - Add auxiliaryManager field
- `internal/web/background_session.go` - Use workspace-scoped API
- `internal/web/title.go` - Add workspaceUUID parameter
- `internal/web/session_ws.go` - Pass workspaceUUID
- `internal/web/queue_title.go` - Add workspace lookup
- `internal/web/config_handlers.go` - Add workspace context to API
- `cmd/mitto/web.go` - Update initialization
- `cmd/mitto-app/main.go` - Update initialization

### Documentation

- `.augment/rules/02-session.md`
- `docs/devel/architecture.md`
- This migration guide

## Risks and Mitigation

| Risk                                | Impact | Mitigation                                      |
| ----------------------------------- | ------ | ----------------------------------------------- |
| Breaking changes during migration   | High   | Keep old API working, gradual migration         |
| Concurrency issues (deadlocks)      | Medium | Careful mutex design, extensive testing         |
| Session limit on ACP servers        | Low    | Document requirement, test with common agents   |
| Performance impact on user sessions | Low    | Auxiliary sessions are lightweight, short-lived |

## Benefits

1. **Correct Workspace Isolation**: Each workspace uses its own ACP server
2. **Resource Efficiency**: No separate process, reuse existing infrastructure
3. **Scalability**: Support multiple concurrent auxiliary tasks per workspace
4. **Consistency**: Auxiliary uses same agent as user sessions
5. **Simplified Architecture**: Remove separate process management code

## Rollback Plan

If issues arise:

1. Revert call site changes
2. Re-enable global auxiliary API
3. Keep new infrastructure for future retry
