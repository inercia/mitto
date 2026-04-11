#!/bin/bash
# List MCP servers configured for Goose
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [...]}

INPUT=$(cat 2>/dev/null || echo '{}')
CONFIG_FILE="${HOME}/.config/goose/config.yaml"

if [ ! -f "$CONFIG_FILE" ]; then
    echo '{"servers": []}'
    exit 0
fi

python3 -c "
import json, sys
try:
    import yaml
except ImportError:
    print(json.dumps({'servers': []}))
    sys.exit(0)

try:
    with open('$CONFIG_FILE') as f:
        config = yaml.safe_load(f)
    extensions = config.get('extensions', {})
    result = []
    for name, cfg in extensions.items():
        entry = {'name': name}
        if cfg.get('type') == 'stdio':
            entry['command'] = cfg.get('cmd', '')
            entry['args'] = cfg.get('args', [])
        elif cfg.get('type') == 'sse':
            entry['url'] = cfg.get('uri', '')
        result.append(entry)
    print(json.dumps({'servers': result}))
except Exception:
    print(json.dumps({'servers': []}))
"
