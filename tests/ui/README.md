# Mitto Web UI Tests

End-to-end tests for the Mitto Web UI using Playwright with TypeScript.

## Prerequisites

1. **Node.js** - Ensure Node.js 18+ is installed
2. **Dependencies** - Install from project root:
   ```bash
   npm install
   ```
3. **Playwright Browsers** - Install Chromium:
   ```bash
   npx playwright install chromium
   ```

## Running Tests

```bash
# Run all UI tests (headless)
npm run test:ui

# Run tests with visible browser
npm run test:ui:headed

# Run tests in debug mode (step through)
npm run test:ui:debug

# View test report after running
npm run test:ui:report
```

Or using Make:

```bash
make test-ui
make test-ui-headed
make test-ui-debug
```

## Test Structure

```
tests/ui/
├── specs/                  # Test specifications (TypeScript)
│   ├── page-load.spec.ts   # Page load and initial state
│   ├── chat.spec.ts        # Chat input and messages
│   ├── connection.spec.ts  # WebSocket connection
│   ├── sessions.spec.ts    # Session management
│   ├── keyboard.spec.ts    # Keyboard navigation
│   ├── markdown.spec.ts    # Markdown rendering
│   ├── errors.spec.ts      # Error handling
│   ├── workspaces.spec.ts  # Multi-workspace
│   └── auth.spec.ts        # Authentication
├── fixtures/               # Playwright fixtures
│   └── test-fixtures.ts    # Custom test fixtures
├── utils/                  # Utilities
│   ├── selectors.ts        # Centralized selectors
│   └── helpers.ts          # Helper functions
├── playwright.config.ts    # Playwright configuration
├── global-setup.ts         # Pre-test setup
├── global-teardown.ts      # Post-test cleanup
└── tsconfig.json           # TypeScript config
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MITTO_TEST_URL` | `http://127.0.0.1:8089` | Base URL for tests |
| `MITTO_DIR` | `/tmp/mitto-test` | Test data directory |
| `MITTO_TEST_AUTH` | - | Set to `1` to enable auth tests |
| `CI` | - | Set in CI environments |

## Writing Tests

### Basic Test

```typescript
import { test, expect } from '../fixtures/test-fixtures';

test('should do something', async ({ page, selectors, helpers }) => {
  await helpers.navigateAndWait(page);

  const element = page.locator(selectors.chatInput);
  await expect(element).toBeVisible();
});
```

### Using Fixtures

The custom fixtures provide:

- `selectors` - Centralized CSS selectors
- `timeouts` - Standard timeout values
- `helpers` - Common operations (navigate, send message, etc.)

See [Playwright documentation](https://playwright.dev/docs/intro) for more details.

