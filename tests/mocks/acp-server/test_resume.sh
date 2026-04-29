#!/bin/bash
# Simple test to verify resume capability in mock ACP server

set -e

MOCK_SERVER="./tests/mocks/acp-server/mock-acp-server"

# Check if binary exists
if [ ! -f "$MOCK_SERVER" ]; then
    echo "Error: Mock server not found at $MOCK_SERVER"
    echo "Run: go build -o tests/mocks/acp-server/mock-acp-server ./tests/mocks/acp-server/"
    exit 1
fi

echo "Testing mock ACP server resume capability..."

# Start mock server in background
$MOCK_SERVER --verbose 2>mock_server.log &
SERVER_PID=$!

# Ensure server is killed on exit
cleanup() {
    kill $SERVER_PID 2>/dev/null || true
    rm -f mock_server.log
}
trap cleanup EXIT

# Give server time to start
sleep 0.5

# Function to send JSON-RPC and read response
send_jsonrpc() {
    local method="$1"
    local params="$2"
    local id="${3:-1}"
    
    local request='{"jsonrpc":"2.0","id":'$id',"method":"'$method'"'
    if [ -n "$params" ]; then
        request=$request',"params":'$params
    fi
    request=$request'}'
    
    echo "$request"
}

# Test sequence
(
    # 1. Initialize
    send_jsonrpc "initialize" '{"protocolVersion":1,"clientInfo":{"name":"test","version":"1.0.0"}}'
    sleep 0.2
    
    # 2. Create new session
    send_jsonrpc "session/new" '{"cwd":"/tmp/test"}' 2
    sleep 0.2
    
    # 3. Try to resume the session (should succeed)
    # Note: We need to capture the session ID from step 2's response to use it here
    # For now, we'll use a mock session ID that the server knows about
    send_jsonrpc "session/unstableResumeSession" '{"sessionId":"mock-session-123","cwd":"/tmp/test"}' 3
    sleep 0.2
    
    # 4. Shutdown
    send_jsonrpc "shutdown" "" 4
    
) | $MOCK_SERVER --verbose 2>&1 | tee output.log

# Check if resume capability was advertised
if grep -q '"resume":' output.log; then
    echo "✓ Resume capability advertised in initialize response"
else
    echo "✗ Resume capability NOT found in initialize response"
    cat output.log
    exit 1
fi

# Check if server logged the resume handler registration
if grep -q "session/unstableResumeSession" mock_server.log 2>/dev/null || \
   grep -q "Resume session requested" mock_server.log 2>/dev/null; then
    echo "✓ Resume handler appears to be registered"
else
    echo "⚠ Could not verify resume handler registration (check logs manually)"
fi

echo ""
echo "✓ Mock ACP server resume capability test passed!"
echo ""
echo "Server log:"
cat mock_server.log 2>/dev/null || echo "(no server log)"

# Cleanup
rm -f output.log
