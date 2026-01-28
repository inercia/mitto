# Mitto Integration Tests

Go-based integration tests for Mitto CLI and API.

## Overview

These tests verify that Mitto works correctly as a complete system, testing:

- CLI commands and flags
- HTTP API endpoints
- WebSocket connections
- Session management
- Workspace handling

## Prerequisites

1. **Build Mitto**:
   ```bash
   make build
   ```

2. **Build Mock ACP Server**:
   ```bash
   make build-mock-acp
   ```

## Running Tests

```bash
# Run all integration tests
make test-integration

# Run CLI tests only
make test-integration-cli

# Run API tests only
make test-integration-api

# Run with verbose output
go test -v -tags=integration ./tests/integration/...
```

## Test Structure

```
tests/integration/
├── integration_test.go     # Shared test setup and helpers
├── cli/
│   └── cli_test.go         # CLI command tests
├── api/
│   └── api_test.go         # HTTP API tests
└── README.md
```

## Writing Tests

### Build Tag

All integration tests must include the build tag:

```go
//go:build integration

package cli
```

### Helper Functions

Use the helper functions from the parent package or `testutil`:

```go
func TestSomething(t *testing.T) {
    binary := getMittoBinary(t)
    mockACP := getMockACPBinary(t)
    testDir := createTestDir(t)
    
    // ... test code
}
```

### Example Test

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
    if err != nil {
        t.Fatalf("Command failed: %v", err)
    }
    
    if !strings.Contains(string(output), "mitto") {
        t.Error("Help output missing expected content")
    }
}
```

## API Tests

API tests start a Mitto web server and make HTTP requests:

```go
func TestSessionsAPI(t *testing.T) {
    resp, err := http.Get(testServerURL + "/api/sessions")
    if err != nil {
        t.Fatalf("Request failed: %v", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        t.Errorf("Expected 200, got %d", resp.StatusCode)
    }
}
```

## Environment

Tests use these environment variables:

| Variable | Description |
|----------|-------------|
| `MITTO_TEST_MODE` | Set to `1` (automatic) |
| `MITTO_DIR` | Temporary test directory |

## Legacy Tests

The bash-based integration tests in `run_all.sh` and `test_*.sh` are still available:

```bash
make test-integration-legacy
```

These require a real ACP server (like auggie) to be installed.

