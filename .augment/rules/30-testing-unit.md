---
description: Go unit test conventions, table-driven tests, test isolation, and mocking patterns
globs:
  - "internal/**/*_test.go"
  - "**/*_test.go"
---

# Go Unit Test Conventions

## Running Tests

```bash
make test              # All unit tests
make test-go           # Go unit tests only
make test-js           # JavaScript unit tests only
```

## Unit Test Conventions

```go
// Use t.TempDir() for file-based tests
func TestSomething(t *testing.T) {
    tmpDir := t.TempDir()
    store, err := NewStore(tmpDir)
    if err != nil {
        t.Fatalf("NewStore failed: %v", err)
    }
    defer store.Close()
}

// Naming: TestType_Method or TestType_Scenario
func TestStore_CreateAndGet(t *testing.T) { ... }
func TestLock_ForceAcquireIdle(t *testing.T) { ... }

// Table-driven tests for multiple scenarios
func TestParse(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    Config
        wantErr bool
    }{
        {"valid config", "...", Config{...}, false},
        {"empty input", "", Config{}, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) { /* ... */ })
    }
}
```

## Test Isolation for Global State

When tests modify global state (environment variables, caches), use `t.Setenv()` and `t.Cleanup()`:

### ❌ Anti-Pattern: Manual Save/Restore

```go
// BAD: Race condition when tests run in parallel across packages
func TestSomething(t *testing.T) {
    original := os.Getenv(appdir.MittoDirEnv)  // May capture another test's value!
    defer func() {
        os.Setenv(appdir.MittoDirEnv, original)
        appdir.ResetCache()
    }()
    // ...
}
```

### ✅ Correct Pattern

```go
// GOOD: t.Setenv automatically restores, t.Cleanup ensures cache reset
func TestSomething(t *testing.T) {
    tmpDir := t.TempDir()
    t.Setenv(appdir.MittoDirEnv, tmpDir)  // Auto-restores on test end
    appdir.ResetCache()                    // Reset AFTER setting env
    t.Cleanup(appdir.ResetCache)           // Ensure cache reset even on panic
    // Test code here...
}
```

### Key Rules

1. **Always use `t.Setenv()`** instead of `os.Setenv()` for environment variables
2. **Call `ResetCache()` AFTER `t.Setenv()`** - the order matters!
3. **Use `t.Cleanup()`** to ensure cache is reset even if test panics

## Testing HTTP Handlers

```go
func TestWriteJSON(t *testing.T) {
    w := httptest.NewRecorder()
    writeJSON(w, http.StatusOK, map[string]string{"key": "value"})
    if w.Code != http.StatusOK {
        t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
    }
}
```

## Testing Error Paths

Always test error conditions, not just happy paths:

```go
func TestNewConnection_EmptyCommand(t *testing.T) {
    ctx := context.Background()
    _, err := NewConnection(ctx, "", true, nil, nil)
    if err == nil {
        t.Error("NewConnection should fail with empty command")
    }
    if !strings.Contains(err.Error(), "empty command") {
        t.Errorf("Error should mention 'empty command', got: %v", err)
    }
}
```

## Test Environment Variables

| Variable | Description |
|----------|-------------|
| `MITTO_TEST_MODE` | Set to `1` to enable test mode |
| `MITTO_DIR` | Override data directory (use `t.Setenv()`) |
| `CI` | Set automatically in CI environments |

## Test Coverage Targets

| Package | Target | Notes |
|---------|--------|-------|
| `internal/config` | 80%+ | Configuration loading |
| `internal/fileutil` | 80%+ | File utilities |
| `internal/session` | 70%+ | Session management |
| `internal/acp` | 70%+ | ACP protocol |
| `internal/web` | 60%+ | Web handlers |
| `internal/cmd` | 30%+ | CLI (hard to unit test) |

Run coverage: `go test -cover ./internal/...`

