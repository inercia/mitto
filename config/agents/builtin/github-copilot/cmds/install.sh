#!/bin/bash
# Install GitHub Copilot CLI
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if ! command -v npm &> /dev/null; then
    echo '{"success": false, "message": "npm is required to install GitHub Copilot CLI. Please install Node.js first.", "version": ""}'
    exit 1
fi

npm install -g @github/copilot >&2 2>&1

if command -v github-copilot &> /dev/null; then
    VERSION=$(github-copilot --version 2>/dev/null | head -1 | tr -d '\n')
    echo "{\"success\": true, \"message\": \"GitHub Copilot CLI installed successfully\", \"version\": \"$VERSION\"}"
else
    echo '{"success": false, "message": "Installation completed", "version": ""}'
fi
