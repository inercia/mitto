#!/bin/bash
# Start the Mitto test server for Playwright UI tests.
# This script creates the test settings before starting mitto.

set -e

# Get the project root (where this script is run from via cd ../..)
PROJECT_ROOT="$(pwd)"

# Use MITTO_DIR from env or default to /tmp/mitto-test
MITTO_DIR="${MITTO_DIR:-/tmp/mitto-test}"
export MITTO_DIR

# Clean and create test directory
rm -rf "$MITTO_DIR"
mkdir -p "$MITTO_DIR/sessions"

# Create settings.json with mock-acp configuration
# CRITICAL: This config MUST have:
#   - external_port: -1 (disable external access, no 0.0.0.0 binding)
#   - NO web.auth section (prevents keychain access on macOS)
#   - host: 127.0.0.1 (localhost only)
# These settings ensure tests run in isolation without external access or auth prompts.
cat > "$MITTO_DIR/settings.json" << EOF
{
  "acp_servers": [
    {
      "name": "mock-acp",
      "command": "${PROJECT_ROOT}/tests/mocks/acp-server/mock-acp-server"
    }
  ],
  "web": {
    "host": "127.0.0.1",
    "port": 8089,
    "external_port": -1,
    "theme": "v2"
  }
}
EOF

echo "Created test settings at $MITTO_DIR/settings.json"

# Start mitto web server
exec ./mitto web --port 8089 --dir mock-acp:tests/fixtures/workspaces/project-alpha

