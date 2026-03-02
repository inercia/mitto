# Hierarchical Session Test Helpers

This directory contains helper tools for testing hierarchical session grouping in Mitto.

## Quick Start

### Option 1: Automated Setup and Test

```bash
# Run everything automatically (setup + tests in headed mode)
./setup-hierarchical-test.sh --run-tests --headed
```

### Option 2: Manual Setup

```bash
# 1. Build the helper
go build -o create-hierarchical-sessions create-hierarchical-sessions.go

# 2. Create hierarchical sessions
export MITTO_DIR=/tmp/mitto-test
./create-hierarchical-sessions \
  -dir "$MITTO_DIR" \
  -parent "Main Analysis Task" \
  -children "Background Research,Code Review,Documentation"

# 3. Run the tests
cd ..
npx playwright test hierarchical-sessions --headed
```

## Files

### `create-hierarchical-sessions.go`

Go program that creates sessions with parent-child relationships directly in the session store.

**Why?** The Mitto API doesn't support setting `parent_session_id` directly. This field is only set by the `mitto_conversation_new` MCP tool. This helper simulates that behavior for testing.

**Usage:**
```bash
./create-hierarchical-sessions \
  -dir /tmp/mitto-test \
  -parent "Parent Session Name" \
  -children "Child 1,Child 2,Child 3" \
  -working-dir /path/to/workspace \
  -acp-server mock-acp
```

**Output:**
- Creates sessions in `$MITTO_DIR/sessions/`
- Writes JSON summary to `$MITTO_DIR/hierarchical-sessions.json`
- Prints session IDs to stdout

### `setup-hierarchical-test.sh`

Automated setup script that builds the helper, creates sessions, and optionally runs tests.

**Usage:**
```bash
# Setup only
./setup-hierarchical-test.sh

# Setup and run tests
./setup-hierarchical-test.sh --run-tests

# Setup and run tests in headed mode
./setup-hierarchical-test.sh --run-tests --headed

# Custom configuration
./setup-hierarchical-test.sh \
  --dir /custom/mitto/dir \
  --parent "My Project" \
  --children "Task 1,Task 2,Task 3" \
  --run-tests
```

**Options:**
- `--dir DIR` - Mitto data directory (default: /tmp/mitto-test)
- `--parent NAME` - Parent session name
- `--children NAMES` - Comma-separated child names
- `--run-tests` - Run Playwright tests after setup
- `--headed` - Run tests in headed mode (visible browser)
- `--help` - Show help message

## Testing Workflow

### 1. Start the Test Server

```bash
# In the project root
make test-ui-server
```

This starts Mitto on `http://localhost:8089` with the mock ACP server.

### 2. Create Hierarchical Sessions

```bash
cd tests/ui/helpers
./setup-hierarchical-test.sh
```

### 3. Run the Tests

```bash
cd tests/ui
npx playwright test hierarchical-sessions
```

### 4. Review Results

Check the screenshots in `tests/ui/test-results/`:
- `hierarchical-tree-initial.png` - Initial tree structure
- `hierarchical-tree-child-active.png` - After clicking child
- `hierarchical-tree-parent-active.png` - After clicking parent

## Debugging

### Check Created Sessions

```bash
# View the JSON output
cat /tmp/mitto-test/hierarchical-sessions.json | jq

# List all sessions via API
curl http://localhost:8089/mitto/api/sessions | jq

# Check for parent_session_id
curl http://localhost:8089/mitto/api/sessions | \
  jq '.[] | select(.parent_session_id != "") | {session_id, name, parent_session_id}'
```

### Check Session Files

```bash
# List session directories
ls -la /tmp/mitto-test/sessions/

# View a session's metadata
cat /tmp/mitto-test/sessions/<session-id>/metadata.json | jq
```

### Manual Browser Testing

1. Start the test server: `make test-ui-server`
2. Create sessions: `./setup-hierarchical-test.sh`
3. Open browser: `http://localhost:8089`
4. Open DevTools Console (F12)
5. Click through sessions and observe:
   - Does the tree stay visible?
   - Are there console errors?
   - Does the session order change?

## Common Issues

### "Session store closed" error

The test server might have stopped. Restart it:
```bash
make test-ui-server
```

### "No such file or directory" for hierarchical-sessions.json

The helper hasn't been run yet. Run:
```bash
./setup-hierarchical-test.sh
```

### Tests skip the advanced test

The advanced test requires the helper to create sessions first:
```bash
./setup-hierarchical-test.sh
npx playwright test hierarchical-sessions --grep "real parent-child"
```

## Related Documentation

- [Hierarchical Sessions Testing Guide](../specs/HIERARCHICAL_SESSIONS_TESTING.md)
- [Test Summary](../../../HIERARCHICAL_SESSION_TEST_SUMMARY.md)
- [Parent Cleanup Implementation](../../../PARENT_CLEANUP_IMPLEMENTATION.md)
