#!/usr/bin/env bash
# Install an MCP server for OpenCode
# Input: {"name": "...", "command": "...", "args": [...], "url": "...", "path": "...", "env": {...}, "headers": {...}, "scope": "user|project"}
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

# OpenCode reads MCP servers from the `mcp` property in opencode.json.
CONFIG_DIR=""
case "$SCOPE" in
    project)
        if [ -z "$WORKSPACE_PATH" ]; then
            echo "{\"success\": false, \"message\": \"path is required for project scope\", \"name\": \"$NAME\"}"
            exit 1
        fi
        CONFIG_FILE="${WORKSPACE_PATH}/opencode.json"
        ;;
    *)
        CONFIG_DIR="${HOME}/.config/opencode"
        CONFIG_FILE="${CONFIG_DIR}/opencode.json"
        ;;
esac

if [ -n "${CONFIG_DIR:-}" ]; then
    mkdir -p "$CONFIG_DIR"
fi

printf '%s\n' "$INPUT" | CONFIG_FILE="$CONFIG_FILE" python3 -c '
import json, os, sys

try:
    data = json.load(sys.stdin)
    name = data.get("name", "")
    command = data.get("command", "")
    url = data.get("url", "")
    args = data.get("args") or []
    env = data.get("env") or {}
    headers = data.get("headers") or {}
    config_file = os.environ["CONFIG_FILE"]

    try:
        with open(config_file, encoding="utf-8") as f:
            config = json.load(f)
    except FileNotFoundError:
        config = {}

    if not isinstance(config, dict):
        raise ValueError("OpenCode config must contain a JSON object")
    servers = config.setdefault("mcp", {})
    if not isinstance(servers, dict):
        raise ValueError("OpenCode config `mcp` property must be an object")

    if url:
        entry = {"type": "remote", "url": url}
        if headers:
            entry["headers"] = headers
    else:
        entry = {"type": "local", "command": [command, *args]}
        if env:
            entry["environment"] = env
    servers[name] = entry

    with open(config_file, "w", encoding="utf-8") as f:
        json.dump(config, f, indent=2)
        f.write("\n")

    print(json.dumps({"success": True, "message": "Added MCP server " + repr(name), "name": name}))
except Exception as exc:
    print(json.dumps({"success": False, "message": "Failed to update OpenCode config: " + str(exc), "name": ""}))
    sys.exit(1)
'

if [ $? -ne 0 ]; then
    exit 1
fi
