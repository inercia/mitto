#!/usr/bin/env bash
# tests/manual/bun-tooling-smoke.sh
#
# Regression guard for mitto-txpp.4 — the "switch package manager to Bun"
# feature. Pins the acceptance criteria as file-level invariants so a
# future edit cannot silently reintroduce npm without failing this test.
#
# Assertions (each maps 1:1 to a close-criterion on mitto-txpp.4):
#
#   T1  bun.lock exists at repo root, is committed to git, and is
#       non-empty.
#   T2  package-lock.json does NOT exist at repo root.
#   T3  .gitignore blocks a root package-lock.json (so an accidental
#       `npm install` does not sneak a divergent lockfile back in).
#   T4  Makefile:
#         - NPM=bun (not NPM=npm, not NPM=bun run — the plan/impl deviation)
#         - deps-js target uses `bun install --frozen-lockfile`
#         - no bare `npm ci` / `npm install`
#         - no `npx` anywhere in Makefile (mitto-3vb: Playwright carve-out lifted)
#   T5  package.json scripts:
#         - no `npx @tailwindcss` (must be `bunx @tailwindcss`)
#         - no `npm run` (must be `bun run` in lint:frontend)
#         - no `npx htmlhint`
#         - no `npx` anywhere in package.json scripts (mitto-3vb: Playwright
#           carve-out lifted — test:ui* now uses `bunx playwright`)
#   T6  scripts/lint-html.sh uses `bunx htmlhint`, not `npx htmlhint`.
#   T7  .github/workflows/tests.yml:
#         - every job that installs JS deps uses oven-sh/setup-bun@v2
#         - no `npm ci` (Playwright cache carve-out uses bun install)
#         - Playwright cache key hashes bun.lock (not package-lock.json)
#         - no `hashFiles('package-lock.json')` anywhere
#         - no `actions/setup-node@` step (mitto-3vb: bunx replaces npx)
#         - no `npx playwright` invocation (mitto-3vb: bunx everywhere)
#   T8  README.md documents the Bun contributor requirement.
#
# Exits 0 on success (green ✅), 1 on any failure (red ❌).
# Runs cross-platform; only reads files, does not invoke bun/npm.
#
# Invoke via `make test-bun-tooling` or run directly:
#   ./tests/manual/bun-tooling-smoke.sh

set -u

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

pass_count=0
fail_count=0

pass() { pass_count=$((pass_count + 1)); printf "${GREEN}✅${NC} %s\n" "$1"; }
fail() { fail_count=$((fail_count + 1)); printf "${RED}❌${NC} %s\n" "$1"; }

# -----------------------------------------------------------------------------
# T1 — bun.lock exists, non-empty, tracked by git
# -----------------------------------------------------------------------------
if [ ! -f bun.lock ]; then
    fail "T1: bun.lock missing at repo root"
elif [ ! -s bun.lock ]; then
    fail "T1: bun.lock exists but is empty"
elif ! git ls-files --error-unmatch bun.lock >/dev/null 2>&1; then
    fail "T1: bun.lock is not tracked by git (must be committed)"
else
    pass "T1: bun.lock is present, non-empty, and git-tracked"
fi

# -----------------------------------------------------------------------------
# T2 — package-lock.json is gone
# -----------------------------------------------------------------------------
if [ -f package-lock.json ]; then
    fail "T2: package-lock.json still exists at repo root (must be deleted)"
elif git ls-files --error-unmatch package-lock.json >/dev/null 2>&1; then
    fail "T2: package-lock.json is still tracked by git"
else
    pass "T2: package-lock.json is deleted and untracked"
fi

# -----------------------------------------------------------------------------
# T3 — .gitignore blocks root package-lock.json
# -----------------------------------------------------------------------------
if [ ! -f .gitignore ]; then
    fail "T3: .gitignore missing"
elif ! grep -qE '^/?package-lock\.json$' .gitignore; then
    fail "T3: .gitignore does not block root package-lock.json"
else
    pass "T3: .gitignore blocks root package-lock.json"
fi

