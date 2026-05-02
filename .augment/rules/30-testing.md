---
description: Go unit/integration tests, test isolation, mock ACP server, JavaScript tests, coverage targets
globs:
  - "internal/**/*_test.go"
  - "tests/integration/**/*"
  - "tests/mocks/**/*"
  - "internal/client/**/*"
  - "web/static/**/*.test.js"
  - "web/static/lib.js"
  - "web/static/package.json"
  - "web/static/utils/*.test.js"
keywords:
  - unit test
  - integration test
  - mock ACP
  - httptest
  - SetupTestServer
  - test isolation
  - t.Setenv
  - ResetCache
---

# Testing

## Running Tests

```bash
make test              # All unit tests
make test-go           # Go unit tests only
make test-js           # JavaScript unit tests (cd web/static && npm test)
make test-integration  # Integration tests
```

## Test Isolation for Global State

```go
// GOOD: t.Setenv automatically restores, t.Cleanup ensures cache reset
func TestSomething(t *testing.T) {
    tmpDir := t.TempDir()
    t.Setenv(appdir.MittoDirEnv, tmpDir)  // Auto-restores
    appdir.ResetCache()                    // Reset AFTER setting env
    t.Cleanup(appdir.ResetCache)           // Ensure reset even on panic
}
```

**Key rules:**
1. Always use `t.Setenv()` instead of `os.Setenv()`
2. Call `ResetCache()` AFTER `t.Setenv()` - order matters
3. Use `t.Cleanup()` for cleanup even on panic

## Coverage Targets

| Package               | Target |
| --------------------- | ------ |
| `internal/conversion` | 90%+   |
| `internal/config`     | 80%+   |
| `internal/session`    | 70%+   |
| `internal/acp`        | 70%+   |
| `internal/web`        | 60%+   |

## Integration Tests

### Mock ACP Server

```bash
make build-mock-acp  # Always rebuild after changes!
```

**Structure**: `main.go` (entry point, stdin/stdout loop), `types.go` (protocol types), `handler.go` (request handlers), `sender.go` (thread-safe sending).

**Scenario matching**: Prompt text matched against regex patterns in `tests/fixtures/responses/*.json`. If images detected, mock responds with acknowledgment before checking scenarios.

### In-Process Test Setup

```go
func SetupTestServer(t *testing.T) *TestServer {
    t.Helper()
    tmpDir := t.TempDir()
    t.Setenv(appdir.MittoDirEnv, tmpDir)
    appdir.ResetCache()
    t.Cleanup(appdir.ResetCache)

    srv, _ := web.NewServer(web.Config{
        ACPCommand: findMockACPServer(t),
        ACPServer: "mock-acp",
        DefaultWorkingDir: filepath.Join(tmpDir, "workspace"),
        AutoApprove: true,
    })
    httpServer := httptest.NewServer(srv.Handler())
    t.Cleanup(httpServer.Close)
    return &TestServer{Server: srv, HTTPServer: httpServer, Client: client.New(httpServer.URL)}
}
```

### Running Integration Tests

```bash
go test -tags integration -v ./tests/integration/inprocess
go test -tags integration -coverprofile=coverage.out \
    -coverpkg=./internal/web/...,./internal/client/... \
    ./tests/integration/inprocess
```

## JavaScript Tests

### Mocking Browser Globals

Functions depending on browser globals should gracefully handle their absence:

```javascript
export function renderUserMarkdown(text) {
    if (typeof window === "undefined" || !window.marked || !window.DOMPurify) {
        return null;  // Graceful fallback - testable in Node.js
    }
}
```

### Mocking localStorage

```javascript
const localStorageMock = (() => {
    let store = {};
    return {
        getItem: (key) => store[key] || null,
        setItem: (key, value) => { store[key] = value; },
        removeItem: (key) => { delete store[key]; },
        clear: () => { store = {}; },
    };
})();
Object.defineProperty(global, "localStorage", { value: localStorageMock });
```

## Text Processing Testing Strategy

Three levels for features that process text/HTML:

1. **Unit tests**: HTML input/output, edge cases, security
2. **Integration tests**: Full markdown-to-HTML pipeline with sanitization
3. **Example tests**: Real-world usage as documentation

### Contains/Excludes Pattern

```go
tests := []struct {
    name     string
    input    string
    contains []string
    excludes []string
}{
    {
        name:  "URL linkified",
        input: "<code>https://example.com</code>",
        contains: []string{`<a href="https://example.com"`},
        excludes: []string{},
    },
}
```

## Lessons Learned

- Always run with `-race` flag for concurrent code
- Test edge cases and negative cases, not just happy paths
- Auth page assets must be in `publicStaticPaths` (symptom: unstyled login page with MIME error)
- CDN resources may be blocked by tracking prevention (Firefox, Safari)
