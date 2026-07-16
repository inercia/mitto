#!/bin/bash
# test_bd_attach.sh — Integration tests for scripts/bd-attach.sh.
#
# Creates a throwaway `bd` issue (ephemeral, so it never lands in exported
# JSONL), exercises every sub-command (add/list/remove/clear) against it,
# verifies the resulting metadata via raw `bd show --json`, and closes/deletes
# the throwaway issue on exit — pass or fail.
#
# Requires: bd, jq. Skips gracefully if either is missing.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BD_ATTACH="$REPO_ROOT/scripts/bd-attach.sh"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
TESTS_RUN=0 TESTS_PASSED=0 TESTS_FAILED=0

pass() { echo -e "${GREEN}[PASS]${NC} $1"; TESTS_PASSED=$((TESTS_PASSED+1)); }
fail() { echo -e "${RED}[FAIL]${NC} $1"; TESTS_FAILED=$((TESTS_FAILED+1)); }
info() { echo -e "[INFO] $1"; }

check() {
  TESTS_RUN=$((TESTS_RUN+1))
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then pass "$name"; else fail "$name"; fi
}

# Prereqs
for cmd in bd jq; do
  command -v "$cmd" >/dev/null 2>&1 || { echo -e "${YELLOW}[SKIP]${NC} '$cmd' not installed"; exit 0; }
done
[[ -x "$BD_ATTACH" ]] || { echo -e "${RED}[FAIL]${NC} $BD_ATTACH not executable"; exit 1; }

# Create throwaway issue (ephemeral so it's not exported to JSONL).
info "Creating throwaway bead for tests..."
CREATE_JSON=$(bd create "bd-attach test fixture" --type task --ephemeral --json 2>/dev/null)
TEST_ID=$(jq -r '.id' <<<"$CREATE_JSON")
[[ -n "$TEST_ID" && "$TEST_ID" != "null" ]] || { echo "failed to create test bead"; exit 1; }
info "Test bead: $TEST_ID"

cleanup() {
  info "Cleaning up test bead $TEST_ID..."
  bd delete "$TEST_ID" --force >/dev/null 2>&1 \
    || bd close "$TEST_ID" --reason "test cleanup" >/dev/null 2>&1 \
    || true
}
trap cleanup EXIT

# --- helpers ---------------------------------------------------------------
attachments_json() { bd show "$TEST_ID" --json | jq -c '.[0].metadata.attachments // []'; }
attachments_len()  { attachments_json | jq 'length'; }
path_at()          { attachments_json | jq -r ".[$1].path"; }

# --- baseline --------------------------------------------------------------
info "T1: list on issue with no attachments"
out=$("$BD_ATTACH" list "$TEST_ID" 2>&1)
check "list reports empty" grep -q "no attachments on $TEST_ID" <<<"$out"

# --- add -------------------------------------------------------------------
info "T2: add first attachment (path only)"
"$BD_ATTACH" add "$TEST_ID" scripts/bd-attach.sh >/dev/null
check "attachments length == 1"       test "$(attachments_len)" = "1"
check "first path stored verbatim"    test "$(path_at 0)" = "scripts/bd-attach.sh"
check "metadata key IS structured (array, not string)" \
  bash -c "bd show $TEST_ID --json | jq -e '.[0].metadata.attachments | type == \"array\"'"

info "T3: add second attachment with --name and --note"
"$BD_ATTACH" add "$TEST_ID" .augment/rules/44-beads-attachments.md --name "convention doc" --note "the schema" >/dev/null
check "attachments length == 2"       test "$(attachments_len)" = "2"
check "second entry has name"         bash -c "test \"$(attachments_json | jq -r '.[1].name')\" = 'convention doc'"
check "second entry has note"         bash -c "test \"$(attachments_json | jq -r '.[1].note')\" = 'the schema'"

