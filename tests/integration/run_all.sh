#!/bin/bash
# Run all integration tests for mitto
# Usage: ./run_all.sh

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "========================================"
echo "Mitto Integration Test Suite"
echo "========================================"
echo ""

# Track overall results
TOTAL_SUITES=0
PASSED_SUITES=0
FAILED_SUITES=0

# Find and run all test scripts
for test_script in "$SCRIPT_DIR"/test_*.sh; do
    if [[ -f "$test_script" ]]; then
        ((TOTAL_SUITES++))
        test_name=$(basename "$test_script")
        echo "Running: $test_name"
        echo "----------------------------------------"
        
        if bash "$test_script"; then
            ((PASSED_SUITES++))
            echo -e "${GREEN}Suite passed: $test_name${NC}"
        else
            ((FAILED_SUITES++))
            echo -e "${RED}Suite failed: $test_name${NC}"
        fi
        echo ""
    fi
done

# Print overall summary
echo "========================================"
echo "Overall Summary"
echo "========================================"
echo "Test suites run:    $TOTAL_SUITES"
echo -e "Test suites passed: ${GREEN}$PASSED_SUITES${NC}"
echo -e "Test suites failed: ${RED}$FAILED_SUITES${NC}"
echo ""

if [[ $FAILED_SUITES -gt 0 ]]; then
    echo -e "${RED}INTEGRATION TESTS FAILED${NC}"
    exit 1
else
    echo -e "${GREEN}ALL INTEGRATION TESTS PASSED${NC}"
    exit 0
fi

