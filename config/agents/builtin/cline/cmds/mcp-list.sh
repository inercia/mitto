#!/bin/bash
# List MCP servers configured for Cline
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "..."}]}

INPUT=$(cat 2>/dev/null || echo '{}')
CONFIG_FILE="${HOME}/.cline/mcp_settings.json"

if [ ! -f "$CONFIG_FILE" ]; then
    echo '{"servers": []}'
    exit 0
fi

python3 -c "
import json, sys
try:
    with open('$CONFIG_FILE') as f:
        config = json.load(f)
    servers = config.get('mcpServers', {})
    result = []
    for name, cfg in servers.items():
        entry = {'name': name}
        if 'command' in cfg:
            entry['command'] = cfg['command']
        if 'args' in cfg:
            entry['args'] = cfg['args']
        if 'url' in cfg:
            entry['url'] = cfg['url']
        result.append(entry)
    print(json.dumps({'servers': result}))
except Exception:
    print(json.dumps({'servers': []}))
"
