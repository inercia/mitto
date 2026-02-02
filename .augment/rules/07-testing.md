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
│   ├── cli/                     # CLI command tests (external process)
│   ├── api/                     # HTTP/WebSocket API tests (external process)
│   └── inprocess/               # In-process integration tests (fast, uses Go client)
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

## Test Isolation for Global State

When tests modify global state (environment variables, caches), use `t.Setenv()` and `t.Cleanup()` to ensure proper isolation. This is **critical** because Go runs tests in parallel across packages.

### ❌ Anti-Pattern: Manual Save/Restore

```go
// BAD: Race condition when tests run in parallel across packages
func TestSomething(t *testing.T) {
    original := os.Getenv(appdir.MittoDirEnv)  // May capture another test's value!
    defer func() {
        os.Setenv(appdir.MittoDirEnv, original)
        appdir.ResetCache()
    }()

    os.Setenv(appdir.MittoDirEnv, tmpDir)
    appdir.ResetCache()  // Race: cache may be read before this runs
    // ...
}
```

### ✅ Correct Pattern: Use t.Setenv() and t.Cleanup()

```go
// GOOD: t.Setenv automatically restores, t.Cleanup ensures cache reset
func TestSomething(t *testing.T) {
    tmpDir := t.TempDir()
    t.Setenv(appdir.MittoDirEnv, tmpDir)  // Auto-restores on test end
    appdir.ResetCache()                    // Reset AFTER setting env
    t.Cleanup(appdir.ResetCache)           // Ensure cache reset even on panic

    // Test code here...
}
```

### Key Rules

1. **Always use `t.Setenv()`** instead of `os.Setenv()` for environment variables in tests
2. **Call `ResetCache()` AFTER `t.Setenv()`** - the order matters!
3. **Use `t.Cleanup()`** to ensure cache is reset even if test panics
4. **Never call `ResetCache()` before setting the env var** - creates a race window

### Why This Matters

Go runs tests in parallel across packages by default. The old pattern had this race:

1. Test A saves `originalDir` and sets `MITTO_DIR=/tmp/testA`
2. Test B saves `originalDir` (which is now `/tmp/testA`!)
3. Test A's defer runs, restoring the original
4. Test B's defer runs, restoring `/tmp/testA` instead of the real original

With `t.Setenv()`, Go handles the save/restore atomically and correctly.

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
| `MITTO_DIR` | Override the Mitto data directory (use `t.Setenv()` in tests) |
| `MITTO_TEST_URL` | Base URL for UI tests (default: `http://127.0.0.1:8089`) |
| `CI` | Set automatically in CI environments |

**Important**: Always use `t.Setenv()` to set environment variables in tests. Never use `os.Setenv()` directly as it can cause race conditions when tests run in parallel. See "Test Isolation for Global State" section above.

## Test Coverage Targets

Current coverage by package (run `go test -cover ./internal/...`):

| Package | Coverage | Target | Notes |
|---------|----------|--------|-------|
| `internal/config` | 82.4% | 80%+ | ✅ Good |
| `internal/fileutil` | 84.6% | 80%+ | ✅ Good |
| `internal/auxiliary` | 71.9% | 70%+ | ✅ Good |
| `internal/logging` | 80%+ | 80%+ | ✅ Good |
| `internal/session` | 73.5% | 70%+ | ✅ Good |
| `internal/msghooks` | 70%+ | 70%+ | ✅ Good (new tests added) |
| `internal/appdir` | 60%+ | 60%+ | ✅ Good (new tests added) |
| `internal/acp` | 61.4% | 70%+ | ⚠️ Needs improvement |
| `internal/web` | 56.8% | 60%+ | ⚠️ Improved with unit + integration tests |
| `internal/client` | 50%+ | 60%+ | ⚠️ Covered by integration tests |
| `internal/cmd` | 8.7% | 30%+ | ❌ CLI logic hard to unit test |

**Priority areas for test improvement:**
1. `internal/acp` - Add more connection lifecycle tests
2. `internal/web` - Add more WebSocket handler tests

