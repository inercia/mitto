#!/usr/bin/env bash
# tests/manual/test-restricted-runner-smoke-assert.sh
#
# Unit test for the assert_runner_log_line function extracted from
# tests/manual/restricted-runner-smoke.sh. Exercises the three outcomes
# without requiring mitto, mock-acp, or a real sandbox:
#   1. positive match (fallback=false)  → exit 0, "Found expected" msg
#   2. Linux + firejail + fallback=true → exit non-zero, hard failure
#   3. macOS + sandbox-exec + fallback=true → exit 0, soft warning
#   4. no runner log line               → exit non-zero, hard failure
#
# Runs cross-platform (only reads a synthetic $LOG_FILE), so this can
# execute in CI on macOS or Linux without any sandbox tooling.

set -u

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SMOKE="$REPO_ROOT/tests/manual/restricted-runner-smoke.sh"

if [ ! -f "$SMOKE" ]; then
    echo "❌ smoke script not found at $SMOKE" >&2
    exit 1
fi

TMP="$(mktemp -d -t smoke-assert.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

pass_count=0
fail_count=0

# run_case OS EXPECTED_TYPE LOG_CONTENT EXPECTED_EXIT EXPECTED_SUBSTRING CASE_NAME
run_case() {
    local os="$1" etype="$2" content="$3" want_exit="$4" want_sub="$5" name="$6"
    local logfile="$TMP/log.$$.$name"
    printf '%s' "$content" > "$logfile"

    # Sub-shell that stubs the color vars and fail(), sources the smoke
    # script far enough to define assert_runner_log_line, then invokes it.
    local output
    local rc
    output="$(
        OS="$os" \
        EXPECTED_TYPE="$etype" \
        LOG_FILE="$logfile" \
        bash -c "
            RED=''; GREEN=''; YELLOW=''; BLUE=''; NC=''
            fail() { echo \"FAIL: \$1\" >&2; exit 1; }
            # Source only the function definition (extract lines between
            # 'assert_runner_log_line() {' and its matching closing brace).
            eval \"\$(awk '/^assert_runner_log_line\\(\\) \\{/,/^\\}\$/' '$SMOKE')\"
            assert_runner_log_line
        " 2>&1
    )"
    rc=$?

    local ok=1
    if [ "$rc" != "$want_exit" ]; then
        ok=0
        echo "❌ $name: exit rc=$rc want=$want_exit"
    fi
    if ! printf '%s' "$output" | grep -qF "$want_sub"; then
        ok=0
        echo "❌ $name: output missing expected substring: $want_sub"
        echo "   captured: $output"
    fi
    if [ "$ok" = "1" ]; then
        pass_count=$((pass_count + 1))
        echo "✅ $name"
    else
        fail_count=$((fail_count + 1))
    fi
}

# Case 1 — positive match: fallback=false, expected type present → exit 0.
run_case "Linux" "firejail" \
    'INFO created restricted runner type=firejail fallback=false session=abc' \
    0 "Found expected runner log line" "positive_match_firejail"

run_case "Darwin" "sandbox-exec" \
    'INFO created restricted runner type=sandbox-exec fallback=false' \
    0 "Found expected runner log line" "positive_match_sandbox_exec"

# Case 2 — Linux + firejail requested + fallback=true → HARD FAIL (this is
# the exact contract added for mitto-6yi.3 Tier 4 sub-part B).
run_case "Linux" "firejail" \
    'INFO created restricted runner type=exec fallback=true reason=firejail-unavailable' \
    1 "requested firejail on Linux but runner fell back to exec" "linux_firejail_fallback_hard_fails"

# Case 3 — macOS + sandbox-exec requested + fallback=true → soft-pass
# (existing behaviour: sandbox-exec may be blocked by policy, warn only).
run_case "Darwin" "sandbox-exec" \
    'INFO created restricted runner type=exec fallback=true' \
    0 "fell back to exec" "darwin_sandbox_exec_fallback_soft_passes"

# Case 4 — no runner log line at all → hard failure.
run_case "Linux" "firejail" \
    'INFO some unrelated log line\nWARN another one\n' \
    1 "expected log line matching" "missing_runner_log_hard_fails"

echo
echo "Summary: $pass_count passed, $fail_count failed"
if [ "$fail_count" -gt 0 ]; then
    exit 1
fi
exit 0
