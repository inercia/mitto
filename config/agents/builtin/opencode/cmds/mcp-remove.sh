#!/usr/bin/env bash
# Remove an MCP server from OpenCode
# Input: {"name": "...", "scope": "...", "path": "..."}
# Output: {"success": bool, "message": "...", "name": "..."}

INPUT=$(cat)

NAME=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null)

if [ -z "$NAME" ]; then
    echo '{"success": false, "message": "name is required", "name": ""}'
    exit 1
fi

SCOPE=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('scope',''))" 2>/dev/null)
WORKSPACE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('path',''))" 2>/dev/null)

# OpenCode reads MCP servers from the `mcp` property in opencode.json.
case "$SCOPE" in
    project)
        if [ -z "$WORKSPACE_PATH" ]; then
            echo "{\"success\": false, \"message\": \"path is required for project scope\", \"name\": \"$NAME\"}"
            exit 1
        fi
        CONFIG_FILE="${WORKSPACE_PATH}/opencode.json"
        ;;
    *)
        CONFIG_FILE="${HOME}/.config/opencode/opencode.json"
        ;;
esac

printf '%s\n' "$INPUT" | CONFIG_FILE="$CONFIG_FILE" python3 -c '
import json, os, sys

try:
    data = json.load(sys.stdin)
    name = data.get("name", "")
    config_file = os.environ["CONFIG_FILE"]

    try:
        with open(config_file, encoding="utf-8") as f:
            config = json.load(f)
    except FileNotFoundError:
        print(json.dumps({"success": False, "message": "Config file not found: " + config_file, "name": name}))
        sys.exit(0)

    servers = config.get("mcp", {})
    if not isinstance(servers, dict):
        raise ValueError("OpenCode config `mcp` property must be an object")
    if name not in servers:
        print(json.dumps({"success": False, "message": "MCP server " + repr(name) + " not found", "name": name}))
        sys.exit(0)

    del servers[name]
    config["mcp"] = servers
    with open(config_file, "w", encoding="utf-8") as f:
        json.dump(config, f, indent=2)
        f.write("\n")

    print(json.dumps({"success": True, "message": "Removed MCP server " + repr(name), "name": name}))
except Exception as exc:
    print(json.dumps({"success": False, "message": "Failed to update OpenCode config: " + str(exc), "name": ""}))
    sys.exit(1)
'

if [ $? -ne 0 ]; then
    exit 1
fi
