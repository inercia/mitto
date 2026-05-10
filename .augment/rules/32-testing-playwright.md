---
description: Playwright UI tests, mock ACP scenarios, browser-specific testing, test fixtures, and selectors
globs:
  - "tests/ui/**/*"
  - "tests/fixtures/responses/**/*"
keywords:
  - playwright
  - e2e
  - ui test
  - browser test
  - mock acp
  - test scenario
---

# Playwright UI Testing

## MANDATORY: Test Configuration Requirements

**ALL Playwright tests MUST use a test configuration with these properties:**

| Property        | Required Value    | Reason                                                       |
| --------------- | ----------------- | ------------------------------------------------------------ |
| `external_port` | `-1`              | Disables external access, no 0.0.0.0 binding                |
| `host`          | `127.0.0.1`       | Localhost only                                               |
| `web.auth`      | **OMIT ENTIRELY** | Prevents keychain access on macOS                            |
| ACP server      | `mock-acp`        | Deterministic testing                                        |

```yaml
# CORRECT test configuration
web:
  host: 127.0.0.1
  port: 8089
  external_port: -1
  theme: v2
  # NO auth section!
```

## Running UI Tests

```bash
make test-ui           # Headless
make test-ui-headed    # Visible browser
make test-ui-debug     # Debug mode with inspector
```

## Test Conventions

```typescript
import { test, expect } from "../fixtures/test-fixtures";

test.describe("Feature Name", () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test("should do something", async ({ page, selectors, timeouts }) => {
    const element = page.locator(selectors.chatInput);
    await expect(element).toBeVisible({ timeout: timeouts.appReady });
  });
});
```

**Centralized selectors** in `tests/ui/utils/selectors.ts`.

## Manual Testing with Playwright MCP

**Use Playwright MCP tools** (`browser_navigate_playwright`, `browser_snapshot_playwright`, `browser_click_playwright`, `browser_type_playwright`, `browser_take_screenshot_playwright`).

```bash
make build-mock-acp
TEST_DIR=/tmp/mitto-test-$$; mkdir -p "$TEST_DIR/workspace"
# Write .mittorc with host:127.0.0.1, external_port:-1, theme:v2, mock-acp server
mitto web --config $TEST_DIR/.mittorc --dir $TEST_DIR/workspace --debug &
```

## Environment Variables

| Variable                | Description                                           |
| ----------------------- | ----------------------------------------------------- |
| `MITTO_TEST_URL`        | Base URL for tests (default: `http://127.0.0.1:8089`) |
| `MITTO_DIR`             | Data directory for test (default: `/tmp/mitto-test`)  |
| `MITTO_TEST_MODE`       | Set to `1` when running Playwright tests              |
| `MITTO_EXTERNAL_SERVER` | Set to `1` for Docker/CI runs — enables test skips    |

## Docker/CI Skip Annotations

Tests that fail in Docker/CI (timing-sensitive, host-path-dependent, multi-tab) must use:

```typescript
test.skip(!!process.env.MITTO_EXTERNAL_SERVER, 'Reason why this fails in Docker');
```

- **Local runs**: 0 skips — full suite runs
- **Docker/CI** (`MITTO_EXTERNAL_SERVER=1`): known-flaky tests skipped
- `make smoke-test` sets `MITTO_EXTERNAL_SERVER=1` automatically

## Mock ACP Server Scenarios

Scenarios in `tests/fixtures/responses/*.json`:

| Scenario File | Trigger Pattern | Description |
|---------------|-----------------|-------------|
| `simple-greeting.json` | Default | Basic agent response |
| `mermaid-diagram.json` | `(?i)show.*mermaid` | Mermaid flowchart |
| `markdown-code-block.json` | `(?i)code.*block` | Code with syntax highlighting |
| `error-response.json` | `(?i)error` | Error message |
| `tool-calls-interleaved.json` | `(?i)tool` | Tool usage simulation |

New scenarios: add JSON files to `tests/fixtures/responses/` with `trigger.pattern` (regex) and `actions` array.

## Test Fixture Patterns

### Auto-Cleanup (session accumulation prevention)

`test-fixtures.ts` includes an `autoCleanup` fixture with `{ auto: true }` — runs before **every** test automatically when session count exceeds `MAX_SESSIONS_THRESHOLD` (10). No manual call needed. Don't remove the explicit `cleanupSessions` fixture — tests that need manual control can still use it.

### `createFreshSession` Race Condition

In Docker, ACP connections take ~1.1s. After clicking new-session, **wait for `mitto_last_session_id` localStorage to update to a new non-empty value** before checking WebSocket readiness — otherwise `waitForWebSocketReady` may attach to the old session's textarea.

```typescript
// After clicking new session button, poll localStorage until ID changes:
await page.waitForFunction(() => !!localStorage.getItem("mitto_last_session_id"));
```

## Browser-Specific Issues

| Issue | Browser | Cause |
|-------|---------|-------|
| CDN resources blocked | Firefox, Safari | Tracking Prevention blocks `cdn.jsdelivr.net` |
| Mermaid not rendering | Firefox | Tracking Prevention blocks Mermaid.js CDN |

**Workaround**: Use Chromium for reliable testing, or disable tracking protection.
