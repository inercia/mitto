#!/bin/bash
# Integration test for mitto --once mode
# Tests that single-shot queries work correctly with an ACP server

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test configuration
TIMEOUT_SECONDS=60
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
MITTO_BIN="$PROJECT_ROOT/mitto"

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_SKIPPED=0

# Helper functions
log_info() {
    echo -e "${NC}[INFO] $1${NC}"
}

log_pass() {
    echo -e "${GREEN}[PASS] $1${NC}"
    ((TESTS_PASSED++))
}

log_fail() {
    echo -e "${RED}[FAIL] $1${NC}"
    ((TESTS_FAILED++))
}

log_skip() {
    echo -e "${YELLOW}[SKIP] $1${NC}"
    ((TESTS_SKIPPED++))
}

# Check if mitto binary exists, build if not
ensure_mitto_built() {
    if [[ ! -f "$MITTO_BIN" ]]; then
        log_info "Building mitto..."
        (cd "$PROJECT_ROOT" && make build)
        if [[ ! -f "$MITTO_BIN" ]]; then
            echo "ERROR: Failed to build mitto"
            exit 1
        fi
    fi
}

# Check if auggie is available
check_auggie_available() {
    if ! command -v auggie &> /dev/null; then
        return 1
    fi
    return 0
}

# Check if mitto config exists with auggie configured
check_mitto_config() {
    local config_file="$HOME/.mittorc"
    if [[ -f "$config_file" ]]; then
        if grep -q "auggie" "$config_file"; then
            return 0
        fi
    fi
    return 1
}

# Test: --once flag with simple prompt
test_once_mode_basic() {
    ((TESTS_RUN++))
    local test_name="once_mode_basic"
    log_info "Running test: $test_name"

    # Run mitto with --once and capture output
    local output
    local exit_code=0

    output=$(timeout "$TIMEOUT_SECONDS" "$MITTO_BIN" cli --once "Say hello" --auto-approve 2>&1) || exit_code=$?

    # Check exit code
    if [[ $exit_code -ne 0 ]]; then
        log_fail "$test_name: Command exited with code $exit_code"
        echo "Output: $output"
        return 1
    fi

    # Check that we got some response (not empty)
    if [[ -z "$output" ]]; then
        log_fail "$test_name: No output received"
        return 1
    fi

    log_pass "$test_name"
    return 0
}

# Test: --once flag exits after response (doesn't hang)
test_once_mode_exits() {
    ((TESTS_RUN++))
    local test_name="once_mode_exits"
    log_info "Running test: $test_name"

    # Run with a short timeout to ensure it exits
    local start_time=$(date +%s)
    local output
    local exit_code=0

    output=$(timeout 30 "$MITTO_BIN" cli --once "What is 2+2?" --auto-approve 2>&1) || exit_code=$?

    local end_time=$(date +%s)
    local duration=$((end_time - start_time))

    # Check that it didn't timeout (30 seconds should be plenty)
    if [[ $exit_code -eq 124 ]]; then
        log_fail "$test_name: Command timed out (hung)"
        return 1
    fi

    # Check exit code
    if [[ $exit_code -ne 0 ]]; then
        log_fail "$test_name: Command exited with code $exit_code"
        echo "Output: $output"
        return 1
    fi

    log_pass "$test_name (completed in ${duration}s)"
    return 0
}

# Test: --once with --acp flag
test_once_mode_with_acp_flag() {
    ((TESTS_RUN++))
    local test_name="once_mode_with_acp_flag"
    log_info "Running test: $test_name"

    local output
    local exit_code=0

    output=$(timeout "$TIMEOUT_SECONDS" "$MITTO_BIN" cli --acp auggie --once "Say hi" --auto-approve 2>&1) || exit_code=$?

    if [[ $exit_code -ne 0 ]]; then
        log_fail "$test_name: Command exited with code $exit_code"
        echo "Output: $output"
        return 1
    fi

    log_pass "$test_name"
    return 0
}

# Main test runner
main() {
    echo "========================================"
    echo "Mitto Integration Tests - Once Mode"
    echo "========================================"
    echo ""

    # Ensure mitto is built
    ensure_mitto_built

    # Check prerequisites
    if ! check_auggie_available; then
        log_skip "auggie is not installed - skipping integration tests"
        echo ""
        echo "To run these tests, install auggie:"
        echo "  https://github.com/augmentcode/auggie"
        echo ""
        exit 0
    fi

    if ! check_mitto_config; then
        log_skip "mitto config not found or auggie not configured"
        echo ""
        echo "Create ~/.mittorc with auggie configured:"
        echo "  acp:"
        echo "    - auggie:"
        echo "        command: auggie --acp"
        echo ""
        exit 0
    fi

    log_info "Prerequisites met, running tests..."
    echo ""

    # Run tests
    test_once_mode_basic || true
    test_once_mode_exits || true
    test_once_mode_with_acp_flag || true

    # Print summary
    echo ""
    echo "========================================"
    echo "Test Summary"
    echo "========================================"
    echo "Tests run:    $TESTS_RUN"
    echo -e "Tests passed: ${GREEN}$TESTS_PASSED${NC}"
    echo -e "Tests failed: ${RED}$TESTS_FAILED${NC}"
    echo -e "Tests skipped: ${YELLOW}$TESTS_SKIPPED${NC}"
    echo ""

    # Exit with failure if any tests failed
    if [[ $TESTS_FAILED -gt 0 ]]; then
        exit 1
    fi
    exit 0
}

main "$@"

