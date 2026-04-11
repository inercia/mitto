#!/bin/bash
# Install Gemini CLI
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if ! command -v npm &> /dev/null; then
    echo '{"success": false, "message": "npm is required to install Gemini CLI. Please install Node.js first.", "version": ""}'
    exit 1
fi

npm install -g @google/gemini-cli >&2 2>&1

if command -v gemini &> /dev/null; then
    VERSION=$(gemini --version 2>/dev/null | head -1 | tr -d '\n')
    echo "{\"success\": true, \"message\": \"Gemini CLI installed successfully\", \"version\": \"$VERSION\"}"
else
    echo '{"success": false, "message": "Installation failed", "version": ""}'
    exit 1
fi
