#!/usr/bin/env bash
# tests/manual/test-restricted-runner-smoke-assert.sh
#
# Unit test for tests/manual/restricted-runner-smoke.sh. Exercises the
# script's shell-level contracts without requiring mitto, mock-acp, or
# a real sandbox:
#
# A. assert_runner_log_line (extracted function):
#   1. positive match (fallback=false)  → exit 0, "Found expected" msg
#   2. Linux + firejail + fallback=true → exit non-zero, hard failure
#   3. macOS + sandbox-exec + fallback=true → exit 0, soft warning
#   4. no runner log line               → exit non-zero, hard failure
#
# B. SMOKE_SCRATCH_DIR guard block (mitto-6yi.4):
#   5. unset → falls back to mktemp -d -t mitto-runner-smoke.* + cleanup trap
#   6. set to pre-existing dir → uses it verbatim, dir survives (CI owns cleanup)
#   7. set to not-yet-created nested dir → mkdir -p creates it, survives
#
# Runs cross-platform (only reads/execs shell fragments; no sandbox tooling
# required), so this can execute in CI on macOS or Linux.

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

# ---------------------------------------------------------------------------
# Section B — SMOKE_SCRATCH_DIR guard block (mitto-6yi.4)
#
# Extracts the "if [ -n \${SMOKE_SCRATCH_DIR:-} ]; then ... fi" block from
# the smoke script and exercises both branches in isolated subshells. The
# post-condition check (does the dir survive the subshell exit?) validates
# that the trap is registered ONLY in the default-mktemp branch — this is
# the exact contract CI relies on to preserve prompt.stderr.log for artifact
# upload.
# ---------------------------------------------------------------------------

SCRATCH_BLOCK="$(sed -n '/SMOKE_SCRATCH_DIR:-/,/^fi$/p' "$SMOKE")"
if ! printf '%s' "$SCRATCH_BLOCK" | grep -q 'mktemp -d -t mitto-runner-smoke'; then
    echo "❌ could not extract SCRATCH_DIR guard block from $SMOKE (test harness bug)" >&2
    exit 1
fi

scratch_pass_count=0
scratch_fail_count=0

# run_scratch_case ENV_MODE VALUE EXPECTED_PATTERN CASE_NAME
#   ENV_MODE: "set" or "unset" — whether to pass SMOKE_SCRATCH_DIR to the block
#   VALUE:    path to use when ENV_MODE=set (ignored when unset)
#   EXPECTED_PATTERN: extended-regex the resolved $SCRATCH_DIR must match
#   CASE_NAME: label for pass/fail reporting
run_scratch_case() {
    local env_mode="$1" value="$2" want_pattern="$3" name="$4"
    local output rc resolved

    if [ "$env_mode" = "set" ]; then
        output="$(SMOKE_SCRATCH_DIR="$value" bash -c "
            set -u
            $SCRATCH_BLOCK
            printf 'RESOLVED=%s\n' \"\$SCRATCH_DIR\"
        " 2>&1)"
        rc=$?
    else
        output="$(env -u SMOKE_SCRATCH_DIR bash -c "
            set -u
            $SCRATCH_BLOCK
            printf 'RESOLVED=%s\n' \"\$SCRATCH_DIR\"
        " 2>&1)"
        rc=$?
    fi

    resolved="$(printf '%s\n' "$output" | awk -F= '/^RESOLVED=/ { print $2 }')"

    local ok=1
    if [ "$rc" != "0" ]; then
        ok=0
        echo "❌ $name: block exited rc=$rc; output=$output"
    fi
    if [ -z "$resolved" ]; then
        ok=0
        echo "❌ $name: block did not print RESOLVED=<path>; output=$output"
    elif ! printf '%s' "$resolved" | grep -qE "$want_pattern"; then
        ok=0
        echo "❌ $name: SCRATCH_DIR=$resolved did not match /$want_pattern/"
    fi
    if [ "$ok" = "1" ]; then
        scratch_pass_count=$((scratch_pass_count + 1))
        echo "✅ $name (SCRATCH_DIR=$resolved)"
    else
        scratch_fail_count=$((scratch_fail_count + 1))
        return
    fi

    # Post-condition: whether the resolved dir survives the subshell exit.
    #   env_mode=set   → dir MUST survive (caller/CI owns cleanup, no trap).
    #   env_mode=unset → dir MUST be gone (trap cleanup EXIT fired).
    if [ "$env_mode" = "set" ]; then
        if [ ! -d "$resolved" ]; then
            scratch_fail_count=$((scratch_fail_count + 1))
            echo "❌ ${name}_survives: override dir cleaned up (should be caller-owned): $resolved"
        else
            scratch_pass_count=$((scratch_pass_count + 1))
            echo "✅ ${name}_survives: override dir survived subshell exit"
        fi
    else
        if [ -d "$resolved" ]; then
            scratch_fail_count=$((scratch_fail_count + 1))
            echo "❌ ${name}_cleaned: default mktemp dir not cleaned up: $resolved"
            rm -rf "$resolved"
        else
            scratch_pass_count=$((scratch_pass_count + 1))
            echo "✅ ${name}_cleaned: default mktemp dir cleaned up on exit"
        fi
    fi
}

