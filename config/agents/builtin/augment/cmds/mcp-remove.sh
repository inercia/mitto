#!/usr/bin/env bash
# Remove an MCP server from Augment
# Input: {"name": "...", "scope": "...", "path": "..."}
# Output: {"success": bool, "message": "...", "name": "..."}

INPUT=$(cat)

NAME=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null)

if [ -z "$NAME" ]; then
    echo '{"success": false, "message": "name is required", "name": ""}'
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

# Run the mcp remove command
auggie "${AUGGIE_ARGS[@]}" mcp remove "$NAME" 2>&1

if [ $? -eq 0 ]; then
    echo "{\"success\": true, \"message\": \"Removed MCP server '$NAME'\", \"name\": \"$NAME\"}"
else
    echo "{\"success\": false, \"message\": \"Failed to remove MCP server '$NAME'\", \"name\": \"$NAME\"}"
    exit 1
fi
