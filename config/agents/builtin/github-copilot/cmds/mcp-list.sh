#!/usr/bin/env bash
# List MCP servers configured for Github Copilot
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "...", "env": {...}}]}

INPUT=$(cat 2>/dev/null || echo '{}')
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

if [ -n "$WORKSPACE_PATH" ]; then
    if [ ! -d "$WORKSPACE_PATH" ]; then
        echo "workspace path does not exist: $WORKSPACE_PATH" >&2
        exit 1
    fi
    cd "$WORKSPACE_PATH" || exit 1
fi

# Copilot owns the effective merge across user, workspace, plugin, managed, and
# built-in sources. Query it instead of duplicating its precedence rules here.
copilot mcp list --json | python3 -c '
import json, sys

data = json.load(sys.stdin)
servers = data.get("mcpServers", {})
if not isinstance(servers, dict):
    raise ValueError("copilot mcp list returned a non-object mcpServers field")

result = []
for name, cfg in servers.items():
    if not isinstance(cfg, dict):
        continue
    entry = {"name": name}
    for key in ("command", "args", "url", "env", "headers"):
        if key in cfg:
            entry[key] = cfg[key]
    result.append(entry)

print(json.dumps({"servers": result}))
'
