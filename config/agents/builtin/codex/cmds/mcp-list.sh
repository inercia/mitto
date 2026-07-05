#!/usr/bin/env bash
# List MCP servers configured for Codex
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "...", "env": {...}}]}

# Codex stores MCP servers in TOML under [mcp_servers.<name>] tables in:
#   user:    ~/.codex/config.toml
#   project: <workspace>/.codex/config.toml (trusted projects)
# Each table has: command, args = [...], url, and an [mcp_servers.<name>.env] subtable.
# Later scopes override earlier ones by server name.
# TOML is parsed via tomllib/tomli when available, else a minimal embedded parser
# (the machine's python3 may lack tomllib, so we must not depend on py3.11+).

INPUT=$(cat 2>/dev/null || echo '{}')

# Extract optional workspace path from input
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

USER_CONFIG="$HOME/.codex/config.toml"
PROJECT_CONFIG=""
if [ -n "$WORKSPACE_PATH" ]; then
    PROJECT_CONFIG="$WORKSPACE_PATH/.codex/config.toml"
fi

MITTO_USER_CONFIG="$USER_CONFIG" \
MITTO_PROJECT_CONFIG="$PROJECT_CONFIG" \
python3 -c "
import json, os, re

def parse_toml(text):
    # Prefer a real TOML parser when present.
    try:
        import tomllib  # py3.11+
        return tomllib.loads(text)
    except Exception:
        pass
    try:
        import tomli  # backport
        return tomli.loads(text)
    except Exception:
        pass
    return _mini_toml(text)

def _strip_comment(s):
    # Remove an unquoted trailing '#' comment.
    out = []
    in_str = False
    quote = ''
    i = 0
    while i < len(s):
        c = s[i]
        if in_str:
            out.append(c)
            if c == quote:
                in_str = False
        else:
            if c in ('\"', \"'\"):
                in_str = True
                quote = c
                out.append(c)
            elif c == '#':
                break
            else:
                out.append(c)
        i += 1
    return ''.join(out)

def _parse_value(v):
    v = v.strip()
    if not v:
        return ''
    if v[0] == '[' and v[-1] == ']':
        inner = v[1:-1].strip()
        if not inner:
            return []
        # Split top-level commas (values here are simple strings/numbers).
        items, buf, in_str, quote = [], [], False, ''
        for c in inner:
            if in_str:
                buf.append(c)
                if c == quote:
                    in_str = False
            elif c in ('\"', \"'\"):
                in_str = True
                quote = c
                buf.append(c)
            elif c == ',':
                items.append(''.join(buf).strip())
                buf = []
            else:
                buf.append(c)
        if buf:
            items.append(''.join(buf).strip())
        return [_parse_scalar(x) for x in items if x != '']
    return _parse_scalar(v)

def _parse_scalar(v):
    v = v.strip()
    if len(v) >= 2 and v[0] == v[-1] and v[0] in ('\"', \"'\"):
        return v[1:-1]
    if v == 'true':
        return True
    if v == 'false':
        return False
    try:
        if re.fullmatch(r'-?[0-9]+', v):
            return int(v)
        return float(v)
    except Exception:
        return v

def _mini_toml(text):
    root = {}
    cur = root
    for raw in text.splitlines():
        line = _strip_comment(raw).strip()
        if not line:
            continue
        if line.startswith('[') and line.endswith(']'):
            path = line[1:-1].strip()
            # Split on unquoted dots.
            parts, buf, in_str, quote = [], [], False, ''
            for c in path:
                if in_str:
                    buf.append(c)
                    if c == quote:
                        in_str = False
                elif c in ('\"', \"'\"):
                    in_str = True
                    quote = c
                elif c == '.':
                    parts.append(''.join(buf).strip())
                    buf = []
                else:
                    buf.append(c)
            if buf:
                parts.append(''.join(buf).strip())
            cur = root
            for p in parts:
                cur = cur.setdefault(p, {})
            continue
        if '=' in line:
            k, _, v = line.partition('=')
            cur[k.strip()] = _parse_value(v)
    return root

def servers_of(data):
    if not isinstance(data, dict):
        return {}
    s = data.get('mcp_servers', {})
    return s if isinstance(s, dict) else {}

def load(path):
    if not path or not os.path.isfile(path):
        return {}
    try:
        with open(path) as f:
            return parse_toml(f.read())
    except Exception:
        return {}

merged = {}
for var in ('MITTO_USER_CONFIG', 'MITTO_PROJECT_CONFIG'):
    for name, cfg in servers_of(load(os.environ.get(var, ''))).items():
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
