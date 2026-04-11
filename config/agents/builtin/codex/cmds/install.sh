#!/bin/bash
# Install Codex CLI
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if ! command -v npm &> /dev/null; then
    echo '{"success": false, "message": "npm is required to install Codex. Please install Node.js first.", "version": ""}'
    exit 1
fi

npm install -g @openai/codex >&2 2>&1

if command -v codex &> /dev/null; then
    VERSION=$(codex --version 2>/dev/null | head -1 | tr -d '\n')
    echo "{\"success\": true, \"message\": \"Codex CLI installed successfully\", \"version\": \"$VERSION\"}"
else
    echo '{"success": false, "message": "Installation failed", "version": ""}'
    exit 1
fi
