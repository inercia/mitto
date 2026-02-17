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

## ⚠️ MANDATORY: Test Configuration Requirements

**ALL Playwright tests MUST use a test configuration with these properties:**

| Property        | Required Value    | Reason                                                       |
| --------------- | ----------------- | ------------------------------------------------------------ |
| `external_port` | `-1`              | **MANDATORY** - Disables external access, no 0.0.0.0 binding |
| `host`          | `127.0.0.1`       | **MANDATORY** - Localhost only                               |
| `web.auth`      | **OMIT ENTIRELY** | **MANDATORY** - Prevents keychain access on macOS            |
| ACP server      | `mock-acp`        | **MANDATORY** - Deterministic testing                        |

### ❌ NEVER DO THIS

```yaml
# WRONG: Missing external_port, has auth
web:
  host: 127.0.0.1
  port: 8089
  auth:
    simple: "password" # ❌ Will trigger keychain access!
```

```bash
# WRONG: Using real ACP or no config
mitto web                                    # ❌ No config!
mitto web --port 8089                        # ❌ No config!
mitto web --config /path/to/normal/config    # ❌ May have auth!
```

### ✅ ALWAYS DO THIS

```yaml
# CORRECT: Proper test configuration
web:
  host: 127.0.0.1
  port: 8089
  external_port: -1 # ✅ Disable external access
  theme: v2
  # NO auth section!   # ✅ No keychain access
```

```bash
# CORRECT: Use test config with mock ACP
mitto web --config $TEST_DIR/.mittorc --dir $TEST_DIR/workspace
```

---

## Running UI Tests

The automated test setup (`make test-ui`) handles configuration automatically via `tests/ui/start-test-server.sh`.

```bash
make test-ui           # Headless
make test-ui-headed    # Visible browser
make test-ui-debug     # Debug mode with inspector
```

## Test Structure

```
tests/ui/
├── specs/                   # Test specifications
├── fixtures/                # Playwright fixtures
├── utils/                   # Test utilities
├── start-test-server.sh     # Creates test config and starts mitto
├── global-setup.ts          # Playwright global setup
└── global-teardown.ts       # Playwright global teardown
```

## Playwright Test Conventions

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

**Centralized selectors** in `tests/ui/utils/selectors.ts`:

```typescript
export const selectors = {
  chatInput: "textarea",
  sendButton: 'button:has-text("Send")',
  userMessage: ".bg-mitto-user, .bg-blue-600",
  agentMessage: ".bg-mitto-agent, .prose",
};

export const timeouts = {
  pageLoad: 10000,
  appReady: 10000,
  agentResponse: 60000,
};
```

---

## Manual Testing with Playwright MCP

**⚠️ IMPORTANT: When doing manual browser testing, you MUST use the Playwright MCP tools instead of playwright-cli.**

The Playwright MCP provides these tools for browser automation:

| Tool | Description |
|------|-------------|
| `browser_navigate_playwright` | Navigate to a URL |
| `browser_snapshot_playwright` | Capture accessibility snapshot (preferred for interaction) |
| `browser_take_screenshot_playwright` | Take a screenshot |
| `browser_click_playwright` | Click on an element by ref |
| `browser_type_playwright` | Type text into an element |
| `browser_fill_form_playwright` | Fill form fields |
| `browser_press_key_playwright` | Press a key |
| `browser_hover_playwright` | Hover over an element |
| `browser_wait_for_playwright` | Wait for text or time |
| `browser_console_messages_playwright` | Get console messages (for error checking) |
| `browser_close_playwright` | Close the browser |

### Manual Testing Workflow

When running tests manually (not via `make test-ui`), you MUST create a proper test config:

### Step 1: Build Dependencies

```bash
make build-mock-acp
```

### Step 2: Create Test Config (MANDATORY FORMAT)

```bash
TEST_DIR=/tmp/mitto-test-$$
mkdir -p "$TEST_DIR/workspace"

# CRITICAL: This config MUST have:
#   - external_port: -1 (disable external access)
#   - NO web.auth section (prevents keychain access)
#   - host: 127.0.0.1 (localhost only)
cat > "$TEST_DIR/.mittorc" << 'EOF'
acp:
  - mock-acp:
      command: ./tests/mocks/acp-server/mock-acp-server

web:
  host: 127.0.0.1
  port: 8089
  external_port: -1
  theme: v2
  # NO auth section - this is intentional!
EOF
```

### Step 3: Start Server

```bash
mitto web --config $TEST_DIR/.mittorc --dir $TEST_DIR/workspace --debug &
sleep 2
curl -s http://127.0.0.1:8089/api/config > /dev/null && echo "Ready!"
```

### Step 4: Use Playwright MCP Tools

