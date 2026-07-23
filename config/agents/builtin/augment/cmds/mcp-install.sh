#!/usr/bin/env bash
# Install an MCP server for Augment
# Input: {"name": "...", "command": "...", "args": [...], "url": "...", "path": "...", "env": {...}, "headers": {...}}
# Output: {"success": bool, "message": "...", "name": "..."}

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

# Check if auggie is available
if ! command -v auggie &>/dev/null; then
    echo "{\"success\": false, \"message\": \"auggie CLI not found\", \"name\": \"$NAME\"}"
    exit 1
fi

WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)
SCOPE=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('scope',''))" 2>/dev/null)

# Build base auggie command args with optional workspace folder
AUGGIE_ARGS=()
if [ -n "$WORKSPACE_PATH" ]; then
    AUGGIE_ARGS+=("--workspace-root=$WORKSPACE_PATH")
fi

# Map scope to auggie flag
SCOPE_ARGS=()
case "$SCOPE" in
    project) SCOPE_ARGS+=("--project") ;;
    local)   SCOPE_ARGS+=("--local") ;;
    # "user" or empty = default (no flag needed)
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

# Build and run the mcp add command (suppress auggie text output from stdout)
if [ -n "$URL" ]; then
    auggie "${AUGGIE_ARGS[@]}" mcp add "${SCOPE_ARGS[@]}" "$NAME" --transport http --url "$URL" "${HEADER_FLAGS[@]}" "${ENV_FLAGS[@]}" --replace >/dev/null 2>&1
elif [ -n "$COMMAND" ]; then
    ARGS=$(echo "$INPUT" | python3 -c "import sys,json; args=json.load(sys.stdin).get('args',[]); print(' '.join(args))" 2>/dev/null)
    if [ -n "$ARGS" ]; then
        auggie "${AUGGIE_ARGS[@]}" mcp add "${SCOPE_ARGS[@]}" "$NAME" --command "$COMMAND" --args "$ARGS" "${ENV_FLAGS[@]}" --replace >/dev/null 2>&1
    else
        auggie "${AUGGIE_ARGS[@]}" mcp add "${SCOPE_ARGS[@]}" "$NAME" --command "$COMMAND" "${ENV_FLAGS[@]}" --replace >/dev/null 2>&1
    fi
fi

if [ $? -eq 0 ]; then
    echo "{\"success\": true, \"message\": \"Added MCP server '$NAME'\", \"name\": \"$NAME\"}"
else
    echo "{\"success\": false, \"message\": \"Failed to add MCP server '$NAME'\", \"name\": \"$NAME\"}"
    exit 1
fi
