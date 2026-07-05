#!/usr/bin/env bash
# List MCP servers configured for Claude Code
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "...", "env": {...}}]}

# Claude Code stores MCP servers under "mcpServers" in:
#   user:    ~/.claude.json (top-level mcpServers)
#   local:   ~/.claude.json (per-project entry: projects.<workspace>.mcpServers)
#   project: <workspace>/.mcp.json (top-level mcpServers, shared/checked-in)
# NOTE: ~/.claude/settings.json is NOT read for mcpServers (silently ignored by Claude Code).
# Later scopes override earlier ones by server name.

INPUT=$(cat 2>/dev/null || echo '{}')

# Extract optional workspace path from input
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

USER_CONFIG="$HOME/.claude.json"
PROJECT_CONFIG=""
if [ -n "$WORKSPACE_PATH" ]; then
    PROJECT_CONFIG="$WORKSPACE_PATH/.mcp.json"
fi

# Merge mcpServers from all scopes (paths passed via env to avoid quoting issues).
MITTO_USER_CONFIG="$USER_CONFIG" \
MITTO_PROJECT_CONFIG="$PROJECT_CONFIG" \
MITTO_WORKSPACE_PATH="$WORKSPACE_PATH" \
python3 -c "
import json, os

def load_json(path):
    if not path or not os.path.isfile(path):
        return None
    try:
        with open(path) as f:
            return json.load(f)
    except Exception:
        return None

def servers_of(data):
    if not isinstance(data, dict):
        return {}
    servers = data.get('mcpServers', {})
    return servers if isinstance(servers, dict) else {}

merged = {}

# 1) user scope: top-level mcpServers in ~/.claude.json
user_data = load_json(os.environ.get('MITTO_USER_CONFIG', ''))
for name, cfg in servers_of(user_data).items():
    if isinstance(cfg, dict):
        merged[name] = cfg

# 2) local scope: per-project mcpServers keyed by workspace path in ~/.claude.json
ws = os.environ.get('MITTO_WORKSPACE_PATH', '')
if ws and isinstance(user_data, dict):
    projects = user_data.get('projects', {})
    if isinstance(projects, dict):
        proj = projects.get(ws, {})
        for name, cfg in servers_of(proj).items():
            if isinstance(cfg, dict):
                merged[name] = cfg

# 3) project scope: <workspace>/.mcp.json
proj_data = load_json(os.environ.get('MITTO_PROJECT_CONFIG', ''))
for name, cfg in servers_of(proj_data).items():
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
    if cfg.get('headers'):
        entry['headers'] = cfg['headers']
    result.append(entry)

print(json.dumps({'servers': result}))
"
