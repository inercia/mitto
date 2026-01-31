---
description: Testing conventions, mock ACP server, coverage targets, and test patterns
globs:
  - "**/*_test.go"
  - "tests/**/*"
  - "**/*.test.js"
  - "**/*.spec.ts"
---

# Testing Framework

## Test Directory Structure

```
tests/
├── fixtures/                    # Shared test fixtures
│   ├── config/                  # Test configuration files
│   ├── workspaces/              # Mock project directories
│   └── responses/               # Canned ACP responses for mock server
├── mocks/
│   ├── acp-server/              # Mock ACP server (Go)
│   │   ├── main.go              # Entry point
│   │   ├── types.go             # Protocol types
│   │   ├── handler.go           # Request handlers
│   │   └── sender.go            # Response utilities
│   └── testutil/                # Shared Go test utilities
├── integration/                 # Go integration tests
│   ├── cli/                     # CLI command tests
│   └── api/                     # HTTP/WebSocket API tests
├── ui/                          # Playwright UI tests (TypeScript)
│   ├── specs/                   # Test specifications
│   ├── fixtures/                # Playwright fixtures
│   └── utils/                   # Test utilities
└── scripts/                     # Test support scripts
```

## Running Tests

```bash
# Unit tests only (fast, no external dependencies)
make test              # All unit tests
make test-go           # Go unit tests only
make test-js           # JavaScript unit tests only

# Integration tests (uses mock ACP server)
make test-integration      # All integration tests
make test-integration-cli  # CLI tests only
make test-integration-api  # API tests only

# UI tests (Playwright)
make test-ui           # Headless
make test-ui-headed    # Visible browser
make test-ui-debug     # Debug mode with inspector

# All tests
make test-all          # Unit + integration + UI
make test-ci           # Full test suite for CI
```

## Mock ACP Server

The mock ACP server (`tests/mocks/acp-server/`) provides deterministic responses for testing:

```bash
# Build the mock server
make build-mock-acp

# Run manually (for debugging)
./tests/mocks/acp-server/mock-acp-server --verbose
```

## Unit Test Conventions

```go
// Use t.TempDir() for file-based tests
func TestSomething(t *testing.T) {
    tmpDir := t.TempDir()
    store, err := NewStore(tmpDir)
    if err != nil {
        t.Fatalf("NewStore failed: %v", err)
    }
    defer store.Close()
}

// Naming: TestType_Method or TestType_Scenario
func TestStore_CreateAndGet(t *testing.T) { ... }
func TestLock_ForceAcquireIdle(t *testing.T) { ... }

// Table-driven tests for multiple scenarios
func TestParse(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    Config
        wantErr bool
    }{
        {"valid config", "...", Config{...}, false},
        {"empty input", "", Config{}, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

## Finding Project Root in Tests

When tests need to access project resources (like the mock ACP server):

```go
func getMockACPPath(t *testing.T) string {
    t.Helper()

    // Find project root by looking for go.mod
    dir, err := os.Getwd()
    if err != nil {
        t.Fatalf("Failed to get working directory: %v", err)
    }

    for {
        if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
            break
        }
        parent := filepath.Dir(dir)
        if parent == dir {
            t.Skip("Could not find project root")
        }
        dir = parent
    }

    mockPath := filepath.Join(dir, "tests", "mocks", "acp-server", "mock-acp-server")
    if _, err := os.Stat(mockPath); os.IsNotExist(err) {
        t.Skip("mock-acp-server not found. Run 'make build-mock-acp' first.")
    }

    return mockPath
}
```

## Testing with Mock ACP Server

```go
func TestConnection_WithMockACP(t *testing.T) {
    mockPath := getMockACPPath(t)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    var output strings.Builder
    conn, err := NewConnection(ctx, mockPath, true, func(msg string) {
        output.WriteString(msg)
    }, nil)
    if err != nil {
        t.Fatalf("NewConnection failed: %v", err)
    }
    defer conn.Close()

    if err := conn.Initialize(ctx); err != nil {
        t.Fatalf("Initialize failed: %v", err)
    }

    // Verify output contains expected messages
    if !strings.Contains(output.String(), "Connected") {
        t.Errorf("Output should contain 'Connected', got: %s", output.String())
    }
}
```

## Testing HTTP Handlers

Use `httptest.NewRecorder()` to test HTTP handlers:

```go
func TestWriteJSON(t *testing.T) {
    w := httptest.NewRecorder()
    writeJSON(w, http.StatusOK, map[string]string{"key": "value"})

    if w.Code != http.StatusOK {
        t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
    }
    if w.Header().Get("Content-Type") != "application/json" {
        t.Error("Content-Type should be application/json")
    }
}

