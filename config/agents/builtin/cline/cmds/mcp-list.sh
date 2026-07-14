#!/usr/bin/env bash
# List MCP servers configured for Cline
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "...", "env": {...}}]}

# Cline (VSCode extension "saoudrizwan.claude-dev") stores MCP servers under
# "mcpServers" in its globalStorage settings file. Location is OS-specific:
#   macOS:   ~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json
#   Linux:   ~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json
#   Windows: %APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\cline_mcp_settings.json
# CLI/SDK variant: ~/.cline/data/settings/cline_mcp_settings.json
# Overrides honored: CLINE_MCP_SETTINGS_PATH (full file path), CLINE_DIR (base dir).
# The first existing candidate wins.

INPUT=$(cat 2>/dev/null || echo '{}')

python3 -c "
import json, os, sys

home = os.path.expanduser('~')
rel = os.path.join('saoudrizwan.claude-dev', 'settings', 'cline_mcp_settings.json')

candidates = []

# Explicit overrides first.
override = os.environ.get('CLINE_MCP_SETTINGS_PATH', '')
if override:
    candidates.append(override)
cline_dir = os.environ.get('CLINE_DIR', '')
if cline_dir:
    candidates.append(os.path.join(cline_dir, 'data', 'settings', 'cline_mcp_settings.json'))

# OS-specific VSCode globalStorage location.
if sys.platform == 'darwin':
    candidates.append(os.path.join(home, 'Library', 'Application Support', 'Code', 'User', 'globalStorage', rel))
elif sys.platform.startswith('win'):
    appdata = os.environ.get('APPDATA', os.path.join(home, 'AppData', 'Roaming'))
    candidates.append(os.path.join(appdata, 'Code', 'User', 'globalStorage', rel))
else:
    candidates.append(os.path.join(home, '.config', 'Code', 'User', 'globalStorage', rel))

# CLI/SDK variant.
candidates.append(os.path.join(home, '.cline', 'data', 'settings', 'cline_mcp_settings.json'))

def load(path):
    if not path or not os.path.isfile(path):
        return None
    try:
        with open(path) as f:
            return json.load(f)
    except Exception:
        return None

config = None
for path in candidates:
    config = load(path)
    if config is not None:
        break

servers = {}
if isinstance(config, dict):
    s = config.get('mcpServers', {})
    if isinstance(s, dict):
        servers = s

result = []
for name, cfg in servers.items():
    if not isinstance(cfg, dict):
        continue
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
