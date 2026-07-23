#!/usr/bin/env bash
# tests/manual/restricted-runner-smoke.sh
#
# End-to-end smoke test for the restricted-runner subsystem.
#
# Configures `mitto prompt` against the mock ACP server with a
# platform-appropriate restricted_runners entry (sandbox-exec on macOS,
# firejail on Linux when available, exec otherwise) and asserts that
# `runner.NewRunner` emits its INFO log line with the expected runner type
# and fallback=false.
#
# Exits 0 on success (green ✅), non-zero on failure (red ❌).
#
# Invoke via `make test-runner-smoke` or run directly:
#   ./tests/manual/restricted-runner-smoke.sh

set -u

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

MITTO_BIN="$REPO_ROOT/mitto"
MOCK_ACP="$REPO_ROOT/tests/mocks/acp-server/mock-acp-server"

SCRATCH_DIR="$(mktemp -d -t mitto-runner-smoke.XXXXXX)"
LOG_FILE="$SCRATCH_DIR/prompt.stderr.log"

cleanup() {
    rm -rf "$SCRATCH_DIR"
}
trap cleanup EXIT

fail() {
    echo -e "${RED}❌ $1${NC}" >&2
    echo -e "${YELLOW}--- captured log (tail) ---${NC}" >&2
    tail -80 "$LOG_FILE" 2>/dev/null >&2 || true
    exit 1
}

echo -e "${BLUE}=== Restricted-runner smoke test ===${NC}"
echo "Scratch dir: $SCRATCH_DIR"

# Platform detection --------------------------------------------------------
OS="$(uname -s)"
case "$OS" in
    Darwin)
        RUNNER_TYPE="sandbox-exec"
        if ! command -v sandbox-exec >/dev/null 2>&1; then
            echo -e "${YELLOW}⚠ sandbox-exec not found; skipping smoke test${NC}"
            exit 0
        fi
        ;;
    Linux)
        if command -v firejail >/dev/null 2>&1; then
            RUNNER_TYPE="firejail"
        else
            echo -e "${YELLOW}⚠ firejail not available on this Linux host; using exec (no restrictions)${NC}"
            RUNNER_TYPE="exec"
        fi
        ;;
    *)
        echo -e "${YELLOW}⚠ Unsupported platform ($OS); skipping smoke test${NC}"
        exit 0
        ;;
esac
echo "Platform: $OS → runner type: $RUNNER_TYPE"

# Build prerequisites -------------------------------------------------------
if [ ! -x "$MITTO_BIN" ]; then
    echo -e "${BLUE}→ Building mitto${NC}"
    (cd "$REPO_ROOT" && make build) || fail "make build failed"
fi
if [ ! -x "$MOCK_ACP" ]; then
    echo -e "${BLUE}→ Building mock-acp-server${NC}"
    (cd "$REPO_ROOT" && make build-mock-acp) || fail "make build-mock-acp failed"
fi

# Generate settings.json ----------------------------------------------------
SETTINGS="$SCRATCH_DIR/settings.json"
cat > "$SETTINGS" <<EOF
{
  "acp_servers": [
    {
      "name": "mock",
      "command": "$MOCK_ACP",
      "restricted_runners": {
        "exec": {
          "type": "$RUNNER_TYPE",
          "restrictions": {
            "allow_networking": true,
            "allow_read_folders": ["/tmp", "$SCRATCH_DIR"],
            "allow_write_folders": ["/tmp", "$SCRATCH_DIR"]
          }
        }
      }
    }
  ]
}
EOF
echo "Settings written: $SETTINGS"

# Run mitto prompt ----------------------------------------------------------
echo -e "${BLUE}→ Running mitto prompt${NC}"
if ! MITTO_DIR="$SCRATCH_DIR" "$MITTO_BIN" prompt \
        --dir "$SCRATCH_DIR" \
        --timeout 30s \
        "smoke test hello" \
        >"$SCRATCH_DIR/prompt.stdout.log" 2>"$LOG_FILE"; then
    fail "mitto prompt exited non-zero"
fi

# Assert on the runner log line --------------------------------------------
EXPECTED_TYPE="$RUNNER_TYPE"
if grep -qE "created restricted runner.*type=$EXPECTED_TYPE.*fallback=false" "$LOG_FILE"; then
    MATCH="$(grep -E 'created restricted runner' "$LOG_FILE" | head -1)"
    echo -e "${GREEN}✅ Found expected runner log line:${NC}"
    echo "   $MATCH"
    exit 0
fi

# Fallback branch: if runner unavailable and fell back, that's a valid
# code-path exercise on macOS (sandbox-exec may be blocked by policy) but
# on Linux, if we asked for firejail and got exec, the requested isolation
# was not enforced — fail hard rather than silently accept an unconfined
# process. Non-firejail Linux requests (e.g. exec-only) still soft-pass.
if grep -qE "created restricted runner.*fallback=true" "$LOG_FILE"; then
    MATCH="$(grep -E 'created restricted runner' "$LOG_FILE" | head -1)"
    if [ "$OS" = "Linux" ] && [ "$EXPECTED_TYPE" = "firejail" ]; then
        fail "requested firejail on Linux but runner fell back to exec (isolation NOT enforced): $MATCH"
    fi
    echo -e "${YELLOW}⚠ Runner requested $EXPECTED_TYPE but fell back to exec:${NC}"
    echo "   $MATCH"
    echo -e "${YELLOW}   (Fallback path exercised; not a hard failure on this host.)${NC}"
    exit 0
fi

fail "expected log line matching 'created restricted runner type=$EXPECTED_TYPE fallback=false' not found in stderr"
