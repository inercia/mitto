#!/bin/bash
# Install Claude Code CLI
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if ! command -v npm &> /dev/null; then
    echo '{"success": false, "message": "npm is required to install Claude Code. Please install Node.js first.", "version": ""}'
    exit 1
fi

npm install -g @anthropic-ai/claude-code >&2 2>&1

if command -v claude &> /dev/null; then
    VERSION=$(claude --version 2>/dev/null | head -1 | tr -d '\n')
    cat <<EOF
{"success": true, "message": "Claude Code installed successfully", "version": "$VERSION"}
EOF
else
    echo '{"success": false, "message": "Installation failed", "version": ""}'
    exit 1
fi
