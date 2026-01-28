# Mitto Test Suite

This directory contains all tests for Mitto, organized by test type.

## Directory Structure

```
tests/
├── fixtures/           # Shared test fixtures
│   ├── config/         # Test configuration files
│   ├── workspaces/     # Mock project directories
│   └── responses/      # Canned ACP responses for mock server
├── mocks/              # Mock implementations
│   ├── acp-server/     # Mock ACP server for testing
│   └── testutil/       # Shared Go test utilities
├── integration/        # Integration tests
│   ├── cli/            # CLI command tests (Go)
│   └── api/            # HTTP/WebSocket API tests (Go)
├── ui/                 # Playwright UI tests
│   ├── specs/          # Test specifications (TypeScript)
│   ├── fixtures/       # Playwright fixtures
│   └── utils/          # Test utilities
└── scripts/            # Test support scripts
```

## Running Tests

### Quick Start

```bash
# Run all unit tests (fast, no external dependencies)
make test

# Run all tests including integration and UI
make test-all

# Setup test environment (first time only)
make test-setup
```

### Unit Tests

```bash
# Go unit tests
make test-go

# JavaScript unit tests
make test-js
```

### Integration Tests

Integration tests use a mock ACP server for deterministic testing.

```bash
# Run all integration tests
make test-integration

# Run CLI tests only
make test-integration-cli

# Run API tests only
make test-integration-api
```

### UI Tests (Playwright)

UI tests run in a real browser against the Mitto web interface.

```bash
# Run UI tests (headless)
make test-ui

# Run UI tests with visible browser
make test-ui-headed

# Run UI tests in debug mode
make test-ui-debug

# View test report after running
make test-ui-report
```

## Mock ACP Server

The mock ACP server (`tests/mocks/acp-server/`) provides deterministic responses for testing. It implements the ACP protocol and responds based on scenario files in `tests/fixtures/responses/`.

### Building the Mock Server

```bash
make build-mock-acp
```

### Running Manually

```bash
./tests/mocks/acp-server/mock-acp-server --verbose
```

## Test Fixtures

### Configuration Files

- `default.yaml` - Standard test configuration
- `auth-enabled.yaml` - Configuration with authentication enabled
- `multi-workspace.yaml` - Multi-workspace configuration

### Mock Workspaces

- `project-alpha/` - Mock Go project
- `project-beta/` - Mock Node.js project
- `empty-project/` - Empty directory for edge cases

## CI/CD

Tests run automatically on GitHub Actions for every push and pull request. See `.github/workflows/tests.yml` for the workflow configuration.

## Writing New Tests

### Go Integration Tests

1. Add test files to `tests/integration/cli/` or `tests/integration/api/`
2. Use the `//go:build integration` build tag
3. Use helper functions from `tests/mocks/testutil/`

### Playwright UI Tests

1. Add test files to `tests/ui/specs/` with `.spec.ts` extension
2. Import fixtures from `../fixtures/test-fixtures`
3. Use centralized selectors from `../utils/selectors`

## Environment Variables

| Variable | Description |
|----------|-------------|
| `MITTO_TEST_MODE` | Set to `1` to enable test mode |
| `MITTO_DIR` | Override the Mitto data directory |
| `MITTO_TEST_URL` | Base URL for UI tests (default: `http://127.0.0.1:8089`) |
| `MITTO_TEST_AUTH` | Set to `1` to enable auth tests |
| `CI` | Set automatically in CI environments |

