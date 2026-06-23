---
description: BackgroundSession component extraction pattern, stateless seams, lock ordering, compile-time assertions, and delegation
globs:
  - "internal/conversation/background_session.go"
  - "internal/conversation/bgsession_*.go"
  - "internal/conversation/*_coordinator.go"
  - "internal/conversation/*_manager.go"
  - "internal/conversation/*_analyzer.go"
keywords:
  - component extraction
  - stateless component
  - deps seam
  - lock ordering
  - delegation
---

# BackgroundSession Component Extraction Pattern

Decomposing `background_session.go` (6,483 LOC → focused ~500 LOC components).

## Extraction Strategy

**Goal**: Extract cohesive method clusters into new files/packages, preserving lock ordering and exported API.

### File Naming
- **Coordinator**: Orchestrates workflows (e.g., `follow_up_coordinator.go`)
- **Manager**: Manages state (e.g., `config_manager.go`)
- **Analyzer**: Analyzes data without mutation (e.g., `shared_session_analyzer.go`)

### Structure Pattern

```go
// 1. NEW FILE: internal/conversation/component_name.go
type componentName struct {
    deps *componentDeps
}

// Unexported deps seam — never exported, always embedded in component
type componentDeps struct {
    // Shared fields from BackgroundSession
    mdBuf     *MarkdownBuffer
    promptMu  *sync.Mutex
    // ... other deps
}

// Export only needed methods; internal methods on receiver
func (c *componentName) PublicMethod() { c.doInternal() }
func (c *componentName) doInternal()   { /*...*/ }

// 2. COMPANION: internal/conversation/component_name_test.go
// Fake the deps struct for testing; use compile-time assertion
var _ conversation.SomeInterface = (*componentName)(nil)
```

## Preserved Invariants

- **Lock ordering**: `promptMu → pendingConfigMu` (or other chains) never violated
- **Exported methods**: Same signature, same visibility as before
- **Exported interfaces**: No changes to `SessionObserver`, `SessionManager` contracts

## Delegation Pattern in Original File

When component is extracted, original delegator becomes thin:

```go
// In background_session.go: thin wrapper
func (bs *BackgroundSession) GetConfig() Model {
    return bs.configMgr.GetConfig()
}
```

## Testing Pattern

Use unexported `deps` struct — provide fake implementations:

```go
type fakePrompter struct { /*...*/ }
func TestComponent_Method(t *testing.T) {
    c := &componentName{
        deps: &componentDeps{
            promptMu: &sync.Mutex{},
            mdBuf: newFakeBuffer(),
        },
    }
    // Test against fake deps
}
```

## Compile-Time Assertions

Place in implementation file to catch interface breaking changes:

```go
// Verify component satisfies interface at compile time
var _ conversation.SharedProcess = (*sharedSessionAnalyzer)(nil)
```
