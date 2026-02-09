---
description: Go unit test conventions, table-driven tests, test isolation, and mocking patterns
globs:
  - "internal/**/*_test.go"
keywords:
  - unit test
  - table-driven
  - t.TempDir
  - t.Setenv
  - test coverage
  - httptest
  - mocking
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
| `internal/conversion` | 90%+ | Markdown conversion, link detection |
| `internal/config` | 80%+ | Configuration loading |
| `internal/fileutil` | 80%+ | File utilities |
| `internal/session` | 70%+ | Session management |
| `internal/acp` | 70%+ | ACP protocol |
| `internal/web` | 60%+ | Web handlers |
| `internal/cmd` | 30%+ | CLI (hard to unit test) |

Run coverage: `go test -cover ./internal/...`

## Testing Text Processing Features

For features that process text/HTML (like link detection):

### Three-Level Testing Strategy

1. **Unit Tests** - Test core logic with HTML input/output:
   ```go
   func TestFileLinker_URLsInBackticks(t *testing.T) {
       linker := NewFileLinker(FileLinkerConfig{...})

       tests := []struct {
           name     string
           input    string
           contains []string  // Expected substrings
           excludes []string  // Should NOT contain
       }{
           {
               name:  "URL in backticks",
               input: "<code>https://example.com</code>",
               contains: []string{
                   `<a href="https://example.com"`,
                   `<code>https://example.com</code></a>`,
               },
           },
       }

       for _, tt := range tests {
           t.Run(tt.name, func(t *testing.T) {
               result := linker.LinkFilePaths(tt.input)
               for _, expected := range tt.contains {
                   if !strings.Contains(result, expected) {
                       t.Errorf("Expected %q in output", expected)
                   }
               }
           })
       }
   }
   ```

2. **Integration Tests** - Test full pipeline (markdown → HTML):
   ```go
   func TestURLsInBackticks_Integration(t *testing.T) {
       converter := NewConverter(WithFileLinks(...))

       html, err := converter.Convert("Check `https://example.com`")
       if err != nil {
           t.Fatalf("Conversion failed: %v", err)
       }

       if !strings.Contains(html, `<a href="https://example.com"`) {
           t.Errorf("URL not linkified in output")
       }
   }
   ```

3. **Example Tests** - Demonstrate usage and serve as documentation:
   ```go
   func Example_urlDetection() {
       converter := NewConverter(WithFileLinks(...))
       html, _ := converter.Convert("Visit `https://example.com`")
       fmt.Println(strings.Contains(html, "<a href="))
       // Output: true
   }
   ```

### Edge Case Testing

Always test edge cases for text processing:

```go
tests := []struct {
    name     string
    input    string
    excludes string  // Should NOT be in output
}{
    {
        name:     "URL in code block should not be linked",
        input:    "<pre><code>https://example.com</code></pre>",
        excludes: `<a href=`,
    },
    {
        name:     "partial URL should not be linked",
        input:    "<code>example.com</code>",
        excludes: `<a href=`,
    },
    {
        name:     "URL with surrounding text should not be linked",
        input:    "<code>see https://example.com here</code>",
        excludes: `<a href=`,
    },
}
```

### Testing with Race Detector

Always run tests with race detector for concurrent code:

```bash
go test -race ./internal/conversion/...
go test -race -count=2 ./internal/conversion/...  # Run twice to catch flaky tests
```

