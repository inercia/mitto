---
type: "agent_requested"
description: "Trying changes in the UI with Playwright, adjusting the layout in the Web UI, running Playwright UI tests, test fixtures, 'mitto web' setup, and browser testing patters"
---

# Playwright UI Testing

## Running UI Tests

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
└── utils/                   # Test utilities
```

## Playwright Test Conventions

```typescript
import { test, expect } from '../fixtures/test-fixtures';

test.describe('Feature Name', () => {
  test.beforeEach(async ({ page, helpers }) => {
    await helpers.navigateAndWait(page);
  });

  test('should do something', async ({ page, selectors, timeouts }) => {
    const element = page.locator(selectors.chatInput);
    await expect(element).toBeVisible({ timeout: timeouts.appReady });
  });
});
```

**Centralized selectors** in `tests/ui/utils/selectors.ts`:

```typescript
export const selectors = {
  chatInput: 'textarea',
  sendButton: 'button:has-text("Send")',
  userMessage: '.bg-mitto-user, .bg-blue-600',
  agentMessage: '.bg-mitto-agent, .prose',
};

export const timeouts = {
  pageLoad: 10000,
  appReady: 10000,
  agentResponse: 60000,
};
```

## ⚠️ CRITICAL: Running Mitto Web for Testing

**NEVER run `mitto web` without a config file!** Without proper configuration, the process **blocks indefinitely** with no output.

### MANDATORY: Create a Test Config File First

```bash
# 1. Build the mock ACP server
make build-mock-acp

# 2. Create test directory and config
TEST_DIR=/tmp/mitto-test-$$
mkdir -p "$TEST_DIR/workspace"
cat > "$TEST_DIR/.mittorc" << 'EOF'
acp:
  - mock-acp:
      command: ./tests/mocks/acp-server/mock-acp-server

web:
  host: 127.0.0.1
  port: 8089
  theme: v2
  external_port: -1  # DISABLED
  # NO auth section - prevents keychain access on macOS
EOF

# 3. Start Mitto
mitto web --config $TEST_DIR/.mittorc --dir $TEST_DIR/workspace --debug &

# 4. Wait and run tests
sleep 2
npx playwright test --headed
```

### Why This Configuration

1. **`external_port: -1`** - Disables external access, no 0.0.0.0 binding
2. **No `web.auth` section** - Avoids keychain prompts on macOS
3. **`--config` flag** - Makes config read-only, disables Settings dialog
4. **Mock ACP server** - Deterministic testing without real AI

### Keychain Access Behavior

Mitto only accesses macOS Keychain when:
- `web.auth.simple` is configured
- External Access is enabled

**For testing without keychain:** Omit `web.auth` section and set `external_port: -1`.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `MITTO_TEST_URL` | Base URL for tests (default: `http://127.0.0.1:8089`) |

## Complete Testing Workflow

```bash
# 1. Build mock ACP
make build-mock-acp

# 2. Create config (no auth = no keychain)
mkdir -p /tmp/mitto-test/workspace
cat > /tmp/mitto-test/.mittorc << 'EOF'
acp:
  - mock-acp:
      command: ./tests/mocks/acp-server/mock-acp-server
web:
  host: 127.0.0.1
  port: 8089
  theme: v2
  external_port: -1
EOF

# 3. Start server
mitto web --config /tmp/mitto-test/.mittorc --dir /tmp/mitto-test/workspace --debug &

# 4. Wait for ready
sleep 2
curl -s http://127.0.0.1:8089/api/config > /dev/null && echo "Ready!"

# 5. Run tests
npx playwright test --headed
# or interactive mode:
npx playwright codegen http://127.0.0.1:8089
```

## Important Notes

- **Always use `--config`** - Without it, Settings dialog may appear or server may hang
- **Always use `--dir`** - Ensures a workspace is configured
- **Port 8089** - Recommended to avoid conflicts with dev server on 8080
- **Mock ACP** - For deterministic testing
