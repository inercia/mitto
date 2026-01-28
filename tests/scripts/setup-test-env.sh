#!/bin/bash
# Setup test environment for Mitto tests
# This script prepares the test environment before running tests

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TEST_DIR="${MITTO_TEST_DIR:-/tmp/mitto-test-$$}"

echo "Setting up test environment..."
echo "  Project root: $PROJECT_ROOT"
echo "  Test directory: $TEST_DIR"

# Create test directory structure
mkdir -p "$TEST_DIR/sessions"
mkdir -p "$TEST_DIR/logs"

# Export environment variables
export MITTO_TEST_MODE=1
export MITTO_DIR="$TEST_DIR"
export MITTO_TEST_WORKSPACE="$PROJECT_ROOT/tests/fixtures/workspaces"
export MITTO_TEST_PORT="${MITTO_TEST_PORT:-8089}"

# Create a test settings.json
cat > "$TEST_DIR/settings.json" << EOF
{
  "acp_servers": [
    {"name": "mock-acp", "command": "$PROJECT_ROOT/tests/mocks/acp-server/mock-acp-server"}
  ],
  "web": {
    "host": "127.0.0.1",
    "port": $MITTO_TEST_PORT,
    "theme": "v2"
  }
}
EOF

# Create workspaces.json with test workspaces
cat > "$TEST_DIR/workspaces.json" << EOF
{
  "workspaces": [
    {
      "acp_server": "mock-acp",
      "acp_command": "$PROJECT_ROOT/tests/mocks/acp-server/mock-acp-server",
      "working_dir": "$PROJECT_ROOT/tests/fixtures/workspaces/project-alpha"
    },
    {
      "acp_server": "mock-acp",
      "acp_command": "$PROJECT_ROOT/tests/mocks/acp-server/mock-acp-server",
      "working_dir": "$PROJECT_ROOT/tests/fixtures/workspaces/project-beta"
    }
  ]
}
EOF

echo "Test environment setup complete."
echo ""
echo "Environment variables set:"
echo "  MITTO_TEST_MODE=$MITTO_TEST_MODE"
echo "  MITTO_DIR=$MITTO_DIR"
echo "  MITTO_TEST_WORKSPACE=$MITTO_TEST_WORKSPACE"
echo "  MITTO_TEST_PORT=$MITTO_TEST_PORT"

# Save environment to a file for other scripts to source
cat > "$TEST_DIR/.env" << EOF
export MITTO_TEST_MODE=1
export MITTO_DIR="$TEST_DIR"
export MITTO_TEST_WORKSPACE="$MITTO_TEST_WORKSPACE"
export MITTO_TEST_PORT=$MITTO_TEST_PORT
export PROJECT_ROOT="$PROJECT_ROOT"
EOF

echo ""
echo "To use this environment in your shell:"
echo "  source $TEST_DIR/.env"

