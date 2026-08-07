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

# Pin the project-alpha folder so its sidebar row (and the per-folder "New
# conversation" button) is visible even before any session exists. Without
# this, a fresh test environment shows "No conversations yet" with zero
# folder rows -- the sidebar's global "New Conversation" button was removed
# in favor of a per-folder button, which only renders once its folder is
# either session-derived or pinned (mitto-vmnh).
WORKSPACE_DIR="${PROJECT_ROOT}/tests/fixtures/workspaces/project-alpha"
cat > "$MITTO_DIR/folders.json" << EOF
{
  "folders": {
    "${WORKSPACE_DIR}": {
      "pinned": true
    }
  }
}
EOF

echo "Created test folders at $MITTO_DIR/folders.json"

# Start mitto web server
exec ./mitto web --port 8089 --dir mock-acp:tests/fixtures/workspaces/project-alpha

