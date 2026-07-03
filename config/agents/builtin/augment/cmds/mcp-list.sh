#!/usr/bin/env bash
# List MCP servers configured for Augment (auggie)
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "...", "env": {...}}]}
#
# Reads auggie's settings files directly instead of `auggie mcp list --json`,
# because that command omits per-server env (and command/args). Auggie stores
# MCP servers under "mcpServers" in:
#   user:    ~/.augment/settings.json
#   project: <workspace>/.augment/settings.json
#   local:   <workspace>/.augment/settings.local.json
# Later scopes override earlier ones by server name.

INPUT=$(cat 2>/dev/null || echo '{}')

# Extract optional workspace path from input
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

USER_SETTINGS="$HOME/.augment/settings.json"
PROJECT_SETTINGS=""
LOCAL_SETTINGS=""
if [ -n "$WORKSPACE_PATH" ]; then
    PROJECT_SETTINGS="$WORKSPACE_PATH/.augment/settings.json"
    LOCAL_SETTINGS="$WORKSPACE_PATH/.augment/settings.local.json"
fi

# Merge mcpServers from all scopes (paths passed via env to avoid quoting issues).
MITTO_USER_SETTINGS="$USER_SETTINGS" \
MITTO_PROJECT_SETTINGS="$PROJECT_SETTINGS" \
MITTO_LOCAL_SETTINGS="$LOCAL_SETTINGS" \
python3 -c "
import json, os

def load(path):
    if not path or not os.path.isfile(path):
        return {}
    try:
        with open(path) as f:
            data = json.load(f)
    except Exception:
        return {}
    servers = data.get('mcpServers', {})
    return servers if isinstance(servers, dict) else {}

merged = {}
# Precedence: user < project < local (later overrides earlier).
for var in ('MITTO_USER_SETTINGS', 'MITTO_PROJECT_SETTINGS', 'MITTO_LOCAL_SETTINGS'):
    for name, cfg in load(os.environ.get(var, '')).items():
        if isinstance(cfg, dict):
            merged[name] = cfg

result = []
for name, cfg in merged.items():
    entry = {'name': name}
    if cfg.get('command'):
        entry['command'] = cfg['command']
    if cfg.get('args'):
        entry['args'] = cfg['args']
    if cfg.get('url'):
        entry['url'] = cfg['url']
    if cfg.get('env'):
        entry['env'] = cfg['env']
    result.append(entry)

print(json.dumps({'servers': result}))
"
