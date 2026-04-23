#!/usr/bin/env bash
# Install an MCP server for Goose
# Input: {"name": "...", "command": "...", "args": [...], "url": "..."}
# Output: {"success": bool, "message": "...", "name": "..."}

INPUT=$(cat)
NAME=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null)
echo "{\"success\": false, \"message\": \"Goose uses YAML config. Edit ~/.config/goose/config.yaml to add extension '$NAME'\", \"name\": \"$NAME\"}"