# Case 5 — SMOKE_SCRATCH_DIR unset: falls back to mktemp mitto-runner-smoke.*
run_scratch_case "unset" "" "mitto-runner-smoke\." "scratch_default_mktemp"

# Case 6 — SMOKE_SCRATCH_DIR set to a pre-existing dir: uses it verbatim.
S6_DIR="$TMP/preexisting"
mkdir -p "$S6_DIR"
S6_PATTERN="^$(printf '%s' "$S6_DIR" | sed 's#[.[\/*^$]#\\&#g')$"
run_scratch_case "set" "$S6_DIR" "$S6_PATTERN" "scratch_override_preexisting"

# Case 7 — SMOKE_SCRATCH_DIR set to a not-yet-created nested dir: mkdir -p makes it.
S7_DIR="$TMP/needs-creating/nested"
S7_PATTERN="^$(printf '%s' "$S7_DIR" | sed 's#[.[\/*^$]#\\&#g')$"
run_scratch_case "set" "$S7_DIR" "$S7_PATTERN" "scratch_override_mkdir_p"

profile_pass_count=0
profile_fail_count=0

# Firejail 0.9.72 rejects the top-level path itself (`whitelist /tmp`). Keep
# this static guard beside the cross-platform shell tests; the Linux CI smoke
# provides the end-to-end validation against a real Firejail installation.
if grep -qE '"allow_(read|write)_folders":[[:space:]]*\["/tmp"' "$SMOKE"; then
    profile_fail_count=$((profile_fail_count + 1))
    echo "❌ firejail_profile_avoids_top_level_tmp: settings still whitelist /tmp"
else
    profile_pass_count=$((profile_pass_count + 1))
    echo "✅ firejail_profile_avoids_top_level_tmp"
fi

if grep -qF 'MOCK_ACP="$SCRATCH_DIR/mock-acp-server"' "$SMOKE" && \
        grep -qF 'cp -f "$MOCK_ACP_SOURCE" "$MOCK_ACP"' "$SMOKE"; then
    profile_pass_count=$((profile_pass_count + 1))
    echo "✅ firejail_fixture_lives_in_whitelisted_workspace"
else
    profile_fail_count=$((profile_fail_count + 1))
    echo "❌ firejail_fixture_lives_in_whitelisted_workspace: mock ACP is not copied into scratch"
fi

echo
echo "assert_runner_log_line: $pass_count passed, $fail_count failed"
echo "SMOKE_SCRATCH_DIR guard:  $scratch_pass_count passed, $scratch_fail_count failed"
echo "Firejail profile guard:    $profile_pass_count passed, $profile_fail_count failed"

total_fail=$((fail_count + scratch_fail_count + profile_fail_count))
if [ "$total_fail" -gt 0 ]; then
    exit 1
fi
exit 0
