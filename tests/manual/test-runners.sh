#!/bin/bash
# Runner System Manual Test Script
# This script helps verify the runner system functionality

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test workspace directory
TEST_DIR="/tmp/mitto-runner-tests"
WORKSPACE_EXEC="$TEST_DIR/workspace-exec"
WORKSPACE_SANDBOX="$TEST_DIR/workspace-sandbox"
WORKSPACE_FIREJAIL="$TEST_DIR/workspace-firejail"
WORKSPACE_DOCKER="$TEST_DIR/workspace-docker"

echo -e "${BLUE}=== Mitto Runner System Test Setup ===${NC}\n"

# Function to print section headers
print_section() {
    echo -e "\n${BLUE}=== $1 ===${NC}"
}

# Function to print test results
print_result() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓ $2${NC}"
    else
        echo -e "${RED}✗ $2${NC}"
    fi
}

# Clean up previous test directories
print_section "Cleaning up previous test directories"
rm -rf "$TEST_DIR"
mkdir -p "$TEST_DIR"
echo -e "${GREEN}✓ Created test directory: $TEST_DIR${NC}"

# Create test workspaces
print_section "Creating test workspaces"

# Workspace 1: exec runner
mkdir -p "$WORKSPACE_EXEC"
cat > "$WORKSPACE_EXEC/.mittorc" <<EOF
# Test workspace with exec runner (no restrictions)
restricted_runners:
  exec:
    restrictions:
      allow_networking: true
    merge_strategy: "extend"
EOF
echo -e "${GREEN}✓ Created workspace: $WORKSPACE_EXEC${NC}"
echo "  Runner: exec (no restrictions)"

# Workspace 2: sandbox-exec runner
mkdir -p "$WORKSPACE_SANDBOX"
cat > "$WORKSPACE_SANDBOX/.mittorc" <<EOF
# Test workspace with sandbox-exec runner (macOS sandboxing)
restricted_runners:
  sandbox-exec:
    restrictions:
      allow_networking: true
      allow_read_folders:
        - "\$WORKSPACE"
        - "\$HOME/.config"
        - "/usr/local/bin"
        - "/usr/bin"
        - "/bin"
      allow_write_folders:
        - "\$WORKSPACE"
        - "/tmp"
      deny_folders:
        - "\$HOME/.ssh"
    merge_strategy: "extend"
EOF
echo -e "${GREEN}✓ Created workspace: $WORKSPACE_SANDBOX${NC}"
echo "  Runner: sandbox-exec (macOS sandboxing)"

# Workspace 3: firejail runner (will fallback on macOS)
mkdir -p "$WORKSPACE_FIREJAIL"
cat > "$WORKSPACE_FIREJAIL/.mittorc" <<EOF
# Test workspace with firejail runner (Linux only - will fallback on macOS)
restricted_runners:
  firejail:
    restrictions:
      allow_networking: true
      allow_read_folders:
        - "\$WORKSPACE"
      allow_write_folders:
        - "\$WORKSPACE"
    merge_strategy: "extend"
EOF
echo -e "${GREEN}✓ Created workspace: $WORKSPACE_FIREJAIL${NC}"
echo "  Runner: firejail (will fallback to exec on macOS)"

# Workspace 4: docker runner
mkdir -p "$WORKSPACE_DOCKER"
cat > "$WORKSPACE_DOCKER/.mittorc" <<EOF
# Test workspace with docker runner (container isolation)
restricted_runners:
  docker:
    restrictions:
      allow_networking: true
      docker:
        image: "alpine:latest"
        memory_limit: "512m"
        cpu_limit: "1.0"
      allow_read_folders:
        - "\$WORKSPACE"
      allow_write_folders:
        - "\$WORKSPACE"
    merge_strategy: "replace"
EOF
echo -e "${GREEN}✓ Created workspace: $WORKSPACE_DOCKER${NC}"
echo "  Runner: docker (container isolation)"

# Check system capabilities
print_section "Checking system capabilities"

# Check platform
PLATFORM=$(uname -s)
echo "Platform: $PLATFORM"

# Check sandbox-exec
if command -v sandbox-exec &> /dev/null; then
    print_result 0 "sandbox-exec available"
else
    print_result 1 "sandbox-exec not available"
fi

# Check firejail
if command -v firejail &> /dev/null; then
    print_result 0 "firejail available"
else
    print_result 1 "firejail not available (expected on macOS)"
fi

# Check docker
if command -v docker &> /dev/null; then
    print_result 0 "docker command available"
    if docker ps &> /dev/null; then
        print_result 0 "docker daemon running"
    else
        print_result 1 "docker daemon not running"
        echo -e "${YELLOW}  → Start Docker to test docker runner${NC}"
    fi
else
    print_result 1 "docker not available"
fi

# Print next steps
print_section "Next Steps"
echo ""
echo "Test workspaces created in: $TEST_DIR"
echo ""
echo "To test manually:"
echo "  1. Start mitto web server:"
echo "     ${YELLOW}mitto web --config tests/manual/test-config.yaml${NC}"
echo ""
echo "  2. Open http://localhost:8080 in browser"
echo ""
echo "  3. Create sessions with different workspaces:"
echo "     - $WORKSPACE_EXEC (exec runner)"
echo "     - $WORKSPACE_SANDBOX (sandbox-exec runner)"
echo "     - $WORKSPACE_FIREJAIL (firejail fallback test)"
echo "     - $WORKSPACE_DOCKER (docker runner)"
echo ""
echo "  4. Verify:"
echo "     - Session header shows correct runner badge"
echo "     - Toast notifications for fallbacks"
echo "     - Runner restrictions are enforced"
echo ""
echo "See tests/manual/runner-test-plan.md for detailed test scenarios"
echo ""