info "T4: add records path even when file is missing on disk (warns to stderr)"
warn_out=$("$BD_ATTACH" add "$TEST_ID" no/such/path.txt --note "missing" 2>&1 >/dev/null)
check "missing-file warning emitted"  grep -q "does not exist on disk" <<<"$warn_out"
check "missing entry still recorded"  test "$(attachments_len)" = "3"

# --- list ------------------------------------------------------------------
info "T5: list output marks missing files with ✗ and present ones with ✓"
list_out=$("$BD_ATTACH" list "$TEST_ID")
check "list shows count line"         grep -q "3 attachment(s)" <<<"$list_out"
check "list marks present with check" grep -q "✓ scripts/bd-attach.sh" <<<"$list_out"
check "list marks missing with cross" grep -q "✗ no/such/path.txt" <<<"$list_out"
check "list renders --name in brackets" grep -q "\[convention doc\]" <<<"$list_out"

# --- remove ----------------------------------------------------------------
info "T6: remove one entry by path"
"$BD_ATTACH" remove "$TEST_ID" no/such/path.txt >/dev/null
check "length dropped to 2"           test "$(attachments_len)" = "2"
check "missing entry gone"            bash -c "! (attachments_json | jq -e 'any(.path == \"no/such/path.txt\")' >/dev/null)"

info "T7: remove of non-existent path errors and does not modify state"
set +e; "$BD_ATTACH" remove "$TEST_ID" not-attached.txt >/dev/null 2>&1; rc=$?; set -e
check "remove of missing path exits non-zero" test "$rc" -ne 0
check "length unchanged after failed remove"  test "$(attachments_len)" = "2"

# --- metadata coexistence -------------------------------------------------
info "T8: unrelated metadata keys are preserved through add/remove/clear"
echo '{"team":"platform","priority_note":"keep-me"}' > /tmp/bd-attach-meta-$$.json
bd update "$TEST_ID" --metadata "@/tmp/bd-attach-meta-$$.json" >/dev/null
rm -f /tmp/bd-attach-meta-$$.json
"$BD_ATTACH" add "$TEST_ID" some/other/path >/dev/null
check "team key still present after add"     bash -c "test \"\$(bd show '$TEST_ID' --json | jq -r '.[0].metadata.team')\" = 'platform'"
"$BD_ATTACH" remove "$TEST_ID" some/other/path >/dev/null
check "team key still present after remove"  bash -c "test \"\$(bd show '$TEST_ID' --json | jq -r '.[0].metadata.team')\" = 'platform'"

# --- clear -----------------------------------------------------------------
info "T9: clear removes only the attachments key, not sibling metadata"
"$BD_ATTACH" clear "$TEST_ID" >/dev/null
check "attachments key removed entirely"     bash -c "bd show $TEST_ID --json | jq -e '.[0].metadata.attachments == null'"
check "clear reports empty on subsequent list" bash -c "\"$BD_ATTACH\" list $TEST_ID | grep -q 'no attachments on'"
check "team key survived clear"              bash -c "test \"\$(bd show '$TEST_ID' --json | jq -r '.[0].metadata.team')\" = 'platform'"

# --- CLI surface -----------------------------------------------------------
info "T10: help/unknown subcommand exits non-zero with usage"
set +e; "$BD_ATTACH" bogus-cmd 2>/dev/null; rc=$?; set -e
check "unknown subcommand exits non-zero" test "$rc" -ne 0
set +e; "$BD_ATTACH" 2>/dev/null; rc=$?; set -e
check "no-args exits non-zero"            test "$rc" -ne 0

# --- summary ---------------------------------------------------------------
echo
echo "========================================"
echo "bd-attach.sh test summary"
echo "========================================"
echo "Tests run:    $TESTS_RUN"
echo -e "Tests passed: ${GREEN}$TESTS_PASSED${NC}"
echo -e "Tests failed: ${RED}$TESTS_FAILED${NC}"
echo

[[ $TESTS_FAILED -gt 0 ]] && exit 1 || exit 0
