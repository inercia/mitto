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

**Known mock-ACP limitation — no capture-group / echo-back templating** (mitto-3od.6): fixture responses in `tests/fixtures/responses/*.json` are static text; the matcher cannot echo runtime-generated values from the inbound prompt into the response. Any end-to-end path that requires the LLM to echo back a caller-generated token — notably the auxiliary-session processor completion protocol, where `internal/processors/apply.go::parseProcessorCompletion` matches on the exact runtime `dispatch_id` in `MITTO_PROCESSOR_COMPLETION {"dispatch_id":"<id>","save_count":N}` — cannot be exercised through the mock as-is. Until the mock gains capture-group substitution, prefer a unit test against `processors.Apply` with a stub `ProcessorRunner`, or a "prompt-contract" test that asserts the built-in YAML prompt literally instructs the LLM to echo the received `dispatch_id`.

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
- **Any per-frame ACP callback fires ONCE PER WebSocket frame of that type, not per API call.** Confirmed instances: `OnEventsLoaded` (mitto-nhq, commit 07053230) and `OnPromptComplete` (mitto-w0a, commit 0b61ae7a). Multiple frames per test are the norm — handshake auto-load + explicit `LoadEvents` + paginated `hasMore` each deliver their own `events_loaded` frame; two sequential prompts deliver two `prompt_complete` frames. `close(sig)` unconditionally in the callback double-closes and panics. Canonical safe pattern: guard with `sync.Once` — see `chatui_smoke_test.go` (`loadedOnce.Do(func(){ close(loaded) })`), `session_edge_cases_test.go` (`initialLoadOnce`/`syncOnce`/`agentMessageOnce`), and `sequence_contract_test.go:55/134/251/466` (4 `OnPromptComplete` sites). Reproducing double-delivery for `LoadEvents` requires SEQUENTIAL non-overlapping calls (~500ms apart) because `handleLoadEventsAsync` in `internal/web/session_ws.go` uses `loadEventsMu.TryLock()` to silently drop concurrent duplicates; for `OnPromptComplete`, two sequential prompts on one connection with an unguarded close callback panic deterministically. **Variant failure mode — silent wrong-frame race, not a panic**: `TestStreamingBaseline_AfterSeqEqualsMaxSeqReturnsNoEvents` (`tests/integration/inprocess/streaming_seq_baseline_test.go`) uses an unscoped buffered `gotReply` channel that can be satisfied by the handshake's auto-load frame instead of the explicit `LoadEvents(...)` call's frame under load, so the test flakes intermittently (passes reliably in isolation) rather than crashing; same root cause, same fix family (track a request ID/generation and only signal for that specific response).
- **JS hook modules that destructure `window.preact` at module scope are single-stub-only across the suite** (mitto-b1k, 2026-09-19). `web/static/hooks/*.js` typically does `const { useState, useEffect } = window.preact;` at module scope, so bindings are captured ONCE per Bun ES-module cache entry. If two `*.test.js` files each install their own `window.preact` stub and then `await import("hooks/useSessionsStore.js")`, Bun serves the FIRST file's cached module to the SECOND — silently. Symptom: each file passes solo, `bun test web/static` reports 30+ unrelated failures. Rule: cover component derivation logic by mirroring it against the pure (window-free) store module (e.g. `web/static/stores/sessionsStore.js`) rather than dynamic-importing the hook wrapper from a new test file. **Fix the hook, not the test**: the established remedy (confirmed again 2026-09-28 on `useToast.js`) is refactoring the hook to read `window.preact.*` LAZILY inside the function body (e.g. inside `useToasts()`), matching the convention already documented in `useBeadsIntegration.js` (mitto-txpp.6) — not patching every test file that trips over the module-scope destructure. See bd memory `js-hooks-module-cache-hazard-window-preact-destructure`.
- **ACP-connection tests must wrap the agent-side pipe with `JSONLineFilterReader`** (mitto-5jnw, commit 43862c37). Any test that instantiates `acp.NewClientSideConnection` directly on a raw `io.Pipe` bypasses the entire Mitto-side transport-layer fix surface (`sanitizeToolCallDiffOldText`, `filterLoadReplayNotification`, ANSI/box-drawing scrubber). All three production sites (`internal/acpproc/shared_acp_process.go`, `internal/acp/connection.go`, `internal/conversation/bgsession_acp_process.go`) wrap stdout with `mittoAcp.NewJSONLineFilterReader(pipe, logger)` before handing it to the SDK — mirror this exactly. Reference: `internal/acpproc/session_update_diff_oldtext_test.go`.
- **`.githooks/pre-commit` runs `make fmt-check` (`gofmt -l .`) over the WHOLE tree**, including gitignored sibling checkouts under `.mitto/worktrees/*` (owned by other concurrent Mitto sessions — never edit them). Consequence: after a Go upgrade whose new `gofmt` reformats previously-clean files (e.g. Go 1.27's trailing-comment alignment tweak of `internal/conversation/markdown_streaming_fixtures_test.go`), the hook will block commits until every worktree copy is also formatted — impossible for the current author. Verify your own staged files independently with `git diff --name-only --cached | grep '\.go$' | xargs gofmt -l` (empty output = safe); if clean, `--no-verify` is the correct escape and the main-tree drift should still land as its own `chore: gofmt <file> for Go <X.Y>` commit isolated from feature work. The repo also ships a custom `.go` git diff driver that renders "No syntactic changes" for cosmetic diffs — bypass with `diff -u <(git show <ref>:<path>) <path>` or `git diff --no-ext-diff` when you need raw bytes.
- **Reproducing `context deadline exceeded` requires the real waiter, not a killed subprocess** (mitto-bitr). Killing a slow fake `bd` script surfaces as `err.Error() == "signal: killed"`, NOT `"context deadline exceeded"`, so any assertion on the literal error string misses the bug's actual failure mode. For `bdexec` slot-contention timeouts, use the `occupyAllBdSlots(t)` helper (`internal/processors/apply_close_curate_maintenance_test.go`) — it acquires all `bdexec.MaxConcurrent` slots against distinct scratch dirs and never releases, so the test's own call blocks on `bdexec.Acquire`'s `case <-ctx.Done()` branch and returns the production error verbatim. Same principle applies to any timeout-family bug: reproduce by exhausting the resource the production code actually waits on, not by racing a subprocess to death.
- **`tests/integration/api/api_test.go` hardcodes `testServerPort = "8099"`** — on a shared dev machine an unrelated process can already own that port. This does NOT always fail fast as connection-refused: a squatter that answers `GET /` with `200` (observed: a corporate agent process, e.g. `cgw-manag`) fools `waitForServer`'s readiness probe, so the suite proceeds and real requests instead fail with `404 page not found` from the wrong server — looking like a code regression. Confirm with `lsof -i :8099` before assuming a code defect; if another process holds it, it's an environment collision (re-running against a temporarily patched free port is a valid way to confirm, then revert).
- **Doc-catalogue drift is only caught by dedicated sync tests, not by compilation**: `internal/prompts/docs_templatefuncs_sync_test.go`'s `TestRulesDocTemplateFuncsCatalogueMatchesFuncMap` fails if a new CEL/template func in `internal/cel/templatefuncs.go` isn't also listed in `.augment/rules/07-prompts.md`'s catalogue. Similarly, a new `WSMsgType*` in `internal/web` compiles fine but silently diverges from the JS SDK until `web/static/sdk/realtime/events.js` (`EVENTS` map + typedef) and both event sections of `docs/devel/websockets/protocol-spec.md` are updated by hand — only `events.test.js` catches the gap, and only for entries it enumerates.
- **Reproducing "committed caller / uncommitted definition" build bugs needs a disposable git worktree, not the shared working tree** (mitto-2g34): a normal `go build ./...` in the working tree can pass even though committed HEAD is broken, because it silently picks up someone's uncommitted WIP (a sibling driver's in-progress file, or your own not-yet-committed definition). Reproduce and guard against this with `git worktree add --detach /tmp/x HEAD && (cd /tmp/x && go build ./...); git worktree remove --force /tmp/x` — see `internal/conversation/head_build_repro_test.go` (`//go:build integration`) for a permanent regression test built on this pattern.
