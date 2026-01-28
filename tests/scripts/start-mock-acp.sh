#!/bin/bash
# Start the mock ACP server for testing
# Usage: ./start-mock-acp.sh [--verbose]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
MOCK_ACP_BIN="$PROJECT_ROOT/tests/mocks/acp-server/mock-acp-server"
SCENARIOS_DIR="$PROJECT_ROOT/tests/fixtures/responses"

# Parse arguments
VERBOSE=""
while [[ $# -gt 0 ]]; do
    case $1 in
        --verbose|-v)
            VERBOSE="--verbose"
            shift
            ;;
        *)
            shift
            ;;
    esac
done

# Build if necessary
if [[ ! -f "$MOCK_ACP_BIN" ]]; then
    echo "Building mock ACP server..."
    (cd "$PROJECT_ROOT" && go build -o "$MOCK_ACP_BIN" ./tests/mocks/acp-server)
fi

echo "Starting mock ACP server..."
echo "  Binary: $MOCK_ACP_BIN"
echo "  Scenarios: $SCENARIOS_DIR"

exec "$MOCK_ACP_BIN" --scenarios "$SCENARIOS_DIR" $VERBOSE

