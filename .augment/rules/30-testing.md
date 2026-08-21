---
description: Go unit/integration tests, test isolation, mock ACP server, JavaScript tests, coverage targets
globs:
  - "internal/**/*_test.go"
  - "tests/integration/**/*"
  - "tests/mocks/**/*"
  - "tests/smoke/**/*"
  - "pkg/api/**/*"
  - "web/static/**/*.test.js"
  - "web/static/lib.js"
  - "package.json"
  - "bunfig.toml"
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
make test-js           # JavaScript unit tests (bun test web/static)
make test-integration  # Integration tests
make smoke-build       # Cross-compile binaries + build Docker image
make smoke-test-cli    # CLI-only smoke tests inside Docker (fast, no browser)
make smoke-test        # Full smoke tests (CLI + Playwright via Docker)
make smoke-clean       # Clean up Docker image and .build/ artifacts
```

## JavaScript Test Runner: Bun

`bun test web/static` is the authoritative JS unit-test runner (Phase 3 of
mitto-txpp). The `web/static` scope matches Jest's old `roots` configuration
and keeps Bun's recursive discovery away from `tests/ui/specs/*.spec.ts`
(Playwright specs, incompatible test runner). `bunfig.toml` preloads
`scripts/bun-happy-dom.js` which registers happy-dom as the global DOM for
tests that touch `window` / `document` / `DOMParser`. Jest and
`jest-environment-jsdom` are no longer devDependencies.

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

**Setup**: `SetupTestServer(t)` in `tests/integration/inprocess/setup_test.go` — isolates temp dir + resets appdir cache.

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
- Verify a previous turn's edits actually persisted (`git status`/`git diff`) before continuing — apparent changes can be lost across session gaps/restarts
- When new test failures appear after a frontend/htm change, `git stash` and re-run the same tests against the base branch first — this distinguishes real regressions from pre-existing flakiness (e.g. test-isolation/state-leak failures) before spending time debugging your own code
- **Per-session sidecar test helpers must create the session first** (mitto-32ef/mitto-e8ij): tests that obtain a `Queue` (or any per-session sidecar) via `store.NewStore(...).Queue(id)` MUST first call `store.Create(session.Metadata{SessionID: id, ACPServer: ..., WorkingDir: ...})`. Since mitto-32ef, `Queue.writeQueue` uses `fileutil.WriteJSONAtomicIfDirExists` and no longer `MkdirAll`s the session dir, so `Queue.Add` fails with `ErrParentDirMissing` if the session was never created. Convention already followed ~30× in `internal/conversation/background_session_test.go`.
- **`.githooks/pre-commit` runs `make fmt-check` (`gofmt -l .`) over the WHOLE tree**, including gitignored sibling checkouts under `.mitto/worktrees/*` (owned by other concurrent Mitto sessions — never edit them). Consequence: after a Go upgrade whose new `gofmt` reformats previously-clean files (e.g. Go 1.27's trailing-comment alignment tweak of `internal/conversation/markdown_streaming_fixtures_test.go`), the hook will block commits until every worktree copy is also formatted — impossible for the current author. Verify your own staged files independently with `git diff --name-only --cached | grep '\.go$' | xargs gofmt -l` (empty output = safe); if clean, `--no-verify` is the correct escape and the main-tree drift should still land as its own `chore: gofmt <file> for Go <X.Y>` commit isolated from feature work. The repo also ships a custom `.go` git diff driver that renders "No syntactic changes" for cosmetic diffs — bypass with `diff -u <(git show <ref>:<path>) <path>` or `git diff --no-ext-diff` when you need raw bytes.
