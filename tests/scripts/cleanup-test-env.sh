#!/bin/bash
# Cleanup test environment after Mitto tests
# This script removes temporary test files and directories

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_DIR="${MITTO_DIR:-/tmp/mitto-test-*}"

echo "Cleaning up test environment..."

# Kill any running mock ACP servers
pkill -f "mock-acp-server" 2>/dev/null || true

# Kill any running mitto test servers
pkill -f "mitto.*--port.*8089" 2>/dev/null || true

# Remove test directories
if [[ "$TEST_DIR" == /tmp/mitto-test* ]]; then
    echo "Removing test directory: $TEST_DIR"
    rm -rf "$TEST_DIR"
fi

# Clean up any stale test directories
for dir in /tmp/mitto-test-*; do
    if [[ -d "$dir" ]]; then
        echo "Removing stale test directory: $dir"
        rm -rf "$dir"
    fi
done

echo "Cleanup complete."

