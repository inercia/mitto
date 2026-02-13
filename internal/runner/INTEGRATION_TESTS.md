# Runner Integration Tests

This directory contains integration tests for the restricted runner implementations.

## Running Integration Tests

Integration tests are tagged with `// +build integration` and are **not run by default**.

To run integration tests:

```bash
go test -tags=integration ./internal/runner/... -v
```

## Test Coverage

### sandbox-exec (macOS only)

Tests the macOS sandbox-exec runner:
- Basic command execution
- stdin/stdout/stderr pipes
- Network restrictions

**Requirements:**
- macOS operating system
- `sandbox-exec` command (built-in on macOS)

**Run:**
```bash
go test -tags=integration ./internal/runner/... -v -run TestSandboxExec
```

### firejail (Linux only)

Tests the Linux firejail runner:
- Basic command execution
- stdin/stdout/stderr pipes
- Network restrictions

**Requirements:**
- Linux operating system
- `firejail` installed (`sudo apt install firejail` or `sudo dnf install firejail`)

**Run:**
```bash
go test -tags=integration ./internal/runner/... -v -run TestFirejail
```

### docker (All platforms)

Tests the Docker runner:
- Basic command execution
- stdin/stdout/stderr pipes
- Container isolation

**Requirements:**
- Docker installed and running
- `alpine:latest` image available (will be pulled if not present)

**Run:**
```bash
go test -tags=integration ./internal/runner/... -v -run TestDocker
```

### Network Restrictions

Tests that network restrictions are properly enforced:
- Uses sandbox-exec on macOS
- Uses firejail on Linux
- Verifies that network access is blocked when `allow_networking: false`

**Run:**
```bash
go test -tags=integration ./internal/runner/... -v -run TestNetworkRestriction
```

## CI/CD Integration

Integration tests are skipped in CI by default. To run them in CI:

1. **GitHub Actions:**
```yaml
- name: Run integration tests
  run: go test -tags=integration ./internal/runner/... -v
  if: runner.os == 'macOS' || runner.os == 'Linux'
```

2. **Local development:**
```bash
# Run all tests including integration
make test-all

# Or manually
go test -tags=integration ./internal/runner/... -v
```

## Test Behavior

All integration tests:
- **Skip gracefully** if required tools are not installed
- **Timeout** after a reasonable duration (10-30 seconds)
- **Clean up** resources (containers, processes) on completion
- **Log** stderr output for debugging

## Troubleshooting

### sandbox-exec tests fail on macOS

**Error:** `sandbox-exec: operation not permitted`

**Solution:** Grant Terminal.app or your IDE full disk access in System Preferences → Security & Privacy → Privacy → Full Disk Access

### firejail tests fail on Linux

**Error:** `firejail: command not found`

**Solution:** Install firejail:
```bash
# Ubuntu/Debian
sudo apt install firejail

# Fedora/RHEL
sudo dnf install firejail

# Arch
sudo pacman -S firejail
```

### docker tests fail

**Error:** `docker daemon not running`

**Solution:** Start Docker:
```bash
# macOS
open -a Docker

# Linux
sudo systemctl start docker
```

**Error:** `Cannot connect to the Docker daemon`

**Solution:** Add your user to the docker group:
```bash
sudo usermod -aG docker $USER
# Log out and back in
```

## Adding New Tests

When adding new integration tests:

1. Add the `// +build integration` tag at the top of the file
2. Check for required tools with `exec.LookPath()`
3. Skip gracefully if tools are not available
4. Use reasonable timeouts (10-30 seconds)
5. Clean up resources in defer statements
6. Log stderr output for debugging

Example:
```go
// +build integration

func TestMyRunner_Integration(t *testing.T) {
    // Check if tool is available
    if _, err := exec.LookPath("mytool"); err != nil {
        t.Skip("mytool not found in PATH")
    }

    // Create runner
    r, err := NewRunner(...)
    if err != nil {
        t.Fatalf("NewRunner failed: %v", err)
    }

    // Test with timeout
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    // Run test
    stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "echo", []string{"test"}, nil)
    // ... test logic ...
}
```

