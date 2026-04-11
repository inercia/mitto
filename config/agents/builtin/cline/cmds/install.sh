#!/bin/bash
# Install Cline CLI
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if ! command -v npm &> /dev/null; then
    echo '{"success": false, "message": "npm is required to install Cline. Please install Node.js first.", "version": ""}'
    exit 1
fi

npm install -g cline >&2 2>&1

if command -v cline &> /dev/null; then
    VERSION=$(cline --version 2>/dev/null | head -1 | tr -d '\n')
    echo "{\"success\": true, \"message\": \"Cline installed successfully\", \"version\": \"$VERSION\"}"
else
    echo '{"success": false, "message": "Installation failed", "version": ""}'
    exit 1
fi
