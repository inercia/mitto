#!/bin/bash
# Install Kilo Code
# Input: {} (none required)
# Output: {"success": bool, "message": "...", "version": "..."}

if ! command -v npm &> /dev/null; then
    echo '{"success": false, "message": "npm is required to install Kilo Code. Please install Node.js first.", "version": ""}'
    exit 1
fi

npm install -g @kilocode/cli >&2 2>&1
echo '{"success": true, "message": "Kilo Code installed", "version": ""}'
