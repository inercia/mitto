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

**Mock ACP**: `make build-mock-acp` always before integration tests. Scenario matching via regex in `tests/fixtures/responses/*.json`.

**Setup**: `SetupTestServer(t)` in `internal/client/test_helpers.go` — isolates temp dir + resets appdir cache.

**Run**: `go test -tags integration ./tests/integration/inprocess`

## JavaScript Tests

Mock browser globals (`window.marked`, `window.DOMPurify`, `localStorage`) for Node.js testability.

## Smoke Tests

Docker-based verification in pristine Linux. Cross-compiled binaries in `tests/smoke/.build/`. Health check: `GET /mitto/api/health`.

## Timeout Testing Anti-Pattern: Shell Command Subprocess Escapes

**Problem**: `exec.CommandContext(ctx, "sh", "-c", "sleep 5")` does NOT kill the child process on context timeout.
- The shell (`sh`) is spawned in a new process group
- When context deadline expires, only the shell is killed
- The original child process (`sleep`) continues running in the background
- Result: `cmd.Run()` unblocks from shell exit, but the actual work process is orphaned

**Fix**: For commands that spawn subprocesses, use process group killing:
```go
// WRONG: Child process escapes the timeout
cmd := exec.CommandContext(ctx, "sh", "-c", command)
err := cmd.Run()  // Returns quickly but sleep 5 still running

// RIGHT: Kill entire process group on timeout
cmd := exec.CommandContext(ctx, "sh", "-c", command)
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
err := cmd.Run()  // Timeout kills shell + all children
```

**See**: `internal/hooks/hooks.go` StartUp() line 132 for correct pattern. RunDown() must be updated similarly.

## Lessons Learned

- Always run with `-race` flag for concurrent code
- Test edge cases and negative cases, not just happy paths
- Auth page assets must be in `publicStaticPaths` (symptom: unstyled login page with MIME error)
- CDN resources may be blocked by tracking prevention (Firefox, Safari)
- Timeout enforcement via context requires process group setup or subprocess escapes