func TestParseJSONBody(t *testing.T) {
    body := `{"name": "test"}`
    r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
    w := httptest.NewRecorder()

    var data struct{ Name string }
    ok := parseJSONBody(w, r, &data)

    if !ok {
        t.Error("parseJSONBody should return true for valid JSON")
    }
    if data.Name != "test" {
        t.Errorf("Name = %q, want %q", data.Name, "test")
    }
}
```

## Testing Error Paths

Always test error conditions, not just happy paths:

```go
func TestNewConnection_EmptyCommand(t *testing.T) {
    ctx := context.Background()
    _, err := NewConnection(ctx, "", true, nil, nil)
    if err == nil {
        t.Error("NewConnection should fail with empty command")
    }
    if !strings.Contains(err.Error(), "empty command") {
        t.Errorf("Error should mention 'empty command', got: %v", err)
    }
}
```

## Test Environment Variables

| Variable | Description |
|----------|-------------|
| `MITTO_TEST_MODE` | Set to `1` to enable test mode |
| `MITTO_DIR` | Override the Mitto data directory |
| `MITTO_TEST_URL` | Base URL for UI tests (default: `http://127.0.0.1:8089`) |
| `CI` | Set automatically in CI environments |

## Test Coverage Targets

Current coverage by package (run `go test -cover ./internal/...`):

| Package | Coverage | Target | Notes |
|---------|----------|--------|-------|
| `internal/config` | 82.4% | 80%+ | ✅ Good |
| `internal/fileutil` | 84.6% | 80%+ | ✅ Good |
| `internal/auxiliary` | 71.9% | 70%+ | ✅ Good |
| `internal/logging` | 80%+ | 80%+ | ✅ Good (new tests added) |
| `internal/session` | 64.5% | 70%+ | ⚠️ Needs improvement |
| `internal/acp` | 61.4% | 70%+ | ⚠️ Needs improvement |
| `internal/appdir` | 56.7% | 60%+ | ⚠️ Close to target |
| `internal/web` | 35%+ | 50%+ | ⚠️ Improved with new tests |
| `internal/cmd` | 8.7% | 30%+ | ❌ CLI logic hard to unit test |

**Priority areas for test improvement:**
1. `internal/session` - Add concurrency tests and error path coverage
2. `internal/acp` - Add more connection lifecycle tests

## Web Package Test Files

The `internal/web` package has comprehensive test coverage:

| Test File | Coverage |
|-----------|----------|
| `http_helpers_test.go` | JSON response helpers, request parsing |
| `websocket_integration_test.go` | WebSocket message flow, reconnection |
| `ws_conn_test.go` | WebSocket connection wrapper |
| `title_test.go` | Session title generation |
| `background_session_test.go` | ACP session lifecycle |
| `external_listener_test.go` | External access listener |
| `websocket_security_test.go` | WebSocket security (rate limiting, etc.) |

## WebSocket Integration Testing

Test WebSocket message flows using `httptest.Server` and `gorilla/websocket`:

