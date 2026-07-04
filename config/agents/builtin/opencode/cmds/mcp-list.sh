#!/usr/bin/env bash
# List MCP servers configured for Opencode
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "...", "env": {...}}]}

INPUT=$(cat 2>/dev/null || echo '{}')
CONFIG_FILE="${HOME}/.config/opencode/opencode.json"

if [ ! -f "$CONFIG_FILE" ]; then
    echo '{"servers": []}'
    exit 0
fi

python3 -c "
import json, sys
try:
    with open('$CONFIG_FILE') as f:
        config = json.load(f)
    servers = config.get('mcp', {})
    result = []
    for name, cfg in servers.items():
        entry = {'name': name}
        t = cfg.get('type')
        cmd = cfg.get('command')
        if t == 'local' or (t is None and isinstance(cmd, list)):
            if isinstance(cmd, list) and len(cmd) > 0:
                entry['command'] = cmd[0]
                if len(cmd) > 1:
                    entry['args'] = cmd[1:]
            elif isinstance(cmd, str) and cmd:
                entry['command'] = cmd
            env = cfg.get('environment')
            if env:
                entry['env'] = env
        if t == 'remote' or (t is None and 'url' in cfg):
            if 'url' in cfg:
                entry['url'] = cfg['url']
        result.append(entry)
    print(json.dumps({'servers': result}))
except Exception:
    print(json.dumps({'servers': []}))
"