# -----------------------------------------------------------------------------
# T4 — Makefile invariants
# -----------------------------------------------------------------------------
if [ ! -f Makefile ]; then
    fail "T4: Makefile missing"
else
    # NPM variable must be exactly `NPM=bun` (bare bun; the existing call
    # sites are $(NPM) run <script>, so a trailing ` run` would produce
    # `bun run run <script>`).
    if ! grep -qE '^NPM[[:space:]]*=[[:space:]]*bun[[:space:]]*$' Makefile; then
        fail "T4a: Makefile must declare 'NPM=bun' (see mitto-txpp.4 impl deviation)"
    else
        pass "T4a: Makefile declares NPM=bun"
    fi

    # deps-js must use bun install --frozen-lockfile
    if ! grep -qE 'bun install --frozen-lockfile' Makefile; then
        fail "T4b: Makefile deps-js must use 'bun install --frozen-lockfile'"
    else
        pass "T4b: Makefile uses 'bun install --frozen-lockfile'"
    fi

    # No bare npm ci / npm install as a Makefile command line. Strip any
    # trailing comment segment (from '#' to end-of-line) before probing so
    # documentation strings that mention "npm ci/install" don't false-match.
    npm_hits="$(sed 's/#.*$//' Makefile | grep -nE '\bnpm[[:space:]]+(ci|install)\b' || true)"
    if [ -n "$npm_hits" ]; then
        fail "T4c: Makefile still contains npm ci/install as a command: $npm_hits"
    else
        pass "T4c: Makefile has no npm ci/install command"
    fi

    # No `npx` anywhere in Makefile (mitto-3vb: Playwright carve-out lifted —
    # test-ui / test-setup / test-ui-report etc. now use `bunx playwright`).
    # Strip Makefile comments first so prose mentioning npx doesn't false-match.
    npx_hits="$(sed 's/#.*$//' Makefile | grep -nE '\bnpx\b' || true)"
    if [ -n "$npx_hits" ]; then
        fail "T4d: Makefile still contains npx invocations: $npx_hits"
    else
        pass "T4d: Makefile has no npx invocations"
    fi
fi

# -----------------------------------------------------------------------------
# T5 — package.json script invariants
# -----------------------------------------------------------------------------
if [ ! -f package.json ]; then
    fail "T5: package.json missing"
else
    # No npx @tailwindcss (must be bunx @tailwindcss)
    if grep -qE 'npx[[:space:]]+@tailwindcss' package.json; then
        fail "T5a: package.json still uses 'npx @tailwindcss' (must be bunx)"
    else
        pass "T5a: package.json tailwind scripts use bunx"
    fi

    # No npm run (must be bun run in lint:frontend)
    if grep -qE 'npm[[:space:]]+run' package.json; then
        fail "T5b: package.json still uses 'npm run' (must be 'bun run')"
    else
        pass "T5b: package.json uses 'bun run' for script chaining"
    fi

    # No npx htmlhint in package.json (it belongs to scripts/lint-html.sh but
    # if someone inlines it here it should also be bunx).
    if grep -qE 'npx[[:space:]]+htmlhint' package.json; then
        fail "T5c: package.json contains 'npx htmlhint' (must be bunx)"
    else
        pass "T5c: package.json has no npx htmlhint"
    fi

    # No `npx` anywhere in package.json (mitto-3vb: Playwright carve-out
    # lifted — test:ui* scripts now use `bunx playwright`).
    pkg_npx="$(grep -nE '\bnpx\b' package.json || true)"
    if [ -n "$pkg_npx" ]; then
        fail "T5d: package.json still contains npx: $pkg_npx"
    else
        pass "T5d: package.json has no npx invocations"
    fi
fi

# -----------------------------------------------------------------------------
# T6 — scripts/lint-html.sh uses bunx htmlhint
# -----------------------------------------------------------------------------
if [ ! -f scripts/lint-html.sh ]; then
    fail "T6: scripts/lint-html.sh missing"
elif grep -qE 'npx[[:space:]]+htmlhint' scripts/lint-html.sh; then
    fail "T6: scripts/lint-html.sh still uses 'npx htmlhint'"
