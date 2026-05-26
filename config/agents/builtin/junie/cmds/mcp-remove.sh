#!/usr/bin/env bash
# Remove an MCP server from Junie
# Input: {"name": "...", "scope": "...", "path": "..."}
# Output: {"success": bool, "message": "...", "name": ""}

INPUT=$(cat)
echo '{"success": false, "message": "Junie MCP servers are configured through JetBrains IDE settings", "name": ""}'