**Note:** Integration tests in `tests/integration/inprocess/` provide additional coverage for `internal/web` and `internal/client` that isn't reflected in unit test coverage numbers. Use `-coverpkg` flag to include them.

## Web Package Test Files

The `internal/web` package has comprehensive test coverage:

| Test File | Coverage |
|-----------|----------|
| `http_helpers_test.go` | JSON response helpers, request parsing |
| `websocket_integration_test.go` | WebSocket message flow, reconnection |
| `ws_conn_test.go` | WebSocket connection wrapper |
| `ws_messages_test.go` | Message buffer (Write, Peek, Flush, Append) |
| `title_test.go` | Session title generation |
| `background_session_test.go` | ACP session lifecycle |
| `external_listener_test.go` | External access listener |
| `websocket_security_test.go` | WebSocket security (rate limiting, etc.) |
| `config_handlers_test.go` | Config API, auth changes |
| `queue_api_test.go` | Queue CRUD, move operations |
| `image_api_test.go` | Image upload, from-path security |

### Testing EventBuffer

The `EventBuffer` stores all streaming events in order. Key test scenarios:

```go
func TestEventBuffer_InterleavedEvents(t *testing.T) {
    buf := NewEventBuffer()

    // Simulate interleaved streaming: message, tool, message, tool, message
    buf.AppendAgentMessage("Let me help... ")
    buf.AppendToolCall("tool-1", "Read file", "running")
    buf.AppendAgentMessage("I found... ")
    buf.AppendToolCall("tool-2", "Edit file", "running")
    buf.AppendAgentMessage("Done!")

    // Should have 5 separate events (not concatenated because interleaved)
    if buf.Len() != 5 {
        t.Errorf("Len = %d, want 5", buf.Len())
    }

    events := buf.Events()

    // Verify order is preserved
    if events[0].Type != BufferedEventAgentMessage {
        t.Errorf("events[0].Type = %v, want AgentMessage", events[0].Type)
    }
    if events[1].Type != BufferedEventToolCall {
        t.Errorf("events[1].Type = %v, want ToolCall", events[1].Type)
    }
    // ... verify remaining events
}

func TestEventBuffer_ConsecutiveMessagesConcatenated(t *testing.T) {
    buf := NewEventBuffer()

    buf.AppendAgentMessage("Hello, ")
    buf.AppendAgentMessage("World!")

    // Consecutive agent messages should be concatenated
    if buf.Len() != 1 {
        t.Errorf("Len = %d, want 1 (messages should be concatenated)", buf.Len())
    }

    result := buf.GetAgentMessage()
    if result != "Hello, World!" {
        t.Errorf("GetAgentMessage = %q, want %q", result, "Hello, World!")
    }
}
```

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

## In-Process Integration Tests

The `tests/integration/inprocess/` package provides **fast integration tests** that run the web server in-process using `httptest.Server`. These tests use the `internal/client` package to interact with the server.

### Advantages Over External Process Tests

| Aspect | In-Process | External Process |
|--------|------------|------------------|
| Speed | Fast (no process spawn) | Slower |
| Debugging | Easy (same process) | Harder |
| Coverage | Counted in coverage reports | Not counted |
| Isolation | Per-test temp directories | Shared state possible |

### Test Server Setup

```go
//go:build integration

package inprocess

import (
    "net/http/httptest"
    "testing"

    "github.com/inercia/mitto/internal/client"
    "github.com/inercia/mitto/internal/web"
)

// SetupTestServer creates an in-process test server with mock ACP.
func SetupTestServer(t *testing.T) *TestServer {
    t.Helper()

    tmpDir := t.TempDir()
    t.Setenv(appdir.MittoDirEnv, tmpDir)
    appdir.ResetCache()
    t.Cleanup(appdir.ResetCache)

    // Find mock ACP server binary
    mockACPCmd := findMockACPServer(t)

    // Create web server config
    webConfig := web.Config{
        ACPCommand:        mockACPCmd,
        ACPServer:         "mock-acp",
        DefaultWorkingDir: filepath.Join(tmpDir, "workspace"),
        AutoApprove:       true,
        FromCLI:           true,
    }

    srv, err := web.NewServer(webConfig)
    if err != nil {
        t.Fatalf("Failed to create web server: %v", err)
    }

    // Use httptest.Server with the server's Handler()
    httpServer := httptest.NewServer(srv.Handler())
    t.Cleanup(httpServer.Close)

    // Create Go client pointing to test server
    mittoClient := client.New(httpServer.URL)

    return &TestServer{
        Server:     srv,
        HTTPServer: httpServer,
        Client:     mittoClient,
    }
}
```

