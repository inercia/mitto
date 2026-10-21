#!/usr/bin/env bash
# Remove an MCP server from OpenCode without discarding JSONC comments.
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
exec node "$SCRIPT_DIR/mcp-jsonc.bundle.cjs" remove
