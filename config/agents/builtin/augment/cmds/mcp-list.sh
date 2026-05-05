#!/usr/bin/env bash
# List MCP servers configured for Augment
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "..."}]}

INPUT=$(cat 2>/dev/null || echo '{}')

# Check if auggie is available
if ! command -v auggie &>/dev/null; then
    echo '{"servers": []}'
    exit 0
fi

# Extract optional workspace path from input
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

# Build auggie command
if [ -n "$WORKSPACE_PATH" ]; then
    AUGGIE_OUTPUT=$(auggie --workspace-root="$WORKSPACE_PATH" mcp list --json 2>/dev/null) || true
else
    AUGGIE_OUTPUT=$(auggie mcp list --json 2>/dev/null) || true
fi

if [ -z "$AUGGIE_OUTPUT" ]; then
    echo '{"servers": []}'
    exit 0
fi

# Transform auggie output to expected format (keep only name, command, args, url)
echo "$AUGGIE_OUTPUT" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    result = []
    for s in data.get('servers', []):
        entry = {'name': s['name']}
        if 'command' in s:
            entry['command'] = s['command']
        if 'args' in s:
            entry['args'] = s['args']
        if 'url' in s:
            entry['url'] = s['url']
        result.append(entry)
    print(json.dumps({'servers': result}))
except Exception:
    print(json.dumps({'servers': []}))
"
