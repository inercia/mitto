---
type: "agent_requested"
description: "Playwright UI tests, test fixtures, selectors, 'mitto web' setup, and browser testing patternsrying or testing "
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
