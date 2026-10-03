#!/usr/bin/env bash
# Status check for Oh My Pi
# Outputs JSON with agent installation status

COMMAND="omp"
VERSION_FLAG="--version"

INSTALLED=false
CMD_PATH=""
VERSION=""

if command -v "$COMMAND" &> /dev/null; then
    INSTALLED=true
    CMD_PATH=$(command -v "$COMMAND" 2>/dev/null || echo "")
    VERSION=$("$COMMAND" "$VERSION_FLAG" 2>/dev/null | head -1 | tr -d '\n' || echo "")
fi

cat <<EOF
{
  "installed": $INSTALLED,
  "version": "$(echo "$VERSION" | sed 's/"/\\"/g')",
  "command": "$COMMAND",
  "path": "$CMD_PATH",
  "mcp_config_found": false,
  "mcp_config_path": ""
}
EOF