```go
func TestWebSocketMessageFlow(t *testing.T) {
    // Create test server with WebSocket endpoint
    mux := http.NewServeMux()
    upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

    mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
        conn, _ := upgrader.Upgrade(w, r, nil)
        defer conn.Close()
        // Handle messages...
    })

    server := httptest.NewServer(mux)
    defer server.Close()

    // Connect WebSocket client
    wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
    conn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
    defer conn.Close()

    // Send and receive messages
    conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"prompt"}`))
    _, msg, _ := conn.ReadMessage()
    // Assert on msg...
}
```

### Test Helper Functions

```go
// Connect to test WebSocket server
func connectTestWS(t *testing.T, server *httptest.Server, path string) *websocket.Conn

// Read WebSocket message with timeout
func readWSMessage(t *testing.T, conn *websocket.Conn, timeout time.Duration) WSMessage

// Send typed WebSocket message
func sendWSMessage(t *testing.T, conn *websocket.Conn, msgType string, data interface{})
```

## Integration Test Conventions

Integration tests use the `//go:build integration` build tag:

```go
//go:build integration

package cli

import (
    "os/exec"
    "testing"
)

func TestCLIHelp(t *testing.T) {
    binary := getMittoBinary(t)
    cmd := exec.Command(binary, "--help")
    output, err := cmd.CombinedOutput()
    // ...
}
```

## Playwright UI Test Conventions

UI tests use TypeScript with custom fixtures:

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

## Running Playwright with Mitto Web

When running Playwright tests interactively (not using the test harness), **always** create a dummy `.mittorc` configuration file and start Mitto in web mode with the `--config` flag. This configuration approach:

1. **Disables the Settings Dialog** - When `--config` is used, the config is read-only and the Settings dialog is never shown
2. **Provides stable test conditions** - Known ACP server and workspace configuration
3. **Allows testing all other UI elements** - Everything except Settings dialog can be tested

### Setup Steps

1. **Create a dummy `.mittorc` file** in a temporary directory:

```yaml
# /tmp/mitto-test/.mittorc
acp:
  - mock-acp:
      command: /path/to/mock-acp-server  # Use the mock ACP server

web:
  host: 127.0.0.1
  port: 8089  # Use a test port
  theme: v2
```

2. **Create a test workspace directory**:

```bash
mkdir -p /tmp/mitto-test/workspace
```

3. **Start Mitto web server** pointing to the RC file:

```bash
mitto web --config /tmp/mitto-test/.mittorc --dir /tmp/mitto-test/workspace
```

4. **Run Playwright** against `http://127.0.0.1:8089`

### Example Interactive Playwright Session

```bash
# 1. Build the mock ACP server
make build-mock-acp

# 2. Create test configuration
mkdir -p /tmp/mitto-test/workspace
cat > /tmp/mitto-test/.mittorc << 'EOF'
acp:
  - mock-acp:
      command: ./tests/mocks/acp-server/mock-acp-server

web:
  port: 8089
  theme: v2
EOF

# 3. Start Mitto (in background or separate terminal)
mitto web --config /tmp/mitto-test/.mittorc --dir /tmp/mitto-test/workspace &

# 4. Run Playwright tests or use Playwright's interactive mode
npx playwright test --headed
# or
npx playwright codegen http://127.0.0.1:8089
```

### Why This Works

The frontend checks `config.config_readonly` from `/api/config`:
- When `--config` flag is used → `config_readonly: true`
- When `config_readonly: true` → Settings dialog is never auto-opened
- All other UI features work normally (chat, sessions, prompts, themes, etc.)

### Important Notes

- **Always use `--config`** - Without it, the Settings dialog may appear
- **Always use `--dir`** - Ensures a workspace is configured
- **Use mock ACP server** - For deterministic testing without real AI
- **Port 8089** - Recommended test port to avoid conflicts with development server on 8080

## JavaScript Unit Tests (lib.js)

Frontend utility functions in `lib.js` are tested with Jest. Tests run in Node.js without a browser.

### Running JavaScript Tests

```bash
cd web/static && npm test
```

### Test File Structure

