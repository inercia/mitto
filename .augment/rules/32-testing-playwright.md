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

**Use Playwright MCP tools instead of playwright-cli for manual browser testing.**

### Setup

```bash
make build-mock-acp

TEST_DIR=/tmp/mitto-test-$$
mkdir -p "$TEST_DIR/workspace"

cat > "$TEST_DIR/.mittorc" << 'EOF'
acp:
  - mock-acp:
      command: ./tests/mocks/acp-server/mock-acp-server
web:
  host: 127.0.0.1
  port: 8089
  external_port: -1
  theme: v2
EOF

mitto web --config $TEST_DIR/.mittorc --dir $TEST_DIR/workspace --debug &
```

### Playwright MCP Tools

| Tool | Description |
|------|-------------|
| `browser_navigate_playwright` | Navigate to a URL |
| `browser_snapshot_playwright` | Capture accessibility snapshot |
| `browser_click_playwright` | Click on an element by ref |
| `browser_type_playwright` | Type text into an element |
| `browser_press_key_playwright` | Press a key |
| `browser_take_screenshot_playwright` | Take a screenshot |
| `browser_console_messages_playwright` | Get console messages |
| `browser_close_playwright` | Close the browser |

## Environment Variables

| Variable          | Description                                           |
| ----------------- | ----------------------------------------------------- |
| `MITTO_TEST_URL`  | Base URL for tests (default: `http://127.0.0.1:8089`) |
| `MITTO_DIR`       | Data directory for test (default: `/tmp/mitto-test`)  |
| `MITTO_TEST_MODE` | Set to `1` when running Playwright tests              |

## Mock ACP Server Scenarios

Scenarios in `tests/fixtures/responses/*.json`:

| Scenario File | Trigger Pattern | Description |
|---------------|-----------------|-------------|
| `simple-greeting.json` | Default | Basic agent response |
| `mermaid-diagram.json` | `(?i)show.*mermaid` | Mermaid flowchart |
| `markdown-code-block.json` | `(?i)code.*block` | Code with syntax highlighting |
| `error-response.json` | `(?i)error` | Error message |
| `tool-calls-interleaved.json` | `(?i)tool` | Tool usage simulation |

### Adding New Scenarios

```json
{
  "scenario": "my-scenario",
  "description": "Description of what this tests",
  "responses": [
    {
      "trigger": { "type": "prompt", "pattern": "(?i)my.*pattern" },
      "actions": [
        { "type": "agent_message", "delay_ms": 50, "chunks": ["Response ", "content"] }
      ]
    }
  ]
}
```

## Browser-Specific Issues

| Issue | Browser | Cause |
|-------|---------|-------|
| CDN resources blocked | Firefox, Safari | Tracking Prevention blocks `cdn.jsdelivr.net` |
| Mermaid not rendering | Firefox | Tracking Prevention blocks Mermaid.js CDN |

**Workaround**: Use Chromium for reliable testing, or disable tracking protection.
