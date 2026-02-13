# Runner System Testing Guide

This directory contains manual testing resources for the restricted runner system.

## Quick Start

### 1. Set Up Test Environment

```bash
# Run the setup script to create test workspaces
./tests/manual/test-runners.sh
```

This creates test workspaces in `/tmp/mitto-runner-tests/`:
- `workspace-exec` - No restrictions (baseline)
- `workspace-sandbox` - macOS sandboxing
- `workspace-firejail` - Linux isolation (fallback test on macOS)
- `workspace-docker` - Container isolation

### 2. Start Mitto Web Server

```bash
# Build mitto first
make build

# Start with test configuration
./mitto web --config tests/manual/test-config.yaml
```

### 3. Open Browser

Navigate to http://localhost:8080

### 4. Run Manual Tests

Follow the test plan in `runner-test-plan.md` to verify:
- ✅ Runner badges display correctly
- ✅ Fallback notifications appear
- ✅ Restrictions are enforced
- ✅ Pre-flight validation works

## Test Files

### test-runners.sh
Setup script that creates test workspaces with different runner configurations.

**Usage**:
```bash
./tests/manual/test-runners.sh
```

**Output**:
- Creates test workspaces in `/tmp/mitto-runner-tests/`
- Checks system capabilities (sandbox-exec, firejail, docker)
- Prints next steps for manual testing

### test-config.yaml
Mitto configuration file with runner settings for all runner types.

**Features**:
- Global per-runner-type configuration
- Reasonable defaults for each runner
- Test ACP server configuration

### runner-test-plan.md
Comprehensive test plan with detailed scenarios.

**Scenarios**:
1. exec runner (baseline)
2. sandbox-exec runner (macOS)
3. firejail fallback (macOS → exec)
4. docker runner (with daemon running)
5. docker fallback (daemon not running)

## Platform-Specific Testing

### macOS
Available runners:
- ✅ exec
- ✅ sandbox-exec
- ❌ firejail (fallback test)
- ⚠️ docker (if daemon running)

**Test Focus**:
- sandbox-exec restrictions
- firejail fallback behavior
- docker container isolation

### Linux
Available runners:
- ✅ exec
- ❌ sandbox-exec (fallback test)
- ✅ firejail
- ⚠️ docker (if daemon running)

**Test Focus**:
- firejail restrictions
- sandbox-exec fallback behavior
- docker container isolation

## Automated Integration Tests

### Run All Integration Tests

```bash
make test-integration-runner
```

### Run Specific Tests

```bash
# Runner platform detection
go test -v -tags=integration ./internal/runner/... -run TestRunnerPlatformDetection

# Runner restrictions
go test -v -tags=integration ./internal/runner/... -run TestRunnerRestrictions

# Web layer integration
go test -v -tags=integration ./internal/web/... -run TestRunnerMetadata
```

## Common Test Scenarios

### Scenario: Verify Runner Badge

1. Create session with workspace
2. Check session header
3. Verify badge shows correct:
   - Icon (🔒 or ⚠️)
   - Color (green or yellow)
   - Runner type name
   - Tooltip text

### Scenario: Verify Fallback Toast

1. Create session with unsupported runner
2. Wait for toast notification
3. Verify toast shows:
   - Warning icon (⚠️)
   - Requested runner type
   - Fallback runner type (exec)
   - Reason for fallback
4. Verify toast auto-dismisses after 10 seconds
5. Verify can dismiss manually

### Scenario: Verify Pre-flight Validation

1. Open workspace settings
2. Select unsupported runner type
3. Click Save
4. Check server logs for warning
5. Verify configuration saved successfully
6. Create session
7. Verify fallback occurs with toast

## Troubleshooting

### Docker Tests Fail

**Problem**: Docker daemon not running

**Solution**:
```bash
# Start Docker Desktop (macOS)
open -a Docker

# Or start Docker daemon (Linux)
sudo systemctl start docker
```

### sandbox-exec Tests Fail on macOS

**Problem**: Sandbox profile syntax error

**Solution**:
- Check `.mittorc` syntax
- Verify folder paths exist
- Check server logs for details

### No Toast Notifications

**Problem**: Fallback not triggering

**Solution**:
- Verify runner type is actually unsupported
- Check browser console for errors
- Verify WebSocket connection is active
- Check server logs for fallback events

## CI/CD Integration

### GitHub Actions

Add to `.github/workflows/test.yml`:

```yaml
- name: Run runner integration tests
  run: make test-integration-runner
```

### Platform-Specific Tests

```yaml
- name: Run macOS runner tests
  if: runner.os == 'macOS'
  run: go test -v -tags=integration ./internal/runner/... -run TestRunnerRestrictions

- name: Run Linux runner tests
  if: runner.os == 'Linux'
  run: go test -v -tags=integration ./internal/runner/... -run TestFirejail
```

## Test Coverage

Current coverage:
- ✅ Platform detection
- ✅ Fallback behavior
- ✅ Metadata storage
- ✅ WebSocket messages
- ✅ UI badge display
- ✅ Toast notifications
- ✅ Pre-flight validation
- ✅ Configuration validation

## Next Steps

After manual testing:
1. Document any issues found
2. Update test plan with new scenarios
3. Add automated tests for edge cases
4. Update documentation with findings

