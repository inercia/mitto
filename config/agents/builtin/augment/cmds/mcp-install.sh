#!/usr/bin/env bash
# Install an MCP server for Augment
# Input: {"name": "...", "command": "...", "args": [...], "url": "...", "path": "..."}
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

# Build base auggie command args with optional workspace folder
AUGGIE_ARGS=()
if [ -n "$WORKSPACE_PATH" ]; then
    AUGGIE_ARGS+=("--workspace-root=$WORKSPACE_PATH")
fi

# Build and run the mcp add command
if [ -n "$URL" ]; then
    auggie "${AUGGIE_ARGS[@]}" mcp add "$NAME" --transport http --url "$URL" --replace 2>&1
elif [ -n "$COMMAND" ]; then
    ARGS=$(echo "$INPUT" | python3 -c "import sys,json; args=json.load(sys.stdin).get('args',[]); print(' '.join(args))" 2>/dev/null)
    if [ -n "$ARGS" ]; then
        auggie "${AUGGIE_ARGS[@]}" mcp add "$NAME" --command "$COMMAND" --args "$ARGS" --replace 2>&1
    else
        auggie "${AUGGIE_ARGS[@]}" mcp add "$NAME" --command "$COMMAND" --replace 2>&1
    fi
fi

if [ $? -eq 0 ]; then
    echo "{\"success\": true, \"message\": \"Added MCP server '$NAME'\", \"name\": \"$NAME\"}"
else
    echo "{\"success\": false, \"message\": \"Failed to add MCP server '$NAME'\", \"name\": \"$NAME\"}"
    exit 1
fi
