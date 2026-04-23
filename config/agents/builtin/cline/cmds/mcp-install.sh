#!/usr/bin/env bash
# Install an MCP server for Cline
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

CONFIG_DIR="${HOME}/.cline"
CONFIG_FILE="${HOME}/.cline/mcp_settings.json"

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

with open('$CONFIG_FILE') as f:
    config = json.load(f)

config.setdefault('mcpServers', {})
if url:
    config['mcpServers'][name] = {'url': url}
elif command:
    entry = {'command': command}
    if args:
        entry['args'] = args
    config['mcpServers'][name] = entry

with open('$CONFIG_FILE', 'w') as f:
    json.dump(config, f, indent=2)

print(json.dumps({'success': True, 'message': 'Added MCP server ' + repr(name), 'name': name}))
"

if [ $? -ne 0 ]; then
    echo "{\"success\": false, \"message\": \"Failed to update config\", \"name\": \"$NAME\"}"
    exit 1
fi
