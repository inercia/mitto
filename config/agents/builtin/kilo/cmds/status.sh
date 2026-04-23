#!/usr/bin/env bash
# Status check for Kilo Code
# Outputs JSON with agent installation and configuration status

COMMAND="kilo"
VERSION_FLAG="--version"
MCP_CONFIG_PATH="${HOME}/.kilo/mcp.json"

# Check if installed
INSTALLED=false
CMD_PATH=""
VERSION=""

if command -v "$COMMAND" &> /dev/null; then
    INSTALLED=true
    CMD_PATH=$(command -v "$COMMAND" 2>/dev/null || echo "")
    VERSION=$("$COMMAND" $VERSION_FLAG 2>/dev/null | head -1 | tr -d '\n' || echo "")
fi

# Check MCP config
MCP_FOUND=false
if [ -f "$MCP_CONFIG_PATH" ]; then
    MCP_FOUND=true
fi

# Output JSON
cat <<EOF
{
  "installed": $INSTALLED,
  "version": "$(echo "$VERSION" | sed 's/"/\\"/g')",
  "command": "$COMMAND",
  "path": "$CMD_PATH",
  "mcp_config_found": $MCP_FOUND,
  "mcp_config_path": "$MCP_CONFIG_PATH"
}
EOF
