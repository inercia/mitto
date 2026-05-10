#!/bin/bash
set -e

MITTO_DIR="${MITTO_DIR:-/home/mitto/.mitto}"
mkdir -p "$MITTO_DIR/sessions"

SCENARIOS_DIR="/home/mitto/fixtures/responses"
MOCK_CMD="mock-acp-server -scenarios ${SCENARIOS_DIR} -delay 200ms"

# Create settings.json (mirrors tests/ui/global-setup.ts structure)
cat > "$MITTO_DIR/settings.json" << EOF
{
  "acp_servers": [
    {
      "name": "mock-acp",
      "command": "${MOCK_CMD}"
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

# Create workspaces.json with a single workspace (matching test expectations)
# NOTE: Most Playwright tests assume single-workspace mode (no workspace selection dialog).
# Workspace dialog tests (workspace-dialog.spec.ts) require >5 workspaces, but those tests
# POST host-local paths via API — which don't exist in Docker. Those tests are expected
# to fail in the Docker smoke test environment.
cat > "$MITTO_DIR/workspaces.json" << EOF
{
  "workspaces": [
    {"acp_server": "mock-acp", "acp_command": "${MOCK_CMD}", "working_dir": "/home/mitto/fixtures/workspaces/project-alpha"}
  ]
}
EOF

# Start Mitto web server bound to 0.0.0.0 so Docker port-mapping works directly
# NOTE: Do NOT use --dir flag here — it overrides workspaces.json and limits to 1 workspace.
exec mitto web --host 0.0.0.0 --port 8089
