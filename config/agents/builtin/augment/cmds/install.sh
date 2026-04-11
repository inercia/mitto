#!/bin/bash
# Install Augment Code CLI (auggie)
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if ! command -v npm &> /dev/null; then
    echo '{"success": false, "message": "npm is required to install auggie. Please install Node.js first.", "version": ""}'
    exit 1
fi

npm install -g @augmentcode/auggie >&2 2>&1

if command -v auggie &> /dev/null; then
    VERSION=$(auggie --version 2>/dev/null | head -1 | tr -d '\n')
    cat <<EOF
{"success": true, "message": "Augment Code CLI installed successfully", "version": "$VERSION"}
EOF
else
    echo '{"success": false, "message": "Installation failed", "version": ""}'
    exit 1
fi
