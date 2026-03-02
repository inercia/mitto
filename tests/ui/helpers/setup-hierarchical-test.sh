#!/bin/bash
set -e

# Setup script for hierarchical session testing
# This script:
# 1. Builds the helper tool
# 2. Creates hierarchical sessions in the test environment
# 3. Optionally runs the Playwright tests

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# Default values
MITTO_DIR="${MITTO_DIR:-/tmp/mitto-test}"
PARENT_NAME="Main Analysis Task"
CHILDREN="Background Research,Code Review,Documentation Update"
RUN_TESTS=false
HEADED=false

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --dir)
      MITTO_DIR="$2"
      shift 2
      ;;
    --parent)
      PARENT_NAME="$2"
      shift 2
      ;;
    --children)
      CHILDREN="$2"
      shift 2
      ;;
    --run-tests)
      RUN_TESTS=true
      shift
      ;;
    --headed)
      HEADED=true
      shift
      ;;
    --help)
      echo "Usage: $0 [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --dir DIR           Mitto data directory (default: /tmp/mitto-test)"
      echo "  --parent NAME       Parent session name (default: 'Main Analysis Task')"
      echo "  --children NAMES    Comma-separated child names (default: 'Background Research,Code Review,Documentation Update')"
      echo "  --run-tests         Run Playwright tests after creating sessions"
      echo "  --headed            Run tests in headed mode (visible browser)"
      echo "  --help              Show this help message"
      echo ""
      echo "Example:"
      echo "  $0 --parent 'My Project' --children 'Task 1,Task 2,Task 3' --run-tests"
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      echo "Use --help for usage information"
      exit 1
      ;;
  esac
done

echo "========================================="
echo "Hierarchical Session Test Setup"
echo "========================================="
echo "Mitto Dir: $MITTO_DIR"
echo "Parent: $PARENT_NAME"
echo "Children: $CHILDREN"
echo ""

# Step 1: Build the helper tool
echo "Step 1: Building helper tool..."
cd "$SCRIPT_DIR"
go build -o create-hierarchical-sessions create-hierarchical-sessions.go
echo "✓ Helper tool built"
echo ""

# Step 2: Create hierarchical sessions
echo "Step 2: Creating hierarchical sessions..."
./create-hierarchical-sessions \
  -dir "$MITTO_DIR" \
  -parent "$PARENT_NAME" \
  -children "$CHILDREN"
echo ""

# Step 3: Verify the output file
OUTPUT_FILE="$MITTO_DIR/hierarchical-sessions.json"
if [ -f "$OUTPUT_FILE" ]; then
  echo "✓ Hierarchical sessions created successfully"
  echo ""
  echo "Session details:"
  cat "$OUTPUT_FILE" | jq '.'
  echo ""
else
  echo "✗ Failed to create hierarchical sessions"
  exit 1
fi

# Step 4: Run tests if requested
if [ "$RUN_TESTS" = true ]; then
  echo "Step 3: Running Playwright tests..."
  cd "$PROJECT_ROOT/tests/ui"
  
  if [ "$HEADED" = true ]; then
    npx playwright test hierarchical-sessions --headed
  else
    npx playwright test hierarchical-sessions
  fi
  
  echo ""
  echo "✓ Tests completed"
  echo ""
  echo "Screenshots saved to: $PROJECT_ROOT/tests/ui/test-results/"
else
  echo "========================================="
  echo "Setup complete!"
  echo "========================================="
  echo ""
  echo "To run the tests manually:"
  echo "  cd $PROJECT_ROOT/tests/ui"
  echo "  npx playwright test hierarchical-sessions"
  echo ""
  echo "To run in headed mode:"
  echo "  npx playwright test hierarchical-sessions --headed"
  echo ""
  echo "To run only the advanced test:"
  echo "  npx playwright test hierarchical-sessions --grep 'real parent-child'"
fi

