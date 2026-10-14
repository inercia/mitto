#!/usr/bin/env bash
# List MCP servers configured for Opencode
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [{"name": "...", "command": "...", "args": [...], "url": "...", "env": {...}}]}

INPUT=$(cat 2>/dev/null || echo '{}')
printf '%s\n' "$INPUT" | HOME="$HOME" python3 -c '
import json, os, sys

def load_jsonc(stream):
    text = stream.read()
    cleaned = []
    i = 0
    in_string = False
    escaped = False
    while i < len(text):
        ch = text[i]
        if in_string:
            cleaned.append(ch)
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == "\"":
                in_string = False
            i += 1
            continue
        if ch == "\"":
            in_string = True
            cleaned.append(ch)
            i += 1
        elif text.startswith("//", i):
            end = text.find("\n", i + 2)
            i = len(text) if end == -1 else end
        elif text.startswith("/*", i):
            end = text.find("*/", i + 2)
            if end == -1:
                raise ValueError("unterminated JSONC comment")
            i = end + 2
        else:
            cleaned.append(ch)
            i += 1

    text = "".join(cleaned)
    result = []
    i = 0
    in_string = False
    escaped = False
    while i < len(text):
        ch = text[i]
        if in_string:
            result.append(ch)
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == "\"":
                in_string = False
        elif ch == "\"":
            in_string = True
            result.append(ch)
        elif ch == ",":
            lookahead = i + 1
            while lookahead < len(text) and text[lookahead].isspace():
                lookahead += 1
            if lookahead == len(text) or text[lookahead] not in "]}":
                result.append(ch)
        else:
            result.append(ch)
        i += 1
    return json.loads("".join(result))

try:
    data = json.load(sys.stdin)
    config_files = [os.path.join(os.environ["HOME"], ".config", "opencode", "opencode.json")]
    workspace = data.get("path")
    if workspace:
        config_files.append(os.path.join(workspace, "opencode.json"))

    servers = {}
    for config_file in config_files:
        try:
            with open(config_file, encoding="utf-8") as f:
                config = load_jsonc(f)
            configured = config.get("mcp", {})
            if isinstance(configured, dict):
                servers.update(configured)
        except FileNotFoundError:
            continue

    result = []
    for name, cfg in servers.items():
        if not isinstance(cfg, dict):
            continue
        entry = {"name": name}
        kind = cfg.get("type")
        command = cfg.get("command")
        if kind == "local" or (kind is None and isinstance(command, (list, str))):
            if isinstance(command, list) and command:
                entry["command"] = command[0]
                if len(command) > 1:
                    entry["args"] = command[1:]
            elif isinstance(command, str) and command:
                entry["command"] = command
            environment = cfg.get("environment")
            if environment:
                entry["env"] = environment
        if kind == "remote" or (kind is None and "url" in cfg):
            if "url" in cfg:
                entry["url"] = cfg["url"]
            if "headers" in cfg:
                entry["headers"] = cfg["headers"]
        result.append(entry)
    print(json.dumps({"servers": result}))
except Exception:
    print(json.dumps({"servers": []}))
'
