#!/usr/bin/env bash
# Remove an MCP server from Goose
# Input: {"name": "...", "scope": "...", "path": "..."}
# Output: {"success": bool, "message": "...", "name": "..."}

INPUT=$(cat)
NAME=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null)
echo "{\"success\": false, \"message\": \"Goose uses YAML config. Edit ~/.config/goose/config.yaml to remove extension '$NAME'\", \"name\": \"$NAME\"}"
