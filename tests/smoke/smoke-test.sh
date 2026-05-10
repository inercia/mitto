#!/bin/bash
# Smoke tests for Mitto on Linux
# Run inside Docker container to verify basic functionality
set -e

PASS=0
FAIL=0

check() {
    local name="$1"; shift
    if "$@" >/dev/null 2>&1; then
        echo "✅ $name"
        PASS=$((PASS+1))
    else
        echo "❌ $name"
        FAIL=$((FAIL+1))
    fi
}

check_output() {
    local name="$1"; shift
    local output
    if output=$("$@" 2>&1); then
        echo "✅ $name"
        PASS=$((PASS+1))
    else
        echo "❌ $name (output: $output)"
        FAIL=$((FAIL+1))
    fi
}

echo "=== Mitto Linux Smoke Tests ==="
echo ""

# --- Binary tests (no server needed) ---
echo "--- Binary Tests ---"
check "mitto binary executable"  mitto --help
check "mock-acp-server binary"   mock-acp-server --help

# --- Start web server ---
echo ""
echo "--- Starting Mitto web server ---"

MITTO_DIR="${MITTO_DIR:-/home/mitto/.mitto}"
mkdir -p "$MITTO_DIR/sessions"

SCENARIOS_DIR="/home/mitto/fixtures/responses"
MOCK_CMD="mock-acp-server -scenarios ${SCENARIOS_DIR} -delay 200ms"

# Create settings if not already present (entrypoint.sh creates them, but this
# allows standalone execution too)
if [ ! -f "$MITTO_DIR/settings.json" ]; then
    cat > "$MITTO_DIR/settings.json" << EOF
{
  "acp_servers": [{"name": "mock-acp", "command": "${MOCK_CMD}"}],
  "web": {"host": "127.0.0.1", "port": 8089, "external_port": -1, "theme": "v2"}
}
EOF
    cat > "$MITTO_DIR/workspaces.json" << EOF
{
  "workspaces": [
    {"acp_server": "mock-acp", "acp_command": "${MOCK_CMD}", "working_dir": "/home/mitto/fixtures/workspaces/project-alpha"}
  ]
}
EOF
fi

mitto web --port 8089 &
MITTO_PID=$!
trap "kill $MITTO_PID 2>/dev/null" EXIT

# Wait for server to be ready
echo "Waiting for server..."
READY=false
for i in $(seq 1 30); do
    if curl -sf http://127.0.0.1:8089/mitto/api/health >/dev/null 2>&1; then
        READY=true
        break
    fi
    sleep 1
done

if [ "$READY" != "true" ]; then
    echo "❌ Server failed to start within 30 seconds"
    exit 1
fi
echo "Server is ready."
echo ""

# --- API tests ---
echo "--- API Tests ---"
check "health endpoint"      curl -sf http://127.0.0.1:8089/mitto/api/health
check "config endpoint"      curl -sf http://127.0.0.1:8089/mitto/api/config
check "workspaces endpoint"  curl -sf http://127.0.0.1:8089/mitto/api/workspaces
check "sessions endpoint"    curl -sf http://127.0.0.1:8089/mitto/api/sessions

# --- Static assets ---
echo ""
echo "--- Static Asset Tests ---"
check "index.html loads"     curl -sf http://127.0.0.1:8089/

# --- Session creation ---
echo ""
echo "--- Session Tests ---"
check "create session" \
    curl -sf -X POST http://127.0.0.1:8089/mitto/api/sessions \
        -H "Content-Type: application/json" \
        -d '{"acp_server":"mock-acp","working_dir":"/home/mitto/fixtures/workspaces/project-alpha"}'

# --- Agent Detection Tests ---
echo ""
echo "--- Agent Detection Tests ---"

SCAN_RESULT=$(curl -sf -X POST http://127.0.0.1:8089/mitto/api/agents/scan 2>&1 || true)

check "agent scan endpoint"       bash -c 'echo "$1" | grep -q "available"'       -- "$SCAN_RESULT"
check "claude-code detected"      bash -c 'echo "$1" | grep -q "claude-code"'     -- "$SCAN_RESULT"
check "gemini stub detected"      bash -c 'echo "$1" | grep -q "gemini"'          -- "$SCAN_RESULT"
check "at least 1 agent available" bash -c '[ "$(echo "$1" | grep -o "\"available\":true" | wc -l)" -ge 1 ]' -- "$SCAN_RESULT"

# --- Results ---
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] || exit 1