elif ! grep -qE 'bunx[[:space:]]+htmlhint' scripts/lint-html.sh; then
    fail "T6: scripts/lint-html.sh does not use 'bunx htmlhint'"
else
    pass "T6: scripts/lint-html.sh uses 'bunx htmlhint'"
fi

# -----------------------------------------------------------------------------
# T7 — .github/workflows/tests.yml invariants
# -----------------------------------------------------------------------------
WF=.github/workflows/tests.yml
if [ ! -f "$WF" ]; then
    fail "T7: $WF missing"
else
    # No `npm ci` anywhere (JS deps go through bun install --frozen-lockfile)
    if grep -qE '\bnpm[[:space:]]+ci\b' "$WF"; then
        fail "T7a: $WF still contains 'npm ci'"
    else
        pass "T7a: $WF has no 'npm ci'"
    fi

    # setup-bun must be present
    if ! grep -qE 'oven-sh/setup-bun@v2' "$WF"; then
        fail "T7b: $WF must use 'oven-sh/setup-bun@v2'"
    else
        pass "T7b: $WF uses 'oven-sh/setup-bun@v2'"
    fi

    # Playwright cache key must hash bun.lock, not package-lock.json
    if grep -qE "hashFiles\('package-lock.json'\)" "$WF"; then
        fail "T7c: $WF still hashes 'package-lock.json' (must be 'bun.lock')"
    else
        pass "T7c: $WF does not hash package-lock.json"
    fi

    if ! grep -qE "hashFiles\('bun\.lock'\)" "$WF"; then
        fail "T7d: $WF Playwright cache key must hash 'bun.lock'"
    else
        pass "T7d: $WF Playwright cache key hashes 'bun.lock'"
    fi

    # At least one `bun install --frozen-lockfile` step must exist
    if ! grep -qE 'bun install --frozen-lockfile' "$WF"; then
        fail "T7e: $WF must run 'bun install --frozen-lockfile'"
    else
        pass "T7e: $WF runs 'bun install --frozen-lockfile'"
    fi

    # mitto-3vb: no `actions/setup-node@` step anywhere in the workflow —
    # Playwright now launches via bunx, so a Node.js toolchain install would be
    # dead weight and reintroducing it silently would mask a regression on the
    # bunx path. Strip YAML `#` comments before probing so reminder prose that
    # mentions setup-node doesn't false-match.
    setup_node_hits="$(sed 's/#.*$//' "$WF" | grep -nE 'actions/setup-node@' || true)"
    if [ -n "$setup_node_hits" ]; then
        fail "T7f: $WF still references actions/setup-node: $setup_node_hits"
    else
        pass "T7f: $WF has no actions/setup-node step (mitto-3vb)"
    fi

    # mitto-3vb: no `npx playwright` in the workflow — all Playwright
    # invocations must go through bunx. Strip YAML `#` comments first so
    # reminder prose that mentions npx doesn't false-match.
    wf_npx_playwright="$(sed 's/#.*$//' "$WF" | grep -nE 'npx[[:space:]]+playwright\b' || true)"
    if [ -n "$wf_npx_playwright" ]; then
        fail "T7g: $WF still uses 'npx playwright': $wf_npx_playwright"
    else
        pass "T7g: $WF has no 'npx playwright' invocation (mitto-3vb)"
    fi
fi

# -----------------------------------------------------------------------------
# T8 — README.md documents the Bun requirement
# -----------------------------------------------------------------------------
if [ ! -f README.md ]; then
    fail "T8: README.md missing"
elif ! grep -qiE '\[?bun\]?.*\(https://bun\.sh' README.md && ! grep -qiE 'bun\.sh/install' README.md; then
    fail "T8: README.md does not mention Bun install (link to bun.sh)"
else
    pass "T8: README.md documents Bun contributor requirement"
fi

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
echo
echo "-----------------------------------------"
printf "Bun tooling smoke: ${GREEN}%d passed${NC}, " "$pass_count"
if [ "$fail_count" -gt 0 ]; then
    printf "${RED}%d failed${NC}\n" "$fail_count"
    exit 1
fi
printf "${GREEN}0 failed${NC}\n"
exit 0
