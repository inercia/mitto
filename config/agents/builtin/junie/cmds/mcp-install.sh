#!/bin/bash
# Install an MCP server for Junie
# Input: {"name": "...", "command": "...", "args": [...], "url": "..."}
# Output: {"success": bool, "message": "...", "name": ""}

INPUT=$(cat)
echo '{"success": false, "message": "Junie MCP servers are configured through JetBrains IDE settings", "name": ""}'
