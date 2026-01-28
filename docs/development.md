# Development Guide

This document covers development setup, building, and testing for Mitto.

## Prerequisites

- **Go 1.23** or later
- **Make** (for build automation)
- **macOS 10.15+** (for building the macOS app)
- **Command Line Tools** (`xcode-select --install`) for macOS builds

## Building

### CLI and Web Server

```bash
# Build the CLI binary
make build

# Install to your GOPATH/bin
make install

# Build and run with arguments
make run ARGS="cli"
make run ARGS="web --port 8080"
```

### macOS Desktop App

```bash
# Build the macOS app bundle (creates Mitto.app)
make build-mac-app

# Clean and rebuild
make clean-mac-app && make build-mac-app
```

The app bundle is created in the project root as `Mitto.app`.

### Cleaning

```bash
# Clean CLI build artifacts
make clean

# Clean macOS app
make clean-mac-app

# Clean everything
make clean clean-mac-app
```

## Testing

```bash
# Run all tests
make test

# Run tests with verbose output
go test -v ./...

# Run tests for a specific package
go test -v ./internal/session/...

# Run integration tests
./tests/integration/run-all.sh
```

### Test Patterns

- Use `t.TempDir()` for file-based tests
- Use table-driven tests for multiple scenarios
- Test both success and error paths

```go
func TestSomething(t *testing.T) {
    tmpDir := t.TempDir()
    // ... test code
}
```

## Code Quality

```bash
# Format code
make fmt

# Run linter (if configured)
go vet ./...
```

## Project Structure

```
cmd/mitto/          → CLI entry point
cmd/mitto-app/      → macOS app entry point
config/             → Embedded default configuration
internal/           → Internal packages
├── acp/            → ACP protocol client
├── appdir/         → Platform-native directories
├── auxiliary/      → Background ACP session
├── cmd/            → CLI commands (Cobra)
├── config/         → Configuration loading
├── fileutil/       → JSON file utilities
├── session/        → Session persistence
└── web/            → Web server and API
platform/mac/       → macOS resources (icons, plist)
web/                → Embedded frontend assets
docs/               → Documentation
tests/              → Integration tests
```

## Running Locally

### CLI Mode

```bash
# Interactive CLI with default ACP server
./mitto cli

# With specific server
./mitto cli --acp claude-code

# With debug logging
./mitto cli --debug
```

### Web Mode

```bash
# Start web server on default port
./mitto web

# Custom port
./mitto web --port 3000

# With specific working directory
./mitto web --dir /path/to/project
```

### macOS App

```bash
# Run the built app
open Mitto.app

# With environment overrides
MITTO_ACP_SERVER=claude-code open Mitto.app
MITTO_WORK_DIR=/path/to/project open Mitto.app
```

## Debugging

### Enable Debug Logging

```bash
# CLI
mitto cli --debug

# Web
mitto web --debug
```

### Frontend Development

The web frontend uses no build step - edit files in `web/static/` and refresh the browser.

- `web/static/app.js` - Main Preact application
- `web/static/lib.js` - Pure utility functions
- `web/static/styles.css` - Custom CSS
- `web/static/index.html` - HTML shell with CDN imports

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests: `make test`
5. Format code: `make fmt`
6. Submit a pull request

See [architecture.md](architecture.md) for detailed information about the codebase structure.

