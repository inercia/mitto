#!/usr/bin/env bash
# List MCP servers configured for Junie
# Input: {"path": "/optional/workspace/path"} (optional, via stdin)
# Output: {"servers": [...]}

INPUT=$(cat 2>/dev/null || echo '{}')
echo '{"servers": []}'