### Using the Go Client

The `internal/client` package provides a typed Go client for the Mitto REST API and WebSocket:

```go
func TestSessionLifecycle(t *testing.T) {
    ts := SetupTestServer(t)

    // Create session via REST API
    session, err := ts.Client.CreateSession(client.CreateSessionRequest{
        Name: "Test Session",
    })
    if err != nil {
        t.Fatalf("CreateSession failed: %v", err)
    }
    defer ts.Client.DeleteSession(session.SessionID)

    // Connect via WebSocket
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    ws, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
        OnConnected: func(sid, cid, acp string) {
            t.Logf("Connected: session=%s, client=%s", sid, cid)
        },
        OnAgentMessage: func(html string) {
            t.Logf("Agent: %s", html)
        },
    })
    if err != nil {
        t.Fatalf("Connect failed: %v", err)
    }
    defer ws.Close()

    // Send prompt and wait for response
    err = ws.SendPrompt("Hello, test!")
    if err != nil {
        t.Fatalf("SendPrompt failed: %v", err)
    }
}
```

### Key Test Scenarios

| Test | What It Validates |
|------|-------------------|
| `TestSessionLifecycle` | Create → List → Get → Delete |
| `TestQueueOperations` | Add → List → Clear queue |
| `TestWebSocketConnection` | Connect → Callbacks → Disconnect |
| `TestSendPromptAndReceiveResponse` | Full prompt/response flow |
| `TestWebSocketRename` | Session rename via WebSocket |

### Running In-Process Tests

```bash
# Run in-process integration tests only
go test -tags integration -v ./tests/integration/inprocess

# Run with coverage (uses -coverpkg to include all packages)
go test -tags integration -coverprofile=coverage.out \
    -coverpkg=./internal/web/...,./internal/client/...,./internal/session/... \
    ./tests/integration/inprocess
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

### Testing Message Merge Functions

The `mergeMessagesWithSync` function handles deduplication and appending (NOT sorting):

```javascript
describe('mergeMessagesWithSync', () => {
  test('preserves existing order and appends new messages', () => {
    // Existing messages are in display order - we should NOT re-sort them
    // New messages from sync are appended at the end
    const existing = [
      { role: ROLE_AGENT, html: 'Third', seq: 3, timestamp: 3000 },
    ];
    const newMessages = [
      { role: ROLE_USER, text: 'First', seq: 1, timestamp: 1000 },
      { role: ROLE_AGENT, html: 'Second', seq: 2, timestamp: 2000 },
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    expect(result).toHaveLength(3);
    // Existing message stays first, new messages are appended
    expect(result[0].seq).toBe(3); // existing stays in place
    expect(result[1].seq).toBe(1); // new messages appended
    expect(result[2].seq).toBe(2);
  });

  test('deduplicates by content hash', () => {
    const existing = [{ role: ROLE_USER, text: 'Hello', timestamp: 1000 }];
    const newMessages = [
      { role: ROLE_USER, text: 'Hello', seq: 1, timestamp: 500 }, // duplicate
      { role: ROLE_AGENT, html: 'Response', seq: 2, timestamp: 1500 },
    ];
    const result = mergeMessagesWithSync(existing, newMessages);
    expect(result).toHaveLength(2);
    expect(result.find((m) => m.role === ROLE_USER).text).toBe('Hello');
    expect(result.find((m) => m.role === ROLE_AGENT).html).toBe('Response');
  });
});
```

**IMPORTANT**: Do NOT sort by `seq` because tool calls are persisted immediately (early seq) while agent messages are buffered (late seq). Sorting would put all tool calls before agent messages.

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
