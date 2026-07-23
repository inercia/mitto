#!/usr/bin/env bash
# Install an MCP server for Claude Code
# Input: {"name": "...", "command": "...", "args": [...], "url": "...", "path": "...", "env": {...}, "headers": {...}}
# Output: {"success": bool, "message": "...", "name": "..."}
#
# Shells out to `claude mcp add` (which persists to ~/.claude.json for user
# scope and to .mcp.json / projects.<abs-workspace>.mcpServers for project /
# local scope). Historically this script hand-edited ~/.claude/settings.json,
# which Claude Code silently ignores for mcpServers — the install reported
# success but the server never showed up. See mitto-45b.

INPUT=$(cat)

NAME=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null)

if [ -z "$NAME" ]; then
    echo '{"success": false, "message": "name is required", "name": ""}'
    exit 1
fi

COMMAND=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('command',''))" 2>/dev/null)
URL=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('url',''))" 2>/dev/null)

if [ -z "$COMMAND" ] && [ -z "$URL" ]; then
    echo '{"success": false, "message": "either command or url is required", "name": ""}'
    exit 1
fi

# Check if claude is available
if ! command -v claude &>/dev/null; then
    echo "{\"success\": false, \"message\": \"claude CLI not found\", \"name\": \"$NAME\"}"
    exit 1
fi

SCOPE=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('scope',''))" 2>/dev/null)
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

# Map scope to `claude mcp add -s <scope>`. Claude's scopes are local (default),
# user, and project — same names we use, so no translation is needed. Project
# and local scopes need to run inside the workspace directory so claude anchors
# the config to the right folder.
SCOPE_ARGS=()
case "$SCOPE" in
    user)    SCOPE_ARGS+=("-s" "user") ;;
    project) SCOPE_ARGS+=("-s" "project") ;;
    local)   SCOPE_ARGS+=("-s" "local") ;;
    # empty = claude's default (local)
esac

# Read env pairs as NUL-delimited "KEY=VAL" strings and build repeated -e flags.
# Values may legitimately contain commas, equals signs, or colons, so we can't
# use naive IFS splitting.
ENV_FLAGS=()
while IFS= read -r -d '' kv; do
    ENV_FLAGS+=(-e "$kv")
done < <(echo "$INPUT" | python3 -c "
import sys, json
data = json.load(sys.stdin)
env = data.get('env') or {}
for k, v in env.items():
    sys.stdout.write(f'{k}={v}\0')
" 2>/dev/null)

# Same pattern for headers with -H and "KEY: VAL".
HEADER_FLAGS=()
while IFS= read -r -d '' kv; do
    HEADER_FLAGS+=(-H "$kv")
done < <(echo "$INPUT" | python3 -c "
import sys, json
data = json.load(sys.stdin)
headers = data.get('headers') or {}
for k, v in headers.items():
    sys.stdout.write(f'{k}: {v}\0')
" 2>/dev/null)

# `claude mcp add` has no --replace flag (unlike auggie), so remove any existing
# entry first for idempotency. Ignore failure — "not found" is expected on the
# common first-install path.
run_claude() {
    if [ -n "$WORKSPACE_PATH" ] && { [ "$SCOPE" = "project" ] || [ "$SCOPE" = "local" ]; }; then
        ( cd "$WORKSPACE_PATH" && claude "$@" )
    else
        claude "$@"
    fi
}

run_claude mcp remove "${SCOPE_ARGS[@]}" "$NAME" >/dev/null 2>&1 || true

# Read stdio args NUL-delimited so args containing spaces / equals / colons
# survive verbatim (unlike the augment script which space-joins them).
STDIO_ARGS=()
while IFS= read -r -d '' a; do
    STDIO_ARGS+=("$a")
done < <(echo "$INPUT" | python3 -c "
import sys, json
data = json.load(sys.stdin)
args = data.get('args') or []
for a in args:
    sys.stdout.write(f'{a}\0')
" 2>/dev/null)

# Build and run the mcp add command (suppress claude text output from stdout).
if [ -n "$URL" ]; then
    # URL-based transport: default to http; sse callers can override by passing
    # command="sse" via the payload today, but the current API only surfaces
    # url, so http is the only path exercised. Env is passed through for
    # symmetry — claude accepts -e on url transports without error.
    run_claude mcp add "${SCOPE_ARGS[@]}" -t http "${ENV_FLAGS[@]}" "${HEADER_FLAGS[@]}" "$NAME" "$URL" >/dev/null 2>&1
elif [ -n "$COMMAND" ]; then
    # stdio transport: name, commandOrUrl, then args verbatim after `--`.
    if [ ${#STDIO_ARGS[@]} -gt 0 ]; then
        run_claude mcp add "${SCOPE_ARGS[@]}" "${ENV_FLAGS[@]}" "$NAME" "$COMMAND" -- "${STDIO_ARGS[@]}" >/dev/null 2>&1
    else
        run_claude mcp add "${SCOPE_ARGS[@]}" "${ENV_FLAGS[@]}" "$NAME" "$COMMAND" >/dev/null 2>&1
    fi
fi

if [ $? -eq 0 ]; then
    echo "{\"success\": true, \"message\": \"Added MCP server '$NAME'\", \"name\": \"$NAME\"}"
else
    echo "{\"success\": false, \"message\": \"Failed to add MCP server '$NAME'\", \"name\": \"$NAME\"}"
    exit 1
fi
