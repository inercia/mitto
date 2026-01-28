#!/bin/bash
# Start Mitto web server for testing
# Usage: ./start-test-server.sh [--port PORT]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
MITTO_BIN="$PROJECT_ROOT/mitto"
TEST_CONFIG="$PROJECT_ROOT/tests/fixtures/config/default.yaml"
PORT="${MITTO_TEST_PORT:-8089}"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --port|-p)
            PORT="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

# Build if necessary
if [[ ! -f "$MITTO_BIN" ]]; then
    echo "Building mitto..."
    (cd "$PROJECT_ROOT" && make build)
fi

# Setup test environment
source "$SCRIPT_DIR/setup-test-env.sh"

echo "Starting Mitto test server..."
echo "  Binary: $MITTO_BIN"
echo "  Config: $TEST_CONFIG"
echo "  Port: $PORT"
echo "  MITTO_DIR: $MITTO_DIR"

# Start the server
exec "$MITTO_BIN" web --port "$PORT" --config "$TEST_CONFIG"