Navigate to the app:
```
Tool: browser_navigate_playwright
Parameters: { "url": "http://127.0.0.1:8089" }
```

Take a snapshot to see element refs:
```
Tool: browser_snapshot_playwright
Parameters: {}
```

Interact with elements using refs from the snapshot:
```
Tool: browser_click_playwright
Parameters: { "ref": "e3", "element": "description" }

Tool: browser_type_playwright
Parameters: { "ref": "e5", "text": "test message" }

Tool: browser_press_key_playwright
Parameters: { "key": "Enter" }
```

### Step 5: Cleanup

Close the browser and stop the server:
```
Tool: browser_close_playwright
Parameters: {}
```

```bash
pkill -f "mitto web.*8089" || true
```

---

## Automated Testing with `make test-ui`

For automated Playwright tests (npm-based), use the existing test infrastructure:

```bash
npx playwright test --headed
# or interactive mode:
npx playwright codegen http://127.0.0.1:8089
```

---

## Why These Requirements?

### `external_port: -1`

- Disables binding to `0.0.0.0`
- Tests run in complete isolation
- No network exposure during tests

### No `web.auth` Section

- Mitto accesses macOS Keychain when auth is configured
- This causes prompts during tests
- Omitting auth = no keychain access

### Mock ACP Server

- Deterministic, repeatable test responses
- No dependency on external AI services
- Fast execution

### `--config` Flag

- Makes configuration read-only
- Disables Settings dialog in UI
- Prevents accidental config changes during tests

---

## Environment Variables

| Variable          | Description                                           |
| ----------------- | ----------------------------------------------------- |
| `MITTO_TEST_URL`  | Base URL for tests (default: `http://127.0.0.1:8089`) |
| `MITTO_DIR`       | Data directory for test (default: `/tmp/mitto-test`)  |
| `MITTO_TEST_MODE` | Set to `1` when running Playwright tests              |

---

## Important Notes

- **Always use `--config`** - Without it, Settings dialog may appear or server may hang
- **Always use `--dir`** - Ensures a workspace is configured
- **Port 8089** - Recommended to avoid conflicts with dev server on 8080
- **Mock ACP** - For deterministic testing
- **Never use auth in test configs** - Will trigger keychain prompts

---

## Mock ACP Server Scenarios

The mock ACP server (`tests/mocks/acp-server/`) responds to specific message patterns with predefined responses. Scenarios are defined in `tests/fixtures/responses/*.json`:

| Scenario File | Trigger Pattern | Description |
|---------------|-----------------|-------------|
| `simple-greeting.json` | Default | Basic agent response |
| `mermaid-diagram.json` | `(?i)show.*mermaid` | Mermaid flowchart |
| `markdown-code-block.json` | `(?i)code.*block` | Code with syntax highlighting |
| `error-response.json` | `(?i)error` | Error message |
| `tool-calls-interleaved.json` | `(?i)tool` | Tool usage simulation |

### Using Test Scenarios

To trigger a specific scenario, send a message matching the pattern:

```javascript
// Trigger mermaid diagram
await textarea.fill("Show mermaid test");
await sendButton.click();

// Wait for rendered diagram
const diagram = page.locator(".mermaid-diagram");
await expect(diagram.first()).toBeVisible({ timeout: 10000 });
```

### Adding New Scenarios

Create a new JSON file in `tests/fixtures/responses/`:

```json
{
  "scenario": "my-scenario",
  "description": "Description of what this tests",
  "responses": [
    {
      "trigger": {
        "type": "prompt",
        "pattern": "(?i)my.*pattern"
      },
      "actions": [
        {
          "type": "agent_message",
          "delay_ms": 50,
          "chunks": ["Response ", "content ", "here"]
        }
      ]
    }
  ]
}
```

---

## Browser-Specific Testing

The Playwright config (`tests/ui/playwright.config.ts`) only runs Chromium by default. To test other browsers:

```typescript
// Uncomment in playwright.config.ts
projects: [
  { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  { name: 'firefox', use: { ...devices['Desktop Firefox'] } },
  { name: 'webkit', use: { ...devices['Desktop Safari'] } },
],
```

```bash
# Install additional browsers
npx playwright install firefox webkit

# Run tests on specific browser
npx playwright test --project=firefox
```

### Known Browser-Specific Issues

| Issue | Browser | Cause |
|-------|---------|-------|
| CDN resources blocked | Firefox, Safari | Tracking Prevention blocks `cdn.jsdelivr.net` |
| Mermaid not rendering | Firefox | Tracking Prevention blocks Mermaid.js CDN |

**Workaround for testing**: Disable tracking protection in browser settings, or use Chromium for reliable testing
