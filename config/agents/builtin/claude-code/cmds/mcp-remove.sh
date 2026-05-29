#!/usr/bin/env bash
# Remove an MCP server from Claude Code
# Input: {"name": "...", "scope": "...", "path": "..."}
# Output: {"success": bool, "message": "...", "name": "..."}

INPUT=$(cat)

NAME=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null)

if [ -z "$NAME" ]; then
    echo '{"success": false, "message": "name is required", "name": ""}'
    exit 1
fi

SCOPE=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('scope',''))" 2>/dev/null)
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

# Determine config file based on scope
case "$SCOPE" in
    project)
        if [ -z "$WORKSPACE_PATH" ]; then
            echo "{\"success\": false, \"message\": \"path is required for project scope\", \"name\": \"$NAME\"}"
            exit 1
        fi
        CONFIG_FILE="${WORKSPACE_PATH}/.claude/settings.json"
        ;;
    local)
        if [ -z "$WORKSPACE_PATH" ]; then
            echo "{\"success\": false, \"message\": \"path is required for local scope\", \"name\": \"$NAME\"}"
            exit 1
        fi
        CONFIG_FILE="${WORKSPACE_PATH}/.claude/settings.local.json"
        ;;
    *)
        CONFIG_FILE="${HOME}/.claude/settings.json"
        ;;
esac

if [ ! -f "$CONFIG_FILE" ]; then
    echo "{\"success\": false, \"message\": \"Config file not found: $CONFIG_FILE\", \"name\": \"$NAME\"}"
    exit 1
fi

echo "$INPUT" | python3 -c "
import json, sys
input_data = json.load(sys.stdin)
name = input_data.get('name', '')

with open('$CONFIG_FILE') as f:
    config = json.load(f)

servers = config.get('mcpServers', {})
if name not in servers:
    print(json.dumps({'success': False, 'message': 'MCP server ' + repr(name) + ' not found', 'name': name}))
    sys.exit(0)

del servers[name]
config['mcpServers'] = servers

with open('$CONFIG_FILE', 'w') as f:
    json.dump(config, f, indent=2)

print(json.dumps({'success': True, 'message': 'Removed MCP server ' + repr(name), 'name': name}))
"

if [ $? -ne 0 ]; then
    echo "{\"success\": false, \"message\": \"Failed to update config\", \"name\": \"$NAME\"}"
    exit 1
fi
