# Mitto Web UI Tests

End-to-end tests for the Mitto Web UI using Playwright.

## Prerequisites

1. **Node.js** - Ensure Node.js is installed
2. **Playwright** - Install dependencies:
   ```bash
   npm install
   npx playwright install chromium
   ```

3. **Running Mitto Server** - Start the web server before running tests:
   ```bash
   # From the project root
   go run ./cmd/mitto web --port 8080
   ```

## Running Tests

```bash
# Run all UI tests
npm run test:ui

# Run tests in headed mode (see the browser)
npm run test:ui:headed

# Run tests in debug mode
npm run test:ui:debug

# View test report after running
npm run test:ui:report
```

## Configuration

Tests are configured in `playwright.config.js`.

### Environment Variables

- `MITTO_TEST_URL` - Base URL for the Mitto web server (default: `http://127.0.0.1:8080`)

Example:
```bash
MITTO_TEST_URL=http://localhost:3000 npm run test:ui
```

## Test Structure

- `page-load.spec.js` - Basic page load and initial state tests
- `sessions.spec.js` - Session management (create, list, rename, delete)
- `chat.spec.js` - Chat input and message display tests
- `connection.spec.js` - WebSocket connection and status tests
- `keyboard.spec.js` - Keyboard navigation and accessibility tests
- `markdown.spec.js` - Markdown rendering and message display tests
- `errors.spec.js` - Error handling and UI resilience tests
- `workspaces.spec.js` - Multi-workspace session functionality tests

## Writing New Tests

Tests use Playwright Test syntax:

```javascript
import { test, expect } from '@playwright/test';

test('should do something', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('.some-element')).toBeVisible();
});
```

See [Playwright documentation](https://playwright.dev/docs/intro) for more details.

