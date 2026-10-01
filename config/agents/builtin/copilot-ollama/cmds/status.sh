#!/usr/bin/env bash
# Status check for Copilot CLI backed by a local Ollama server.
# "installed" requires BOTH the copilot and ollama executables; whether the
# Ollama server is currently reachable is reported separately.

COMMAND="copilot"
VERSION_FLAG="--version"
MCP_CONFIG_PATH="${HOME}/.copilot/mcp-config.json"
OLLAMA_URL="http://localhost:11434"

INSTALLED=false
CMD_PATH=""
VERSION=""
OLLAMA_INSTALLED=false
OLLAMA_RUNNING=false

if command -v ollama &> /dev/null; then
    OLLAMA_INSTALLED=true
    if curl -sf -m 3 "$OLLAMA_URL/api/version" &> /dev/null; then
        OLLAMA_RUNNING=true
    fi
fi

if command -v "$COMMAND" &> /dev/null && [ "$OLLAMA_INSTALLED" = true ]; then
    INSTALLED=true
    CMD_PATH=$(command -v "$COMMAND" 2>/dev/null || echo "")
    VERSION=$("$COMMAND" $VERSION_FLAG 2>/dev/null | head -1 | tr -d '\n' || echo "")
fi

MCP_FOUND=false
if [ -f "$MCP_CONFIG_PATH" ]; then
    MCP_FOUND=true
fi

cat <<EOF
{
  "installed": $INSTALLED,
  "version": "$(echo "$VERSION" | sed 's/"/\\"/g')",
  "command": "$COMMAND",
  "path": "$CMD_PATH",
  "ollama_installed": $OLLAMA_INSTALLED,
  "ollama_running": $OLLAMA_RUNNING,
  "mcp_config_found": $MCP_FOUND,
  "mcp_config_path": "$MCP_CONFIG_PATH"
}
EOF
