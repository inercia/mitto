---
description: Go unit/integration tests, test isolation, mock ACP server, JavaScript tests, coverage targets
globs:
  - "internal/**/*_test.go"
  - "tests/integration/**/*"
  - "tests/mocks/**/*"
  - "tests/smoke/**/*"
  - "internal/client/**/*"
  - "web/static/**/*.test.js"
  - "web/static/lib.js"
  - "web/static/package.json"
  - "web/static/utils/*.test.js"
keywords:
  - unit test
  - integration test
  - smoke test
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
make smoke-build       # Cross-compile binaries + build Docker image
make smoke-test-cli    # CLI-only smoke tests inside Docker (fast, no browser)
make smoke-test        # Full smoke tests (CLI + Playwright via Docker)
make smoke-clean       # Clean up Docker image and .build/ artifacts
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

- Browser globals (`window.marked`, `window.DOMPurify`): check `typeof window === "undefined"` and return `null` as graceful fallback — makes code testable in Node.js
- `localStorage`: mock with a plain object implementing `getItem/setItem/removeItem/clear`, assigned via `Object.defineProperty(global, "localStorage", { value: mock })`

## Text Processing Testing Strategy

Use `contains`/`excludes` fields in table-driven tests for HTML output assertions (see `internal/conversion/*_test.go` for examples).

## Smoke Tests (Docker / Linux)

Smoke tests verify Mitto works in a pristine Linux environment using Docker + cross-compiled binaries.

**Location**: `tests/smoke/` — Dockerfile, entrypoint.sh, smoke-test.sh, docker-compose.yml, run.sh

**Key architecture**:
- Mitto binds to `0.0.0.0:8089` via `mitto web --host 0.0.0.0 --port 8089`
- Docker maps host `8089 → container 8089` directly (no socat needed)
- Cross-compiled binaries are staged in `tests/smoke/.build/` (gitignored)
- `MITTO_DIR` env var controls Mitto's data directory inside the container

**entrypoint.sh** writes `settings.json` + `workspaces.json`, then `exec mitto web --host 0.0.0.0`

**Health check**: `GET /mitto/api/health` → `{"status":"healthy",...}`

**Full Playwright smoke run** uses `MITTO_EXTERNAL_SERVER=1` and `MITTO_TEST_URL=http://localhost:8089`

## Lessons Learned

- Always run with `-race` flag for concurrent code
- Test edge cases and negative cases, not just happy paths
- Auth page assets must be in `publicStaticPaths` (symptom: unstyled login page with MIME error)
- CDN resources may be blocked by tracking prevention (Firefox, Safari)
