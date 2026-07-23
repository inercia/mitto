#!/usr/bin/env bash
# Install an MCP server for Kilo
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

SCOPE=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('scope',''))" 2>/dev/null)
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

# Determine config file based on scope
case "$SCOPE" in
    project)
        if [ -z "$WORKSPACE_PATH" ]; then
            echo "{\"success\": false, \"message\": \"path is required for project scope\", \"name\": \"$NAME\"}"
            exit 1
        fi
        CONFIG_DIR="${WORKSPACE_PATH}/.kilo"
        CONFIG_FILE="${WORKSPACE_PATH}/.kilo/mcp.json"
        ;;
    *)
        CONFIG_DIR="${HOME}/.kilo"
        CONFIG_FILE="${HOME}/.kilo/mcp.json"
        ;;
esac

mkdir -p "$CONFIG_DIR"
if [ ! -f "$CONFIG_FILE" ]; then
    echo '{"mcpServers":{}}' > "$CONFIG_FILE"
fi

echo "$INPUT" | python3 -c "
import json, sys
input_data = json.load(sys.stdin)
name = input_data.get('name', '')
command = input_data.get('command', '')
url = input_data.get('url', '')
args = input_data.get('args', [])
env = input_data.get('env') or {}
headers = input_data.get('headers') or {}

with open('$CONFIG_FILE') as f:
    config = json.load(f)

config.setdefault('mcpServers', {})
if url:
    entry = {'url': url}
    if headers:
        entry['headers'] = headers
    if env:
        entry['env'] = env
    config['mcpServers'][name] = entry
elif command:
    entry = {'command': command}
    if args:
        entry['args'] = args
    if env:
        entry['env'] = env
    config['mcpServers'][name] = entry

with open('$CONFIG_FILE', 'w') as f:
    json.dump(config, f, indent=2)

print(json.dumps({'success': True, 'message': 'Added MCP server ' + repr(name), 'name': name}))
"

if [ $? -ne 0 ]; then
    echo "{\"success\": false, \"message\": \"Failed to update config\", \"name\": \"$NAME\"}"
    exit 1
fi
