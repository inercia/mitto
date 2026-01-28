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
| `internal/session` | 64.5% | 70%+ | ⚠️ Needs improvement |
| `internal/acp` | 61.4% | 70%+ | ⚠️ Needs improvement |
| `internal/appdir` | 56.7% | 60%+ | ⚠️ Close to target |
| `internal/web` | 28.2% | 50%+ | ❌ Needs significant work |
| `internal/cmd` | 8.7% | 30%+ | ❌ CLI logic hard to unit test |

**Priority areas for test improvement:**
1. `internal/web` - Add tests for HTTP handlers and WebSocket message routing
2. `internal/session` - Add concurrency tests and error path coverage
3. `internal/acp` - Add more connection lifecycle tests

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

