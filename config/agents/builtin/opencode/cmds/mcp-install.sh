#!/usr/bin/env bash
# Install an MCP server for OpenCode without discarding JSONC comments.
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
exec node "$SCRIPT_DIR/mcp-jsonc.bundle.cjs" install