```javascript
// lib.test.js
import {
    hasMarkdownContent,
    renderUserMarkdown,
    MAX_MARKDOWN_LENGTH,
    // ... other exports
} from './lib.js';

describe('hasMarkdownContent', () => {
    test('returns false for plain text', () => {
        expect(hasMarkdownContent('Hello world')).toBe(false);
    });

    test('detects headers', () => {
        expect(hasMarkdownContent('# Header')).toBe(true);
    });
});
```

### Mocking Browser Globals

Functions that depend on browser globals (like `window.marked`) should gracefully handle their absence:

```javascript
// In lib.js - check for browser environment
export function renderUserMarkdown(text) {
    // Check if marked and DOMPurify are available
    if (typeof window === 'undefined' || !window.marked || !window.DOMPurify) {
        return null;  // Graceful fallback
    }
    // ... render logic
}

// In lib.test.js - test the fallback
test('returns null when window.marked is not available', () => {
    // In Node.js, window is undefined, so this tests the fallback
    expect(renderUserMarkdown('# Header')).toBeNull();
});
```

### Mocking localStorage

```javascript
// Mock localStorage for testing
const localStorageMock = (() => {
    let store = {};
    return {
        getItem: (key) => store[key] || null,
        setItem: (key, value) => { store[key] = value; },
        removeItem: (key) => { delete store[key]; },
        clear: () => { store = {}; }
    };
})();

Object.defineProperty(global, 'localStorage', { value: localStorageMock });

describe('savePendingPrompt', () => {
    beforeEach(() => {
        localStorageMock.clear();
    });

    test('saves and retrieves a pending prompt', () => {
        savePendingPrompt('session1', 'prompt1', 'Hello', []);
        const pending = getPendingPrompts();
        expect(pending['prompt1']).toBeDefined();
    });
});
```

### Testing Pure Functions

Keep functions in `lib.js` pure (no side effects, no DOM access) for easy testing:

```javascript
// Good: Pure function, easy to test
export function hasMarkdownContent(text) {
    if (!text || typeof text !== 'string') return false;
    return /^#{1,6}\s+\S/m.test(text);  // Check for headers
}

// Avoid: Function with side effects
export function renderAndInsert(text, element) {
    element.innerHTML = marked.parse(text);  // Hard to test
}
```

### Test Coverage for lib.js

Key areas to test:
- **Input validation**: null, undefined, empty string, wrong types
- **Edge cases**: very long strings, special characters
- **Regex patterns**: ensure patterns match expected inputs and reject non-matches
- **Error handling**: graceful fallbacks when dependencies unavailable

### Testing Pending Prompt Functions

The pending prompt system requires localStorage mocking:

```javascript
describe('Pending Prompts', () => {
    beforeEach(() => {
        localStorageMock.clear();
    });

    describe('generatePromptId', () => {
        test('generates unique IDs', () => {
            const id1 = generatePromptId();
            const id2 = generatePromptId();
            expect(id1).not.toBe(id2);
        });

        test('includes timestamp prefix', () => {
            const id = generatePromptId();
            expect(id).toMatch(/^prompt_\d+_/);
        });
    });

    describe('savePendingPrompt', () => {
        test('saves prompt with all fields', () => {
            savePendingPrompt('session1', 'prompt1', 'Hello', ['img1']);
            const pending = getPendingPrompts();
            expect(pending['prompt1']).toEqual({
                sessionId: 'session1',
                message: 'Hello',
                imageIds: ['img1'],
                timestamp: expect.any(Number)
            });
        });
    });

    describe('cleanupExpiredPrompts', () => {
        test('removes prompts older than 5 minutes', () => {
            // Create expired prompt
            const pending = { 'old': { timestamp: Date.now() - 6 * 60 * 1000 } };
            localStorage.setItem('mitto_pending_prompts', JSON.stringify(pending));
            cleanupExpiredPrompts();
            expect(getPendingPrompts()).toEqual({});
        });
    });
});
```
